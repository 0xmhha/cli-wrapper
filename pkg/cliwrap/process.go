package cliwrap

import (
	"context"
	"sync"

	"github.com/cli-wrapper/cli-wrapper/internal/controller"
)

// ProcessHandle is the user-facing handle to a managed process.
type ProcessHandle interface {
	ID() string
	Spec() Spec
	Status() Status
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Close(ctx context.Context) error
}

type processHandle struct {
	spec Spec
	mgr  *Manager

	mu         sync.Mutex
	ctrl       *controller.Controller
	lastStatus Status
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
