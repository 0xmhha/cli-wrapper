package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// RunSpec describes the child process to fork/exec.
type RunSpec struct {
	Command     string
	Args        []string
	Env         map[string]string
	WorkDir     string
	StopTimeout time.Duration
	OnStarted   func(pid int) // called after cmd.Start() succeeds, before Wait()
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
}

// NewRunner returns a Runner with default sinks set to io.Discard.
func NewRunner() *Runner {
	return &Runner{
		StdoutSink: io.Discard,
		StderrSink: io.Discard,
	}
}

// Run forks/execs the child and blocks until it exits or ctx is canceled.
// On ctx cancel it sends SIGTERM, waits StopTimeout, then SIGKILL.
func (r *Runner) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
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
