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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	spec, err := cliwrap.NewSpec("noisy", noisy).
		WithStopTimeout(2 * time.Second).
		Build()
	require.NoError(t, err)

	h, err := mgr.Register(spec)
	require.NoError(t, err)
	require.NoError(t, h.Start(ctx))

	// fixture-noisy writes "out N" / "err N" every 100 ms. Wait until
	// we can observe both streams in the ring buffer (bounded by 5 s).
	require.Eventually(t, func() bool {
		stdout := mgr.LogsSnapshot("noisy", 0)
		stderr := mgr.LogsSnapshot("noisy", 1)
		return bytes.Contains(stdout, []byte("out 0")) &&
			bytes.Contains(stderr, []byte("err 0"))
	}, 5*time.Second, 100*time.Millisecond,
		"expected fixture-noisy output to appear in log ring buffer within 5 s")

	// Snapshot both streams and assert sanity:
	// stdout should contain "out " prefix lines, stderr should contain "err ".
	stdout := mgr.LogsSnapshot("noisy", 0)
	stderr := mgr.LogsSnapshot("noisy", 1)

	require.Contains(t, string(stdout), "out 0")
	require.NotContains(t, string(stdout), "err ", "stdout stream must not contain stderr lines")

	require.Contains(t, string(stderr), "err 0")
	require.NotContains(t, string(stderr), "out ", "stderr stream must not contain stdout lines")

	require.NoError(t, mgr.Shutdown(ctx))
}
