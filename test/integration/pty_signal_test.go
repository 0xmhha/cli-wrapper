// SPDX-License-Identifier: Apache-2.0

//go:build integration

package integration

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// TestPTY_SignalSIGINTTerminatesSleep spawns /bin/sleep 100 in PTY mode, sends
// SIGINT via h.Signal, and asserts the process exits within 2 seconds.
func TestPTY_SignalSIGINTTerminatesSleep(t *testing.T) {
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

	spec, err := cliwrap.NewSpec("pty-signal-sigint", "/bin/sleep", "100").
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

	// Subscribe to PTY data to keep the agent's output path active.
	_, unsub := h.SubscribePTYData()
	defer unsub()

	// Deliver SIGINT to the foreground process group.
	signalCtx, signalCancel := context.WithTimeout(ctx, 2*time.Second)
	defer signalCancel()
	require.NoError(t, h.Signal(signalCtx, syscall.SIGINT))

	// Assert the process exits within 2 seconds of receiving SIGINT.
	require.Eventually(t, func() bool {
		state := h.Status().State
		return state == cliwrap.StateStopped || state == cliwrap.StateCrashed
	}, 2*time.Second, 20*time.Millisecond, "sleep did not exit within 2s after SIGINT")
}
