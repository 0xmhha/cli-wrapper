// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"context"
	"errors"
	"fmt"
	"io"
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

	// SpillerDir holds the WAL for the outgoing direction.
	SpillerDir string

	// Capacity is the in-memory outbox capacity.
	Capacity int

	// WALBytes is the WAL size cap.
	WALBytes int64

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
	sp, err := NewSpiller(cfg.SpillerDir, cfg.Capacity, cfg.WALBytes)
	if err != nil {
		return nil, fmt.Errorf("ipc: spiller: %w", err)
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

// Seqs exposes the outbound sequence-number generator so higher layers can
// attach seqs to frames they build.
func (c *Conn) Seqs() *SeqGenerator { return c.seqs }

// Start launches the reader and writer goroutines. It is safe to call more
// than once; subsequent calls are no-ops.
func (c *Conn) Start() {
	c.startOnce.Do(func() {
		c.wg.Add(2)
		go c.readLoop()
		go c.writeLoop()
	})
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
		c.closed.Store(true)
		c.cancelFunc()
		_ = c.rwc.Close()
		c.sp.Outbox().Close()

		// Always wait unconditionally for reader/writer goroutines to exit.
		// The writer selects on cancelCtx.Done() so it exits immediately
		// after cancel, and the reader returns on rwc close.
		c.wg.Wait()

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
	defer c.wg.Done()
	for {
		h, body, err := c.fr.ReadFrame()
		if err != nil {
			// EOF or closed network pipe is the normal shutdown signal.
			return
		}
		// Per spec §2.7, receivers must be idempotent on SeqNo regardless
		// of the FlagIsReplay bit. Dedup every inbound frame so higher
		// layers see each seq at most once.
		if c.dedup.Seen(h.SeqNo) {
			continue
		}
		if hp := c.handler.Load(); hp != nil && *hp != nil {
			(*hp)(OutboxMessage{Header: h, Payload: body})
		}
	}
}

func (c *Conn) writeLoop() {
	defer c.wg.Done()
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
