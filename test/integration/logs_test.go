// test/integration/logs_test.go
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// TestIntegration_LogsSnapshotCapturesChildOutput confirms that child
// stdout and stderr bytes flow end-to-end through the agent → controller
// → manager pipeline and become readable via Manager.LogsSnapshot.
func TestIntegration_LogsSnapshotCapturesChildOutput(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)
	root := findRoot(t)
	noisy := filepath.Join(root, "test/fixtures/bin/fixture-noisy")

	mgr, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	// Bound the entire test to 60 s. Phase 1 (10 s) + Phase 2 (30 s) +
	// deferred Shutdown (5 s) + test overhead can add up to ~50 s on the
	// slowest observed CI runners (macOS × Go 1.23.x). The 60 s cap is
	// conservative so the test never races against go test -timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// CRITICAL: defer Shutdown so a test failure (including a fatal
	// require.Eventually) still drains all goroutines spawned by
	// Register → Start. Without this, a failed Eventually leaves the
	// Manager's watcher + controller readLoop + agent conn alive, and
	// the NEXT test's goleak check catches the contamination, causing
	// cascading failures. Use a fresh shutdownCtx so the outer ctx
	// expiring doesn't prevent shutdown from running to completion.
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = mgr.Shutdown(shutdownCtx)
	}()

	spec, err := cliwrap.NewSpec("noisy", noisy).
		WithStopTimeout(2 * time.Second).
		Build()
	require.NoError(t, err)

	h, err := mgr.Register(spec)
	require.NoError(t, err)
	require.NoError(t, h.Start(ctx))

	// Phase 1: wait for the process to reach Running state. Start(ctx)
	// returns after the START_CHILD IPC frame is enqueued, which is
	// BEFORE the agent has forked the child, so the first log byte is
	// never immediately available. We must wait for the controller to
	// observe MsgChildStarted and transition to StateRunning before we
	// start polling the log buffer — otherwise the log Eventually
	// budget is consumed by agent spawn + handshake + fork latency
	// instead of being a pure observation window.
	require.Eventually(t, func() bool {
		return h.Status().State == cliwrap.StateRunning
	}, 10*time.Second, 50*time.Millisecond,
		"process should reach Running state (agent spawn + handshake + child fork)")

	// Phase 2: fixture-noisy writes "out N" / "err N" every 100 ms.
	// Now that the child is guaranteed running, the first chunk should
	// arrive well within a few hundred milliseconds. We use a very
	// generous 30 s budget because CI macOS runners have shown latency
	// spikes where the IPC layer takes 10+ seconds to flush the first
	// MsgLogChunk frame through the agent → controller → collector
	// pipeline, even though the child is already confirmed Running.
	require.Eventually(t, func() bool {
		stdout := mgr.LogsSnapshot("noisy", 0)
		stderr := mgr.LogsSnapshot("noisy", 1)
		return bytes.Contains(stdout, []byte("out 0")) &&
			bytes.Contains(stderr, []byte("err 0"))
	}, 30*time.Second, 100*time.Millisecond,
		"expected fixture-noisy output to appear in log ring buffer within 30 s")

	// Snapshot both streams and assert sanity:
	// stdout should contain "out " prefix lines, stderr should contain "err ".
	stdout := mgr.LogsSnapshot("noisy", 0)
	stderr := mgr.LogsSnapshot("noisy", 1)

	require.Contains(t, string(stdout), "out 0")
	require.NotContains(t, string(stdout), "err ", "stdout stream must not contain stderr lines")

	require.Contains(t, string(stderr), "err 0")
	require.NotContains(t, string(stderr), "out ", "stderr stream must not contain stdout lines")
}
