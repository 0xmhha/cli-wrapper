// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// Manager-driven restart wiring is deferred; this test verifies that the
// manual Start/Close cycle works as a building block.
func TestIntegration_ManualRestartOnFailure(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)
	root := findRoot(t)
	crasher := filepath.Join(root, "test/fixtures/bin/fixture-crasher")

	mgr, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	spec, err := cliwrap.NewSpec("restarts", crasher, "--after", "100ms", "--code", "5").
		WithStopTimeout(time.Second).
		Build()
	require.NoError(t, err)

	h, err := mgr.Register(spec)
	require.NoError(t, err)

	crashes := 0
	for i := 0; i < 3; i++ {
		require.NoError(t, h.Start(ctx))
		require.Eventually(t, func() bool {
			return h.Status().State == cliwrap.StateCrashed
		}, 3*time.Second, 20*time.Millisecond)
		crashes++
		require.NoError(t, h.Close(ctx))
	}
	require.Equal(t, 3, crashes)

	require.NoError(t, mgr.Shutdown(ctx))
}
