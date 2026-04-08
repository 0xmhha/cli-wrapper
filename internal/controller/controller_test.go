package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/internal/supervise"
)

func TestController_StartAgentAndStop(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)

	spawner := supervise.NewSpawner(supervise.SpawnerOptions{AgentPath: agentBin})

	spec := cwtypes.Spec{
		ID:          "echo-test",
		Command:     "/bin/sh",
		Args:        []string{"-c", "exit 0"},
		StopTimeout: 2 * time.Second,
	}

	ctrl, err := NewController(ControllerOptions{
		Spec:       spec,
		Spawner:    spawner,
		RuntimeDir: t.TempDir(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, ctrl.Start(ctx))

	// Wait for natural exit.
	require.Eventually(t, func() bool {
		return ctrl.State() == cwtypes.StateStopped || ctrl.State() == cwtypes.StateCrashed
	}, 3*time.Second, 20*time.Millisecond)

	require.NoError(t, ctrl.Close(ctx))
}
