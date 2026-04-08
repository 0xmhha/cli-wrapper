package cliwrap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
)

func TestManager_RegisterAndRun(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)

	mgr, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	spec, err := NewSpec("exit0", "/bin/sh", "-c", "exit 0").
		WithStopTimeout(2 * time.Second).
		Build()
	require.NoError(t, err)

	h, err := mgr.Register(spec)
	require.NoError(t, err)

	require.NoError(t, h.Start(ctx))

	require.Eventually(t, func() bool {
		return h.Status().State == StateStopped
	}, 3*time.Second, 20*time.Millisecond)

	require.NoError(t, mgr.Shutdown(ctx))
}
