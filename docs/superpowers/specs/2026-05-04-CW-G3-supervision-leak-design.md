# CW-G3 — Supervision Cleanup Under Burst Spawn/Terminate Design Specification

**Date:** 2026-05-04
**Status:** Draft — promoted from `../../ai-m/docs/superpowers/REMAINING-WORK.md` §CW-G3
**Owner:** TBD
**Related:**
- `docs/superpowers/specs/2026-04-29-CW-G1-agent-disconnect-design.md` (sibling supervision gap)
- `../../ai-m/docs/superpowers/specs/2026-05-04-CW-G2-idle-cpu-design.md` (the bench scenario that surfaced this)
- `../../ai-m/docs/superpowers/REMAINING-WORK.md` §CW-G3

## Overview

When `pkg/cliwrap.ProcessHandle.Stop` is called repeatedly under high frequency (e.g. ~100 stop/start cycles per second on Apple M2), the supervised child PTY process is not consistently reaped before the agent process itself terminates. Each leaked child reparents to `launchd` and lingers as a zombie until the kernel reaper drains the queue. Sustained burst (~500 cycles in <60s) exhausts macOS `kern.maxproc` (default 4000) within the test host's session, causing every subsequent `fork(2)` system-wide to fail with `EAGAIN: resource temporarily unavailable`.

The bug surfaced 2026-05-04 while attempting to gather a 5-run median for the cliwrap idle-CPU acceptance gate (CW-G2 Track A2). It is not a bench-only artifact: real users running ai-m sessions that frequently spawn short-lived `cliwrap` sessions can hit the same wall after enough cycles.

This is a direct contradiction of cli-wrapper's stated "production-grade supervision" goal. Stop must mean stopped — including transitive children — by the time the API call returns.

### Goals

- `pkg/cliwrap.ProcessHandle.Stop(ctx)` returns only after the supervised child PTY process has been reaped (`wait4` returned), not merely signaled.
- Agent's `Run()` does not return until all active child runners have completed cleanup, or a hard deadline elapses.
- Agent-side child stop budget is strictly less than host-side agent stop budget, so the chain `host → agent → child` reliably completes within `host.AgentHandle.Close` 3s grace.
- A burst regression test (`test/chaos/burst_spawn_test.go`, new) spawns ≥200 `Manager.Spawn` + `ProcessHandle.Stop` cycles in tight succession, and asserts:
  - Zero zombies on completion (`ps -A | grep '<defunct>'` filtered for cliwrap-agent / fixture children produces 0 rows).
  - Zero unbounded process count growth (final pid count − baseline pid count ≤ a small constant, e.g. 5).
  - No `fork failed` errors during or immediately after the burst (`os.StartProcess` of a sentinel test fixture succeeds).
- Existing unit, integration, and chaos tests pass with `-race` clean.

### Non-goals

- **Persistent agent / reattach across host crash.** Tracked separately as CW-G4. CW-G3 is strictly about the **foreground-mode** lifecycle: when the host issues Stop (or the host itself terminates), the entire process tree shuts down cleanly. The persistent / detach / reattach model is a feature spec, not a bug fix.
- **Cross-platform PR_SET_PDEATHSIG-style kernel hooks.** macOS does not provide one and CW-G3 must not regress macOS-first support. The fix is purely cooperative cleanup at the cli-wrapper level.
- **Throttling at the spawn API.** A `Manager.Spawn` rate limiter is a possible CW-G3 follow-up if the cooperative fix proves insufficient under pathological loads, but it is not the primary fix.
- **Lifecycle-event API redesign.** The existing `Manager.Events()` + `event.Bus` infrastructure already covers the user-visible "child died" notification (D1 from the design discussion). CW-G3 may enrich the `Stopped` event with a `Reason` field if it falls out naturally, but no new event surface is introduced.
- **Windows.** cli-wrapper is POSIX-only at v0.x.

### Platform Support

| Platform | Status |
|---|---|
| Linux x86_64 / arm64 | First-class |
| macOS x86_64 / arm64 | First-class (primary repro platform) |
| Windows | Out of scope |

---

## Current Behavior (verified 2026-05-04 against `main` at `f2daea8`)

The teardown chain has four layers, each with its own timeout. Their misalignment is the root cause.

### Layer 1 — host: `internal/supervise/spawner.go::AgentHandle.Close`
```go
_ = h.Process.Signal(syscall.SIGTERM)
done := make(chan struct{})
go func() { _, _ = h.Process.Wait(); close(done) }()
select {
case <-done:
case <-time.After(3 * time.Second):
    _ = h.Process.Kill()      // SIGKILL
    <-done
}
```
Agent gets **3 s** to exit on its own; otherwise force-killed.

### Layer 2 — agent main: `internal/agent/main.go::Run`
```go
<-ctx.Done()                  // SIGTERM → ctx canceled (via signal.NotifyContext in cmd/)
shutdownCtx, cancel := ctxWithTimeoutSeconds(5)
defer cancel()
_ = conn.Close(shutdownCtx)
return nil
```
On ctx cancel, agent closes the IPC connection (5 s budget) and returns. **It does not wait for active runners.** Any in-flight `cmd.Wait()` in a runner goroutine is killed when the process exits.

### Layer 3 — runner escalation: `internal/agent/runner.go::~line 145`
```go
select {
case <-ctx.Done():
    _ = cmd.Process.Signal(syscall.SIGTERM)
    timeout := spec.StopTimeout      // default 5 s
    if timeout <= 0 { timeout = 5 * time.Second }
    select {
    case <-stopSignal:
    case <-time.After(timeout):
        _ = cmd.Process.Kill()
    }
case <-stopSignal:
}
```
Runner gives child **5 s** to die after SIGTERM, then SIGKILLs.

### Layer 4 — child isolation: `internal/agent/runner_pty.go:50`
```go
SysProcAttr: &syscall.SysProcAttr{Setsid: true, ...}
```
Child is in its own session/process group. **If the agent dies, the kernel reparents the child to PID 1 (`launchd` on macOS), where it lingers until launchd reaps it.**

### How the misalignment leaks

```
t=0 ms     host SIGTERMs agent
t=0 ms     agent ctx canceled, runner sends SIGTERM to child
t=0 ms     child begins cooperative shutdown (may take up to 5 s, e.g. shells flushing)
t=3000 ms  host SIGKILLs agent (3 s grace expired)
              ↑ agent dies here
              ↑ runner goroutine dies, no wait4 on child
              ↑ child reparented to launchd
t=?        launchd reaps child (best-effort, queued)
```
Under sustained burst (~100 cycles/s × multi-run bench), launchd queue length grows faster than its reaper drains. Process count climbs until `kern.maxproc` is hit and the host can no longer fork.

---

## Proposed Design

The fix has three coupled changes, each minimal and independently testable.

### Change 1 — agent main waits for active runners

`internal/agent/dispatcher.go` (or `internal/agent/main.go` if dispatcher already tracks runners):

Add a synchronous `Drain(ctx context.Context) error` that returns when all active runners have completed `cmd.Wait()` on their children, or when `ctx` deadline elapses.

`internal/agent/main.go::Run` becomes:
```go
<-ctx.Done()

drainCtx, cancel := ctxWithTimeout(2500 * time.Millisecond)   // see Change 3
defer cancel()
_ = d.Drain(drainCtx)        // NEW: block until children reaped (or budget exhausted)

shutdownCtx, cancelShutdown := ctxWithTimeoutSeconds(5)
defer cancelShutdown()
_ = conn.Close(shutdownCtx)
return nil
```

Reasoning: the agent must not return from `Run` (which lets the process exit) while runner goroutines are still mid-reap. A bounded `Drain` keeps the contract that agent termination is bounded in time.

### Change 2 — child stop budget tightened

In `internal/agent/runner.go`, the default `StopTimeout` (currently 5 s) becomes **2 s**. Existing call sites that pass an explicit `spec.StopTimeout` are unaffected.

Reasoning: agent-side child grace must be strictly less than host-side agent grace. With host = 3 s, choosing agent = 2 s leaves a 1 s margin for the IPC close + drain bookkeeping in Change 1. Children that take >2 s to handle SIGTERM are SIGKILL'd by the runner (already implemented), but **the runner now successfully reaches `cmd.Wait()` before agent exits**.

Real CLI tools used (`bash`, `cat`, `claude code`) handle SIGTERM in <100 ms in the steady case. The 5 s default was conservative for unknown CLIs; 2 s remains generous.

If a user explicitly needs longer (e.g. for a CLI with a multi-second shutdown procedure), they pass a larger `Spec.StopTimeout` and **also configure host-side `AgentHandle.Close` budget** (a follow-up: today the host budget is hardcoded). Cross-layer budget configuration is filed as a doc task, not implemented in CW-G3.

### Change 3 — drain budget < host kill budget

The `Drain(ctx)` deadline in Change 1 is **2.5 s**, leaving 0.5 s headroom inside the host's 3 s grace.

Time accounting:
| Step | Budget | Accumulator |
|---|---:|---:|
| Runner SIGTERM child → SIGKILL fallback | 2.0 s | 2.0 s |
| Runner `cmd.Wait()` on a SIGKILLed child | <0.05 s typical | 2.05 s |
| Drain bookkeeping (waitgroup + cancel signal) | <0.1 s | 2.15 s |
| Margin | 0.35 s | 2.5 s |
| Host SIGKILL agent | — | 3.0 s |

This is tight but bounded. Crucially, the host SIGKILL fallback at 3.0 s is unchanged; it remains the safety net for a deadlocked agent. The new design just makes that fallback rarely needed.

### Change 4 — burst regression test

`test/chaos/burst_spawn_test.go` (new file):

```go
func TestBurst_SpawnStop_NoLeak(t *testing.T) {
    if testing.Short() { t.Skip("burst test takes ~10s") }
    defer goleak.VerifyNone(t)

    baselinePIDs := pidCount(t)

    mgr := newTestManager(t)
    defer mgr.Close()

    const N = 200
    for i := 0; i < N; i++ {
        h, err := mgr.Spawn(ctx, fmt.Sprintf("burst-%d", i), Spec{
            Argv: []string{"/bin/cat"},
            PTY:  true,
        })
        if err != nil { t.Fatalf("spawn %d: %v", i, err) }
        if err := h.Stop(ctx); err != nil { t.Fatalf("stop %d: %v", i, err) }
    }

    // Allow brief reaper drain — no longer than test budget.
    deadline := time.Now().Add(2 * time.Second)
    for time.Now().Before(deadline) {
        if pidCount(t)-baselinePIDs <= 5 { break }
        time.Sleep(50 * time.Millisecond)
    }

    delta := pidCount(t) - baselinePIDs
    if delta > 5 {
        t.Fatalf("process count grew by %d after %d spawn/stop cycles (expected ≤5)", delta, N)
    }

    // Sanity: host can still fork.
    if err := exec.Command("/bin/true").Run(); err != nil {
        t.Fatalf("host fork failed after burst: %v", err)
    }
}
```

`pidCount(t)` is a small helper that runs `ps -A | wc -l` and returns an int.

The test is gated on `!testing.Short()` because it intentionally takes 10 s and burns ~200 cliwrap-agent + child cycles. CI may run it in a separate "chaos" lane.

---

## Edge Cases

| Case | Behavior | Notes |
|---|---|---|
| Child already exited before Stop | `cmd.Wait()` returns immediately, drain is fast | Existing behavior, preserved |
| Child catches SIGTERM, ignores it | Runner SIGKILLs at 2 s, `cmd.Wait()` returns instantly | Was: SIGKILL at 5 s but agent already SIGKILL'd by host |
| Child is in uninterruptible kernel state (D-state) | Runner SIGKILL doesn't help; `cmd.Wait()` blocks; drain hits 2.5 s deadline; host SIGKILL agent at 3 s; child orphan → launchd | Existing failure mode preserved as last resort. D-state is rare for CLI workloads. |
| Stop called while no child active (e.g. between Run and Stop) | Drain is a no-op | Idempotent |
| Multiple Stop calls in flight (concurrent) | First Stop completes the cleanup; subsequent Stops are no-ops via existing `closed` flag | Existing behavior |
| Agent SIGKILL'd directly (not SIGTERM first) | No graceful path; child orphan; reaped by launchd over time | Same as today. CW-G3 does not promise to handle SIGKILL of agent; that is a separate scenario covered by CW-G1-style detection on host. |
| Host crashes (agent loses parent) | Agent ctx not canceled (no signal received); agent continues; eventually IPC EOF on socket → existing CW-G1 wiring handles | Out of scope per non-goals; CW-G4 territory |

---

## Test Strategy

1. **Unit** (new): `internal/agent/dispatcher_test.go::TestDispatcher_DrainWaitsForActiveRunners` — start 3 fake runners, signal context, assert `Drain` returns only after all 3 have completed; assert Drain returns within deadline if a runner hangs.
2. **Unit** (new): `internal/agent/runner_test.go::TestRunner_DefaultStopTimeoutIs2s` — guard against silent regression of Change 2.
3. **Integration** (modified): existing `test/integration/pty_*_test.go` — must continue to pass; verify shorter stop budget does not break legitimate slow-shutdown CLIs (none in current tests).
4. **Chaos** (new): `test/chaos/burst_spawn_test.go::TestBurst_SpawnStop_NoLeak` — described above; the primary CW-G3 acceptance test.
5. **Bench** (downstream, ai-m): `BENCH_RUNS=5 go run ./bench` from ai-m must complete without `fork failed`. This is the original symptom and the ultimate user-visible validation.

All tests pass with `-race`.

---

## Migration

No public API changes. The default `StopTimeout` constant in `internal/agent/runner.go` changes from `5*time.Second` to `2*time.Second`. Callers passing `Spec.StopTimeout` explicitly are unaffected.

`Manager.Drain` is a new internal-package method on the agent's dispatcher; not exposed via `pkg/cliwrap`. (If host-side cleanup later wants a public `Manager.Drain(ctx)` for graceful host shutdown, that is filed as a CW-G4 concern.)

CHANGELOG entry: a "Reliability" line under `[Unreleased]`.

---

## Risks

| Risk | Mitigation |
|---|---|
| 2 s child grace too short for some real CLIs (e.g. databases doing pre-shutdown sync) | Configurable via existing `Spec.StopTimeout` per-spawn. Default change is for the bench/test-style short-lived case. Document in CHANGELOG. |
| Drain implementation introduces deadlock if a runner holds a lock that drain needs | Code-review focused on lock ordering. Drain uses a `sync.WaitGroup` only; runners signal completion via `wg.Done()` after their existing cleanup. No new locks. |
| Burst test is flaky on shared CI runners (other processes contributing to pid count) | Use `delta := after - baseline` rather than absolute count. Allow up to 5 unrelated processes. |
| Host-side `AgentHandle.Close` 3 s grace not enough on heavily loaded hosts | Out of scope here; the existing 3 s is preserved and is the same risk pre-CW-G3. If empirically insufficient, a follow-up `CW-G3.1` can make host grace configurable. |
| The 2 s / 2.5 s / 3 s budget cascade is implicit; future code edits in any layer can break the invariant silently | Add inline comment in each layer pointing to this spec. Add a TestRunner_DefaultStopTimeoutIs2s guard (Change 2 above). |

---

## Effort

**M (3–5 hrs subagent compute)** — comparable to CW-G1.

Breakdown:
- Spec/plan polish (writing this + plan): ~30 min
- Change 1 (Drain) + unit test: ~60 min
- Change 2 (StopTimeout default) + unit test: ~15 min
- Change 3 (drain budget wiring): ~15 min
- Change 4 (burst chaos test) + tuning: ~60 min
- Code review + iteration: ~30 min
- ai-m re-validation (BENCH_RUNS=5): ~30 min

---

## Out-of-spec but related work

The user-facing supervision discussion (2026-05-04) raised four desired behaviors for agent + child lifecycle:

1. ✅ **Child death → event/hook** — already covered by `Manager.Events()` + `event.Bus` (`pkg/cliwrap/manager_events.go`). CW-G3 does not change this; it may enrich `Stopped` event with a `Reason` field if convenient.
2. ✅ **Intentional termination → cascade** — addressed by CW-G3 (this spec).
3. ⏳ **Unintentional termination + no orphan** — partially addressed: when host SIGKILLs agent (or agent crashes), CW-G1's wiring detects it; CW-G3 narrows the orphan window for the cooperative case but does not close the SIGKILL-of-agent case (would require persistent agent — CW-G4).
4. ⏳ **Reattach across restart** — full CW-G4 scope.

Items #3 and #4 form the backlog item **CW-G4 — persistent session + reattach**, to be designed in a separate spec after CW-G3 lands.
