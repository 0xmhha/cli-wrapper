# CW-G1 — Agent Disconnect Detection Design Specification

**Date:** 2026-04-29
**Status:** Draft — promoted from PHASE2-ROADMAP §CW-G1
**Owner:** TBD
**Related:**
- `docs/superpowers/specs/2026-04-28-pty-first-class-support-design.md`
- `../ai-m/docs/superpowers/PHASE2-ROADMAP.md` §CW-G1
- Skipped chaos test: `test/chaos/pty_agent_crash_test.go`

## Overview

When the `cliwrap-agent` subprocess dies (SIGKILL, segfault, OOM kill), `cli-wrapper`'s host-side IPC layer (`internal/ipc/conn.go`) currently exits its `readLoop` silently on socket EOF. The owning `internal/controller.Controller` keeps its in-memory state at `StateRunning` indefinitely, never transitioning to `StateCrashed`. Consumers of `pkg/cliwrap.ProcessHandle.Status()` therefore see a phantom-running session even though the supervised child is also gone.

This is a direct contradiction of the project's reliability posture. The constant `internal/controller/crash.go::CrashSourceConnectionLost` exists in the codebase and is documented as a crash source, but no code path triggers it. The chaos test `test/chaos/pty_agent_crash_test.go` (added during PTY first-class support work, Task 21) is currently `t.Skip`'d with a clear marker pointing at this gap.

This spec defines the wiring that closes the gap: a callback on `ipc.Conn` fires on socket EOF/error, the controller transitions to `StateCrashed` with `CrashSource = CrashSourceConnectionLost`, downstream consumers (Status, SubscribePTYData, SubscribeLogChunk) observe the change.

### Goals

- `ipc.Conn.readLoop` notifies its owner on socket EOF or unrecoverable read error.
- `Controller` consumes that notification and transitions process state to `StateCrashed` with `CrashSource = CrashSourceConnectionLost` and a brief diagnostic message.
- `pkg/cliwrap.ProcessHandle.Status()` reports `StateCrashed` consistently after the agent dies.
- All inbound streaming subscriptions (`SubscribePTYData`, log chunk subscriptions) receive a closed channel, so consumers can detect the end of stream.
- `test/chaos/pty_agent_crash_test.go` un-skips and passes within the existing 5-second deadline with `-race` clean.
- No regression in unit, integration, or other chaos tests.

### Non-goals

- Auto-respawn of crashed agents. Restart policies (`Spec.Restart`) currently apply to child processes; agent restart is a separate concern (out of scope here).
- Fault-tolerant agent migration / hot-takeover.
- Surfacing the disconnect cause beyond a sentinel error type. Detailed forensics (stack traces, signal numbers) belong to a future observability spec.

### Platform Support

| Platform | Status |
|---|---|
| Linux x86_64 / arm64 | First-class |
| macOS x86_64 / arm64 | First-class |
| Windows | Out of scope (cli-wrapper as a whole is POSIX-only at v0.x) |

---

## Architecture Delta

The change is contained to two packages: `internal/ipc` and `internal/controller`. Public API surface is unchanged.

```
┌──────── Host Process ───────────────────────┐
│                                              │
│  Controller                                  │
│    ├── on disconnect: ──→ state=Crashed ──→ Status() returns StateCrashed
│    │                       │
│    │                       └─→ closes inbound subscription channels
│    │
│    └── owns Conn (with new OnDisconnect cb)
│            │
└────────────┼─────────────────────────────────┘
             │  Unix socket
             ▼
   cliwrap-agent (existing — no changes)
```

### What changes

- **`internal/ipc/conn.go`**: gains `SetOnDisconnect(func(error))`. The `readLoop` calls this callback once when it exits due to EOF or any unrecoverable read error. Idempotent (callback fires at most once per Conn instance).
- **`internal/controller/controller.go`**: registers a callback during `Start()` that pushes a synthetic state-transition event into the controller's existing state machine.
- **`test/chaos/pty_agent_crash_test.go`**: `t.Skip` removed. Test runs end-to-end against a real cliwrap-agent and asserts `StateCrashed` (or `StateStopped`) within 5 s after agent SIGKILL.

### What does NOT change

- IPC frame format, message type IDs, capability handshake, WAL semantics.
- Public `pkg/cliwrap.Spec`, `pkg/cliwrap.Manager`, `pkg/cliwrap.ProcessHandle` API surface.
- Restart policy semantics for child processes.
- Existing `CrashSourceXxx` constants (only the unused `CrashSourceConnectionLost` becomes wired).

---

## Decisions

### Why callback over synthetic message

Two approaches were considered:

**Option A (chosen) — Callback**
- `ipc.Conn` exposes `SetOnDisconnect(func(error))`.
- Conn calls it from `readLoop` exit path.
- Pros: minimal new IPC surface; callback is a private inter-package contract; clear separation between "on-the-wire protocol" and "transport lifecycle."
- Cons: another callback indirection (acceptable; controller already has callbacks for log chunks, PTY data, etc.).

**Option B — Synthetic message**
- `readLoop` synthesizes a sentinel `MsgTypeAgentDisconnected` and delivers it via the existing inbound dispatch path.
- Pros: state-machine code stays uniform with other inbound events.
- Cons: blurs "real protocol" vs "synthetic"; complicates WAL replay (synthetic message would land in WAL, then on replay would fire a spurious disconnect event).

**Decision: Option A**, because WAL hygiene matters more than dispatch uniformity.

### Idempotency

The disconnect callback fires at most once per Conn. If `Stop()` is called normally and the socket then closes, the disconnect callback should still NOT fire (because Stop already drove the state transition). Implementation: a `sync.Once` guard inside `readLoop` exit, plus a flag set by `Conn.Close()` that suppresses the callback.

### Diagnostic detail

The callback receives a single `error` argument. Distinguish:
- EOF → wrapped as `ErrAgentDisconnectedEOF`
- Other read error → wrapped as `ErrAgentDisconnectedUnexpected` with cause via `errors.Unwrap`.

Both map to `CrashSourceConnectionLost` for state-machine purposes. The error is preserved for observability (logs, metrics).

---

## API Surface (internal)

### `internal/ipc/conn.go`

```go
// SetOnDisconnect registers a callback invoked once when the read loop exits
// due to EOF or unrecoverable error (NOT from a normal Close). Callbacks are
// invoked at most once per Conn lifetime, even across reconnect attempts.
func (c *Conn) SetOnDisconnect(cb func(error))
```

```go
var (
    ErrAgentDisconnectedEOF        = errors.New("ipc: agent disconnected (EOF)")
    ErrAgentDisconnectedUnexpected = errors.New("ipc: agent disconnected (unexpected)")
)
```

### `internal/controller/controller.go`

No new public methods. `Start()` internally:

```go
c.conn.SetOnDisconnect(func(cause error) {
    c.transitionToState(ProcessStateUpdate{
        State:       StateCrashed,
        CrashSource: CrashSourceConnectionLost,
        Message:     fmt.Sprintf("agent disconnected: %v", cause),
    })
})
```

(Adapt the exact field names to whatever the existing state machine uses.)

---

## Testing Strategy

### Unit tests (new)

- `internal/ipc/conn_disconnect_test.go`:
  - `TestConn_SetOnDisconnect_FiresOnSocketEOF` — close one side of a socket pair, assert callback fires within 100 ms with `ErrAgentDisconnectedEOF`.
  - `TestConn_SetOnDisconnect_DoesNotFireOnNormalClose` — call `Conn.Close()`, assert callback never fires.
  - `TestConn_SetOnDisconnect_FiresAtMostOnce` — register a callback, force EOF, assert callback fired exactly once.

- `internal/controller/controller_disconnect_test.go`:
  - `TestController_StateTransitionsOnDisconnect` — fake Conn, fire its OnDisconnect, assert controller's Status reaches `StateCrashed` with `CrashSourceConnectionLost`.

### Integration test (existing — un-skip)

- `test/chaos/pty_agent_crash_test.go::TestPTY_AgentCrashDetectedWithin5s` — remove `t.Skip("blocked on agent disconnect → crash state wiring (see Phase 2 task)")`. Run with `-race` and the cliwrap-agent binary in PATH. Should complete within 5 s.

### Regression

All existing unit + integration + chaos suites must continue green with `-race`.

---

## Out of Scope (Phase 3)

These are deliberately excluded — pick up in their own specs if/when triggered.

| ID | Item | Rationale |
|---|---|---|
| **CW-P3-1** | Auto-restart of crashed agent | Different concern; needs supervision policy similar to but separate from child restart policy |
| **CW-P3-2** | Reconnect retries with backoff | Implies the agent might come back; today the simpler model is "agent dead = process dead" |
| **CW-P3-3** | Detailed crash forensics (signal, exit code, stderr capture) | Observability spec; not blocking reliability |

---

## Open Questions

1. **What about partial reads?** If `readLoop` is mid-frame when EOF arrives, should the partial frame be reported as a separate error? Current proposal: log as warning, fire single OnDisconnect. Revisit if it muddles diagnostics.
2. **Reconnect semantics.** The callback contract says "fires once per Conn". If the host wants to reconnect (a future spec), it would create a new Conn instance and re-register. Confirm this matches Manager intent.
3. **Test stability under macOS.** Socket-pair EOF detection is sometimes flaky on macOS in CI. May need a retry / longer timeout in `TestPTY_AgentCrashDetectedWithin5s`. Validate during implementation.

---

## Appendix — Why this is `CW-G1` not `CW-P2-X`

The `*-G*` IDs (gaps) denote items discovered during Phase 1 work that contradict a stated Phase 1 goal. The `*-P2-*` IDs denote items deliberately scoped out at Phase 1 design time. CW-G1 came from a chaos test that should have passed but couldn't — it's a debt, not a deferral. That priority distinction matters: gaps usually outrank deferrals in promotion order.
