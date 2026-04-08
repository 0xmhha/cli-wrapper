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

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/internal/ipc"
	"github.com/0xmhha/cli-wrapper/internal/supervise"
)

// ControllerOptions configures a Controller.
type ControllerOptions struct {
	Spec       cwtypes.Spec
	Spawner    *supervise.Spawner
	RuntimeDir string
}

// Controller manages exactly one agent subprocess and its child.
type Controller struct {
	opts   ControllerOptions
	state  atomic.Int32
	handle *supervise.AgentHandle
	conn   *ipc.Conn

	mu       sync.Mutex
	childPID int
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

	conn.OnMessage(c.handleMessage)
	conn.Start()

	// Send HELLO to prompt agent's HELLO_ACK.
	_ = c.send(ipc.MsgHello, ipc.HelloPayload{ProtocolVersion: uint8(ipc.ProtocolVersion), AgentID: c.opts.Spec.ID}, false)
	_ = c.send(ipc.MsgStartChild, ipc.StartChildPayload{
		Command:     c.opts.Spec.Command,
		Args:        c.opts.Spec.Args,
		Env:         c.opts.Spec.Env,
		WorkDir:     c.opts.Spec.WorkDir,
		StopTimeout: c.opts.Spec.StopTimeout.Milliseconds(),
	}, true)
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
	}
}
