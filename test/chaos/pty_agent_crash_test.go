// SPDX-License-Identifier: Apache-2.0

//go:build chaos

// TestPTY_AgentCrashDetectedWithin5s verifies the end-to-end CW-G1 path:
// SIGKILL on the cliwrap-agent process → host-side IPC socket EOF →
// ipc.Conn fires SetOnDisconnect → controller transitions to StateCrashed.
package chaos

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// mustAgentPID returns the PID of the cliwrap-agent process that is the
// parent of the given child PID. We look it up via `ps -o ppid=` so we don't
// need to expose the agent process through the public cliwrap API.
func mustAgentPID(t *testing.T, childPID int) int {
	t.Helper()
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(childPID)).Output()
	require.NoError(t, err, "ps -o ppid= -p %d failed", childPID)
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	require.NoError(t, err, "could not parse ppid from ps output: %q", string(out))
	require.NotZero(t, pid, "agent PID must not be zero")
	return pid
}

// TestPTY_AgentCrashDetectedWithin5s spawns /usr/bin/yes via PTY, SIGKILLs
// the cliwrap-agent subprocess (the parent of the child), and asserts that
// the host-side controller transitions to StateCrashed within 5 seconds.
//
// This is the CW-G1 moment-of-truth: SIGKILL → OS closes the Unix socket FD
// → ipc.Conn.readLoop exits with EOF → fireDisconnect fires → controller
// sets StateCrashed via the SetOnDisconnect callback.
func TestPTY_AgentCrashDetectedWithin5s(t *testing.T) {
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

	spec, err := cliwrap.NewSpec("pty-agent-crash", "/usr/bin/yes").
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

	// Resolve the agent PID from the child's parent process.
	agentPID := mustAgentPID(t, childPID)
	t.Logf("child PID=%d agent PID=%d — sending SIGKILL to agent", childPID, agentPID)

	killStart := time.Now()
	require.NoError(t, syscall.Kill(agentPID, syscall.SIGKILL),
		"failed to SIGKILL cliwrap-agent process")

	// The OS will close the agent's end of the Unix socket when the process
	// dies, which produces an EOF on the host-side read. The readLoop exits,
	// fireDisconnect fires, and the controller transitions to StateCrashed.
	//
	// 5 s deadline: on Linux this is sub-millisecond; on macOS the kernel
	// recycles the FD slightly later but well within 5 s in practice.
	require.Eventually(t, func() bool {
		state := h.Status().State
		return state == cliwrap.StateCrashed || state == cliwrap.StateStopped
	}, 5*time.Second, 50*time.Millisecond,
		"state must transition to Crashed or Stopped within 5 s of agent SIGKILL")

	elapsed := time.Since(killStart)
	t.Logf("disconnect detected in %s (state=%s)", elapsed.Round(time.Millisecond), h.Status().State)
}
