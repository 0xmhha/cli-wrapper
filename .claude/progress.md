# cli-wrapper Implementation Progress

## Completed Plans
- **Plan 01 — IPC Foundation**: 17/17 tasks DONE
  - Build: PASS, Vet: PASS, Tests: 36+ PASS (race), Coverage: 80.6%
  - All commits follow open-source conventional commit style
  - Key components: frame protocol, MessagePack codec, persistent outbox+WAL, Conn peer

## Completed Plans (continued)
- **Plan 02 — Process Supervision Core**: 20/20 tasks DONE
  - Build: PASS, Vet: PASS, Tests: PASS (race), goleak clean
  - 10 packages: agent, controller, cwtypes, eventbus, ipc, logcollect, platform, supervise, pkg/cliwrap, pkg/event + test/integration
  - Key components: Platform abstraction, Event bus, Log collector, Spec/Status types, SpecBuilder, ExponentialBackoff, Test fixtures, Agent binary (Runner+Dispatcher), Spawner (socketpair+SIGTERM→SIGKILL), Controller, Manager (public API), integration tests (crash detection, leak stress)

## Current Plan
- **Plan 03 — Resource & Sandbox**: NOT STARTED
  - Plan file: `docs/superpowers/plans/2026-04-07-cli-wrapper-plan-03-resource-sandbox.md`

## Remaining Plans
- Plan 04 — Config & Management CLI: NOT STARTED
- Plan 05 — Hardening & Reliability: NOT STARTED

## Commit Rules
- Open source style, English, concise
- NO co-author lines
- Conventional commits format (feat, fix, test, chore, ci, docs, style)

## TDD Process
- RED: write test first, confirm failure
- GREEN: write implementation, confirm pass
- Run `go test ./... -count=1 -race` after each task
- goleak.VerifyNone(t) required for goroutine tests

## Key Review Fixes Applied (from plan review)
- B06: internal/cwtypes leaf package to break import cycle (Plan 02)
- M06: Runner.OnStarted callback for CHILD_STARTED timing (Plan 02)
- M11: exec.Command instead of exec.CommandContext in Spawner (Plan 02)
- M10: AgentHandle.Close sends SIGTERM→SIGKILL (Plan 02)
- B02: Outbox done-channel pattern (Plan 01, already implemented)
- M03: DedupTracker watermark approach (Plan 01, already implemented)
- M05: WAL Retire rename-before-close (Plan 01, already implemented)
