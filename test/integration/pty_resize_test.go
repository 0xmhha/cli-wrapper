// SPDX-License-Identifier: Apache-2.0

//go:build integration

package integration

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// TestPTY_ResizeChangesChildView spawns a bash loop that continuously prints
// tput cols/lines, triggers a resize via h.Resize, and asserts the child picks
// up the new dimensions via SIGWINCH.
func TestPTY_ResizeChangesChildView(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)
	mgr, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = mgr.Shutdown(shutdownCtx)
	}()

	spec, err := cliwrap.NewSpec("pty-resize", "/bin/bash").
		WithPTY(&cliwrap.PTYConfig{InitialCols: 80, InitialRows: 24}).
		WithStopTimeout(2 * time.Second).
		Build()
	require.NoError(t, err)

	h, err := mgr.Register(spec)
	require.NoError(t, err)

	require.NoError(t, h.Start(ctx))
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = h.Stop(stopCtx)
		_ = h.Close(stopCtx)
	}()

	require.Eventually(t, func() bool {
		return h.Status().State == cliwrap.StateRunning
	}, 5*time.Second, 20*time.Millisecond, "process did not reach running state")

	ch, unsub := h.SubscribePTYData()
	defer unsub()

	// Send the tput loop to bash via stdin.
	cmd := []byte("while :; do tput cols; tput lines; sleep 0.1; done\n")
	require.NoError(t, h.WriteInput(ctx, cmd))

	// Wait for initial cols (80) to appear.
	require.True(t, drainContains(t, ch, "80", 3*time.Second), "expected initial cols=80 in PTY output")

	// Resize to 132×50; TIOCSWINSZ delivers SIGWINCH automatically.
	require.NoError(t, h.Resize(ctx, 132, 50))

	// Allow the loop to pick up new dimensions.
	require.True(t, drainContains(t, ch, "132", 3*time.Second), "expected cols=132 after resize")
	require.True(t, drainContains(t, ch, "50", 3*time.Second), "expected rows=50 after resize")
}

// drainContains reads from ch until a chunk contains substr OR timeout elapses.
func drainContains(t *testing.T, ch <-chan cliwrap.PTYData, substr string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case d, ok := <-ch:
			if !ok {
				return false
			}
			if bytes.Contains(d.Bytes, []byte(substr)) {
				return true
			}
		case <-deadline.C:
			return false
		}
	}
}
