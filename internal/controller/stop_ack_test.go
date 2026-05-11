// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/internal/supervise"
)

// TestController_StopBlocksUntilAck pins the v0.4.4 contract that when
// the agent advertises frame_ack, Controller.Stop does NOT return until
// the agent has drained the runner. The proof is that immediately after
// Stop returns, the child is reported as terminal — pre-v0.4.4 hosts
// observed a window of milliseconds where state was still StateStopping
// because the agent hadn't yet finished cancelling the runner.
func TestController_StopBlocksUntilAck(t *testing.T) {
	agentBin := supervise.BuildAgentForTest(t)
	spawner := supervise.NewSpawner(supervise.SpawnerOptions{AgentPath: agentBin})

	ctrl, err := NewController(ControllerOptions{
		Spec: cwtypes.Spec{
			ID:          "stop-ack-test",
			Command:     "/bin/sleep",
			Args:        []string{"60"},
			StopTimeout: 2 * time.Second,
		},
		Spawner:    spawner,
		RuntimeDir: t.TempDir(),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = ctrl.Close(ctx)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, ctrl.Start(ctx))

	// Sanity: agent is up and advertises the new capability.
	require.Eventually(t, func() bool { return ctrl.State() == cwtypes.StateRunning },
		3*time.Second, 20*time.Millisecond)
	require.True(t, ctrl.AgentSupportsFrameAck(), "v0.4.4 agent must advertise frame_ack")

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	t0 := time.Now()
	require.NoError(t, ctrl.Stop(stopCtx))
	elapsed := time.Since(t0)

	// Immediate post-Stop check: the controller's state must reflect a
	// terminal transition. Pre-v0.4.4 callers would see StateStopping
	// here, then have to poll for Stopped — the whole point of the ack
	// is to eliminate that polling window.
	s := ctrl.State()
	require.Contains(t,
		[]cwtypes.State{cwtypes.StateStopped, cwtypes.StateCrashed, cwtypes.StateFailed},
		s,
		"after Stop returns the state must be terminal (got %v after %v)", s, elapsed)
}

// TestController_AgentSupportsFrameAck_FeatureLookup pins the capability
// check the new Stop path branches on. Empty features slice (pre-0.4.4
// agent that doesn't reply to capability query, or older agents that
// reply without the frame_ack token) must return false so Stop falls
// back to the fire-and-forget send path. Presence of the token must
// return true.
func TestController_AgentSupportsFrameAck_FeatureLookup(t *testing.T) {
	cases := []struct {
		name     string
		features []string
		want     bool
	}{
		{"empty (pre-0.4.4 / no reply)", nil, false},
		{"other features only", []string{"pty", "persistence"}, false},
		{"with frame_ack", []string{"pty", "persistence", "frame_ack"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Controller{features: tc.features}
			require.Equal(t, tc.want, c.AgentSupportsFrameAck())
		})
	}
}
