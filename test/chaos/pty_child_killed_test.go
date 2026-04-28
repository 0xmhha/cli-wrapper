// SPDX-License-Identifier: Apache-2.0

//go:build chaos

package chaos

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// TestPTY_ChildSIGKILLDetected spawns /bin/sleep 100 via PTY, delivers
// SIGKILL directly to the child process (obtained from Status().ChildPID),
// and asserts the state transitions to Stopped or Crashed within 3 seconds.
// The cliwrap-agent observes the child's exit and sends MsgChildExited, which
// the controller translates into the terminal state transition.
func TestPTY_ChildSIGKILLDetected(t *testing.T) {
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

	spec, err := cliwrap.NewSpec("pty-child-killed", "/bin/sleep", "100").
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

	// Wait until the child is fully running so ChildPID is populated.
	require.Eventually(t, func() bool {
		return h.Status().State == cliwrap.StateRunning
	}, 5*time.Second, 20*time.Millisecond, "process did not reach running state")

	childPID := h.Status().ChildPID
	require.NotZero(t, childPID, "ChildPID must be non-zero once running")
	t.Logf("child PID=%d — sending SIGKILL", childPID)

	require.NoError(t, syscall.Kill(childPID, syscall.SIGKILL),
		"failed to SIGKILL child process")

	// The agent detects the child exit and sends MsgChildExited; the
	// controller transitions to Crashed (non-zero exit) or Stopped within 3 s.
	require.Eventually(t, func() bool {
		state := h.Status().State
		return state == cliwrap.StateCrashed || state == cliwrap.StateStopped
	}, 3*time.Second, 100*time.Millisecond,
		"state must transition to Crashed or Stopped within 3 s of child SIGKILL")
}
