# Changelog

## [Unreleased]

### Added
- `cliwrap logs <id>` now prints the current stdout/stderr ring-buffer
  contents for a supervised process. Use `--stream stdout|stderr|all`
  to filter (default: `all`, which prints stdout followed by stderr).
  This is a snapshot command — `--follow` is not yet implemented.
  Under the hood, the agent now tees child stdout/stderr into
  `MsgLogChunk` IPC frames; the host Manager owns a `logcollect.Collector`
  with a 1 MiB per-stream ring buffer per process.
- `cliwrap events` now streams lifecycle events (process started,
  stopped, crashed, etc.) from a running manager over the mgmt
  socket. Use `--process <id,id,...>` to filter by process id; empty
  means "all processes". Events are printed in
  `HH:MM:SS.mmm  event.type  process.id  summary` format, one per
  line, until the client disconnects (ctrl-c) or the manager shuts
  down. The mgmt protocol gains a streaming upgrade: after receiving
  `MsgEventsSubscribe`, the server keeps the connection open and
  pushes `MsgEventsStream` frames from the Manager's event bus until
  either side closes.
- `cliwrap logs --follow` / `-f` live tail mode. After printing the
  current ring-buffer snapshot, the command keeps the connection
  open and prints each new log chunk as it arrives, until the user
  interrupts with ctrl-c or the manager shuts down. Follow mode
  requires a single stream (`--stream stdout` or `--stream stderr`)
  because one mgmt connection corresponds to one server-side
  subscription. The Manager gains a public `WatchLogs(processID)`
  API returning a buffered channel of `LogChunk` values and an
  unregister function; this is the underlying primitive that also
  drives the mgmt server's follow handler.

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
  v0.1.0. Only the CI plumbing around lint and DCO changed, plus the
  addition of the Security Guard Agent design spec under
  `docs/superpowers/specs/`.

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
- `cliwrap events` streaming is not yet implemented.
- Sandbox providers other than `noop` and `scriptdir` ship as external modules.
- cgroups v2 throttling is not yet wired.
- Windows is not supported.
