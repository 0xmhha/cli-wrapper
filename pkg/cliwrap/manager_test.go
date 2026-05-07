// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"context"
	"os"
	"path/filepath"
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

func TestManager_WithPersistentDirSetsField(t *testing.T) {
	tmp := t.TempDir()
	agentBin := supervise.BuildAgentForTest(t)
	m, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
		WithPersistentDir(tmp),
	)
	require.NoError(t, err)
	defer func() { _ = m.Shutdown(context.Background()) }()

	require.Equal(t, tmp, m.persistentDir, "WithPersistentDir should set m.persistentDir")
}

func TestManager_DefaultPersistentDir(t *testing.T) {
	agentBin := supervise.BuildAgentForTest(t)
	m, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)
	defer func() { _ = m.Shutdown(context.Background()) }()

	want := filepath.Join(os.Getenv("HOME"), ".cliwrap", "sessions")
	require.Equal(t, want, m.persistentDir, "default persistentDir = $HOME/.cliwrap/sessions")
}
