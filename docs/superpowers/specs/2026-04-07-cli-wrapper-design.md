# cli-wrapper Design Specification

**Date:** 2026-04-07
**Status:** Draft — in progress
**Owner:** TBD

## Overview

`cli-wrapper` is a production-grade Go library (with a companion management CLI) that supervises arbitrary CLI programs as background child processes on macOS and Linux. It provides lifecycle management, log collection, resource control, sandboxed execution, and a reliable command/event API for host applications that embed it.

### Goals

- Supervise any CLI program with minimal configuration
- Safely run multiple background processes from a single host application
- Guarantee no message loss between the host application and its subprocesses
- Prevent memory leaks across start/stop/crash cycles
- Collect stdout/stderr with a debuggable log pipeline
- Provide pluggable sandbox isolation
- Ship a companion CLI (`cliwrap`) for standalone management

### Non-Goals (for v1)

- Windows support (roadmap only)
- Built-in orchestration across hosts (single-host only)
- GUI or web dashboard
- Writing or linking any code derived from tmux (inspiration only, no code reuse)

### Platform Support

| Platform | Status |
|----------|--------|
| Linux (x86_64, arm64) | First-class |
| macOS (x86_64, arm64) | First-class |
| Windows | Roadmap, out of scope for v1 |

---

## Section 1 — System Architecture & Package Layout

### 1.1 High-Level Architecture

The library is embedded directly in the host application process. Each managed CLI program runs inside its own **agent subprocess** (a binary shipped with the library, `cliwrap-agent`). The host application communicates with each agent over a dedicated Unix Domain Socket using a framed MessagePack protocol.

```
┌──────────────────────── Host Application Process ────────────────────────┐
│                                                                           │
│  ┌─────────────────────── cli-wrapper Library (Go) ──────────────────┐   │
│  │                                                                    │   │
│  │  ┌──────────┐   ┌────────────┐   ┌──────────────┐  ┌───────────┐  │   │
│  │  │ Manager  │◄──┤  EventBus  │◄──┤   Registry   │  │  Config   │  │   │
│  │  │  API     │   │ (channels) │   │ (processes)  │  │  Loader   │  │   │
│  │  └────┬─────┘   └─────▲──────┘   └──────┬───────┘  └───────────┘  │   │
│  │       │               │                 │                         │   │
│  │  ┌────▼───────────────┴────┐  ┌─────────▼──────┐  ┌────────────┐  │   │
│  │  │  Agent Controllers      │  │ Resource       │  │ Log        │  │   │
│  │  │  (one per agent proc)   │  │ Monitor        │  │ Collector  │  │   │
│  │  └────┬────────────────────┘  └────────────────┘  └─────▲──────┘  │   │
│  │       │                                                   │       │   │
│  │       │ Unix Domain Socket (per agent)                    │       │   │
│  │       │ Framed MessagePack Protocol                       │       │   │
│  │       ▼                                                   │       │   │
│  └───────┼───────────────────────────────────────────────────┼───────┘   │
│          │                                                   │           │
└──────────┼───────────────────────────────────────────────────┼───────────┘
           │                                                   │
    ┌──────▼───────────┐                               ┌───────┴──────┐
    │  cliwrap-agent   │                               │   Log Files  │
    │  (subprocess)    │                               │   + RingBuf  │
    │                  │                               └──────────────┘
    │  ┌────────────┐  │
    │  │ Child CLI  │  │   (optionally inside a Sandbox)
    │  │  Process   │  │
    │  └────────────┘  │
    └──────────────────┘
```

### 1.2 Core Components

| Component | Responsibility |
|-----------|----------------|
| **Manager** | Public entry point; exposes `Start/Stop/List/Subscribe` and owns the Registry |
| **Registry** | Holds the authoritative map of `processID → ManagedProcess` state |
| **Agent Controller** | One per agent process; owns the UDS connection and reader/writer goroutines |
| **EventBus** | In-process pub/sub channel system for lifecycle and error events |
| **Resource Monitor** | Samples RSS/CPU for every agent's child and enforces limits |
| **Log Collector** | Consumes `LOG_CHUNK` / `LOG_FILE_REF` messages and writes to ring buffers + rotated files |
| **Config Loader** | Parses YAML configs and converts them into the same structs used by the Go API |
| **cliwrap-agent** (binary) | Executes the actual CLI as a child, collects stdout/stderr, and serves the IPC protocol |
| **cliwrap** (CLI binary) | Standalone management tool; embeds the library and talks to the host via YAML configs |

### 1.3 Process Topology

- **Host application process** — embeds the library. Contains the Manager, all Agent Controllers, and supporting goroutines.
- **Agent processes** — one per managed CLI. The library spawns these; they inherit a UDS fd via `SocketPair` or connect to a per-agent UDS path under the instance's runtime directory.
- **Child CLI processes** — the actual user-defined CLI. Each is a direct child of its agent and never a direct child of the host.

This topology ensures:

- Agent crashes are isolated from the host (host survives agent panics)
- Child CLI output never goes through the host's process tree
- Sandbox providers operate on the agent-to-child boundary

### 1.4 Package Layout

```
cli-wrapper/
├── go.mod
├── Makefile
├── README.md
├── LICENSE
│
├── pkg/                          # Public API
│   ├── cliwrap/                  # Top-level library entry point
│   │   ├── manager.go            # Manager struct and public API
│   │   ├── options.go            # Functional options
│   │   ├── process.go            # ProcessHandle returned to users
│   │   └── errors.go             # Public error types
│   ├── config/                   # Config definitions and YAML loader
│   │   ├── config.go
│   │   ├── loader.go
│   │   └── schema.go
│   ├── event/                    # Event types and EventBus interface
│   │   ├── event.go
│   │   └── bus.go
│   └── sandbox/                  # Sandbox plugin interface
│       ├── provider.go
│       └── script.go
│
├── internal/                     # Internal implementation
│   ├── agent/                    # Agent-side logic
│   │   ├── main.go
│   │   ├── runner.go
│   │   └── dispatcher.go
│   ├── ipc/                      # IPC protocol
│   │   ├── frame.go              # Length-prefixed framing
│   │   ├── codec.go              # MessagePack encoding
│   │   ├── message.go            # Message type definitions
│   │   ├── outbox.go             # Persistent outbox + WAL spill
│   │   └── conn.go               # UDS connection management
│   ├── controller/               # Host-side per-agent controller
│   │   ├── controller.go
│   │   ├── reader.go
│   │   └── writer.go
│   ├── supervise/                # Process supervision
│   │   ├── spawner.go
│   │   ├── watcher.go
│   │   └── restart.go
│   ├── logcollect/               # Log collection pipeline
│   │   ├── ringbuf.go
│   │   ├── rotator.go
│   │   └── sink.go
│   ├── resource/                 # Resource monitoring
│   │   ├── monitor.go
│   │   ├── collector_linux.go
│   │   ├── collector_darwin.go
│   │   └── limits.go
│   └── platform/                 # OS abstraction
│       ├── platform.go
│       ├── platform_linux.go
│       └── platform_darwin.go
│
├── cmd/                          # Binaries
│   ├── cliwrap/                  # Management CLI
│   │   ├── main.go
│   │   ├── cmd_start.go
│   │   ├── cmd_stop.go
│   │   ├── cmd_list.go
│   │   ├── cmd_logs.go
│   │   └── cmd_attach.go
│   └── cliwrap-agent/
│       └── main.go
│
├── examples/
│   ├── basic/
│   └── with-sandbox/
│
├── docs/
│   └── superpowers/specs/
│
└── test/
    ├── integration/
    └── fixtures/
```

### 1.5 Package Boundary Rules

- `pkg/` is the stable public API and follows semver. Host applications import from here.
- `internal/` may change at any time. All integration tests exercise `internal/` via the public surface.
- Agent and library share only `internal/ipc`; agent must not import anything from `pkg/cliwrap`.
- `cmd/cliwrap-agent` is compiled as a separate binary and placed next to the library or discovered via `PATH` / `CLIWRAP_AGENT_PATH`.

---

## Section 2 — Data Model & IPC Protocol

### 2.1 Conceptual Hierarchy

```
Manager (one per Host App)
  └── ProcessGroup (logical grouping, e.g., "backend-services")
       └── ManagedProcess (individual CLI process)
            ├── AgentLink (IPC connection state)
            ├── Spec (immutable configuration)
            ├── Status (mutable runtime state)
            └── LogStream (ring buffer handle)
```

`ProcessGroup` is a lightweight label used for bulk operations (start/stop a group, subscribe to group events). It does not create a separate process or supervision domain.

### 2.2 Core Types

#### Spec (immutable configuration)

```go
// pkg/cliwrap/process.go
type Spec struct {
    ID             string
    Name           string
    Command        string
    Args           []string
    Env            map[string]string
    WorkDir        string
    Restart        RestartPolicy
    MaxRestarts    int
    RestartBackoff time.Duration
    Sandbox        *SandboxSpec
    Resources      ResourceLimits
    LogOptions     LogOptions
    StopTimeout    time.Duration
    Stdin          StdinMode
}

type RestartPolicy int

const (
    RestartNever RestartPolicy = iota
    RestartOnFailure
    RestartAlways
)

type ResourceLimits struct {
    MaxRSS        uint64
    MaxCPUPercent float64
    OnExceed      ExceedAction
}

type ExceedAction int

const (
    ExceedWarn ExceedAction = iota
    ExceedThrottle
    ExceedKill
)

type StdinMode int

const (
    StdinNone    StdinMode = iota // /dev/null
    StdinPipe                     // agent forwards bytes from host on request
    StdinInherit                  // inherit agent's stdin (rarely useful)
)

type SandboxSpec struct {
    Provider string         // registered provider name
    Config   map[string]any // provider-specific configuration
}

type LogOptions struct {
    RingBufferBytes         uint64         // default 4 MiB per stream
    FileRotation            *FileRotationOptions
    PreserveWALOnShutdown   bool
}

type FileRotationOptions struct {
    Dir      string
    MaxSize  int64 // default 64 MiB
    MaxFiles int   // default 7
    Compress bool  // gzip on rotate
}
```

#### Status (mutable runtime state)

```go
type State int

const (
    StatePending State = iota
    StateStarting
    StateRunning
    StateStopping
    StateStopped
    StateCrashed
    StateRestarting
    StateFailed
)

type Status struct {
    State         State
    AgentPID      int
    ChildPID      int
    StartedAt     time.Time
    LastExitAt    time.Time
    LastExitCode  int
    RestartCount  int
    LastError     string
    PendingReason string  // populated when State == StatePending
    RSS           uint64
    CPUPercent    float64
}
```

#### ProcessHandle (user-facing handle)

```go
type ProcessHandle interface {
    ID() string
    Spec() Spec
    Status() Status
    Stop(ctx context.Context) error
    SendSignal(sig os.Signal) error
    SendStdin(data []byte) error
    Logs() LogReader
    Subscribe() <-chan event.Event
}
```

### 2.3 IPC Framing

Every IPC frame carries a fixed 17-byte header followed by a MessagePack-encoded payload:

```
┌─────────┬─────────┬─────────┬─────────┬────────┬──────────┬──────────────┐
│ Magic   │ Version │ MsgType │ Flags   │ SeqNo  │ Length   │ Payload      │
│ 2 bytes │ 1 byte  │ 1 byte  │ 1 byte  │ 8 bytes│ 4 bytes  │ N bytes      │
│ 0xC1 D9 │ 0x01    │ enum    │ bitmask │ be u64 │ be u32   │              │
└─────────┴─────────┴─────────┴─────────┴────────┴──────────┴──────────────┘
```

| Field | Size | Purpose |
|-------|------|---------|
| `Magic` | 2 bytes | Sync marker (`0xC1 0xD9`). Mismatch closes the connection. |
| `Version` | 1 byte | Protocol major version. Bump on breaking changes. |
| `MsgType` | 1 byte | Message type enum (see catalog below). |
| `Flags` | 1 byte | `ACK_REQUIRED=0x01`, `IS_REPLAY=0x02`, `FILE_REF=0x04` |
| `SeqNo` | 8 bytes | Monotonic sender-assigned sequence number. |
| `Length` | 4 bytes | Payload length in bytes. Max 16 MiB. |
| `Payload` | N bytes | MessagePack-encoded struct matching the `MsgType`. |

Frames larger than 16 MiB are rejected. Oversized data must use the `FILE_REF` mechanism described in §2.6.

### 2.4 Message Catalog

**Control plane (host → agent)**

| Type | Name | Purpose |
|------|------|---------|
| 0x01 | `HELLO` | Handshake (protocol version, agent ID) |
| 0x02 | `START_CHILD` | Deliver `Spec` and instruct agent to exec the child |
| 0x03 | `STOP_CHILD` | Request graceful termination with timeout |
| 0x04 | `SIGNAL_CHILD` | Send a specific signal to the child |
| 0x05 | `WRITE_STDIN` | Forward bytes to child's stdin |
| 0x06 | `SET_LOG_LEVEL` | Toggle debug mode |
| 0x07 | `PING` | Heartbeat request |
| 0x08 | `SHUTDOWN` | Graceful agent termination |
| 0x0A | `ACK` | Acknowledge a received data-plane message (`seq_ack` in payload) |
| 0x0B | `RESUME` | On reconnect, exchange last-seen sequence numbers |

**Data plane (agent → host)**

| Type | Name | Purpose |
|------|------|---------|
| 0x81 | `HELLO_ACK` | Handshake reply with capabilities |
| 0x82 | `CHILD_STARTED` | Child exec succeeded (`pid` in payload) |
| 0x83 | `CHILD_EXITED` | Child exited (exit code + signal + reason) |
| 0x84 | `LOG_CHUNK` | stdout/stderr fragment (inline, <= 64 KiB) |
| 0x85 | `LOG_FILE_REF` | Reference to a log file when throughput is high |
| 0x86 | `CHILD_ERROR` | Exec failure or runtime error from the agent |
| 0x87 | `PONG` | Heartbeat reply |
| 0x88 | `RESOURCE_SAMPLE` | RSS/CPU sample |
| 0x89 | `AGENT_FATAL` | Agent is about to terminate due to internal error |
| 0x8A | `ACK` | Acknowledge a received control-plane message |

### 2.5 Representative Payloads

```go
// internal/ipc/message.go

type StartChildPayload struct {
    Command     string            `msgpack:"cmd"`
    Args        []string          `msgpack:"args"`
    Env         map[string]string `msgpack:"env"`
    WorkDir     string            `msgpack:"wd"`
    StdinMode   uint8             `msgpack:"stdin"`
    StopTimeout int64             `msgpack:"stop_to_ms"`
}

type ChildExitedPayload struct {
    PID      int32  `msgpack:"pid"`
    ExitCode int32  `msgpack:"code"`
    Signal   int32  `msgpack:"sig"`
    ExitedAt int64  `msgpack:"at"`
    Reason   string `msgpack:"reason"`
}

type LogChunkPayload struct {
    Stream uint8  `msgpack:"s"` // 0=stdout, 1=stderr, 2=diag (debug-mode only)
    SeqNo  uint64 `msgpack:"n"` // monotonic per-stream sequence
    Data   []byte `msgpack:"d"`
}

type LogFileRefPayload struct {
    Stream   uint8  `msgpack:"s"`
    Path     string `msgpack:"p"`
    Size     uint64 `msgpack:"sz"`
    Checksum string `msgpack:"ck"` // blake3
}

type AckPayload struct {
    AckedSeq uint64 `msgpack:"ack"`
}
```

### 2.6 Large Payload Handling

1. **Inline mode (default)**: each `LOG_CHUNK` carries at most 64 KiB of payload.
2. **Burst detection**: if an agent buffers more than 1 MiB of child output within a 1-second window, it switches to File mode for that stream.
3. **File mode**: the agent writes the stream to an append-only file under `<runtime_dir>/logs/<process_id>/<stream>.<seq>.log` and sends a `LOG_FILE_REF` referencing that file. The host ingests the file into its ring buffer / rotator, then deletes it.
4. **Checksum**: every `LOG_FILE_REF` includes a blake3 checksum. The host validates before ingestion.
5. **Fall-back**: if the host cannot access the file (ENOENT, checksum mismatch), the agent is asked to re-deliver via inline chunks.

### 2.7 Reliability — Persistent Outbox with Delayed Delivery

Both endpoints (host-side Controller and agent-side Dispatcher) implement a **persistent outbox** to guarantee zero message loss, even under back-pressure or connection loss.

```
┌─ Sender Side (Host or Agent) ────────────────────────┐
│                                                       │
│  Producer                                             │
│     │                                                 │
│     ▼                                                 │
│  ┌─────────────────────┐                             │
│  │ In-Memory Outbox    │ ← bounded channel (4096)    │
│  │ (bounded channel)   │                             │
│  └────────┬────────────┘                             │
│           │  if full ─────────┐                      │
│           ▼                   ▼                      │
│     ┌──────────┐       ┌─────────────────┐          │
│     │ Sender   │       │ Spill Writer    │          │
│     │ Goroutine│       │ (append-only    │          │
│     └────┬─────┘       │  WAL file)      │          │
│          │             └─────────────────┘          │
│          │                    ▲                      │
│          │                    │ replay when drained  │
│          ▼                    │                      │
│       UDS conn                │                      │
│          │                    │                      │
│          │  send failure ─────┘                      │
│          │  (conn down)                              │
│          ▼                                           │
│     awaits ACK                                       │
└───────────────────────────────────────────────────────┘
```

#### Semantics

1. **Normal path**: Producer → in-memory bounded outbox → Sender goroutine → UDS → receiver ACK.
2. **Outbox full**: when the bounded channel is full, the Spill Writer immediately appends the message to an on-disk WAL file (`<runtime_dir>/outbox/<endpoint>/<seq>.wal`). The producer is never blocked.
3. **Drain resume**: when the in-memory outbox drains, the Spill Writer replays WAL entries in `SeqNo` order into the in-memory outbox (delayed delivery).
4. **Connection loss**: un-ACKed messages are moved back into the WAL for durability. On reconnect, a `RESUME` handshake exchanges last-seen `SeqNo`, and the sender replays any missing messages with the `IS_REPLAY` flag set.
5. **Retirement**: a WAL entry is retired only after the receiver returns an `ACK` carrying the original `SeqNo`.
6. **Crash recovery**: if an agent restarts but its WAL directory survives, the host-side Controller replays the WAL against the new agent after the initial handshake.
7. **WAL size cap**: the WAL has a configurable size ceiling (default 256 MiB). On overflow, the library emits a `BackpressureStall` event and begins blocking producers — this is explicit, observable back-pressure rather than silent loss.

#### Duplicate Handling

Because the sender may replay messages after reconnect, receivers must be **idempotent on `SeqNo`**. Each receiver tracks the highest `SeqNo` it has processed per peer and discards duplicates transparently.

#### ACK Requirements

- All data-plane messages from the agent set `ACK_REQUIRED=0x01`.
- All control-plane messages that mutate state (`START_CHILD`, `STOP_CHILD`, `SIGNAL_CHILD`, `WRITE_STDIN`, `SHUTDOWN`) set `ACK_REQUIRED=0x01`.
- `PING`/`PONG` and `RESOURCE_SAMPLE` do not require ACK (lossy is acceptable).

### 2.8 Reliability — Heartbeats and Liveness

- Host sends `PING` every 5 seconds.
- Agent must reply with `PONG` within 2 seconds.
- 3 consecutive missed `PONG`s → agent is considered dead → Controller closes the UDS, triggers crash-detection flow (§Section 3).
- The same applies in reverse: agents monitor `PING` arrival and self-terminate after a timeout so they never become orphaned if the host crashes.

---

---

## Section 3 — Lifecycle Management & Crash Detection

### 3.1 State Machine

```
         ┌──────────┐
         │ Pending  │  ← immediately after Register()
         └────┬─────┘
              │ Start()
              ▼
         ┌──────────┐
         │ Starting │  ← agent spawn + handshake
         └────┬─────┘
              │ CHILD_STARTED
              ▼
         ┌──────────┐
  ┌──────┤ Running  │──────┐
  │      └────┬─────┘      │
  │           │            │
  │           │ Stop()     │ CHILD_EXITED
  │           │            │ (exit≠0 or signal)
  │           ▼            ▼
  │      ┌──────────┐  ┌──────────┐
  │      │ Stopping │  │ Crashed  │
  │      └────┬─────┘  └────┬─────┘
  │           │             │
  │           │ CHILD_EXITED│ consult RestartPolicy
  │           ▼             │
  │      ┌──────────┐       │
  │      │ Stopped  │       │
  │      └──────────┘       │
  │                         │
  │   ┌─────────────────────┴─── no restart / over limit
  │   ▼                          ▼
  │ ┌──────────┐            ┌────────┐
  │ │Restarting│            │ Failed │
  │ └────┬─────┘            └────────┘
  │      │ backoff elapsed
  └──────┘ → back to Starting
```

#### Transition Rules

| From | To | Trigger |
|------|-----|---------|
| Pending → Starting | `Start()` invoked and the system budget allows |
| Pending → Stopped | `Stop()` invoked before any start (no-op cleanup) |
| Starting → Running | `CHILD_STARTED` received and ACKed |
| Starting → Stopping | `Stop()` invoked mid-start (Controller aborts the handshake) |
| Starting → Crashed | Spawn failed or agent fatal |
| Running → Stopping | `Stop()` invoked |
| Running → Crashed | `CHILD_EXITED` with non-zero exit or signal |
| Stopping → Stopped | `CHILD_EXITED` after SIGTERM |
| Stopping → Crashed | StopTimeout exceeded, SIGKILL required (policy-dependent) |
| Crashed → Restarting | RestartPolicy permits and MaxRestarts not reached |
| Crashed → Failed | RestartPolicy disallows or MaxRestarts reached |
| Restarting → Starting | Backoff elapsed |
| Restarting → Stopped | `Stop()` invoked while waiting for backoff |
| * → Stopped | `Manager.Shutdown()` (explicit user termination) |

### 3.2 Start Sequence

```
Host                           Agent
 │                                │
 │── Spawn agent proc ────────────▶
 │   (UDS fd via socketpair)       │
 │                                │
 │◀─── HELLO ─────────────────────│
 │── HELLO_ACK ───────────────────▶
 │                                │
 │── START_CHILD(Spec) ───────────▶
 │                                │─── fork/exec child
 │                                │
 │◀─── CHILD_STARTED(pid) ────────│
 │── ACK(seq) ────────────────────▶
 │                                │
 │   state: Starting → Running     │
 │                                │
 │◀─── LOG_CHUNK ×N ──────────────│
 │── ACK(seq) ×N ─────────────────▶
```

#### Implementation Details

1. Manager calls `supervise/spawner.go` to locate the agent binary:
   - Priority: `CLIWRAP_AGENT_PATH` env var > same directory as host executable > `PATH`
2. `socketpair(AF_UNIX, SOCK_STREAM, 0)` creates the fd pair. The agent-side fd is passed via `exec.Cmd.ExtraFiles`.
3. Inside the agent, `os.NewFile(3, "ipc")` wraps the inherited fd directly — no filesystem path is used, avoiding path-based attack vectors.
4. Handshake timeout is 3 seconds by default. Missed handshake transitions Starting → Crashed.
5. `START_CHILD` → `CHILD_STARTED` has a 5-second default timeout.

### 3.3 Stop Sequence

```
Host                           Agent                 Child
 │                                │                     │
 │── STOP_CHILD(timeout) ─────────▶                     │
 │                                │── SIGTERM ──────────▶
 │   state: Running → Stopping     │                     │
 │                                │                     │
 │                                │◀── exit ────────────│
 │◀─── CHILD_EXITED ──────────────│                     │
 │── ACK(seq) ────────────────────▶                     │
 │                                │                     │
 │── SHUTDOWN ────────────────────▶                     │
 │◀─── agent exits ───────────────┘                     │
 │                                                      │
 │  state: Stopping → Stopped                           │
 │  ProcessHandle remains valid (can be restarted)       │
```

#### `Stop()` Contract

- Default path: SIGTERM → wait StopTimeout → SIGKILL
- `StopTimeout` defaults to 10 seconds, configurable per Spec
- On SIGTERM → SIGKILL escalation, emits `CHILD_ERROR(reason="stop_timeout")`
- `Stop()` is blocking but honors `ctx` cancellation; on cancel it returns immediately while cleanup continues in background

### 3.4 Crash Detection — 4-Layer Defense

The host application must never miss a subprocess crash. Four overlapping layers ensure detection:

#### Layer 1 — Explicit Notification

Normally the agent sends `CHILD_EXITED`. This is the fastest path and provides the richest information (exit code, signal, reason).

#### Layer 2 — UDS Connection Close

If the agent itself panics or is SIGKILLed, the UDS connection closes. The Controller's reader goroutine detects `io.EOF` or `ECONNRESET` and immediately transitions the process to `StateCrashed`, emitting an `AgentFatal` event.

#### Layer 3 — Heartbeat Timeout

Three consecutive missed `PONG` replies indicates the agent is hung or deadlocked. The Controller forcibly kills the agent with `Process.Kill()` and falls through to Layer 2.

#### Layer 4 — OS-level Process Wait

`supervise/watcher.go` runs a dedicated goroutine that blocks on `os.Process.Wait()`. This collects the agent's OS-level exit status (exit code, received signal). This is the final source of truth: if any other layer misses a crash, this one always catches it.

#### Unified Crash Info

```go
type CrashSource int

const (
    CrashSourceExplicit CrashSource = iota
    CrashSourceConnectionLost
    CrashSourceHeartbeat
    CrashSourceOSWait
)

type CrashInfo struct {
    Source     CrashSource
    AgentExit  int    // filled by Layer 4
    AgentSig   int
    ChildExit  int    // filled by Layer 1 when possible
    ChildSig   int
    Reason     string
    DetectedAt time.Time
}
```

The Controller uses the earliest-arriving layer's data but always augments it with Layer 4's final OS wait result.

### 3.5 Restart Policy & Backoff

```go
// internal/supervise/restart.go

type BackoffStrategy interface {
    Next(attempt int) time.Duration
}

// Default: exponential backoff with jitter
type ExponentialBackoff struct {
    Initial    time.Duration // default 1s
    Max        time.Duration // default 60s
    Multiplier float64       // default 2.0
    Jitter     float64       // default 0.2 (±20%)
}
```

#### Restart Decision

```
CHILD_EXITED received
    │
    ▼
Check RestartPolicy
    ├── Never     → StateFailed
    ├── OnFailure → exit==0 ? StateStopped : restart flow
    └── Always    → restart flow
        │
        ▼
RestartCount < MaxRestarts?
    ├── No  → StateFailed + emit event
    └── Yes →
        │
        ▼
    StateRestarting
    wait backoff.Next(RestartCount)
    (if ctx canceled, transition to StateFailed)
    │
    ▼
    Re-Start() using the same Spec
    RestartCount++
```

#### Leak Prevention During Restart

- The previous agent's resources (UDS, goroutines, WAL) must be fully drained before spawning the new agent.
- `Controller.Close()` is idempotent and uses `sync.WaitGroup` to wait for reader/writer/heartbeat/watcher goroutines.
- WAL files are **not** deleted on restart — they are replayed to the new agent after its handshake.

### 3.6 Graceful Shutdown

When `Manager.Shutdown(ctx)` is called:

1. Iterate over the Registry and call `Stop()` on every `ManagedProcess` in parallel.
2. `errgroup.WithContext(ctx)` waits for all shutdowns.
3. If `ctx` expires, remaining agents receive `SIGKILL` (graceful failure accepted).
4. WAL directories are cleaned up according to `LogOptions.PreserveWALOnShutdown`.
5. EventBus closes, Resource Monitor stops, Log Collector flushes.

**Guarantee**: if `Shutdown()` returns without error, **zero child processes remain**. If `ctx` forces an early return, the library performs best-effort cleanup and emits `ShutdownIncomplete` events for any processes that could not be stopped cleanly.

### 3.7 Goroutine Leak Prevention

Per-agent goroutines:

| Goroutine | Role | Exit Condition |
|-----------|------|----------------|
| `Controller.reader` | Reads frames from UDS | Conn close or ctx cancel |
| `Controller.writer` | Drains outbox to UDS | Outbox close or ctx cancel |
| `Controller.heartbeat` | Sends periodic PING | Ctx cancel |
| `Controller.watcher` | Blocks on `os.Process.Wait()` | Agent exit |
| `SpillWriter` | Manages WAL | Explicit `Close()` |

#### Rules

1. Every goroutine receives a `context.Context` and has a `<-ctx.Done()` branch.
2. The Controller owns the root context; `Close()` cancels it.
3. `sync.WaitGroup` in `Close()` waits for all goroutines to exit.
4. Tests use `go.uber.org/goleak.VerifyNone(t)` to detect leaks. This is a required CI gate.

---

---

## Section 4 — Event System & Log Collection Pipeline

### 4.1 Event System

#### 4.1.1 Event Type Catalog

All events are defined in `pkg/event` and use a sealed interface pattern so only types from that package can implement `Event`.

```go
// pkg/event/event.go
type Event interface {
    EventType() Type
    ProcessID() string
    Timestamp() time.Time
    sealed() // unexported marker
}

type Type string

const (
    // Lifecycle events
    TypeProcessRegistered Type = "process.registered"
    TypeProcessStarting   Type = "process.starting"
    TypeProcessStarted    Type = "process.started"
    TypeProcessStopping   Type = "process.stopping"
    TypeProcessStopped    Type = "process.stopped"
    TypeProcessCrashed    Type = "process.crashed"
    TypeProcessRestarting Type = "process.restarting"
    TypeProcessFailed     Type = "process.failed"

    // Agent events
    TypeAgentSpawned      Type = "agent.spawned"
    TypeAgentFatal        Type = "agent.fatal"
    TypeAgentDisconnected Type = "agent.disconnected"

    // Log events
    TypeLogChunk    Type = "log.chunk"
    TypeLogFileRef  Type = "log.file_ref"
    TypeLogOverflow Type = "log.overflow"

    // Resource events
    TypeResourceSample      Type = "resource.sample"
    TypeResourceLimitWarn   Type = "resource.limit_warn"
    TypeResourceLimitKilled Type = "resource.limit_killed"

    // Reliability events
    TypeBackpressureStall Type = "reliability.backpressure_stall"
    TypeWALReplayed       Type = "reliability.wal_replayed"
    TypeDuplicateDropped  Type = "reliability.duplicate_dropped"

    // System events
    TypeShutdownRequested  Type = "system.shutdown_requested"
    TypeShutdownIncomplete Type = "system.shutdown_incomplete"
)
```

#### 4.1.2 Representative Event Structures

```go
type baseEvent struct {
    processID string
    ts        time.Time
}

func (e baseEvent) ProcessID() string    { return e.processID }
func (e baseEvent) Timestamp() time.Time { return e.ts }
func (e baseEvent) sealed()               {}

type ProcessStartedEvent struct {
    baseEvent
    AgentPID int
    ChildPID int
}
func (ProcessStartedEvent) EventType() Type { return TypeProcessStarted }

type ProcessCrashedEvent struct {
    baseEvent
    CrashInfo     CrashInfo
    WillRestart   bool
    NextAttemptIn time.Duration
}
func (ProcessCrashedEvent) EventType() Type { return TypeProcessCrashed }

type ResourceLimitWarnEvent struct {
    baseEvent
    Kind    string // "rss" | "cpu"
    Current float64
    Limit   float64
    Action  ExceedAction
}
func (ResourceLimitWarnEvent) EventType() Type { return TypeResourceLimitWarn }

type BackpressureStallEvent struct {
    baseEvent
    Endpoint    string // "host" | "agent"
    WALSize     uint64
    WALSizeMax  uint64
    DroppedMsgs int
}
```

#### 4.1.3 EventBus Architecture

```
                    ┌──────────────────────────┐
                    │      EventBus            │
                    │                          │
Publishers ─────────▶   publish(event)         │
(Controller,        │        │                 │
 ResourceMonitor,   │        ▼                 │
 LogCollector)      │   Dispatcher goroutine   │
                    │        │                 │
                    │        ├─────────────┐   │
                    │        ▼             ▼   │
                    │   Subscriber 1  Sub N    │
                    │   (channel)     (...)    │
                    └──────────────────────────┘
```

```go
// pkg/event/bus.go
type Bus interface {
    // Subscribe returns a channel that receives events matching the filter.
    // The returned channel is buffered (default 256). Callers must Close()
    // the subscription or drain it; a subscriber that cannot keep up
    // receives SlowSubscriberDroppedEvent and is eventually disconnected.
    Subscribe(filter Filter) Subscription

    // Publish is non-blocking. If the internal dispatcher queue is full,
    // the event is spilled to the WAL (same mechanism as IPC outbox).
    Publish(e Event)
}

type Filter struct {
    Types      []Type
    ProcessIDs []string
    GroupNames []string
}

type Subscription interface {
    Events() <-chan Event
    Close() error
}
```

#### 4.1.4 Slow Subscriber Policy

- Each subscription owns a bounded channel (default 256).
- The dispatcher writes non-blockingly. A full channel increments `dropCount`.
- After 10 consecutive dropped events, the bus emits `SlowSubscriberDroppedEvent` and closes the subscription.
- This guarantees **one slow consumer can never block the entire dispatcher**.

#### 4.1.5 Event Persistence (WAL)

- The publish path has its own WAL to avoid event loss when no subscribers exist yet or all subscribers are slow.
- Default capacity 64 MiB. Order is always preserved — there is no LIFO drop policy.
- `Manager.Shutdown()` waits for the WAL to flush.

### 4.2 Log Collection Pipeline

#### 4.2.1 End-to-End Flow

```
┌─ Agent Process ──────────────────────────┐
│                                           │
│  Child stdout ────┐                       │
│                   ▼                       │
│              ┌─────────┐                  │
│              │ Line    │ ◀── used only in │
│              │ Splitter│     debug mode   │
│              └────┬────┘                  │
│                   ▼                       │
│              ┌──────────┐                 │
│   Burst ─────▶ Burst    │                 │
│   detector   │ Buffer   │                 │
│              └────┬─────┘                 │
│                   │                       │
│    ┌──────────────┴──────────────┐        │
│    ▼                             ▼        │
│ ┌──────────┐              ┌──────────┐    │
│ │ Inline   │              │ File     │    │
│ │ Encoder  │              │ Writer   │    │
│ │ LOG_CHUNK│              │ LOG_FILE_│    │
│ └────┬─────┘              │  REF     │    │
│      │                    └────┬─────┘    │
│      └────────┬────────────────┘          │
│               ▼                           │
│          IPC Outbox                       │
└───────────────┼───────────────────────────┘
                │ UDS
                ▼
┌─ Host Process ──────────────────────────┐
│                                          │
│   Controller.reader                      │
│         │                                │
│         ▼                                │
│   Log Collector                          │
│         │                                │
│   ┌─────┴──────┐                         │
│   ▼            ▼                         │
│ ┌──────┐  ┌──────────┐                   │
│ │Ring  │  │ File     │                   │
│ │Buffer│  │ Rotator  │                   │
│ └───┬──┘  └────┬─────┘                   │
│     │          │                         │
│     ▼          ▼                         │
│  LogReader  Rotated files                │
│  (users)    (disk)                       │
│                                          │
│   EventBus.Publish(LogChunk/LogFileRef)  │
└──────────────────────────────────────────┘
```

#### 4.2.2 Agent-Side Stream Reader

```go
// internal/agent/runner.go
type StreamReader struct {
    stream     uint8   // 0=stdout, 1=stderr, 2=diag (debug mode)
    src        io.Reader
    inline     chan<- LogChunkPayload // to IPC outbox
    fileWriter *FileWriter            // for File Mode
    burst      *BurstDetector
    seq        atomic.Uint64
}
```

Behavior:

1. The agent obtains pipes via `exec.Cmd.StdoutPipe()` / `StderrPipe()`.
2. Each stream is read by a dedicated goroutine with a fixed 64 KiB buffer.
3. Line boundaries are **not** preserved — raw chunks are sent. Host-side callers can split lines if needed.
4. The `BurstDetector` tracks bytes written in a 1-second sliding window.
   - Switches to File Mode when the burst exceeds the threshold (default 1 MiB/s).
   - Returns to Inline Mode after 1 second below the threshold.
5. File Mode writes to `<runtime>/logs/<process>/<stream>-<seq>.log` and sends `LOG_FILE_REF` on completion (path + blake3 checksum).
6. On child exit, the reader flushes any remaining buffer and emits an end-of-stream marker.

#### 4.2.3 Host-Side Ring Buffer

```go
// internal/logcollect/ringbuf.go
type RingBuffer struct {
    mu       sync.RWMutex
    data     []byte   // fixed-size byte slice
    capacity int      // default 4 MiB per stream per process
    head     int      // next write position
    size     int      // bytes currently stored
    entries  []entry  // metadata for each chunk
    lastSeq  uint64
}

type entry struct {
    offset int
    length int
    ts     time.Time
    seq    uint64
}
```

Characteristics:

- Fixed-size byte storage with FIFO eviction of the oldest chunks.
- `Snapshot()` returns a read-only copy of the current buffer.
- `Tail(ctx, n int)` returns the last `n` bytes and then streams new data.
- `Follow(ctx)` streams the current buffer plus every subsequent chunk.
- All three APIs use `sync.Cond` to signal arrivals — no polling.

Memory ceiling:

- Each process has one ring buffer per stream (stdout + stderr).
- Default 4 MiB × 2 = 8 MiB per process.
- Configurable via `LogOptions.RingBufferBytes`.
- Manager-level limit: `GlobalLogRingBufferBytes`.

#### 4.2.4 File Rotator

```go
// internal/logcollect/rotator.go
type FileRotator struct {
    dir         string
    baseName    string // e.g. "redis-stdout"
    maxSize     int64  // default 64 MiB
    maxFiles    int    // default 7
    compress    bool   // default false (gzip on rotate)
    currentFile *os.File
    currentSize int64
    mu          sync.Mutex
}
```

Rotation rules:

- Rotate when the current file exceeds `maxSize`.
- Pattern: `redis-stdout.log` → `redis-stdout.1.log` → ... → `redis-stdout.N.log`.
- When `maxFiles` is exceeded, the oldest file is deleted.
- Optional: gzip compression of rotated files in a background goroutine.
- On disk-write failure: 3 retries with exponential backoff, then emit `LogOverflow` and pause file writes for that process (ring buffer continues).

#### 4.2.5 Sink Interface (Extensibility)

```go
// internal/logcollect/sink.go
type Sink interface {
    Write(entry LogEntry) error
    Flush() error
    Close() error
}

type LogEntry struct {
    ProcessID string
    Stream    uint8
    SeqNo     uint64
    Timestamp time.Time
    Data      []byte
}
```

Built-in sinks:

- `RingBufferSink` — writes to per-process ring buffers.
- `FileSink` — delegates to `FileRotator`.
- `StdoutSink` — in debug mode, prefixes entries and writes to host stdout.

Roadmap (future `pkg/logsink` plugin interface): `SyslogSink`, `JournalSink`, `HTTPSink`.

#### 4.2.6 Debug Mode

```go
type DebugMode int

const (
    DebugOff     DebugMode = iota
    DebugInfo              // basic log collection
    DebugVerbose           // + IPC message logging
    DebugTrace             // + goroutine event logging
)
```

When debug mode is enabled:

1. A `SET_LOG_LEVEL` message is sent to every agent.
2. Agents emit diagnostic data as `LOG_CHUNK(stream=2=diag)` in addition to normal streams.
3. The host's ring buffers are scaled up 4×.
4. IPC frame metadata (MsgType, SeqNo, Size) is recorded in a separate trace log — **payload bytes are never included**.
5. The Resource Monitor sampling interval shortens (5s → 1s).

**Security note**: even in `DebugTrace` mode, payload contents are excluded from logs to avoid leaking sensitive data. Users must explicitly set `DebugLogPayloads = true` to opt in.

---

---

## Section 5 — Resource Monitoring & Sandbox Plugin Interface

### 5.1 Resource Monitoring

#### 5.1.1 Goals

Two layers of oversight are required to keep resource usage bounded (requirements 10 and 11):

1. **Per-process monitoring** — sample RSS, CPU, file descriptors, and thread counts for every child CLI, and enforce per-process limits.
2. **System-wide monitoring** — track total host memory, load average, and reject new starts when the host would be overcommitted.

#### 5.1.2 Architecture

```
┌──────────────── Host Process ────────────────┐
│                                               │
│  ┌───────────────────────────────────────┐   │
│  │ ResourceMonitor                       │   │
│  │                                       │   │
│  │  ┌──────────────┐  ┌────────────────┐ │   │
│  │  │ System       │  │ Process        │ │   │
│  │  │ Collector    │  │ Collector      │ │   │
│  │  │ (host-wide)  │  │ (per child)    │ │   │
│  │  └──────┬───────┘  └────────┬───────┘ │   │
│  │         │                    │         │   │
│  │         ▼                    ▼         │   │
│  │    ┌──────────────────────────┐        │   │
│  │    │ Evaluator                │        │   │
│  │    │  - per-process limits    │        │   │
│  │    │  - system-wide budget    │        │   │
│  │    └────┬─────────────────────┘        │   │
│  │         │                               │   │
│  │         ▼                               │   │
│  │    ┌────────────────┐                  │   │
│  │    │ Action         │──▶ EventBus       │   │
│  │    │ Dispatcher     │──▶ Controller     │   │
│  │    └────────────────┘                   │   │
│  └───────────────────────────────────────┘   │
└────────────────────────────────────────────────┘
```

#### 5.1.3 OS Abstraction

```go
// internal/resource/collector.go
type Sample struct {
    PID        int
    RSS        uint64  // bytes
    VMS        uint64  // bytes
    CPUPercent float64 // 0-100, per-core normalized
    NumThreads int
    NumFDs     int
    SampledAt  time.Time
}

type ProcessCollector interface {
    Collect(pid int) (Sample, error)
}

type SystemSample struct {
    TotalMemory     uint64
    AvailableMemory uint64
    LoadAvg1        float64
    LoadAvg5        float64
    NumCPU          int
    SampledAt       time.Time
}

type SystemCollector interface {
    Collect() (SystemSample, error)
}
```

#### 5.1.4 Implementation Files

- `collector_linux.go` (`//go:build linux`): parses `/proc/<pid>/stat`, `/proc/<pid>/status`, `/proc/meminfo`, `/proc/loadavg`
- `collector_darwin.go` (`//go:build darwin`): uses `libproc` (`proc_pidinfo`) and `sysctl` for `hw.memsize`, `vm.page_free_count`

**External dependencies are intentionally minimized.** We do not use gopsutil because:

- Fewer dependencies reduce supply-chain risk
- We collect only the exact fields we need
- A future move to native (Rust/C) code is a clean replacement point

#### 5.1.5 Sampling Loop

```go
type Monitor struct {
    interval    time.Duration // default 5s, 1s in debug mode
    systemColl  SystemCollector
    procColl    ProcessCollector
    registry    *Registry
    evaluator   *Evaluator
    bus         event.Bus
    done        chan struct{}
}
```

- A single goroutine ticks at `interval`.
- Each tick:
  1. Calls `SystemCollector.Collect()` once.
  2. Snapshots the registry for processes in `StateRunning`.
  3. Calls `ProcessCollector.Collect(childPID)` for each (sequential in v1 for simplicity).
  4. Passes the samples to `Evaluator.Evaluate(...)`.
- Termination is explicit via `close(done)`.

#### 5.1.6 Evaluator

```go
type Evaluator struct {
    specs        map[string]ResourceLimits
    systemBudget SystemBudget
    breachState  map[string]*breachTracker
}

type SystemBudget struct {
    MaxMemoryPercent float64 // default 80
    MinFreeMemory    uint64  // default 512 MiB
    MaxLoadAvg       float64 // default NumCPU × 2
}

type breachTracker struct {
    firstBreach time.Time
    lastAction  time.Time
    count       int
}
```

Enforcement logic:

```
For each per-process sample:
  if sample.RSS > spec.MaxRSS and breach >= 3 consecutive:
      → execute spec.OnExceed
  if sample.CPUPercent > spec.MaxCPUPercent and breach >= 10 consecutive:
      → execute spec.OnExceed

For system-wide samples:
  if AvailableMemory < systemBudget.MinFreeMemory:
      → throttle/kill the highest-RSS process whose RestartPolicy=Never
  if LoadAvg1 > systemBudget.MaxLoadAvg:
      → reject new Start() requests (StartDeferred)
```

The sustained-breach requirement (3 ticks for memory, 10 for CPU) filters transient spikes. CPU requires more ticks because its signal is noisier.

#### 5.1.7 ExceedAction Implementation

| Action | Behavior |
|--------|----------|
| `ExceedWarn` | Emit `ResourceLimitWarnEvent` only; no interference. |
| `ExceedThrottle` | Linux: toggle `SIGSTOP`/`SIGCONT` periodically to limit CPU share. Darwin: no true throttle; SIGSTOP once, then SIGCONT after a fixed delay. |
| `ExceedKill` | Call `Controller.Stop()`; on timeout, SIGKILL. |

**Throttle caveat**: SIGSTOP/SIGCONT is coarse. Some CLIs may corrupt state or drop connections when stopped. `ExceedThrottle` is therefore opt-in and the documentation must warn loudly. Finer-grained throttling via cgroups v2 (`cpu.max`) is on the roadmap; v1 ships with the SIGSTOP approach.

#### 5.1.8 Backpressure on Start

When `Start()` is called, the Evaluator checks the system budget:

```go
if !systemBudget.canAccommodate(estimatedRSS) {
    // Leave the process in StatePending with a PendingReason,
    // enqueue it for retry, and return nil (not an error).
    // The caller's ProcessHandle reflects the deferral.
    queueForLater(handle, "system_memory_budget")
    return nil
}
```

- `estimatedRSS` = `Spec.Resources.MaxRSS` if set, otherwise a default (128 MiB).
- Deferred starts wait in a FIFO queue and are retried when resources free up.
- The process remains in `StatePending`; `Status.PendingReason` carries a human-readable explanation (for example, `"system_memory_budget"` or `"load_avg_exceeded"`).
- `Start()` does not return an error for a deferral — back-pressure is an expected, observable state, not a failure. Callers who want to treat it as an error can check `Status().State == StatePending` after calling `Start()`.

### 5.2 Sandbox Plugin Interface

#### 5.2.1 Goals

Per requirement 5 and the earlier decision:

- Provide a plugin-shaped `SandboxProvider` interface.
- Ship only a minimal reference implementation in the core library.
- Support the pattern of **generating an execution script, copying it into the sandbox, and then running it inside the sandbox**.

#### 5.2.2 Core Interfaces

```go
// pkg/sandbox/provider.go
type Provider interface {
    Name() string
    Prepare(ctx context.Context, spec SandboxSpec, scripts []Script) (Instance, error)
}

type Instance interface {
    ID() string
    Exec(cmd string, args []string, env map[string]string) (*exec.Cmd, error)
    Teardown(ctx context.Context) error
}
```

- `Prepare` allocates the isolated environment (disk, container image, namespace, ...).
- `Exec` returns an `exec.Cmd` that, when run **by the agent**, launches the CLI *inside* the sandbox.
- `Teardown` releases resources. It is called after the child exits, best-effort.

#### 5.2.3 Script Injection

```go
// pkg/sandbox/script.go
type Script struct {
    InnerPath string      // e.g. "/cliwrap/entrypoint.sh"
    Mode      os.FileMode
    Contents  []byte
}

func NewEntrypointScript(cmd string, args []string, env map[string]string) Script {
    var b strings.Builder
    b.WriteString("#!/bin/sh\nset -e\n")
    for k, v := range env {
        fmt.Fprintf(&b, "export %s=%s\n", k, shellQuote(v))
    }
    b.WriteString("exec ")
    b.WriteString(shellQuote(cmd))
    for _, a := range args {
        b.WriteString(" ")
        b.WriteString(shellQuote(a))
    }
    b.WriteString("\n")
    return Script{InnerPath: "/cliwrap/entrypoint.sh", Mode: 0o755, Contents: []byte(b.String())}
}
```

End-to-end flow:

```
agent starts
    │
    ▼
sandbox.Provider.Prepare(spec, scripts)
    │  ↳ provider-specific setup (create dir, pull image, create namespace)
    │  ↳ copy each Script into the sandbox at script.InnerPath
    │
    ▼
instance.Exec(cmd, args, env)
    │  ↳ returns an exec.Cmd that runs the entrypoint inside the sandbox
    │
    ▼
agent spawns the child via this exec.Cmd
    │  ↳ stdout/stderr are still captured by the agent's pipes
    │
    ▼
child exits
    │
    ▼
instance.Teardown()
    │  ↳ remove sandbox dir, destroy namespace, etc.
```

Key properties:

- Scripts are **always** copied into the sandbox — they are self-contained and do not depend on host-visible paths.
- The agent runs **outside** the sandbox; it merely spawns the `exec.Cmd` the provider gives it.
- stdout/stderr capture and the IPC protocol are unchanged regardless of sandbox mode.
- Teardown failures emit events but never block the child's exit handling.

#### 5.2.4 Built-in Providers

```
pkg/sandbox/
├── provider.go
├── script.go
└── providers/
    ├── noop/           # default — no sandbox
    │   └── noop.go
    └── scriptdir/      # reference implementation for the script-injection pattern
        └── scriptdir.go
```

- `noop.Provider` — used when a Spec has no sandbox. `Prepare` is a no-op, `Exec` returns a plain `exec.Command`, `Teardown` is a no-op.
- `scriptdir.Provider` — not a real sandbox, but the reference implementation of the script-injection pattern. Creates a temp directory, copies scripts, runs the entrypoint via `sh`, and removes the directory on teardown. **The documentation clearly states it provides no isolation.**

Real isolation providers (bwrap, nsjail, Docker, Firecracker, ...) live in separate modules so the core library does not drag in heavy dependencies.

#### 5.2.5 Provider Registration

```go
mgr := cliwrap.NewManager(
    cliwrap.WithSandboxProvider(bwrap.New()),
    cliwrap.WithSandboxProvider(docker.New(...)),
)

spec := Spec{
    Command: "risky-cli",
    Sandbox: &SandboxSpec{
        Provider: "bwrap",
        Config:   map[string]any{...}, // provider-specific
    },
}
```

If a Spec references a provider name that has not been registered, `Start()` returns `ErrSandboxProviderNotFound`.

#### 5.2.6 Security Notes

- `Script.Contents` is written to disk in plaintext — **never embed secrets** in script contents. Environment variables are also exposed in the process environment; sensitive values must be provided via a separate secret channel and the documentation must make this explicit.
- Providers must create temp directories with mode `0700`.
- Teardown failures leave residual files behind; the library emits a `SandboxTeardownFailed` event so users can reap them.
- `scriptdir` must prominently document that it does not provide isolation.

---

---

## Section 6 — Configuration System & Management CLI

### 6.1 Configuration System

#### 6.1.1 Dual Entry Point, Single Data Model

The library exposes two configuration paths but uses **exactly one canonical data model**:

```
┌─────────────────────────┐       ┌─────────────────────────┐
│ Host App (Go Library)   │       │ cliwrap CLI (binary)    │
│                         │       │                         │
│  cliwrap.Register(      │       │  $ cliwrap start -f     │
│    Spec{...}) ──────┐   │       │        config.yaml     │
│                     │   │       │           │             │
│  Functional options │   │       │           ▼             │
│  cliwrap.WithSpec() │   │       │  config.Load("...")     │
│                     │   │       │           │             │
└─────────────────────┼───┘       │           ▼             │
                      │           │  []Spec                 │
                      │           │           │             │
                      │           │           ▼             │
                      │           │  cliwrap.Register(spec) │
                      │           └───────────┼─────────────┘
                      │                       │
                      └────────┬──────────────┘
                               ▼
                    ┌────────────────────┐
                    │  Spec (canonical)  │
                    │  + validation      │
                    └────────────────────┘
                               │
                               ▼
                    ┌────────────────────┐
                    │  Manager.Register  │
                    └────────────────────┘
```

The YAML loader is a thin adapter that produces `[]Spec`. Validation logic lives once in `Spec.Validate()` and serves both entry points.

#### 6.1.2 Go API (Primary)

```go
// pkg/cliwrap/options.go
type ManagerOption func(*Manager)

func WithRuntimeDir(path string) ManagerOption
func WithAgentPath(path string) ManagerOption
func WithEventBusCapacity(n int) ManagerOption
func WithGlobalLogRingBufferBytes(n uint64) ManagerOption
func WithSystemBudget(b SystemBudget) ManagerOption
func WithSandboxProvider(p sandbox.Provider) ManagerOption
func WithDebugMode(m DebugMode) ManagerOption

// Per-process builder
type SpecBuilder struct { /* ... */ }

func NewSpec(id, cmd string, args ...string) *SpecBuilder
func (b *SpecBuilder) WithEnv(k, v string) *SpecBuilder
func (b *SpecBuilder) WithWorkDir(path string) *SpecBuilder
func (b *SpecBuilder) WithRestart(p RestartPolicy) *SpecBuilder
func (b *SpecBuilder) WithMaxRestarts(n int) *SpecBuilder
func (b *SpecBuilder) WithResourceLimits(l ResourceLimits) *SpecBuilder
func (b *SpecBuilder) WithSandbox(s *SandboxSpec) *SpecBuilder
func (b *SpecBuilder) WithLogOptions(o LogOptions) *SpecBuilder
func (b *SpecBuilder) Build() (Spec, error)
```

Usage example:

```go
mgr, err := cliwrap.NewManager(
    cliwrap.WithRuntimeDir("/var/run/myapp"),
    cliwrap.WithDebugMode(cliwrap.DebugInfo),
    cliwrap.WithSandboxProvider(bwrap.New()),
)
if err != nil { /* ... */ }
defer mgr.Shutdown(ctx)

spec, err := cliwrap.NewSpec("redis", "redis-server", "--port", "6379").
    WithRestart(cliwrap.RestartOnFailure).
    WithMaxRestarts(5).
    WithResourceLimits(cliwrap.ResourceLimits{
        MaxRSS:   256 << 20,
        OnExceed: cliwrap.ExceedKill,
    }).
    Build()

handle, err := mgr.Register(spec)
if err != nil { /* ... */ }
if err := handle.Start(ctx); err != nil { /* ... */ }
```

API design principles:

- Builders only report errors from `Build()`; intermediate methods do not accumulate errors.
- Functional options are manager-level only; per-process configuration goes through the builder.
- `Register` and `Start` are separate, enabling "define now, start later" patterns.

#### 6.1.3 YAML Schema

```yaml
# config.yaml
version: "1"

runtime:
  dir: "/var/run/cliwrap"
  debug: info                # off | info | verbose | trace
  agent_path: ""             # empty = auto-discovery

system_budget:
  max_memory_percent: 80
  min_free_memory: "512MiB"
  max_load_avg: 8.0

sandbox_providers:
  - name: noop
  - name: scriptdir

groups:
  - name: backend
    processes:
      - id: redis
        name: "Redis Server"
        command: redis-server
        args: ["--port", "6379", "--save", ""]
        workdir: /var/lib/redis
        env:
          REDIS_LOG_LEVEL: notice
        restart: on_failure        # never | on_failure | always
        max_restarts: 5
        restart_backoff: 2s
        stop_timeout: 10s
        stdin: none                # none | pipe | inherit
        resources:
          max_rss: "256MiB"
          max_cpu_percent: 100
          on_exceed: kill          # warn | throttle | kill
        log_options:
          ring_buffer_bytes: "4MiB"
          file_rotation:
            dir: /var/log/cliwrap/redis
            max_size: "64MiB"
            max_files: 7
            compress: true
        sandbox: null

      - id: worker
        command: /opt/app/worker
        args: ["--queue", "high"]
        restart: always
        resources:
          max_rss: "512MiB"
        sandbox:
          provider: scriptdir
          config:
            tmp_dir: /var/tmp/cliwrap
```

Schema characteristics:

- Top-level `version` field manages schema evolution.
- Byte sizes are strings like `"256MiB"` / `"1GB"`. Durations follow Go's `time.ParseDuration` format.
- `groups` are labels only — they provide no runtime isolation.
- `sandbox.config` is a provider-specific `map[string]any`.

#### 6.1.4 Loader Implementation

```go
// pkg/config/loader.go
func LoadFile(path string) (*Config, error) {
    b, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("read config: %w", err)
    }
    return Load(b)
}

func Load(data []byte) (*Config, error) {
    var raw rawConfig
    if err := yaml.Unmarshal(data, &raw); err != nil {
        return nil, fmt.Errorf("parse yaml: %w", err)
    }
    cfg, err := raw.toCanonical()
    if err != nil {
        return nil, err
    }
    if err := cfg.Validate(); err != nil {
        return nil, err
    }
    return cfg, nil
}

type Config struct {
    Version          string
    Runtime          RuntimeConfig
    SystemBudget     cliwrap.SystemBudget
    SandboxProviders []string        // names, resolved at runtime
    Groups           []ProcessGroup
}

type ProcessGroup struct {
    Name      string
    Processes []cliwrap.Spec
}
```

`Spec.Validate()` rules:

- `ID` is non-empty and matches `[a-zA-Z0-9_-]+`.
- `Command` is non-empty (absolute paths recommended but not required).
- `WorkDir`, if set, is checked for existence (warning, not error).
- `MaxRSS` ≥ 16 KiB (one page).
- `StopTimeout` ≥ 100 ms.
- `RestartBackoff` ≥ 0.
- No duplicate `ID`s across all groups.

#### 6.1.5 Environment Variable Expansion

YAML values may contain `${VAR}` patterns:

```yaml
env:
  DB_URL: "${DATABASE_URL}"
  LOG_LEVEL: "${LOG_LEVEL:-info}"
```

- `${VAR}`: missing value is a validation error.
- `${VAR:-default}`: missing value falls back to `default`.
- Expansion happens during `Load` and before `Validate`.
- Expansion also applies to `command` and `args`. The documentation must warn about injection risks when values come from untrusted sources.

### 6.2 Management CLI (`cliwrap`)

#### 6.2.1 Execution Model

Two possible execution models for the standalone CLI:

- **Foreground mode**: `cliwrap` runs continuously and owns the Manager in-process. Termination (Ctrl+C) stops all managed processes.
- **Detached mode**: `cliwrap` forks a separate supervisor process and returns immediately. The CLI then communicates with the supervisor via IPC.

**v1 decision: Foreground mode only.** Detached mode is on the roadmap.

Rationale:

1. Foreground mode integrates cleanly with external supervisors (systemd, launchd, container runtimes) and works as PID 1 in a container.
2. Detached mode introduces a daemon lifecycle that exceeds the scope of "library + management CLI".
3. Users who need a daemon can compose `cliwrap run -f config.yaml &` with existing tools.

#### 6.2.2 Command Catalog

```
cliwrap [global-flags] <command> [command-flags] [args...]

Global flags:
  --config, -f PATH       config file path (default: ./cliwrap.yaml)
  --runtime-dir PATH      override runtime directory
  --debug LEVEL           off | info | verbose | trace
  --log-format FMT        text | json
  --help, -h
  --version

Commands:
  run                     start manager + processes (foreground blocking)
  validate                validate config without starting anything
  list                    list running processes (requires running manager)
  start <ID> [<ID>...]    start specific processes
  stop <ID> [<ID>...]     stop specific processes
  restart <ID>            stop then start
  logs <ID>               stream logs from a process
  attach <ID>             interactive foreground attach
  status <ID>             detailed status
  events                  stream events from all processes
  version                 print version and exit
```

#### 6.2.3 `run` — Main Command

```
cliwrap run -f config.yaml
```

Behavior:

1. Load and validate the config.
2. Construct the Manager and register sandbox providers.
3. `Register` + immediately `Start` every process in the config.
4. On SIGINT/SIGTERM, call `Manager.Shutdown(ctx)` and exit.
5. Exit codes:
   - `0` — clean exit (including signal-driven shutdown)
   - `1` — config error
   - `2` — one or more processes ended in `StateFailed`
   - `3` — shutdown timeout

Output is human-readable by default; `--log-format json` produces JSON lines suitable for log aggregators.

#### 6.2.4 Query Commands (`list`, `status`, `logs`, `events`)

These commands connect to an already-running `cliwrap run`:

```
runtime_dir/
├── manager.sock       ← control socket (UDS)
├── agents/
│   ├── redis.sock
│   └── worker.sock
└── ...
```

`cliwrap run` creates and listens on `runtime_dir/manager.sock`. Other `cliwrap` subcommands connect to this socket and exchange messages using the same framed MessagePack protocol (reused from the agent IPC), with additional message types:

| Type | Name | Direction |
|------|------|-----------|
| 0xA0 | `LIST_REQUEST` | CLI → manager |
| 0xA1 | `LIST_RESPONSE` | manager → CLI |
| 0xA2 | `STATUS_REQUEST` | CLI → manager |
| 0xA3 | `STATUS_RESPONSE` | manager → CLI |
| 0xA4 | `LOGS_REQUEST` | CLI → manager |
| 0xA5 | `LOGS_STREAM` | manager → CLI (streaming) |
| 0xA6 | `EVENTS_SUBSCRIBE` | CLI → manager |
| 0xA7 | `EVENTS_STREAM` | manager → CLI (streaming) |
| 0xA8 | `START_REQUEST` | CLI → manager |
| 0xA9 | `STOP_REQUEST` | CLI → manager |

`manager.sock` is created with mode `0600` owned by the user who started `cliwrap run`. Remote access is not supported in v1.

#### 6.2.5 `attach` — Foreground Attach

```
cliwrap attach <process-id>
```

Behavior:

- The CLI sends an `ATTACH_REQUEST` to the manager.
- The manager streams the current ring-buffer snapshot for the target process, then forwards new `LOG_CHUNK`s as they arrive.
- If `Spec.Stdin == Pipe`, stdin from the CLI is forwarded to the manager → agent → child's stdin.
- Ctrl+C: the CLI sends `DETACH_REQUEST` and exits. The child keeps running.
- Ctrl+] (default escape sequence): the CLI exits without touching manager state.

**Scope**: this is log streaming + stdin forwarding, not a true TTY multiplexer. Full interactive TTY attach (window size, escape sequences, alternate screen) is roadmap for v2.

#### 6.2.6 `validate` — Offline Validation

```
cliwrap validate -f config.yaml
```

- Loads and validates the config.
- Errors are human-readable and include the offending field path.
- Warnings (for example, missing `workdir`) go to stderr with exit code 0.
- Suitable for CI pipelines to catch misconfigurations before deploy.

#### 6.2.7 Output Formats

Text (default):

```
$ cliwrap list
ID       NAME            STATE     PID    RSS      CPU%   RESTARTS  UPTIME
redis    Redis Server    running   1234   128.3Mi  2.1    0         1h23m
worker   Background ..   running   1245   256.1Mi  15.7   2         45m
webhook  Webhook Rcvr    crashed   -      -        -      3         -
```

JSON (`--log-format json`):

```json
{"id":"redis","name":"Redis Server","state":"running","pid":1234,"rss":134744064,"cpu_percent":2.1,"restarts":0,"uptime":"1h23m"}
```

JSON output is JSONL (one object per line) so it can be piped directly into `jq` or similar tools.

---

---

## Section 7 — Error Handling & Testing Strategy

### 7.1 Error Handling Strategy

#### 7.1.1 Error Categories

cli-wrapper classifies errors into four categories, each with its own handling rules.

| Category | Examples | Handling |
|----------|----------|----------|
| **User Error** | Invalid spec, duplicate ID, missing executable | Return immediately, no state change |
| **Transient Error** | Temporary I/O failure, momentary network hiccup | Retry with backoff, emit event |
| **Child Crash** | Managed CLI exits abnormally | `RestartPolicy`-driven restart flow |
| **Library Fatal** | Go panic, internal invariant violation | Emit event, attempt graceful shutdown |

**Never**:

- Use `panic()` for error handling — a library must not crash its host.
- Swallow or ignore errors silently.
- Log the same error multiple times — add context once as it propagates.

#### 7.1.2 Public Error Types

```go
// pkg/cliwrap/errors.go
package cliwrap

import "errors"

// Sentinel errors — compare with errors.Is()
var (
    ErrProcessNotFound         = errors.New("cliwrap: process not found")
    ErrProcessAlreadyExists    = errors.New("cliwrap: process already exists")
    ErrProcessNotRunning       = errors.New("cliwrap: process not running")
    ErrProcessAlreadyRunning   = errors.New("cliwrap: process already running")
    ErrManagerShuttingDown     = errors.New("cliwrap: manager is shutting down")
    ErrManagerClosed           = errors.New("cliwrap: manager is closed")
    ErrSandboxProviderNotFound = errors.New("cliwrap: sandbox provider not registered")
    ErrAgentSpawnFailed        = errors.New("cliwrap: failed to spawn agent")
    ErrHandshakeTimeout        = errors.New("cliwrap: handshake timeout")
    ErrStopTimeout             = errors.New("cliwrap: stop timeout")
    ErrBackpressureStalled     = errors.New("cliwrap: backpressure stalled")
    ErrInvalidSpec             = errors.New("cliwrap: invalid spec")
)

// Structured errors — carry context

type SpecValidationError struct {
    Field  string
    Value  any
    Reason string
}

func (e *SpecValidationError) Error() string {
    return fmt.Sprintf("invalid spec field %q (value=%v): %s", e.Field, e.Value, e.Reason)
}

func (e *SpecValidationError) Is(target error) bool {
    return target == ErrInvalidSpec
}

type AgentCommunicationError struct {
    ProcessID string
    Phase     string // "handshake" | "command" | "read" | "write"
    Err       error
}

func (e *AgentCommunicationError) Error() string {
    return fmt.Sprintf("agent communication failed (pid=%s, phase=%s): %v",
        e.ProcessID, e.Phase, e.Err)
}

func (e *AgentCommunicationError) Unwrap() error { return e.Err }
```

Rules:

- Sentinel errors exist only for comparison.
- Structured errors carry context as fields and support `Unwrap()`.
- Every error string uses the `cliwrap:` prefix so boundaries are obvious.

#### 7.1.3 Error Propagation

```go
// Bad: original error information is lost
func (c *Controller) start() error {
    if err := c.handshake(); err != nil {
        return errors.New("handshake failed") // ❌
    }
}

// Good: wrap with layer-specific context
func (c *Controller) start() error {
    if err := c.handshake(); err != nil {
        return fmt.Errorf("controller start: handshake: %w", err) // ✓
    }
}
```

Each layer should:

- Wrap the original error with `%w`.
- Add only the context its own layer understands (process ID, operation name).
- Avoid re-wrapping errors that already carry enough structure.

#### 7.1.4 Panic Recovery

Every long-lived goroutine (on both host and agent sides) is spawned through this helper:

```go
func safeGoroutine(name string, bus event.Bus, fn func()) {
    go func() {
        defer func() {
            if r := recover(); r != nil {
                stack := debug.Stack()
                bus.Publish(event.LibraryPanic{
                    Goroutine: name,
                    Value:     fmt.Sprintf("%v", r),
                    Stack:     string(stack),
                })
            }
        }()
        fn()
    }()
}
```

Rationale: a panic inside the library is a bug, but swallowing it silently would make it unobservable. This pattern contains panics at goroutine boundaries and ensures the host application can detect them via events.

#### 7.1.5 Error Observability

Every major error path both **returns the error** and **publishes an event**:

```go
func (m *Manager) Start(id string) (ProcessHandle, error) {
    handle, err := m.doStart(id)
    if err != nil {
        m.bus.Publish(event.ProcessStartFailed{
            baseEvent: baseEvent{processID: id, ts: time.Now()},
            Reason:    err.Error(),
        })
        return nil, fmt.Errorf("start %s: %w", id, err)
    }
    return handle, nil
}
```

This ensures that both direct callers and event subscribers (monitoring, logging, alerting systems) can react to failures.

### 7.2 Testing Strategy

#### 7.2.1 Test Pyramid

```
           ┌───────────────┐
           │  E2E (cliwrap │
           │    binary)    │    ~5% of tests
           └───────────────┘
          ┌─────────────────┐
          │  Integration    │
          │  (real agents)  │   ~25% of tests
          └─────────────────┘
        ┌─────────────────────┐
        │    Unit Tests       │
        │  (mocks, isolated)  │   ~70% of tests
        └─────────────────────┘
```

#### 7.2.2 Unit Tests

Scope: pure logic inside individual packages (IPC framing, ring buffer, state machine, backoff, Evaluator, YAML loader, spec validation).

Principles:

- Filesystem and network dependencies are isolated behind interfaces and replaced with fakes.
- Time-related logic uses an injected `clock.Clock` so tests can fast-forward.
- Each test function should complete within 200 ms.
- Table-driven tests are the default.

Example — frame decoder covers all edge cases:

```go
func TestFrameDecoder(t *testing.T) {
    tests := []struct {
        name    string
        input   []byte
        want    Frame
        wantErr error
    }{
        {"happy path", validFrame, Frame{/* ... */}, nil},
        {"magic mismatch", corruptMagic, Frame{}, ErrInvalidMagic},
        {"truncated header", shortBytes, Frame{}, io.ErrUnexpectedEOF},
        {"length exceeds max", oversizedFrame, Frame{}, ErrFrameTooLarge},
        {"partial payload", partialFrame, Frame{}, io.ErrUnexpectedEOF},
    }
    // ...
}
```

#### 7.2.3 Integration Tests

Scope: spawn real agent processes and validate IPC, lifecycle, crash detection, and log collection.

Key fixtures under `test/fixtures/`:

- `fixture-echo` — echoes stdin to stdout
- `fixture-noisy` — writes periodic stdout/stderr
- `fixture-crasher` — exits with code 99 after N seconds
- `fixture-hanger` — ignores SIGTERM (tests the SIGKILL escalation path)
- `fixture-leaker` — simulates resource leaks
- `fixture-burst` — produces enough output to force File Mode

All fixtures are pure Go, built into `test/fixtures/bin/`.

Representative test:

```go
func TestLifecycle_CrashDetection(t *testing.T) {
    mgr, cleanup := newTestManager(t)
    defer cleanup()

    sub := mgr.Subscribe(event.Filter{
        Types: []event.Type{event.TypeProcessCrashed},
    })
    defer sub.Close()

    h, _ := mgr.Register(cliwrap.Spec{
        ID:      "crash1",
        Command: "test/fixtures/bin/fixture-crasher",
        Args:    []string{"--after", "500ms"},
        Restart: cliwrap.RestartNever,
    })
    h.Start(ctx)

    select {
    case ev := <-sub.Events():
        crashed := ev.(event.ProcessCrashedEvent)
        require.Equal(t, int32(99), crashed.CrashInfo.ChildExit)
    case <-time.After(5 * time.Second):
        t.Fatal("crash event not received")
    }

    require.Equal(t, cliwrap.StateFailed, h.Status().State)
}
```

#### 7.2.4 Chaos / Reliability Tests

A dedicated category for memory-leak, goroutine-leak, and message-loss detection.

```go
func TestReliability_NoGoroutineLeaks(t *testing.T) {
    defer goleak.VerifyNone(t)

    mgr, cleanup := newTestManager(t)
    for i := 0; i < 100; i++ {
        h, _ := mgr.Register(testSpec(i))
        h.Start(ctx)
        h.Stop(ctx)
    }
    cleanup()
}

func TestReliability_NoMessageLoss(t *testing.T) {
    // Agent sends 10_000 numbered messages.
    // Connection is forcibly dropped mid-stream (chaos injection).
    // Host must receive 1..10000 with ordering preserved after reconnect.
}

func TestReliability_BackpressureToWAL(t *testing.T) {
    // Artificially fill the outbox.
    // Verify WAL spill files are created.
    // Verify all messages are delivered after connection recovery.
    // Verify WAL files are cleaned up.
}

func TestReliability_MemoryBounded(t *testing.T) {
    // 100 processes, 1000 start/stop cycles each.
    // RSS must stay within a defined band.
    // Heap diff used to pinpoint leaks.
}
```

#### 7.2.5 Cross-Platform Tests

CI runs the full suite on Linux (amd64, arm64) and macOS (amd64, arm64).

Platform-specific tests:

- `resource_linux_test.go` validates `/proc` parsing.
- `resource_darwin_test.go` validates the `sysctl` / `libproc` paths.
- `//go:build linux` / `//go:build darwin` build tags isolate platform code.
- Interface-level tests run on both platforms.

#### 7.2.6 Fuzz Tests

The IPC frame decoder and YAML loader are fuzz-tested:

```go
func FuzzFrameDecoder(f *testing.F) {
    f.Add(validFrameBytes)
    f.Fuzz(func(t *testing.T, data []byte) {
        dec := NewDecoder(bytes.NewReader(data))
        _, _ = dec.Decode()
        // Goal: no panic, graceful error.
    })
}

func FuzzYAMLLoader(f *testing.F) {
    f.Add([]byte(validYAML))
    f.Fuzz(func(t *testing.T, data []byte) {
        _, _ = config.Load(data)
    })
}
```

CI runs short fuzz iterations (30 s) per PR and longer runs (10 m) nightly.

#### 7.2.7 Benchmarks

```go
func BenchmarkIPCRoundTrip(b *testing.B)    // PING/PONG round-trip
func BenchmarkLogThroughput(b *testing.B)   // 1 MB/s stdout handling
func BenchmarkManyProcesses(b *testing.B)   // 100 concurrent start/stop
func BenchmarkRingBufferWrite(b *testing.B) // ring buffer write throughput
func BenchmarkWALSpill(b *testing.B)        // outbox spill cost
```

Performance targets for v1:

- IPC round-trip: < 500 μs
- Process start: < 100 ms (including handshake)
- Ring buffer write: > 100 MB/s
- Concurrent managed processes: ≥ 200

#### 7.2.8 Coverage Targets

- `pkg/`: ≥ 85%
- `internal/`: ≥ 75%
- Overall: ≥ 80%
- CI fails a PR that reduces overall coverage by more than 1%.

### 7.3 Observability

#### 7.3.1 Structured Logging

- Uses `log/slog` from the standard library.
- The host application injects a handler via `WithLogger(handler slog.Handler)`.
- Default handler: `slog.NewTextHandler(os.Stderr, ...)`.
- Every log entry carries common attributes: `module`, `process_id`, `agent_pid`.

#### 7.3.2 Metrics

v1 provides in-memory metrics only; a Prometheus exporter is on the roadmap.

```go
type Metrics struct {
    ProcessesRegistered   int64
    ProcessesRunning      int64
    ProcessesCrashed      int64 // cumulative
    IPCMessagesSent       int64
    IPCMessagesReceived   int64
    IPCMessagesDropped    int64
    WALSpillBytes         int64
    LogBytesCollected     int64
    ResourceLimitBreaches int64
}

func (m *Manager) Metrics() Metrics // atomic snapshot
```

The host polls `Metrics()` and pushes to its own metrics system.

---

## Appendix A — Open Questions & Roadmap

These items are intentionally deferred past v1:

- **Windows support** — full port including PTY-equivalent and process APIs.
- **Detached CLI mode** — `cliwrap` launching a persistent supervisor daemon.
- **True TTY attach** — window resize, escape sequences, alternate screen.
- **cgroups v2 throttling** — fine-grained CPU and memory enforcement on Linux.
- **Prometheus exporter** — HTTP endpoint exposing metrics.
- **Remote management** — `cliwrap` connecting to a manager across hosts.
- **Rust/C native components** — to replace hot paths if Go profiling reveals GC pressure.
- **Plugin sinks** — `pkg/logsink` interface for syslog, journal, HTTP sinks.
- **Session persistence** — resuming managed processes across host application restarts.

## Appendix B — Out of Scope

These are explicitly not within this library's responsibility:

- Log aggregation beyond file rotation (no built-in ELK, Loki, etc.).
- Service discovery or networking between managed processes.
- Job scheduling (cron-like triggers).
- Secrets management — users must use their own mechanisms.
- GUI or web dashboard.

