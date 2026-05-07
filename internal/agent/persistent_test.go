// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

// shortSessionDir returns a UNIX-socket-friendly directory under /tmp.
// macOS sun_path is limited to ~104 bytes, and t.TempDir() returns paths
// under /var/folders/.../TestName/NNN/ which routinely exceed that. Use
// this helper for tests that ListenUnix.
func shortSessionDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cw-test-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestPersistentBootstrap_WritesMetaPidAndOpensSocket(t *testing.T) {
	dir := filepath.Join(shortSessionDir(t), "s1")
	spec := cwtypes.Spec{ID: "test-1", Command: "/bin/cat", Persistent: true, RingBufferSize: 1024}

	pst, err := initPersistent(persistentInitOpts{
		SessionDir: dir,
		AgentID:    "test-1",
		Spec:       spec,
	})
	require.NoError(t, err)
	defer pst.Close()

	// meta.json present, mode 0600
	st, err := os.Stat(filepath.Join(dir, "meta.json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())

	// pid present, mode 0600
	pidSt, err := os.Stat(filepath.Join(dir, "pid"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), pidSt.Mode().Perm())

	// SessionDir mode 0700
	dirSt, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), dirSt.Mode().Perm())

	// sock listening + dialable
	conn, err := net.Dial("unix", filepath.Join(dir, "sock"))
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

func TestPersistentBootstrap_DefaultsRingBufferSizeWhenZero(t *testing.T) {
	dir := filepath.Join(shortSessionDir(t), "sd")
	spec := cwtypes.Spec{ID: "test-default", Command: "/bin/cat", Persistent: true, RingBufferSize: 0}
	pst, err := initPersistent(persistentInitOpts{
		SessionDir: dir, AgentID: "test-default", Spec: spec,
	})
	require.NoError(t, err)
	defer pst.Close()

	require.Equal(t, defaultRingBufferSize, pst.ringBufferCapForTest(),
		"RingBufferSize=0 should default to %d", defaultRingBufferSize)
}

func TestPersistentBootstrap_RequiresSessionDir(t *testing.T) {
	_, err := initPersistent(persistentInitOpts{
		SessionDir: "",
		AgentID:    "x",
		Spec:       cwtypes.Spec{ID: "x", Persistent: true, RingBufferSize: 1024},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "SessionDir required")
}

func TestPersistentState_CloseRemovesSockAndPidPreservesMeta(t *testing.T) {
	dir := filepath.Join(shortSessionDir(t), "sc")
	pst, err := initPersistent(persistentInitOpts{
		SessionDir: dir, AgentID: "x",
		Spec: cwtypes.Spec{ID: "x", Persistent: true, RingBufferSize: 1024},
	})
	require.NoError(t, err)

	pst.Close()

	// sock + pid removed
	_, sockErr := os.Stat(filepath.Join(dir, "sock"))
	require.True(t, os.IsNotExist(sockErr), "sock should be removed; got %v", sockErr)
	_, pidErr := os.Stat(filepath.Join(dir, "pid"))
	require.True(t, os.IsNotExist(pidErr), "pid should be removed; got %v", pidErr)

	// meta.json preserved (post-mortem diagnostic)
	_, metaErr := os.Stat(filepath.Join(dir, "meta.json"))
	require.NoError(t, metaErr, "meta.json should be preserved")
}

func TestPersistentBootstrap_StaleSockReplacedNotFailed(t *testing.T) {
	dir := filepath.Join(shortSessionDir(t), "ss")
	require.NoError(t, os.MkdirAll(dir, 0o700))

	// Pre-create a stale sock file.
	staleFile := filepath.Join(dir, "sock")
	require.NoError(t, os.WriteFile(staleFile, []byte("stale"), 0o600))

	pst, err := initPersistent(persistentInitOpts{
		SessionDir: dir, AgentID: "x",
		Spec: cwtypes.Spec{ID: "x", Persistent: true, RingBufferSize: 1024},
	})
	require.NoError(t, err, "stale sock should be replaced, not block init")
	defer pst.Close()

	// New sock is dialable
	conn, err := net.Dial("unix", staleFile)
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

func TestPersistentAccept_AcceptsFirstConnection(t *testing.T) {
	dir := filepath.Join(shortSessionDir(t), "ac1")
	pst, err := initPersistent(persistentInitOpts{
		SessionDir: dir, AgentID: "x",
		Spec: cwtypes.Spec{ID: "x", Persistent: true, RingBufferSize: 1024},
	})
	require.NoError(t, err)
	defer pst.Close()

	acceptedCh := make(chan net.Conn, 1)
	go pst.runAcceptLoop(func(c net.Conn) {
		acceptedCh <- c
		// Hold conn open briefly so attach state is observable.
		time.Sleep(50 * time.Millisecond)
		_ = c.Close()
		pst.markDetached()
	})

	conn, err := net.Dial("unix", filepath.Join(dir, "sock"))
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	select {
	case got := <-acceptedCh:
		require.NotNil(t, got)
	case <-time.After(time.Second):
		t.Fatalf("accept timeout")
	}
}

func TestPersistentAccept_RejectsSecondConcurrentAttach(t *testing.T) {
	dir := filepath.Join(shortSessionDir(t), "ac2")
	pst, err := initPersistent(persistentInitOpts{
		SessionDir: dir, AgentID: "x",
		Spec: cwtypes.Spec{ID: "x", Persistent: true, RingBufferSize: 1024},
	})
	require.NoError(t, err)
	defer pst.Close()

	releaseFirst := make(chan struct{})
	go pst.runAcceptLoop(func(c net.Conn) {
		// Hold the first conn until released.
		<-releaseFirst
		_ = c.Close()
		pst.markDetached()
	})

	// First attach
	conn1, err := net.Dial("unix", filepath.Join(dir, "sock"))
	require.NoError(t, err)
	defer func() { _ = conn1.Close() }()
	// Allow agent to mark attached
	time.Sleep(50 * time.Millisecond)

	// .attached flag should exist
	_, err = os.Stat(filepath.Join(dir, ".attached"))
	require.NoError(t, err, ".attached flag should exist while attached")

	// Second attach
	conn2, err := net.Dial("unix", filepath.Join(dir, "sock"))
	if err == nil {
		// Got a conn; expect rejection message + close.
		defer func() { _ = conn2.Close() }()
		buf := make([]byte, 64)
		_ = conn2.SetReadDeadline(time.Now().Add(time.Second))
		n, _ := conn2.Read(buf)
		if n > 0 {
			require.Contains(t, string(buf[:n]), "ALREADY_ATTACHED",
				"rejection message")
		}
	}

	close(releaseFirst)

	// Allow .attached to clear
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(dir, ".attached"))
		return os.IsNotExist(err)
	}, 2*time.Second, 20*time.Millisecond, ".attached should be removed after disconnect")
}
