// SPDX-License-Identifier: Apache-2.0

package chaos

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// TestPersistent_BurstSpawnReattach_NoLeak exercises the CW-G4
// spawn → host-exit → reattach → stop cycle 25 times in succession,
// asserting that no cliwrap-agent or cat processes survive.
//
// This implicitly validates that CW-G3.1 (host-side Manager state
// accumulation) is sidestepped by per-session UNIX sockets — each
// persistent session has its own sock so the per-Manager handshake
// resource pile-up does not happen.
//
// Opt-in: requires CHAOS_BURST=1.
func TestPersistent_BurstSpawnReattach_NoLeak(t *testing.T) {
	if os.Getenv("CHAOS_BURST") == "" {
		t.Skip("opt-in: set CHAOS_BURST=1 to run")
	}
	if testing.Short() {
		t.Skip("burst test takes ~30s")
	}
	if os.Getenv("CI") != "" && os.Getenv("BURST_FORCE") == "" {
		t.Skip("CI detected; set BURST_FORCE=1 to override")
	}
	defer goleak.VerifyNone(t)

	baselineAgentPIDs := captureCliwrapAgentPIDs()
	baselineCatPIDs := captureCatPIDs()
	t.Cleanup(func() { killCliwrapAgentsExcept(baselineAgentPIDs) })

	// Use /tmp for short paths (UNIX sun_path limit on macOS).
	persistentDir, err := os.MkdirTemp("/tmp", "cw-pburst-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(persistentDir) })

	agentBin := supervise.BuildAgentForTest(t)

	const N = 25
	for i := 0; i < N; i++ {
		if err := runPersistentBurstCycle(t, agentBin, persistentDir, i); err != nil {
			t.Fatalf("cycle %d: %v", i, err)
		}

		// Mid-loop sanity check every 5 iters.
		if (i+1)%5 == 0 {
			agentDelta := len(captureCliwrapAgentPIDs()) - len(baselineAgentPIDs)
			catDelta := countCatPIDsExcept(baselineCatPIDs)
			if agentDelta > 3 {
				t.Fatalf("ABORT iter %d: cliwrap-agent delta=%d > 3", i+1, agentDelta)
			}
			if catDelta > 3 {
				t.Fatalf("ABORT iter %d: cat delta=%d > 3", i+1, catDelta)
			}
		}
	}

	// Allow brief reaper drain.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		agentDelta := len(captureCliwrapAgentPIDs()) - len(baselineAgentPIDs)
		catDelta := countCatPIDsExcept(baselineCatPIDs)
		if agentDelta == 0 && catDelta == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if d := len(captureCliwrapAgentPIDs()) - len(baselineAgentPIDs); d != 0 {
		t.Fatalf("post-burst cliwrap-agent survivors: %d", d)
	}
	if d := countCatPIDsExcept(baselineCatPIDs); d != 0 {
		t.Fatalf("post-burst cat survivors: %d", d)
	}
}

// runPersistentBurstCycle does one spawn → close → reattach → stop cycle.
func runPersistentBurstCycle(t *testing.T, agentBin, persistentDir string, i int) error {
	t.Helper()
	id := fmt.Sprintf("p-%d", i)

	// Phase 1: spawn + close (leave agent running).
	mgr1, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(filepath.Join(t.TempDir(), "rt1-"+strconv.Itoa(i))),
		cliwrap.WithPersistentDir(persistentDir),
	)
	if err != nil {
		return fmt.Errorf("mgr1: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	spec, err := cliwrap.NewSpec(id, "/bin/cat").
		WithPTY(&cliwrap.PTYConfig{InitialCols: 80, InitialRows: 24}).
		WithStopTimeout(2 * time.Second).
		Persistent().
		WithRingBufferSize(1024).
		Build()
	if err != nil {
		return fmt.Errorf("spec: %w", err)
	}

	h1, err := mgr1.Register(spec)
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}
	if err := h1.Start(ctx); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	// Wait briefly for Running.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if h1.Status().State == cliwrap.StateRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = h1.Close(closeCtx)
	_ = mgr1.Shutdown(closeCtx)
	closeCancel()

	// Phase 2: reattach + stop.
	mgr2, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(filepath.Join(t.TempDir(), "rt2-"+strconv.Itoa(i))),
		cliwrap.WithPersistentDir(persistentDir),
	)
	if err != nil {
		return fmt.Errorf("mgr2: %w", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = mgr2.Shutdown(shutdownCtx)
		shutdownCancel()
	}()

	reattachCtx, reattachCancel := context.WithTimeout(context.Background(), 5*time.Second)
	h2, err := mgr2.Reattach(reattachCtx, id)
	reattachCancel()
	if err != nil {
		return fmt.Errorf("reattach: %w", err)
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := h2.Stop(stopCtx); err != nil {
		stopCancel()
		return fmt.Errorf("stop: %w", err)
	}
	_ = h2.Close(stopCtx)
	stopCancel()

	return nil
}

// countCatPIDsExcept returns the number of currently running 'cat'
// processes that are NOT in the baseline set.
func countCatPIDsExcept(baseline map[int]bool) int {
	current := captureCatPIDs()
	delta := 0
	for pid := range current {
		if !baseline[pid] {
			delta++
		}
	}
	return delta
}

// captureCatPIDs is defined in burst_spawn_test.go.
