package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/cli-wrapper/cli-wrapper/internal/supervise"
	"github.com/cli-wrapper/cli-wrapper/pkg/cliwrap"
)

func TestIntegration_CrashDetection(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)
	root := findRoot(t)
	crasher := filepath.Join(root, "test/fixtures/bin/fixture-crasher")

	mgr, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	spec, err := cliwrap.NewSpec("crasher", crasher, "--after", "200ms", "--code", "17").
		WithStopTimeout(2 * time.Second).
		Build()
	require.NoError(t, err)

	h, err := mgr.Register(spec)
	require.NoError(t, err)
	require.NoError(t, h.Start(ctx))

	require.Eventually(t, func() bool {
		return h.Status().State == cliwrap.StateCrashed
	}, 3*time.Second, 20*time.Millisecond, "process should transition to Crashed after exit code 17")

	require.NoError(t, mgr.Shutdown(ctx))
}
