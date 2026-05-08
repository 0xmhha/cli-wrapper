# Changelog

## [Unreleased]

### Added
- Windows cross-compilation foundation: stub `_windows.go` files for
  `internal/platform`, `internal/resource`, `internal/agent` (PTY), and
  `internal/supervise` (Spawner). The non-spawn-touching packages
  (`internal/ipc`, `internal/mgmt`, `internal/eventbus`, `internal/logcollect`,
  `internal/cgroup` (returns ErrUnsupportedPlatform), `pkg/cliwrap`, etc.)
  now build cleanly under `GOOS=windows`. New `windows-build` CI job vets
  + builds on `windows-latest` to catch accidental Unix-only additions.
  Runtime support (Named Pipes IPC + Job Objects + ConPTY) remains
  multi-week roadmap work — the Windows stubs return ErrUnsupportedOnWindows
  / ErrPTYUnsupportedOnWindows at Spawn / spawnPTY entry points.
- `.github/FUNDING.yml` with `github: [0xmhha]` enabled and the rest of
  the supported platforms left as commented-out placeholders.
- `internal/cgroup` package: cgroup v2 throttling building block (Linux).
  Helper for placing a child PID into a cgroup v2 directory with CPU
  quota (`cpu.max`), memory limit (`memory.max`), and I/O weight
  (`io.weight`). Non-Linux builds receive a stub returning
  `ErrUnsupportedPlatform`. Runner integration is a follow-up.
- `firejail` sandbox provider at `pkg/sandbox/providers/firejail/`.
  Linux-only. Wraps the SUID-root firejail binary (seccomp + caps drop +
  namespace isolation). Configurable via `SandboxSpec.Config`:
  `profile`, `netns`, `nonet`, `private`, `noroot`, `whitelist`,
  `blacklist`, `caps_drop`, `seccomp`.
- `sandbox-exec` sandbox provider at `pkg/sandbox/providers/sandboxexec/`.
  macOS-only. Wraps the deprecated-but-functional `sandbox-exec` binary;
  accepts a Scheme profile inline (materialized to a temp file), by
  built-in name (`profile_name`), or by file path (`profile_file`).
- File-backed log persistence: `cliwrap.WithLogFileDir(dir)` and the
  matching `runtime.log_file_dir` YAML key. When set, every log chunk
  is also written to `<dir>/<processID>.<stream>.log` via FileRotator
  alongside the in-memory ring buffer. `WithLogFileRotation(maxSize,
  maxFiles)` overrides per-rotator defaults.
- Log ring buffer size is now configurable: `cliwrap.WithLogRingBufferBytes`
  ManagerOption + `runtime.log_ring_buffer_bytes` YAML key.
  `cliwrap.DefaultLogRingBufferBytes` constant (1 MiB) preserves
  pre-existing behavior when unset.
- `cliwrap events --type` flag to filter the event stream by type
  (CSV; e.g. `--type=process.crashed,process.failed`). Wires
  `EventsSubscribePayload.Types` through to `event.Filter.Types`.
- Benchmark regression tracking: `make bench-pty / bench-baseline /
  bench-current / bench-compare / bench-tools` Makefile targets and a
  GitHub Actions workflow (`.github/workflows/bench.yml`) that runs the
  default (untagged) benchmarks on every push and PR, uploading output
  as a 30-day artifact for local benchstat comparison.
- `bubblewrap` sandbox provider at `pkg/sandbox/providers/bubblewrap/`.
  Linux-only (returns `ErrUnsupportedPlatform` elsewhere); requires
  `bwrap` on PATH at construction (`ErrBwrapNotFound` otherwise).
  Defaults: read-only host root, fresh `/tmp` (tmpfs), fresh `/proc`
  and `/dev`, full namespace isolation (`--unshare-all`), no network,
  `--die-with-parent`. Configurable via `cwtypes.SandboxSpec.Config`
  (`network bool`, `bind_rw []string`, `bind_ro []string`, `chdir string`).
  Argv assembly is unit-tested on every platform; namespace exec is
  exercised on Linux when `bwrap` is available.
- `cliwrap logs --since <duration>` and `--lines N` (alias `-n`) filters.
  `--since 5m` returns chunks emitted within the last 5 minutes; `--lines 50`
  returns only the trailing 50 newline-delimited lines. Filters compose: the
  --since filter is applied first, then --lines trims the result. Filters
  apply to the snapshot (whether standalone or in `--follow`'s initial
  snapshot); subsequent live chunks in follow mode are not filtered.
  Required moving `RingBufferSink` from byte-ring to chunk-list storage so
  per-chunk arrival timestamps are retained.
- CW-G4: Persistent sessions + reattach. New `Spec.Persistent` flag opts a
  session into outliving the host process. New `Spec.RingBufferSize`
  controls in-memory PTY scrollback for redraw on reattach. New
  `WithPersistentDir(path)` Manager option, `Manager.ListPersistent()`,
  `Manager.Reattach(ctx, id)`. New CLI commands `cliwrap list --persistent`
  and `cliwrap kill <id>`. Default behavior (`Persistent=false`) unchanged.
  When `Persistent=true`, the spawner sets `Setsid: true` and redirects
  stdio to `<sessionDir>/agent.log`; the agent advertises a `"persistence"`
  capability and listens on `<sessionDir>/sock` for reattach. Single-host
  attach at a time (concurrent reattach returns `ErrAlreadyAttached`).
  `h.Stop` on a persistent session terminates the agent and cleans
  sock+pid; `h.Close` releases the host's connection only, leaving the
  agent alive in detached mode.
- First-class PTY support for child processes via `Spec.PTY`.
- New IPC messages: `MsgPTYData`, `MsgPTYWrite`, `MsgPTYResize`, `MsgPTYSignal`.
- Capability negotiation handshake (`MsgCapabilityQuery`/`Reply`); host refuses PTY against incompatible agents with `ErrPTYUnsupportedByAgent`.
- `ProcessHandle.WriteInput`, `Resize`, `Signal`, `SubscribePTYData`.
- Log rotator continues to record output regardless of spawn mode (PTY data tees into the existing logcollect pipeline).
- Integration tests for PTY echo, resize, signal, log persistence, and capability negotiation.
- Chaos tests for slow PTY consumer, child SIGKILL.
- Benchmarks for PTY throughput and keystroke round-trip latency.

### Fixed
- CW-G5: silent first-frame loss on concurrent senders. `Seqs().Next()`
  followed by `Outbox.Enqueue()` was not atomic, so two goroutines could
  interleave such that the higher-seq frame enqueued first; the
  receiver's watermark `DedupTracker` then dropped the lower-seq frame
  as a "duplicate", silently losing it. Surfaced as a ~2% flake in
  `TestIntegration_LogsSnapshotCapturesChildOutput` under `-race` where
  fixture-noisy's first stdout chunk could be dropped while stderr
  arrived normally. Fix: new `Conn.SendWithNewSeq(msgType, flags, payload)`
  that performs sequence assignment and outbox enqueue under a single
  mutex; updated the three call sites in `agent/dispatcher.go`,
  `controller/controller.go`, `controller/pty.go`. The integration test's
  Phase-2 budget tightened from 30 s back to 5 s.
- CW-G1: cliwrap-agent crashes are now detected by the host within ~50ms.
  `ipc.Conn` fires a new `SetOnDisconnect` callback on socket EOF /
  unrecoverable read error; the controller transitions to `StateCrashed`
  with `CrashSource = CrashSourceConnectionLost`. Resolves the gap that
  caused `test/chaos/pty_agent_crash_test.go` to be skipped.
- CW-G3: supervision cleanup leak under burst spawn/terminate. Agent's
  `Run()` now drains active runners before `conn.Close` so child
  reaping (`cmd.Wait`) completes before the agent process exits.
  Default child stop budget (`Spec.StopTimeout` when unset) lowered
  from 5 s to 2 s to fit inside the host's 3 s `AgentHandle.Close`
  grace; without this cascade alignment, sustained burst spawn/stop
  cycles on macOS leaked children to launchd until `kern.maxproc` was
  exhausted (~500 cycles in <60 s on Apple M2).
- CW-G3.1 RESOLVED (incidentally by CW-G3): host-side state accumulation
  that previously surfaced as `controller capability negotiation: context
  deadline exceeded` at iter ~20 of `TestBurst_SpawnStop_NoLeak`. Re-
  validation at N=25, 50, 100 all PASS with stable goroutine + fd counts.
  CW-G3's drain-before-conn.Close cascade alignment narrowed the host-
  side cleanup race window enough that per-iteration Conn close completes
  before the next spawn handshake. `burstDefaultN` lifted from 15 to 50.

## [0.2.0] - 2026-04-09

Observability release. Three user-facing subcommands — `cliwrap logs`
snapshot, `cliwrap logs --follow` live tail, and `cliwrap events`
streaming — are now fully wired end-to-end through the agent, IPC,
manager, mgmt server, and CLI client. The mgmt protocol gains a
"streaming upgrade" pattern that the two follow-mode commands share:
after an initial request, the server keeps the connection open and
pushes frames until the client disconnects or the manager shuts down.

### Added

- `cliwrap logs <id>` prints the current stdout/stderr ring-buffer
  contents for a supervised process. Use `--stream stdout|stderr|all`
  to filter (default: `all`, which prints stdout followed by stderr).
  Under the hood, the agent now tees child stdout/stderr into
  `MsgLogChunk` IPC frames; the host `Manager` owns a
  `logcollect.Collector` with a 1 MiB per-stream ring buffer per
  process.

- `cliwrap logs --follow` / `-f` live tail mode. After printing the
  current ring-buffer snapshot, the command keeps the connection
  open and prints each new log chunk as it arrives, until the user
  interrupts with ctrl-c or the manager shuts down. Follow mode
  requires a single stream (`--stream stdout` or `--stream stderr`)
  because one mgmt connection corresponds to one server-side
  subscription. The `Manager` gains a public `WatchLogs(processID)`
  API returning a buffered channel of `LogChunk` values and an
  unregister function; this is the underlying primitive that also
  drives the mgmt server's follow handler.

- `cliwrap events` streams lifecycle events (process started,
  stopped, crashed, etc.) from a running manager over the mgmt
  socket. Use `--process <id,id,...>` to filter by process id;
  empty means "all processes". Events are printed in
  `HH:MM:SS.mmm  event.type  process.id  summary` format, one per
  line, until the client disconnects (ctrl-c) or the manager shuts
  down.

- Mgmt protocol: streaming upgrade pattern. After receiving
  `MsgEventsSubscribe` or `MsgLogsRequest` with `Follow=true`, the
  server dedicates the connection to the stream and pushes
  `MsgEventsStream` / `MsgLogsStream` frames from either the
  `Manager.Events()` bus or the new `Manager.WatchLogs` channel
  until either side closes. `LogsRequestPayload` gains a `Follow bool`
  field; `EventsSubscribePayload` gains a `ProcessIDs []string`
  filter; the existing `EventsStreamPayload` schema is unchanged.

- `internal/mgmt.Client` gains `Stream(t, payload)` and `ReadFrame()`
  methods to drain an arbitrary number of frames after sending a
  single streaming-upgrade request.

### Fixed

- CI `unit` job now runs `make fixtures` before `go test ./...`,
  so integration tests that shell out to `test/fixtures/bin/`
  binaries work correctly in CI. Previously the binaries did not
  exist in the unit matrix, and most integration tests accidentally
  passed because they expected `StateCrashed` from a failing
  `exec.Command` — the new `cliwrap logs` integration test exposed
  the hole by expecting `StateRunning`, which requires a real
  executable child.

- `TestIntegration_LogsSnapshotCapturesChildOutput` stabilized by
  splitting the single `require.Eventually` into two phases
  (wait for `StateRunning`, then wait for log content) with
  generous 10 s budgets each, and by moving `mgr.Shutdown` into a
  deferred closure with its own fresh context so a fatal Eventually
  no longer leaks goroutines into sibling tests. 20/20 green under
  `-race` locally.

- `TestChaos_WALReplayAfterDisconnect` stabilized by sending a
  200-message burst immediately before `Close` instead of relying
  on a 5-message race with the receiver. The large burst guarantees
  the WAL still holds unacked entries regardless of scheduler
  timing. 20/20 green under `-race` locally.

- `TestMonitor_PollsAndStops` stabilized by replacing a fixed
  80 ms `time.Sleep` with `require.Eventually(..., 2*time.Second,
  10*time.Millisecond)`, so the test waits for the actual
  observable condition instead of a wall-clock budget. 20/20
  green under `-race` locally.

- `cliwrap events` and `cliwrap logs --follow` no longer silently
  swallow non-EOF transport errors as exit 0. Only `io.EOF`,
  `io.ErrUnexpectedEOF`, and `net.ErrClosed` are treated as clean
  termination; all other read errors print to stderr and return
  exit 1, so shells and CI pipelines can react.

### Changed

- Mgmt server `handleLogsFollow` rejects `req.Stream > 1` with an
  inline error frame instead of subscribing to `WatchLogs` and
  silently dropping every chunk (which would leak the subscription
  and the connection).
- Mgmt server `handleConn` now handles `MsgLogsRequest` inline
  (both snapshot and follow modes) to avoid decoding the payload
  twice. The dead `MsgLogsRequest` case in `handleRequest` has
  been removed.
- `LogChunk` struct moved from `pkg/cliwrap` into `internal/cwtypes`
  to break a potential `internal/mgmt` → `pkg/cliwrap` import cycle.
  `pkg/cliwrap` re-exports it as a type alias, so public callers
  still see `cliwrap.LogChunk`.

## [0.1.1] - 2026-04-08

First release with published binaries. The `v0.1.0` tag was created
but its `Release` workflow did not execute due to a race between the
initial `main` push and the `v0.1.0` tag push (GitHub Actions did not
index the freshly-added release workflow in time for the tag event).
The tag itself is still valid for `go get` consumers, but GitHub
Releases for `v0.1.0` contains no prebuilt artifacts. `v0.1.1` is
the first tag whose Release workflow ran cleanly end-to-end.

### Fixed
- `internal/resource/collector_linux.go`: the deferred
  `os.File.Close()` on `/proc/meminfo` was unchecked, causing the CI
  Lint job to fail on the first main push. Wrapped in an explicit
  discard closure. The issue was not caught by local lint runs
  because the file carries a `//go:build linux` constraint and
  cross-target golangci-lint on a macOS workstation hits
  `runtime/cgo` export-data errors.
- `.github/workflows/dco.yml`: added an explicit bot-author exemption
  list (`dependabot[bot]`, `github-actions[bot]`, `renovate[bot]`).
  Without it, Dependabot-authored PRs were universally rejected by
  the DCO sign-off check since bot commits cannot meaningfully
  certify the Developer Certificate of Origin.

### Unchanged from v0.1.0
- All features, public API, and configuration remain identical to
  v0.1.0. Only the CI plumbing around lint and DCO changed.

## [0.1.0] - 2026-04-08

Initial public release. Tag exists and is consumable via
`go get github.com/0xmhha/cli-wrapper@v0.1.0`, but no prebuilt
binaries are attached to its GitHub Release (see note under v0.1.1).

### Changed
- **License: relicensed from LGPL-2.1-or-later to Apache-2.0.** The
  initial LGPL-2.1 choice created friction with Go's static-linking
  default (LGPL §6 expects users to be able to relink against a
  modified library, which is awkward for Go binaries). Apache-2.0 is
  the de-facto standard for Go libraries, includes an explicit patent
  grant, and removes the static-linking ambiguity. SPDX identifier
  changed to `Apache-2.0`. This change is safe to apply pre-1.0 with
  no external committers; all prior commits are by the project owner.

### Added
- `NOTICE` file per Apache-2.0 attribution convention.
- `SPDX-License-Identifier: Apache-2.0` headers on all Go source files.
- IPC foundation: framing, MessagePack codec, persistent outbox with WAL, Conn (Plan 01).
- Process supervision core: agent binary, controller, manager, event types, log collection, state machine (Plan 02).
- Resource monitoring and sandbox plugin interface with noop + scriptdir providers (Plan 03).
- YAML config loader and management CLI with run/validate/list/status/stop commands (Plan 04).
- Heartbeats, unified crash info, restart loop, EventBus wiring, chaos tests, CI matrix (Plan 05).

### Known Limitations
- Sandbox providers other than `noop` and `scriptdir` ship as external modules.
- cgroups v2 throttling is not yet wired.
- Windows is not supported.
