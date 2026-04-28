// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

// ErrNoActivePTY is returned when a PTY write or control operation is attempted
// but no PTY child is currently running.
var ErrNoActivePTY = errors.New("agent: no active PTY child")

// RunSpec describes the child process to fork/exec.
type RunSpec struct {
	Command     string
	Args        []string
	Env         map[string]string
	WorkDir     string
	StopTimeout time.Duration
	OnStarted   func(pid int) // called after cmd.Start() succeeds, before Wait()
	PTY         *cwtypes.PTYConfig
}

// RunResult summarizes the child's exit.
type RunResult struct {
	PID      int
	ExitCode int
	Signal   int
	ExitedAt time.Time
	Reason   string
}

// Runner starts a single child process and blocks until it exits.
type Runner struct {
	// StdoutSink, if non-nil, is used to tee the child's stdout bytes.
	StdoutSink io.Writer
	// StderrSink, if non-nil, is used to tee the child's stderr bytes.
	StderrSink io.Writer

	// sender is used to emit MsgTypePTYData frames in PTY mode.
	// It must be set via SetSender before calling Run with a PTY spec.
	sender chunkSender

	// activePTY tracks the running PTY process for use by Tasks 11-13
	// (MsgPTYWrite, MsgPTYResize, MsgPTYSignal handlers).
	ptyMu     sync.Mutex
	activePTY *ptyProc
}

// NewRunner returns a Runner with default sinks set to io.Discard.
func NewRunner() *Runner {
	return &Runner{
		StdoutSink: io.Discard,
		StderrSink: io.Discard,
	}
}

// SetSender configures the outbound frame sender used in PTY mode.
// Call this before Run when RunSpec.PTY is non-nil.
func (r *Runner) SetSender(s chunkSender) { r.sender = s }

// ActivePTYProc returns the currently active ptyProc, or nil if none is running.
// Safe for concurrent use. Used by Tasks 11-13 to dispatch PTY control messages.
func (r *Runner) ActivePTYProc() *ptyProc {
	r.ptyMu.Lock()
	defer r.ptyMu.Unlock()
	return r.activePTY
}

// WriteToActivePTY forwards b to the running PTY child's stdin.
// Returns ErrNoActivePTY if no PTY child is currently active.
func (r *Runner) WriteToActivePTY(b []byte) error {
	p := r.ActivePTYProc()
	if p == nil {
		return ErrNoActivePTY
	}
	return p.WriteInput(b)
}

// ResizeActivePTY sends TIOCSWINSZ to the running PTY child with the given
// cols and rows. Returns ErrNoActivePTY if no PTY child is currently active.
func (r *Runner) ResizeActivePTY(cols, rows uint16) error {
	p := r.ActivePTYProc()
	if p == nil {
		return ErrNoActivePTY
	}
	return p.Resize(cols, rows)
}

// SignalActivePTY delivers sig to the foreground process group of the running
// PTY child. Returns ErrNoActivePTY if no PTY child is currently active.
func (r *Runner) SignalActivePTY(sig syscall.Signal) error {
	p := r.ActivePTYProc()
	if p == nil {
		return ErrNoActivePTY
	}
	return p.Signal(sig)
}

// Run forks/execs the child and blocks until it exits or ctx is canceled.
// On ctx cancel it sends SIGTERM, waits StopTimeout, then SIGKILL.
// If spec.PTY is non-nil, the child is started inside a pseudo-terminal and
// output is emitted as MsgTypePTYData frames via the configured sender.
func (r *Runner) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
	if spec.PTY != nil {
		return r.runPTY(ctx, spec)
	}

	cmd := exec.Command(spec.Command, spec.Args...)
	cmd.Env = flattenEnv(spec.Env)
	if spec.WorkDir != "" {
		cmd.Dir = spec.WorkDir
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{}, fmt.Errorf("runner: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return RunResult{}, fmt.Errorf("runner: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return RunResult{}, fmt.Errorf("runner: start: %w", err)
	}
	pid := cmd.Process.Pid

	// Notify caller that the child is running (before Wait).
	if spec.OnStarted != nil {
		spec.OnStarted(pid)
	}

	// Pump stdout and stderr concurrently.
	copyDone := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(r.StdoutSink, stdout); copyDone <- struct{}{} }()
	go func() { _, _ = io.Copy(r.StderrSink, stderr); copyDone <- struct{}{} }()

	// Kick off a goroutine that escalates on ctx cancel.
	stopSignal := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Signal(syscall.SIGTERM)
			timeout := spec.StopTimeout
			if timeout <= 0 {
				timeout = 5 * time.Second
			}
			select {
			case <-stopSignal:
			case <-time.After(timeout):
				_ = cmd.Process.Kill()
			}
		case <-stopSignal:
		}
	}()

	waitErr := cmd.Wait()
	close(stopSignal)
	<-copyDone
	<-copyDone

	var exitCode int
	var sig int
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
				if ws.Exited() {
					exitCode = ws.ExitStatus()
				}
				if ws.Signaled() {
					sig = int(ws.Signal())
					exitCode = 128 + sig
				}
			} else {
				exitCode = -1
			}
		} else {
			return RunResult{PID: pid}, fmt.Errorf("runner: wait: %w", waitErr)
		}
	}

	return RunResult{
		PID:      pid,
		ExitCode: exitCode,
		Signal:   sig,
		ExitedAt: time.Now(),
		Reason:   "exited",
	}, nil
}

// runPTY starts the child inside a PTY and blocks until it exits. Output is
// streamed as MsgTypePTYData frames via r.sender. The goroutine reading from
// the PTY master terminates naturally when the child exits and the master
// reaches EOF/EIO, satisfying goleak requirements.
func (r *Runner) runPTY(ctx context.Context, spec RunSpec) (RunResult, error) {
	proc, err := spawnPTY(ctx, spec)
	if err != nil {
		return RunResult{}, fmt.Errorf("runner: pty spawn: %w", err)
	}

	pid := proc.cmd.Process.Pid

	// Notify caller before blocking on exit.
	if spec.OnStarted != nil {
		spec.OnStarted(pid)
	}

	// Register the proc so Tasks 11-13 can dispatch PTY control messages.
	r.ptyMu.Lock()
	r.activePTY = proc
	r.ptyMu.Unlock()

	var seq atomic.Uint64
	proc.OnData(func(b []byte) {
		s := seq.Add(1)
		if r.sender != nil {
			// Pass the struct directly; SendControl encodes it once via
			// ipc.EncodePayload. Pre-encoding here then re-encoding in
			// SendControl would double-wrap the bytes.
			_ = r.sender.SendControl(ipc.MsgTypePTYData, ipc.PTYData{Seq: s, Bytes: b}, false)
		}
		// Tee to stdout sink (log collector) regardless of host attachment state
		// — per spec §3.3, PTY output must be persisted the same as pipe output.
		if r.StdoutSink != nil {
			_, _ = r.StdoutSink.Write(b)
		}
	})
	proc.startReadPump()

	// Block until the read pump exits (PTY master EOF/EIO after child exits).
	<-proc.Done()

	// Reap the child. cmd.Wait() should return quickly since the process has
	// already exited (PTY master EOF is the signal). This also closes the
	// goroutine for the SIGTERM escalation path in ctx cancellation.
	waitErr := proc.cmd.Wait()
	_ = proc.Close() // idempotent; ensures Done() is closed

	r.ptyMu.Lock()
	r.activePTY = nil
	r.ptyMu.Unlock()

	var exitCode int
	var sig int
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
				if ws.Exited() {
					exitCode = ws.ExitStatus()
				}
				if ws.Signaled() {
					sig = int(ws.Signal())
					exitCode = 128 + sig
				}
			} else {
				exitCode = -1
			}
		} else {
			return RunResult{PID: pid}, fmt.Errorf("runner: pty wait: %w", waitErr)
		}
	}

	return RunResult{
		PID:      pid,
		ExitCode: exitCode,
		Signal:   sig,
		ExitedAt: time.Now(),
		Reason:   "exited",
	}, nil
}

func flattenEnv(env map[string]string) []string {
	if len(env) == 0 {
		return os.Environ()
	}
	base := os.Environ()
	for k, v := range env {
		base = append(base, k+"="+v)
	}
	return base
}
