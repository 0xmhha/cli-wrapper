# cli-wrapper

A production-grade Go library and companion CLI for supervising background CLI processes on macOS and Linux.

## Status

Early development. Plan 01 (IPC Foundation) is in progress. See `docs/superpowers/plans/` for the roadmap.

## Development

```bash
# Run tests with race detector
make test

# Run linters
make lint

# Generate coverage
make cover
```

## Layout

- `pkg/` — public API (semver-stable, not yet implemented)
- `internal/ipc/` — frame protocol, outbox, WAL, connection wrapper (Plan 01)
- `docs/superpowers/specs/` — design specs
- `docs/superpowers/plans/` — implementation plans
