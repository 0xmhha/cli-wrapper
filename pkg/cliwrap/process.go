// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"context"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/0xmhha/cli-wrapper/internal/controller"
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
}

type processHandle struct {
	spec Spec
	mgr  *Manager

	mu         sync.Mutex
	ctrl       *controller.Controller
	lastStatus Status

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
	p.ctrl = ctrl
	return ctrl.Start(ctx)
}

func (p *processHandle) Stop(ctx context.Context) error {
	p.mu.Lock()
	ctrl := p.ctrl
	p.mu.Unlock()
	if ctrl == nil {
		return ErrProcessNotRunning
	}
	return ctrl.Stop(ctx)
}

func (p *processHandle) Close(ctx context.Context) error {
	p.mu.Lock()
	ctrl := p.ctrl
	if ctrl != nil {
		p.lastStatus = Status{
			State:    ctrl.State(),
			ChildPID: ctrl.ChildPID(),
		}
	}
	p.ctrl = nil
	p.mu.Unlock()
	if ctrl == nil {
		return nil
	}
	return ctrl.Close(ctx)
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
func (p *processHandle) SubscribePTYData() (<-chan PTYData, func()) {
	if p.spec.PTY == nil {
		ch := make(chan PTYData)
		close(ch)
		return ch, func() {}
	}
	p.mu.Lock()
	ctrl := p.ctrl
	p.mu.Unlock()
	if ctrl == nil {
		ch := make(chan PTYData)
		close(ch)
		return ch, func() {}
	}
	return ctrl.SubscribePTYData()
}
