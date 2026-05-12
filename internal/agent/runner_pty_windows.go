// SPDX-License-Identifier: Apache-2.0

//go:build windows

package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// ErrPTYUnsupportedOnWindows is returned by every PTY operation when the
// agent is built for Windows. The Windows ConPTY backend is roadmap item
// CW-P2-5 and is not yet implemented; until then `Spec.PTY != nil` causes
// child spawn to fail at the agent level (which surfaces to the host as
// ErrPTYUnsupportedByAgent via the existing capability negotiation).
var ErrPTYUnsupportedOnWindows = errors.New("agent: PTY mode not implemented on Windows (CW-P2-5)")

// ptyProc has the same shape as the Unix implementation so runner.go can
// reference it without per-platform branching at the call site. Every
// method returns ErrPTYUnsupportedOnWindows.
type ptyProc struct {
	closeCh chan struct{}
	onData  func([]byte)
	cmd     *exec.Cmd // unused on Windows; kept for runner.go cross-platform access
}

func spawnPTY(ctx context.Context, spec RunSpec) (*ptyProc, error) {
	return nil, ErrPTYUnsupportedOnWindows
}

func setWinsize(f *os.File, cols, rows uint16) error { return ErrPTYUnsupportedOnWindows }
func (p *ptyProc) Resize(cols, rows uint16) error    { return ErrPTYUnsupportedOnWindows }
func (p *ptyProc) Winsize() (uint16, uint16, error)  { return 0, 0, ErrPTYUnsupportedOnWindows }
func (p *ptyProc) OnData(cb func([]byte))            { p.onData = cb }
func (p *ptyProc) Done() <-chan struct{}             { return p.closeCh }
func (p *ptyProc) Close() error                      { return ErrPTYUnsupportedOnWindows }
func (p *ptyProc) WriteInput(b []byte) error         { return ErrPTYUnsupportedOnWindows }
func (p *ptyProc) Signal(sig syscall.Signal) error   { return ErrPTYUnsupportedOnWindows }
func (p *ptyProc) startReadPump()                    {}
