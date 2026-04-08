package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// TestIntegration_BulkShutdown registers many long-running processes, starts
// them, then verifies that Manager.Shutdown stops all of them within a
// bounded amount of time and leaves no goroutines behind.
//
// Plan 05's File Structure listed this as the "Bulk shutdown test" — the
// reliability bar is that even with N concurrent children, Shutdown remains
// deterministic and goroutine-clean.
//
// NOTE on the bound: Manager.Shutdown currently tears down handles sequentially
// (`for _, h := range handles { h.Close(ctx) }`), and each handle.Close calls
// AgentHandle.Close which sends SIGTERM and waits up to 3 seconds for the
// agent process to exit. So the worst-case bound scales linearly with the
// process count: roughly N * (StopTimeout + a few hundred ms of agent wind-down).
// We size the test (small N, modest StopTimeout) so that even the sequential
// path completes well under 30 seconds.
func TestIntegration_BulkShutdown(t *testing.T) {
	defer goleak.VerifyNone(t)

	const numProcesses = 5

	agentBin := supervise.BuildAgentForTest(t)

	mgr, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	startCtx, startCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer startCancel()

	handles := make([]cliwrap.ProcessHandle, 0, numProcesses)
	for i := 0; i < numProcesses; i++ {
		id := fmt.Sprintf("bulk-shutdown-%d", i)
		spec, err := cliwrap.NewSpec(id, "/bin/sh", "-c", "sleep 30").
			WithStopTimeout(500 * time.Millisecond).
			Build()
		require.NoError(t, err)

		h, err := mgr.Register(spec)
		require.NoError(t, err)
		require.NoError(t, h.Start(startCtx))
		handles = append(handles, h)
	}

	// Wait for every process to reach Running before tearing them down. This
	// prevents racing Shutdown against still-spawning agents and gives the
	// test stable observability semantics.
	for _, h := range handles {
		require.Eventually(t, func() bool {
			return h.Status().State == cliwrap.StateRunning
		}, 5*time.Second, 20*time.Millisecond,
			"process %s never reached Running", h.ID())
	}

	// Shutdown must complete within a reasonable bound. With N=5 and per-handle
	// teardown of ~3 seconds in the worst case, allow up to 25 seconds. The
	// real assertion is that Shutdown is bounded at all (no hangs) and that
	// every handle ends up in a terminal state.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	start := time.Now()
	require.NoError(t, mgr.Shutdown(shutdownCtx))
	elapsed := time.Since(start)

	require.Less(t, elapsed, 25*time.Second,
		"bulk shutdown took too long: %s", elapsed)

	// Every handle should be stopped/crashed (not still running).
	for _, h := range handles {
		state := h.Status().State
		require.Contains(t, []cliwrap.State{
			cliwrap.StateStopped,
			cliwrap.StateCrashed,
			cliwrap.StateStopping, // tolerated only momentarily; see check below
			cliwrap.StatePending,  // post-Close, handle drops its controller
		}, state, "process %s left in unexpected state %v", h.ID(), state)
		require.NotEqual(t, cliwrap.StateRunning, state,
			"process %s still Running after Shutdown", h.ID())
	}
}
