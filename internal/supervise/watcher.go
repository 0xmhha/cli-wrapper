// SPDX-License-Identifier: Apache-2.0

package supervise

import (
	"context"
	"errors"
	"os"
	"syscall"
)

// WatchResult is the OS-level summary of a child's exit.
type WatchResult struct {
	PID      int
	ExitCode int
	Signal   int
}

// Watcher wraps os.Process.Wait in a context-aware API.
type Watcher struct {
	proc *os.Process
}

// NewWatcher returns a Watcher for proc.
func NewWatcher(proc *os.Process) *Watcher {
	return &Watcher{proc: proc}
}

// Wait blocks until the process exits or ctx is canceled. On ctx cancel
// it returns ctx.Err() without killing the process — the caller must
// decide how to escalate.
func (w *Watcher) Wait(ctx context.Context) (WatchResult, error) {
	type waitOut struct {
		state *os.ProcessState
		err   error
	}
	ch := make(chan waitOut, 1)
	go func() {
		st, err := w.proc.Wait()
		ch <- waitOut{st, err}
	}()

	select {
	case out := <-ch:
		if out.err != nil {
			return WatchResult{PID: w.proc.Pid}, out.err
		}
		st := out.state
		res := WatchResult{PID: w.proc.Pid, ExitCode: st.ExitCode()}
		if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			res.Signal = int(ws.Signal())
		}
		return res, nil
	case <-ctx.Done():
		return WatchResult{PID: w.proc.Pid}, errors.New("watcher: context canceled")
	}
}
