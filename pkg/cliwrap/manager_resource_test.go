package cliwrap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/cli-wrapper/cli-wrapper/internal/supervise"
)

func TestManager_ChildPIDs(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)

	mgr, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	spec, err := NewSpec("sleeper", "/bin/sh", "-c", "sleep 0.5").
		WithStopTimeout(2 * time.Second).
		Build()
	require.NoError(t, err)

	h, err := mgr.Register(spec)
	require.NoError(t, err)
	require.NoError(t, h.Start(ctx))

	// Wait until the child has transitioned to Running before asserting on
	// ChildPIDs — Start returns once the agent handshake is done, but the
	// CHILD_STARTED message carrying the PID arrives asynchronously.
	require.Eventually(t, func() bool {
		return h.Status().State == StateRunning
	}, 2*time.Second, 20*time.Millisecond)

	require.Eventually(t, func() bool {
		return len(mgr.ChildPIDs()) > 0
	}, 2*time.Second, 20*time.Millisecond)

	require.Eventually(t, func() bool {
		return h.Status().State == StateStopped
	}, 3*time.Second, 20*time.Millisecond)

	require.NoError(t, mgr.Shutdown(ctx))
}
