// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
)

// TestProcessHandle_CloseStatusReflectsTerminalState pins the contract that
// Status() returns a terminal state after Close, not whatever transient
// value (typically StateRunning) was active during the in-flight shutdown.
// Before the snapshot-after-Close fix, hosts asserting
// Status() == StateStopped immediately after Close would observe
// StateRunning because processHandle.Close snapshotted lastStatus BEFORE
// invoking ctrl.Close.
func TestProcessHandle_CloseStatusReflectsTerminalState(t *testing.T) {
	agentBin := supervise.BuildAgentForTest(t)
	mgr, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = mgr.Shutdown(ctx)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	spec, err := NewSpec("close-status", "/bin/sleep", "60").Build()
	require.NoError(t, err)
	h, err := mgr.Register(spec)
	require.NoError(t, err)
	require.NoError(t, h.Start(ctx))

	require.Eventually(t, func() bool {
		return h.Status().State == StateRunning
	}, 3*time.Second, 20*time.Millisecond)

	// Stop the child, then Close. After Close returns the status must be
	// terminal — StateStopped is the expected value for a graceful Stop;
	// StateCrashed / StateFailed would also be valid terminal outcomes if
	// the child died abnormally between Stop's send and Close's snapshot.
	require.NoError(t, h.Stop(ctx))
	require.NoError(t, h.Close(ctx))

	final := h.Status().State
	require.Contains(t,
		[]State{StateStopped, StateCrashed, StateFailed},
		final,
		"Status() after Close must be terminal, got %v", final)
}
