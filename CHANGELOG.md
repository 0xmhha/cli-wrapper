# Changelog

## [Unreleased]

### Added
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
