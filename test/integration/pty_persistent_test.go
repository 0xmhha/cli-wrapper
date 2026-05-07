// SPDX-License-Identifier: Apache-2.0

//go:build integration

package integration

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// shortPersistentDir returns a UNIX-socket-friendly directory under /tmp.
// macOS sun_path is ~104 bytes; t.TempDir() prefixes are too long.
func shortPersistentDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cw-pers-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestPersistent_BasicSpawnAndStop verifies a Persistent=true session
// can be spawned, started, used, stopped, and closed cleanly with no
// goroutine or process leaks.
func TestPersistent_BasicSpawnAndStop(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)
	persistentDir := shortPersistentDir(t)

	mgr, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
		cliwrap.WithPersistentDir(persistentDir),
	)
	require.NoError(t, err)
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = mgr.Shutdown(shutdownCtx)
		shutdownCancel()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	spec, err := cliwrap.NewSpec("persistent-basic", "/bin/cat").
		WithPTY(&cliwrap.PTYConfig{InitialCols: 80, InitialRows: 24}).
		WithStopTimeout(2 * time.Second).
		Persistent().
		WithRingBufferSize(4096).
		Build()
	require.NoError(t, err)

	h, err := mgr.Register(spec)
	require.NoError(t, err)
	require.NoError(t, h.Start(ctx))

	require.Eventually(t, func() bool {
		return h.Status().State == cliwrap.StateRunning
	}, 5*time.Second, 20*time.Millisecond)

	// Verify session metadata files exist
	sessionDir := filepath.Join(persistentDir, "persistent-basic")
	_, err = os.Stat(filepath.Join(sessionDir, "meta.json"))
	require.NoError(t, err, "meta.json should exist")
	_, err = os.Stat(filepath.Join(sessionDir, "pid"))
	require.NoError(t, err, "pid file should exist")
	_, err = os.Stat(filepath.Join(sessionDir, "sock"))
	require.NoError(t, err, "sock should exist")

	// Stop terminates the persistent session: child dies + agent self-terminates.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	require.NoError(t, h.Stop(stopCtx))
	_ = h.Close(stopCtx)
	stopCancel()

	// After Stop, sock + pid should be cleaned. meta.json + agent.log preserved.
	require.Eventually(t, func() bool {
		_, sockErr := os.Stat(filepath.Join(sessionDir, "sock"))
		_, pidErr := os.Stat(filepath.Join(sessionDir, "pid"))
		return os.IsNotExist(sockErr) && os.IsNotExist(pidErr)
	}, 3*time.Second, 50*time.Millisecond, "sock + pid should be removed after Stop")

	_, metaErr := os.Stat(filepath.Join(sessionDir, "meta.json"))
	require.NoError(t, metaErr, "meta.json should be preserved post-mortem")
}

// TestPersistent_HostExitAndReattach verifies the core reattach contract:
// after the original host releases its connection (h.Close on persistent
// session), the agent stays alive. A new Manager.Reattach reconnects and
// SubscribePTYData first yields the ring buffer contents.
func TestPersistent_HostExitAndReattach(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)
	persistentDir := shortPersistentDir(t)

	// First Manager: spawn the persistent session and write some PTY input.
	mgr1, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
		cliwrap.WithPersistentDir(persistentDir),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	spec, err := cliwrap.NewSpec("persistent-reattach", "/bin/cat").
		WithPTY(&cliwrap.PTYConfig{InitialCols: 80, InitialRows: 24}).
		WithStopTimeout(2 * time.Second).
		Persistent().
		WithRingBufferSize(4096).
		Build()
	require.NoError(t, err)

	h1, err := mgr1.Register(spec)
	require.NoError(t, err)
	require.NoError(t, h1.Start(ctx))
	require.Eventually(t, func() bool {
		return h1.Status().State == cliwrap.StateRunning
	}, 5*time.Second, 20*time.Millisecond)

	// Read agent PID for survival check.
	pidBytes, err := os.ReadFile(filepath.Join(persistentDir, "persistent-reattach", "pid"))
	require.NoError(t, err)

	// Send some content through the PTY so the ring buffer has data.
	require.NoError(t, h1.WriteInput(ctx, []byte("hello-redraw\n")))

	// Wait for echo to be observed (ensures the bytes are in the ring buffer).
	ch1, unsub1 := h1.SubscribePTYData()
	gotEcho := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !gotEcho {
		select {
		case d := <-ch1:
			if bytes.Contains(d.Bytes, []byte("hello-redraw")) {
				gotEcho = true
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	unsub1()
	require.True(t, gotEcho, "echo should be observed before detach")

	// Close on persistent: releases the conn but agent stays alive.
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	require.NoError(t, h1.Close(closeCtx))
	closeCancel()

	// Shutdown the Manager (does not affect persistent agent).
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = mgr1.Shutdown(shutdownCtx)
	shutdownCancel()

	// Verify agent process still alive: kill -0 against the recorded PID.
	pidStr := bytes.TrimSpace(pidBytes)
	require.NotEmpty(t, pidStr)
	// Parse PID and check via signal 0
	var pid int
	for _, b := range pidStr {
		if b >= '0' && b <= '9' {
			pid = pid*10 + int(b-'0')
		}
	}
	require.Greater(t, pid, 0)
	proc, err := os.FindProcess(pid)
	require.NoError(t, err)
	require.NoError(t, proc.Signal(syscall.Signal(0)), "agent process should still be alive after Manager.Shutdown")

	// Second Manager: Reattach to the persistent session.
	mgr2, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
		cliwrap.WithPersistentDir(persistentDir),
	)
	require.NoError(t, err)
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = mgr2.Shutdown(shutdownCtx)
		shutdownCancel()
	}()

	reattachCtx, reattachCancel := context.WithTimeout(context.Background(), 10*time.Second)
	h2, err := mgr2.Reattach(reattachCtx, "persistent-reattach")
	reattachCancel()
	require.NoError(t, err, "Reattach should succeed")
	require.NotNil(t, h2)

	// First SubscribePTYData chunk should contain the historical content
	// from the ring buffer ("hello-redraw" we sent earlier).
	ch2, unsub2 := h2.SubscribePTYData()
	defer unsub2()

	gotHistorical := false
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !gotHistorical {
		select {
		case d := <-ch2:
			if bytes.Contains(d.Bytes, []byte("hello-redraw")) {
				gotHistorical = true
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	require.True(t, gotHistorical, "reattach should deliver ring buffer content with prior PTY output")

	// Tear down properly
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	require.NoError(t, h2.Stop(stopCtx))
	_ = h2.Close(stopCtx)
	stopCancel()
}

// TestPersistent_StopFromReattachedHostFullyTerminates verifies that
// h.Stop on a reattach-derived handle terminates the agent (not just
// the child) and removes session metadata.
func TestPersistent_StopFromReattachedHostFullyTerminates(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)
	persistentDir := shortPersistentDir(t)

	mgr1, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
		cliwrap.WithPersistentDir(persistentDir),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	spec, err := cliwrap.NewSpec("persistent-stop", "/bin/cat").
		WithPTY(&cliwrap.PTYConfig{InitialCols: 80, InitialRows: 24}).
		WithStopTimeout(2 * time.Second).
		Persistent().
		WithRingBufferSize(2048).
		Build()
	require.NoError(t, err)

	h1, err := mgr1.Register(spec)
	require.NoError(t, err)
	require.NoError(t, h1.Start(ctx))
	require.Eventually(t, func() bool {
		return h1.Status().State == cliwrap.StateRunning
	}, 5*time.Second, 20*time.Millisecond)

	// Detach the original host
	_ = h1.Close(ctx)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = mgr1.Shutdown(shutdownCtx)
	shutdownCancel()

	// Reattach
	mgr2, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
		cliwrap.WithPersistentDir(persistentDir),
	)
	require.NoError(t, err)
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = mgr2.Shutdown(shutdownCtx)
		shutdownCancel()
	}()

	reattachCtx, reattachCancel := context.WithTimeout(context.Background(), 10*time.Second)
	h2, err := mgr2.Reattach(reattachCtx, "persistent-stop")
	reattachCancel()
	require.NoError(t, err)

	// Stop from reattached host should fully terminate.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	require.NoError(t, h2.Stop(stopCtx))
	_ = h2.Close(stopCtx)
	stopCancel()

	// Sock + pid should be removed; meta.json + agent.log preserved.
	sessionDir := filepath.Join(persistentDir, "persistent-stop")
	require.Eventually(t, func() bool {
		_, sockErr := os.Stat(filepath.Join(sessionDir, "sock"))
		_, pidErr := os.Stat(filepath.Join(sessionDir, "pid"))
		return os.IsNotExist(sockErr) && os.IsNotExist(pidErr)
	}, 3*time.Second, 50*time.Millisecond, "sock + pid removed after Stop from reattached host")

	_, metaErr := os.Stat(filepath.Join(sessionDir, "meta.json"))
	require.NoError(t, metaErr, "meta.json preserved")
}
