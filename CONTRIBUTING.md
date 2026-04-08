# Contributing to cli-wrapper

Thank you for your interest in contributing! This document describes the
workflow, conventions, and expectations for contributing code, documentation,
tests, and bug reports to **cli-wrapper**.

Please be respectful and constructive in all project interactions — issues,
PRs, and discussions. Technical disagreements are welcome; personal attacks
are not.

---

## Table of Contents

- [Ways to Contribute](#ways-to-contribute)
- [Before You Start](#before-you-start)
- [Development Setup](#development-setup)
- [Running Tests](#running-tests)
- [Coding Standards](#coding-standards)
- [Commit Messages](#commit-messages)
- [Developer Certificate of Origin (DCO)](#developer-certificate-of-origin-dco)
- [Pull Request Process](#pull-request-process)
- [Reporting Bugs](#reporting-bugs)
- [Requesting Features](#requesting-features)
- [Security Vulnerabilities](#security-vulnerabilities)
- [License of Contributions](#license-of-contributions)

---

## Ways to Contribute

There is more than one way to help:

- Report bugs and regressions
- Improve documentation (including this file)
- Write tests (especially chaos / integration scenarios)
- Implement sandbox providers
- Propose and discuss design changes
- Triage open issues and review pull requests

Small contributions are just as welcome as large ones. A typo fix, a clearer
error message, or an added test case all count.

## Before You Start

- **Search existing issues and PRs** before opening a new one — your idea or bug
  may already be tracked.
- For **significant changes** (new public API, new CLI subcommand, new sandbox
  provider, architectural refactor), please open a **discussion issue first**
  to align on the approach before writing code.
- For **small changes** (bug fixes, docs, tests, internal refactors), feel free
  to open a PR directly.

## Development Setup

**Prerequisites:**

- Go **1.22 or newer** ([install](https://go.dev/dl/))
- `make`
- Linux or macOS (Windows is not currently supported)
- `git`

**Clone and build:**

```bash
git clone https://github.com/0xmhha/cli-wrapper.git
cd cli-wrapper
make fixtures                # build test fixture binaries
go build ./...               # verify everything compiles
```

**Recommended tooling:**

- [`gofmt`](https://pkg.go.dev/cmd/gofmt) (built in) — formatting is enforced
- [`go vet`](https://pkg.go.dev/cmd/vet) — part of `make lint`
- [`staticcheck`](https://staticcheck.dev/) — optional but recommended
- [`goleak`](https://github.com/uber-go/goleak) — already wired into test teardown

## Running Tests

cli-wrapper has four test tiers. Before submitting a PR, all four must pass:

```bash
make test             # unit tests (race detector, goleak)
make integration      # end-to-end tests with real subprocesses
make chaos            # chaos scenarios (slow subscribers, WAL replay)
make fuzz-short       # 30s fuzz run on the frame reader
```

For quick inner-loop iteration:

```bash
make test-short       # skips long tests
go test -run TestName ./path/to/package   # single test
```

Coverage:

```bash
make cover            # emits coverage.txt and prints the summary
```

**CI enforces:**

- `gofmt` clean (no pending formatting)
- `go vet` clean
- All unit, integration, chaos, and fuzz targets green on Linux **and** macOS
- No new goroutine leaks (`goleak` verifies teardown)

## Coding Standards

### Style

- **`gofmt`** — non-negotiable, enforced in CI
- **`go vet`** — must be clean
- **Effective Go** — follow the idioms ([link](https://go.dev/doc/effective_go))
- **Small files, small functions** — aim for <400 lines/file, <50 lines/func
- **No deep nesting** — more than 4 levels is a smell; extract helpers

### API design

- Public APIs live in `pkg/`. **Breaking changes require a discussion issue.**
- Internal helpers live in `internal/` — they are free to change at any time.
- Prefer **functional options** over large constructor parameter lists
  (see `WithAgentPath`, `WithRuntimeDir` for the established pattern).
- Errors are values. Return `error`, never panic in library code.
- **Wrap errors** with context: `fmt.Errorf("subsystem: action: %w", err)`.

### Tests

- **Every new feature needs a test.** Every bug fix needs a regression test.
- Prefer table-driven tests for units with many input permutations.
- Integration tests use real subprocesses via `test/fixtures/`.
- **Never introduce a goroutine leak.** If a test needs to wait for cleanup,
  use `goleak.VerifyNone(t)` or an existing teardown helper.
- Tests must be **deterministic**. No `time.Sleep`-based synchronization;
  use channels, sync primitives, or `testify/require.Eventually`.

### Concurrency

- Every goroutine you spawn must have a documented termination path.
- Channels are owned by the sender. Closing is the sender's responsibility.
- Prefer `context.Context` for cancellation over bespoke stop channels.

## Commit Messages

We follow a **Conventional Commits**–style format:

```
<type>(<scope>): <short summary>

<optional longer body explaining the why>

Signed-off-by: Your Name <your.email@example.com>
```

**Types:**

| Type       | Use for                                          |
|------------|--------------------------------------------------|
| `feat`     | A new feature                                    |
| `fix`      | A bug fix                                        |
| `docs`     | Documentation only                               |
| `test`     | Adding or improving tests                        |
| `refactor` | Code change that neither fixes a bug nor adds a feature |
| `perf`     | A performance improvement                        |
| `chore`    | Build scripts, dependencies, tooling             |
| `ci`       | CI/CD configuration changes                      |
| `style`    | Formatting, whitespace, `gofmt` only             |

**Scope** is the affected package or subsystem, e.g.
`ipc`, `supervise`, `sandbox`, `cli`, `config`.

**Examples:**

```
fix(supervise): avoid double-close of crash channel on restart

The restart loop closed the crash channel before the watcher goroutine
had observed it, leading to a panic under rapid restart-on-failure.
Close the channel exactly once, in the watcher goroutine.

Signed-off-by: Jane Doe <jane@example.com>
```

```
feat(sandbox): add bubblewrap provider

Signed-off-by: Jane Doe <jane@example.com>
```

Keep the summary line **≤72 characters**. Use the body for the **why**, not the what.

## Developer Certificate of Origin (DCO)

cli-wrapper uses the [Developer Certificate of Origin](https://developercertificate.org/)
to certify that contributors have the right to submit their work under the
project license.

By signing off on a commit, you certify the full text of the DCO, which is:

> By making a contribution to this project, I certify that:
>
> (a) The contribution was created in whole or in part by me and I have the
>     right to submit it under the open source license indicated in the file; or
>
> (b) The contribution is based upon previous work that, to the best of my
>     knowledge, is covered under an appropriate open source license and I have
>     the right under that license to submit that work with modifications,
>     whether created in whole or in part by me, under the same open source
>     license (unless I am permitted to submit under a different license), as
>     indicated in the file; or
>
> (c) The contribution was provided directly to me by some other person who
>     certified (a), (b) or (c) and I have not modified it.
>
> (d) I understand and agree that this project and the contribution are public
>     and that a record of the contribution (including all personal information
>     I submit with it, including my sign-off) is maintained indefinitely and
>     may be redistributed consistent with this project or the open source
>     license(s) involved.

### How to sign off

Add the `-s` (or `--signoff`) flag to your `git commit`:

```bash
git commit -s -m "fix(ipc): handle partial frame reads"
```

This appends a line to your commit message:

```
Signed-off-by: Your Name <your.email@example.com>
```

The name and email **must match** the identity configured in `git config
user.name` / `user.email`.

> **Tip:** add `git commit -s` as a shell alias, or set up a commit template
> so you never forget. You can also amend missing signoffs with:
> `git commit --amend -s --no-edit`

Our CI checks that every commit in a PR carries a valid `Signed-off-by` trailer.
PRs without one will be asked to re-sign their history.

## Pull Request Process

1. **Fork** the repository and create a topic branch off `main`.
   ```bash
   git checkout -b fix/supervise-double-close
   ```
2. **Make your changes** following the coding standards above.
3. **Add tests** that demonstrate the bug or exercise the feature.
4. **Run the full test suite**:
   ```bash
   make lint test integration chaos
   ```
5. **Sign your commits** (`git commit -s`).
6. **Push and open a PR** against `main`:
   - Fill in the PR template.
   - Link to related issues (e.g. `Fixes #123`).
   - Describe **what** changed and **why**.
   - Include test output or screenshots if helpful.
7. **Respond to review feedback.** Push follow-up commits rather than force-pushing
   until the review is complete, then squash or tidy history before merge.
8. A maintainer will merge once CI is green and the review is approved.

**We aim to triage new PRs within a week.** If you haven't heard back, a polite
ping is welcome.

## Reporting Bugs

Open a [bug report issue](https://github.com/0xmhha/cli-wrapper/issues/new?template=bug_report.yml)
and include:

- **cli-wrapper version** (commit hash if built from source)
- **OS and Go version** (`go version`, `uname -a`)
- **Minimal reproduction** — config file + commands, or a Go snippet
- **Observed behavior** vs **expected behavior**
- **Relevant logs** (with `runtime.debug: verbose` or `trace` if possible)

Bug reports with a reliable reproduction are fixed dramatically faster than
vague ones.

## Requesting Features

Open a [feature request issue](https://github.com/0xmhha/cli-wrapper/issues/new?template=feature_request.yml)
and describe:

- The **problem** you are trying to solve (not the solution)
- The **use case** — who benefits, and how often
- Any **alternatives** you considered
- Whether you are willing to **implement** it

Not every feature will be accepted. cli-wrapper aims to stay small and opinionated.
Features that can live cleanly as a sandbox provider or an external package are
usually preferred over new core surface area.

## Security Vulnerabilities

**Do not open a public issue for security vulnerabilities.** See
[`SECURITY.md`](./SECURITY.md) for the private disclosure process.

## License of Contributions

By contributing to cli-wrapper, you agree that your contributions will be
licensed under the **Apache License, Version 2.0**, the same license that
covers the project. See [`LICENSE`](./LICENSE) for the full text and
[`NOTICE`](./NOTICE) for attribution.

Apache-2.0 includes an explicit patent grant: by contributing code, you
grant downstream users a perpetual, worldwide, royalty-free patent license
covering any patent claims you own that are necessarily infringed by your
contribution. This is an important protection for everyone using the
project — please make sure you have the right to grant it before opening
a PR.

Your DCO sign-off certifies that you have the right to contribute under
this license.

---

Thank you for helping make cli-wrapper better.
