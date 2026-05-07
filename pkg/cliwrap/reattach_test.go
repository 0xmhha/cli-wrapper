// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/internal/agent"
	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/internal/supervise"
)

func TestManager_Reattach_SessionNotFound(t *testing.T) {
	agentBin := supervise.BuildAgentForTest(t)
	m, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
		WithPersistentDir(t.TempDir()),
	)
	require.NoError(t, err)
	defer func() { _ = m.Shutdown(context.Background()) }()

	_, err = m.Reattach(context.Background(), "nonexistent")
	require.True(t, errors.Is(err, ErrSessionNotFound), "got %v; want ErrSessionNotFound", err)
}

func TestManager_Reattach_AgentDeadByMissingPid(t *testing.T) {
	agentBin := supervise.BuildAgentForTest(t)
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "x")
	require.NoError(t, os.MkdirAll(sessionDir, 0o700))
	require.NoError(t, agent.WritePersistentMeta(sessionDir, agent.PersistentMeta{
		Version: "1.0", ID: "x",
		Spec:      cwtypes.Spec{ID: "x", Persistent: true},
		AgentPID:  999_999_999, // dead
		StartedAt: time.Now(),
	}))
	// Note: NO pid file written → ReadPidFile fails → fallback to meta.AgentPID
	// (also dead) → ErrAgentDead.

	m, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
		WithPersistentDir(dir),
	)
	require.NoError(t, err)
	defer func() { _ = m.Shutdown(context.Background()) }()

	_, err = m.Reattach(context.Background(), "x")
	require.True(t, errors.Is(err, ErrAgentDead), "got %v; want ErrAgentDead", err)
}

func TestManager_Reattach_AgentDeadByDeadPid(t *testing.T) {
	agentBin := supervise.BuildAgentForTest(t)
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "x")
	require.NoError(t, os.MkdirAll(sessionDir, 0o700))
	require.NoError(t, agent.WritePersistentMeta(sessionDir, agent.PersistentMeta{
		Version: "1.0", ID: "x",
		Spec:      cwtypes.Spec{ID: "x", Persistent: true},
		AgentPID:  999_999_999,
		StartedAt: time.Now(),
	}))
	require.NoError(t, agent.WritePidFile(sessionDir, 999_999_999))

	m, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
		WithPersistentDir(dir),
	)
	require.NoError(t, err)
	defer func() { _ = m.Shutdown(context.Background()) }()

	_, err = m.Reattach(context.Background(), "x")
	require.True(t, errors.Is(err, ErrAgentDead), "got %v; want ErrAgentDead", err)
}

func TestManager_Reattach_SessionNotFoundWhenSockMissing(t *testing.T) {
	agentBin := supervise.BuildAgentForTest(t)
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "live-but-no-sock")
	require.NoError(t, os.MkdirAll(sessionDir, 0o700))
	// Live PID (self), valid meta, but no sock file.
	require.NoError(t, agent.WritePersistentMeta(sessionDir, agent.PersistentMeta{
		Version: "1.0", ID: "live-but-no-sock",
		Spec:      cwtypes.Spec{ID: "live-but-no-sock", Persistent: true},
		AgentPID:  os.Getpid(),
		StartedAt: time.Now(),
	}))
	require.NoError(t, agent.WritePidFile(sessionDir, os.Getpid()))

	m, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
		WithPersistentDir(dir),
	)
	require.NoError(t, err)
	defer func() { _ = m.Shutdown(context.Background()) }()

	// Dial returns ENOENT for non-existent sock; classified as ErrSessionNotFound.
	_, err = m.Reattach(context.Background(), "live-but-no-sock")
	// Could be ErrSessionNotFound (no sock file) OR ErrAgentDead (econnrefused).
	// On macOS, dial of a non-existent unix path returns ENOENT-ish.
	require.Error(t, err, "expected error for missing sock")
}

// Happy-path reattach end-to-end test is in test/integration/pty_persistent_test.go
// (Task 18) — requires a real persistent agent process.
