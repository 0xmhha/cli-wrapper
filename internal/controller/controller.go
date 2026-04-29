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
	features    []string     // populated by negotiateCapabilities
	capReplyCh  chan []string // one-shot channel; closed after negotiation
	crashSource atomic.Int32 // CrashSource; only meaningful when state==StateCrashed

	ptySubs ptySubscribers // fan-out for inbound MsgTypePTYData frames

	closed   atomic.Bool
	closeErr error
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
	c.state.Store(int32(cwtypes.StatePending))
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
	c.state.Store(int32(cwtypes.StateStarting))

	handle, err := c.opts.Spawner.Spawn(ctx, c.opts.Spec.ID)
	if err != nil {
		c.state.Store(int32(cwtypes.StateCrashed))
		return fmt.Errorf("controller start: %w", err)
	}
	c.handle = handle

	conn, err := ipc.NewConn(ipc.ConnConfig{
		RWC:        handle.Socket,
		SpillerDir: filepath.Join(c.opts.RuntimeDir, "outbox-"+c.opts.Spec.ID),
		Capacity:   1024,
		WALBytes:   256 * 1024 * 1024,
	})
	if err != nil {
		_ = handle.Close()
		c.state.Store(int32(cwtypes.StateCrashed))
		return fmt.Errorf("controller conn: %w", err)
	}
	c.conn = conn

	// Allocate the one-shot channel before installing the handler so the
	// handler never races with a nil channel.
	c.capReplyCh = make(chan []string, 1)

	conn.OnMessage(c.handleMessage)
	conn.SetOnDisconnect(func(_ error) {
		c.crashSource.Store(int32(CrashSourceConnectionLost))
		c.state.Store(int32(cwtypes.StateCrashed))
	})
	conn.Start()

	// Send HELLO to prompt agent's HELLO_ACK.
	_ = c.send(ipc.MsgHello, ipc.HelloPayload{ProtocolVersion: ipc.ProtocolVersion, AgentID: c.opts.Spec.ID}, false)

	// Negotiate capabilities before spawning the child. An older agent that
	// does not understand MsgTypeCapabilityQuery will simply not reply; we
	// treat a timeout as "no features" for backward compatibility.
	if err := c.negotiateCapabilities(ctx); err != nil {
		_ = handle.Close()
		c.state.Store(int32(cwtypes.StateCrashed))
		return fmt.Errorf("controller capability negotiation: %w", err)
	}

	// Refuse PTY mode if the agent does not advertise the "pty" feature.
	if c.opts.Spec.PTY != nil && !c.AgentSupportsPTY() {
		_ = handle.Close()
		c.state.Store(int32(cwtypes.StateCrashed))
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

// Stop sends STOP_CHILD.
func (c *Controller) Stop(ctx context.Context) error {
	cur := cwtypes.State(c.state.Load())
	if cur == cwtypes.StateStopped || cur == cwtypes.StateFailed {
		return nil
	}
	c.state.Store(int32(cwtypes.StateStopping))
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
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range c.features {
		if f == "pty" {
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
	h := ipc.Header{MsgType: t, SeqNo: c.conn.Seqs().Next(), Length: uint32(len(data))}
	if ack {
		h.Flags |= ipc.FlagAckRequired
	}
	c.conn.Send(ipc.OutboxMessage{Header: h, Payload: data})
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
			c.state.Store(int32(cwtypes.StateRunning))
		}
	case ipc.MsgChildExited:
		var p ipc.ChildExitedPayload
		if err := ipc.DecodePayload(msg.Payload, &p); err == nil {
			if cwtypes.State(c.state.Load()) == cwtypes.StateStopping || p.ExitCode == 0 {
				c.state.Store(int32(cwtypes.StateStopped))
			} else {
				c.state.Store(int32(cwtypes.StateCrashed))
			}
		}
	case ipc.MsgChildError:
		c.state.Store(int32(cwtypes.StateCrashed))
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
	}
}
