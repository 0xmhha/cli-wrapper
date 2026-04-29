# CW-G1 Agent Disconnect Detection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire `ipc.Conn` socket EOF / read errors into `controller.Controller`'s state machine so a crashed `cliwrap-agent` is detected within 5 seconds and the owning `ProcessHandle` reports `StateCrashed` with `CrashSource = CrashSourceConnectionLost`. Un-skip the existing chaos test that proves the behavior.

**Architecture:** Single callback contract between `ipc.Conn` and `controller.Controller`. Conn exposes `SetOnDisconnect(func(error))` invoked once from the read-loop exit path on EOF or unrecoverable error (suppressed when shutdown is initiated by `Conn.Close()`). Controller registers a callback that drives its existing state machine to `StateCrashed`.

**Tech Stack:** Go 1.22, `internal/ipc`, `internal/controller`, `pkg/cliwrap` (no new dependencies).

**Spec:** `docs/superpowers/specs/2026-04-29-CW-G1-agent-disconnect-design.md`

---

## Project Conventions (apply to EVERY task)

(Same as other cli-wrapper plans; reproduced here so this plan stands alone.)

- **DCO sign-off mandatory**: every `git commit` uses `-s`. CI rejects unsigned commits.
- **Conventional Commits**: `feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `style:`, `chore:`, `build:`, `bench:` (with optional scope). Subject ≤72 chars.
- **Forbidden trailers**: never `Co-Authored-By:` or "Generated with Claude" lines.
- **Test naming**: `TestSubject_Behavior` underscore.
- **Goroutine hygiene**: every test package that spawns goroutines includes `TestMain` with `goleak.VerifyTestMain(m)`. The packages this plan touches (`internal/ipc`, `internal/controller`) already have such `TestMain`s.
- **errcheck**: bare `defer X(...)` on a function that returns an error fails lint. Use `defer func() { _ = h.Close() }()`.

---

## File Structure

| Path | Action | Responsibility |
|---|---|---|
| `internal/ipc/errors.go` | Modify | Add `ErrAgentDisconnectedEOF`, `ErrAgentDisconnectedUnexpected` |
| `internal/ipc/conn.go` | Modify | Add `SetOnDisconnect`; invoke from readLoop exit; suppress on normal Close |
| `internal/ipc/conn_disconnect_test.go` | Create | 3 unit tests: fires on EOF, doesn't fire on Close, fires at most once |
| `internal/controller/controller.go` | Modify | In `Start`, register `SetOnDisconnect` callback that drives state machine to `StateCrashed` |
| `internal/controller/controller_disconnect_test.go` | Create | 1 unit test: synthetic disconnect → `StateCrashed` observed via Status |
| `test/chaos/pty_agent_crash_test.go` | Modify | Remove `t.Skip`; verify integration end-to-end |
| `CHANGELOG.md` | Modify | New entry under `[Unreleased]` — fix CW-G1 |

---

## Task 1: Add public sentinel errors for disconnect

**Files:**
- Modify: `internal/ipc/errors.go` (or create if absent)

- [ ] **Step 1: Failing test**

`internal/ipc/errors_test.go` (extend if exists; create if absent):

```go
func TestDisconnectErrors_Distinct(t *testing.T) {
    require.NotEqual(t, ErrAgentDisconnectedEOF, ErrAgentDisconnectedUnexpected)
    require.NotEmpty(t, ErrAgentDisconnectedEOF.Error())
    require.NotEmpty(t, ErrAgentDisconnectedUnexpected.Error())
}
```

- [ ] **Step 2: Run to fail**

```bash
cd /Users/wm-it-22-00661/Work/github/study/ai/harness/cli-wrapper
go test ./internal/ipc/ -run TestDisconnectErrors -v
```

- [ ] **Step 3: Add the constants**

In `internal/ipc/errors.go`:

```go
package ipc

import "errors"

// ... existing var block ...

var (
    ErrAgentDisconnectedEOF        = errors.New("ipc: agent disconnected (EOF)")
    ErrAgentDisconnectedUnexpected = errors.New("ipc: agent disconnected (unexpected)")
)
```

- [ ] **Step 4: Run to pass**

```bash
go test ./internal/ipc/ -run TestDisconnectErrors -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc/
git commit -s -m "feat(ipc): add disconnect sentinel errors"
```

---

## Task 2: Add `SetOnDisconnect` and wire into readLoop

**Files:**
- Modify: `internal/ipc/conn.go`
- Test: `internal/ipc/conn_disconnect_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestConn_OnDisconnect_FiresOnSocketEOF(t *testing.T) {
    a, b, err := socketPair()
    require.NoError(t, err)
    defer func() { _ = b.Close() }()

    conn := NewConn(a, /* ... existing constructor args ... */)
    var fired int32
    var gotErr error
    var mu sync.Mutex
    conn.SetOnDisconnect(func(err error) {
        atomic.StoreInt32(&fired, 1)
        mu.Lock(); gotErr = err; mu.Unlock()
    })
    go conn.Run() // or whatever starts readLoop

    // Trigger EOF on the other side.
    require.NoError(t, b.Close())

    require.Eventually(t, func() bool {
        return atomic.LoadInt32(&fired) == 1
    }, time.Second, 10*time.Millisecond)

    mu.Lock(); defer mu.Unlock()
    require.ErrorIs(t, gotErr, ErrAgentDisconnectedEOF)
}

func TestConn_OnDisconnect_DoesNotFireOnNormalClose(t *testing.T) {
    a, b, err := socketPair()
    require.NoError(t, err)
    defer func() { _ = b.Close() }()

    conn := NewConn(a, /* ... */)
    var fired int32
    conn.SetOnDisconnect(func(error) { atomic.StoreInt32(&fired, 1) })
    go conn.Run()

    time.Sleep(50 * time.Millisecond)
    require.NoError(t, conn.Close())
    time.Sleep(200 * time.Millisecond)

    require.Equal(t, int32(0), atomic.LoadInt32(&fired),
        "OnDisconnect must NOT fire when shutdown is initiated locally")
}

func TestConn_OnDisconnect_FiresAtMostOnce(t *testing.T) {
    a, b, _ := socketPair()
    defer func() { _ = b.Close() }()

    conn := NewConn(a, /* ... */)
    var calls int32
    conn.SetOnDisconnect(func(error) { atomic.AddInt32(&calls, 1) })
    go conn.Run()

    require.NoError(t, b.Close()) // EOF #1
    time.Sleep(100 * time.Millisecond)
    _ = conn.Close()              // would-be EOF #2
    time.Sleep(100 * time.Millisecond)

    require.Equal(t, int32(1), atomic.LoadInt32(&calls))
}

// socketPair returns two connected *os.File suitable for cli-wrapper Conn.
// Adapt to whatever helper already exists in conn_test.go.
func socketPair() (*os.File, *os.File, error) {
    fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
    if err != nil { return nil, nil, err }
    return os.NewFile(uintptr(fds[0]), "a"), os.NewFile(uintptr(fds[1]), "b"), nil
}
```

(Adjust `NewConn` constructor args to actual signature. If a `socketPair` helper already exists in `conn_test.go`, reuse instead of redefining.)

- [ ] **Step 2: Run to fail**

```bash
go test ./internal/ipc/ -run TestConn_OnDisconnect -v
```
Expected: undefined `SetOnDisconnect`.

- [ ] **Step 3: Implement `SetOnDisconnect` and wire into readLoop**

In `internal/ipc/conn.go`:

```go
type Conn struct {
    // ... existing fields ...
    disconnectMu sync.Mutex
    onDisconnect func(error)
    closing      atomic.Bool       // set by Close(); suppresses callback
    fireOnce     sync.Once
}

// SetOnDisconnect registers a callback invoked once when the read loop exits
// due to EOF or unrecoverable error (NOT from a normal Close). Callbacks are
// invoked at most once per Conn lifetime.
func (c *Conn) SetOnDisconnect(cb func(error)) {
    c.disconnectMu.Lock()
    c.onDisconnect = cb
    c.disconnectMu.Unlock()
}

func (c *Conn) fireDisconnect(cause error) {
    if c.closing.Load() {
        return // suppress: shutdown was locally initiated
    }
    c.fireOnce.Do(func() {
        c.disconnectMu.Lock()
        cb := c.onDisconnect
        c.disconnectMu.Unlock()
        if cb != nil {
            cb(cause)
        }
    })
}
```

Modify the existing `Close()`:

```go
func (c *Conn) Close() error {
    c.closing.Store(true)
    // ... existing close logic ...
}
```

Modify the existing `readLoop` (or whatever the read goroutine is named) exit path:

```go
func (c *Conn) readLoop() {
    var exitErr error
    defer func() {
        if errors.Is(exitErr, io.EOF) {
            c.fireDisconnect(ErrAgentDisconnectedEOF)
        } else if exitErr != nil {
            c.fireDisconnect(fmt.Errorf("%w: %w", ErrAgentDisconnectedUnexpected, exitErr))
        } else {
            c.fireDisconnect(ErrAgentDisconnectedEOF)
        }
    }()

    for {
        // ... existing read loop body ...
        // on read error, set exitErr = err and return
    }
}
```

(Adapt to actual `readLoop` shape — preserve all existing behavior; only ADD the deferred fire.)

- [ ] **Step 4: Run to pass**

```bash
go test ./internal/ipc/ -run TestConn_OnDisconnect -v -race
```
Expected: PASS.

- [ ] **Step 5: Run full ipc package to check no regression**

```bash
go test ./internal/ipc/ -v -race
```

- [ ] **Step 6: Commit**

```bash
git add internal/ipc/
git commit -s -m "feat(ipc): SetOnDisconnect callback fires on socket EOF / read error

The callback fires at most once per Conn and is suppressed when shutdown
is initiated locally via Close(). Closes the gap where readLoop exited
silently on agent process death; the controller can now observe the loss."
```

---

## Task 3: Wire callback into Controller state machine

**Files:**
- Modify: `internal/controller/controller.go`
- Test: `internal/controller/controller_disconnect_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestController_StateTransitionsOnDisconnect(t *testing.T) {
    h, agent := newTestController(t) // existing helper from controller_test.go
    require.NoError(t, h.Start(context.Background(), Spec{ /* minimal valid spec */ }))

    // Wait until controller is in StateRunning.
    require.Eventually(t, func() bool {
        return h.Status().State == StateRunning
    }, time.Second, 10*time.Millisecond)

    // Force the underlying Conn's disconnect callback to fire.
    agent.ForceDisconnect(ipc.ErrAgentDisconnectedEOF)

    require.Eventually(t, func() bool {
        st := h.Status()
        return st.State == StateCrashed && st.CrashSource == CrashSourceConnectionLost
    }, time.Second, 10*time.Millisecond,
        "expected StateCrashed with CrashSourceConnectionLost; got %+v", h.Status())
}
```

(`agent.ForceDisconnect` is a new method on the test helper. If the helper doesn't expose access to the underlying Conn, add a thin method or use a fake Conn for this specific test.)

- [ ] **Step 2: Run to fail**

```bash
go test ./internal/controller/ -run TestController_StateTransitionsOnDisconnect -v
```

- [ ] **Step 3: Implement**

In `internal/controller/controller.go` `Start()`, after the connection is established but before any other state transition:

```go
c.conn.SetOnDisconnect(func(cause error) {
    c.transitionToState(StateUpdate{
        State:       StateCrashed,
        CrashSource: CrashSourceConnectionLost,
        Message:     fmt.Sprintf("agent disconnected: %v", cause),
    })
})
```

(Adapt method names. If `transitionToState` is called something else, use that. The struct fields may be different — copy the controller's existing patterns.)

- [ ] **Step 4: Run to pass**

```bash
go test ./internal/controller/ -v -race
```

- [ ] **Step 5: Commit**

```bash
git add internal/controller/
git commit -s -m "feat(controller): transition to StateCrashed on agent disconnect

Wires the new ipc.Conn.SetOnDisconnect callback into the controller's
state machine. CrashSource is set to CrashSourceConnectionLost.
Resolves CW-G1: chaos test pty_agent_crash_test.go can now un-skip."
```

---

## Task 4: Un-skip integration chaos test and verify end-to-end

**Files:**
- Modify: `test/chaos/pty_agent_crash_test.go`

- [ ] **Step 1: Remove the skip**

Replace the body of `TestPTY_AgentCrashDetectedWithin5s` with the original implementation that was deferred. The original (per `5b87dac` commit description) was:

```go
//go:build chaos
package chaos

import (
    "os/exec"
    "syscall"
    "testing"
    "time"

    "github.com/stretchr/testify/require"
    "github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

func TestPTY_AgentCrashDetectedWithin5s(t *testing.T) {
    m := setupManager(t) // existing chaos test helper
    h, err := m.Spawn(ctxT(t), cliwrap.Spec{
        ID: "crash", Command: "/usr/bin/yes", PTY: &cliwrap.PTYConfig{},
    })
    require.NoError(t, err)
    defer func() { _ = h.Stop(ctxT(t)) }()

    // Find and SIGKILL the cliwrap-agent that hosts this child.
    pid := mustAgentPID(t, h)
    require.NoError(t, syscall.Kill(pid, syscall.SIGKILL))

    require.Eventually(t, func() bool {
        st := h.Status()
        return st.State == cliwrap.StateCrashed || st.State == cliwrap.StateStopped
    }, 5*time.Second, 100*time.Millisecond,
        "agent SIGKILL should be detected within 5s; status=%+v", h.Status())
}

// mustAgentPID locates the cliwrap-agent process supervising the given handle.
// Implementation: parses ps for the child PID's PPID, OR uses pgrep if the
// agent registers under a known name.
func mustAgentPID(t *testing.T, h cliwrap.ProcessHandle) int {
    t.Helper()
    childPID, err := h.ChildPID(ctxT(t))
    require.NoError(t, err)
    out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(childPID)).Output()
    require.NoError(t, err)
    pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
    require.NoError(t, err)
    return pid
}
```

(Adjust `setupManager`, `ctxT`, `mustAgentPID` to actual chaos-test helpers. The original implementation in commit history may have used a different agent-PID strategy; preserve whatever was there minus the `t.Skip`.)

- [ ] **Step 2: Run**

```bash
go test -tags chaos -race ./test/chaos/ -run TestPTY_AgentCrashDetectedWithin5s -v
```
Expected: PASS within ~5 s.

- [ ] **Step 3: Commit**

```bash
git add test/chaos/pty_agent_crash_test.go
git commit -s -m "test(chaos): un-skip PTY agent crash test (CW-G1 resolved)

The skip marker added in 5b87dac is removed now that ipc.Conn fires
SetOnDisconnect and controller transitions to StateCrashed within 5s
of agent SIGKILL. End-to-end chaos test verifies the behavior under
-race."
```

---

## Task 5: CHANGELOG + final verification

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Add entry under `[Unreleased]`**

```markdown
### Fixed
- CW-G1: cliwrap-agent crashes are now detected by the host within ~5s.
  `ipc.Conn` fires a new `SetOnDisconnect` callback on socket EOF /
  unrecoverable read error; the controller transitions to `StateCrashed`
  with `CrashSource = CrashSourceConnectionLost`. Resolves the gap that
  caused `test/chaos/pty_agent_crash_test.go` to be skipped.
```

- [ ] **Step 2: Final verification across the whole matrix**

```bash
go test ./...
go test -tags integration ./...
go test -tags chaos ./...
```
Expected: all green with `-race`. The previously-skipped chaos test now passes.

- [ ] **Step 3: Commit**

```bash
git add CHANGELOG.md
git commit -s -m "docs(changelog): record CW-G1 fix"
```

---

## Final Verification Snapshot

After all 5 tasks:

```bash
go test -race ./...
go test -race -tags integration ./...
go test -race -tags chaos ./...
go test -tags bench ./test/bench/ -bench . -benchtime=10x
```

All suites green. The chaos test `TestPTY_AgentCrashDetectedWithin5s` flips from SKIP to PASS.

Update the cross-repo doc:

```bash
# In ai-m repo:
# - mark CW-G1 row in PHASE2-ROADMAP.md as RESOLVED with this plan's commits
# - move CW-G1 from "Discovered Gaps" to "Resolved" section in STATUS.md
# - delete (or mark complete) the corresponding TodoWrite entry
```
