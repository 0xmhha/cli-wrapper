// SPDX-License-Identifier: Apache-2.0

// Package controller manages the lifecycle of a single cliwrap-agent
// subprocess and its managed child. Each Controller owns one agent
// connection, tracks state transitions, and translates inbound IPC
// messages into observable state changes.
package controller

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/internal/ipc"
	"github.com/0xmhha/cli-wrapper/internal/supervise"
)

// ControllerOptions configures a Controller.
type ControllerOptions struct {
	Spec       cwtypes.Spec
	Spawner    *supervise.Spawner
	RuntimeDir string

	// PersistentDir is the parent directory for per-session metadata
	// (CW-G4). Used only when Spec.Persistent=true. Caller (Manager)
	// passes Manager.persistentDir.
	PersistentDir string

	// OutboxCapacity overrides the in-memory IPC outbox slot count. Zero
	// uses the legacy default (1024). Larger values reduce the chance of
	// overflow under bursty traffic at the cost of memory.
	OutboxCapacity int

	// DisableWAL skips the disk-backed write-ahead log on the outgoing
	// IPC direction. When true, outbox overflow drops messages rather
	// than fsync-spilling to disk; the caller accepts the rare-drop
	// trade-off in exchange for predictable latency under bursty input.
	// Default (false) preserves the legacy "messages are never lost"
	// guarantee.
	DisableWAL bool

	// OnLogChunk is invoked for every inbound ipc.MsgLogChunk frame.
	// The callback runs on the ipc.Conn dispatch goroutine, so it must
	// not block for long. Callers that need to do heavy work should
	// hand the data off to a separate goroutine. May be nil, in which
	// case log chunks are dropped.
	OnLogChunk func(stream uint8, data []byte)
}

// Controller manages exactly one agent subprocess and its child.
type Controller struct {
	opts   ControllerOptions
	state  atomic.Int32
	handle *supervise.AgentHandle
	conn   *ipc.Conn

	mu          sync.Mutex
	childPID    int
	features    []string      // populated by negotiateCapabilities
	capReplyCh  chan []string // one-shot channel; closed after negotiation
	crashSource atomic.Int32  // CrashSource; only meaningful when state==StateCrashed

	ptySubs ptySubscribers // fan-out for inbound MsgTypePTYData frames

	// onStateChangeMu guards onStateChange. The callback is invoked from
	// setState whenever the controller's state actually transitions to a
	// new value. Hosts use this to react to child exit / crash without
	// polling. The callback runs on whichever goroutine called setState
	// (typically the IPC dispatch loop), so callbacks MUST NOT block.
	onStateChangeMu sync.Mutex
	onStateChange   func(cwtypes.State)

	// CW-G4: when reattaching to a persistent session, the agent emits
	// MsgPTYRingDump with the ring buffer snapshot. Stored here until the
	// next SubscribePTYData subscriber drains it. Subsequent subscribers
	// see only live data — the dump is delivered exactly once.
	initialPTYBuffer     []byte
	initialBufferDrained atomic.Bool

	closed   atomic.Bool
	closeErr error
}

// SetOnStateChange registers cb to be called every time the controller's
// state transitions to a new value. The callback is also invoked when
// setState is called with the same value as the current state? No —
// duplicate transitions are filtered: cb fires only when old != new.
// Pass nil to disable. Replacing an existing callback is supported and
// atomic with respect to setState.
//
// The callback runs on the goroutine that triggered the transition —
// usually the IPC dispatch loop or a host-initiated Start/Stop call —
// so it MUST NOT block. Hosts that need to do heavy work should hand
// off to a separate goroutine.
func (c *Controller) SetOnStateChange(cb func(cwtypes.State)) {
	c.onStateChangeMu.Lock()
	c.onStateChange = cb
	c.onStateChangeMu.Unlock()
}

// setState swaps the controller's state and fires the registered
// OnStateChange callback when (and only when) the value actually
// changed. All in-package state transitions go through this helper so
// callbacks never miss a transition and never fire on no-ops.
func (c *Controller) setState(s cwtypes.State) {
	old := c.state.Swap(int32(s))
	if old == int32(s) {
		return
	}
	c.onStateChangeMu.Lock()
	cb := c.onStateChange
	c.onStateChangeMu.Unlock()
	if cb != nil {
		cb(s)
	}
}

// NewController constructs a Controller.
func NewController(opts ControllerOptions) (*Controller, error) {
	if opts.Spawner == nil {
		return nil, errors.New("controller: Spawner is required")
	}
	if opts.RuntimeDir == "" {
		return nil, errors.New("controller: RuntimeDir is required")
	}
	c := &Controller{opts: opts}
	c.setState(cwtypes.StatePending)
	return c, nil
}

// State returns the current state.
func (c *Controller) State() cwtypes.State {
	return cwtypes.State(c.state.Load())
}

// CrashSource returns the source that triggered the last crash transition.
// The value is only meaningful when State() == StateCrashed.
func (c *Controller) CrashSource() CrashSource {
	return CrashSource(c.crashSource.Load())
}

// Options returns the options the controller was constructed with.
// Exposed for test inspection; production code should not rely on this.
func (c *Controller) Options() ControllerOptions {
	return c.opts
}

// ChildPID returns the PID of the running child (0 if none).
func (c *Controller) ChildPID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.childPID
}

// Start spawns the agent and issues START_CHILD.
func (c *Controller) Start(ctx context.Context) error {
	c.setState(cwtypes.StateStarting)

	// CW-G4: pass persistence intent through Spawn options. SessionDir
	// is the per-session metadata dir under WithPersistentDir.
	spawnOpts := supervise.SpawnOpts{Persistent: c.opts.Spec.Persistent}
	if c.opts.Spec.Persistent {
		spawnOpts.SessionDir = filepath.Join(c.opts.PersistentDir, c.opts.Spec.ID)
	}
	handle, err := c.opts.Spawner.Spawn(ctx, c.opts.Spec.ID, spawnOpts)
	if err != nil {
		c.setState(cwtypes.StateCrashed)
		return fmt.Errorf("controller start: %w", err)
	}
	c.handle = handle

	capacity := c.opts.OutboxCapacity
	if capacity <= 0 {
		capacity = 1024
	}
	conn, err := ipc.NewConn(ipc.ConnConfig{
		RWC:        handle.Socket,
		SpillerDir: filepath.Join(c.opts.RuntimeDir, "outbox-"+c.opts.Spec.ID),
		Capacity:   capacity,
		WALBytes:   256 * 1024 * 1024,
		DisableWAL: c.opts.DisableWAL,
	})
	if err != nil {
		_ = handle.Close()
		c.setState(cwtypes.StateCrashed)
		return fmt.Errorf("controller conn: %w", err)
	}
	c.conn = conn

	// Allocate the one-shot channel before installing the handler so the
	// handler never races with a nil channel.
	c.capReplyCh = make(chan []string, 1)

	conn.OnMessage(c.handleMessage)
	conn.SetOnDisconnect(func(_ error) {
		c.crashSource.Store(int32(CrashSourceConnectionLost))
		c.setState(cwtypes.StateCrashed)
	})
	conn.Start()

	// Send HELLO to prompt agent's HELLO_ACK.
	_ = c.send(ipc.MsgHello, ipc.HelloPayload{ProtocolVersion: ipc.ProtocolVersion, AgentID: c.opts.Spec.ID}, false)

	// Negotiate capabilities before spawning the child. An older agent that
	// does not understand MsgTypeCapabilityQuery will simply not reply; we
	// treat a timeout as "no features" for backward compatibility.
	if err := c.negotiateCapabilities(ctx); err != nil {
		_ = handle.Close()
		c.setState(cwtypes.StateCrashed)
		return fmt.Errorf("controller capability negotiation: %w", err)
	}

	// Refuse PTY mode if the agent does not advertise the "pty" feature.
	if c.opts.Spec.PTY != nil && !c.AgentSupportsPTY() {
		_ = handle.Close()
		c.setState(cwtypes.StateCrashed)
		return cwtypes.ErrPTYUnsupportedByAgent
	}

	payload := ipc.StartChildPayload{
		Command:     c.opts.Spec.Command,
		Args:        c.opts.Spec.Args,
		Env:         c.opts.Spec.Env,
		WorkDir:     c.opts.Spec.WorkDir,
		StopTimeout: c.opts.Spec.StopTimeout.Milliseconds(),
	}
	if c.opts.Spec.PTY != nil {
		payload.PTY = &ipc.StartChildPTY{
			InitialCols: c.opts.Spec.PTY.InitialCols,
			InitialRows: c.opts.Spec.PTY.InitialRows,
			Echo:        c.opts.Spec.PTY.Echo,
		}
	}
	_ = c.send(ipc.MsgStartChild, payload, true)
	return nil
}

// Stop sends MsgStopChild.
//
// When the connected agent advertises the `frame_ack` capability, Stop
// blocks (until ctx) on the agent's MsgAckData reply, which the agent
// emits AFTER `dispatcher.stopChild` has cancelled the runner and
// awaited its wg. The caller is therefore guaranteed that by the time
// Stop returns nil, the child is dead and the runner has fully cleaned
// up. Older agents that lack the capability fall back to fire-and-
// forget (current historical behaviour) — Stop returns immediately
// once the frame is enqueued.
func (c *Controller) Stop(ctx context.Context) error {
	cur := cwtypes.State(c.state.Load())
	if cur == cwtypes.StateStopped || cur == cwtypes.StateFailed {
		return nil
	}
	c.setState(cwtypes.StateStopping)
	payload, err := ipc.EncodePayload(ipc.StopChildPayload{TimeoutMs: c.opts.Spec.StopTimeout.Milliseconds()})
	if err != nil {
		return err
	}
	if c.AgentSupportsFrameAck() {
		return c.conn.SendAndAwaitAck(ctx, ipc.MsgStopChild, 0, payload)
	}
	// Pre-frame_ack agents: fire-and-forget. The caller may follow up
	// with handle.Close, which carries the SIGTERM + Drain path.
	return c.send(ipc.MsgStopChild, ipc.StopChildPayload{TimeoutMs: c.opts.Spec.StopTimeout.Milliseconds()}, true)
}

// Close tears everything down. Idempotent.
func (c *Controller) Close(ctx context.Context) error {
	if c.closed.Swap(true) {
		return c.closeErr
	}
	if c.conn != nil {
		c.closeErr = c.conn.Close(ctx)
	}
	if c.handle != nil {
		_ = c.handle.Close()
	}
	return c.closeErr
}

// CloseConnOnly closes the IPC connection without sending SIGTERM to
// the agent. Used by ProcessHandle.Close on persistent sessions
// (CW-G4) — the agent should remain alive in detached mode, ready
// for next reattach.
//
// For reattach-derived controllers, c.handle is nil already, so
// Close and CloseConnOnly are equivalent. The asymmetry only matters
// for the original spawning host's controller, where c.handle holds
// the AgentHandle whose Close would SIGTERM the agent.
func (c *Controller) CloseConnOnly(ctx context.Context) error {
	if c.closed.Swap(true) {
		return c.closeErr
	}
	if c.conn != nil {
		c.closeErr = c.conn.Close(ctx)
	}
	// Intentionally NOT calling c.handle.Close().
	return c.closeErr
}

// negotiateCapabilities sends MsgTypeCapabilityQuery and waits up to 5 s for
// MsgTypeCapabilityReply. A timeout is treated as "no features" (backward
// compatibility with agents that pre-date capability negotiation).
func (c *Controller) negotiateCapabilities(ctx context.Context) error {
	if err := c.send(ipc.MsgTypeCapabilityQuery, nil, false); err != nil {
		return err
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	select {
	case features, ok := <-c.capReplyCh:
		if ok {
			c.mu.Lock()
			c.features = features
			c.mu.Unlock()
		}
		return nil
	case <-deadline.C:
		// Older agent: no capability reply — treat as no features.
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// AgentSupportsPTY reports whether the connected agent advertised the "pty"
// feature during capability negotiation.
func (c *Controller) AgentSupportsPTY() bool {
	return c.agentHasFeature("pty")
}

// AgentSupportsPersistence reports whether the connected agent advertised
// the "persistence" feature during capability negotiation. Used by
// processHandle.Start to refuse Persistent=true against pre-CW-G4 agents.
func (c *Controller) AgentSupportsPersistence() bool {
	return c.agentHasFeature("persistence")
}

// AgentSupportsFrameAck reports whether the connected agent advertised
// the "frame_ack" feature: it emits MsgAckData in response to any
// inbound frame whose header has FlagAckRequired set. When set,
// Controller.Stop uses Conn.SendAndAwaitAck to turn MsgStopChild into
// a synchronous request/response. When unset, Stop falls back to the
// historical fire-and-forget path. v0.4.4+.
func (c *Controller) AgentSupportsFrameAck() bool {
	return c.agentHasFeature("frame_ack")
}

// agentHasFeature is the shared lookup for capability feature checks.
func (c *Controller) agentHasFeature(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range c.features {
		if f == name {
			return true
		}
	}
	return false
}

func (c *Controller) send(t ipc.MsgType, payload any, ack bool) error {
	var data []byte
	if payload != nil {
		b, err := ipc.EncodePayload(payload)
		if err != nil {
			return err
		}
		data = b
	}
	var flags ipc.Flags
	if ack {
		flags |= ipc.FlagAckRequired
	}
	c.conn.SendWithNewSeq(t, flags, data)
	return nil
}

func (c *Controller) handleMessage(msg ipc.OutboxMessage) {
	switch msg.Header.MsgType {
	case ipc.MsgTypeCapabilityReply:
		// Deliver features to negotiateCapabilities via the one-shot channel.
		// Non-blocking send: if the channel is already closed or full (double
		// reply), we silently ignore the extra frame.
		reply, err := ipc.DecodeCapabilityReply(msg.Payload)
		if err != nil {
			return
		}
		select {
		case c.capReplyCh <- reply.Features:
		default:
		}
	case ipc.MsgHelloAck:
		// Handshake complete; nothing to record for now.
	case ipc.MsgChildStarted:
		var p ipc.ChildStartedPayload
		if err := ipc.DecodePayload(msg.Payload, &p); err == nil {
			c.mu.Lock()
			c.childPID = int(p.PID)
			c.mu.Unlock()
			c.setState(cwtypes.StateRunning)
		}
	case ipc.MsgChildExited:
		var p ipc.ChildExitedPayload
		if err := ipc.DecodePayload(msg.Payload, &p); err == nil {
			if cwtypes.State(c.state.Load()) == cwtypes.StateStopping || p.ExitCode == 0 {
				c.setState(cwtypes.StateStopped)
			} else {
				c.setState(cwtypes.StateCrashed)
			}
		}
	case ipc.MsgChildError:
		// Gate the StateCrashed transition behind the Transient flag.
		// Transient errors (decode/pty_write/pty_resize/pty_signal/start —
		// the child is still alive, the agent just couldn't service one
		// RPC) leave state unchanged so hosts on OnStateChange don't see
		// a spurious Running → Crashed → Running sawtooth. Pre-0.4.3
		// agents omit the field; its zero value (false) preserves the
		// historical "every ChildError → Crashed" behaviour for that
		// upgrade path.
		var p ipc.ChildErrorPayload
		_ = ipc.DecodePayload(msg.Payload, &p)
		if !p.Transient {
			c.setState(cwtypes.StateCrashed)
		}
	case ipc.MsgLogChunk:
		if c.opts.OnLogChunk == nil {
			return
		}
		var p ipc.LogChunkPayload
		if err := ipc.DecodePayload(msg.Payload, &p); err != nil {
			return
		}
		c.opts.OnLogChunk(p.Stream, p.Data)
	case ipc.MsgTypePTYData:
		d, err := ipc.DecodePTYData(msg.Payload)
		if err != nil {
			return
		}
		c.ptySubs.fanOut(PTYData{Seq: d.Seq, Bytes: d.Bytes})
	case ipc.MsgTypePTYRingDump:
		// CW-G4: one-shot ring buffer dump from a persistent agent on
		// reattach. Store as initial buffer; the next SubscribePTYData
		// subscriber drains it before live data.
		var p ipc.PTYRingDumpPayload
		if err := ipc.DecodePayload(msg.Payload, &p); err != nil {
			return
		}
		c.SetInitialPTYBuffer(p.Bytes)
	}
}

// SetInitialPTYBuffer stores the ring buffer snapshot received via
// MsgPTYRingDump on reattach. The buffer is delivered as the first
// chunk(s) to the next SubscribePTYData subscriber; subsequent
// subscribers see only live data.
//
// CW-G4. Idempotent (last-write-wins) but typically called once per
// reattach.
func (c *Controller) SetInitialPTYBuffer(b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.initialPTYBuffer = make([]byte, len(b))
	copy(c.initialPTYBuffer, b)
	// Reset drained flag so the new buffer is delivered to the next subscriber.
	c.initialBufferDrained.Store(false)
}
