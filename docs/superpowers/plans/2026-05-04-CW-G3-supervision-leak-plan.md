# CW-G3 Supervision Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate the cliwrap-agent + child cleanup leak that occurs under burst spawn/terminate by aligning the host → agent → child shutdown budgets and making the agent's `Run()` block on active-runner drain. After this plan lands, ai-m's `BENCH_RUNS=5 go run ./bench` runs to completion without exhausting macOS `kern.maxproc`, and a new burst chaos test demonstrates ≥200 spawn/stop cycles with bounded process growth.

**Architecture:** Three coupled changes plus a regression test.
1. New `Dispatcher.Drain(ctx)` returns when all active runners have completed `cmd.Wait()`.
2. `agent.Run()` calls `Drain` between `<-ctx.Done()` and `conn.Close()`.
3. Default `Spec.StopTimeout` lowered from 5 s to 2 s so the cascade fits inside the host's 3 s `AgentHandle.Close` grace.

**Tech Stack:** Go 1.22+, `internal/agent`, `test/chaos` (no new dependencies).

**Spec:** `docs/superpowers/specs/2026-05-04-CW-G3-supervision-leak-design.md`

**Out of scope (deferred to CW-G4):** persistent agent across host crash, reattach, daemon-mode session model.

---

## Project Conventions (apply to EVERY task)

- **DCO sign-off mandatory**: every `git commit` uses `-s`. CI rejects unsigned commits. Forgotten sign-offs are recoverable via `git rebase HEAD~N --exec "git commit --amend --no-edit -s"`.
- **Conventional Commits**: `fix:`, `refactor:`, `test:`, `docs:`, `chore:` with optional scope. Subject ≤72 chars.
- **Forbidden trailers**: never `Co-Authored-By:` or "Generated with Claude" lines.
- **Test naming**: `TestSubject_Behavior` (underscore).
- **Goroutine hygiene**: every test package that spawns goroutines includes `TestMain` with `goleak.VerifyTestMain(m)`. `internal/agent` already has one. `test/chaos` already has one.
- **errcheck**: bare `defer X(...)` on a function that returns an error fails lint. Use `defer func() { _ = h.Close() }()`.
- **TDD discipline**: every task starts with a failing test (`go test -run NewTestName ./...` returns FAIL), then minimal code to GREEN, then refactor.

---

## File Structure

| Path | Action | Responsibility |
|---|---|---|
| `internal/agent/dispatcher.go` | Modify | Add `Drain(ctx)`; track active-runner waitgroup |
| `internal/agent/dispatcher_test.go` | Modify | New tests for Drain semantics |
| `internal/agent/main.go` | Modify | Call `d.Drain(drainCtx)` between `<-ctx.Done()` and `conn.Close` |
| `internal/agent/runner.go` | Modify | Lower default `StopTimeout` to 2 s; cross-reference spec in comment |
| `internal/agent/runner_test.go` | Modify | Add guard test for default StopTimeout |
| `test/chaos/burst_spawn_test.go` | Create | Burst spawn/stop regression test |
| `CHANGELOG.md` | Modify | Entry under `[Unreleased]` — Reliability — CW-G3 fix |

---

## Task 1: Verify the leak with a minimal failing chaos test

This task lands the regression test FIRST (pure RED). The implementation tasks then turn it GREEN.

**Files:**
- Create: `test/chaos/burst_spawn_test.go`

- [ ] **Step 1: Read the spec**

Read `docs/superpowers/specs/2026-05-04-CW-G3-supervision-leak-design.md` §"Change 4 — burst regression test" for the exact assertions and rationale.

- [ ] **Step 2: Write the failing test**

`test/chaos/burst_spawn_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package chaos

import (
    "context"
    "fmt"
    "os/exec"
    "testing"
    "time"

    "github.com/0xmhha/cli-wrapper/pkg/cliwrap"
    "go.uber.org/goleak"
)

// pidCount returns the number of processes visible to the test host.
// Implementation: shell out to `ps -A | wc -l` (portable on macOS + Linux).
func pidCount(t *testing.T) int {
    t.Helper()
    out, err := exec.Command("sh", "-c", "ps -A | wc -l").Output()
    if err != nil { t.Fatalf("pidCount: %v", err) }
    n := 0
    for _, b := range out {
        if b >= '0' && b <= '9' {
            n = n*10 + int(b-'0')
        }
    }
    return n
}

func TestBurst_SpawnStop_NoLeak(t *testing.T) {
    if testing.Short() { t.Skip("burst test takes ~10s") }
    defer goleak.VerifyNone(t)

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    baselinePIDs := pidCount(t)

    mgr, err := newTestManagerForBurst(t)   // helper from testhelpers
    if err != nil { t.Fatalf("manager: %v", err) }
    defer func() { _ = mgr.Close() }()

    const N = 200
    for i := 0; i < N; i++ {
        h, err := mgr.Spawn(ctx, fmt.Sprintf("burst-%d", i), cliwrap.Spec{
            Argv: []string{"/bin/cat"},
            PTY:  true,
        })
        if err != nil { t.Fatalf("spawn %d: %v", i, err) }
        if err := h.Stop(ctx); err != nil { t.Fatalf("stop %d: %v", i, err) }
    }

    // Allow brief reaper drain — no longer than 2 s.
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

`newTestManagerForBurst` is the existing test helper in `test/chaos/testhelpers.go` (or equivalent). If absent, copy the pattern from `test/chaos/pty_agent_crash_test.go` setup. **Do not invent a new helper API; reuse what's there.**

- [ ] **Step 3: Run to fail (and confirm we are observing the actual leak)**

```bash
cd harness/cli-wrapper
go test -run TestBurst_SpawnStop_NoLeak ./test/chaos/ -v
```

Expected RED: either the test fails on `process count grew by N (expected ≤5)`, OR it fails on `host fork failed after burst`. Either failure mode is acceptable evidence of the leak. Capture the exact failure output in the commit body for Task 1.

If the test passes (system is somehow not exhibiting the leak today), that's a signal to investigate before proceeding — DO NOT mark Task 1 done by passing-on-first-try. Report back; the fix may be wrong target.

- [ ] **Step 4: Commit**

```bash
git add test/chaos/burst_spawn_test.go
git commit -s -m "test(chaos): burst spawn/stop regression test (RED for CW-G3)

Spawns 200 cliwrap PTY children in tight succession via
Manager.Spawn + ProcessHandle.Stop and asserts:
- Process count returns to within +5 of baseline within 2s of completion.
- Host can still fork after the burst.

Currently FAILS on macOS (Apple M2): process count balloons by
N+ as orphaned children pile up before launchd reaps them, and
post-burst /bin/true forks fail with EAGAIN.

This is the regression test for CW-G3. Tasks 2-5 turn it GREEN.

Spec: docs/superpowers/specs/2026-05-04-CW-G3-supervision-leak-design.md"
```

---

## Task 2: Add `Dispatcher.Drain(ctx)` with active-runner tracking

**Files:**
- Modify: `internal/agent/dispatcher.go`
- Modify: `internal/agent/dispatcher_test.go`

- [ ] **Step 1: Failing test**

`internal/agent/dispatcher_test.go` (extend; existing test file):

```go
func TestDispatcher_DrainWaitsForActiveRunners(t *testing.T) {
    defer goleak.VerifyNone(t)

    d := NewDispatcher(nil) // nil conn ok for this test; no IPC traffic

    // Simulate 3 active runners each holding a "running" token for 200ms.
    var releases []func()
    for i := 0; i < 3; i++ {
        release := d.markRunnerActive() // NEW package-private helper
        releases = append(releases, release)
    }

    drainDone := make(chan error, 1)
    go func() {
        ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
        defer cancel()
        drainDone <- d.Drain(ctx)
    }()

    // Drain must NOT have returned yet.
    select {
    case err := <-drainDone:
        t.Fatalf("Drain returned before runners released: err=%v", err)
    case <-time.After(50 * time.Millisecond):
    }

    // Release all runners.
    for _, r := range releases { r() }

    select {
    case err := <-drainDone:
        if err != nil { t.Fatalf("Drain returned err: %v", err) }
    case <-time.After(500 * time.Millisecond):
        t.Fatalf("Drain did not return after runners released")
    }
}

func TestDispatcher_DrainRespectsContextDeadline(t *testing.T) {
    defer goleak.VerifyNone(t)

    d := NewDispatcher(nil)
    release := d.markRunnerActive()
    defer release()

    ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
    defer cancel()

    err := d.Drain(ctx)
    if err == nil {
        t.Fatalf("Drain returned nil; expected deadline-exceeded error")
    }
    if !errors.Is(err, context.DeadlineExceeded) {
        t.Fatalf("Drain err = %v; expected DeadlineExceeded", err)
    }
}
```

- [ ] **Step 2: Run to fail**

```bash
go test -run TestDispatcher_Drain ./internal/agent/ -v
```

Expected RED: undefined `d.Drain` and `d.markRunnerActive`.

- [ ] **Step 3: Implement**

In `internal/agent/dispatcher.go`:

```go
// Add to Dispatcher struct:
//   activeRunners sync.WaitGroup

// markRunnerActive registers an active runner with the dispatcher's drain
// tracker. The returned release() must be called exactly once when the
// runner has finished its cleanup (including cmd.Wait on its child).
//
// This is package-private; runners call it from their lifecycle.
func (d *Dispatcher) markRunnerActive() (release func()) {
    d.activeRunners.Add(1)
    var once sync.Once
    return func() { once.Do(d.activeRunners.Done) }
}

// Drain blocks until all runners that called markRunnerActive have
// released their tokens, or ctx is canceled. Returns ctx.Err() on timeout.
//
// CW-G3: this is called from agent.Run after <-ctx.Done() so the agent
// process does not exit while runner goroutines are still reaping their
// children. See docs/superpowers/specs/2026-05-04-CW-G3-supervision-leak-design.md
func (d *Dispatcher) Drain(ctx context.Context) error {
    done := make(chan struct{})
    go func() {
        d.activeRunners.Wait()
        close(done)
    }()
    select {
    case <-done:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}
```

- [ ] **Step 4: Wire runners**

In `internal/agent/runner.go` (both the regular and PTY paths), wrap the runner's main goroutine body so it acquires + releases the active-runner token. Conceptually:

```go
release := d.markRunnerActive()
defer release()
// ... existing runner body including the eventual cmd.Wait() ...
```

The exact insertion point is "the goroutine that ultimately blocks on `cmd.Wait()`". The release must occur AFTER `cmd.Wait()` returns, not before.

If the runner has multiple early-return paths (error during setup), each must release. The `defer release()` pattern handles this naturally.

- [ ] **Step 5: Re-run unit tests**

```bash
go test -run TestDispatcher_Drain ./internal/agent/ -v -race
go test ./internal/agent/ -race    # full package, no regression
```

GREEN required.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/dispatcher.go internal/agent/dispatcher_test.go internal/agent/runner.go
git commit -s -m "feat(agent): Dispatcher.Drain blocks on active-runner WaitGroup (CW-G3)

Adds an internal active-runner waitgroup so the agent process can
defer its own exit until all runner goroutines have completed
cmd.Wait() on their supervised children.

Drain(ctx) returns when the waitgroup hits zero, or ctx.Err() on
deadline. Runners acquire/release tokens around their existing
cleanup path.

This is the foundation for CW-G3's fix: it makes 'agent has stopped'
include 'all children have been reaped'. Task 3 wires it into
agent.Run.

Spec: docs/superpowers/specs/2026-05-04-CW-G3-supervision-leak-design.md"
```

---

## Task 3: Wire Drain into agent.Run

**Files:**
- Modify: `internal/agent/main.go`

- [ ] **Step 1: Failing test (integration-style)**

This change is small enough that the existing burst chaos test (Task 1) is the failing test. Confirm before editing:

```bash
go test -run TestBurst_SpawnStop_NoLeak ./test/chaos/ -v
```

Still RED (process count grows or post-burst fork fails).

- [ ] **Step 2: Implement**

`internal/agent/main.go::Run` becomes:

```go
<-ctx.Done()

// CW-G3: block until active runners have completed cmd.Wait on their
// children, before letting the agent process exit. The drain budget is
// strictly less than the host's AgentHandle.Close 3s grace so we can
// still complete conn.Close + return on time.
//
// Spec: docs/superpowers/specs/2026-05-04-CW-G3-supervision-leak-design.md
drainCtx, drainCancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
_ = d.Drain(drainCtx)
drainCancel()

shutdownCtx, cancel := ctxWithTimeoutSeconds(5)
defer cancel()
_ = conn.Close(shutdownCtx)
return nil
```

Note: the drain timeout is hardcoded to 2.5 s here, mirroring the spec's budget table. Future configurability (passing host grace via env var) is a follow-up.

- [ ] **Step 3: Re-run**

```bash
go test ./internal/agent/ -race           # no unit regression
go test -run TestBurst_SpawnStop_NoLeak ./test/chaos/ -v
```

The chaos test is **expected to still fail** at this point — Drain alone doesn't help if the runner's StopTimeout (5 s) is longer than the host's grace (3 s); the agent now waits for runners but the runners themselves are too slow. Task 4 fixes that.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/main.go
git commit -s -m "feat(agent): Run drains active runners before conn.Close (CW-G3)

Inserts d.Drain(2.5s) between <-ctx.Done() and conn.Close so the
agent process does not exit while runner goroutines are still mid
cmd.Wait. The 2.5s budget fits inside host AgentHandle.Close's 3s
grace with margin for IPC close.

Effect on its own is partial: runners still use a 5s child stop
timeout (longer than host's grace), so under burst the chaos test
still fails. Task 4 lowers the runner StopTimeout default to 2s
to complete the fix.

Spec: docs/superpowers/specs/2026-05-04-CW-G3-supervision-leak-design.md"
```

---

## Task 4: Lower default Spec.StopTimeout to 2s

**Files:**
- Modify: `internal/agent/runner.go`
- Modify: `internal/agent/runner_test.go`

- [ ] **Step 1: Failing test**

`internal/agent/runner_test.go` (extend):

```go
func TestRunner_DefaultStopTimeoutIs2s(t *testing.T) {
    // Guard against silent regression of CW-G3 budget cascade.
    // Spec: 2.0s child grace must fit inside agent's 2.5s drain budget,
    // which fits inside host's 3.0s AgentHandle.Close grace.
    if defaultStopTimeout != 2*time.Second {
        t.Fatalf("defaultStopTimeout = %v; want 2s (CW-G3)", defaultStopTimeout)
    }
}
```

- [ ] **Step 2: Run to fail**

```bash
go test -run TestRunner_DefaultStopTimeoutIs2s ./internal/agent/ -v
```

Either fails because `defaultStopTimeout` doesn't exist as a named constant yet, or fails because it equals 5 s.

- [ ] **Step 3: Extract the magic number to a named constant**

In `internal/agent/runner.go`:

```go
// defaultStopTimeout is the time a runner gives a child to cooperatively
// exit on SIGTERM before SIGKILLing it. Must be strictly less than
// agent.Run's drain budget (2.5s), which must be strictly less than
// host AgentHandle.Close's grace (3.0s). See CW-G3 spec.
const defaultStopTimeout = 2 * time.Second
```

Update the runner code:
```go
// was:
timeout := spec.StopTimeout
if timeout <= 0 {
    timeout = 5 * time.Second
}
// becomes:
timeout := spec.StopTimeout
if timeout <= 0 {
    timeout = defaultStopTimeout
}
```

Apply to both the regular runner and the PTY runner if both have the magic number.

- [ ] **Step 4: Re-run**

```bash
go test -run TestRunner_DefaultStopTimeoutIs2s ./internal/agent/ -v -race    # green
go test ./internal/agent/ -race                                              # no regression
go test -run TestBurst_SpawnStop_NoLeak ./test/chaos/ -v                     # expected GREEN now
```

The burst chaos test should now pass: agent Drain waits for runners, runners use 2 s budget, total fits in host's 3 s, no orphans.

If the chaos test STILL fails at this point — STOP, do not proceed. Either the leak has another mechanism not covered by the spec, or one of Tasks 2–4 has a bug. Investigate before continuing.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/runner.go internal/agent/runner_test.go
git commit -s -m "fix(agent): default child StopTimeout 5s -> 2s (CW-G3)

The 5s default was conservative for unknown CLIs but exceeded the
host's 3s AgentHandle.Close grace, guaranteeing that under any
slow-shutdown child the host SIGKILLs the agent before the runner
finishes cmd.Wait, leaving the child orphaned to launchd.

2s aligns with the CW-G3 budget cascade:
  child SIGTERM -> 2s grace -> child reap < 2.5s drain < 3.0s host grace

Real CLI tools (bash, cat, claude code) handle SIGTERM in <100ms;
2s remains generous. Callers needing longer pass an explicit
Spec.StopTimeout per spawn.

The new burst chaos test now passes: 200 spawn/stop cycles complete
with bounded process growth and no fork failures.

Spec: docs/superpowers/specs/2026-05-04-CW-G3-supervision-leak-design.md"
```

---

## Task 5: CHANGELOG entry

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Read existing CHANGELOG format**

```bash
head -40 CHANGELOG.md
```

Confirm the `[Unreleased]` section structure (likely with `### Reliability` or similar subheaders).

- [ ] **Step 2: Add entry**

Under `[Unreleased]` → `### Reliability` (create subheader if absent):

```markdown
- **CW-G3** Fix supervision cleanup leak under burst spawn/terminate. Agent now drains active runners before exiting, and the default child stop timeout (`Spec.StopTimeout` when unset) is lowered from 5 s to 2 s so the cascade fits inside the host's 3 s grace. Without this fix, ~500 spawn/stop cycles in <60 s on macOS exhausted `kern.maxproc`.
```

- [ ] **Step 3: Commit**

```bash
git add CHANGELOG.md
git commit -s -m "docs(changelog): record CW-G3 supervision cleanup fix"
```

---

## Task 6: Final acceptance — full test suite + downstream validation

This task does no code edits; it's the integration verification before declaring CW-G3 RESOLVED.

- [ ] **Step 1: cli-wrapper full suite**

```bash
cd harness/cli-wrapper
go test ./... -race
go test -run TestBurst ./test/chaos/ -v       # explicit re-run, no -short
```

All green required.

- [ ] **Step 2: Reinstall agent + downstream ai-m bench**

```bash
go install ./cmd/cliwrap-agent
cd ../../ai-m
PATH="/Users/wm-it-22-00661/.gvm/pkgsets/go1.23.12/global/bin:$PATH" \
    BENCH_RUNS=5 go run ./bench
```

Expected: bench completes for all 5 scenarios on both backends without `fork failed: resource temporarily unavailable`. Per-metric medians written to `bench/last-run.{json,md}` (now gitignored).

- [ ] **Step 3: Acceptance gate**

```bash
go build -o /tmp/aim-bench-gate ./bench/gate
/tmp/aim-bench-gate -baseline=bench/baseline.json -current=bench/last-run.json
```

Gate result depends on whether `idle_cpu` is durably under threshold at the median. If yes, CW-G2 may close in the same session as CW-G3. If no, CW-G2 Track B (cli-wrapper-side diagnosis) is the natural next step.

- [ ] **Step 4: Report results back to caller**

Report:
- All cli-wrapper tests passing (with `-race`).
- Burst chaos test process-count delta on success run (e.g. "delta = 2 after 200 cycles").
- ai-m gate result (PASS/FAIL per rule, with median values from `BENCH_RUNS=5`).
- Whether CW-G2 closes incidentally.

This is the trigger to update `STATUS.md`, `REMAINING-WORK.md`, and `PHASE2-ROADMAP.md` (in the orchestrating session, not by this subagent).

---

## Verification matrix

| Test | Before fix | After fix |
|---|---|---|
| `go test ./internal/agent/ -race` | PASS | PASS (with new dispatcher tests added) |
| `go test ./internal/ipc/ -race` | PASS | PASS (untouched) |
| `go test ./internal/controller/ -race` | PASS | PASS (untouched) |
| `go test ./test/chaos/ -race -short` | PASS (skips burst) | PASS (skips burst) |
| `go test -run TestBurst ./test/chaos/ -v` | FAIL (process leak) | PASS |
| `BENCH_RUNS=5 go run ./bench` (ai-m) | fails mid-run with EAGAIN | completes; medians written |

---

## Rollback plan

Each task is one commit. To rollback:

- Tasks 5–6 are docs/test only; safe to leave or revert independently.
- Task 4 alone is reversible by `git revert <sha>` (reverts to 5 s default).
- Tasks 2 + 3 should be reverted together (Drain wiring without StopTimeout fix is asymmetric but safe).
- Task 1 (the chaos test) is reversible independently; reverting it just removes the regression guard.

No database migrations, no on-disk format changes.
