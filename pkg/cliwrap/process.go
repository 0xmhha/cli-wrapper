// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/controller"
	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

// PTYData carries one chunk of PTY output delivered to a SubscribePTYData caller.
type PTYData = controller.PTYData

// ProcessHandle is the user-facing handle to a managed process.
type ProcessHandle interface {
	ID() string
	Spec() Spec
	Status() Status
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Close(ctx context.Context) error

	// WriteInput forwards b as PTY stdin to the running child.
	// Returns ErrPTYNotConfigured if Spec.PTY is nil.
	WriteInput(ctx context.Context, b []byte) error

	// Resize sends a TIOCSWINSZ to the running PTY child.
	// Returns ErrPTYNotConfigured if Spec.PTY is nil.
	Resize(ctx context.Context, cols, rows uint16) error

	// Signal delivers sig to the foreground process group of the PTY child.
	// Returns ErrPTYNotConfigured if Spec.PTY is nil.
	Signal(ctx context.Context, sig syscall.Signal) error

	// SubscribePTYData registers a receiver for PTY output frames.
	// If Spec.PTY is nil the returned channel is immediately closed.
	// The caller MUST invoke the returned unsubscribe function exactly once.
	SubscribePTYData() (<-chan PTYData, func())

	// OnStateChange registers cb to be called every time the underlying
	// controller transitions to a new state (StateStarting →
	// StateRunning → StateStopping/Stopped/Crashed/...). The callback
	// fires only on actual transitions; same-value re-stores are
	// filtered out. Pass nil to disable. Replacing an existing callback
	// is supported and atomic with respect to in-flight transitions.
	//
	// The callback may be registered before or after Start; in the
	// pre-Start case it is buffered until Start wires it onto the
	// controller, so even the StateStarting → StateRunning transition
	// is observable.
	//
	// The callback runs on whichever goroutine triggered the transition
	// (typically the IPC dispatch loop or the Start/Stop call itself),
	// so it MUST NOT block. Hosts that need to do heavy work should
	// hand off to a separate goroutine.
	//
	// The Status passed to the callback reflects the freshly-updated
	// state at call time; ChildPID is the value at that instant.
	OnStateChange(cb func(Status))
}

type processHandle struct {
	spec Spec
	mgr  *Manager

	mu         sync.Mutex
	ctrl       *controller.Controller
	lastStatus Status

	// onStateChangeCb is the host-supplied callback registered via
	// OnStateChange. When ctrl is nil (pre-Start) it is held here and
	// wired onto the freshly-constructed controller in Start. Protected
	// by mu (covers both fields together so wireOnStateChange runs
	// atomically with respect to ctrl assignment).
	onStateChangeCb func(Status)

	nextSeq atomic.Uint64
}

func (p *processHandle) ID() string { return p.spec.ID }

func (p *processHandle) Spec() Spec { return p.spec }

func (p *processHandle) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctrl == nil {
		if p.lastStatus.State != 0 {
			return p.lastStatus
		}
		return Status{State: StatePending}
	}
	return Status{
		State:    p.ctrl.State(),
		ChildPID: p.ctrl.ChildPID(),
	}
}

func (p *processHandle) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctrl != nil {
		return ErrProcessAlreadyRunning
	}
	ctrl, err := p.mgr.newController(p.spec)
	if err != nil {
		return err
	}
	// Wire any callback that was registered before Start so the
	// StateStarting → StateRunning transition is observable. The
	// controller's setState is safe to call concurrently with this;
	// SetOnStateChange takes its own lock.
	if p.onStateChangeCb != nil {
		hostCb := p.onStateChangeCb
		ctrl.SetOnStateChange(func(s cwtypes.State) {
			hostCb(Status{State: s, ChildPID: ctrl.ChildPID()})
		})
	}
	if err := ctrl.Start(ctx); err != nil {
		return err
	}
	// CW-G4: refuse Persistent=true against an agent that didn't advertise
	// the "persistence" capability. Tear down the controller cleanly so the
	// agent exits before returning the error.
	if p.spec.Persistent && !ctrl.AgentSupportsPersistence() {
		_ = ctrl.Close(ctx)
		return ErrPersistenceUnsupportedByAgent
	}
	p.ctrl = ctrl
	return nil
}

func (p *processHandle) OnStateChange(cb func(Status)) {
	p.mu.Lock()
	p.onStateChangeCb = cb
	ctrl := p.ctrl
	p.mu.Unlock()

	// If the controller is already running, wire the callback through
	// directly so transitions that happen after this call fire it. If
	// not, Start will wire it when ctrl is constructed.
	if ctrl != nil {
		if cb == nil {
			ctrl.SetOnStateChange(nil)
			return
		}
		ctrl.SetOnStateChange(func(s cwtypes.State) {
			cb(Status{State: s, ChildPID: ctrl.ChildPID()})
		})
	}
}

func (p *processHandle) Stop(ctx context.Context) error {
	p.mu.Lock()
	ctrl := p.ctrl
	persistent := p.spec.Persistent
	stopTimeout := p.spec.StopTimeout
	p.mu.Unlock()
	if ctrl == nil {
		return ErrProcessNotRunning
	}
	if err := ctrl.Stop(ctx); err != nil {
		return err
	}
	// CW-G4: persistent sessions have NO SIGTERM safety net (Close uses
	// CloseConnOnly, no AgentHandle.Close). If MsgStopChild is dropped by
	// the conn close race (writeLoop canceled before flushing outbox),
	// the agent never learns to stop. Synchronously wait for the agent
	// to acknowledge child exit via the state transition triggered by
	// MsgChildExited reception. Bounded by StopTimeout + 1s slack.
	if persistent {
		if stopTimeout <= 0 {
			stopTimeout = 5 * time.Second
		}
		deadline := time.Now().Add(stopTimeout + time.Second)
		for time.Now().Before(deadline) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s := ctrl.State()
			if s == StateStopped || s == StateCrashed || s == StateFailed {
				return nil
			}
			time.Sleep(20 * time.Millisecond)
		}
		return errors.New("cliwrap: persistent Stop timed out waiting for child exit")
	}
	return nil
}

func (p *processHandle) Close(ctx context.Context) error {
	p.mu.Lock()
	ctrl := p.ctrl
	persistent := p.spec.Persistent
	p.ctrl = nil
	p.mu.Unlock()
	if ctrl == nil {
		return nil
	}

	// CW-G4: on persistent sessions, Close releases this host's connection
	// only; the agent stays alive in detached mode for the next reattach.
	// Use h.Stop(ctx) to fully terminate the persistent session.
	var err error
	if persistent {
		err = ctrl.CloseConnOnly(ctx)
	} else {
		err = ctrl.Close(ctx)
	}

	// Snapshot the controller's terminal state AFTER Close so subsequent
	// p.Status() calls see the post-shutdown state instead of whatever
	// transient state was active mid-Close (usually StateRunning). Pre-fix
	// hosts that asserted Status() == Exited immediately after Close
	// observed Running and broke (concrete example: ai-m's
	// TestCliwrap_CreateLookupTerminate).
	p.mu.Lock()
	p.lastStatus = Status{
		State:    ctrl.State(),
		ChildPID: ctrl.ChildPID(),
	}
	p.mu.Unlock()

	return err
}

// WriteInput forwards b as PTY stdin input to the running child.
func (p *processHandle) WriteInput(ctx context.Context, b []byte) error {
	if p.spec.PTY == nil {
		return ErrPTYNotConfigured
	}
	p.mu.Lock()
	ctrl := p.ctrl
	p.mu.Unlock()
	if ctrl == nil {
		return ErrProcessNotRunning
	}
	seq := p.nextSeq.Add(1)
	payload, err := ipc.EncodePTYWrite(ipc.PTYWrite{Seq: seq, Bytes: b})
	if err != nil {
		return err
	}
	return ctrl.SendPTY(ctx, ipc.MsgTypePTYWrite, payload)
}

// Resize sends a TIOCSWINSZ to the running PTY child.
func (p *processHandle) Resize(ctx context.Context, cols, rows uint16) error {
	if p.spec.PTY == nil {
		return ErrPTYNotConfigured
	}
	p.mu.Lock()
	ctrl := p.ctrl
	p.mu.Unlock()
	if ctrl == nil {
		return ErrProcessNotRunning
	}
	payload, err := ipc.EncodePTYResize(ipc.PTYResize{Cols: cols, Rows: rows})
	if err != nil {
		return err
	}
	return ctrl.SendPTY(ctx, ipc.MsgTypePTYResize, payload)
}

// Signal delivers sig to the foreground process group of the PTY child.
func (p *processHandle) Signal(ctx context.Context, sig syscall.Signal) error {
	if p.spec.PTY == nil {
		return ErrPTYNotConfigured
	}
	p.mu.Lock()
	ctrl := p.ctrl
	p.mu.Unlock()
	if ctrl == nil {
		return ErrProcessNotRunning
	}
	payload, err := ipc.EncodePTYSignal(ipc.PTYSignal{Signum: int32(sig)})
	if err != nil {
		return err
	}
	return ctrl.SendPTY(ctx, ipc.MsgTypePTYSignal, payload)
}

// SubscribePTYData registers a receiver for PTY output frames.
// If Spec.PTY is nil the returned channel is immediately closed.
func (p *processHandle) SubscribePTYData() (ch <-chan PTYData, unsubscribe func()) {
	closedEmptyChan := func() <-chan PTYData {
		c := make(chan PTYData)
		close(c)
		return c
	}
	if p.spec.PTY == nil {
		return closedEmptyChan(), func() {}
	}
	p.mu.Lock()
	ctrl := p.ctrl
	p.mu.Unlock()
	if ctrl == nil {
		return closedEmptyChan(), func() {}
	}
	return ctrl.SubscribePTYData()
}
