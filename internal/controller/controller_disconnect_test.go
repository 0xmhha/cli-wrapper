// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/internal/supervise"
)

// TestController_StateTransitionsOnDisconnect verifies that an unexpected loss
// of the IPC socket causes the controller to transition to StateCrashed with
// CrashSourceConnectionLost.
//
// We simulate the disconnect by closing the host-side socket without going
// through conn.Close() (which would suppress the disconnect callback).
// ForceDisconnectForTest is defined in export_test.go and is test-only.
func TestController_StateTransitionsOnDisconnect(t *testing.T) {
	agentBin := supervise.BuildAgentForTest(t)
	spawner := supervise.NewSpawner(supervise.SpawnerOptions{AgentPath: agentBin})

	spec := cwtypes.Spec{
		ID:          "disconnect-test",
		Command:     "/bin/sh",
		Args:        []string{"-c", "sleep 60"},
		StopTimeout: 2 * time.Second,
	}

	ctrl, err := NewController(ControllerOptions{
		Spec:       spec,
		Spawner:    spawner,
		RuntimeDir: t.TempDir(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, ctrl.Start(ctx))

	// Wait until the controller reaches StateRunning (child started).
	require.Eventually(t, func() bool {
		return ctrl.State() == cwtypes.StateRunning
	}, 5*time.Second, 20*time.Millisecond,
		"timed out waiting for StateRunning; got %s", ctrl.State())

	// Close the host-side socket without going through conn.Close(), so the
	// disconnect callback is not suppressed. This is the test-only path that
	// simulates a network-level disconnect (e.g. agent process killed, OS
	// recycles the fd, or a cable-pull scenario).
	require.NoError(t, ctrl.ForceDisconnectForTest())

	// The controller must transition to StateCrashed with CrashSourceConnectionLost.
	require.Eventually(t, func() bool {
		return ctrl.State() == cwtypes.StateCrashed &&
			ctrl.CrashSource() == CrashSourceConnectionLost
	}, 2*time.Second, 10*time.Millisecond,
		"expected StateCrashed/CrashSourceConnectionLost; got state=%s source=%s",
		ctrl.State(), ctrl.CrashSource())

	// Cleanup: Close will error (double-close on socket) but that's expected.
	// We just need to reap goroutines.
	_ = ctrl.Close(ctx)
}
