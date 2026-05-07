// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/internal/agent"
	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/internal/supervise"
)

func TestManager_ListPersistent_EmptyDir(t *testing.T) {
	agentBin := supervise.BuildAgentForTest(t)
	m, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
		WithPersistentDir(t.TempDir()),
	)
	require.NoError(t, err)
	defer func() { _ = m.Shutdown(context.Background()) }()

	got, err := m.ListPersistent()
	require.NoError(t, err)
	require.Empty(t, got, "no sessions on disk")
}

func TestManager_ListPersistent_NonexistentDirReturnsEmpty(t *testing.T) {
	agentBin := supervise.BuildAgentForTest(t)
	m, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
		WithPersistentDir(filepath.Join(t.TempDir(), "does-not-exist")),
	)
	require.NoError(t, err)
	defer func() { _ = m.Shutdown(context.Background()) }()

	got, err := m.ListPersistent()
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestManager_ListPersistent_DiscoversWrittenSession(t *testing.T) {
	agentBin := supervise.BuildAgentForTest(t)
	dir := t.TempDir()
	m, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
		WithPersistentDir(dir),
	)
	require.NoError(t, err)
	defer func() { _ = m.Shutdown(context.Background()) }()

	// Write a session metadata directly (simulating what an agent did).
	sessionDir := filepath.Join(dir, "demo-1")
	require.NoError(t, os.MkdirAll(sessionDir, 0o700))
	startedAt := time.Now().UTC()
	meta := agent.PersistentMeta{
		Version:   "1.0",
		ID:        "demo-1",
		Spec:      cwtypes.Spec{ID: "demo-1", Persistent: true},
		AgentPID:  os.Getpid(), // self — guaranteed alive
		StartedAt: startedAt,
	}
	require.NoError(t, agent.WritePersistentMeta(sessionDir, meta))
	require.NoError(t, agent.WritePidFile(sessionDir, os.Getpid()))

	got, err := m.ListPersistent()
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "demo-1", got[0].ID)
	require.True(t, got[0].Alive, "self pid should be Alive=true")
	require.False(t, got[0].Attached, ".attached flag absent")
	require.True(t, got[0].StartedAt.Equal(startedAt))
}

func TestManager_ListPersistent_StaleEntryReportedAsNotAlive(t *testing.T) {
	agentBin := supervise.BuildAgentForTest(t)
	dir := t.TempDir()
	m, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
		WithPersistentDir(dir),
	)
	require.NoError(t, err)
	defer func() { _ = m.Shutdown(context.Background()) }()

	sessionDir := filepath.Join(dir, "stale")
	require.NoError(t, os.MkdirAll(sessionDir, 0o700))
	require.NoError(t, agent.WritePersistentMeta(sessionDir, agent.PersistentMeta{
		Version: "1.0", ID: "stale",
		Spec:      cwtypes.Spec{ID: "stale", Persistent: true},
		AgentPID:  999_999_999,
		StartedAt: time.Now(),
	}))
	require.NoError(t, agent.WritePidFile(sessionDir, 999_999_999))

	got, err := m.ListPersistent()
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.False(t, got[0].Alive, "PID 999999999 should be Alive=false")
}

func TestManager_ListPersistent_SortedByID(t *testing.T) {
	agentBin := supervise.BuildAgentForTest(t)
	dir := t.TempDir()
	m, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
		WithPersistentDir(dir),
	)
	require.NoError(t, err)
	defer func() { _ = m.Shutdown(context.Background()) }()

	for _, id := range []string{"zeta", "alpha", "mid"} {
		sd := filepath.Join(dir, id)
		require.NoError(t, os.MkdirAll(sd, 0o700))
		require.NoError(t, agent.WritePersistentMeta(sd, agent.PersistentMeta{
			Version: "1.0", ID: id,
			Spec:      cwtypes.Spec{ID: id, Persistent: true},
			AgentPID:  1,
			StartedAt: time.Now(),
		}))
		require.NoError(t, agent.WritePidFile(sd, 1))
	}

	got, err := m.ListPersistent()
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, "alpha", got[0].ID)
	require.Equal(t, "mid", got[1].ID)
	require.Equal(t, "zeta", got[2].ID)
}

func TestManager_ListPersistent_SkipsPartialDirs(t *testing.T) {
	agentBin := supervise.BuildAgentForTest(t)
	dir := t.TempDir()
	m, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
		WithPersistentDir(dir),
	)
	require.NoError(t, err)
	defer func() { _ = m.Shutdown(context.Background()) }()

	// A dir with no meta.json (partial write or rm) should be silently skipped.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "partial"), 0o700))

	got, err := m.ListPersistent()
	require.NoError(t, err)
	require.Empty(t, got, "partial dir should be skipped")
}
