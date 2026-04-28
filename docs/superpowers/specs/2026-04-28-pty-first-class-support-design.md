# PTY First-Class Support Design Specification

**Date:** 2026-04-28
**Status:** Draft — pending review
**Owner:** TBD
**Related:** `2026-04-07-cli-wrapper-design.md` (extends)

## Overview

`cli-wrapper` was created so that **agent applications can use Claude Code (and similar interactive LLM CLIs) as their LLM backend**, instead of calling LLM HTTP APIs directly. Hosting an interactive CLI requires a real pseudo-terminal (PTY): the child must believe it owns a terminal so that TUI rendering, raw-mode keyboard handling, signal delivery, and window-size negotiation all work correctly.

The current cli-wrapper implementation spawns child processes with **plain pipes** (`stdout`/`stderr` captured into a ring buffer and a rotating log file). This works for non-interactive daemons such as `redis-server` or custom services, but it cannot host interactive TUI programs — including Claude Code itself, the original target use case.

This document specifies how cli-wrapper acquires **first-class PTY support**: a new spawn mode in which the child process is attached to a pseudo-terminal master held by the agent subprocess, with bidirectional byte streaming, window-resize propagation, and foreground process group signal delivery exposed through new IPC messages. The change makes cli-wrapper viable as a backend for two additional use cases — general application/CLI tool hosting (e.g. as a tmux replacement embedded in another program) and end-user interactive workflows on top of the existing supervision guarantees.

### Background — Why this is the completion of cli-wrapper's identity, not a feature add

cli-wrapper is built on three pillars: **agent isolation** (each child runs inside a dedicated agent subprocess, host crash domain unaffected), **message-grade reliability** (WAL-backed outbox, dedup/ack, exactly-once replay), and **resource governance** (RSS/CPU caps, restart policies). These pillars hold for any kind of child process. The decision to ship v1 with pipe-only spawn was a scoping decision, not a design endpoint. PTY support is the missing capability that lets the original target user (an agent embedding Claude Code) actually adopt cli-wrapper.

This is reflected in non-goal #4 of the original design ("no code derived from tmux"): the project deliberately built its own supervision substrate while leaving the PTY layer for a later iteration. That iteration is this spec.

### Goals

- Spawn child processes attached to a real PTY when configured, while preserving the existing pipe mode for non-interactive children.
- Stream PTY output to the host with the same delivery guarantees as `MsgLogChunk` (no reordering across messages, replayable from WAL).
- Accept user input from the host (`MsgPTYWrite`) and deliver it to the PTY master.
- Propagate terminal resize (`MsgPTYResize` → `TIOCSWINSZ`) and signals (`MsgPTYSignal` → foreground PG) reliably.
- Keep the existing log rotator and ring buffer running for **all** spawn modes — PTY mode does not turn off log persistence.
- Maintain backward compatibility: existing wire format unchanged, existing API additive.
- Pass the existing test matrix (unit + integration + chaos + goleak) plus new PTY-specific tests.

### Non-Goals (this spec)

- Host process restart survival (the agent dying with the host is still acceptable; survives only across child crashes via existing restart policy).
- Multiple host attachments to the same child (single-host model retained).
- Multiplexed window/pane semantics inside a single child (one PTY per child).
- VT cell-buffer emulation in cli-wrapper (raw byte stream; VT semantics stay in the host).
- Windows support (Linux + macOS only, per project scope).

### Platform Support

| Platform | Status |
|----------|--------|
| Linux (x86_64, arm64) | First-class — `posix_openpt` + `grantpt` + `unlockpt` |
| macOS (x86_64, arm64) | First-class — same POSIX path |
| Windows | Out of scope (no `pty` story consistent with project scope) |

---

## Section 1 — Architecture Delta

The component diagram from the original spec is preserved. The change is **inside `cliwrap-agent`** (PTY master fd held there) and **inside `internal/ipc`** (four new message types).

```
┌──────────── Host Application Process ────────────┐
│                                                   │
│  pkg/cliwrap.Manager                              │
│      │                                            │
│      ▼                                            │
│  Controller ────── new IPC msgs (PTY*) ───────┐   │
│      │                                         │   │
└──────┼─────────────────────────────────────────┼──┘
       │  Unix socket pair                       │
       ▼                                         │
┌──────────── cliwrap-agent (subprocess) ────────▼──┐
│                                                    │
│  Dispatcher                                        │
│      │                                             │
│      ├─── runner (PTY mode) ─── PTY master ─┐      │
│      │                                       │      │
│      │                                       ▼      │
│      │                                     child    │
│      │                                     (TUI)    │
│      │                                                │
│      └─── logcollect (ring + rotator) ◄── MsgPTYData│
│                                              tee    │
└────────────────────────────────────────────────────┘
```

### 1.1 What changes inside the agent

- `internal/agent/runner.go` gains a **PTY branch**. When `Spec.PTY != nil`, the runner opens a PTY pair (`pty.Open()` from `creack/pty`), assigns the slave end to the child's stdin/stdout/stderr, and starts the child with `Setsid: true; Setctty: true; Ctty: int(slave.Fd())`. The slave is closed in the agent after `cmd.Start()`.
- The runner spawns two goroutines per PTY child:
  - **PTY → host**: read from PTY master in chunks, deliver as `MsgPTYData`, **and tee into `logcollect`** (ring buffer + file rotator).
  - **host → PTY**: receive `MsgPTYWrite` and write the bytes to PTY master.
- `internal/agent/dispatcher.go` adds routing for the four new message types.

### 1.2 What changes inside the IPC layer

`internal/ipc` adds **four new message type IDs** to the existing frame protocol. The 17-byte header layout, framing, dedup, and WAL behavior are unchanged. The MessagePack payload schemas are defined in §2.

### 1.3 What does NOT change

- Public `Manager.Spawn`/`Stop`/`Status`/`Subscribe` semantics.
- WAL format (`outbox.wal`), frame header (17B), socketpair setup.
- `internal/logcollect` ring buffer + rotator file format.
- Restart policy, resource budget, sandbox plugin interface.
- Any existing `Spec` field default behavior (a `Spec` with `PTY == nil` behaves exactly as today).

---

## Section 2 — Wire Protocol Extension

Four new messages. All flow over the existing frame channel; payloads are MessagePack.

### 2.1 `MsgPTYData` (agent → host)

| Field | Type | Notes |
|---|---|---|
| `seq` | uint64 | Monotonic per-process sequence. WAL-replayed in order. |
| `bytes` | bytes | Raw PTY master output. Chunk size driven by read syscall (typical 4–32 KB). |

Replaces `MsgLogChunk` for PTY-mode children **on the host stream**, but `logcollect` still receives the same bytes locally — see §3.

### 2.2 `MsgPTYWrite` (host → agent)

| Field | Type | Notes |
|---|---|---|
| `seq` | uint64 | Monotonic per-direction. |
| `bytes` | bytes | Raw bytes to write to PTY master. Typically user keystrokes. |

Delivery is at-least-once (existing IPC guarantees). The agent does not interpret the bytes; they pass through verbatim.

### 2.3 `MsgPTYResize` (host → agent)

| Field | Type | Notes |
|---|---|---|
| `cols` | uint16 | Window columns. |
| `rows` | uint16 | Window rows. |

Triggers `ioctl(TIOCSWINSZ, &winsize{rows, cols, 0, 0})` on the PTY master fd. The agent does not also send `SIGWINCH` — `TIOCSWINSZ` does that automatically for the foreground process group.

### 2.4 `MsgPTYSignal` (host → agent)

| Field | Type | Notes |
|---|---|---|
| `signum` | int32 | POSIX signal number (e.g. 2 = SIGINT). |

Delivered to the **foreground process group** of the PTY (`syscall.Kill(-pgid, sig)`), not just the immediate child. This ensures signals reach grandchildren spawned by the TUI (editors, pagers, etc.).

If `signum` corresponds to `SIGTERM`/`SIGKILL`, the existing teardown path also runs — these signals are not a substitute for `Stop()`.

### 2.5 Backward compatibility

- New message type IDs occupy previously-unused values; no collision with existing IDs.
- An older agent that receives an unknown type ID returns `ErrUnknownMsgType` — host detects this in handshake (see §6) and refuses to enable PTY mode for that child.
- An older host that never sends PTY messages is unaffected; agent still spawns pipe-mode children correctly.

---

## Section 3 — Public API Extension

### 3.1 `pkg/cliwrap/types.go`

```go
type Spec struct {
    // ... existing fields ...
    PTY *PTYConfig `yaml:"pty,omitempty"`
}

type PTYConfig struct {
    InitialCols uint16 `yaml:"initial_cols"` // default 80 if zero
    InitialRows uint16 `yaml:"initial_rows"` // default 24 if zero
    Echo        bool   `yaml:"echo"`         // default false; tcsetattr ECHO bit
}
```

YAML loader applies defaults (80×24, echo off). A `Spec` with `PTY == nil` is identical to today.

### 3.2 `pkg/cliwrap/manager.go`

```go
// Existing
func (m *Manager) Spawn(ctx context.Context, spec Spec) (*ProcessHandle, error)
func (h *ProcessHandle) Stop(ctx context.Context, grace time.Duration) error

// New (no-op / error if spec.PTY == nil)
func (h *ProcessHandle) WriteInput(ctx context.Context, b []byte) error
func (h *ProcessHandle) Resize(ctx context.Context, cols, rows uint16) error
func (h *ProcessHandle) Signal(ctx context.Context, sig syscall.Signal) error
func (h *ProcessHandle) SubscribePTYData() (<-chan PTYData, func())
```

`SubscribePTYData` returns a channel of frame chunks plus an unsubscribe func. The implementation drains from the existing eventbus pipeline that already serves `LogChunk`. Multiple subscribers OK; backpressure handled by the eventbus (§5).

For pipe-mode children (`spec.PTY == nil`), `WriteInput`/`Resize`/`Signal` return `ErrPTYNotConfigured`. `SubscribePTYData` returns an empty channel that closes on process exit.

### 3.3 Log persistence is independent of mode

The agent `tee`s every byte read from the PTY master into `internal/logcollect`. Concretely:

```go
// runner pty mode (pseudo)
n, err := masterFd.Read(buf)
if n > 0 {
    sendMsgPTYData(buf[:n])           // host stream
    logcollect.Write(buf[:n])         // ring + rotator (same as pipe mode)
}
```

This means:
- `outbox.wal` continues to record `MsgLogChunk`-equivalent bytes (under a new internal channel for PTY data — see §5.2 for the WAL framing decision).
- The on-disk rotator file (`<workdir>/<id>/stdout.log`) keeps the full byte stream regardless of host attachment state.
- `cliwrap logs <id>` works for PTY mode children identically.

---

## Section 4 — Agent Runner PTY Mode

### 4.1 Spawn sequence

```go
func spawnPTY(spec Spec) error {
    ptmx, tty, err := pty.Open() // creack/pty
    if err != nil { return err }

    cmd := exec.Command(spec.Program, spec.Args...)
    cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
    cmd.Dir = spec.Cwd
    cmd.Env = spec.Env
    cmd.SysProcAttr = &syscall.SysProcAttr{
        Setsid:  true,
        Setctty: true,
        Ctty:    int(tty.Fd()),
    }

    // Apply initial winsize and termios BEFORE Start, so child sees correct state.
    setWinsize(ptmx, spec.PTY.InitialCols, spec.PTY.InitialRows)
    setTermios(ptmx, termios{echo: spec.PTY.Echo, /* canonical mode default */})

    if err := cmd.Start(); err != nil {
        ptmx.Close(); tty.Close(); return err
    }
    tty.Close() // child holds slave; agent only needs master

    // Track foreground process group separately from cmd.Process.Pid
    pgid, _ := syscall.Getpgid(cmd.Process.Pid)
    runner.recordPGID(pgid)

    // Start pumps
    go runner.pumpPTYToHost(ptmx)
    go runner.pumpHostToPTY(ptmx)

    return cmd.Wait() // existing supervision wait loop unchanged
}
```

### 4.2 Read loop

- Buffer: 32 KB on stack-friendly slice (avoids GC churn).
- On `Read` error == `io.EOF` → child closed PTY (typically exit). Drain remaining buffer, close pumps, mark process exited.
- On `Read` error == `syscall.EIO` → POSIX raises EIO when all slave-side fds are closed. Treat as EOF.
- On other error → log + close pumps + propagate to controller as `MsgChildExited` (existing path).

### 4.3 Write loop

- Receives `MsgPTYWrite` chunks from dispatcher channel.
- Write blocks until PTY master accepts. If write fails (EIO), pump terminates; controller is notified.
- No buffering on agent side — backpressure is communicated by IPC channel fill, which propagates to host.

### 4.4 Resize and signal

- `MsgPTYResize` → `ioctl(int(ptmx.Fd()), syscall.TIOCSWINSZ, &winsize{...})`. Returns error to host on failure (rare).
- `MsgPTYSignal` → `syscall.Kill(-pgid, sig)`. The minus prefix is required to address the process group; otherwise grandchildren are missed.

### 4.5 Teardown ordering

On `Stop()`:
1. Send `SIGHUP` to PG (modeling tmux's behavior on session death).
2. Close PTY master after a short grace (default 200 ms, configurable later).
3. If child still alive, escalate via existing SIGTERM → SIGKILL ladder.
4. Pumps exit on EIO/EOF; existing reaper closes WAL.

---

## Section 5 — Concurrency, Buffering, and Backpressure

### 5.1 Goroutine inventory (per PTY child)

| Goroutine | Lifetime | Block conditions |
|---|---|---|
| `cmd.Wait()` | spawn → exit | wait4 syscall |
| PTY → host pump | spawn → EOF | PTY read; bus send |
| host → PTY pump | spawn → channel close | dispatch channel recv; PTY write |
| logcollect tee | shared with pump | local writes |

All four exit on `Stop()` or natural exit. Verified by goleak in tests.

### 5.2 WAL framing for PTY data

Decision: **PTY data is WAL-persisted under a new internal channel ID**, not under the `LogChunk` channel ID. Rationale:
- Replay logic must not deliver PTY bytes as if they were generic log chunks (consumer subscriptions differ).
- Frame header layout is unchanged; only the channel ID byte differs.
- `LogChunk` and `PTYData` share the same `seq` space per process to preserve global ordering; consumers filter by channel ID.

### 5.3 Host-side eventbus backpressure

Existing eventbus uses bounded channels per subscriber. PTY data subscribers are no exception. If a subscriber falls behind, the existing `slow_subscriber` policy applies (drop oldest event with metric increment). For the host attach UX (defined in the ai-m spec), the host is expected to drain at terminal speed; if it cannot, dropping is preferable to blocking the agent's read loop.

### 5.4 Memory bounds

- Per-process PTY → host queue: 64 frames (≈2 MB worst case at 32 KB chunks).
- Per-process host → PTY queue: 16 frames (keystrokes are tiny).
- `logcollect` ring: unchanged (existing 1 MB default).
- Rotator file: unchanged (existing rotation policy).

---

## Section 6 — Capability Negotiation

A host that uses an updated `pkg/cliwrap` may run against an older agent binary on a stale system. To prevent silent breakage, this spec **introduces** a capability negotiation handshake (the prior wire format had none):

1. On agent connect, host sends `MsgCapabilityQuery` (new in this spec; assigned a fresh message type ID alongside the four PTY messages).
2. Agent replies with `MsgCapabilityReply{features: ["pty"]}` if PTY-capable.
3. If host requested `Spec.PTY != nil` and agent reply does not list `"pty"`, `Spawn` returns `ErrPTYUnsupportedByAgent` immediately. No partial spawn, no fallback.
4. Older agents that do not implement the handshake respond with `ErrUnknownMsgType`; host treats this as "no PTY support" and applies rule 3.

This pattern lets cli-wrapper add capabilities later without breaking older deployments.

---

## Section 7 — Testing Strategy

### 7.1 Unit tests (new)

- `internal/ipc/ptymsg_test.go`: encode/decode round-trip for all four new messages.
- `internal/agent/runner_pty_test.go`: spawn `bash -i` with PTY, verify echo round-trip, prompt detection.
- `internal/agent/dispatcher_test.go`: extend with PTY message routing.

### 7.2 Integration tests (new)

- `test/integration/pty_echo_test.go`: end-to-end host ↔ agent ↔ bash echo loop.
- `test/integration/pty_resize_test.go`: send `MsgPTYResize`, run `tput cols` / `tput lines` inside bash, assert.
- `test/integration/pty_signal_test.go`: spawn `sleep 100`, send `MsgPTYSignal{SIGINT}`, assert exit within 1 s.
- `test/integration/pty_log_persistence_test.go`: PTY mode, kill host between writes, restart host, replay from WAL, assert no byte loss.
- `test/integration/pty_capability_test.go`: simulate older agent (feature flag), assert `Spawn` fails with `ErrPTYUnsupportedByAgent`.

### 7.3 Chaos tests (extend `test/chaos/`)

- `test/chaos/pty_slow_consumer_test.go`: host subscriber blocks; assert agent does not block, drops oldest, metric incremented.
- `test/chaos/pty_agent_crash_test.go`: SIGKILL agent mid-stream; host detects connection loss, controller transitions to `Crashed`.
- `test/chaos/pty_child_killed_externally_test.go`: external `kill -9 <child>`; agent sees EIO, host gets `MsgChildExited`.

### 7.4 Benchmarks (new)

- `test/bench/pty_throughput_test.go`: PTY child runs `yes | head -c 100M`; measure host receive throughput.
- `test/bench/pty_latency_test.go`: keystroke → echo round-trip p50/p95/p99.

These benchmarks feed the cross-project `bench/` harness defined in the ai-m spec.

### 7.5 Existing test suite

All existing tests (unit, integration, chaos, goleak) must continue to pass without modification. No flaky regression; CI gates on full green.

---

## Section 8 — Out of Scope (Phase 2 — must not be lost)

These are deliberately excluded from this spec but are tracked as follow-up work. Each must get its own spec when picked up. IDs are prefixed `CW-P2-*` to distinguish from the ai-m spec's own Phase 2 list (`AIM-P2-*`).

| ID | Item | Rationale for deferral |
|---|---|---|
| **CW-P2-1** | Host process restart survival (agent + child outlive host crash) | Requires fd inheritance / daemon detach model; large surface, low immediate value for current target users. |
| **CW-P2-2** | VT cell-buffer emulation inside agent | Host can do this if it wants; pushing into agent breaks the "raw byte fidelity" contract and adds CPU cost. |
| **CW-P2-3** | Multi-host attach to one child | Requires fan-out + permission model + reconnection semantics. Single-host model is intentional. |
| **CW-P2-4** | Window/pane multiplexing inside one child | One-PTY-per-child kept simple; multi-pane is an upper-layer concern (host can spawn N children). |
| **CW-P2-5** | Windows ConPTY backend | Project scope decision; revisit when there is a credible Windows user. |
| **CW-P2-6** | Programmatic termios negotiation API | Currently only `echo` exposed; full termios API (icanon, isig, etc.) deferred until a real consumer asks. |

---

## Section 9 — Open Questions

1. **Default PTY chunk size**: 32 KB chosen for amortized syscall cost. If host latency targets (see ai-m spec §12) require finer granularity, may need tunable.
2. **EIO vs EOF normalization**: should the agent surface a distinct event on EIO (slave fully closed) vs EOF (controlled exit)? Current proposal: collapse both into `ChildExited` with exit code from `wait4`. Revisit if observability gap surfaces.
3. **`SIGWINCH` debouncing**: rapid resize events (e.g. user dragging terminal) currently translate 1:1 to `TIOCSWINSZ`. Consider 50 ms debounce on host side; not on agent side (keep agent stateless).

---

## Appendix A — Dependency Additions

| Dep | License | Rationale |
|---|---|---|
| `github.com/creack/pty v1.1.x` | MIT | De facto Go PTY library, cross-platform POSIX. Vetted, stable, single small dependency. |

No other additions. `golang.org/x/sys/unix` (already transitively present) used for `ioctl` and termios.
