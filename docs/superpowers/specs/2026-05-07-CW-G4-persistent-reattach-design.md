# CW-G4 — Persistent Sessions + Reattach Design Specification

**Date:** 2026-05-07
**Status:** Draft — promoted from CW-P2-1 (now triggered by ai-m's claude-code persistence demand)
**Owner:** TBD
**Related:**
- `docs/superpowers/specs/2026-05-04-CW-G3-supervision-leak-design.md` (foreground cleanup; CW-G4 builds on top of this contract)
- `docs/superpowers/specs/2026-04-28-pty-first-class-support-design.md` (PTY semantics inherited)
- `docs/superpowers/specs/2026-04-29-CW-G1-agent-disconnect-design.md` (agent crash detection; reused for persistent agents)
- `../../ai-m/docs/superpowers/STATUS.md` §CW-P2-1 (origin), §AIM-P2-2 (downstream consumer)

## Overview

cli-wrapper today is foreground-mode only. The supervised child process lives and dies with the host process: when the host exits (intentionally via `AgentHandle.Close` or unintentionally via crash), the agent receives SIGTERM (cooperative) or its IPC peer closes (crash); CW-G3 ensures cooperative shutdown reaps the child cleanly. There is no mechanism for a supervised child to outlive the host that spawned it.

This forces real workflows into corners. A user iterating on ai-m code who wants their `claude code` session to keep running across ai-m restarts has no answer. A user whose ai-m process crashes for any reason loses their long-running CLI work entirely. The cli-wrapper "supervision" contract is too tight to its host.

CW-G4 introduces **persistent sessions**: when `Spec.Persistent = true`, the agent self-daemonizes at spawn time, listens on a per-session UNIX socket for reattach, and outlives the host. New hosts (after host restart, crash, or simply a different ai-m instance) can discover and reattach to surviving persistent sessions, restoring an interactive view of the still-running child.

### Goals

- Per-session opt-in via `Spec.Persistent bool` (default false). Zero behavior change for non-persistent sessions.
- Persistent sessions: agent + child outlive host process termination, regardless of how the host died.
- Reattach API discovers and reconnects to surviving sessions. The reattached `ProcessHandle` is functionally equivalent to a freshly-spawned one (WriteInput, SubscribePTYData, Stop, Close, etc.).
- On reattach, the new host immediately receives a snapshot of recent PTY output (in-memory ring buffer) so its terminal renders the current screen state.
- Single-host attach at a time. Second concurrent reattach is rejected with `ErrAlreadyAttached`.
- No automatic cleanup of persistent metadata. The user explicitly terminates sessions or removes stale dirs; cli-wrapper provides diagnostic CLI (`cliwrap list --persistent`, `cliwrap kill <id>`) for visibility.
- Existing tests pass unchanged. CW-G3 burst chaos test continues to pass at default N=15.

### Non-goals

- **Detach API.** No `h.Detach(ctx)` in v1. Persistence is for crash/restart recovery, not as a routine lifecycle state. An explicit Detach API would lower the threshold for accidentally accumulating orphan sessions; rejected after design review.
- **Multi-host concurrent attach.** Filed as CW-P2-3. Out of scope.
- **Agent crash auto-restart.** If the persistent agent process itself dies (segfault, OOM kill), the supervised child is also lost and the metadata becomes stale. v1 surfaces this via `ErrAgentDead` on Reattach and `Alive=false` in ListPersistent; manual cleanup required. No auto-respawn.
- **Token-based authentication for reattach.** v1 relies solely on filesystem permissions (parent dir 0700, files 0600). Per-session tokens can be added later without breaking compat if a multi-tenant threat model emerges.
- **VT cell-buffer emulation for screen restoration.** Filed as CW-P2-2. Ring buffer ships raw bytes; the new host's terminal interprets ANSI/CSI escapes naturally.
- **TTL-based auto-expiry of persistent sessions.** No "die after N hours of detached" policy in v1. The user manages cleanup.
- **Windows ConPTY backend.** Out of scope per project policy (CW-P2-5).
- **Cross-host migration of persistent sessions.** A persistent session is bound to the user account and machine that created it.

### Platform Support

| Platform | Status |
|---|---|
| Linux x86_64 / arm64 | First-class (POSIX `setsid`, UNIX sockets, filesystem perms) |
| macOS x86_64 / arm64 | First-class (primary repro platform) |
| Windows | Out of scope |

---

## Architecture Delta

### What changes (Persistent=true sessions only)

- **Spawner branch.** When `spec.Persistent` is true, `internal/supervise.Spawner.Spawn` configures the child `cmd` with `SysProcAttr{Setsid: true}`, redirects stdio to `<sessionDir>/agent.log`, sets `cmd.Dir = "/"`, and passes a `--persistent` argv flag plus `CLIWRAP_PERSISTENT=1` and `CLIWRAP_SESSION_DIR=<sessionDir>` env vars. The child agent process detaches from host's terminal session, controlling terminal, and cwd.
- **Agent persistent main path.** The agent binary, when invoked with the persistent flag, additionally: ignores SIGHUP, opens a UNIX listening socket at `<sessionDir>/sock` (mode 0600), writes a PID file and meta.json, allocates an in-memory PTY ring buffer of size `spec.RingBufferSize`, and runs an Accept loop that adopts incoming connections as the active IPC channel.
- **Reattach IPC.** Two new IPC frame types: `MsgReattachQuery` (host → agent on dial, requests handshake), `MsgPTYRingDump` (agent → host on attach, single-frame snapshot of ring buffer). The existing `MsgHello` and capability negotiation are reused.
- **Manager API additions.** `WithPersistentDir(path)` Manager option, `Manager.ListPersistent()`, `Manager.Reattach(ctx, id)`, plus new `PersistentSessionInfo` type and new error sentinels (`ErrSessionNotFound`, `ErrAlreadyAttached`, `ErrAgentDead`, `ErrPersistenceUnsupportedByAgent`, `ErrIncompatibleAgent`).
- **Spec additions.** `Spec.Persistent bool`, `Spec.RingBufferSize int`. Both default to zero values and are inert when `Persistent=false`.
- **CLI commands.** `cliwrap list --persistent` (table output) and `cliwrap kill <id>` (Reattach + Stop + Close shortcut).
- **Capability advertisement.** Agents add `"persistence"` to their capability reply. New hosts request `Persistent=true` only against agents that advertise it; mismatch surfaces `ErrPersistenceUnsupportedByAgent` at Spawn time.

### What stays the same

- All non-persistent (`Persistent=false`, default) sessions: identical behavior to today, including CW-G3 cleanup cascade.
- IPC protocol framing, capability negotiation handshake, all existing message types.
- PTY first-class support semantics, `OnData` broadcast, log collection pipeline.
- Restart policies (`Spec.Restart`).
- CW-G1 disconnect detection: the controller-side wiring continues to work for persistent agents — when the agent process dies (crash, kill -9), the new host's IPC peer sees EOF and the controller transitions to `StateCrashed` exactly as before. Reattach surfaces this as `ErrAgentDead`.

### What's deferred (file separately when triggered)

- **`h.Detach(ctx)` API** — would let a host release its connection without exiting itself. v1 sees no compelling use case beyond ergonomics; orphan-accumulation risk outweighs benefit.
- **Multi-host attach** — CW-P2-3.
- **Agent crash auto-restart** — separate feature.
- **VT cell-buffer emulation** — CW-P2-2; current ring buffer of raw bytes is sufficient for typical CLI tools.
- **TTL / auto-expiry** — add when accumulation is observed in real use.
- **Token authentication** — add if multi-tenant threat model arrives.
- **Cooperative cleanup for non-persistent sessions on host crash** — pre-existing minor gap from CW-G3 (CW-G3 fixed cooperative path; SIGKILL of host still leaves agent idle and child orphaned). Not addressed by CW-G4. File as CW-G5 if priorities shift.

---

## Lifecycle States

```
                 Spawn(Persistent=true)
                          │
                          ▼
                    ┌──────────┐
                    │ Spawning │
                    └─────┬────┘
                          │ first host connected
                          ▼
                  ┌──────────────────┐    host disconnect (EOF / SIGTERM)
                  │ Running attached │ ──────────────────────────┐
                  └─────────┬────────┘                            │
                            ▲                                     ▼
                            │                          ┌────────────────────┐
                            └──────────────────────────│ Running detached   │
                                  Reattach (sock)      └─────────┬──────────┘
                                                                 │
                            ┌────────────────────────────────────┤
                            │              child exits (any reason — Stop, natural)
                            ▼
                       ┌─────────┐
                       │ Stopped │ → agent self-terminates → sock+pid removed,
                       └─────────┘   meta.json + agent.log preserved (post-mortem)
```

**Invariant:** persistent agent terminates when child terminates. There is no "persistent agent without child" mode. The persistence property is about surviving host process lifecycle, not about outliving the supervised work.

---

## Public API Surface (cli-wrapper)

### Spec extensions

```go
type Spec struct {
    /* existing fields unchanged */

    // Persistent makes the agent + child outlive the host process. When true,
    // the agent self-daemonizes at spawn time and accepts reattach via a
    // UNIX socket at WithPersistentDir/<ID>/sock.
    //
    // Default false: foreground-mode session, identical to historical behavior.
    Persistent bool

    // RingBufferSize is the in-memory PTY output buffer size in bytes,
    // used to redraw the current screen state on reattach. Only meaningful
    // when Persistent=true. Default 256*1024 (256 KiB).
    RingBufferSize int
}
```

### Manager option

```go
// WithPersistentDir overrides the directory where persistent-session
// metadata is stored. Default: $HOME/.cliwrap/sessions/. The directory
// is created with mode 0700 on first use; per-session subdirectories
// are 0700 with files 0600.
//
// Tests should pass t.TempDir() to keep state isolated.
func WithPersistentDir(path string) ManagerOption
```

### New types and methods

```go
type PersistentSessionInfo struct {
    ID        string
    Spec      Spec      // restored from meta.json (Persistent=true is implied)
    AgentPID  int       // agent's pid at start; may be stale
    StartedAt time.Time // recorded at agent startup with nanosecond precision
    Alive     bool      // best-effort: kill -0 AgentPID
    Attached  bool      // best-effort: agent's <dir>/.attached flag file
}

// ListPersistent scans WithPersistentDir for valid sessions.
// Stale entries (Alive=false) are returned but not auto-cleaned;
// the user removes them via `cliwrap kill <id>` or rm.
func (m *Manager) ListPersistent() ([]PersistentSessionInfo, error)

// Reattach connects to a previously-spawned persistent session by ID.
// On success, returns a ProcessHandle equivalent to one returned by
// Manager.Register followed by Start: WriteInput, SubscribePTYData,
// Signal, Resize, Status, Stop, and Close all behave identically.
//
// SubscribePTYData on a Reattach-derived handle yields the ring buffer
// snapshot first (as one or more chunks in chronological order), then
// transitions to live PTY data. This is consistent with a Spawn-derived
// handle's first SubscribePTYData call (which also yields all received
// PTY data from session start, just from an empty buffer).
func (m *Manager) Reattach(ctx context.Context, id string) (ProcessHandle, error)
```

### New error sentinels (`pkg/cliwrap/errors.go`)

```go
ErrSessionNotFound              // <dir>/<id>/ does not exist
ErrAgentDead                    // pid file points to a dead PID
ErrAlreadyAttached              // another host is currently connected
ErrIncompatibleAgent            // capability version mismatch on reattach
ErrPersistenceUnsupportedByAgent // Spawn(Persistent=true) against a pre-CW-G4 agent
```

### Stop / Close semantics for persistent sessions

| API | `Persistent=false` (existing) | `Persistent=true` (new) |
|---|---|---|
| `h.Stop(ctx)` | child kill, cmd.Wait | child kill + agent self-terminate + meta cleanup (sock, pid removed; meta.json + agent.log preserved) |
| `h.Close(ctx)` | conn close + agent SIGTERM (existing) | conn close only. Agent stays alive in detached state, ready for next reattach. |

To fully terminate a persistent session, call `h.Stop()`. To merely release the host's connection while leaving the session running, call `h.Close()`. The CLI command `cliwrap kill <id>` is a convenience that does Reattach + Stop + Close in sequence.

---

## Daemonize Mechanism

Daemonization happens at the host's spawn time, not by the agent self-forking. Go's runtime makes post-startup `fork()` unsafe; configuring `os/exec.Cmd.SysProcAttr` before `Start()` achieves the same detachment cleanly.

```go
// internal/supervise/spawner.go::Spawn (sketch)

cmd := exec.CommandContext(ctx, agentPath, ...)

if spec.Persistent {
    sessionDir := filepath.Join(persistentDir, spec.ID)
    if err := os.MkdirAll(sessionDir, 0o700); err != nil {
        return nil, err
    }
    logFile, err := os.OpenFile(
        filepath.Join(sessionDir, "agent.log"),
        os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
    if err != nil { return nil, err }

    cmd.Stdin  = nil // /dev/null effectively
    cmd.Stdout = logFile
    cmd.Stderr = logFile
    cmd.Dir    = "/"
    cmd.Env    = append(cmd.Env, "CLIWRAP_PERSISTENT=1",
                                  "CLIWRAP_SESSION_DIR="+sessionDir)
    cmd.Args   = append(cmd.Args, "--persistent")
    cmd.SysProcAttr = &syscall.SysProcAttr{
        Setsid: true,
    }
    // log file fd is inherited by child via Stdout/Stderr; can close in parent
    defer logFile.Close()
}
// Persistent=false branch unchanged from today
```

**`Setsid: true` consequences:**
- Agent is in a new POSIX session, no controlling terminal. Terminal closure does not deliver SIGHUP.
- Agent's parent is the host process; on host death, kernel reparents agent to PID 1 (`launchd` on macOS, `init` on Linux). Agent continues running.
- Agent's stdio is the log file, not the host's terminal. Host's terminal close does not affect agent's writers.

**Agent persistent-mode handler** (`internal/agent/main.go`):
- Detect `--persistent` flag (or `CLIWRAP_PERSISTENT=1`); if set:
  - `signal.Ignore(syscall.SIGHUP)` (defense; Setsid should already prevent SIGHUP, but explicit ignore is belt-and-suspenders).
  - Write `<sessionDir>/pid` (one decimal line, mode 0600).
  - Write `<sessionDir>/meta.json` (spec + startedAt + version, mode 0600).
  - Open `<sessionDir>/sock` UNIX listener (`net.Listen("unix", ...)`; chmod 0600 on the socket file).
  - Initialize PTY ring buffer of size `spec.RingBufferSize` (allocated zero-init).
  - Run accept loop in a goroutine; each accepted connection is a candidate IPC channel.
  - Existing IPC over fd 3 (socketpair from spawner) is the FIRST host connection. On its EOF, agent transitions to detached state; main loop continues, accept loop services future reattach.

The accept loop, simplified:

```go
for {
    conn, err := listener.Accept()
    if err != nil { /* shutdown path */ }

    agent.attachLock.Lock()
    if agent.attached {
        _ = sendErrorFrame(conn, ErrAlreadyAttached)
        _ = conn.Close()
        agent.attachLock.Unlock()
        continue
    }
    agent.attached = true
    agent.attachLock.Unlock()

    _ = os.WriteFile(filepath.Join(sessionDir, ".attached"), nil, 0o600)

    snapshot := agent.ringBuffer.Snapshot()
    _ = sendFrame(conn, MsgPTYRingDump, snapshot)
    agent.adoptIPCConn(conn) // replaces fd-3 socketpair as primary IPC

    // When this conn returns EOF (host disconnect):
    //   agent.attached = false
    //   os.Remove(<sessionDir>/.attached)
    //   loop continues; next reattach is welcome
}
```

**On agent self-termination** (child exited, `h.Stop`, etc.):
- Close listener, remove `<sessionDir>/sock`, `<sessionDir>/pid`.
- Preserve `meta.json` + `agent.log` for post-mortem.
- Exit cleanly. CW-G3's Drain + StopTimeout cascade still applies for child cleanup.

---

## Metadata File Structure

```
<persistentDir>/                    [mode 0700, lazy-created]
  <session-id>/                     [mode 0700]
    sock           [mode 0600]      UNIX listening socket; removed on agent self-exit
    pid            [mode 0600]      agent PID, decimal text, single line; removed on agent self-exit
    meta.json      [mode 0600]      session metadata; preserved across self-exit
    agent.log      [mode 0600]      daemonized stdout/stderr (append-only); preserved across self-exit
    .attached      [mode 0600]      flag file; present iff agent.attached==true (best-effort)
```

`meta.json` schema:

```json
{
  "version": "1.0",
  "id": "agent-claude-abc123",
  "spec": { /* Spec fields including Persistent=true and RingBufferSize */ },
  "agentPID": 78901,
  "startedAt": "2026-05-07T10:23:04.123456789Z"
}
```

`startedAt` precision is nanoseconds. On reattach, the agent's `MsgHello` includes its own startedAt; if the value differs from `meta.json`'s, the attempt fails with `ErrAgentDead` (PID rollover defense — the agent we're connecting to is not the one that wrote meta.json).

**ID conflict policy at spawn:**
- If `<persistentDir>/<id>/` exists and the agent is alive: `ErrSessionAlreadyExists`. Cannot create.
- If `<persistentDir>/<id>/` exists but agent is dead (stale): same `ErrSessionAlreadyExists` with a hint message. **Spawn does not auto-clean stale dirs.** User must run `cliwrap kill <id>` (which does the cleanup) or `rm -rf <persistentDir>/<id>/`. This is consistent with the manual-cleanup policy.

---

## PTY Ring Buffer

```go
// internal/agent/ringbuffer.go (new)

type ringBuffer struct {
    mu   sync.Mutex
    data []byte // size == spec.RingBufferSize, zero-init
    head int    // next write position
    full bool   // wraparound flag
}

func newRingBuffer(size int) *ringBuffer { /* size validation, allocate */ }

// Write appends p. If len(p) > buffer size, only the tail of p is retained.
// Wraparound is automatic; full=true once written ≥ size bytes.
func (rb *ringBuffer) Write(p []byte)

// Snapshot returns a copy of the buffer contents in chronological order
// (oldest byte first, newest last). Length is min(bytes-written, size).
// Returns an empty slice if no writes have occurred.
func (rb *ringBuffer) Snapshot() []byte
```

**Hot path integration:** the existing `OnData` callback (registered by runner_pty.go on `proc`) tees PTY bytes to (a) the host via MsgPTYData, (b) the StdoutSink (log collector), and (c) — when persistent — the ring buffer. The ring buffer write is `O(len(p))` with one mutex acquisition; under the bench `output_throughput` workload (cliwrap measured ~65 MB/s), this is well below the bottleneck.

**Default size 256 KiB rationale:** an 80×24 terminal is ~2 KiB raw, but typical CLI output includes ANSI sequences, scrollback, formatted JSON or LLM responses; 256 KiB covers ~1000 lines of typical mixed content. Configurable for verbose tools (claude code, vim with redraws, etc.).

**Reattach behavior:** the ring buffer Snapshot is delivered via a single `MsgPTYRingDump` frame at attach time. The host-side controller queues this dump such that the next `SubscribePTYData` subscriber receives it as the first chunk(s) before live PTY data. Implementation detail: a one-shot "initial buffer" field on the controller, drained on first subscribe, plus the existing live broadcaster. From the application's perspective, the API is uniform — the first subscriber simply sees historical data followed by live, the same way a freshly-spawned session's first subscriber sees data from session start.

---

## Reattach End-to-End Flow

```
Caller: mgr := cliwrap.NewManager(WithPersistentDir(path))
        infos, err := mgr.ListPersistent()
        h, err := mgr.Reattach(ctx, "agent-claude-abc123")
                ┌──────────────────────────────────────────┐
                │ 1. Manager reads <dir>/<id>/meta.json    │
                │    Verifies pid alive (kill -0)          │
                │    Returns early on ErrSessionNotFound   │
                │    or ErrAgentDead.                       │
                └────────────────────┬─────────────────────┘
                                     │
                                     ▼
                ┌──────────────────────────────────────────┐
                │ 2. Manager dials <dir>/<id>/sock          │
                │    On ECONNREFUSED with alive pid:        │
                │      brief retry, then ErrAgentDead.      │
                └────────────────────┬─────────────────────┘
                                     │
                                     ▼
                ┌──────────────────────────────────────────┐
                │ 3. Manager sends MsgReattachQuery         │
                │    (capability version, intent: reattach) │
                │    Receives MsgHello with startedAt.      │
                │    Compares startedAt with meta.json's;   │
                │    mismatch → ErrAgentDead.               │
                │    Receives MsgPTYRingDump.               │
                └────────────────────┬─────────────────────┘
                                     │
                                     ▼
                ┌──────────────────────────────────────────┐
                │ 4. Agent-side accept loop:                │
                │    - check agent.attached; if true,       │
                │      send ErrAlreadyAttached, close conn  │
                │    - else mark attached, write .attached  │
                │    - send MsgHello + MsgPTYRingDump       │
                │    - adopt conn as primary IPC channel    │
                └────────────────────┬─────────────────────┘
                                     │
                                     ▼
                ┌──────────────────────────────────────────┐
                │ 5. Manager constructs ProcessHandle       │
                │    queues ring dump as initial buffer     │
                │    Returns handle to caller               │
                └──────────────────────────────────────────┘
```

**Disconnect → detached transition:**
- The host's process death or explicit `h.Close` closes the conn from host side.
- Agent's adopted IPC conn sees EOF.
- Agent: `agent.attached = false`, `os.Remove(<dir>/.attached)`, accept loop continues for next reattach.
- Detached state is identical to never-attached for accept-loop purposes.

### Error Matrix

| Scenario | `Manager.Reattach` returns |
|---|---|
| `<dir>/<id>/` does not exist | `ErrSessionNotFound` |
| `<dir>/<id>/sock` missing or unreadable | `ErrSessionNotFound` (treat as "no session") |
| `<dir>/<id>/pid` missing | `ErrAgentDead` |
| pid file present but `kill -0 pid` says dead | `ErrAgentDead` |
| Dial succeeds but agent's `MsgHello.startedAt` ≠ `meta.json.startedAt` (PID rollover) | `ErrAgentDead` |
| Dial succeeds, agent says "already attached" | `ErrAlreadyAttached` |
| Dial succeeds but capability version mismatch | `ErrIncompatibleAgent` |
| `ctx` canceled or deadline expired during any phase | `ctx.Err()` (`DeadlineExceeded`, etc.) |

### Edge Cases

| Case | Behavior |
|---|---|
| Detached state, child exits naturally | Agent broadcasts `MsgChildExited` to ring buffer (no attached host to receive); agent self-terminates; sock + pid removed; meta + log preserved for post-mortem. |
| Detached state for very long duration (days) | v1: no TTL. Agent + child remain alive indefinitely. Listed by `ListPersistent` until terminated. Consistent with manual-cleanup policy. |
| Reattach context canceled mid-handshake | Agent's send (Hello, RingDump) fails on closed conn; defer-protected rollback restores `agent.attached = false`, removes `.attached` flag, accept loop continues. The conn is closed without lingering state. |
| Agent process killed externally (`kill -9`) | Sock unconnectable, pid points to dead PID, ListPersistent shows `Alive=false`. Caller reattach gets `ErrAgentDead`. User cleans manually. |
| Concurrent `ListPersistent` from multiple processes | Read-only on filesystem; no race. `Attached` field is best-effort (read of `.attached` flag), may briefly disagree with reality during attach/detach transitions. |
| PID rollover (machine reboot, original PID reassigned to unrelated process) | `kill -0 pid` returns true (false positive), but `MsgHello.startedAt` mismatches `meta.json.startedAt` → `ErrAgentDead`. |
| Write to `meta.json` is interrupted (host crash mid-write) | Agent writes meta.json before opening listener; partial write blocks listener creation, agent exits with error. ListPersistent on partial dir gracefully returns `ErrSessionNotFound`. |
| `Spec.RingBufferSize` is 0 (unset by caller, Persistent=true) | Defaults to 256 KiB at agent startup. |
| `Spec.RingBufferSize` is negative or larger than reasonable cap (say, 64 MiB) | Spec validation rejects at Register time with a clear error. |

---

## CLI Commands

`cmd/cliwrap` gains two new commands:

```
$ cliwrap list --persistent
ID                  PID    STARTED                  ALIVE  ATTACHED
agent-claude-abc    78901  2026-05-07T10:23:04Z     true   yes
agent-bash-xyz      54321  2026-05-06T16:11:02Z     false  stale (rm -rf to clean)

$ cliwrap kill agent-claude-abc
Connecting to session agent-claude-abc...
Stopping child (pid 78902, claude)... done.
Cleaning up session metadata... done.
```

Implementation:
- `cliwrap list --persistent`: opens default Manager (with default WithPersistentDir), calls `ListPersistent`, formats as a table.
- `cliwrap kill <id>`: opens default Manager, calls `Reattach(ctx, id)`, then `h.Stop(ctx)`, then `h.Close(ctx)`. Errors are surfaced verbatim. For stale sessions (ErrAgentDead), prints a hint to `rm -rf` directly.

These commands use the same `WithPersistentDir` resolution as the Go API: env override `CLIWRAP_PERSISTENT_DIR`, then default `~/.cliwrap/sessions/`.

---

## Capability Negotiation

Add `"persistence"` to the agent's capability reply set (existing `MsgCapabilityReply`). The host checks this before honoring `Persistent=true`:

```go
if spec.Persistent && !ctrl.AgentSupportsPersistence() {
    return nil, errors.Join(backend.ErrBackendUnavailable,
                            ErrPersistenceUnsupportedByAgent)
}
```

This ensures that if a new host (CW-G4-aware) is paired with an old agent binary (pre-CW-G4), `Persistent=true` requests fail at Spawn with a clear error rather than producing a silently-non-persistent session.

---

## Test Strategy

### Unit tests (no real subprocess)

- `internal/agent/ringbuffer_test.go` — write boundary cases (empty, exact-fill, overflow, wraparound), Snapshot ordering correctness, `-race`-safe under concurrent Write/Snapshot.
- `internal/agent/persistent_main_test.go` — agent's persistent-mode initialization (mock filesystem, verify pid + meta.json + sock created with correct perms; SIGHUP ignore is registered).
- `internal/supervise/spawner_persistent_test.go` — Spawner branch when `spec.Persistent=true`: cmd.SysProcAttr.Setsid true, stdio redirected to log file, env contains CLIWRAP_PERSISTENT, argv contains --persistent.
- `pkg/cliwrap/persistent_meta_test.go` — meta.json round-trip serialization, version field, startedAt precision.

### Integration tests (real subprocess)

- `test/integration/pty_persistent_test.go::TestPersistent_BasicLifecycle` — spawn persistent session running `/bin/cat`, write input, verify echo, h.Stop, verify cleanup.
- `test/integration/pty_persistent_test.go::TestPersistent_HostDeathSurvival` — spawn, kill host process (`os.Process.Kill` on host's own representation? — actually use a subprocess host harness), verify agent + child still alive via pgrep + sock dial succeeds.
- `test/integration/pty_persistent_test.go::TestPersistent_ReattachDeliversRingBuffer` — spawn, write known PTY content, kill host, ListPersistent, Reattach, SubscribePTYData first chunk contains the known content prefix.
- `test/integration/pty_persistent_test.go::TestPersistent_AlreadyAttached` — spawn, Reattach (1), Reattach (2) returns `ErrAlreadyAttached` while (1) is connected. Disconnect (1), Reattach (2) succeeds.
- `test/integration/pty_persistent_test.go::TestPersistent_AgentDead` — write a meta.json + pid file pointing to a dead PID, Reattach returns `ErrAgentDead`.
- `test/integration/pty_persistent_test.go::TestPersistent_PIDRollover` — same as above, but pid file points to a different live process; `MsgHello.startedAt` mismatch triggers `ErrAgentDead`.
- `test/integration/pty_persistent_test.go::TestPersistent_ChildNaturalExit` — spawn `/bin/echo hello`, child exits, agent self-terminates, sock + pid removed, meta + log preserved. ListPersistent returns `Alive=false`.
- `test/integration/pty_persistent_test.go::TestPersistent_StopFromReattachedHost` — spawn, kill host, Reattach, h.Stop, full cleanup verified.
- `test/integration/pty_persistent_test.go::TestPersistent_CloseDoesNotKillAgent` — spawn, h.Close, sock still listening, agent still alive, second Reattach succeeds.
- `test/integration/pty_persistent_test.go::TestPersistent_CapabilityRefusedByOldAgent` — feature-flag the agent binary build to omit "persistence" capability; new host's Spawn(Persistent=true) returns `ErrPersistenceUnsupportedByAgent`.

### Chaos tests

- `test/chaos/persistent_burst_test.go::TestPersistent_BurstSpawnReattach_NoLeak` — opt-in (`CHAOS_BURST=1`); 25 spawn → kill host → Reattach → Stop cycles. Asserts cliwrap-agent count delta ≤ 3 and cat count delta ≤ 3 (same pattern as existing burst test).
  - This exercise also probes whether CW-G3.1 (host-side Manager state accumulation) is sidestepped by per-session UNIX sockets. Expected: no cumulative degradation.

### Backward compatibility

- All existing unit, integration, and chaos tests pass unchanged.
- CW-G3 burst chaos test (CHAOS_BURST=1, default N=15) continues to PASS — verifies non-persistent behavior unchanged.
- Existing `ai-m` cliwrap backend (which constructs `cw.Spec` without `Persistent`) sees zero behavior change.

### CLI tests

- `cmd/cliwrap/list_persistent_test.go` — table output format, alive/stale rendering.
- `cmd/cliwrap/kill_test.go` — kill happy path, kill on stale (returns ErrAgentDead, prints hint).

### `-race` and goroutine hygiene

- Every test package adds or already has `goleak.VerifyTestMain(m)`.
- All tests pass with `-race` clean.

---

## Effort Estimate

| Stage | Estimated subagent compute |
|---|---:|
| 1. Foundation (Spec.Persistent, RingBufferSize, WithPersistentDir, meta + pid file IO, error sentinels) | ~2 hrs |
| 2. Daemonize (spawner Setsid + stdio redirect + flag passing; agent persistent-mode handler) | ~2 hrs |
| 3. UNIX listener + reattach IPC (MsgReattachQuery, MsgPTYRingDump frame types; accept loop; attach lock) | ~3 hrs |
| 4. Ring buffer impl + integration into PTY hot path; dump-on-reattach | ~1.5 hrs |
| 5. Manager.ListPersistent + Manager.Reattach + ProcessHandle initial-buffer wiring | ~1.5 hrs |
| 6. Lifecycle (h.Stop terminates persistent agent; child natural exit handler; meta cleanup) | ~1 hr |
| 7. CLI commands (`list --persistent`, `kill`) | ~1 hr |
| 8. Integration tests + bug fixing | ~3 hrs |
| **Total** | **~15 hrs** |

LoC estimate: ~2000 production code + ~1000 test code = ~3000 LoC delta.

Comparable to CW-G1 + CW-G3 combined; substantially larger than CW-G3 alone.

---

## Open Questions / Decisions Already Made

| # | Question | Decision |
|---|---|---|
| 1 | CLIWRAP_PERSISTENT via env or argv? | **Both** — argv `--persistent` (primary, cleaner), env `CLIWRAP_PERSISTENT=1` (defense + carries `CLIWRAP_SESSION_DIR`). |
| 2 | How to detect PID rollover? | meta.json stores `startedAt` with nanosecond precision; agent's `MsgHello` echoes its actual startedAt; mismatch → `ErrAgentDead`. |
| 3 | Capability negotiation backward compat | Agents advertise `"persistence"` capability. New hosts request `Persistent=true` only if advertised; mismatch → `ErrPersistenceUnsupportedByAgent` at Spawn. |
| 4 | `WithRuntimeDir` vs `WithPersistentDir` confusion | They are independent: RuntimeDir = agent's own work files (outbox WAL etc., applies to all sessions); PersistentDir = persistent-session metadata only. Documented explicitly in README. |
| 5 | Default `RingBufferSize`? | 256 KiB. Configurable per Spec. |
| 6 | CW-G3 Drain mechanism integration | Reused unchanged. Persistent agent's self-termination path also calls Drain before returning. No new code. |
| 7 | macOS launchd reparenting after host death | Standard adoption + reaping. Agent's `Setsid: true` ensures it has no controlling terminal that launchd cares about. No additional work. |
| 8 | Concurrent `ListPersistent` from multiple processes | Read-only, no mutation, no race. `Attached` flag may briefly disagree with reality during transitions. Best-effort. |
| 9 | How does `cliwrap kill` differ from `h.Stop` + `h.Close`? | Identical semantically; `cliwrap kill` is a CLI ergonomic shortcut. Internally: Reattach → Stop → Close. |
| 10 | `Spec.RingBufferSize` validation cap | Reject < 0 and > 64 MiB at Spec.Validate. Reasonable guardrail without being too restrictive. |

---

## Migration / Backward Compatibility

- **No breaking changes.** All new fields default to zero values that mean "non-persistent, today's behavior."
- Existing ai-m cliwrap backend (`internal/llm/backend/cliwrap/backend.go`) sees zero behavior change unless it explicitly opts in by setting `Persistent: true` on the spec.
- Existing tests pass unchanged.
- The new `"persistence"` capability advertisement is additive; pre-CW-G4 hosts that don't check for it ignore it.

CHANGELOG entry under `[Unreleased]`:

```
### Added
- CW-G4: Persistent sessions + reattach. New `Spec.Persistent` flag opts a
  session into outliving the host process. New `Spec.RingBufferSize`
  controls in-memory PTY scrollback for redraw on reattach. New
  `WithPersistentDir(path)` Manager option. New `Manager.ListPersistent()`,
  `Manager.Reattach(ctx, id)`. New CLI commands `cliwrap list --persistent`
  and `cliwrap kill <id>`. Default behavior (Persistent=false) unchanged.
```

---

## Out-of-spec but Related

- **AIM-P2-2** (ai-m side of host restart survival): consumes CW-G4 directly. ai-m needs to: (a) on startup, optionally call `mgr.ListPersistent()` and offer to reattach in TUI; (b) expose a per-session "make persistent" toggle in session-create UI; (c) handle Reattach errors with sensible UX. Filed separately; depends on CW-G4 landing.
- **Cooperative cleanup for non-persistent sessions on host SIGKILL** (pre-existing minor gap): CW-G3 fixed cooperative path only. SIGKILL of host still leaves non-persistent agent idle and child orphan. CW-G4 does not address this; file as CW-G5 if priorities shift. Note: with CW-G4, users who care about survival should explicitly opt into Persistent=true, which sidesteps the SIGKILL gap entirely.
- **CW-G3.1** (host-side Manager state accumulation at iter ~20): persistent sessions use per-session UNIX sockets, not the Manager-shared spawner state path. CW-G4's chaos test will validate whether persistent-burst sidesteps the limit. If yes, CW-G3.1 may be effectively obsoleted for persistent workloads.
- **CW-P2-3** (multi-host attach): CW-G4 explicitly rejects concurrent attach. Multi-host is a future evolution that would replace `ErrAlreadyAttached` with a multiplexer; CW-G4's single-host-attach contract makes that a clean future add.
- **CW-P2-2** (VT cell-buffer emulation): CW-G4 ships raw bytes via ring buffer. If real users observe redraw artifacts (color bleed, cursor confusion), CW-P2-2 promotes to active.
