// SPDX-License-Identifier: Apache-2.0

//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// TestPTY_RefusedByOlderAgent verifies that the host refuses a PTY spawn
// when the agent does not reply to MsgTypeCapabilityQuery.
//
// CLIWRAP_AGENT_NO_CAPABILITY=1 activates the dispatcher escape hatch
// (internal/agent/dispatcher.go) that silently drops the capability query,
// simulating an older agent binary. The host's negotiateCapabilities times
// out (or receives no reply) and concludes the agent lacks PTY support.
// Controller.Start must then return cwtypes.ErrPTYUnsupportedByAgent,
// re-exported as cliwrap.ErrPTYUnsupportedByAgent.
//
// Env-injection method: t.Setenv before BuildAgentForTest / manager
// construction. The spawner inherits os.Environ() at subprocess-exec time
// (see internal/supervise/spawner.go:99), so the env var is present in the
// agent process without requiring changes to Spawner or Manager APIs.
func TestPTY_RefusedByOlderAgent(t *testing.T) {
	defer goleak.VerifyNone(t)

	// Activate the "older agent" escape hatch before the agent is spawned.
	// t.Setenv restores the original value (or unsets) when the test ends.
	t.Setenv("CLIWRAP_AGENT_NO_CAPABILITY", "1")

	agentBin := supervise.BuildAgentForTest(t)
	mgr, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	// The capability negotiation timeout is 5 s; use a context that is
	// generous enough to cover that plus startup overhead.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = mgr.Shutdown(shutdownCtx)
	}()

	spec, err := cliwrap.NewSpec("pty-refused-by-older-agent", "/bin/cat").
		WithPTY(&cliwrap.PTYConfig{InitialCols: 80, InitialRows: 24}).
		WithStopTimeout(2 * time.Second).
		Build()
	require.NoError(t, err)

	h, err := mgr.Register(spec)
	require.NoError(t, err)

	startErr := h.Start(ctx)
	require.Error(t, startErr, "Start must fail when agent does not support PTY")
	require.True(t,
		errors.Is(startErr, cliwrap.ErrPTYUnsupportedByAgent),
		"expected ErrPTYUnsupportedByAgent, got: %v", startErr,
	)
}
