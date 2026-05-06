// SPDX-License-Identifier: Apache-2.0

// TestBurst_SpawnStop_NoLeak is the CW-G3 regression test (RED). It exercises
// burst spawn/terminate via the public cliwrap API and asserts that the host
// does not leak processes. Today this test FAILS — the leak is real.
//
// Safety: this test deliberately drives the host toward kern.maxproc
// exhaustion (the very state CW-G3 produces). Multiple gates protect the
// developer's machine. See spec §"Test Strategy → Test safety constraints".
//
// Spec: docs/superpowers/specs/2026-05-04-CW-G3-supervision-leak-design.md
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
const (
	burstDefaultN       = 50
	burstMaxN           = 100 // hard cap. cli-wrapper should never need >100 concurrent.
	burstAgentPreflight = 10  // skip if host already has >10 cliwrap-agent processes alive
	burstAgentCeiling   = 30  // mid-loop abort if cliwrap-agent count exceeds this
	burstAbortDelta     = 10  // mid-loop abort if pidCount delta from baseline exceeds this (primary leak signal)
	burstCheckEvery     = 5   // check cadence
)

// pidCount returns the number of running processes visible to the test host.
// It remains a useful leak signal because the leak window briefly inflates
// total system pidCount (children mid-exit) even when steady-state cliwrap-agent
// count is low.
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
	t.Cleanup(func() { killCliwrapAgentsExcept(baselineAgentPIDs) })

	if len(baselineAgentPIDs) > burstAgentPreflight {
		t.Skipf("host already has %d cliwrap-agent processes (>%d); too loaded for safe burst test",
			len(baselineAgentPIDs), burstAgentPreflight)
	}
	baseline := pidCount(t)

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
			cur := pidCount(t)
			agentCount := len(captureCliwrapAgentPIDs())
			if agentCount > burstAgentCeiling {
				t.Fatalf("ABORT iter %d: cliwrap-agent process count=%d > ceiling=%d; abort to protect host",
					i+1, agentCount, burstAgentCeiling)
			}
			if cur-baseline > burstAbortDelta {
				t.Fatalf("ABORT iter %d: pidCount delta=%d > %d; leak detected (CW-G3 fix should eliminate this)",
					i+1, cur-baseline, burstAbortDelta)
			}
		}
	}

	// Allow brief reaper drain.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if pidCount(t)-baseline <= 5 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if delta := pidCount(t) - baseline; delta > 5 {
		t.Fatalf("post-burst delta=%d > 5 after %d cycles", delta, N)
	}
	if err := exec.Command("/bin/true").Run(); err != nil {
		t.Fatalf("host fork failed after burst: %v", err)
	}
}
