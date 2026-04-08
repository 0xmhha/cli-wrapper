# Plan Review — Discovered Issues

**Date:** 2026-04-07
**Reviewed plans:** 01 (IPC), 02 (Supervision), 03 (Resource/Sandbox), 04 (Config/CLI), 05 (Hardening)
**Constraint:** Fixes must NOT change the project's purpose or design intent. Only correct logical errors.

## Verification Summary

| Severity | Total | FIXED | PARTIAL (accepted) | FALSE_POSITIVE | Deferred |
|----------|-------|-------|--------------------|----------------|----------|
| BLOCKER  | 8     | 8     | 0                  | 0              | 0        |
| MAJOR    | 17    | 13    | 2                  | 1              | 1        |
| MINOR    | 15    | 12    | 0                  | 0              | 3        |
| **Total**| **40**| **33**| **2**              | **1**          | **4**    |

**All 8 BLOCKERs FIXED.** Remaining unfixed:
- M04 (PARTIAL) — Spiller.Enqueue brief block during Retire; documented as v1 acceptable
- M08 (PARTIAL) — Runner io.Copy deadlock with blocking sinks; contract documented
- M02 (FALSE_POSITIVE) — OnMessage race was not real
- m01, m06, m13, m15 — cosmetic/doc-only; deferred to implementation phase

## Status legend

- `UNVERIFIED` — raised by initial review, not yet double-checked
- `CONFIRMED` — verified real issue, must be fixed
- `FALSE_POSITIVE` — verification showed the initial analysis was wrong; no fix needed
- `PARTIAL` — real concern but nuanced; fix clarified or optional
- `FIXED` — fix applied to plan document

## Severity legend

- `BLOCKER` — will not compile, or will crash, or test will fail deterministically
- `MAJOR` — wrong behavior, leaks, spec deviation, non-deterministic test
- `MINOR` — works but has pitfalls, documentation gap, or future maintenance risk

---

## BLOCKER issues

### B01 — Plan 01 Task 3: frame.go import block mid-file

- **Plan:** 01 IPC Foundation
- **Task / Location:** Task 3 Step 3, "Append to `internal/ipc/frame.go`"
- **Severity:** BLOCKER
- **Status:** FIXED
- **Problem:** Task 3 Step 3 instructs the implementer to *append* a block that starts with `import (...)` to `frame.go`. In Go, `import` declarations must appear immediately after the `package` clause, before any other declarations. Appending `import` after the Task 2 constants would produce a syntax error.
- **Fix approach:** Rewrite Task 3 Step 3 to show the final merged file (or an explicit "insert imports at top, then append the rest" instruction). Move the import block to the top of the file snippet.

### B02 — Plan 01 Task 10: Outbox.Close race → send on closed channel

- **Plan:** 01
- **Task / Location:** Task 10 Step 3, `internal/ipc/outbox.go`, `Enqueue` / `Close`
- **Severity:** BLOCKER
- **Status:** FIXED
- **Problem:** `Enqueue` checks `closed` under the mutex, releases the mutex, then does `select { case o.ch <- msg: ...; default: ... }`. A concurrent `Close` acquiring the mutex, setting `closed=true`, and executing `close(o.ch)` between the Unlock and the send would cause a `send on closed channel` panic.
- **Fix approach:** Use a `done` chan as the shutdown signal and never `close(ch)`, or hold the mutex across the send (worse for throughput), or use `defer recover()` (anti-pattern). Preferred: switch to a `done` channel + select pattern.

### B03 — Plan 01 Task 12: TestSpiller_AckRetiresWALEntries has broken semantics

- **Plan:** 01
- **Task / Location:** Task 12 Step 1, `internal/ipc/spiller_test.go`, `TestSpiller_AckRetiresWALEntries`
- **Severity:** BLOCKER
- **Status:** FIXED
- **Problem:** With `Capacity=1`, enqueueing seqs 1, 2, 3 puts seq 1 in the channel and spills 2, 3 to the WAL. Calling `Ack(2)` retires seq ≤ 2, leaving WAL = {3}. The test then calls `ReplayInto(outbox)`, which tries to inject seq 3. But the outbox still contains seq 1 (never drained) and capacity is 1 → `InjectFront` fails, seq 3 is silently dropped from the replay path. Subsequent drain may or may not see seq 3 depending on whether the WAL is re-read. Test is non-deterministic.
- **Fix approach:** Drain the outbox before calling `ReplayInto`, or use capacity ≥ 2, or assert differently.

### B04 — Plan 02 Task 14: test uses invalid `ctxT` interface alias

- **Plan:** 02
- **Task / Location:** Task 14 Step 1, `internal/agent/dispatcher_test.go`, helper `shortCtx` / `newCtx`
- **Severity:** BLOCKER
- **Status:** FIXED
- **Problem:** The test defines `type ctxT = interface{ Done() <-chan struct{} }` and passes that to `ipc.Conn.Close(ctx)` which requires a full `context.Context` (with `Err()`, `Deadline()`, `Value()` methods). The interface alias is missing those methods → compilation failure.
- **Fix approach:** Remove `ctxT` alias, use `context.Context` directly, and inline `context.WithTimeout(...)` in tests.

### B05 — Plan 03 Task 3: macOS `ps` does not support `nlwp`

- **Plan:** 03
- **Task / Location:** Task 3 Step 3, `internal/resource/collector_darwin.go`, `darwinProcessCollector.Collect`
- **Severity:** BLOCKER
- **Status:** FIXED
- **Problem:** The collector shells out to `ps -o rss=,vsz=,%cpu=,nlwp=`. BSD/macOS `ps(1)` does not recognize `nlwp` — the command exits non-zero or emits the literal header, failing `TestProcessCollectorDarwin_SelfPID`.
- **Fix approach:** Either remove `nlwp` from the output spec and leave `NumThreads` as 0 on Darwin, or use an alternative column (`thcount` is Linux only too; the closest macOS equivalent requires `proc_pidinfo` via cgo, which is out of scope for v1).

### B06 — Cross-plan: import cycle between `pkg/cliwrap` and `internal/controller`

- **Plan:** 02 (root cause), 03 (surfaces)
- **Task / Location:** Plan 02 Task 17 `internal/controller/controller.go` imports `pkg/cliwrap`; Plan 02 Task 18 `pkg/cliwrap/process.go` imports `internal/controller`.
- **Severity:** BLOCKER
- **Status:** FIXED
- **Problem:** `internal/controller` uses `cliwrap.State`, `cliwrap.Spec`, `cliwrap.StateRunning`, etc. `pkg/cliwrap` constructs Controllers via `internal/controller.NewController`. That is a textbook import cycle. `go build` will fail at Plan 02 completion.
- **Fix approach:** Extract the shared types (`State`, `Spec`, `ResourceLimits`, `SandboxSpec`, `StdinMode`, `RestartPolicy`, `ExceedAction`, `Status`) into a leaf package (e.g., `internal/cwtypes` or `pkg/cliwrap/types`) imported by both `pkg/cliwrap` and `internal/controller`. Alternatively, invert the dependency by defining a controller interface inside `pkg/cliwrap` and keeping the concrete implementation in `internal/controller` without importing `pkg/cliwrap` types — but this requires more re-design. The leaf package approach is the smallest fix.

### B07 — Plan 05 Task 4: Manager watcher goroutine is not waited on shutdown

- **Plan:** 05
- **Task / Location:** Task 4 Step 3, `pkg/cliwrap/manager_events.go`, `watchLoop` + `Shutdown` modification
- **Severity:** BLOCKER
- **Status:** FIXED
- **Problem:** `startWatcherOnce` spawns `go m.watchLoop()` without `sync.WaitGroup`. `Shutdown` calls `close(m.watchStop)` but does not wait for the loop to exit. Tests wrap execution in `goleak.VerifyNone(t)` → the leftover watcher goroutine will cause `TestManager_EmitsLifecycleEvents` to fail.
- **Fix approach:** Add `watchWG sync.WaitGroup` to Manager, have `watchLoop` call `m.watchWG.Add(1)` via `Start` and `defer m.watchWG.Done()`, and `Shutdown` must `close(watchStop)` then `watchWG.Wait()`.

### B08 — Plan 05 Task 4: global `managerBuses sync.Map` leaks Manager references

- **Plan:** 05
- **Task / Location:** Task 4 Step 3, `pkg/cliwrap/manager_events.go`, package var `managerBuses`
- **Severity:** BLOCKER
- **Status:** FIXED
- **Problem:** `managerBuses` is a package-level `sync.Map` keyed by `*Manager`. Entries are never deleted. Every `NewManager` + `Events()` leaves a live pointer, preventing GC. In test suites this accumulates across every subtest and the EventBus itself is never `Close()`d.
- **Fix approach:** Store `*eventbus.Bus` as a lazy field on Manager (protected by `sync.Once`); delete the global map entirely; `Shutdown` calls `bus.Close()`.

---

## MAJOR issues

### M01 — Plan 01 Task 14: Conn.Close goroutine join loss on ctx timeout

- **Plan:** 01
- **Task:** Task 14, `internal/ipc/conn.go`, `Close`
- **Severity:** MAJOR
- **Status:** FIXED
- **Verification:** The code closes `rwc` and cancels the context *before* waiting on `done`. On `net.Pipe` and UDS, closing one end causes the reader to return immediately; the writer's `Dequeue(100ms)` plus `closed.Load()` check terminates within ~100 ms. So the "leak" is transient — goroutines exit within 100 ms after Close returns. However, `goleak.VerifyNone` running immediately after Close CAN still observe them if the 100 ms tick has not yet elapsed, causing flaky tests.
- **Problem:** When `Close(ctx)` hits the `ctx.Done()` branch it returns immediately while writer goroutine may still be in its 100 ms Dequeue window. Under fast test cleanup, `goleak.VerifyNone` can observe the still-running writer.
- **Fix approach:** Make the writer select on `c.cancelCtx.Done()` directly (in addition to Dequeue), so it exits immediately on cancel. Also, always `wg.Wait()` unconditionally in `Close` (after closing rwc) to guarantee join before return.

### M02 — Plan 01 Task 14: OnMessage race window before first frame

- **Plan:** 01
- **Task:** Task 14, `internal/ipc/conn.go`, `OnMessage` / `Start`
- **Severity:** MAJOR
- **Status:** FALSE_POSITIVE
- **Verification:** Re-reading `TestConn_EndToEnd_OverPipe`: `connB.OnMessage(...)` is called BEFORE `connA.Start()` and `connB.Start()`. The test does not exhibit the race. The API is still technically racy if a future caller calls OnMessage after Start, but the plan's test is correct.
- **Problem:** N/A — false alarm from initial review.
- **Fix approach:** No code fix required. Optional: add a doc comment to `OnMessage` recommending it be called before `Start`.

### M03 — Plan 01 Task 14: DedupTracker grows unbounded

- **Plan:** 01
- **Task:** Task 9 + Task 14, `internal/ipc/seq.go` / `conn.go`
- **Severity:** MAJOR
- **Status:** FIXED
- **Problem:** `DedupTracker` stores every observed SeqNo in a `map[uint64]struct{}` forever. For a long-running connection that leaks memory unboundedly. Spec §2.7 actually only requires tracking the highest processed SeqNo per peer (a watermark), not a full set.
- **Fix approach:** Replace with a monotonic watermark: `lastSeen uint64`. A frame is a duplicate iff `seq <= lastSeen`. Handle out-of-order replays by also allowing `seq > lastSeen` to pass and advance the watermark.

### M04 — Plan 01 Task 12: Spiller.Enqueue can block under sustained Retire

- **Plan:** 01
- **Task:** Task 12, `internal/ipc/spiller.go`, `spill` / `Ack`
- **Severity:** MAJOR
- **Status:** PARTIAL
- **Verification:** Confirmed the lock arrangement. Retire holds `s.mu` for tens of milliseconds on a large WAL (typical case: sub-millisecond). This violates the spec's "Enqueue never blocks" in the strict sense. For v1 with modest WAL sizes (< 16 MiB typical, 256 MiB max), the delay is bounded and small. A full re-architecture is out of scope for this fix.
- **Problem:** Brief blocking window in Enqueue while Retire runs. Not a "never blocks" guarantee.
- **Fix approach:** Document the contract explicitly as "Enqueue may briefly block for up to the Retire latency of the WAL". Add a v2 roadmap item for lock splitting. No code change for v1.

### M05 — Plan 01 Task 11: WAL.Retire atomicity (close-then-rename)

- **Plan:** 01
- **Task:** Task 11 Step 3, `internal/ipc/wal.go`, `Retire`
- **Severity:** MAJOR
- **Status:** FIXED
- **Verification:** The order is `close old fd → rename tmp over path → reopen`. POSIX `close` does NOT unlink the file; the file at `w.path` remains on disk between close and rename. Rename is atomic and replaces it. There is no "file absent" window externally. However, if the process crashes between `close` and `rename`, on restart the WAL still contains the old (un-retired) records — duplicates will be re-sent, which is SAFE because receivers are idempotent on SeqNo. The issue is more about suboptimal ordering than unsafety.
- **Problem:** Unconventional ordering; the canonical pattern is `rename → close`. In the current order, a mid-Retire panic may corrupt Spiller internal bookkeeping even if the file itself is fine.
- **Fix approach:** Reorder to `rename(tmp, path) → close old fd → reopen`. Small code change, no new risk.

### M06 — Plan 02 Task 14: CHILD_STARTED fires AFTER child exit

- **Plan:** 02
- **Task:** Task 14, `internal/agent/dispatcher.go`, `startChild` goroutine
- **Severity:** MAJOR
- **Status:** FIXED
- **Problem:** The dispatcher's `startChild` does `res, err := runner.Run(ctx, ...)` which blocks until the child exits, then sends `MsgChildStarted` followed by `MsgChildExited`. Spec §3.2 requires `CHILD_STARTED` to fire when `exec` succeeds, not when the child exits. The host sees both messages at once, `StartedAt` is inaccurate, and `StateRunning` is an infinitesimal flash.
- **Fix approach:** Add a `Started` callback/channel to `RunSpec` or `Runner`. Runner calls the callback after `cmd.Start()` returns successfully and before `cmd.Wait()`. Dispatcher sends `CHILD_STARTED` from inside the callback, `CHILD_EXITED` after Wait returns.

### M07 — Plan 02 Task 12: agent never reads CLIWRAP_AGENT_ID env var

- **Plan:** 02
- **Task:** Task 12, `internal/agent/main.go`, `DefaultConfig` / `Run`
- **Severity:** MAJOR
- **Status:** FIXED
- **Problem:** Plan 02 Task 15 spawner sets `cmd.Env = append(os.Environ(), "CLIWRAP_AGENT_ID="+agentID)`. But `agent.DefaultConfig()` leaves `AgentID` empty and neither `cmd/cliwrap-agent/main.go` nor `agent.Run` read `os.Getenv("CLIWRAP_AGENT_ID")`. The fallback `fmt.Sprintf("agent-%d", os.Getpid())` is always used, so the host-supplied ID is silently ignored. Logs and runtime dirs end up with PID-based names that do not match the controller's expectations.
- **Fix approach:** In `DefaultConfig`, read `os.Getenv("CLIWRAP_AGENT_ID")` and `os.Getenv("CLIWRAP_AGENT_RUNTIME")` into the config struct.

### M08 — Plan 02 Task 13: Runner io.Copy double-wait can deadlock

- **Plan:** 02
- **Task:** Task 13, `internal/agent/runner.go`, `Run`
- **Severity:** MAJOR
- **Status:** PARTIAL
- **Verification:** Confirmed the exact pattern. Default sinks are `io.Discard`, which never blocks. Plan 02's tests all use default sinks, so they pass. The deadlock ONLY materializes when Plan 05 (or a user) wires a blocking sink — e.g., a channel-backed writer that fills up. This is a latent bug triggered by later plans.
- **Problem:** Runner can deadlock on `<-copyDone` if a sink blocks indefinitely on Write.
- **Fix approach:** Document the contract — `StdoutSink`/`StderrSink` must never block indefinitely. Additionally, wait on `copyDone` with a bounded timeout (e.g., 5 seconds after child exit) and then proceed, logging a warning. Small code change, large safety gain.

### M09 — Plan 02 Task 14: stopChild timeout path leaks goroutine

- **Plan:** 02
- **Task:** Task 14, `internal/agent/dispatcher.go`, `stopChild`
- **Severity:** MAJOR
- **Status:** FIXED
- **Problem:** `stopChild` cancels the ctx, starts a waiter goroutine for `d.wg.Wait()`, and races it against `time.After(timeout)`. If the timeout wins, the waiter goroutine remains alive indefinitely (child may be stuck in `cmd.Wait`). Under `goleak.VerifyNone`, tests will fail intermittently.
- **Fix approach:** Ensure `Runner.Run` always terminates within a bounded time by sending SIGKILL when SIGTERM escalation fails; the dispatcher then always gets a clean `wg.Wait()` return.

### M10 — Plan 02 Task 15: AgentHandle.Close does not signal agent

- **Plan:** 02
- **Task:** Task 15, `internal/supervise/spawner.go`, `AgentHandle.Close`
- **Severity:** MAJOR
- **Status:** FIXED
- **Problem:** `AgentHandle.Close` closes the socket and calls `process.Wait()`. It never sends SIGTERM or SIGKILL. If the agent's main loop is stuck (e.g., dispatcher blocked in `stopChild`), `Wait()` blocks forever and the test hangs.
- **Fix approach:** After closing the socket, send SIGTERM to the agent, sleep a short grace period, then SIGKILL if still alive. Wrap `Wait()` in a bounded timeout.

### M11 — Plan 02 Task 15: Spawner uses exec.CommandContext with spawn ctx

- **Plan:** 02
- **Task:** Task 15, `internal/supervise/spawner.go`, `Spawner.Spawn`
- **Severity:** MAJOR
- **Status:** FIXED
- **Problem:** `exec.CommandContext(ctx, s.opts.AgentPath)` ties the agent's lifetime to the spawn ctx. If the caller passes a 5-second spawn timeout ctx, the agent will be killed 5 seconds after spawn, regardless of whether the managed child is still running.
- **Fix approach:** Use `exec.Command` (no ctx) so the agent's lifetime is controlled solely by `AgentHandle.Close`. Apply the spawn timeout around the handshake, not the exec.

### M12 — Plan 03 Task 4: vm.page_free_count alone underestimates available memory

- **Plan:** 03
- **Task:** Task 3 Step 3, `internal/resource/collector_darwin.go`, `darwinSystemCollector.Collect`
- **Severity:** MAJOR
- **Status:** FIXED
- **Problem:** macOS caches heavily. `vm.page_free_count` captures only the free list, not inactive/cached/speculative pages. The Evaluator compares `AvailableMemory` against `MinFreeMemory` and `AvailableRatio`. On a loaded Mac this will perpetually show low available memory and trigger spurious `SystemBudget` enforcement.
- **Fix approach:** Sum `vm.page_free_count + vm.page_speculative_count + vm.page_inactive_count + vm.page_purgeable_count` via `sysctl`, or shell out to `vm_stat` and parse. Both approaches are standard; the first is faster.

### M13 — Plan 03 Task 7: pkg/sandbox.SandboxSpec duplicates pkg/cliwrap.SandboxSpec without converter

- **Plan:** 03
- **Task:** Task 7 Step 3, `pkg/sandbox/provider.go`
- **Severity:** MAJOR
- **Status:** FIXED
- **Problem:** Plan 02 defines `cliwrap.SandboxSpec`. Plan 03 defines a separate `sandbox.SandboxSpec` with the same fields "to avoid an import cycle". The plan never ships a conversion function. Any Manager code that picks up the user's `cliwrap.SandboxSpec` and calls `provider.Prepare(ctx, sandbox.SandboxSpec, ...)` will not compile.
- **Fix approach:** Move `SandboxSpec` into the leaf types package introduced by B06, so both `pkg/cliwrap` and `pkg/sandbox` can import the same type. Alternatively, add `sandbox.FromCliwrap(cliwrap.SandboxSpec) sandbox.SandboxSpec`.

### M14 — Plan 04 Task 10: `cliwrap list` default socket path mismatches `cliwrap run`

- **Plan:** 04
- **Task:** Task 10, `cmd/cliwrap/cmd_list.go`, `managerSocketPath`
- **Severity:** MAJOR
- **Status:** FIXED
- **Problem:** `managerSocketPath` defaults to `os.TempDir()/cliwrap/manager.sock` when `--runtime-dir` is not provided. But `cliwrap run` uses `cfg.Runtime.Dir/manager.sock` (a user-configured path). Without matching `-runtime-dir`, the list command cannot find a running manager.
- **Fix approach:** Read `CLIWRAP_RUNTIME_DIR` env var written by `cliwrap run`, or record the runtime dir in a user-cache file (`$XDG_STATE_HOME/cliwrap/last-runtime-dir`) that the client commands can read.

### M15 — Plan 05 Task 2: CrashInfo test is a tautology

- **Plan:** 05
- **Task:** Task 2 Step 1, `internal/controller/crash_test.go`, `TestCrashInfo_EarliestWins`
- **Severity:** MAJOR
- **Status:** FIXED
- **Problem:** The test records a second `CrashInfo{AgentExit: 0}` and asserts `c.AgentExit == 0`. Since the initial value is 0, the assertion is satisfied regardless of whether `Record` merged anything. It does not verify Layer 4 information capture.
- **Fix approach:** Change the second Record call to `CrashInfo{AgentExit: 137, AgentSig: 9}` and assert `c.AgentExit == 137 && c.AgentSig == 9 && c.Source == CrashSourceExplicit` (so the explicit source stays primary but OS wait data is augmented).

### M16 — Plan 05 Task 6: wal_test_helpers.go is NOT excluded from production binary

- **Plan:** 05
- **Task:** Task 6 Step 2, `internal/ipc/wal_test_helpers.go`
- **Severity:** MAJOR
- **Status:** FIXED
- **Problem:** Go only excludes files whose name ends in `_test.go` from production builds. `wal_test_helpers.go` is compiled into the production `ipc` package, exposing the `WALReplayForTest` helper to anyone importing the package.
- **Fix approach:** Rename to `wal_testhelpers_test.go` (or more idiomatic: create an `export_test.go` that re-exports the helper for the same package's `_test.go` files). The chaos test would then live under `internal/ipc` and use the exported test symbol.

### M17 — Plan 05 Task 4: publishTransition emits ProcessStarted twice per lifecycle

- **Plan:** 05
- **Task:** Task 4 Step 3, `pkg/cliwrap/manager_events.go`, `publishTransition`
- **Severity:** MAJOR
- **Status:** FIXED
- **Problem:** The helper `startEvent` maps both `StateStarting` and `StateRunning` transitions to `ProcessStartedEvent` (a placeholder, as a "Starting" constructor does not exist). Subscribers observe two `TypeProcessStarted` events per process. Downstream code that counts or aggregates events will double-count.
- **Fix approach:** Add `NewProcessStarting` in `pkg/event` (and the corresponding constant), or skip emission on `StateStarting` entirely and only emit on `StateRunning`.

---

## MINOR issues

### m01 — Plan 01 Task 6: fuzz seed overlaps with Task 5 helper

- **Plan:** 01, Task 6
- **Severity:** MINOR
- **Status:** CONFIRMED
- **Problem:** Fuzz seed function `encodeFrameForFuzz` duplicates logic from `encodeFrame` in `reader_test.go`. No conflict (different names, same package) but fragmented test code.
- **Fix approach:** Have fuzz test call `encodeFrame` directly. Minor cosmetic.

### m02 — Plan 01 Task 9: NewSeqGenerator(0) underflow

- **Plan:** 01, Task 9
- **Severity:** MINOR
- **Status:** FIXED
- **Problem:** `NewSeqGenerator(0)` stores `-1` into a uint64 via `start - 1`, producing `math.MaxUint64`. The first `Next()` call returns 0. Silent wrap-around.
- **Fix approach:** Document that `start >= 1` is required, or clamp in the constructor.

### m03 — Plan 01 Task 15: benchmark Close timeout is too short

- **Plan:** 01, Task 15
- **Severity:** MINOR
- **Status:** FIXED
- **Problem:** `ctx, cancel := context.WithTimeout(context.Background(), time.Second)` for a benchmark that may enqueue millions of frames. Close may time out, leaking goroutines. Not caught by goleak (benchmarks don't use it) but inflates later measurements.
- **Fix approach:** Use a 10-second timeout or use `context.Background()` without a timeout.

### m04 — Plan 02 Task 17: `var _ = time.Second` dead code

- **Plan:** 02, Task 17
- **Severity:** MINOR
- **Status:** FIXED
- **Problem:** A dead-code line at the bottom of `controller.go` exists solely to satisfy the `time` import. The `time` package is not actually used elsewhere in the file as written.
- **Fix approach:** Remove both the dead line and the `time` import, or add a real use (e.g., `StartedAt` field, timeout handling).

### m05 — Plan 02 Task 18: processHandle.Close regresses Status to StatePending

- **Plan:** 02, Task 18
- **Severity:** MINOR
- **Status:** FIXED
- **Problem:** `processHandle.Close` sets `p.ctrl = nil`. Subsequent `Status()` calls return `Status{State: StatePending}`, which is confusing — the process ran and finished, it is not "pending" to start.
- **Fix approach:** Cache the final `Status` before nilling `ctrl`, and return the cached snapshot from `Status()` afterwards.

### m06 — Plan 02 Task 19: integration test depends on manually built fixtures

- **Plan:** 02, Task 19
- **Severity:** MINOR
- **Status:** CONFIRMED
- **Problem:** The integration test hard-codes `test/fixtures/bin/fixture-crasher`. If the implementer forgets to run `make fixtures` first, the test fails with a confusing "no such file" error.
- **Fix approach:** Add a `TestMain` that runs `go build` for required fixtures, or add a build tag and a Makefile rule that bundles them.

### m07 — Plan 03 Task 5: monitor test idFor collides for i ≥ 10

- **Plan:** 03, Task 5
- **Severity:** MINOR
- **Status:** FIXED
- **Problem:** `idFor(i)` builds `"p" + string(rune('0'+i))`. For `i >= 10`, `rune('0'+i)` is a non-digit character. IDs collide if the test ever uses more than 10 PIDs.
- **Fix approach:** Use `fmt.Sprintf("p%d", i)`.

### m08 — Plan 03 Task 8: NewEntrypointScript does not quote args

- **Plan:** 03, Task 8
- **Severity:** MINOR
- **Status:** FIXED
- **Problem:** The script generator writes `exec cmd arg1 arg2 ...` without quoting args. An argument containing spaces or shell metacharacters breaks the exec.
- **Fix approach:** Pipe every arg through the existing `shellQuote` helper.

### m09 — Plan 03 Task 8: script_test.go defined twice in plan

- **Plan:** 03, Task 8 Step 1
- **Severity:** MINOR
- **Status:** FIXED
- **Problem:** The plan shows two versions of `pkg/sandbox/script_test.go` back-to-back ("Restructured: a cleaner version"). An implementer reading straight through may write both into the same file or commit the first and rewrite to the second.
- **Fix approach:** Delete the first version from the plan and keep only the cleaner one.

### m10 — Plan 03: Monitor/Evaluator/Sandbox not wired into Manager

- **Plan:** 03
- **Severity:** MINOR
- **Status:** FIXED
- **Problem:** Plan 03 adds all the building blocks but the Manager never constructs a Monitor or registers Sandbox providers. The features are unreachable from user code.
- **Fix approach:** Either wire them in Plan 03 (expands the plan) or add an explicit "NOT COVERED" note deferring the wiring to Plan 05, and ensure Plan 05 has corresponding tasks.

### m11 — Plan 04 Task 3: ExpandEnv has no `$` escape mechanism

- **Plan:** 04, Task 3
- **Severity:** MINOR
- **Status:** FIXED
- **Problem:** A literal `$` in YAML values (e.g., a password like `abc${def}ghi` that should be taken as-is) cannot be preserved — it is always expanded or errored on.
- **Fix approach:** Support `$$` → `$` escape in `ExpandEnv`, and document it in the config schema.

### m12 — Plan 04 Task 5: unknown MsgType leaves client hanging

- **Plan:** 04, Task 5
- **Severity:** MINOR
- **Status:** FIXED
- **Problem:** `handleRequest`'s default branch returns an error, and `handleConn` does `return` (closing the connection) without sending a reply. Clients see EOF instead of a structured error.
- **Fix approach:** Respond with a StatusResponsePayload that has an `Err` field set, then continue the loop.

### m13 — Plan 04 Task 7: ListEntry.RSS is always zero

- **Plan:** 04, Task 7
- **Severity:** MINOR
- **Status:** CONFIRMED
- **Problem:** `toListEntry` sets `RSS: st.RSS`, but `processHandle.Status()` never populates RSS. Until resource telemetry is wired, the field is always zero. The field is present in the payload and the test covers it, giving a false sense of readiness.
- **Fix approach:** Either remove the field for v1 or add an explicit TODO and wire it when Plan 05 hooks the Monitor.

### m14 — Plan 05 Task 1: Heartbeat initial lastPong hides first-tick edge cases

- **Plan:** 05, Task 1
- **Severity:** MINOR
- **Status:** FIXED
- **Problem:** `NewHeartbeat` stores `time.Now().UnixNano()` at construction. The first miss check compares `age > Interval+Timeout` (strict `>`). On slow CI runners the timing can drift just past the boundary, causing a spurious early miss or a miss that is delayed one extra tick.
- **Fix approach:** Initialize `lastPong` to `0` and treat zero as "no pong yet, do not count age".

### m15 — Plan 05 Task 9: CI fuzz-short target only runs FuzzFrameReader

- **Plan:** 05, Task 9
- **Severity:** MINOR
- **Status:** CONFIRMED
- **Problem:** `go test -fuzz=...` accepts one target at a time. The Make target runs only `FuzzFrameReader` for 30 s. Any other fuzz targets added in future plans are silently skipped.
- **Fix approach:** Loop in the Make target over known fuzz targets, failing fast on the first that errors.

---

## Out of scope

The following observations were noted but deliberately excluded from this issue list because they would expand the project's purpose or require re-design beyond logical error fixes:

- Adding Prometheus exporter
- Windows support
- cgroups v2 throttling
- Rust/C native components
- Full TTY attach

These remain in the spec's "Appendix A — Open Questions & Roadmap" section and will not be addressed by this plan revision.
