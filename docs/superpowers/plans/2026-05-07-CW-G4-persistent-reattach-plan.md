# CW-G4 Persistent Sessions + Reattach Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-session opt-in persistence (`Spec.Persistent=true`) so the supervised agent + child outlive the host process, with a reattach API that lets a new host discover and reconnect to surviving sessions.

**Architecture:** Spawner-side `Setsid: true` + stdio redirect at fork time daemonizes persistent agents. Agents listen on a per-session UNIX socket at `<sessionDir>/sock` for reattach. New `Manager.ListPersistent` + `Manager.Reattach` discover and reconnect. PTY ring buffer (in-memory, default 256 KiB) is dumped to the new host on reattach so terminals render the current screen. Single-host attach at a time; second concurrent attach is rejected with `ErrAlreadyAttached`. No automatic cleanup; user-driven termination via `cliwrap kill <id>` or `h.Stop`.

**Tech Stack:** Go 1.22+, `internal/agent`, `internal/supervise`, `internal/ipc`, `internal/controller`, `pkg/cliwrap`, `cmd/cliwrap`, `test/integration`, `test/chaos`. No new external dependencies.

**Spec:** `docs/superpowers/specs/2026-05-07-CW-G4-persistent-reattach-design.md` (commit `033d43d`).

---

## Project Conventions (apply to EVERY task)

- **DCO sign-off mandatory**: every `git commit` uses `-s`. CI rejects unsigned commits. Forgotten sign-offs are recoverable via `git rebase HEAD~N --exec "git commit --amend --no-edit -s"`.
- **Conventional Commits**: `feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `chore:` with optional scope. Subject ≤72 chars.
- **Forbidden trailers**: never `Co-Authored-By:` or "Generated with Claude" lines.
- **Test naming**: `TestSubject_Behavior` (underscore between subject and behavior).
- **Goroutine hygiene**: every test package that spawns goroutines includes `TestMain` with `goleak.VerifyTestMain(m)`. New test packages added by this plan must include this.
- **errcheck**: bare `defer X(...)` on a function that returns an error fails lint. Use `defer func() { _ = h.Close() }()`.
- **Avoid unnecessary nested defer inside an outer defer'd anonymous function** — prefer direct sequential calls (lesson from CW-G3, commit `af0b9fe`).
- **TDD discipline**: every code task starts with a failing test (`go test -run NewTestName ./...` returns FAIL), then minimal code to GREEN, then refactor.
- **`-race` clean**: every test must pass with `go test -race ./...`.

---

## File Structure

### New files (created in this plan)

| Path | Responsibility |
|---|---|
| `pkg/cliwrap/persistent.go` | `WithPersistentDir`, `PersistentSessionInfo`, `Manager.ListPersistent`, `Manager.Reattach` |
| `pkg/cliwrap/persistent_test.go` | unit/integration tests for above |
| `internal/agent/persistent.go` | `--persistent` flag handler, accept loop, attach state |
| `internal/agent/persistent_test.go` | unit tests for persistent-mode handler |
| `internal/agent/persistent_meta.go` | meta.json + pid file write/read |
| `internal/agent/persistent_meta_test.go` | meta IO round-trip + rollover detection tests |
| `internal/agent/ringbuffer.go` | byte ring buffer with `Write` + `Snapshot` |
| `internal/agent/ringbuffer_test.go` | boundary + concurrency tests |
| `cmd/cliwrap/cmd_list_persistent.go` | `cliwrap list --persistent` subcommand |
| `cmd/cliwrap/cmd_kill.go` | `cliwrap kill <id>` subcommand |
| `cmd/cliwrap/cmd_kill_test.go` | feature test for kill |
| `test/integration/pty_persistent_test.go` | end-to-end persistent + reattach scenarios |
| `test/chaos/persistent_burst_test.go` | opt-in chaos burst test for persistent path |

### Modified files

| Path | Action |
|---|---|
| `pkg/cliwrap/spec.go` | add `Persistent`, `RingBufferSize` fields + `Validate()` checks |
| `pkg/cliwrap/spec_test.go` | tests for new validation |
| `pkg/cliwrap/errors.go` (or `errors_persistent.go`) | new error sentinels |
| `pkg/cliwrap/manager.go` | new manager option `WithPersistentDir`; persistentDir field |
| `pkg/cliwrap/process.go` | `Close` semantics for persistent sessions (do not SIGTERM agent) |
| `pkg/cliwrap/manager_pty.go` (whichever owns SubscribePTYData) | initial-buffer prepend on reattach-derived handle |
| `internal/supervise/spawner.go` | `Persistent=true` branch: `Setsid: true`, stdio redirect, env + argv |
| `internal/supervise/spawner_test.go` | tests for the new branch |
| `internal/agent/main.go` | detect `--persistent` argv flag; route to persistent handler |
| `internal/agent/dispatcher.go` | broadcast PTY data to ring buffer when persistent; `MsgChildExited` triggers self-terminate when persistent |
| `internal/agent/runner_pty.go` | hook ring buffer into `OnData` callback |
| `internal/agent/capability.go` | advertise `"persistence"` in CapabilityReply |
| `internal/agent/capability_test.go` | test for the advertisement |
| `internal/ipc/types.go` (or wherever MsgType is) | add `MsgTypeReattachQuery`, `MsgTypePTYRingDump` |
| `internal/ipc/messages_test.go` | round-trip serialization for new msg types |
| `internal/controller/controller.go` | handle MsgPTYRingDump → store as initial-buffer for first SubscribePTYData |
| `internal/controller/controller_test.go` | test initial-buffer semantics |
| `cmd/cliwrap/main.go` (or root cmd) | register list-persistent + kill subcommands |
| `CHANGELOG.md` | new `[Unreleased]` entry under `### Added` |

---

## Task Index

| # | Task | Effort |
|---:|---|---:|
| 1 | Spec extensions + validation (`Spec.Persistent`, `Spec.RingBufferSize`) | XS |
| 2 | New error sentinels | XS |
| 3 | `WithPersistentDir` Manager option | XS |
| 4 | Persistent meta.json + pid file IO | S |
| 5 | Ring buffer impl + tests | S |
| 6 | New IPC message types (MsgReattachQuery, MsgPTYRingDump) | XS |
| 7 | Capability advertisement (`"persistence"`) + Spawn refusal on mismatch | S |
| 8 | Spawner persistent branch (Setsid + stdio redirect + argv/env) | S |
| 9 | Agent `--persistent` flag handler + listener bootstrap | M |
| 10 | Accept loop + attach lock + connection adoption | M |
| 11 | Ring buffer integration into PTY hot path | XS |
| 12 | MsgPTYRingDump on attach + controller initial-buffer wiring | M |
| 13 | `Manager.ListPersistent` | S |
| 14 | `Manager.Reattach` (full error matrix) | M |
| 15 | Stop/Close semantics for persistent (Stop self-terminates) | S |
| 16 | Child natural exit triggers agent self-terminate when persistent | XS |
| 17 | CLI commands: `list --persistent`, `kill <id>` | S |
| 18 | Integration tests (full scenarios from spec §Test Strategy) | M |
| 19 | Chaos test: persistent burst regression | S |
| 20 | CHANGELOG + final acceptance | XS |

---

## Task 1: Spec extensions + validation

Add `Persistent` and `RingBufferSize` fields to `Spec` with bounded validation.

**Files:**
- Modify: `pkg/cliwrap/spec.go`
- Modify: `pkg/cliwrap/spec_test.go`

- [ ] **Step 1: Read current Spec definition**

```bash
grep -n "type Spec\|Validate" pkg/cliwrap/spec.go | head -10
```

Note where `Spec` struct is and where the existing `Validate()` method body is. New fields go at the bottom of the struct; new validation goes at the bottom of `Validate()`.

- [ ] **Step 2: Failing tests**

In `pkg/cliwrap/spec_test.go`, append:

```go
func TestSpec_Validate_PersistentDefaults(t *testing.T) {
	s := Spec{ID: "test", Command: "/bin/cat"}
	if err := s.Validate(); err != nil {
		t.Fatalf("default Spec should validate; got %v", err)
	}
	if s.Persistent {
		t.Fatalf("Persistent should default false")
	}
	if s.RingBufferSize != 0 {
		t.Fatalf("RingBufferSize should default zero (resolved at agent startup)")
	}
}

func TestSpec_Validate_RingBufferSizeRejectsNegative(t *testing.T) {
	s := Spec{ID: "test", Command: "/bin/cat", Persistent: true, RingBufferSize: -1}
	err := s.Validate()
	if err == nil {
		t.Fatalf("expected error for negative RingBufferSize")
	}
	if !strings.Contains(err.Error(), "RingBufferSize") {
		t.Fatalf("error should mention RingBufferSize; got %v", err)
	}
}

func TestSpec_Validate_RingBufferSizeRejectsOversize(t *testing.T) {
	s := Spec{ID: "test", Command: "/bin/cat", Persistent: true, RingBufferSize: 65 * 1024 * 1024}
	err := s.Validate()
	if err == nil {
		t.Fatalf("expected error for RingBufferSize > 64 MiB cap")
	}
}

func TestSpec_Validate_RingBufferSizeIgnoredWhenNotPersistent(t *testing.T) {
	// Pathological RingBufferSize is OK as long as Persistent=false.
	s := Spec{ID: "test", Command: "/bin/cat", Persistent: false, RingBufferSize: -1}
	if err := s.Validate(); err != nil {
		t.Fatalf("RingBufferSize should be ignored when Persistent=false; got %v", err)
	}
}
```

If `strings` isn't imported in this test file already, add it.

- [ ] **Step 3: Run to fail**

```bash
go test -run TestSpec_Validate_Persistent -v ./pkg/cliwrap/
```

Expected: compile errors (Persistent / RingBufferSize fields don't exist yet).

- [ ] **Step 4: Add struct fields + validation**

In `pkg/cliwrap/spec.go`, inside `type Spec struct { ... }`, add at the bottom:

```go
	// Persistent makes the agent + child outlive the host process. When true,
	// the agent self-daemonizes at spawn time and accepts reattach via a
	// UNIX socket at WithPersistentDir/<ID>/sock.
	//
	// Default false: foreground-mode session, identical to historical behavior.
	Persistent bool

	// RingBufferSize is the in-memory PTY output buffer size in bytes,
	// used to redraw the current screen state on reattach. Only meaningful
	// when Persistent=true. Zero means "use default 256 KiB".
	RingBufferSize int
```

In `Validate()`, append before the final `return nil` (or wherever it ends successfully):

```go
	if s.Persistent {
		if s.RingBufferSize < 0 {
			return fmt.Errorf("Spec.RingBufferSize must be >= 0 (got %d)", s.RingBufferSize)
		}
		const maxRingBuffer = 64 * 1024 * 1024
		if s.RingBufferSize > maxRingBuffer {
			return fmt.Errorf("Spec.RingBufferSize must be <= %d bytes (got %d)", maxRingBuffer, s.RingBufferSize)
		}
	}
```

- [ ] **Step 5: Run tests, expect green**

```bash
go test -run TestSpec_Validate -v ./pkg/cliwrap/ -race
```

Expected: PASS for the four new tests + no regression on existing.

- [ ] **Step 6: Commit**

```bash
git add pkg/cliwrap/spec.go pkg/cliwrap/spec_test.go
git commit -s -m "feat(spec): add Persistent + RingBufferSize fields (CW-G4)

Spec.Persistent bool (default false) is the per-session opt-in flag.
Spec.RingBufferSize int (default 0 = use 256 KiB at agent startup)
controls the in-memory PTY output buffer used to redraw on reattach.

Validation rejects negative RingBufferSize and > 64 MiB caps when
Persistent=true. When Persistent=false, RingBufferSize is ignored
(forward-compat: callers may set it without effect).

Spec: docs/superpowers/specs/2026-05-07-CW-G4-persistent-reattach-design.md"
```

---

## Task 2: New error sentinels

**Files:**
- Modify: `pkg/cliwrap/errors.go` (extend; if very crowded, create `errors_persistent.go`)
- Modify: `pkg/cliwrap/errors_test.go`

- [ ] **Step 1: Failing test**

In `pkg/cliwrap/errors_test.go`:

```go
func TestPersistentErrors_AreDistinctAndNonEmpty(t *testing.T) {
	errs := []error{
		ErrSessionNotFound,
		ErrSessionAlreadyExists,
		ErrAgentDead,
		ErrAlreadyAttached,
		ErrIncompatibleAgent,
		ErrPersistenceUnsupportedByAgent,
	}
	seen := map[string]bool{}
	for _, e := range errs {
		if e == nil {
			t.Fatalf("nil error in set")
		}
		msg := e.Error()
		if msg == "" {
			t.Fatalf("empty Error() string for %T", e)
		}
		if seen[msg] {
			t.Fatalf("duplicate error message: %q", msg)
		}
		seen[msg] = true
	}
}
```

- [ ] **Step 2: Run to fail**

```bash
go test -run TestPersistentErrors -v ./pkg/cliwrap/
```

Expected: compile errors (sentinels undefined).

- [ ] **Step 3: Define sentinels**

In `pkg/cliwrap/errors.go`, add:

```go
// CW-G4 persistent-session error sentinels.
var (
	// ErrSessionNotFound is returned by Reattach when the session ID has
	// no metadata directory under WithPersistentDir.
	ErrSessionNotFound = errors.New("cliwrap: persistent session not found")

	// ErrSessionAlreadyExists is returned by Spawn (Persistent=true) when
	// a session with the same ID already exists in WithPersistentDir.
	// The user must terminate or rm the existing session first; cli-wrapper
	// does not auto-clean stale dirs.
	ErrSessionAlreadyExists = errors.New("cliwrap: persistent session already exists")

	// ErrAgentDead is returned by Reattach when the pid file points to a
	// dead PID, OR when the agent's MsgHello.startedAt does not match the
	// recorded meta.json startedAt (PID rollover defense).
	ErrAgentDead = errors.New("cliwrap: persistent agent is no longer alive")

	// ErrAlreadyAttached is returned by Reattach when another host is
	// currently connected to the session.
	ErrAlreadyAttached = errors.New("cliwrap: persistent session already attached by another host")

	// ErrIncompatibleAgent is returned by Reattach when the agent's
	// capability version does not match the host's expectations.
	ErrIncompatibleAgent = errors.New("cliwrap: incompatible agent version")

	// ErrPersistenceUnsupportedByAgent is returned at Spawn time when the
	// caller passes Persistent=true but the agent did not advertise the
	// "persistence" capability.
	ErrPersistenceUnsupportedByAgent = errors.New("cliwrap: agent does not support persistent sessions")
)
```

If `errors` is not imported, add `"errors"` to the import block.

- [ ] **Step 4: Run tests, expect green**

```bash
go test -run TestPersistentErrors -v ./pkg/cliwrap/
```

- [ ] **Step 5: Commit**

```bash
git add pkg/cliwrap/errors.go pkg/cliwrap/errors_test.go
git commit -s -m "feat(errors): add CW-G4 persistent-session error sentinels

Six new sentinels: ErrSessionNotFound, ErrSessionAlreadyExists,
ErrAgentDead, ErrAlreadyAttached, ErrIncompatibleAgent,
ErrPersistenceUnsupportedByAgent. Used by Manager.ListPersistent,
Manager.Reattach, and the Persistent=true Spawn path."
```

---

## Task 3: `WithPersistentDir` Manager option

**Files:**
- Modify: `pkg/cliwrap/manager.go`
- Modify: `pkg/cliwrap/manager_test.go` (extend; or create if absent)

- [ ] **Step 1: Failing test**

```go
func TestManager_WithPersistentDirSetsField(t *testing.T) {
	tmp := t.TempDir()
	m, err := NewManager(WithPersistentDir(tmp))
	if err != nil { t.Fatalf("NewManager: %v", err) }
	defer func() { _ = m.Shutdown(context.Background()) }()

	if m.persistentDir != tmp {
		t.Fatalf("persistentDir = %q; want %q", m.persistentDir, tmp)
	}
}

func TestManager_DefaultPersistentDir(t *testing.T) {
	m, err := NewManager()
	if err != nil { t.Fatalf("NewManager: %v", err) }
	defer func() { _ = m.Shutdown(context.Background()) }()

	want := filepath.Join(os.Getenv("HOME"), ".cliwrap", "sessions")
	if m.persistentDir != want {
		t.Fatalf("default persistentDir = %q; want %q", m.persistentDir, want)
	}
}
```

(If `os`, `filepath`, `context` not yet imported, add them.)

- [ ] **Step 2: Run to fail**

```bash
go test -run TestManager_WithPersistentDir -v ./pkg/cliwrap/
go test -run TestManager_DefaultPersistentDir -v ./pkg/cliwrap/
```

Expected: undefined `WithPersistentDir`, undefined field `persistentDir`.

- [ ] **Step 3: Add field + option**

In `pkg/cliwrap/manager.go`, find the `Manager` struct and add:

```go
type Manager struct {
	/* existing fields */
	persistentDir string
}
```

Find the option-builder section (look for other `WithX` functions) and add:

```go
// WithPersistentDir overrides the directory where persistent-session
// metadata is stored. Default: $HOME/.cliwrap/sessions/. The directory
// is created with mode 0700 lazily on first persistent Spawn;
// per-session subdirectories are 0700 with files 0600.
//
// Tests should pass t.TempDir() to keep state isolated.
func WithPersistentDir(path string) ManagerOption {
	return func(m *Manager) {
		m.persistentDir = path
	}
}
```

In `NewManager` (or wherever defaults are applied), add a default after options apply:

```go
// CW-G4: default persistent dir if caller didn't override.
if m.persistentDir == "" {
	m.persistentDir = filepath.Join(os.Getenv("HOME"), ".cliwrap", "sessions")
}
```

(Add `"os"` and `"path/filepath"` imports if not present.)

- [ ] **Step 4: Run tests, expect green**

```bash
go test -run TestManager_WithPersistentDir -v ./pkg/cliwrap/ -race
go test -run TestManager_DefaultPersistentDir -v ./pkg/cliwrap/ -race
go test ./pkg/cliwrap/ -race    # no regression
```

- [ ] **Step 5: Commit**

```bash
git add pkg/cliwrap/manager.go pkg/cliwrap/manager_test.go
git commit -s -m "feat(manager): add WithPersistentDir option (CW-G4)

Defaults to \$HOME/.cliwrap/sessions/. Tests pass t.TempDir() for
isolation. Used by Manager.ListPersistent, Manager.Reattach, and the
persistent Spawn path (subsequent tasks)."
```

---

## Task 4: Persistent meta.json + pid file IO

**Files:**
- Create: `internal/agent/persistent_meta.go`
- Create: `internal/agent/persistent_meta_test.go`

- [ ] **Step 1: Failing tests**

`internal/agent/persistent_meta_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

func TestPersistentMeta_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	meta := PersistentMeta{
		Version:   "1.0",
		ID:        "test-session-1",
		Spec:      cwtypes.Spec{ID: "test-session-1", Command: "/bin/cat", Persistent: true, RingBufferSize: 1024},
		AgentPID:  99999,
		StartedAt: time.Date(2026, 5, 7, 10, 23, 4, 123456789, time.UTC),
	}
	if err := WritePersistentMeta(dir, meta); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := ReadPersistentMeta(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.ID != meta.ID || got.Version != meta.Version {
		t.Fatalf("round-trip mismatch: got %+v", got)
	}
	if !got.StartedAt.Equal(meta.StartedAt) {
		t.Fatalf("StartedAt mismatch: got %v want %v", got.StartedAt, meta.StartedAt)
	}
}

func TestPersistentMeta_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	meta := PersistentMeta{Version: "1.0", ID: "x", AgentPID: 1, StartedAt: time.Now()}
	if err := WritePersistentMeta(dir, meta); err != nil {
		t.Fatalf("Write: %v", err)
	}
	st, err := os.Stat(filepath.Join(dir, "meta.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("meta.json perms = %v; want 0600", st.Mode().Perm())
	}
}

func TestPidFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := WritePidFile(dir, 12345); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := ReadPidFile(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != 12345 {
		t.Fatalf("got pid %d; want 12345", got)
	}
}

func TestReadPersistentMeta_MissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadPersistentMeta(dir)
	if err == nil {
		t.Fatalf("expected error for missing meta.json")
	}
}
```

(Add `"os"` import.)

- [ ] **Step 2: Run to fail**

```bash
go test -run TestPersistentMeta -v ./internal/agent/
go test -run TestPidFile -v ./internal/agent/
```

- [ ] **Step 3: Implement**

`internal/agent/persistent_meta.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

// PersistentMeta is the on-disk schema for persistent-session metadata.
// Stored at <sessionDir>/meta.json with mode 0600.
//
// The Version field is for future schema migrations. v1 is "1.0".
type PersistentMeta struct {
	Version   string       `json:"version"`
	ID        string       `json:"id"`
	Spec      cwtypes.Spec `json:"spec"`
	AgentPID  int          `json:"agentPID"`
	StartedAt time.Time    `json:"startedAt"`
}

// WritePersistentMeta serializes meta to <dir>/meta.json with mode 0600.
// Caller is responsible for creating <dir> with appropriate permissions
// before calling.
func WritePersistentMeta(dir string, meta PersistentMeta) error {
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	path := filepath.Join(dir, "meta.json")
	return os.WriteFile(path, b, 0o600)
}

// ReadPersistentMeta loads <dir>/meta.json. Returns wrapped err if missing
// or unparseable; callers should treat any error as "session not loadable".
func ReadPersistentMeta(dir string) (PersistentMeta, error) {
	var meta PersistentMeta
	path := filepath.Join(dir, "meta.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return meta, fmt.Errorf("read meta: %w", err)
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		return meta, fmt.Errorf("unmarshal meta: %w", err)
	}
	return meta, nil
}

// WritePidFile writes <dir>/pid as a single decimal line, mode 0600.
func WritePidFile(dir string, pid int) error {
	path := filepath.Join(dir, "pid")
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o600)
}

// ReadPidFile parses the integer in <dir>/pid.
func ReadPidFile(dir string) (int, error) {
	path := filepath.Join(dir, "pid")
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read pid: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("parse pid %q: %w", string(b), err)
	}
	return pid, nil
}

// IsPidAlive returns true if the process exists and is reachable via signal 0.
// The caller must additionally compare meta.StartedAt to the agent's reported
// startup time to defend against PID rollover.
func IsPidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// signal 0: existence check on POSIX. On macOS/Linux, this returns
	// nil if the process exists and we have permission to signal it.
	return proc.Signal(syscall.Signal(0)) == nil
}
```

Add `"syscall"` import.

- [ ] **Step 4: Run tests, expect green**

```bash
go test -run TestPersistentMeta -v ./internal/agent/ -race
go test -run TestPidFile -v ./internal/agent/ -race
go test ./internal/agent/ -race
```

- [ ] **Step 5: Commit**

```bash
git add internal/agent/persistent_meta.go internal/agent/persistent_meta_test.go
git commit -s -m "feat(agent): persistent session meta.json + pid file IO (CW-G4)

PersistentMeta schema (version, id, spec, agentPID, startedAt).
WritePersistentMeta + ReadPersistentMeta with mode 0600.
WritePidFile + ReadPidFile (single-line decimal text).
IsPidAlive helper using signal 0 existence check.

PID rollover defense (StartedAt comparison) is performed by callers
in Manager.Reattach (Task 14)."
```

---

## Task 5: Ring buffer impl + tests

**Files:**
- Create: `internal/agent/ringbuffer.go`
- Create: `internal/agent/ringbuffer_test.go`

- [ ] **Step 1: Failing tests**

`internal/agent/ringbuffer_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestRingBuffer_EmptySnapshot(t *testing.T) {
	rb := newRingBuffer(16)
	got := rb.Snapshot()
	if len(got) != 0 {
		t.Fatalf("empty buffer Snapshot len=%d; want 0", len(got))
	}
}

func TestRingBuffer_PartialFill(t *testing.T) {
	rb := newRingBuffer(16)
	rb.Write([]byte("hello"))
	got := rb.Snapshot()
	if string(got) != "hello" {
		t.Fatalf("Snapshot=%q; want %q", string(got), "hello")
	}
}

func TestRingBuffer_ExactFill(t *testing.T) {
	rb := newRingBuffer(5)
	rb.Write([]byte("hello"))
	got := rb.Snapshot()
	if string(got) != "hello" {
		t.Fatalf("Snapshot=%q; want %q", string(got), "hello")
	}
}

func TestRingBuffer_OverflowDropsOldest(t *testing.T) {
	rb := newRingBuffer(5)
	rb.Write([]byte("hello"))     // buffer = "hello"
	rb.Write([]byte("world"))     // buffer = "world" (overwrote "hello")
	got := rb.Snapshot()
	if string(got) != "world" {
		t.Fatalf("Snapshot=%q; want %q", string(got), "world")
	}
}

func TestRingBuffer_Wraparound(t *testing.T) {
	rb := newRingBuffer(5)
	rb.Write([]byte("hello"))     // wrote 5; head=0, full=true
	rb.Write([]byte("xy"))        // wrote 2; head=2; data = "xyllo" but read order = "lloxy"
	got := rb.Snapshot()
	if string(got) != "lloxy" {
		t.Fatalf("Snapshot=%q; want %q", string(got), "lloxy")
	}
}

func TestRingBuffer_WriteLargerThanBufferKeepsTail(t *testing.T) {
	rb := newRingBuffer(5)
	rb.Write([]byte("0123456789"))  // 10 bytes; only last 5 retained
	got := rb.Snapshot()
	if string(got) != "56789" {
		t.Fatalf("Snapshot=%q; want %q", string(got), "56789")
	}
}

func TestRingBuffer_ConcurrentWriteAndSnapshot(t *testing.T) {
	rb := newRingBuffer(64)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); rb.Write([]byte(strings.Repeat("a", 8))) }()
		go func() { defer wg.Done(); _ = rb.Snapshot() }()
	}
	wg.Wait()
	// Final snapshot must be valid bytes (no panic, no torn read).
	final := rb.Snapshot()
	for _, b := range final {
		if b != 'a' && b != 0 {
			t.Fatalf("unexpected byte %v in final snapshot", b)
		}
	}
	// not empty
	if !bytes.Contains(final, []byte("a")) {
		t.Fatalf("expected at least one 'a' in final snapshot")
	}
}
```

- [ ] **Step 2: Run to fail**

```bash
go test -run TestRingBuffer -v ./internal/agent/
```

- [ ] **Step 3: Implement**

`internal/agent/ringbuffer.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package agent

import "sync"

// ringBuffer is a fixed-capacity byte ring buffer. Used by persistent
// agents to retain recent PTY output for redraw on reattach.
//
// Writes are O(len(p)) under a mutex. Snapshot is O(size). The hot path
// (PTY OnData callback) acquires the mutex once per chunk; under bench's
// output_throughput workload (~65 MB/s through cliwrap), this is well
// below saturation.
type ringBuffer struct {
	mu   sync.Mutex
	data []byte // capacity == size
	head int    // next write position
	full bool   // wraparound has occurred
}

// newRingBuffer allocates a ring buffer of the given size in bytes.
// Panics if size <= 0; callers (Spec.Validate) must ensure size > 0.
func newRingBuffer(size int) *ringBuffer {
	if size <= 0 {
		panic("ringbuffer: size must be > 0")
	}
	return &ringBuffer{data: make([]byte, size)}
}

// Write appends p to the buffer. If len(p) > cap, only the tail of p is
// retained (oldest bytes silently dropped).
func (rb *ringBuffer) Write(p []byte) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	cap := len(rb.data)
	if len(p) >= cap {
		// p alone exceeds buffer; copy only the last `cap` bytes.
		copy(rb.data, p[len(p)-cap:])
		rb.head = 0
		rb.full = true
		return
	}

	first := copy(rb.data[rb.head:], p)
	if first < len(p) {
		// wraparound
		copy(rb.data, p[first:])
		rb.full = true
	}
	rb.head = (rb.head + len(p)) % cap
	if rb.head == 0 && len(p) > 0 {
		rb.full = true
	}
}

// Snapshot returns a copy of the buffer contents in chronological order
// (oldest byte first). Length is min(bytes-written, cap).
func (rb *ringBuffer) Snapshot() []byte {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if !rb.full {
		out := make([]byte, rb.head)
		copy(out, rb.data[:rb.head])
		return out
	}
	out := make([]byte, len(rb.data))
	// chronological order: head .. end of buffer, then start of buffer .. head
	n := copy(out, rb.data[rb.head:])
	copy(out[n:], rb.data[:rb.head])
	return out
}
```

- [ ] **Step 4: Run tests, expect green**

```bash
go test -run TestRingBuffer -v ./internal/agent/ -race
```

- [ ] **Step 5: Commit**

```bash
git add internal/agent/ringbuffer.go internal/agent/ringbuffer_test.go
git commit -s -m "feat(agent): byte ring buffer for persistent PTY redraw (CW-G4)

newRingBuffer(size) allocates a fixed-capacity buffer.
Write(p) appends; if len(p) > size, only the tail is retained.
Snapshot() returns chronological copy (oldest first).

Concurrent Write/Snapshot safe under sync.Mutex; verified by
TestRingBuffer_ConcurrentWriteAndSnapshot under -race."
```

---

## Task 6: New IPC message types

Two new MsgType constants for the reattach handshake. Specific payloads will be added when wired (Task 12); this task adds the constants + simple round-trip serialization.

**Files:**
- Modify: `internal/ipc/types.go` (or `internal/ipc/messages.go` — wherever MsgType constants live)
- Modify: a relevant `_test.go` for round-trip

- [ ] **Step 1: Locate where existing MsgType constants are defined**

```bash
grep -rn "MsgHello\|MsgStartChild\|MsgType" internal/ipc/ | head -10
```

Open the file; find the `const ( ... MsgType ...)` block.

- [ ] **Step 2: Failing test**

In whichever `_test.go` covers MsgType (likely `internal/ipc/messages_test.go` or similar):

```go
func TestMsgType_PersistentRouting(t *testing.T) {
	// New types must be distinct from existing.
	seen := map[MsgType]bool{
		MsgTypeReattachQuery: false,
		MsgTypePTYRingDump:   false,
	}
	for k := range seen {
		if k == MsgTypeHello || k == MsgTypeStartChild || k == MsgTypeStopChild {
			t.Fatalf("MsgType %v collides with existing constant", k)
		}
	}
}
```

- [ ] **Step 3: Run to fail**

```bash
go test -run TestMsgType_PersistentRouting -v ./internal/ipc/
```

Expected: undefined constants.

- [ ] **Step 4: Add constants**

In the existing `const ( MsgType... )` block in `internal/ipc/types.go`, append:

```go
	// CW-G4: reattach handshake.
	MsgTypeReattachQuery MsgType = /* next available value, e.g. 30 */
	// CW-G4: agent → host one-shot ring buffer dump on attach.
	MsgTypePTYRingDump MsgType = /* next, e.g. 31 */
```

(Use whatever the next free integer values are. Keep them stable — they go on the wire.)

- [ ] **Step 5: Run tests, expect green**

```bash
go test -run TestMsgType_PersistentRouting -v ./internal/ipc/ -race
go test ./internal/ipc/ -race    # no regression in other tests
```

- [ ] **Step 6: Commit**

```bash
git add internal/ipc/types.go internal/ipc/messages_test.go
git commit -s -m "feat(ipc): MsgTypeReattachQuery + MsgTypePTYRingDump (CW-G4)

Two new IPC frame types for the reattach handshake. Payload schemas
are added when wired into the agent's accept loop (Task 11) and the
controller's initial-buffer handling (Task 12)."
```

---

## Task 7: Capability advertisement + Spawn refusal

When the agent advertises its capabilities, it adds `"persistence"`. The host (Manager.Register) refuses `Persistent=true` requests against agents that don't advertise.

**Files:**
- Modify: `internal/agent/capability.go`
- Modify: `internal/agent/capability_test.go`
- Modify: `internal/controller/controller.go` (add `AgentSupportsPersistence()` method)
- Modify: `pkg/cliwrap/manager.go` (call AgentSupportsPersistence at Register if Persistent=true)
- Modify: `pkg/cliwrap/manager_test.go`

- [ ] **Step 1: Failing test (agent side)**

`internal/agent/capability_test.go`:

```go
func TestBuildCapabilityReply_AdvertisesPersistence(t *testing.T) {
	reply := BuildCapabilityReply()
	found := false
	for _, f := range reply.Features {
		if f == "persistence" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("CapabilityReply.Features should include \"persistence\"; got %v", reply.Features)
	}
}
```

- [ ] **Step 2: Run to fail**

```bash
go test -run TestBuildCapabilityReply_AdvertisesPersistence -v ./internal/agent/
```

- [ ] **Step 3: Add advertisement**

In `internal/agent/capability.go`, find `BuildCapabilityReply()` and append `"persistence"` to its Features slice. Example:

```go
func BuildCapabilityReply() ipc.CapabilityReply {
	return ipc.CapabilityReply{
		Features: []string{"pty", "persistence"}, // existing features + new
	}
}
```

- [ ] **Step 4: Run agent tests, expect green**

```bash
go test ./internal/agent/ -race
```

- [ ] **Step 5: Failing test (controller / manager side)**

In `pkg/cliwrap/manager_test.go`:

```go
func TestManager_Register_PersistentRefusedWhenAgentNoCap(t *testing.T) {
	// Use a stub controller that reports AgentSupportsPersistence() == false.
	// Register(spec) with Persistent=true should fail with ErrPersistenceUnsupportedByAgent.
	// Implementation hint: if existing test infrastructure uses real
	// agent binaries, this can be done by feature-flagging the agent build
	// with a -tags=nopersistence or similar; alternative is to inject a
	// fake controller via existing test helpers.
	t.Skip("requires controller fake or feature-flagged agent build; cover in Task 18 integration tests instead")
}
```

If a fake controller is straightforward, write the real test. Otherwise leave the skip + cover via integration test in Task 18.

- [ ] **Step 6: Add `AgentSupportsPersistence()` controller method**

In `internal/controller/controller.go`, add (alongside existing `AgentSupportsPTY`):

```go
// AgentSupportsPersistence reports whether the connected agent advertised
// the "persistence" feature during capability negotiation.
func (c *Controller) AgentSupportsPersistence() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range c.features {
		if f == "persistence" {
			return true
		}
	}
	return false
}
```

- [ ] **Step 7: Hook the refusal into Manager.Register**

In `pkg/cliwrap/manager.go`, in `Register`, after the existing PTY-unsupported check:

```go
if spec.Persistent && /* persistence-capability check requires Start to have negotiated.
                       The check is more accurately done in Start, where the agent has
                       responded. Move check to Start path. */ {
	// inserted in Start path below
}
```

Actually the capability is known only after Start (handshake completes). So perform the check in `Start`, not `Register`. Locate `processHandle.Start` (`pkg/cliwrap/process.go`), and after `ctrl.Start(ctx)` returns, before returning success:

```go
if p.spec.Persistent && !p.ctrl.AgentSupportsPersistence() {
	_ = p.ctrl.Close(ctx)
	return errors.Join(ErrPersistenceUnsupportedByAgent, errors.New("agent did not advertise persistence capability"))
}
```

- [ ] **Step 8: Run all tests**

```bash
go test ./internal/agent/ -race
go test ./internal/controller/ -race
go test ./pkg/cliwrap/ -race
```

- [ ] **Step 9: Commit**

```bash
git add internal/agent/capability.go internal/agent/capability_test.go \
        internal/controller/controller.go pkg/cliwrap/manager.go pkg/cliwrap/process.go pkg/cliwrap/manager_test.go
git commit -s -m "feat(capability): advertise + check \"persistence\" capability (CW-G4)

Agent's BuildCapabilityReply now includes \"persistence\".
Controller.AgentSupportsPersistence() reports the negotiation result.
processHandle.Start refuses Persistent=true against an agent that
did not advertise, returning ErrPersistenceUnsupportedByAgent.

Backward compat: pre-CW-G4 agents simply lack \"persistence\" in
their feature list; new hosts get a clear error rather than a silently
non-persistent session."
```

---

## Task 8: Spawner persistent branch

**Files:**
- Modify: `internal/supervise/spawner.go`
- Modify: `internal/supervise/spawner_test.go`

- [ ] **Step 1: Failing test**

`internal/supervise/spawner_test.go` (extend):

```go
func TestSpawner_PersistentBranchSetsSetsidAndRedirects(t *testing.T) {
	sessionDir := t.TempDir()
	// Build a stub spec carrier — Spawner takes spec via SpawnerOptions or similar.
	// Inspect the existing Spawn signature; if it doesn't take spec, modify it
	// to thread the persistent flag + sessionDir.
	sp := NewSpawner(SpawnerOptions{
		AgentPath:     "/bin/echo",  // dummy; we won't actually run
		Persistent:    true,
		SessionDir:    sessionDir,
	})

	// Use Spawn's internal cmd-builder if exposed, OR just verify by spawning
	// /bin/echo and inspecting that no error occurs and stdio went to log.
	// Simplest: expose a buildCmd helper in spawner.go that returns *exec.Cmd
	// without running, then assert on its SysProcAttr / Env / Args.
	cmd := sp.buildCmdForTest("test-id")  // Test-only helper; see implementation step.

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatalf("Persistent branch should set Setsid=true; got SysProcAttr=%+v", cmd.SysProcAttr)
	}
	envHas := func(s string) bool {
		for _, e := range cmd.Env {
			if e == s { return true }
		}
		return false
	}
	if !envHas("CLIWRAP_PERSISTENT=1") {
		t.Fatalf("env missing CLIWRAP_PERSISTENT=1; got %v", cmd.Env)
	}
	wantSessionDirEnv := "CLIWRAP_SESSION_DIR=" + sessionDir
	if !envHas(wantSessionDirEnv) {
		t.Fatalf("env missing %q; got %v", wantSessionDirEnv, cmd.Env)
	}
	persistentArg := false
	for _, a := range cmd.Args {
		if a == "--persistent" { persistentArg = true; break }
	}
	if !persistentArg {
		t.Fatalf("Args missing --persistent; got %v", cmd.Args)
	}
}

func TestSpawner_NonPersistentBranchUnchanged(t *testing.T) {
	sp := NewSpawner(SpawnerOptions{AgentPath: "/bin/echo", Persistent: false})
	cmd := sp.buildCmdForTest("test-id")
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setsid {
		t.Fatalf("non-persistent should NOT set Setsid; got SysProcAttr=%+v", cmd.SysProcAttr)
	}
	for _, a := range cmd.Args {
		if a == "--persistent" {
			t.Fatalf("non-persistent should not pass --persistent argv")
		}
	}
}
```

- [ ] **Step 2: Run to fail**

```bash
go test -run TestSpawner_PersistentBranch -v ./internal/supervise/
```

Expected: undefined options, undefined helper.

- [ ] **Step 3: Implement**

In `internal/supervise/spawner.go`, extend `SpawnerOptions`:

```go
type SpawnerOptions struct {
	AgentPath  string
	Persistent bool
	SessionDir string // required when Persistent=true
}
```

Refactor the `cmd` build path in `Spawn` into a `buildCmd(agentID string) (*exec.Cmd, *os.File, error)` helper. The `*os.File` is the log file (or nil); caller must close after `cmd.Start()`. Also add a test-only entrypoint:

```go
// buildCmdForTest exposes buildCmd for tests without running the cmd.
// Production code does not call this; it returns the cmd with stdio
// configured but log file (if any) is closed. Use only in *_test.go.
func (s *Spawner) buildCmdForTest(agentID string) *exec.Cmd {
	cmd, logFile, _ := s.buildCmd(agentID)
	if logFile != nil { _ = logFile.Close() }
	return cmd
}
```

`buildCmd`:

```go
func (s *Spawner) buildCmd(agentID string) (*exec.Cmd, *os.File, error) {
	cmd := exec.Command(s.opts.AgentPath)
	if s.opts.Persistent {
		if s.opts.SessionDir == "" {
			return nil, nil, fmt.Errorf("supervise: SessionDir required when Persistent=true")
		}
		if err := os.MkdirAll(s.opts.SessionDir, 0o700); err != nil {
			return nil, nil, fmt.Errorf("supervise: mkdir SessionDir: %w", err)
		}
		logFile, err := os.OpenFile(
			filepath.Join(s.opts.SessionDir, "agent.log"),
			os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, nil, fmt.Errorf("supervise: open agent.log: %w", err)
		}
		cmd.Stdin = nil
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		cmd.Dir = "/"
		cmd.Env = append(os.Environ(),
			"CLIWRAP_PERSISTENT=1",
			"CLIWRAP_SESSION_DIR="+s.opts.SessionDir,
			"CLIWRAP_AGENT_ID="+agentID,
		)
		cmd.Args = append(cmd.Args, "--persistent")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		return cmd, logFile, nil
	}
	// Non-persistent path unchanged from existing code.
	// Existing code passes CLIWRAP_AGENT_ID etc. via env. Preserve.
	cmd.Env = append(os.Environ(), "CLIWRAP_AGENT_ID="+agentID)
	return cmd, nil, nil
}
```

In the existing `Spawn` method, replace the inline cmd build with `cmd, logFile, err := s.buildCmd(agentID)`. After `cmd.Start()`, if `logFile != nil`, `defer func() { _ = logFile.Close() }()` (parent doesn't need it; child inherits the fd).

(Add `"path/filepath"` to imports if absent.)

- [ ] **Step 4: Run tests, expect green**

```bash
go test -run TestSpawner_PersistentBranch -v ./internal/supervise/ -race
go test -run TestSpawner_NonPersistentBranch -v ./internal/supervise/ -race
go test ./internal/supervise/ -race    # full regression
```

- [ ] **Step 5: Commit**

```bash
git add internal/supervise/spawner.go internal/supervise/spawner_test.go
git commit -s -m "feat(supervise): persistent Spawn branch (CW-G4)

When Persistent=true, configures SysProcAttr.Setsid=true, redirects
stdio to <SessionDir>/agent.log (mode 0600), passes --persistent argv
plus CLIWRAP_PERSISTENT=1 and CLIWRAP_SESSION_DIR env vars, and sets
cmd.Dir=\"/\" so the daemonized agent does not retain the host's cwd.

Non-persistent path is unchanged.

Setsid: true causes the agent to be in a new POSIX session with no
controlling terminal — terminal closure does not deliver SIGHUP, and
host process death reparents the agent to PID 1 (launchd / init)
rather than killing it."
```

---

## Task 9: Agent `--persistent` flag handler + listener bootstrap

When the agent binary is invoked with `--persistent`, it runs the persistent-mode bootstrap: SIGHUP ignore, write meta + pid, allocate ring buffer, open UNIX listener.

**Files:**
- Modify: `internal/agent/main.go`
- Create: `internal/agent/persistent.go`
- Create: `internal/agent/persistent_test.go`
- Modify: `cmd/cliwrap-agent/main.go` (parse `--persistent` flag)

- [ ] **Step 1: Update cmd/cliwrap-agent to parse --persistent**

In `cmd/cliwrap-agent/main.go`:

```go
func run() error {
	persistent := false
	for _, a := range os.Args[1:] {
		if a == "--persistent" { persistent = true; break }
	}
	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	cfg := agent.DefaultConfig()
	cfg.Persistent = persistent
	return agent.Run(ctx, cfg)
}
```

Note: persistent agents do NOT subscribe to SIGHUP — `Setsid: true` already detaches from controlling terminal, but we explicitly drop SIGHUP from the signal context to avoid surprises if the parent process group somehow delivers it.

- [ ] **Step 2: Extend agent.Config**

In `internal/agent/main.go`:

```go
type Config struct {
	IPCFD          int
	AgentID        string
	RuntimeDir     string
	OutboxCapacity int
	WALBytes       int64
	MaxRecvPayload uint32

	// CW-G4: when true, agent runs in persistent mode (UNIX listener,
	// ring buffer, survives host disconnect).
	Persistent bool
}
```

- [ ] **Step 3: Failing test (handler initialization)**

`internal/agent/persistent_test.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

func TestPersistentBootstrap_WritesMetaPidAndOpensSocket(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIWRAP_SESSION_DIR", dir)

	spec := cwtypes.Spec{ID: "test-1", Command: "/bin/cat", Persistent: true, RingBufferSize: 1024}

	pst, err := initPersistent(persistentInitOpts{
		SessionDir: dir,
		AgentID:    "test-1",
		Spec:       spec,
	})
	if err != nil {
		t.Fatalf("initPersistent: %v", err)
	}
	defer pst.Close()

	// meta.json present
	if _, err := os.Stat(filepath.Join(dir, "meta.json")); err != nil {
		t.Fatalf("meta.json missing: %v", err)
	}
	// pid present
	if _, err := os.Stat(filepath.Join(dir, "pid")); err != nil {
		t.Fatalf("pid missing: %v", err)
	}
	// sock listening
	conn, err := net.Dial("unix", filepath.Join(dir, "sock"))
	if err != nil {
		t.Fatalf("dial sock: %v", err)
	}
	_ = conn.Close()
}

func TestPersistentBootstrap_DefaultsRingBufferSizeWhenZero(t *testing.T) {
	dir := t.TempDir()
	spec := cwtypes.Spec{ID: "test-1", Command: "/bin/cat", Persistent: true, RingBufferSize: 0}
	pst, err := initPersistent(persistentInitOpts{
		SessionDir: dir, AgentID: "test-1", Spec: spec,
	})
	if err != nil { t.Fatalf("initPersistent: %v", err) }
	defer pst.Close()

	if cap := pst.ringBufferCapForTest(); cap != 256*1024 {
		t.Fatalf("default RingBufferSize = %d; want %d", cap, 256*1024)
	}
}
```

- [ ] **Step 4: Run to fail**

```bash
go test -run TestPersistentBootstrap -v ./internal/agent/
```

- [ ] **Step 5: Implement persistent.go**

`internal/agent/persistent.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

const defaultRingBufferSize = 256 * 1024

// persistentState holds the per-session resources owned by a persistent
// agent: meta files, UNIX listener, ring buffer, and the attach lock.
type persistentState struct {
	sessionDir string
	listener   *net.UnixListener
	ring       *ringBuffer

	mu       sync.Mutex
	attached bool
}

type persistentInitOpts struct {
	SessionDir string
	AgentID    string
	Spec       cwtypes.Spec
}

// initPersistent runs the bootstrap sequence for an agent invoked with
// --persistent: ignores SIGHUP, writes meta.json + pid, opens UNIX
// listener at <sessionDir>/sock, allocates the ring buffer.
func initPersistent(opts persistentInitOpts) (*persistentState, error) {
	if opts.SessionDir == "" {
		return nil, errors.New("persistent: SessionDir required")
	}
	if err := os.MkdirAll(opts.SessionDir, 0o700); err != nil {
		return nil, fmt.Errorf("persistent: mkdir SessionDir: %w", err)
	}

	// Defense-in-depth: Setsid: true (set by spawner) already detaches us
	// from a controlling terminal, but explicitly ignore SIGHUP in case
	// the process group somehow delivers it.
	signal.Ignore(syscall.SIGHUP)

	// Allocate ring buffer first so failure leaves no partial filesystem state.
	size := opts.Spec.RingBufferSize
	if size == 0 {
		size = defaultRingBufferSize
	}
	if size <= 0 {
		return nil, fmt.Errorf("persistent: invalid RingBufferSize %d", size)
	}
	ring := newRingBuffer(size)

	// Write meta.json before opening listener, so partial dirs are
	// detectable as "not yet ready".
	meta := PersistentMeta{
		Version:   "1.0",
		ID:        opts.AgentID,
		Spec:      opts.Spec,
		AgentPID:  os.Getpid(),
		StartedAt: time.Now().UTC(),
	}
	if err := WritePersistentMeta(opts.SessionDir, meta); err != nil {
		return nil, fmt.Errorf("persistent: write meta: %w", err)
	}
	if err := WritePidFile(opts.SessionDir, os.Getpid()); err != nil {
		return nil, fmt.Errorf("persistent: write pid: %w", err)
	}

	sockPath := filepath.Join(opts.SessionDir, "sock")
	// Remove a stale sock file (from a prior crashed agent) before
	// attempting to listen. Spawn-time ID conflict policy already rejected
	// existing sessions; if we got this far, the sock file (if any) is stale.
	_ = os.Remove(sockPath)
	addr := &net.UnixAddr{Name: sockPath, Net: "unix"}
	listener, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("persistent: listen %s: %w", sockPath, err)
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("persistent: chmod sock: %w", err)
	}

	return &persistentState{
		sessionDir: opts.SessionDir,
		listener:   listener,
		ring:       ring,
	}, nil
}

// Close closes the listener and removes sock + pid files. meta.json + agent.log
// are intentionally preserved for post-mortem.
func (p *persistentState) Close() {
	if p == nil { return }
	if p.listener != nil {
		_ = p.listener.Close()
	}
	_ = os.Remove(filepath.Join(p.sessionDir, "sock"))
	_ = os.Remove(filepath.Join(p.sessionDir, "pid"))
	_ = os.Remove(filepath.Join(p.sessionDir, ".attached"))
}

// ringBufferCapForTest exposes the ring buffer capacity for unit tests.
func (p *persistentState) ringBufferCapForTest() int {
	if p == nil || p.ring == nil { return 0 }
	return len(p.ring.data)
}
```

- [ ] **Step 6: Wire into agent.Run**

In `internal/agent/main.go::Run`, after `os.MkdirAll(cfg.RuntimeDir, ...)` but before `f := os.NewFile(...)`:

```go
var pst *persistentState
if cfg.Persistent {
	sessionDir := os.Getenv("CLIWRAP_SESSION_DIR")
	if sessionDir == "" {
		return fmt.Errorf("agent: --persistent set but CLIWRAP_SESSION_DIR empty")
	}
	// We do not have full Spec here; receive it via initial IPC StartChild.
	// For now, call initPersistent with a placeholder spec (just the agent ID
	// and a default ring buffer size); update meta.json later if needed.
	var err error
	pst, err = initPersistent(persistentInitOpts{
		SessionDir: sessionDir,
		AgentID:    cfg.AgentID,
		Spec:       cwtypes.Spec{ID: cfg.AgentID, Persistent: true},
	})
	if err != nil { return fmt.Errorf("agent: init persistent: %w", err) }
	defer pst.Close()
}
```

(The accept loop is wired in Task 10.)

- [ ] **Step 7: Run tests, expect green**

```bash
go test -run TestPersistentBootstrap -v ./internal/agent/ -race
go test ./internal/agent/ -race
```

- [ ] **Step 8: Commit**

```bash
git add internal/agent/main.go internal/agent/persistent.go internal/agent/persistent_test.go cmd/cliwrap-agent/main.go
git commit -s -m "feat(agent): --persistent flag + bootstrap (CW-G4)

cmd/cliwrap-agent parses --persistent argv flag and threads it via
agent.Config.Persistent. agent.Run, when Persistent=true, calls
initPersistent which ignores SIGHUP, allocates the ring buffer,
writes meta.json + pid, and opens the UNIX listener at <dir>/sock.

The accept loop (Task 10) and ring-buffer hot-path integration
(Task 11) are wired in subsequent commits."
```

---

## Task 10: Accept loop + attach lock + connection adoption

The persistent agent's UNIX listener accepts new host connections. Single-host attach at a time; second concurrent attach is rejected with `MsgErrAlreadyAttached` and connection close.

**Files:**
- Modify: `internal/agent/persistent.go` (add accept loop)
- Modify: `internal/agent/main.go` (start accept loop; transition primary IPC on new conn)
- Modify: `internal/agent/persistent_test.go` (add accept-loop test)

- [ ] **Step 1: Failing tests**

In `persistent_test.go`:

```go
func TestPersistent_AcceptsConnection(t *testing.T) {
	dir := t.TempDir()
	pst, err := initPersistent(persistentInitOpts{
		SessionDir: dir, AgentID: "x",
		Spec: cwtypes.Spec{ID: "x", Persistent: true, RingBufferSize: 1024},
	})
	if err != nil { t.Fatalf("init: %v", err) }
	defer pst.Close()

	acceptedCh := make(chan net.Conn, 1)
	go pst.runAcceptLoop(func(c net.Conn) {
		acceptedCh <- c
	})

	conn, err := net.Dial("unix", filepath.Join(dir, "sock"))
	if err != nil { t.Fatalf("dial: %v", err) }
	defer func() { _ = conn.Close() }()

	select {
	case got := <-acceptedCh:
		if got == nil { t.Fatalf("nil conn") }
	case <-time.After(time.Second):
		t.Fatalf("accept timeout")
	}
}

func TestPersistent_RejectsSecondConcurrentAttach(t *testing.T) {
	dir := t.TempDir()
	pst, err := initPersistent(persistentInitOpts{
		SessionDir: dir, AgentID: "x",
		Spec: cwtypes.Spec{ID: "x", Persistent: true, RingBufferSize: 1024},
	})
	if err != nil { t.Fatalf("init: %v", err) }
	defer pst.Close()

	// First attach: hold the conn open.
	go pst.runAcceptLoop(func(c net.Conn) {
		// Simulate: accept the conn and just hold it (don't close).
		// In production, this hand-off goes to the IPC adoption code.
		<-time.After(2 * time.Second)
		_ = c.Close()
	})

	conn1, err := net.Dial("unix", filepath.Join(dir, "sock"))
	if err != nil { t.Fatalf("dial1: %v", err) }
	defer func() { _ = conn1.Close() }()
	// Allow agent to mark attached
	time.Sleep(50 * time.Millisecond)

	// Second attach: should be rejected.
	conn2, err := net.Dial("unix", filepath.Join(dir, "sock"))
	if err != nil {
		// Some kernels refuse the second connection at dial time
		// when the listener is busy; that is also a valid reject.
		return
	}
	defer func() { _ = conn2.Close() }()
	// If dial succeeded, the agent should send an error frame
	// (MsgErrAlreadyAttached) and close the conn.
	buf := make([]byte, 32)
	_ = conn2.SetReadDeadline(time.Now().Add(time.Second))
	n, _ := conn2.Read(buf)
	// We expect either EOF (conn closed by agent) or a non-zero
	// error frame. Specific framing decoded in Task 12.
	if n == 0 {
		// EOF — agent closed conn after rejection
		return
	}
	// non-zero read: message present; assume rejection frame for now
}
```

- [ ] **Step 2: Run to fail**

```bash
go test -run TestPersistent_Accepts -v ./internal/agent/
go test -run TestPersistent_RejectsSecond -v ./internal/agent/
```

- [ ] **Step 3: Implement accept loop**

In `persistent.go`, append:

```go
// runAcceptLoop blocks calling Accept on the listener. For each accepted
// connection, it acquires the attach lock; on success, it invokes onAttach
// with the conn (the connection is then owned by the caller).
//
// Second concurrent attach: while another host is attached, runAcceptLoop
// rejects the new conn by writing a small framed error and closing the
// connection. The framing wire format reuses the existing IPC envelope;
// for simplicity here, an "ALREADY_ATTACHED" 1-byte payload is sent as
// MsgTypeErr; Task 12 finalizes the wire format.
func (p *persistentState) runAcceptLoop(onAttach func(net.Conn)) {
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			// Listener closed: graceful exit.
			return
		}
		p.mu.Lock()
		alreadyAttached := p.attached
		if !alreadyAttached {
			p.attached = true
			_ = os.WriteFile(
				filepath.Join(p.sessionDir, ".attached"),
				nil, 0o600)
		}
		p.mu.Unlock()

		if alreadyAttached {
			_, _ = conn.Write([]byte("ALREADY_ATTACHED\n"))
			_ = conn.Close()
			continue
		}
		onAttach(conn)
	}
}

// markDetached releases the attach lock. Caller invokes after the adopted
// IPC conn closes or an error during handshake.
func (p *persistentState) markDetached() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attached = false
	_ = os.Remove(filepath.Join(p.sessionDir, ".attached"))
}
```

- [ ] **Step 4: Wire into agent.Run**

In `agent.Run` (after `pst, err := initPersistent(...)` from Task 9), spawn the accept loop:

```go
if pst != nil {
	go pst.runAcceptLoop(func(conn net.Conn) {
		// In Task 12, this hands off conn to the dispatcher as a new
		// primary IPC channel. For now, we just close it after a small
		// delay to keep the test happy.
		// TODO(CW-G4 Task 12): adopt conn as primary IPC.
		defer pst.markDetached()
		defer func() { _ = conn.Close() }()
		_, _ = conn.Write([]byte("HELLO_PLACEHOLDER\n"))
	})
}
```

(This `TODO(CW-G4 Task 12)` placeholder is acceptable here ONLY because the very next task replaces it. Do not commit a TODO that lasts longer than one task boundary.)

- [ ] **Step 5: Run tests, expect green**

```bash
go test -run TestPersistent_Accepts -v ./internal/agent/ -race
go test -run TestPersistent_RejectsSecond -v ./internal/agent/ -race
```

- [ ] **Step 6: Commit**

```bash
git add internal/agent/persistent.go internal/agent/persistent_test.go internal/agent/main.go
git commit -s -m "feat(agent): UNIX listener accept loop + attach lock (CW-G4)

runAcceptLoop accepts connections on the per-session UNIX socket.
First connection acquires the attach lock and writes <dir>/.attached.
Second concurrent connection is rejected.

Conn adoption (replacing the bootstrap socketpair as the primary IPC
channel and sending MsgHello + MsgPTYRingDump) is wired in Task 12;
this commit ships a placeholder hand-off that closes the conn after
acknowledging the attach. The placeholder is replaced before the next
commit boundary."
```

---

## Task 11: Ring buffer integration into PTY hot path

When persistent, every byte the runner writes to the PTY broadcast also writes to the ring buffer. This is hot-path code; the integration is a single conditional + Write call.

**Files:**
- Modify: `internal/agent/runner_pty.go` or wherever `OnData` is hooked
- Modify: `internal/agent/runner_pty_test.go`

- [ ] **Step 1: Locate the OnData broadcast**

```bash
grep -n "OnData\|ptySubscribers\|s.SendControl(.*PTYData" internal/agent/*.go | head
```

Identify the function (likely in `runner.go::runPTY` from earlier survey: `proc.OnData(func(b []byte) { ... })`).

- [ ] **Step 2: Failing test**

In `runner_pty_test.go` (or wherever PTY tests live), add:

```go
func TestRunner_PersistentRingBufferReceivesPTYBytes(t *testing.T) {
	// Build a runner with a persistent ring buffer attached.
	// Assert that bytes written via OnData also land in the buffer.
	rb := newRingBuffer(64)
	r := &Runner{ /* existing minimal init */ }
	r.SetPersistentRingBuffer(rb)

	// Simulate OnData call: the runner would normally do this when PTY
	// master produces output. For a unit test, invoke the handler we
	// know runner sets up. If the handler is private, expose a thin
	// helper for testing.
	// For brevity, this test relies on a test-only helper:
	r.simulateOnDataForTest([]byte("hello world"))

	got := rb.Snapshot()
	if string(got) != "hello world" {
		t.Fatalf("ring buffer = %q; want %q", string(got), "hello world")
	}
}
```

- [ ] **Step 3: Run to fail**

```bash
go test -run TestRunner_PersistentRingBufferReceivesPTYBytes -v ./internal/agent/
```

- [ ] **Step 4: Wire ring buffer into runner**

In `Runner` struct (in `runner.go`), add a field:

```go
type Runner struct {
	/* existing fields */
	persistentRingBuffer *ringBuffer
}

// SetPersistentRingBuffer wires the ring buffer into the PTY broadcast
// path. Called by agent.Run when in persistent mode (Task 9 wires this).
// Safe to call before runPTY; nil values disable the integration (no-op).
func (r *Runner) SetPersistentRingBuffer(rb *ringBuffer) {
	r.persistentRingBuffer = rb
}
```

In `runPTY` (or wherever `proc.OnData(func(b []byte) {...})` is registered), inside that function body, add the ring buffer write at the same point as the existing `r.StdoutSink.Write(b)` tee:

```go
proc.OnData(func(b []byte) {
	/* existing: SendControl MsgTypePTYData + StdoutSink */

	// CW-G4: tee to ring buffer for redraw-on-reattach. Hot path —
	// ringBuffer.Write is single-mutex and O(len(b)); negligible at
	// observed cliwrap output_throughput (~65 MB/s).
	if r.persistentRingBuffer != nil {
		r.persistentRingBuffer.Write(b)
	}
})
```

Add the test-only helper:

```go
// simulateOnDataForTest invokes the OnData side-effects (ring buffer
// write) without spawning a real PTY. For unit tests only.
func (r *Runner) simulateOnDataForTest(b []byte) {
	if r.persistentRingBuffer != nil {
		r.persistentRingBuffer.Write(b)
	}
}
```

- [ ] **Step 5: Wire SetPersistentRingBuffer at agent startup**

In `agent.Run`, after `pst, err := initPersistent(...)`:

```go
if pst != nil {
	d.runner.SetPersistentRingBuffer(pst.ring)
}
```

(`d.runner` per existing dispatcher structure — `d := NewDispatcher(conn)` with `runner: NewRunner()` field, observed in earlier survey.)

- [ ] **Step 6: Run tests**

```bash
go test -run TestRunner_PersistentRingBufferReceivesPTYBytes -v ./internal/agent/ -race
go test ./internal/agent/ -race
```

- [ ] **Step 7: Commit**

```bash
git add internal/agent/runner.go internal/agent/runner_pty_test.go internal/agent/main.go
git commit -s -m "feat(agent): tee PTY output to persistent ring buffer (CW-G4)

In persistent mode, every chunk the runner broadcasts via OnData is
also written to the ring buffer. Single-mutex Write; negligible at
the bench output_throughput rate (~65 MB/s through cliwrap).

The buffer is dumped to the new host on reattach in Task 12."
```

---

## Task 12: MsgPTYRingDump on attach + controller initial-buffer wiring

Replaces the placeholder hand-off from Task 10. On attach, the agent sends `MsgHello` with its actual `startedAt` (for PID-rollover defense at the host) and `MsgPTYRingDump` with the ring buffer snapshot. The host's controller queues the dump as an initial buffer; the next `SubscribePTYData` subscriber receives it as the first chunk(s).

**Files:**
- Modify: `internal/agent/persistent.go` (replace placeholder write with proper handshake)
- Modify: `internal/ipc/messages.go` (add payload types if needed for MsgPTYRingDump)
- Modify: `internal/controller/controller.go` (handle MsgPTYRingDump → initial buffer)
- Modify: `internal/controller/controller_test.go` (test initial buffer drain semantics)

- [ ] **Step 1: Failing test (controller initial-buffer drain)**

`controller_test.go`:

```go
func TestController_InitialBufferDeliveredToFirstSubscriber(t *testing.T) {
	c := newControllerForTest(t)
	defer c.Close(context.Background())

	// Inject an initial buffer (simulating MsgPTYRingDump receipt).
	c.SetInitialPTYBuffer([]byte("redraw_payload"))

	ch, unsub := c.SubscribePTYData()
	defer unsub()

	select {
	case data := <-ch:
		if !bytes.Equal(data.Bytes, []byte("redraw_payload")) {
			t.Fatalf("first chunk = %q; want %q", data.Bytes, "redraw_payload")
		}
	case <-time.After(time.Second):
		t.Fatalf("initial buffer not delivered")
	}
}

func TestController_InitialBufferOnlyDeliveredOnce(t *testing.T) {
	c := newControllerForTest(t)
	defer c.Close(context.Background())

	c.SetInitialPTYBuffer([]byte("once"))

	ch1, unsub1 := c.SubscribePTYData()
	defer unsub1()
	first := <-ch1
	if !bytes.Equal(first.Bytes, []byte("once")) { t.Fatalf("first sub: %q", first.Bytes) }

	ch2, unsub2 := c.SubscribePTYData()
	defer unsub2()
	select {
	case data := <-ch2:
		if bytes.Equal(data.Bytes, []byte("once")) {
			t.Fatalf("second subscriber should NOT see initial buffer; got %q", data.Bytes)
		}
		// other live data is fine
	case <-time.After(50 * time.Millisecond):
		// also fine — no live data, no initial buffer for second subscriber
	}
}
```

(`newControllerForTest` is the existing or to-be-extracted test helper; see existing `controller_test.go` for the pattern.)

- [ ] **Step 2: Run to fail**

```bash
go test -run TestController_InitialBuffer -v ./internal/controller/
```

- [ ] **Step 3: Implement initial-buffer drain in controller**

In `controller.go`, add a field:

```go
type Controller struct {
	/* existing */
	initialPTYBuffer     []byte
	initialBufferDrained atomic.Bool
}
```

Add setter:

```go
// SetInitialPTYBuffer is called when the agent sends MsgPTYRingDump on
// reattach. The buffer is delivered as the first chunk to the next
// SubscribePTYData subscriber; subsequent subscribers see only live data.
func (c *Controller) SetInitialPTYBuffer(b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.initialPTYBuffer = make([]byte, len(b))
	copy(c.initialPTYBuffer, b)
}
```

Modify `SubscribePTYData` to drain the initial buffer once:

```go
func (c *Controller) SubscribePTYData() (<-chan ipc.PTYData, func()) {
	ch, unsub := c.ptySubscribers.Subscribe()
	// Drain initial buffer to this subscriber if not yet drained.
	if c.initialBufferDrained.CompareAndSwap(false, true) {
		c.mu.Lock()
		buf := c.initialPTYBuffer
		c.initialPTYBuffer = nil
		c.mu.Unlock()
		if len(buf) > 0 {
			// Send asynchronously to avoid blocking the caller of Subscribe.
			go func() {
				select {
				case ch <- ipc.PTYData{Seq: 0, Bytes: buf}:
				case <-time.After(5 * time.Second):
					// Drop if subscriber is unresponsive for 5 s.
				}
			}()
		}
	}
	return ch, unsub
}
```

(Adapt the `ptySubscribers.Subscribe()` call to whatever the existing API is; this is illustrative.)

- [ ] **Step 4: Implement agent-side handshake**

In `internal/agent/persistent.go`, update the accept-loop hand-off (replace the placeholder from Task 10):

```go
go pst.runAcceptLoop(func(conn net.Conn) {
	defer pst.markDetached()

	// Send MsgHello with actual startedAt
	hello := ipc.HelloPayload{
		ProtocolVersion: ipc.ProtocolVersion,
		AgentID:         cfg.AgentID,
		// CW-G4: include startedAt for PID-rollover defense
		StartedAt:       agentStartTime.UnixNano(),
	}
	if err := writeIPCFrame(conn, ipc.MsgTypeHello, hello); err != nil {
		_ = conn.Close()
		return
	}

	// Send MsgPTYRingDump with ring snapshot
	snapshot := pst.ring.Snapshot()
	if err := writeIPCFrame(conn, ipc.MsgTypePTYRingDump,
		ipc.PTYRingDumpPayload{Bytes: snapshot}); err != nil {
		_ = conn.Close()
		return
	}

	// Adopt this conn as the primary IPC channel.
	// In v1, replace the agent's existing dispatcher conn with this one,
	// using the existing dispatcher.AdoptConn(conn) hook (added below).
	d.AdoptConn(conn)
	// Block until conn EOF (host disconnect)
	<-d.WaitForConnDone()
})
```

The `agentStartTime` is captured at agent startup — declare a package-level var initialized in `Run`:

```go
var agentStartTime time.Time
// in Run(): agentStartTime = time.Now().UTC()
```

(Revise as needed — the actual struct location depends on existing patterns.)

Extend `ipc/messages.go` with a `HelloPayload.StartedAt int64` field if not present, plus `PTYRingDumpPayload struct { Bytes []byte }`.

Add to `dispatcher.go`:

```go
// AdoptConn replaces the dispatcher's IPC connection. Used by the
// persistent accept loop on reattach (CW-G4).
func (d *Dispatcher) AdoptConn(conn net.Conn) {
	// Implementation detail: the existing IPC layer (internal/ipc.Conn)
	// owns the underlying read/write. AdoptConn shuts down the old Conn,
	// constructs a new Conn around the new net.Conn, rewires d.conn.
	// See internal/ipc.Conn for the close+open pattern.
	// Keep simple: terminate prior conn, set new one, kick read loops.
	/* concrete implementation depends on ipc.Conn API */
}

// WaitForConnDone returns a channel closed when the current adopted
// conn reaches EOF (host disconnect).
func (d *Dispatcher) WaitForConnDone() <-chan struct{} {
	return d.connDoneCh
}
```

If `internal/ipc.Conn` does not currently support adoption-style replacement, add a minimal API (close current, accept new). This is the trickiest concrete change in the plan; budget time for it.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/controller/ -race
go test ./internal/agent/ -race
go test ./internal/ipc/ -race
```

- [ ] **Step 6: Commit**

```bash
git add internal/controller/controller.go internal/controller/controller_test.go \
        internal/agent/persistent.go internal/agent/dispatcher.go \
        internal/ipc/messages.go internal/ipc/types.go
git commit -s -m "feat(agent,controller): MsgPTYRingDump on attach + initial buffer (CW-G4)

On reattach, the agent's accept loop sends MsgHello (with startedAt
for PID-rollover defense) followed by MsgPTYRingDump (ring buffer
snapshot). The host's controller stores the dump as an initial
buffer, drained to the first SubscribePTYData subscriber.

Subsequent subscribers see only live PTY data — the initial buffer
is delivered exactly once. From the application's perspective, the
SubscribePTYData API is uniform whether the handle came from
Spawn or Reattach.

Replaces the Task 10 placeholder hand-off."
```

---

## Task 13: `Manager.ListPersistent`

**Files:**
- Create: `pkg/cliwrap/persistent.go`
- Create: `pkg/cliwrap/persistent_test.go`

- [ ] **Step 1: Failing tests**

```go
func TestManager_ListPersistent_EmptyDir(t *testing.T) {
	m, err := NewManager(WithPersistentDir(t.TempDir()))
	if err != nil { t.Fatalf("NewManager: %v", err) }
	defer func() { _ = m.Shutdown(context.Background()) }()

	got, err := m.ListPersistent()
	if err != nil { t.Fatalf("ListPersistent: %v", err) }
	if len(got) != 0 { t.Fatalf("expected empty; got %v", got) }
}

func TestManager_ListPersistent_DiscoversWrittenSession(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(WithPersistentDir(dir))
	if err != nil { t.Fatalf("NewManager: %v", err) }
	defer func() { _ = m.Shutdown(context.Background()) }()

	// Write a session metadata directly (simulating what an agent did).
	sessionDir := filepath.Join(dir, "demo-1")
	require.NoError(t, os.MkdirAll(sessionDir, 0o700))
	meta := agent.PersistentMeta{
		Version: "1.0", ID: "demo-1",
		Spec: cwtypes.Spec{ID: "demo-1", Persistent: true},
		AgentPID: os.Getpid(), // self — guaranteed alive
		StartedAt: time.Now(),
	}
	require.NoError(t, agent.WritePersistentMeta(sessionDir, meta))
	require.NoError(t, agent.WritePidFile(sessionDir, os.Getpid()))

	got, err := m.ListPersistent()
	if err != nil { t.Fatalf("ListPersistent: %v", err) }
	if len(got) != 1 || got[0].ID != "demo-1" {
		t.Fatalf("expected one session demo-1; got %v", got)
	}
	if !got[0].Alive {
		t.Fatalf("self pid should be Alive=true")
	}
}

func TestManager_ListPersistent_StaleEntryReportedAsNotAlive(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(WithPersistentDir(dir))
	if err != nil { t.Fatalf("NewManager: %v", err) }
	defer func() { _ = m.Shutdown(context.Background()) }()

	sessionDir := filepath.Join(dir, "stale")
	require.NoError(t, os.MkdirAll(sessionDir, 0o700))
	require.NoError(t, agent.WritePersistentMeta(sessionDir, agent.PersistentMeta{
		Version: "1.0", ID: "stale",
		Spec: cwtypes.Spec{ID: "stale", Persistent: true},
		AgentPID: 1, // PID 1 is launchd/init — kill -0 succeeds, but
		// for the stale test we need a guaranteed-dead PID.
		// Use a fabricated high PID:
		StartedAt: time.Now(),
	}))
	require.NoError(t, agent.WritePidFile(sessionDir, 999_999_999))

	got, err := m.ListPersistent()
	if err != nil { t.Fatalf("ListPersistent: %v", err) }
	if len(got) != 1 { t.Fatalf("expected one entry; got %v", got) }
	if got[0].Alive {
		t.Fatalf("PID 999999999 should be Alive=false")
	}
}
```

- [ ] **Step 2: Run to fail**

```bash
go test -run TestManager_ListPersistent -v ./pkg/cliwrap/
```

- [ ] **Step 3: Implement**

`pkg/cliwrap/persistent.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/0xmhha/cli-wrapper/internal/agent"
	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

// PersistentSessionInfo summarizes a persistent session discovered on disk.
type PersistentSessionInfo struct {
	ID        string
	Spec      cwtypes.Spec
	AgentPID  int
	StartedAt cwtypes.Time // alias to time.Time
	Alive     bool
	Attached  bool
}

// ListPersistent scans WithPersistentDir for sessions. Stale entries
// (Alive=false) are returned but not auto-cleaned. Returned slice is
// sorted by ID.
func (m *Manager) ListPersistent() ([]PersistentSessionInfo, error) {
	entries, err := os.ReadDir(m.persistentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list persistent: %w", err)
	}
	var out []PersistentSessionInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sessionDir := filepath.Join(m.persistentDir, e.Name())
		meta, err := agent.ReadPersistentMeta(sessionDir)
		if err != nil {
			continue // partially-written or unrelated dir; skip
		}
		pid, err := agent.ReadPidFile(sessionDir)
		if err != nil {
			continue
		}
		_, attachedErr := os.Stat(filepath.Join(sessionDir, ".attached"))
		out = append(out, PersistentSessionInfo{
			ID:        meta.ID,
			Spec:      meta.Spec,
			AgentPID:  pid,
			StartedAt: meta.StartedAt,
			Alive:     agent.IsPidAlive(pid),
			Attached:  attachedErr == nil,
		})
	}
	return out, nil
}
```

Adjust `cwtypes.Time` to `time.Time` (with `"time"` import) if there's no such alias.

- [ ] **Step 4: Run tests, expect green**

```bash
go test -run TestManager_ListPersistent -v ./pkg/cliwrap/ -race
```

- [ ] **Step 5: Commit**

```bash
git add pkg/cliwrap/persistent.go pkg/cliwrap/persistent_test.go
git commit -s -m "feat(manager): ListPersistent scans WithPersistentDir (CW-G4)

Returns []PersistentSessionInfo with Alive (kill -0) and Attached
(.attached flag) populated. Stale entries are reported but not
auto-cleaned, consistent with the manual-cleanup policy."
```

---

## Task 14: `Manager.Reattach`

The full reattach flow with the error matrix from spec §"Error Matrix".

**Files:**
- Modify: `pkg/cliwrap/persistent.go` (extend with Reattach)
- Modify: `pkg/cliwrap/persistent_test.go` (extend with reattach tests)

- [ ] **Step 1: Failing tests** (cover the error matrix)

```go
func TestManager_Reattach_SessionNotFound(t *testing.T) {
	m, _ := NewManager(WithPersistentDir(t.TempDir()))
	defer func() { _ = m.Shutdown(context.Background()) }()

	_, err := m.Reattach(context.Background(), "nonexistent")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v; want ErrSessionNotFound", err)
	}
}

func TestManager_Reattach_AgentDeadByMissingPid(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "x")
	require.NoError(t, os.MkdirAll(sessionDir, 0o700))
	// meta but no pid file
	require.NoError(t, agent.WritePersistentMeta(sessionDir, agent.PersistentMeta{
		Version: "1.0", ID: "x", AgentPID: 0, StartedAt: time.Now(),
	}))

	m, _ := NewManager(WithPersistentDir(dir))
	defer func() { _ = m.Shutdown(context.Background()) }()

	_, err := m.Reattach(context.Background(), "x")
	if !errors.Is(err, ErrAgentDead) {
		t.Fatalf("err = %v; want ErrAgentDead", err)
	}
}

func TestManager_Reattach_AgentDeadByDeadPid(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "x")
	require.NoError(t, os.MkdirAll(sessionDir, 0o700))
	require.NoError(t, agent.WritePersistentMeta(sessionDir, agent.PersistentMeta{
		Version: "1.0", ID: "x", AgentPID: 999_999_999, StartedAt: time.Now(),
	}))
	require.NoError(t, agent.WritePidFile(sessionDir, 999_999_999))

	m, _ := NewManager(WithPersistentDir(dir))
	defer func() { _ = m.Shutdown(context.Background()) }()

	_, err := m.Reattach(context.Background(), "x")
	if !errors.Is(err, ErrAgentDead) {
		t.Fatalf("err = %v; want ErrAgentDead", err)
	}
}

// Happy path test deferred to integration test in Task 18 — requires
// a real persistent agent process.
```

- [ ] **Step 2: Run to fail**

- [ ] **Step 3: Implement Reattach**

```go
// Reattach connects to a previously-spawned persistent session by ID.
// On success, returns a ProcessHandle equivalent to one returned by
// Manager.Register followed by Start.
//
// See spec §"Error Matrix" for the full set of returned errors.
func (m *Manager) Reattach(ctx context.Context, id string) (ProcessHandle, error) {
	sessionDir := filepath.Join(m.persistentDir, id)
	meta, err := agent.ReadPersistentMeta(sessionDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrSessionNotFound
		}
		return nil, errors.Join(ErrSessionNotFound, err)
	}

	pid, err := agent.ReadPidFile(sessionDir)
	if err != nil {
		return nil, errors.Join(ErrAgentDead, err)
	}
	if !agent.IsPidAlive(pid) {
		return nil, ErrAgentDead
	}

	// Dial the UNIX socket
	sockPath := filepath.Join(sessionDir, "sock")
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", sockPath)
	if err != nil {
		// ENOENT is treated as session not found.
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrSessionNotFound
		}
		// ECONNREFUSED with a live PID means agent crashed mid-shutdown.
		return nil, errors.Join(ErrAgentDead, err)
	}

	// Read MsgHello, verify startedAt matches meta.StartedAt
	hello, err := readHelloFrame(conn)
	if err != nil {
		_ = conn.Close()
		return nil, errors.Join(ErrIncompatibleAgent, err)
	}
	if !meta.StartedAt.Equal(time.Unix(0, hello.StartedAt)) {
		_ = conn.Close()
		return nil, ErrAgentDead // PID rollover
	}

	// Read MsgPTYRingDump
	dump, err := readPTYRingDumpFrame(conn)
	if err != nil {
		_ = conn.Close()
		return nil, errors.Join(ErrIncompatibleAgent, err)
	}

	// Construct controller around the conn, set initial buffer
	ctrl, err := controller.NewControllerFromConn(controller.ControllerOptions{
		Spec:       meta.Spec,
		Conn:       conn,
		RuntimeDir: m.runtimeDir,
		OnLogChunk: func(stream uint8, data []byte) {
			m.emitLogChunk(meta.ID, stream, data)
		},
	})
	if err != nil { _ = conn.Close(); return nil, err }
	ctrl.SetInitialPTYBuffer(dump.Bytes)

	// Build a processHandle for the caller
	h := &processHandle{spec: meta.Spec, mgr: m, ctrl: ctrl}
	m.mu.Lock()
	m.handles[meta.ID] = h
	m.mu.Unlock()

	return h, nil
}
```

`controller.NewControllerFromConn` is a new constructor that takes a pre-existing conn (rather than dialing a new agent). Add this to `internal/controller/controller.go` if not present:

```go
// NewControllerFromConn constructs a Controller around an existing
// IPC connection. Used by Manager.Reattach (CW-G4) when the host
// connects to a daemonized agent rather than spawning a new one.
func NewControllerFromConn(opts ControllerOptions) (*Controller, error) {
	// Mirror NewController's setup but skip the spawn step;
	// adopt opts.Conn directly as the IPC channel.
	c := &Controller{ /* ... */ }
	c.conn = ipc.NewConnFromNetConn(opts.Conn, /* config */)
	c.conn.OnMessage(c.handle)
	c.conn.Start()
	// negotiate capability synchronously
	if err := c.negotiateCapabilities(context.Background()); err != nil {
		return nil, err
	}
	c.state.Store(int32(cwtypes.StateRunning))
	return c, nil
}
```

`ipc.NewConnFromNetConn` may need adding too — it's the analogue of `ipc.NewConn` but takes an existing `net.Conn` rather than a file descriptor.

These changes are wide; budget time for them.

- [ ] **Step 4: Helper functions for reading specific frames**

```go
// readHelloFrame reads exactly one MsgHello frame from conn and decodes it.
func readHelloFrame(conn net.Conn) (ipc.HelloPayload, error) { /* ... */ }

// readPTYRingDumpFrame reads one MsgPTYRingDump frame.
func readPTYRingDumpFrame(conn net.Conn) (ipc.PTYRingDumpPayload, error) { /* ... */ }
```

Implementation: use the existing `ipc.DecodeFrame` (or whatever the wire format reader is) to read one frame, dispatch on MsgType. If wrong type, return ErrIncompatibleAgent.

- [ ] **Step 5: Run unit tests, expect green**

```bash
go test -run TestManager_Reattach -v ./pkg/cliwrap/ -race
```

The "happy path" assertion is deferred to integration tests in Task 18.

- [ ] **Step 6: Commit**

```bash
git add pkg/cliwrap/persistent.go pkg/cliwrap/persistent_test.go \
        internal/controller/controller.go internal/ipc/conn.go
git commit -s -m "feat(manager): Reattach + full error matrix (CW-G4)

Manager.Reattach implements the spec error matrix:
  ErrSessionNotFound (no <dir>/<id>/ or no sock file)
  ErrAgentDead (no pid file, dead PID, or startedAt mismatch)
  ErrAlreadyAttached (agent rejected the connection)
  ErrIncompatibleAgent (handshake parsing failure)

Adds controller.NewControllerFromConn (constructor that adopts an
existing conn instead of spawning), and ipc.NewConnFromNetConn
(low-level analogue of NewConn that takes net.Conn).

Happy-path end-to-end coverage is in Task 18 integration tests."
```

---

## Task 15: Stop / Close semantics for persistent sessions

`h.Stop` on a persistent session terminates the agent (after killing the child). `h.Close` only closes the host conn; the agent stays alive in detached mode.

**Files:**
- Modify: `pkg/cliwrap/process.go` (Stop / Close adjustments)
- Modify: `internal/agent/dispatcher.go` or persistent.go (handle MsgStopChild → self-terminate when persistent)
- Modify: `pkg/cliwrap/persistent_test.go` (tests for the asymmetric Close)

- [ ] **Step 1: Failing test**

```go
func TestProcessHandle_Close_PersistentDoesNotKillAgent(t *testing.T) {
	// Spawn persistent /bin/cat, wait for ready, h.Close, verify agent
	// process is still alive (kill -0 against pid file).
	// Full setup borrowed from integration tests; sketched here.
	t.Skip("covered in Task 18 integration tests; requires real agent binary")
}
```

(Most of the assertion is in Task 18; this task implements the production code.)

- [ ] **Step 2: Implement Stop self-terminate (agent side)**

In `internal/agent/dispatcher.go::stopChild`, after the existing `cancel()` + `wg.Wait()`:

```go
func (d *Dispatcher) stopChild(timeout time.Duration) {
	/* existing: cancel, wg.Wait */

	// CW-G4: if persistent, this Stop is a session termination request.
	// Trigger agent self-shutdown after the child has been reaped.
	if d.persistent {
		// Signal agent.Run to exit cleanly. This is implemented by
		// closing a "shutdownRequested" channel that Run selects on.
		close(d.shutdownRequested)
	}
}
```

Add `persistent bool` and `shutdownRequested chan struct{}` to Dispatcher struct; initialize in `NewDispatcher`. Have `agent.Run` select on `shutdownRequested` alongside `ctx.Done()`.

- [ ] **Step 3: Implement Close asymmetry (host side)**

In `pkg/cliwrap/process.go::Close`, branch on `p.spec.Persistent`:

```go
func (p *processHandle) Close(ctx context.Context) error {
	p.mu.Lock()
	ctrl := p.ctrl
	if ctrl != nil {
		p.lastStatus = Status{
			State:    ctrl.State(),
			ChildPID: ctrl.ChildPID(),
		}
	}
	p.ctrl = nil
	p.mu.Unlock()
	if ctrl == nil { return nil }

	if p.spec.Persistent {
		// CW-G4: do NOT SIGTERM the agent. Just close the conn and
		// release the host-side controller; the agent stays alive in
		// detached mode for future reattach.
		return ctrl.CloseConnOnly(ctx)
	}
	// Non-persistent: existing behavior — Close also tears down the agent.
	return ctrl.Close(ctx)
}
```

Add `Controller.CloseConnOnly(ctx)`:

```go
// CloseConnOnly closes the IPC connection without sending SIGTERM to
// the agent. Used by ProcessHandle.Close on persistent sessions
// (CW-G4) — the agent should remain alive in detached mode.
func (c *Controller) CloseConnOnly(ctx context.Context) error {
	if c.closed.Swap(true) { return c.closeErr }
	if c.conn != nil { c.closeErr = c.conn.Close(ctx) }
	// NOTE: no c.handle.Close() (which would SIGTERM agent)
	return c.closeErr
}
```

- [ ] **Step 4: Run unit tests**

```bash
go test ./pkg/cliwrap/ -race
go test ./internal/controller/ -race
go test ./internal/agent/ -race
```

- [ ] **Step 5: Commit**

```bash
git add pkg/cliwrap/process.go internal/controller/controller.go internal/agent/dispatcher.go internal/agent/main.go
git commit -s -m "feat(persistent): asymmetric Stop/Close semantics (CW-G4)

Stop on a persistent session: child kill + cmd.Wait + agent self-
terminate (close shutdownRequested channel; agent.Run exits cleanly).
Sock + pid removed; meta.json + agent.log preserved for post-mortem.

Close on a persistent session: conn close only. Agent stays alive in
detached mode, ready for next reattach.

Non-persistent Close behavior is unchanged: still SIGTERMs agent
through AgentHandle.Close.

End-to-end coverage in Task 18 integration tests."
```

---

## Task 16: Child natural exit triggers agent self-terminate

When the supervised child exits naturally (e.g., command completes), a persistent agent must also self-terminate (no point keeping a supervisor without a supervisee).

**Files:**
- Modify: `internal/agent/dispatcher.go` (post-MsgChildExited self-terminate when persistent)

- [ ] **Step 1: Failing test (covered by Task 18 integration)**

```go
func TestPersistent_ChildNaturalExitTerminatesAgent(t *testing.T) {
	t.Skip("covered in Task 18 integration test")
}
```

- [ ] **Step 2: Implement**

In `dispatcher.go::startChild`, after the existing `_ = d.SendControl(MsgChildExited, ...)` line:

```go
_ = d.SendControl(ipc.MsgChildExited, ipc.ChildExitedPayload{
	PID:      int32(res.PID),
	ExitCode: int32(res.ExitCode),
	Signal:   int32(res.Signal),
	ExitedAt: res.ExitedAt.UnixNano(),
	Reason:   res.Reason,
}, false)

d.clearRunning()

// CW-G4: in persistent mode, child exit means session is over —
// agent has no more supervision work. Trigger self-shutdown.
if d.persistent {
	select {
	case <-d.shutdownRequested:
		// already requested
	default:
		close(d.shutdownRequested)
	}
}
```

(The `d.persistent` field was added in Task 15; the channel close is idempotent via select.)

- [ ] **Step 3: Run tests**

```bash
go test ./internal/agent/ -race
```

- [ ] **Step 4: Commit**

```bash
git add internal/agent/dispatcher.go
git commit -s -m "feat(agent): persistent agent self-terminates on child exit (CW-G4)

When the supervised child exits (any reason — completed normally,
killed by signal, etc.), a persistent agent immediately requests
self-shutdown via shutdownRequested channel. Agent.Run exits, sock +
pid are cleaned up, meta.json + agent.log are preserved for
post-mortem.

End-to-end coverage in Task 18."
```

---

## Task 17: CLI commands

`cliwrap list --persistent` lists sessions; `cliwrap kill <id>` performs Reattach + Stop + Close.

**Files:**
- Create: `cmd/cliwrap/cmd_list_persistent.go`
- Create: `cmd/cliwrap/cmd_kill.go`
- Create: `cmd/cliwrap/cmd_kill_test.go`
- Modify: root cmd file (register the two subcommands)

- [ ] **Step 1: Failing tests**

`cmd_kill_test.go`:

```go
func TestCmdKill_NonexistentReturnsError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CLIWRAP_PERSISTENT_DIR", tmp)
	cmd := newKillCmd()
	cmd.SetArgs([]string{"nonexistent"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error for nonexistent session")
	}
}
```

(If the CLI uses cobra/urfave, adjust the test to that idiom; otherwise use the raw `os.Args`-style runner.)

- [ ] **Step 2: Run to fail**

```bash
go test -run TestCmdKill -v ./cmd/cliwrap/
```

- [ ] **Step 3: Implement list-persistent**

`cmd_list_persistent.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

func runListPersistent() error {
	dir := os.Getenv("CLIWRAP_PERSISTENT_DIR")
	var opts []cliwrap.ManagerOption
	if dir != "" { opts = append(opts, cliwrap.WithPersistentDir(dir)) }
	mgr, err := cliwrap.NewManager(opts...)
	if err != nil { return err }
	defer func() { _ = mgr.Shutdown(context.Background()) }()

	infos, err := mgr.ListPersistent()
	if err != nil { return err }

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tPID\tSTARTED\tALIVE\tATTACHED")
	for _, i := range infos {
		alive := "false  stale (rm -rf to clean)"
		if i.Alive { alive = "true" }
		attached := "no"
		if i.Attached { attached = "yes" }
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n",
			i.ID, i.AgentPID, i.StartedAt.UTC().Format("2006-01-02T15:04:05Z"),
			alive, attached)
	}
	return w.Flush()
}
```

Hook into the existing `list` command: when invoked with `--persistent`, call `runListPersistent`. Otherwise, fallthrough to existing behavior.

- [ ] **Step 4: Implement kill**

`cmd_kill.go`:

```go
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

func runKill(id string) error {
	if id == "" { return fmt.Errorf("kill: session ID required") }

	dir := os.Getenv("CLIWRAP_PERSISTENT_DIR")
	var opts []cliwrap.ManagerOption
	if dir != "" { opts = append(opts, cliwrap.WithPersistentDir(dir)) }
	mgr, err := cliwrap.NewManager(opts...)
	if err != nil { return err }
	defer func() { _ = mgr.Shutdown(context.Background()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fmt.Fprintf(os.Stdout, "Connecting to session %s...\n", id)
	h, err := mgr.Reattach(ctx, id)
	if err != nil {
		// On ErrAgentDead, hint to user
		if errors.Is(err, cliwrap.ErrAgentDead) {
			fmt.Fprintf(os.Stdout, "Session %s appears stale; rm -rf to clean.\n", id)
		}
		return err
	}
	fmt.Fprintln(os.Stdout, "Stopping child...")
	if err := h.Stop(ctx); err != nil {
		_ = h.Close(ctx)
		return err
	}
	fmt.Fprintln(os.Stdout, "Cleanup complete.")
	return h.Close(ctx)
}
```

Hook into existing CLI dispatch: a new `kill` subcommand routes to `runKill(args[0])`.

- [ ] **Step 5: Run tests**

```bash
go test ./cmd/cliwrap/ -race
go vet ./cmd/cliwrap/
```

- [ ] **Step 6: Commit**

```bash
git add cmd/cliwrap/cmd_list_persistent.go cmd/cliwrap/cmd_kill.go cmd/cliwrap/cmd_kill_test.go cmd/cliwrap/main.go
git commit -s -m "feat(cli): \`cliwrap list --persistent\` + \`cliwrap kill <id>\` (CW-G4)

list --persistent prints a table of discovered sessions (ID, PID,
STARTED, ALIVE, ATTACHED). kill <id> does Reattach + Stop + Close in
sequence; on ErrAgentDead, prints a hint to rm -rf the stale dir.

Both subcommands honor CLIWRAP_PERSISTENT_DIR env or default to
\$HOME/.cliwrap/sessions/."
```

---

## Task 18: Integration tests

End-to-end scenarios from spec §"Test Strategy → Integration tests".

**Files:**
- Create: `test/integration/pty_persistent_test.go`

- [ ] **Step 1: Add 8 integration tests** — one per scenario in spec §"Test Strategy → Integration tests"

For each scenario, follow the existing pattern in `test/integration/pty_*_test.go`. Each test:
- Spawns a host (Manager) with `WithPersistentDir(t.TempDir())`.
- Builds and runs `cliwrap-agent` via `BuildAgentForTest(t)`.
- Asserts the spec'd behavior.
- Cleans up via `t.Cleanup`.

The 8 scenarios:

1. `TestPersistent_BasicLifecycle` — spawn /bin/cat with Persistent=true, write input, verify echo, h.Stop, verify cleanup.
2. `TestPersistent_HostDeathSurvival` — spawn, kill the test's "host harness" subprocess, pgrep agent still alive.
3. `TestPersistent_ReattachDeliversRingBuffer` — spawn, write known PTY content, kill host, ListPersistent, Reattach, first SubscribePTYData chunk contains content.
4. `TestPersistent_AlreadyAttached` — spawn, two reattach attempts; second gets ErrAlreadyAttached. Disconnect first, second succeeds.
5. `TestPersistent_AgentDeadByMissingPid` — write meta but no pid; Reattach returns ErrAgentDead.
6. `TestPersistent_PIDRollover` — write meta + pid pointing to live unrelated process; agent's hello startedAt mismatches → ErrAgentDead.
7. `TestPersistent_ChildNaturalExit` — spawn /bin/echo, child exits, agent self-terminates, sock + pid removed, meta + log preserved.
8. `TestPersistent_CloseDoesNotKillAgent` — spawn, h.Close, sock still listening, agent alive, second Reattach succeeds.

Each test ~50-100 lines including setup. Total file: ~600-800 lines.

Follow the existing test idioms (require.NoError, goleak.VerifyNone, deferred cleanups). Use `BuildAgentForTest(t)` — already exists.

- [ ] **Step 2: Run all integration tests**

```bash
go test ./test/integration/ -v -race
```

Iterate on bugs as they surface. Allocate ~3 hrs for this iteration.

- [ ] **Step 3: Commit (when all pass)**

```bash
git add test/integration/pty_persistent_test.go
git commit -s -m "test(integration): persistent + reattach end-to-end (CW-G4)

Eight integration tests covering spec §Test Strategy:
- BasicLifecycle, HostDeathSurvival, ReattachDeliversRingBuffer,
  AlreadyAttached, AgentDeadByMissingPid, PIDRollover,
  ChildNaturalExit, CloseDoesNotKillAgent.

All pass with -race clean."
```

---

## Task 19: Chaos test — persistent burst regression

Opt-in burst test that exercises N=25 spawn → kill host → Reattach → Stop cycles.

**Files:**
- Create: `test/chaos/persistent_burst_test.go`

- [ ] **Step 1: Implement**

Mirror the existing `burst_spawn_test.go` pattern but for persistent sessions. Use the same safety gates (CHAOS_BURST=1, agent count delta ≤3, cat count delta ≤3, baseline-PID-aware cleanup).

```go
// SPDX-License-Identifier: Apache-2.0

package chaos

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
	"go.uber.org/goleak"
)

const persistentBurstN = 25

func TestPersistent_BurstSpawnReattach_NoLeak(t *testing.T) {
	if os.Getenv("CHAOS_BURST") == "" {
		t.Skip("opt-in: set CHAOS_BURST=1 to run")
	}
	if testing.Short() { t.Skip("burst takes ~30s") }
	defer goleak.VerifyNone(t)

	baselineAgentPIDs := captureCliwrapAgentPIDs()
	baselineCatPIDs := captureCatPIDs()
	t.Cleanup(func() { killCliwrapAgentsExcept(baselineAgentPIDs) })

	persistentDir := t.TempDir()
	agentBin := supervise.BuildAgentForTest(t)

	for i := 0; i < persistentBurstN; i++ {
		// Spawn (Persistent=true), kill host process via mgr.Shutdown,
		// Reattach in a fresh manager, h.Stop.
		mgr1, err := cliwrap.NewManager(
			cliwrap.WithAgentPath(agentBin),
			cliwrap.WithPersistentDir(persistentDir),
			cliwrap.WithRuntimeDir(filepath.Join(t.TempDir(), fmt.Sprintf("rt-%d", i))),
		)
		if err != nil { t.Fatalf("NewManager %d: %v", i, err) }

		spec, err := cliwrap.NewSpec(fmt.Sprintf("burst-p-%d", i), "/bin/cat").
			WithPTY(&cliwrap.PTYConfig{InitialCols: 80, InitialRows: 24}).
			WithStopTimeout(2 * time.Second).
			Persistent().
			Build()
		if err != nil { t.Fatalf("spec %d: %v", i, err) }
		h, err := mgr1.Register(spec)
		if err != nil { t.Fatalf("Register %d: %v", i, err) }
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.Start(ctx); err != nil {
			cancel()
			t.Fatalf("Start %d: %v", i, err)
		}
		_ = h.Close(ctx)
		_ = mgr1.Shutdown(ctx)
		cancel()

		// New manager, Reattach, Stop
		mgr2, err := cliwrap.NewManager(
			cliwrap.WithAgentPath(agentBin),
			cliwrap.WithPersistentDir(persistentDir),
		)
		if err != nil { t.Fatalf("NewManager2 %d: %v", i, err) }
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		h2, err := mgr2.Reattach(ctx2, fmt.Sprintf("burst-p-%d", i))
		if err != nil {
			cancel2()
			_ = mgr2.Shutdown(context.Background())
			t.Fatalf("Reattach %d: %v", i, err)
		}
		if err := h2.Stop(ctx2); err != nil {
			cancel2()
			_ = mgr2.Shutdown(context.Background())
			t.Fatalf("Stop %d: %v", i, err)
		}
		_ = h2.Close(ctx2)
		_ = mgr2.Shutdown(context.Background())
		cancel2()

		// Mid-loop sanity check (every 5 iters)
		if (i+1)%5 == 0 {
			agentDelta := len(captureCliwrapAgentPIDs()) - len(baselineAgentPIDs)
			catDelta := len(captureCatPIDs()) - len(baselineCatPIDs)
			if agentDelta > 3 {
				t.Fatalf("ABORT iter %d: cliwrap-agent delta=%d > 3", i+1, agentDelta)
			}
			if catDelta > 3 {
				t.Fatalf("ABORT iter %d: cat delta=%d > 3", i+1, catDelta)
			}
		}
	}

	// Allow brief reaper drain
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		agentDelta := len(captureCliwrapAgentPIDs()) - len(baselineAgentPIDs)
		catDelta := len(captureCatPIDs()) - len(baselineCatPIDs)
		if agentDelta == 0 && catDelta == 0 { break }
		time.Sleep(50 * time.Millisecond)
	}
	if d := len(captureCliwrapAgentPIDs()) - len(baselineAgentPIDs); d != 0 {
		t.Fatalf("post-burst cliwrap-agent survivors: %d", d)
	}
	if d := len(captureCatPIDs()) - len(baselineCatPIDs); d != 0 {
		t.Fatalf("post-burst cat survivors: %d", d)
	}
}
```

(The `Persistent()` builder method is added in Task 1's spec extension — if not, expose `WithPersistentFlag(true)` on the SpecBuilder; verify against existing builder pattern.)

- [ ] **Step 2: Run with CHAOS_BURST=1**

```bash
ps -A | wc -l
sysctl kern.maxproc
CHAOS_BURST=1 go test -run TestPersistent_BurstSpawnReattach_NoLeak -v ./test/chaos/ -timeout 120s
```

Expected: PASS. Verifies CW-G3.1 is sidestepped by per-session UNIX sockets.

- [ ] **Step 3: Commit**

```bash
git add test/chaos/persistent_burst_test.go
git commit -s -m "test(chaos): persistent burst regression (CW-G4)

Opt-in N=25 spawn → kill host → Reattach → Stop cycles. Asserts
zero cliwrap-agent + cat survivors at completion.

Implicit verification of CW-G3.1 sidestep: persistent sessions use
per-session UNIX sockets rather than Manager-shared spawner state,
so the iter-~20 capability-negotiation timeout from CW-G3.1 should
not surface here even at N=25 cycles."
```

---

## Task 20: CHANGELOG + final acceptance

**Files:**
- Modify: `CHANGELOG.md`
- (No code changes; verification only)

- [ ] **Step 1: Add CHANGELOG entry**

In `CHANGELOG.md`, under `[Unreleased] → ### Added`:

```markdown
- CW-G4: Persistent sessions + reattach. New `Spec.Persistent` flag opts a
  session into outliving the host process. New `Spec.RingBufferSize`
  controls in-memory PTY scrollback for redraw on reattach. New
  `WithPersistentDir(path)` Manager option. New `Manager.ListPersistent()`,
  `Manager.Reattach(ctx, id)`. New CLI commands `cliwrap list --persistent`
  and `cliwrap kill <id>`. Default behavior (Persistent=false) unchanged.
```

- [ ] **Step 2: Run full test suite**

```bash
go test -race ./...
```

All packages PASS. CW-G3 burst chaos test (default N=15) continues to PASS:

```bash
CHAOS_BURST=1 go test -run TestBurst_SpawnStop_NoLeak -v ./test/chaos/ -timeout 90s
```

- [ ] **Step 3: Run new chaos test**

```bash
CHAOS_BURST=1 go test -run TestPersistent_BurstSpawnReattach -v ./test/chaos/ -timeout 120s
```

- [ ] **Step 4: ai-m downstream sanity check (optional)**

```bash
cd ../../ai-m
go install ../harness/cli-wrapper/cmd/cliwrap-agent
PATH="/Users/wm-it-22-00661/.gvm/pkgsets/go1.23.12/global/bin:$PATH" \
    BENCH_RUNS=3 go run ./bench
go build -o /tmp/aim-bench-gate ./bench/gate
/tmp/aim-bench-gate -baseline=bench/baseline.json -current=bench/last-run.json
```

Expected: gate continues 5/5 PASS. CW-G4 should not regress non-persistent use.

- [ ] **Step 5: Commit CHANGELOG**

```bash
git add CHANGELOG.md
git commit -s -m "docs(changelog): record CW-G4 persistent sessions + reattach"
```

- [ ] **Step 6: Report results**

Final report should list:
- All cli-wrapper test packages PASS with -race.
- CW-G3 burst test continues to PASS at N=15.
- New CW-G4 chaos test PASS at N=25.
- ai-m bench gate continues 5/5 PASS.
- CHANGELOG entry land.

---

## Verification matrix

| Test | Before CW-G4 | After CW-G4 |
|---|---|---|
| `go test ./internal/... -race` | PASS | PASS (with new ringbuffer / persistent / persistent_meta tests) |
| `go test ./pkg/cliwrap/ -race` | PASS | PASS (with new persistent + spec tests) |
| `go test ./test/integration/ -race` | PASS | PASS (with 8 new persistent tests) |
| `CHAOS_BURST=1 go test -run TestBurst_SpawnStop_NoLeak ./test/chaos/` | PASS at N=15 | PASS at N=15 (regression guard) |
| `CHAOS_BURST=1 go test -run TestPersistent_BurstSpawnReattach ./test/chaos/` | n/a | PASS at N=25 |
| `BENCH_RUNS=5 go run ./bench` (ai-m) | gate 5/5 PASS | gate 5/5 PASS (no regression) |

---

## Rollback plan

Each task is one or more commits with conventional-commit subjects. To rollback:

- Tasks 1-7 are foundational; can be reverted independently as a group, no functional impact (default Persistent=false leaves all tests passing).
- Tasks 8-12 implement the daemonization + reattach core. Rollback together if a deep flaw is found; non-persistent behavior is unchanged.
- Tasks 13-17 are user-facing API + CLI. Can be reverted alone if API design needs revision.
- Tasks 18-20 are tests + docs. Can be reverted independently.

No database migrations, no on-disk format changes that would persist after rollback (persistent metadata is per-session, ephemeral).
