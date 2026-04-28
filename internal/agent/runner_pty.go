// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

// ptyProc wraps a child process started with a pseudo-terminal.
type ptyProc struct {
	cmd     *exec.Cmd
	ptmx    *os.File
	pgid    int
	onData  func([]byte)
	closeCh chan struct{}
	once    sync.Once
}

// spawnPTY starts spec.Command inside a PTY and returns a ptyProc handle.
// The caller must invoke p.startReadPump() to begin streaming output, and
// must call p.Close() when done.
func spawnPTY(ctx context.Context, spec RunSpec) (*ptyProc, error) {
	if err := cwtypes.ApplyPTYDefaults(spec.PTY); err != nil {
		return nil, err
	}

	ptmx, tty, err := pty.Open()
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
	if spec.WorkDir != "" {
		cmd.Dir = spec.WorkDir
	}
	cmd.Env = flattenEnv(spec.Env)
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty
	// Ctty is the fd number in the *child* process. Because tty is assigned
	// to cmd.Stdin (fd 0), the controlling terminal fd in the child is 0.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}

	if err := setWinsize(ptmx, spec.PTY.InitialCols, spec.PTY.InitialRows); err != nil {
		_ = ptmx.Close()
		_ = tty.Close()
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		_ = ptmx.Close()
		_ = tty.Close()
		return nil, err
	}
	// tty is no longer needed in the parent after Start.
	_ = tty.Close()

	pgid, _ := syscall.Getpgid(cmd.Process.Pid)
	p := &ptyProc{
		cmd:     cmd,
		ptmx:    ptmx,
		pgid:    pgid,
		closeCh: make(chan struct{}),
	}
	return p, nil
}

// setWinsize applies cols/rows to the PTY master file descriptor.
func setWinsize(f *os.File, cols, rows uint16) error {
	return pty.Setsize(f, &pty.Winsize{
		Cols: cols,
		Rows: rows,
	})
}

// Resize sets the PTY window size to cols × rows via TIOCSWINSZ.
func (p *ptyProc) Resize(cols, rows uint16) error {
	return setWinsize(p.ptmx, cols, rows)
}

// Winsize returns the current PTY window dimensions as (cols, rows).
func (p *ptyProc) Winsize() (uint16, uint16, error) {
	sz, err := pty.GetsizeFull(p.ptmx)
	if err != nil {
		return 0, 0, err
	}
	return sz.Cols, sz.Rows, nil
}

// OnData registers a callback that is invoked with each chunk of output from
// the PTY. It must be called before startReadPump.
func (p *ptyProc) OnData(cb func([]byte)) { p.onData = cb }

// Done returns a channel that is closed when the read pump exits (i.e. after
// the child's PTY master reaches EOF/EIO).
func (p *ptyProc) Done() <-chan struct{} { return p.closeCh }

// Close closes the PTY master, which terminates the read pump, and signals
// Done. It is idempotent.
func (p *ptyProc) Close() error {
	p.once.Do(func() { close(p.closeCh) })
	return p.ptmx.Close()
}

// WriteInput writes b to the PTY master, delivering it as stdin to the child.
func (p *ptyProc) WriteInput(b []byte) error {
	_, err := p.ptmx.Write(b)
	return err
}

// startReadPump launches a goroutine that reads from the PTY master and
// dispatches bytes to the OnData callback. The goroutine exits when the PTY
// master returns an error (typically EOF or EIO after the child exits) and
// closes Done().
func (p *ptyProc) startReadPump() {
	go func() {
		defer p.once.Do(func() { close(p.closeCh) })
		buf := make([]byte, 32<<10)
		for {
			n, err := p.ptmx.Read(buf)
			if n > 0 && p.onData != nil {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				p.onData(cp)
			}
			if err != nil {
				return
			}
		}
	}()
}
