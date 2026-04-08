package cliwrap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cli-wrapper/cli-wrapper/internal/mgmt"
	"github.com/cli-wrapper/cli-wrapper/internal/supervise"
)

func TestManager_ListReturnsRegisteredProcesses(t *testing.T) {
	agentBin := supervise.BuildAgentForTest(t)
	mgr, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = mgr.Shutdown(ctx)
	}()

	spec, err := NewSpec("listed", "/bin/sh", "-c", "sleep 0.1").Build()
	require.NoError(t, err)
	_, err = mgr.Register(spec)
	require.NoError(t, err)

	var _ mgmt.ManagerAPI = mgr // compile-time assertion
	entries := mgr.List()
	require.Len(t, entries, 1)
	require.Equal(t, "listed", entries[0].ID)
}
