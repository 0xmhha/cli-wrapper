// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// MessageHandler is invoked for every frame that Conn receives.
// It MUST NOT block; handlers should forward work to a buffered channel.
type MessageHandler func(OutboxMessage)

// ConnConfig parameterises NewConn.
type ConnConfig struct {
	// RWC is the underlying bidirectional stream (net.Conn, net.Pipe, etc.).
	RWC io.ReadWriteCloser

	// SpillerDir holds the WAL for the outgoing direction. Ignored when
	// DisableWAL is true.
	SpillerDir string

	// Capacity is the in-memory outbox capacity.
	Capacity int

	// WALBytes is the WAL size cap. Ignored when DisableWAL is true.
	WALBytes int64

	// DisableWAL skips disk-backed write-ahead logging entirely. When true,
	// outbox overflow drops messages instead of spilling to disk; the
	// SpillerDir/WALBytes fields are ignored. Use for interactive PTY
	// callers that prefer drops over the per-keystroke fsync cost incurred
	// when the in-memory outbox fills under load — drops in that case are
	// tolerable (the user simply retypes), and avoiding fsync removes the
	// observed cumulative-latency mode that scales with disk throughput.
	// Default (false) preserves the legacy "messages are never lost"
	// guarantee.
	DisableWAL bool

	// MaxRecvPayload caps inbound frame size. If zero, MaxPayloadSize is used.
	MaxRecvPayload uint32
}

// Conn is a full-duplex, framed, ACK-aware peer. It owns:
//   - a Spiller (outbox + WAL) for outgoing frames
//   - a writer goroutine that drains the outbox to the wire
//   - a reader goroutine that pushes inbound frames to the handler
type Conn struct {
	rwc         io.ReadWriteCloser
	sp          *Spiller
	fw          *FrameWriter
	fr          *FrameReader
	handler     atomic.Pointer[MessageHandler]
	seqs        *SeqGenerator
	dedup       *DedupTracker
	closed      atomic.Bool
	wg          sync.WaitGroup
	cancelCtx   context.Context
	cancelFunc  context.CancelFunc
	startOnce   sync.Once
	closeOnce   sync.Once
	closeResult error

	// disconnect callback fields
	disconnectMu sync.Mutex
	onDisconnect func(error)
	fireOnce     sync.Once

	// sendMu serializes SendWithNewSeq across goroutines so the (Next() →
	// Outbox.Enqueue()) pair is atomic. Without it, two concurrent senders
	// could interleave such that the higher seq enqueues first; the
	// receiver's watermark dedup would then drop the lower-seq frame as a
	// "duplicate", silently losing it. See CW-G5.
	sendMu sync.Mutex

	// pendingAcks tracks frames sent via SendAndAwaitAck that are waiting
	// for the remote to emit MsgAckData / MsgAckControl with the matching
	// AckedSeq. Read path intercepts those types and resolves the channel.
	pendingMu   sync.Mutex
	pendingAcks map[uint64]chan struct{}
}

// NewConn constructs a Conn. Call Start() to launch the reader/writer
// goroutines.
func NewConn(cfg ConnConfig) (*Conn, error) {
	if cfg.RWC == nil {
		return nil, errors.New("ipc: ConnConfig.RWC is required")
	}
	if cfg.Capacity <= 0 {
		cfg.Capacity = 1024
	}
	if cfg.WALBytes <= 0 {
		cfg.WALBytes = 256 * 1024 * 1024
	}
	if cfg.MaxRecvPayload == 0 {
		cfg.MaxRecvPayload = MaxPayloadSize
	}
	var sp *Spiller
	if cfg.DisableWAL {
		sp = NewInMemorySpiller(cfg.Capacity)
	} else {
		var err error
		sp, err = NewSpiller(cfg.SpillerDir, cfg.Capacity, cfg.WALBytes)
		if err != nil {
			return nil, fmt.Errorf("ipc: spiller: %w", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{
		rwc:        cfg.RWC,
		sp:         sp,
		fw:         NewFrameWriter(cfg.RWC),
		fr:         NewFrameReader(cfg.RWC, cfg.MaxRecvPayload),
		seqs:       NewSeqGenerator(1),
		dedup:      NewDedupTracker(),
		cancelCtx:  ctx,
		cancelFunc: cancel,
	}
	return c, nil
}

// OnMessage sets the handler invoked for each incoming frame. Subsequent
// calls replace the previous handler atomically.
func (c *Conn) OnMessage(h MessageHandler) {
	c.handler.Store(&h)
}

// SetOnDisconnect registers a callback invoked exactly once when the read loop
// exits due to EOF or an unrecoverable read error. The callback is NOT invoked
// when the shutdown is locally initiated via Close(). Subsequent calls replace
// the registered callback; replacing after Start() is best-effort.
func (c *Conn) SetOnDisconnect(cb func(error)) {
	c.disconnectMu.Lock()
	c.onDisconnect = cb
	c.disconnectMu.Unlock()
}

// fireDisconnect calls the registered onDisconnect callback at most once.
// It is suppressed when c.closed is already true (locally initiated shutdown).
//
// After invoking the user callback, cancelFunc is called so writeLoop —
// blocked on Outbox.Dequeue with a 100 ms timeout — exits within one tick
// instead of polling forever against a dead socket. Without this, hosts
// that observe disconnect only via SetOnDisconnect (and never call
// Conn.Close themselves) leaked one writeLoop goroutine per dead agent,
// surfacing as goleak failures in test binaries that mix integration
// tests with goleak-guarded unit tests.
func (c *Conn) fireDisconnect(cause error) {
	if c.closed.Load() {
		return // locally initiated shutdown — do not fire
	}
	c.fireOnce.Do(func() {
		c.disconnectMu.Lock()
		cb := c.onDisconnect
		c.disconnectMu.Unlock()
		if cb != nil {
			cb(cause)
		}
		c.cancelFunc()
	})
}

// Seqs exposes the outbound sequence-number generator so higher layers can
// attach seqs to frames they build.
func (c *Conn) Seqs() *SeqGenerator { return c.seqs }

// Done returns a channel that is closed when the conn's reader/writer
// goroutines have exited (after Close or remote EOF). Used by callers
// who need to wait for the conn to finish without holding a reference
// to its WaitGroup. CW-G4: agent's accept handler waits on this so it
// knows when to mark the session detached.
func (c *Conn) Done() <-chan struct{} { return c.cancelCtx.Done() }

// Start launches the reader and writer goroutines. It is safe to call more
// than once; subsequent calls are no-ops.
func (c *Conn) Start() {
	c.startOnce.Do(func() {
		c.wg.Add(2)
		go c.readLoop()
		go c.writeLoop()
	})
}

// SendWithNewSeq assigns the next outbound sequence number and enqueues
// the frame atomically. Use this in preference to Seqs().Next() + Send()
// from any code path that may race with another sender on the same Conn:
// without serialization the higher-seq frame can enqueue first, and the
// receiver's watermark-based DedupTracker silently drops the lower-seq
// frame as a "duplicate". CW-G5.
func (c *Conn) SendWithNewSeq(msgType MsgType, flags Flags, payload []byte) bool {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.closed.Load() {
		return false
	}
	h := Header{
		MsgType: msgType,
		Flags:   flags,
		SeqNo:   c.seqs.Next(),
		Length:  uint32(len(payload)),
	}
	return c.sp.Outbox().Enqueue(OutboxMessage{Header: h, Payload: payload})
}

// SendAndAwaitAck enqueues a frame with FlagAckRequired set and blocks
// until the remote emits MsgAckData (or MsgAckControl in the
// agent→host direction) carrying the original frame's SeqNo as
// AckedSeq, or until ctx is cancelled, or until the conn is closed.
//
// Use this only when the remote advertises the `frame_ack` capability.
// Hosts MUST fall back to SendWithNewSeq for older agents — see
// Controller.Stop for the gating pattern. There is no built-in
// timeout: ctx is the sole bound.
//
// Returns nil on ack received. Returns ctx.Err() on cancellation,
// errors.New("ipc: conn closed") on conn shutdown, or
// errors.New("ipc: outbox enqueue failed") if the outbox refused the
// frame.
func (c *Conn) SendAndAwaitAck(ctx context.Context, msgType MsgType, flags Flags, payload []byte) error {
	c.sendMu.Lock()
	if c.closed.Load() {
		c.sendMu.Unlock()
		return errors.New("ipc: conn closed")
	}
	seq := c.seqs.Next()
	ch := make(chan struct{})
	c.pendingMu.Lock()
	if c.pendingAcks == nil {
		c.pendingAcks = make(map[uint64]chan struct{})
	}
	c.pendingAcks[seq] = ch
	c.pendingMu.Unlock()
	h := Header{
		MsgType: msgType,
		Flags:   flags | FlagAckRequired,
		SeqNo:   seq,
		Length:  uint32(len(payload)),
	}
	enqueued := c.sp.Outbox().Enqueue(OutboxMessage{Header: h, Payload: payload})
	c.sendMu.Unlock()

	cleanup := func() {
		c.pendingMu.Lock()
		delete(c.pendingAcks, seq)
		c.pendingMu.Unlock()
	}

	if !enqueued {
		cleanup()
		return errors.New("ipc: outbox enqueue failed")
	}

	select {
	case <-ch:
		// deliverAck already removed the entry.
		return nil
	case <-ctx.Done():
		cleanup()
		return ctx.Err()
	case <-c.cancelCtx.Done():
		cleanup()
		return errors.New("ipc: conn closed")
	}
}

// deliverAck routes an inbound ack frame to the pending SendAndAwaitAck
// waiter, if any. Unknown SeqNos are dropped — the sender may have
// already given up via ctx cancellation.
func (c *Conn) deliverAck(seq uint64) {
	c.pendingMu.Lock()
	ch, ok := c.pendingAcks[seq]
	if ok {
		delete(c.pendingAcks, seq)
	}
	c.pendingMu.Unlock()
	if ok {
		close(ch)
	}
}

// Send enqueues msg for delivery. Returns false if the conn is closed.
func (c *Conn) Send(msg OutboxMessage) bool {
	if c.closed.Load() {
		return false
	}
	return c.sp.Outbox().Enqueue(msg)
}

// Close gracefully stops reader/writer goroutines and releases resources.
// It is idempotent. The ctx is used only to bound the spiller close; the
// goroutine join (wg.Wait) is always unconditional so that goleak never
// observes lingering goroutines.
func (c *Conn) Close(ctx context.Context) error {
	c.closeOnce.Do(func() {
		dbg := os.Getenv("CLIWRAP_DEBUG") == "1"
		if dbg {
			fmt.Fprintf(os.Stderr, "IPC: Close enter\n")
		}
		c.closed.Store(true)
		c.cancelFunc()
		if dbg {
			fmt.Fprintf(os.Stderr, "IPC: cancelFunc done\n")
		}
		_ = c.rwc.Close()
		if dbg {
			fmt.Fprintf(os.Stderr, "IPC: rwc.Close done\n")
		}
		c.sp.Outbox().Close()
		if dbg {
			fmt.Fprintf(os.Stderr, "IPC: outbox.Close done; calling wg.Wait\n")
		}

		// Always wait unconditionally for reader/writer goroutines to exit.
		// The writer selects on cancelCtx.Done() so it exits immediately
		// after cancel, and the reader returns on rwc close.
		c.wg.Wait()
		if dbg {
			fmt.Fprintf(os.Stderr, "IPC: wg.Wait done\n")
		}

		// Use ctx only to bound the spiller (WAL) close.
		spillDone := make(chan error, 1)
		go func() {
			spillDone <- c.sp.Close()
		}()
		select {
		case err := <-spillDone:
			if err != nil {
				c.closeResult = err
			}
		case <-ctx.Done():
			c.closeResult = ctx.Err()
		}
	})
	return c.closeResult
}

func (c *Conn) readLoop() {
	dbg := os.Getenv("CLIWRAP_DEBUG") == "1"
	defer func() {
		if dbg {
			fmt.Fprintf(os.Stderr, "IPC: readLoop exiting (wg.Done)\n")
		}
		c.wg.Done()
	}()
	var exitErr error
	defer func() {
		// Fire the disconnect callback unless shutdown was locally initiated.
		// errors.Is(io.EOF) covers clean EOF; nil would mean loop exited
		// normally without an error (shouldn't happen, but guard it).
		switch {
		case exitErr == nil, errors.Is(exitErr, io.EOF):
			c.fireDisconnect(ErrAgentDisconnectedEOF)
		default:
			c.fireDisconnect(fmt.Errorf("%w: %w", ErrAgentDisconnectedUnexpected, exitErr))
		}
	}()
	for {
		h, body, err := c.fr.ReadFrame()
		if err != nil {
			if dbg {
				fmt.Fprintf(os.Stderr, "IPC: readLoop ReadFrame err: %v\n", err)
			}
			// EOF or closed network pipe is the normal shutdown signal.
			exitErr = err
			return
		}
		// Per spec §2.7, receivers must be idempotent on SeqNo regardless
		// of the FlagIsReplay bit. Dedup every inbound frame so higher
		// layers see each seq at most once.
		if c.dedup.Seen(h.SeqNo) {
			continue
		}
		// Intercept wire-level ack frames before invoking the user
		// handler. SendAndAwaitAck callers are waiting on these; the
		// controller / dispatcher higher-level handlers have no reason
		// to see them.
		if h.MsgType == MsgAckData || h.MsgType == MsgAckControl {
			var p AckPayload
			if err := DecodePayload(body, &p); err == nil {
				c.deliverAck(p.AckedSeq)
			}
			continue
		}
		if hp := c.handler.Load(); hp != nil && *hp != nil {
			(*hp)(OutboxMessage{Header: h, Payload: body})
		}
	}
}

func (c *Conn) writeLoop() {
	dbg := os.Getenv("CLIWRAP_DEBUG") == "1"
	defer func() {
		if dbg {
			fmt.Fprintf(os.Stderr, "IPC: writeLoop exiting (wg.Done)\n")
		}
		c.wg.Done()
	}()
	for {
		// Wait for either a message or a cancel signal. By selecting on
		// cancelCtx.Done() directly (instead of polling closed + Dequeue),
		// the writer exits immediately when Close cancels the context,
		// preventing goleak flakes.
		select {
		case <-c.cancelCtx.Done():
			return
		default:
		}
		msg, ok := c.sp.Outbox().Dequeue(100 * time.Millisecond)
		if !ok {
			// Check cancel again after a failed dequeue.
			select {
			case <-c.cancelCtx.Done():
				return
			default:
			}
			if c.closed.Load() {
				return
			}
			continue
		}
		if _, err := c.fw.WriteFrame(msg.Header, msg.Payload); err != nil {
			// On write failure, re-queue via the WAL for eventual replay.
			_ = c.sp.spill(msg)
			return
		}
	}
}
