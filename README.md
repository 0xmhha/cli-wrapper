# cli-wrapper

Production-grade Go library and CLI for supervising background CLI processes on macOS and Linux.

## Features

- Zero-leak process supervision (agent subprocess per CLI)
- Guaranteed message delivery via persistent outbox + WAL
- 4-layer crash detection (explicit, connection, heartbeat, OS wait)
- Plug-in sandbox providers (noop, scriptdir, or your own)
- YAML config + Go API (both compile to the same Spec)
- Management CLI (`cliwrap`) with list/status/stop commands
- goleak-verified in CI across Linux and macOS

## Quick Start

### Go API

```go
mgr, _ := cliwrap.NewManager(
    cliwrap.WithAgentPath("/usr/local/bin/cliwrap-agent"),
    cliwrap.WithRuntimeDir("/var/run/myapp"),
)
defer mgr.Shutdown(context.Background())

spec, _ := cliwrap.NewSpec("redis", "redis-server", "--port", "6379").
    WithRestart(cliwrap.RestartOnFailure).
    Build()

h, _ := mgr.Register(spec)
_ = h.Start(context.Background())
```

### Management CLI

```bash
cliwrap validate -f config.yaml
cliwrap run -f config.yaml

# in another terminal:
cliwrap list
cliwrap status redis
cliwrap stop redis
```

## Development

```bash
make fixtures       # build test fixtures
make test           # unit tests with race detector
make integration    # integration tests
make chaos          # chaos tests
make bench          # benchmarks
make fuzz-short     # 30s fuzz run
```

## Project Layout

- `pkg/cliwrap/` — public API
- `pkg/config/` — YAML loader
- `pkg/event/` — event types + bus interface
- `pkg/sandbox/` — sandbox plugin interface
- `internal/ipc/` — frame protocol and persistent outbox
- `internal/agent/` — cliwrap-agent logic
- `internal/controller/` — host-side agent controller
- `internal/supervise/` — spawner, watcher, restart loop
- `internal/logcollect/` — ring buffer and file rotator
- `internal/resource/` — per-process and system sampling
- `internal/mgmt/` — management socket server and client
- `cmd/cliwrap/` — management CLI
- `cmd/cliwrap-agent/` — agent subprocess binary
- `docs/superpowers/` — design specs and implementation plans

## Status

v1 targets macOS and Linux. Windows is on the roadmap.
