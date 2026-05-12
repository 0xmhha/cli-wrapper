// SPDX-License-Identifier: Apache-2.0

// TestBurst_SpawnStop_NoLeak is the CW-G3 regression test (RED). It exercises
// burst spawn/terminate via the public cliwrap API and asserts that the host
// does not leak processes. Today this test FAILS — the leak is real.
//
// Safety: this test deliberately drives the host toward kern.maxproc
// exhaustion (the very state CW-G3 produces). Multiple gates protect the
// developer's machine. See spec §"Test Strategy → Test safety constraints".
package chaos

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// Safety constants. See spec §"Test safety constraints".
//
// These are absolute counts tied to cli-wrapper's intended operating scale,
// NOT fractions of OS-level kern.maxproc. cli-wrapper is a CLI session
// supervisor — typical use is single-digit to low-tens of concurrent agents
// per host. The macOS kern.maxproc default (~4000) is irrelevant: if a
// cli-wrapper deployment ever has >30 cliwrap-agent processes alive, that
// is itself a malfunction, regardless of OS headroom.
//
// The leak signal is **cli-wrapper-attributable count delta** (cliwrap-agent
// and PTY child basename "cat") rather than system-wide pidCount, which is
// polluted by unrelated background activity (Spotlight, browser helpers).
// A correctly-supervised burst keeps both deltas at 0 across all iterations.
const (
	// burstDefaultN was originally 15 because of CW-G3.1 — a host-side
	// capability-negotiation timeout that surfaced around iteration ~20.
	// Diagnostic re-validation 2026-05-07 (post CW-G3 + CW-G4 landing) at
	// N=25 / 50 / 100 showed goroutines and host fds remained stable
	// (goroutines=3, fds=11 throughout). The host-side state leak no
	// longer reproduces; CW-G3.1 is RESOLVED — incidentally fixed by
	// CW-G3's drain + StopTimeout cascade alignment which narrowed the
	// host-side cleanup race window. N=50 gives strong regression
	// coverage (~20s test time) without crossing into hot-loop territory.
	// Raise via BURST_N for stress runs; cap is burstMaxN below.
	burstDefaultN       = 50
	burstMaxN           = 100 // hard cap. cli-wrapper should never need >100 concurrent.
	burstAgentPreflight = 10  // skip if host already has >10 cliwrap-agent processes alive
	burstAgentCeiling   = 30  // mid-loop abort if cliwrap-agent count exceeds this (defense-in-depth)
	burstAgentDelta     = 3   // mid-loop abort if cliwrap-agent count grew by >this from baseline
	burstChildDelta     = 3   // mid-loop abort if PTY child (cat) count grew by >this from baseline
	burstCheckEvery     = 5   // check cadence
)

// pidCount returns the number of running processes visible to the test host.
// Kept as a debug helper — surfaced in failure messages for context — but no
// longer used as the abort gate. cli-wrapper-attributable counts (see
// captureCliwrapAgentPIDs and captureCatPIDs) are the actual leak signals.
func pidCount(t *testing.T) int {
	t.Helper()
	out, err := exec.Command("sh", "-c", "ps -A | wc -l").Output()
	if err != nil {
		t.Fatalf("pidCount: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("pidCount parse %q: %v", string(out), err)
	}
	return n
}

// captureCatPIDs returns the set of currently running 'cat' processes
// (basename match against ps comm column). Used to detect leaked PTY
// children spawned by the burst test, since each iteration spawns
// /bin/cat under PTY.
func captureCatPIDs() map[int]bool {
	pids := map[int]bool{}
	out, err := exec.Command("sh", "-c", "ps -A -o pid,comm").Output()
	if err != nil {
		return pids
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[1] != "cat" {
			continue
		}
		if pid, err := strconv.Atoi(fields[0]); err == nil {
			pids[pid] = true
		}
	}
	return pids
}

// captureCliwrapAgentPIDs returns the set of currently running cliwrap-agent
// PIDs. Used as the baseline so cleanup only kills agents WE spawned.
func captureCliwrapAgentPIDs() map[int]bool {
	pids := map[int]bool{}
	out, err := exec.Command("sh", "-c", "ps -A -o pid,comm").Output()
	if err != nil {
		return pids
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if !strings.Contains(fields[1], "cliwrap-agent") {
			continue
		}
		if pid, err := strconv.Atoi(fields[0]); err == nil {
			pids[pid] = true
		}
	}
	return pids
}

// killCliwrapAgentsExcept terminates cliwrap-agent processes that are NOT in
// the baseline set. Sends SIGTERM, waits briefly, then SIGKILL on stragglers.
// Never affects unrelated processes by design.
func killCliwrapAgentsExcept(baseline map[int]bool) {
	current := captureCliwrapAgentPIDs()
	var stray []string
	for pid := range current {
		if !baseline[pid] {
			stray = append(stray, strconv.Itoa(pid))
		}
	}
	if len(stray) == 0 {
		return
	}
	args := append([]string{"-TERM"}, stray...)
	_ = exec.Command("kill", args...).Run()
	time.Sleep(500 * time.Millisecond)
	args = append([]string{"-KILL"}, stray...)
	_ = exec.Command("kill", args...).Run()
}

func TestBurst_SpawnStop_NoLeak(t *testing.T) {
	// Opt-in only: this test intentionally exercises kern.maxproc.
	if os.Getenv("CHAOS_BURST") == "" {
		t.Skip("opt-in: set CHAOS_BURST=1 to run the burst leak regression test")
	}
	if testing.Short() {
		t.Skip("burst test takes ~10s")
	}
	if os.Getenv("CI") != "" && os.Getenv("BURST_FORCE") == "" {
		t.Skip("CI detected; set BURST_FORCE=1 to override")
	}
	defer goleak.VerifyNone(t)

	baselineAgentPIDs := captureCliwrapAgentPIDs()
	baselineCatPIDs := captureCatPIDs()
	t.Cleanup(func() { killCliwrapAgentsExcept(baselineAgentPIDs) })

	if len(baselineAgentPIDs) > burstAgentPreflight {
		t.Skipf("host already has %d cliwrap-agent processes (>%d); too loaded for safe burst test",
			len(baselineAgentPIDs), burstAgentPreflight)
	}
	baselineAgentCount := len(baselineAgentPIDs)
	baselineCatCount := len(baselineCatPIDs)
	baselinePID := pidCount(t) // for debug context only

	N := burstDefaultN
	if v := os.Getenv("BURST_N"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > burstMaxN {
				t.Fatalf("BURST_N=%d exceeds hard cap %d (cli-wrapper should never need >100 concurrent)", n, burstMaxN)
			}
			N = n
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Manager construction reuses the pattern from test/chaos/pty_agent_crash_test.go:
	// BuildAgentForTest + NewManager(WithAgentPath, WithRuntimeDir).
	agentBin := supervise.BuildAgentForTest(t)
	mgr, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = mgr.Shutdown(shutdownCtx)
		shutdownCancel()
	}()

	for i := 0; i < N; i++ {
		spec, err := cliwrap.NewSpec(fmt.Sprintf("burst-%d", i), "/bin/cat").
			WithPTY(&cliwrap.PTYConfig{InitialCols: 80, InitialRows: 24}).
			WithStopTimeout(2 * time.Second).
			Build()
		if err != nil {
			t.Fatalf("spec %d: %v", i, err)
		}
		h, err := mgr.Register(spec)
		if err != nil {
			t.Fatalf("register %d: %v (pidCount=%d)", i, err, pidCount(t))
		}
		if err := h.Start(ctx); err != nil {
			t.Fatalf("start %d: %v (pidCount=%d)", i, err, pidCount(t))
		}
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.Stop(stopCtx); err != nil {
			stopCancel()
			t.Fatalf("stop %d: %v (pidCount=%d)", i, err, pidCount(t))
		}
		_ = h.Close(stopCtx)
		stopCancel()

		if (i+1)%burstCheckEvery == 0 {
			agentNow := len(captureCliwrapAgentPIDs())
			catNow := len(captureCatPIDs())
			if agentNow > burstAgentCeiling {
				t.Fatalf("ABORT iter %d: cliwrap-agent count=%d > ceiling=%d (defense-in-depth)",
					i+1, agentNow, burstAgentCeiling)
			}
			if d := agentNow - baselineAgentCount; d > burstAgentDelta {
				t.Fatalf("ABORT iter %d: cliwrap-agent count grew by %d (>%d); leak detected (pidCount=%d, baselinePID=%d)",
					i+1, d, burstAgentDelta, pidCount(t), baselinePID)
			}
			if d := catNow - baselineCatCount; d > burstChildDelta {
				t.Fatalf("ABORT iter %d: PTY child (cat) count grew by %d (>%d); leak detected (pidCount=%d, baselinePID=%d)",
					i+1, d, burstChildDelta, pidCount(t), baselinePID)
			}
		}
	}

	// Allow brief reaper drain — async kernel reap can lag h.Close() return.
	deadline := time.Now().Add(2 * time.Second)
	var agentSurvivors, catSurvivors int
	for time.Now().Before(deadline) {
		agentSurvivors = len(captureCliwrapAgentPIDs()) - baselineAgentCount
		catSurvivors = len(captureCatPIDs()) - baselineCatCount
		if agentSurvivors == 0 && catSurvivors == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if agentSurvivors != 0 || catSurvivors != 0 {
		t.Fatalf("post-burst survivors after %d cycles: cliwrap-agent=%d, cat=%d (expected both 0)",
			N, agentSurvivors, catSurvivors)
	}
}
