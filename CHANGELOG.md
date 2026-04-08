# Changelog

## [Unreleased]

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
- `cliwrap logs` and `cliwrap events` streaming is not yet implemented.
- Sandbox providers other than `noop` and `scriptdir` ship as external modules.
- cgroups v2 throttling is not yet wired.
- Windows is not supported.
