# cli-wrapper

> Production-grade Go library and CLI for supervising background CLI processes on macOS and Linux.

[![CI](https://github.com/0xmhha/cli-wrapper/actions/workflows/ci.yml/badge.svg)](https://github.com/0xmhha/cli-wrapper/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/0xmhha/cli-wrapper.svg)](https://pkg.go.dev/github.com/0xmhha/cli-wrapper)
[![Go Report Card](https://goreportcard.com/badge/github.com/0xmhha/cli-wrapper)](https://goreportcard.com/report/github.com/0xmhha/cli-wrapper)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)
[![Go Version](https://img.shields.io/badge/go-%3E%3D1.22-00ADD8.svg)](https://go.dev/dl/)
[![Platforms](https://img.shields.io/badge/platforms-linux%20%7C%20macOS-lightgrey.svg)](#requirements)

**cli-wrapper** supervises arbitrary child CLI processes with guaranteed delivery of lifecycle
events, zero goroutine leaks, and a pluggable sandboxing layer. It ships as both an embeddable
Go library (`pkg/cliwrap`) and a standalone management CLI (`cliwrap`).

---

## Table of Contents

- [Why cli-wrapper?](#why-cli-wrapper)
- [Features](#features)
- [Requirements](#requirements)
- [Installation](#installation)
- [Quick Start](#quick-start)
  - [Go API](#go-api)
  - [Management CLI](#management-cli)
- [Configuration](#configuration)
- [Architecture](#architecture)
- [Project Layout](#project-layout)
- [Development](#development)
- [Roadmap](#roadmap)
- [Contributing](#contributing)
- [Security](#security)
- [License](#license)
- [Acknowledgements](#acknowledgements)

---

## Why cli-wrapper?

Running a long-lived child process reliably is deceptively hard. You need to:

- Detect every possible failure mode (exit code, broken pipe, silent hang, OS kill)
- Restart with backoff without leaking PIDs or file descriptors
- Deliver lifecycle events even across agent crashes
- Enforce resource budgets without coupling to cgroups internals
- Integrate a sandbox without rewriting your supervisor

**cli-wrapper** is an opinionated answer to this problem. Instead of spawning children
in-process, the host library launches a dedicated `cliwrap-agent` subprocess per CLI, so a
runaway child can never take down your main application. Lifecycle events pass through a
**persistent WAL-backed outbox**, so a crashed agent resumes exactly where it left off.

## Features

- **Zero-leak process supervision** — goleak-verified in CI on both Linux and macOS
- **Guaranteed event delivery** — persistent outbox + WAL survives agent crashes
- **4-layer crash detection** — explicit exit, broken connection, heartbeat miss, OS `wait(2)`
- **Restart policies** — `never`, `on_failure`, `always` with exponential backoff and cap
- **Resource budgets** — per-process RSS / CPU thresholds with `kill` or `signal` actions
- **Plug-in sandbox providers** — `noop`, `scriptdir`, or bring your own (`pkg/sandbox`)
- **YAML + Go API parity** — both compile to the same canonical `Spec`
- **Management CLI** — `cliwrap run | validate | list | status | stop`
- **Chaos- and fuzz-tested** — slow subscribers, WAL replay, frame reader fuzz harness

## Requirements

| Requirement | Version    |
|-------------|------------|
| Go          | **1.22+**  |
| Linux       | kernel 4.18+ (cgroups v2 optional) |
| macOS       | 12 Monterey or newer |

> Windows is **not** supported in v1. See [Roadmap](#roadmap).

## Installation

### As a Go library

```bash
go get github.com/0xmhha/cli-wrapper@latest
```

### As the management CLI

```bash
go install github.com/0xmhha/cli-wrapper/cmd/cliwrap@latest
go install github.com/0xmhha/cli-wrapper/cmd/cliwrap-agent@latest
```

Both binaries are required for the CLI: `cliwrap` is the host-side controller,
`cliwrap-agent` is the per-process supervisor subprocess.

### From source

```bash
git clone https://github.com/0xmhha/cli-wrapper.git
cd cli-wrapper
make fixtures
go build -o bin/cliwrap       ./cmd/cliwrap
go build -o bin/cliwrap-agent ./cmd/cliwrap-agent
```

## Quick Start

### Go API

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

func main() {
    mgr, err := cliwrap.NewManager(
        cliwrap.WithAgentPath("/usr/local/bin/cliwrap-agent"),
        cliwrap.WithRuntimeDir("/var/run/myapp"),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer mgr.Shutdown(context.Background())

    spec, err := cliwrap.NewSpec("redis", "/usr/bin/redis-server", "--port", "6379").
        WithRestart(cliwrap.RestartOnFailure).
        WithMaxRestarts(3).
        WithStopTimeout(5 * time.Second).
        Build()
    if err != nil {
        log.Fatal(err)
    }

    h, err := mgr.Register(spec)
    if err != nil {
        log.Fatal(err)
    }
    if err := h.Start(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

### Management CLI

Create a config file:

```yaml
# cliwrap.yaml
version: "1"
runtime:
  dir: /tmp/cliwrap
  debug: info
groups:
  - name: web
    processes:
      - id: redis
        name: Redis
        command: /usr/bin/redis-server
        args: ["--port", "6379"]
        restart: on_failure
        max_restarts: 3
        restart_backoff: 2s
        stop_timeout: 5s
        resources:
          max_rss: "256MiB"
          on_exceed: kill
```

Validate and run it:

```bash
cliwrap validate -f cliwrap.yaml
cliwrap run      -f cliwrap.yaml
```

Then, from another terminal:

```bash
cliwrap list              # list supervised processes
cliwrap status redis      # inspect a single process
cliwrap stop   redis      # graceful stop (SIGTERM then SIGKILL on timeout)
```

## Configuration

| Key                           | Type     | Description                                                          |
|-------------------------------|----------|----------------------------------------------------------------------|
| `version`                     | string   | Config schema version. Currently `"1"`.                              |
| `runtime.dir`                 | path     | Runtime state directory (sockets, WAL, PID files). **Required.**     |
| `runtime.debug`               | enum     | `off` \| `info` \| `verbose` \| `trace`                              |
| `runtime.agent_path`          | path     | Override the `cliwrap-agent` binary location.                        |
| `system_budget.max_memory_percent` | float | Refuse new spawns when system memory exceeds this percent.          |
| `system_budget.min_free_memory`    | bytes | Minimum free memory to allow spawns. Accepts `256MiB`, `1GiB`, etc. |
| `system_budget.max_load_avg`       | float | Max 1-minute load average before refusing spawns.                   |
| `sandbox_providers`           | list     | Sandbox providers to enable (`noop`, `scriptdir`, or custom).        |
| `groups[].name`               | string   | Logical group name (e.g. `web`, `batch`).                            |
| `groups[].processes[]`        | list     | Process specs. See table below.                                      |

Per-process fields:

| Key                | Type       | Default        | Description                                       |
|--------------------|------------|----------------|---------------------------------------------------|
| `id`               | string     | —              | Unique process identifier. **Required.**          |
| `name`             | string     | `id`           | Human-readable name.                              |
| `command`          | path       | —              | Executable path. **Required.**                    |
| `args`             | list       | `[]`           | Command arguments.                                |
| `env`              | map        | `{}`           | Environment overrides.                            |
| `work_dir`         | path       | inherited      | Working directory.                                |
| `restart`          | enum       | `never`        | `never` \| `on_failure` \| `always`               |
| `max_restarts`     | int        | unlimited      | Cap on restart attempts before giving up.         |
| `restart_backoff`  | duration   | `1s`           | Initial backoff; doubles on repeated failures.    |
| `stop_timeout`     | duration   | `10s`          | SIGTERM → SIGKILL grace period.                   |
| `resources.max_rss`    | bytes  | unlimited      | Hard RSS cap. Accepts `256MiB`, `1GiB`.           |
| `resources.on_exceed`  | enum   | `kill`         | `kill` \| `signal:SIGTERM` \| `log`               |

Environment variables inside YAML values are expanded via `${VAR}` syntax.

## Architecture

```
 ┌──────────────────────────┐
 │         Host App         │
 │  (import pkg/cliwrap)    │
 │                          │
 │  Manager ─► Controller   │──┐
 └──────────────────────────┘  │  Unix socket, length-prefixed frames,
                               │  MessagePack payloads, persistent WAL
 ┌──────────────────────────┐  │
 │      cliwrap-agent       │◄─┘
 │  (one subprocess per ID) │
 │                          │
 │  Supervise ─► fork/exec  │──► child CLI process
 │    │                     │
 │    ├─ heartbeat loop     │
 │    ├─ log collector      │
 │    ├─ resource sampler   │
 │    └─ sandbox provider   │
 └──────────────────────────┘
```

Design goals, trade-offs, and rationale live in
[`docs/superpowers/specs/2026-04-07-cli-wrapper-design.md`](./docs/superpowers/specs/2026-04-07-cli-wrapper-design.md).

## Project Layout

| Path                   | Purpose                                          |
|------------------------|--------------------------------------------------|
| `pkg/cliwrap/`         | Public API (`Manager`, `Spec`, `SpecBuilder`)    |
| `pkg/config/`          | YAML loader and canonical `Config` types         |
| `pkg/event/`           | Event types and `Bus` interface                  |
| `pkg/sandbox/`         | Sandbox provider plug-in interface               |
| `internal/ipc/`        | Frame protocol + persistent outbox + WAL         |
| `internal/agent/`      | `cliwrap-agent` dispatcher logic                 |
| `internal/controller/` | Host-side agent controller                       |
| `internal/supervise/`  | Spawner, watcher, restart loop                   |
| `internal/logcollect/` | Ring buffer + file rotator                      |
| `internal/resource/`   | Per-process and system sampling                  |
| `internal/mgmt/`       | Management socket server and client              |
| `cmd/cliwrap/`         | Management CLI binary                            |
| `cmd/cliwrap-agent/`   | Agent subprocess binary                          |
| `test/integration/`    | End-to-end integration tests                     |
| `test/chaos/`          | Crash and slow-subscriber chaos tests            |
| `test/bench/`          | Benchmarks                                       |
| `docs/superpowers/`    | Design specs and implementation plans            |

## Development

```bash
make fixtures       # build test fixture binaries (echo, noisy, crasher, hanger)
make test           # unit tests with race detector
make integration    # end-to-end integration tests (needs fixtures)
make chaos          # chaos tests (slow subscribers, WAL replay, memory bounds)
make bench          # benchmarks
make fuzz-short     # 30s frame-reader fuzz run
make cover          # coverage with summary
make lint           # gofmt + go vet
```

CI runs unit, integration, chaos, and fuzz targets on `ubuntu-latest` and
`macos-latest` across Go 1.22 and 1.23.

## Roadmap

v1 targets macOS and Linux with a stable Go API. See
[`CHANGELOG.md`](./CHANGELOG.md) for a per-plan breakdown of shipped work.

Planned post-v1:

- [ ] `cliwrap logs` and `cliwrap events` streaming subcommands
- [ ] cgroups v2 throttling (CPU quota, I/O weight)
- [ ] Additional sandbox providers (bubblewrap, firejail, sandbox-exec profiles)
- [ ] Windows support via Job Objects
- [ ] Prometheus metrics exporter

## Contributing

Contributions are welcome and appreciated. Please read
[**CONTRIBUTING.md**](./CONTRIBUTING.md) before opening a pull request — it covers
the development workflow, commit conventions, test expectations, and the
**Developer Certificate of Origin (DCO)** signoff requirement.

Quick rules:

1. **Sign your commits** with `git commit -s` (DCO)
2. **All tests must pass** (`make test integration chaos`)
3. **No new goroutine leaks** (CI enforces `goleak`)
4. **gofmt clean** (`make lint`)
5. **One logical change per PR** — small and reviewable beats big and comprehensive

For major changes, please open an issue first to discuss the design.

## Security

If you discover a security vulnerability, please do **not** open a public issue.
See [`SECURITY.md`](./SECURITY.md) for the coordinated disclosure process.

## License

`cli-wrapper` is licensed under the **Apache License, Version 2.0**.
See [`LICENSE`](./LICENSE) for the full text and [`NOTICE`](./NOTICE) for
attribution requirements.

```
SPDX-License-Identifier: Apache-2.0
```

In short: you may use, modify, and redistribute this library — including
inside proprietary applications — provided that you:

- Preserve the copyright notice and the `LICENSE` and `NOTICE` files
- State any significant changes you make to the source
- Do not use the project's name, trademarks, or contributors' names to
  endorse derived products without permission

The Apache-2.0 license includes an explicit patent grant from contributors,
giving downstream users protection against patent claims on the code they
contributed.

## Acknowledgements

- [`go.uber.org/goleak`](https://github.com/uber-go/goleak) for the goroutine leak detection that keeps this library honest.
- [`github.com/vmihailenco/msgpack`](https://github.com/vmihailenco/msgpack) for fast, reflection-free MessagePack encoding.
- Design influenced by [`s6`](https://skarnet.org/software/s6/), [`runit`](http://smarden.org/runit/), and [`systemd`](https://systemd.io/)'s service supervision model.
