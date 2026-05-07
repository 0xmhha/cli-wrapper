// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

func TestPersistentMeta_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	meta := PersistentMeta{
		Version: "1.0",
		ID:      "test-session-1",
		Spec: cwtypes.Spec{
			ID:             "test-session-1",
			Command:        "/bin/cat",
			Persistent:     true,
			RingBufferSize: 1024,
		},
		AgentPID:  99999,
		StartedAt: time.Date(2026, 5, 7, 10, 23, 4, 123456789, time.UTC),
	}
	require.NoError(t, WritePersistentMeta(dir, meta))

	got, err := ReadPersistentMeta(dir)
	require.NoError(t, err)
	require.Equal(t, meta.ID, got.ID)
	require.Equal(t, meta.Version, got.Version)
	require.Equal(t, meta.AgentPID, got.AgentPID)
	require.True(t, got.StartedAt.Equal(meta.StartedAt), "StartedAt round-trip: got %v want %v", got.StartedAt, meta.StartedAt)
	require.Equal(t, meta.Spec.RingBufferSize, got.Spec.RingBufferSize)
	require.True(t, got.Spec.Persistent)
}

func TestPersistentMeta_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	meta := PersistentMeta{Version: "1.0", ID: "x", AgentPID: 1, StartedAt: time.Now()}
	require.NoError(t, WritePersistentMeta(dir, meta))

	st, err := os.Stat(filepath.Join(dir, "meta.json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm(), "meta.json perms")
}

func TestReadPersistentMeta_MissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadPersistentMeta(dir)
	require.Error(t, err, "expected error for missing meta.json")
}

func TestPidFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WritePidFile(dir, 12345))

	got, err := ReadPidFile(dir)
	require.NoError(t, err)
	require.Equal(t, 12345, got)
}

func TestPidFile_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, WritePidFile(dir, 1))

	st, err := os.Stat(filepath.Join(dir, "pid"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm(), "pid perms")
}

func TestReadPidFile_Missing(t *testing.T) {
	_, err := ReadPidFile(t.TempDir())
	require.Error(t, err)
}

func TestIsPidAlive_SelfTrue(t *testing.T) {
	require.True(t, IsPidAlive(os.Getpid()), "self pid should be alive")
}

func TestIsPidAlive_ZeroAndNegativeFalse(t *testing.T) {
	require.False(t, IsPidAlive(0))
	require.False(t, IsPidAlive(-1))
}

func TestIsPidAlive_ImpossiblyHighFalse(t *testing.T) {
	// Standard POSIX kernel won't issue PIDs this high without rollover;
	// macOS kern.maxproc default is 4000; Linux default is 4194303.
	// Either way, 999_999_999 won't be alive.
	require.False(t, IsPidAlive(999_999_999))
}
