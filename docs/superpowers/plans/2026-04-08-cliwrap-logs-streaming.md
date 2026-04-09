# cliwrap logs Streaming (MVP: Snapshot) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the end-to-end log collection pipeline so that `cliwrap logs <id>` prints the current stdout/stderr ring-buffer contents of a supervised process. This removes the first entry from `CHANGELOG.md`'s "Known Limitations" list and delivers a feature already promised by `cliwrap --help`.

**Architecture:** The plumbing already exists as isolated pieces — `LogChunkPayload` in the IPC layer, `logcollect.Collector` as a standalone package, `mgmt.MsgLogsRequest`/`LogsStreamPayload` in the mgmt protocol, `Runner.StdoutSink`/`StderrSink` in the agent runner — but **nothing connects them**. Child stdout/stderr today is silently discarded by `Runner`'s default sinks. This plan connects the four islands into one flow:

1. **Agent**: A new `chunkWriter` (`io.Writer`) wraps bytes into `MsgLogChunk` IPC frames and is installed as `Runner.StdoutSink` / `StderrSink` when the dispatcher starts a child.
2. **Controller**: Inbound `MsgLogChunk` frames trigger a caller-supplied `OnLogChunk` callback (new field on `ControllerOptions`).
3. **Manager**: A lazily-constructed `logcollect.Collector` is fed from the `OnLogChunk` callback; `Manager.LogsSnapshot(id, stream)` exposes the ring buffer through the `mgmt.ManagerAPI` interface.
4. **Mgmt server**: `MsgLogsRequest` handler calls `LogsSnapshot` and returns a single `LogsStreamPayload` with `EOF=true`.
5. **CLI**: `cmd_logs.go` parses args, dials the mgmt socket, sends `MsgLogsRequest` for the requested stream(s), prints bytes.

**Tech Stack:** Go 1.22 standard library + existing project packages (`internal/ipc`, `internal/mgmt`, `internal/logcollect`, `pkg/cliwrap`). No new dependencies.

**Scope boundaries:**
- ✅ **In scope**: snapshot mode only — `cliwrap logs <id>` dumps the current ring-buffer contents and exits.
- ✅ **In scope**: `--stream stdout|stderr|all` flag (default: `all`). `all` makes two sequential mgmt calls.
- ❌ **Out of scope**: `--follow` / `-f` live tailing (requires persistent mgmt connection and server-push streaming; defer to follow-up plan).
- ❌ **Out of scope**: `--since <duration>` / `--lines N` filtering (the ring buffer has no notion of time or line boundaries in v1).
- ❌ **Out of scope**: `cliwrap events` implementation. Shares the streaming machinery but is a separate feature.
- ❌ **Out of scope**: rotator / file-backed log persistence. The MVP is in-memory only.
- ❌ **Out of scope**: the `diag` (stream=2) channel — for agent debug output, not user-facing.

**Context for the implementer:**

- **Assume zero context** on the cli-wrapper internals. File paths and types are exact. Each code block can be copy-pasted as-is.
- **Test naming convention** in this repo: `TestX_Behavior` (underscore separator). Use `t *testing.T`, `require` from `github.com/stretchr/testify/require`, and `goleak.VerifyNone(t)` at the top of any test that spawns goroutines.
- **Commit message style**: Conventional Commits (`feat:`, `fix:`, `refactor:`, `test:`, `docs:`, etc.). Subject line ≤72 chars. Body explains the *why*. DCO sign-off is mandatory: use `git commit -s`. **Never** add `Co-Authored-By:` or "Generated with Claude" trailers — the project forbids them.
- **Linter**: CI runs `golangci-lint` with the v2 config in `.golangci.yml`. `errcheck` flags unchecked Close-on-defer. Use `defer func() { _ = x.Close() }()` for intentional discards, matching the pattern in `internal/mgmt/server.go:83` and `cmd/cliwrap/cmd_list.go:28`.
- **Goroutine hygiene**: Every test must pass `goleak.VerifyNone(t)`. `io.Copy` inside `Runner.Run` runs in a goroutine that terminates when the child's pipe closes — do not introduce new unbounded goroutines in the chunkWriter.
- **Existing patterns to follow**:
  - `cmd/cliwrap/cmd_list.go` for CLI command structure (flag parsing, mgmt dial, decode, print)
  - `internal/mgmt/server_test.go` for mgmt server unit test patterns
  - `test/integration/crash_test.go` for end-to-end integration test structure

---

## File Structure

| File | Status | Responsibility |
|------|--------|----------------|
| `internal/agent/logchunk_writer.go` | **Create** | `io.Writer` adapter that sends `MsgLogChunk` frames via the dispatcher |
| `internal/agent/logchunk_writer_test.go` | **Create** | Unit tests for the writer (byte accounting, sequence numbering, zero-length handling) |
| `internal/agent/dispatcher.go` | **Modify** | Wire stdout/stderr `chunkWriter` instances when dispatcher starts a child |
| `internal/agent/dispatcher_test.go` | **Modify** | Assert `Runner.StdoutSink` is set after startChild |
| `internal/controller/controller.go` | **Modify** | Add `OnLogChunk` field to `ControllerOptions`; invoke it from `handleMessage` on `MsgLogChunk` |
| `internal/controller/controller_test.go` | **Modify** | Test that inbound `MsgLogChunk` triggers the callback with correct stream + data |
| `pkg/cliwrap/manager.go` | **Modify** | Add lazy `collector` field; inject `OnLogChunk` when building controllers |
| `pkg/cliwrap/manager_logs.go` | **Create** | `ensureCollector`, `emitLogChunk`, `LogsSnapshot` — all Collector access goes through this file |
| `pkg/cliwrap/manager_logs_test.go` | **Create** | Unit tests: snapshot round-trip, unknown id, unknown stream |
| `pkg/cliwrap/mgmt_api.go` | **Modify** | Add `LogsSnapshot` method delegating to `manager_logs.go` |
| `internal/mgmt/proto.go` | *(no change)* | `LogsRequestPayload` and `LogsStreamPayload` already defined |
| `internal/mgmt/server.go` | **Modify** | Extend `ManagerAPI` interface with `LogsSnapshot`; handle `MsgLogsRequest` → `MsgLogsStream` with `EOF=true` |
| `internal/mgmt/server_test.go` | **Modify** | Add `fakeManager.LogsSnapshot`; new round-trip test for logs request |
| `cmd/cliwrap/cmd_logs.go` | **Modify** (replace stub) | Parse flags, dial mgmt, call `MsgLogsRequest`, print bytes |
| `test/integration/logs_test.go` | **Create** | Spawn `fixture-noisy`, wait for output, verify `cliwrap logs` snapshot contains expected lines |
| `CHANGELOG.md` | **Modify** | Move `cliwrap logs` out of "Known Limitations" and into `[Unreleased]` `Added` |

---

## Task 1: `chunkWriter` — io.Writer → MsgLogChunk adapter

The agent-side adapter that wraps arbitrary `[]byte` writes into `MsgLogChunk` IPC frames. This is a pure unit with no goroutines — it's called synchronously from `io.Copy`'s worker goroutine inside `Runner.Run`.

**Files:**
- Create: `internal/agent/logchunk_writer.go`
- Create: `internal/agent/logchunk_writer_test.go`

- [ ] **Step 1.1: Write the failing test**

Create the file with the first test case.

```go
// internal/agent/logchunk_writer_test.go
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

type recordingSender struct {
	calls []recordedCall
}

type recordedCall struct {
	msgType ipc.MsgType
	payload ipc.LogChunkPayload
}

func (r *recordingSender) SendControl(t ipc.MsgType, payload any, ack bool) error {
	if p, ok := payload.(ipc.LogChunkPayload); ok {
		r.calls = append(r.calls, recordedCall{msgType: t, payload: p})
	}
	return nil
}

func TestChunkWriter_WriteEmitsLogChunkWithStream(t *testing.T) {
	rec := &recordingSender{}
	w := newChunkWriter(rec, 0) // stream 0 = stdout

	n, err := w.Write([]byte("hello world"))
	require.NoError(t, err)
	require.Equal(t, 11, n)

	require.Len(t, rec.calls, 1)
	require.Equal(t, ipc.MsgLogChunk, rec.calls[0].msgType)
	require.Equal(t, uint8(0), rec.calls[0].payload.Stream)
	require.Equal(t, []byte("hello world"), rec.calls[0].payload.Data)
}

func TestChunkWriter_SequenceNumbersIncrement(t *testing.T) {
	rec := &recordingSender{}
	w := newChunkWriter(rec, 1) // stream 1 = stderr

	_, _ = w.Write([]byte("a"))
	_, _ = w.Write([]byte("b"))
	_, _ = w.Write([]byte("c"))

	require.Len(t, rec.calls, 3)
	require.Equal(t, uint64(1), rec.calls[0].payload.SeqNo)
	require.Equal(t, uint64(2), rec.calls[1].payload.SeqNo)
	require.Equal(t, uint64(3), rec.calls[2].payload.SeqNo)
	for _, c := range rec.calls {
		require.Equal(t, uint8(1), c.payload.Stream)
	}
}

func TestChunkWriter_EmptyWriteIsNoop(t *testing.T) {
	rec := &recordingSender{}
	w := newChunkWriter(rec, 0)

	n, err := w.Write(nil)
	require.NoError(t, err)
	require.Equal(t, 0, n)

	n, err = w.Write([]byte{})
	require.NoError(t, err)
	require.Equal(t, 0, n)

	require.Empty(t, rec.calls, "empty writes must not emit frames")
}
```

- [ ] **Step 1.2: Run the test to verify it fails**

```bash
go test ./internal/agent -run TestChunkWriter -v
```

Expected: `FAIL` with errors like `undefined: newChunkWriter`. This confirms the test harness compiles against existing types but the implementation is missing.

- [ ] **Step 1.3: Write the minimal implementation**

```go
// internal/agent/logchunk_writer.go
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"sync/atomic"

	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

// chunkSender is the minimal interface chunkWriter needs from a Dispatcher.
// Using an interface keeps the writer testable without spinning up a real
// IPC Conn.
type chunkSender interface {
	SendControl(t ipc.MsgType, payload any, ack bool) error
}

// chunkWriter is an io.Writer that wraps each Write call into one
// ipc.MsgLogChunk frame carrying the bytes as the payload Data.
//
// chunkWriter is safe for use from a single goroutine (the io.Copy worker
// inside Runner.Run). Per-write SeqNo is monotonic; the stream id is fixed
// for the lifetime of the writer.
type chunkWriter struct {
	sender chunkSender
	stream uint8
	seq    atomic.Uint64
}

// newChunkWriter returns a chunkWriter that emits MsgLogChunk frames
// tagged with the given stream id (0=stdout, 1=stderr, 2=diag).
func newChunkWriter(sender chunkSender, stream uint8) *chunkWriter {
	return &chunkWriter{sender: sender, stream: stream}
}

// Write implements io.Writer. Empty writes are a no-op and return (0, nil).
// A non-empty write always produces exactly one MsgLogChunk frame.
func (w *chunkWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	// Defensive copy: io.Copy reuses its internal buffer across calls, so
	// retaining the original slice would cause later writes to corrupt the
	// payload of earlier MsgLogChunk frames while they sit in the outbox.
	buf := make([]byte, len(p))
	copy(buf, p)

	next := w.seq.Add(1)
	payload := ipc.LogChunkPayload{
		Stream: w.stream,
		SeqNo:  next,
		Data:   buf,
	}
	if err := w.sender.SendControl(ipc.MsgLogChunk, payload, false); err != nil {
		return 0, err
	}
	return len(p), nil
}
```

- [ ] **Step 1.4: Run the test to verify it passes**

```bash
go test ./internal/agent -run TestChunkWriter -v
```

Expected: `PASS` for all three tests.

- [ ] **Step 1.5: Run vet and build**

```bash
go vet ./internal/agent/...
go build ./...
```

Expected: no output (clean).

- [ ] **Step 1.6: Commit**

```bash
git add internal/agent/logchunk_writer.go internal/agent/logchunk_writer_test.go
git commit -s -m "feat(agent): add chunkWriter io.Writer → MsgLogChunk adapter

Introduce chunkWriter, an io.Writer that wraps each Write into one
ipc.MsgLogChunk frame sent via a chunkSender interface (satisfied by
Dispatcher.SendControl). Each write gets a monotonic SeqNo so the
host side can reason about order, and the payload bytes are defensively
copied because io.Copy reuses its internal buffer between calls —
without the copy, later writes would corrupt the payload of earlier
frames still sitting in the outbox.

This is the first piece of wiring for 'cliwrap logs' snapshot support.
The Dispatcher will install two chunkWriter instances (stdout and
stderr) on its Runner in a follow-up commit; without that wiring, the
writer is currently unused production code."
```

---

## Task 2: Wire chunkWriter into Dispatcher.startChild

The agent's `Dispatcher.startChild` currently leaves `Runner.StdoutSink` / `StderrSink` at their defaults (`io.Discard`). This task installs two `chunkWriter` instances — one for stdout, one for stderr — on the runner before invoking `Run`.

**Files:**
- Modify: `internal/agent/dispatcher.go` (function `startChild`, lines ~50–95)
- Modify: `internal/agent/dispatcher_test.go`

- [ ] **Step 2.1: Write the failing test**

Open `internal/agent/dispatcher_test.go`. Find the existing tests and add the following case at the bottom of the file.

```go
// Append to internal/agent/dispatcher_test.go

func TestDispatcher_StartChildInstallsLogChunkSinks(t *testing.T) {
	// We can't easily spin up a real Conn in a unit test, but we can
	// observe the effect by calling startChild on a dispatcher whose
	// runner is a spy. After startChild runs (and before the fake child
	// exits), runner.StdoutSink and runner.StderrSink must be non-nil
	// chunkWriter instances.
	d := &Dispatcher{runner: NewRunner()}

	// Before startChild: defaults (io.Discard) are set by NewRunner.
	require.NotNil(t, d.runner.StdoutSink)
	require.NotNil(t, d.runner.StderrSink)

	// Call the helper that installs the sinks (extracted for testability).
	d.installLogSinks(&recordingSender{})

	_, okOut := d.runner.StdoutSink.(*chunkWriter)
	require.True(t, okOut, "StdoutSink should be a *chunkWriter after install")

	_, okErr := d.runner.StderrSink.(*chunkWriter)
	require.True(t, okErr, "StderrSink should be a *chunkWriter after install")
}
```

Note: the test exercises a helper `installLogSinks` that you will add in Step 2.3. This keeps the wiring unit-testable without a real IPC conn.

- [ ] **Step 2.2: Run the test to verify it fails**

```bash
go test ./internal/agent -run TestDispatcher_StartChildInstallsLogChunkSinks -v
```

Expected: `FAIL` with `d.installLogSinks undefined`.

- [ ] **Step 2.3: Add the helper and wire it into startChild**

Open `internal/agent/dispatcher.go`. Add this method near the bottom of the file, just above `SendControl`:

```go
// installLogSinks replaces the runner's default io.Discard sinks with
// chunkWriter instances that forward bytes to the host via MsgLogChunk.
// Extracted from startChild so it can be unit-tested without a real Conn.
func (d *Dispatcher) installLogSinks(sender chunkSender) {
	d.runner.StdoutSink = newChunkWriter(sender, 0) // 0 = stdout
	d.runner.StderrSink = newChunkWriter(sender, 1) // 1 = stderr
}
```

Then modify `startChild` to call it. Find this block:

```go
	ctx, cancel := context.WithCancel(context.Background())
	d.cancelFn = cancel
	d.mu.Unlock()

	d.wg.Add(1)
```

Insert the sink installation immediately before `d.wg.Add(1)`:

```go
	ctx, cancel := context.WithCancel(context.Background())
	d.cancelFn = cancel
	d.mu.Unlock()

	d.installLogSinks(d)

	d.wg.Add(1)
```

`d` (the `*Dispatcher`) already implements `chunkSender` because it has a `SendControl` method with the right signature, so no explicit interface assertion is needed.

- [ ] **Step 2.4: Run the test to verify it passes**

```bash
go test ./internal/agent -run TestDispatcher_StartChildInstallsLogChunkSinks -v
```

Expected: `PASS`.

- [ ] **Step 2.5: Run the full agent test suite to catch regressions**

```bash
go test -race -count=1 ./internal/agent/...
```

Expected: all tests pass. The existing dispatcher tests should still work because `installLogSinks` only modifies the sinks — it does not change any existing behavior.

- [ ] **Step 2.6: Commit**

```bash
git add internal/agent/dispatcher.go internal/agent/dispatcher_test.go
git commit -s -m "feat(agent): install chunkWriter sinks on startChild

Replace Runner.StdoutSink/StderrSink from their io.Discard defaults
with chunkWriter instances when the dispatcher spawns a child. The
installation is factored into a small installLogSinks helper so it
can be unit-tested without standing up a real ipc.Conn.

With this change, every byte the child writes to stdout or stderr is
now wrapped in a MsgLogChunk frame and enqueued on the outbound IPC
channel. The host side (controller + manager) still ignores these
frames — that wiring follows in subsequent commits."
```

---

## Task 3: Controller forwards MsgLogChunk via OnLogChunk callback

The controller already has `handleMessage` which is registered as the `conn.OnMessage` callback. It currently switches on message type and handles `MsgHelloAck`, `MsgChildStarted`, `MsgChildExited`, `MsgChildError`. Add a `MsgLogChunk` case that invokes a caller-supplied callback.

**Files:**
- Modify: `internal/controller/controller.go`
- Modify: `internal/controller/controller_test.go`

- [ ] **Step 3.1: Write the failing test**

Open `internal/controller/controller_test.go` and add this test at the end.

```go
// Append to internal/controller/controller_test.go

func TestController_HandleLogChunkInvokesCallback(t *testing.T) {
	var (
		gotStream uint8
		gotData   []byte
		called    int
	)

	c := &Controller{
		opts: ControllerOptions{
			OnLogChunk: func(stream uint8, data []byte) {
				gotStream = stream
				gotData = append(gotData, data...)
				called++
			},
		},
	}

	payload, err := ipc.EncodePayload(ipc.LogChunkPayload{
		Stream: 1,
		SeqNo:  42,
		Data:   []byte("hello stderr"),
	})
	require.NoError(t, err)

	c.handleMessage(ipc.OutboxMessage{
		Header:  ipc.Header{MsgType: ipc.MsgLogChunk},
		Payload: payload,
	})

	require.Equal(t, 1, called)
	require.Equal(t, uint8(1), gotStream)
	require.Equal(t, []byte("hello stderr"), gotData)
}

func TestController_HandleLogChunkWithoutCallbackIsNoop(t *testing.T) {
	// No callback set — should not panic.
	c := &Controller{opts: ControllerOptions{}}

	payload, _ := ipc.EncodePayload(ipc.LogChunkPayload{
		Stream: 0,
		Data:   []byte("ignored"),
	})

	require.NotPanics(t, func() {
		c.handleMessage(ipc.OutboxMessage{
			Header:  ipc.Header{MsgType: ipc.MsgLogChunk},
			Payload: payload,
		})
	})
}
```

- [ ] **Step 3.2: Run the test to verify it fails**

```bash
go test ./internal/controller -run TestController_HandleLogChunk -v
```

Expected: `FAIL` with `unknown field OnLogChunk in struct literal of type ControllerOptions`.

- [ ] **Step 3.3: Extend ControllerOptions with OnLogChunk**

Open `internal/controller/controller.go`. Find the `ControllerOptions` struct near the top:

```go
// ControllerOptions configures a Controller.
type ControllerOptions struct {
	Spec       cwtypes.Spec
	Spawner    *supervise.Spawner
	RuntimeDir string
}
```

Add a new field:

```go
// ControllerOptions configures a Controller.
type ControllerOptions struct {
	Spec       cwtypes.Spec
	Spawner    *supervise.Spawner
	RuntimeDir string

	// OnLogChunk is invoked for every inbound ipc.MsgLogChunk frame.
	// The callback runs on the ipc.Conn dispatch goroutine, so it must
	// not block for long. Callers that need to do heavy work should
	// hand the data off to a separate goroutine. May be nil, in which
	// case log chunks are dropped.
	OnLogChunk func(stream uint8, data []byte)
}
```

- [ ] **Step 3.4: Handle MsgLogChunk in handleMessage**

In the same file, find `handleMessage`:

```go
func (c *Controller) handleMessage(msg ipc.OutboxMessage) {
	switch msg.Header.MsgType {
	case ipc.MsgHelloAck:
		// Handshake complete; nothing to record for now.
```

Add a new case before the closing brace of the switch, after `MsgChildError`:

```go
	case ipc.MsgChildError:
		c.state.Store(int32(cwtypes.StateCrashed))
	case ipc.MsgLogChunk:
		if c.opts.OnLogChunk == nil {
			return
		}
		var p ipc.LogChunkPayload
		if err := ipc.DecodePayload(msg.Payload, &p); err != nil {
			return
		}
		c.opts.OnLogChunk(p.Stream, p.Data)
	}
}
```

- [ ] **Step 3.5: Run the tests to verify they pass**

```bash
go test ./internal/controller -run TestController_HandleLogChunk -v
```

Expected: `PASS` for both tests.

- [ ] **Step 3.6: Run the full controller test suite**

```bash
go test -race -count=1 ./internal/controller/...
```

Expected: all tests pass (existing behavior unchanged, new cases green).

- [ ] **Step 3.7: Commit**

```bash
git add internal/controller/controller.go internal/controller/controller_test.go
git commit -s -m "feat(controller): forward MsgLogChunk via OnLogChunk callback

Add an optional OnLogChunk callback to ControllerOptions and invoke it
from handleMessage when an inbound MsgLogChunk frame arrives. The
callback receives the stream id (0=stdout, 1=stderr) and the raw bytes
from LogChunkPayload.Data.

The callback is opt-in: leaving it nil causes incoming log chunks to
be silently dropped, preserving the current behavior for any caller
that does not care about log forwarding. The nil-check comes before
the decode so we pay zero cost per frame when logging is disabled.

This is the host-side hook that the Manager will wire up next, letting
log bytes flow from agent → controller → manager collector."
```

---

## Task 4: Manager collector + log-chunk routing

The `Manager` gains a lazily-constructed `*logcollect.Collector` and wires its `OnLogChunk` hook into every controller it creates. All collector-access code lives in a new file `manager_logs.go` so the main `manager.go` stays focused on lifecycle.

**Files:**
- Modify: `pkg/cliwrap/manager.go` (add field, inject callback)
- Create: `pkg/cliwrap/manager_logs.go`
- Create: `pkg/cliwrap/manager_logs_test.go`

- [ ] **Step 4.1: Write the failing test**

Create the new test file.

```go
// pkg/cliwrap/manager_logs_test.go
// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManager_LogsSnapshotRoundTrip(t *testing.T) {
	m := &Manager{}

	// Nothing written yet: snapshot for unknown id/stream is nil.
	require.Nil(t, m.LogsSnapshot("redis", 0))

	// Simulate inbound log chunks as if the controller callback fired.
	m.emitLogChunk("redis", 0, []byte("line1\n"))
	m.emitLogChunk("redis", 0, []byte("line2\n"))
	m.emitLogChunk("redis", 1, []byte("err1\n"))

	stdout := m.LogsSnapshot("redis", 0)
	require.Equal(t, "line1\nline2\n", string(stdout))

	stderr := m.LogsSnapshot("redis", 1)
	require.Equal(t, "err1\n", string(stderr))

	// Unrelated id remains empty.
	require.Nil(t, m.LogsSnapshot("other", 0))
}

func TestManager_LogsSnapshotIsolatesProcesses(t *testing.T) {
	m := &Manager{}

	m.emitLogChunk("a", 0, []byte("A-out"))
	m.emitLogChunk("b", 0, []byte("B-out"))

	require.Equal(t, []byte("A-out"), m.LogsSnapshot("a", 0))
	require.Equal(t, []byte("B-out"), m.LogsSnapshot("b", 0))
}
```

- [ ] **Step 4.2: Run the test to verify it fails**

```bash
go test ./pkg/cliwrap -run TestManager_LogsSnapshot -v
```

Expected: `FAIL` with `m.emitLogChunk undefined` and `m.LogsSnapshot undefined`.

- [ ] **Step 4.3: Create manager_logs.go**

```go
// pkg/cliwrap/manager_logs.go
// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"sync"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/logcollect"
)

// logsMu guards collectorOnce/collector so the lazy init is safe under
// concurrent callbacks arriving from multiple controllers.
//
// The collector itself is concurrency-safe (sync.RWMutex), but the
// pointer assignment during lazy init must be synchronized.
type managerLogs struct {
	collectorOnce sync.Once
	collector     *logcollect.Collector
}

// ensureCollector returns the lazily-constructed Collector. Thread-safe.
func (m *Manager) ensureCollector() *logcollect.Collector {
	m.logs.collectorOnce.Do(func() {
		m.logs.collector = logcollect.NewCollector(logcollect.CollectorOptions{
			// 1 MiB per (process, stream) — enough for several thousand
			// short log lines without unbounded memory growth.
			RingBufferBytes: 1 << 20,
		})
	})
	return m.logs.collector
}

// emitLogChunk is the hook wired into each Controller's OnLogChunk
// callback. It captures the processID from the closure and forwards
// the bytes into the shared Collector.
func (m *Manager) emitLogChunk(processID string, stream uint8, data []byte) {
	if len(data) == 0 {
		return
	}
	m.ensureCollector().Write(logcollect.LogEntry{
		ProcessID: processID,
		Stream:    stream,
		Timestamp: time.Now(),
		Data:      data,
	})
}

// LogsSnapshot returns the current ring-buffer contents for the given
// (processID, stream). Returns nil if no bytes have been recorded for
// that pair.
func (m *Manager) LogsSnapshot(processID string, stream uint8) []byte {
	// Read through ensureCollector so the first caller (typically a
	// mgmt client asking for logs of a process that hasn't written
	// anything yet) still sees nil instead of a panic.
	return m.ensureCollector().Snapshot(processID, stream)
}
```

- [ ] **Step 4.4: Add the logs field to Manager**

Open `pkg/cliwrap/manager.go`. Find the `Manager` struct:

```go
type Manager struct {
	agentPath  string
	runtimeDir string
	spawner    *supervise.Spawner

	mu      sync.Mutex
	handles map[string]*processHandle
	closed  bool

	// Plan 05 additions
	watcherOnce sync.Once
	watchStop   chan struct{}
	watchWG     sync.WaitGroup

	busOnce sync.Once
	bus     *eventbus.Bus
}
```

Add the logs field at the bottom of the struct:

```go
type Manager struct {
	agentPath  string
	runtimeDir string
	spawner    *supervise.Spawner

	mu      sync.Mutex
	handles map[string]*processHandle
	closed  bool

	// Plan 05 additions
	watcherOnce sync.Once
	watchStop   chan struct{}
	watchWG     sync.WaitGroup

	busOnce sync.Once
	bus     *eventbus.Bus

	// cliwrap logs pipeline — Collector is lazy-constructed.
	logs managerLogs
}
```

- [ ] **Step 4.5: Run the tests to verify they pass**

```bash
go test ./pkg/cliwrap -run TestManager_LogsSnapshot -v
```

Expected: `PASS` for both tests.

- [ ] **Step 4.6: Run the full pkg/cliwrap suite**

```bash
go test -race -count=1 ./pkg/cliwrap/...
```

Expected: all pass. The existing tests don't touch `m.logs` so there's no regression risk here.

- [ ] **Step 4.7: Commit**

```bash
git add pkg/cliwrap/manager.go pkg/cliwrap/manager_logs.go pkg/cliwrap/manager_logs_test.go
git commit -s -m "feat(cliwrap): add lazy Collector and LogsSnapshot to Manager

Introduce pkg/cliwrap/manager_logs.go holding all Collector access:
ensureCollector (lazy sync.Once init with 1 MiB per-stream capacity),
emitLogChunk (writes an entry keyed by processID + stream), and
LogsSnapshot (reads current ring-buffer contents).

The new managerLogs struct is embedded as an unexported field on
Manager. Nothing instantiates the Collector yet except LogsSnapshot
calls themselves — the controller-side wiring that routes actual
log bytes into emitLogChunk comes in the next commit."
```

---

## Task 5: Wire Manager.emitLogChunk into every new Controller

The `Manager.newController` helper constructs a `Controller` via `controller.NewController(...)`. Extend it so each controller has `OnLogChunk` set to a closure that captures the process ID and calls `m.emitLogChunk`.

**Files:**
- Modify: `pkg/cliwrap/manager.go` (function `newController`)
- Modify: `pkg/cliwrap/manager_logs_test.go` (add integration-ish test)

- [ ] **Step 5.1: Write the failing test**

Append this test to `pkg/cliwrap/manager_logs_test.go`:

```go
// Append to pkg/cliwrap/manager_logs_test.go

func TestManager_NewControllerInjectsOnLogChunk(t *testing.T) {
	// Use the public NewManager constructor so the internal Spawner is
	// set up. Direct &Manager{...} literals leave spawner nil, which
	// causes controller.NewController to reject the options with
	// "controller: Spawner is required". The agent path need not be
	// real — it's only exec'd at Start() time, and this test never
	// calls Start().
	m, err := NewManager(
		WithAgentPath("/does/not/matter"),
		WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	spec, err := NewSpec("p1", "/bin/true").Build()
	require.NoError(t, err)

	ctrl, err := m.newController(spec)
	require.NoError(t, err)
	require.NotNil(t, ctrl)

	// Simulate the controller forwarding a log chunk. The closure
	// should route to m.emitLogChunk with processID="p1".
	opts := ctrl.Options() // helper added below
	require.NotNil(t, opts.OnLogChunk, "newController must set OnLogChunk")
	opts.OnLogChunk(0, []byte("from p1"))

	require.Equal(t, []byte("from p1"), m.LogsSnapshot("p1", 0))
}
```

This test references a helper `Options()` that you will add on `*controller.Controller` in the next step — it simply returns the stored `ControllerOptions` so tests can inspect them.

- [ ] **Step 5.2: Add `Options` accessor on Controller**

Open `internal/controller/controller.go`. Find the `State()` method and add immediately after it:

```go
// State returns the current state.
func (c *Controller) State() cwtypes.State {
	return cwtypes.State(c.state.Load())
}

// Options returns the options the controller was constructed with.
// Exposed for test inspection; production code should not rely on this.
func (c *Controller) Options() ControllerOptions {
	return c.opts
}
```

- [ ] **Step 5.3: Run the test to verify it fails**

```bash
go test ./pkg/cliwrap -run TestManager_NewControllerInjectsOnLogChunk -v
```

Expected: `FAIL` with `opts.OnLogChunk` being nil — because `newController` does not set it yet.

- [ ] **Step 5.4: Modify newController to inject the callback**

Open `pkg/cliwrap/manager.go`. Find:

```go
func (m *Manager) newController(spec Spec) (*controller.Controller, error) {
	return controller.NewController(controller.ControllerOptions{
		Spec:       spec,
		Spawner:    m.spawner,
		RuntimeDir: m.runtimeDir,
	})
}
```

Replace with:

```go
func (m *Manager) newController(spec Spec) (*controller.Controller, error) {
	id := spec.ID
	return controller.NewController(controller.ControllerOptions{
		Spec:       spec,
		Spawner:    m.spawner,
		RuntimeDir: m.runtimeDir,
		OnLogChunk: func(stream uint8, data []byte) {
			m.emitLogChunk(id, stream, data)
		},
	})
}
```

The `id := spec.ID` local variable is pedantic but deliberate: it makes the closure capture a small string rather than the whole `Spec` value, which avoids keeping the spec alive past its natural lifetime.

- [ ] **Step 5.5: Run the tests to verify they pass**

```bash
go test ./pkg/cliwrap -run TestManager_NewControllerInjectsOnLogChunk -v
go test -race -count=1 ./pkg/cliwrap/... ./internal/controller/...
```

Expected: both runs pass.

- [ ] **Step 5.6: Commit**

```bash
git add pkg/cliwrap/manager.go pkg/cliwrap/manager_logs_test.go internal/controller/controller.go
git commit -s -m "feat(cliwrap): route controller log chunks into Manager collector

Update Manager.newController to install an OnLogChunk closure on the
ControllerOptions it passes into controller.NewController. The closure
captures the spec.ID up front (as a local string) so the controller
does not pin the full Spec value in memory, and forwards every chunk
to Manager.emitLogChunk with the process id.

With this wire in place, child stdout/stderr bytes now flow:

  child process
    ↓ (os.Pipe)
  Runner.StdoutSink / StderrSink (chunkWriter)
    ↓ (MsgLogChunk IPC frame)
  controller.handleMessage
    ↓ (OnLogChunk callback)
  Manager.emitLogChunk
    ↓ (logcollect.Collector.Write)
  RingBufferSink

Also add a small Controller.Options() accessor used only by unit
tests to verify the callback wiring."
```

---

## Task 6: Mgmt ManagerAPI extension + server MsgLogsRequest handler

Extend the `mgmt.ManagerAPI` interface with a `LogsSnapshot` method and handle `MsgLogsRequest` in the server by returning a single `MsgLogsStream` frame with `EOF=true`.

**Files:**
- Modify: `internal/mgmt/server.go`
- Modify: `internal/mgmt/server_test.go`
- Modify: `pkg/cliwrap/mgmt_api.go`

- [ ] **Step 6.1: Write the failing test**

Open `internal/mgmt/server_test.go`. Locate the `fakeManager` struct and extend it:

```go
// Replace the existing fakeManager with this expanded version.
type fakeManager struct {
	logs map[string][]byte // key = id + "/" + stream
}

func (f *fakeManager) List() []ListEntry {
	return []ListEntry{{ID: "p1", State: "running", ChildPID: 42}}
}
func (f *fakeManager) StatusOf(id string) (ListEntry, error) {
	if id != "p1" {
		return ListEntry{}, errNotFound
	}
	return ListEntry{ID: "p1", State: "running", ChildPID: 42}, nil
}
func (f *fakeManager) Stop(ctx context.Context, id string) error { return nil }

func (f *fakeManager) LogsSnapshot(id string, stream uint8) []byte {
	if f.logs == nil {
		return nil
	}
	return f.logs[id+"/"+string(rune('0'+stream))]
}
```

Then add a new round-trip test at the bottom of the file:

```go
func TestServer_LogsRequestRoundTrip(t *testing.T) {
	defer goleak.VerifyNone(t)

	fm := &fakeManager{
		logs: map[string][]byte{
			"p1/0": []byte("out1\nout2\n"),
			"p1/1": []byte("err1\n"),
		},
	}

	sockPath := filepath.Join(t.TempDir(), "mgr.sock")
	srv, err := NewServer(ServerOptions{
		SocketPath: sockPath,
		Manager:    fm,
		SpillerDir: t.TempDir(),
	})
	require.NoError(t, err)
	require.NoError(t, srv.Start())
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Close(ctx)
	}()

	cli, err := Dial(sockPath)
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()

	// Request stdout for p1.
	h, body, err := cli.Call(MsgLogsRequest, LogsRequestPayload{ID: "p1", Stream: 0})
	require.NoError(t, err)
	require.Equal(t, MsgLogsStream, h.MsgType)

	var resp LogsStreamPayload
	require.NoError(t, ipc.DecodePayload(body, &resp))
	require.Equal(t, "p1", resp.ID)
	require.Equal(t, uint8(0), resp.Stream)
	require.Equal(t, []byte("out1\nout2\n"), resp.Data)
	require.True(t, resp.EOF)
}
```

- [ ] **Step 6.2: Run the test to verify it fails**

```bash
go test ./internal/mgmt -run TestServer_LogsRequest -v
```

Expected: `FAIL` with compile errors: `fakeManager does not implement ManagerAPI (missing LogsSnapshot method)` — because the interface hasn't been extended yet. Also the request handler doesn't exist yet.

- [ ] **Step 6.3: Extend the ManagerAPI interface and add the handler**

Open `internal/mgmt/server.go`. Find:

```go
// ManagerAPI is the subset of Manager functionality the server exposes.
// Using an interface keeps tests decoupled from the concrete Manager.
type ManagerAPI interface {
	List() []ListEntry
	StatusOf(id string) (ListEntry, error)
	Stop(ctx context.Context, id string) error
}
```

Add `LogsSnapshot`:

```go
// ManagerAPI is the subset of Manager functionality the server exposes.
// Using an interface keeps tests decoupled from the concrete Manager.
type ManagerAPI interface {
	List() []ListEntry
	StatusOf(id string) (ListEntry, error)
	Stop(ctx context.Context, id string) error
	LogsSnapshot(id string, stream uint8) []byte
}
```

Then find `handleRequest` and add a new case before the `default`:

```go
	case MsgStopRequest:
		var req StopRequestPayload
		_ = ipc.DecodePayload(body, &req)
		err := s.opts.Manager.Stop(ctx, req.ID)
		resp := StatusResponsePayload{}
		if err != nil {
			resp.Err = err.Error()
		}
		return resp, MsgStatusResponse, nil
	case MsgLogsRequest:
		var req LogsRequestPayload
		_ = ipc.DecodePayload(body, &req)
		data := s.opts.Manager.LogsSnapshot(req.ID, req.Stream)
		resp := LogsStreamPayload{
			ID:     req.ID,
			Stream: req.Stream,
			Data:   data,
			EOF:    true,
		}
		return resp, MsgLogsStream, nil
	default:
```

- [ ] **Step 6.4: Implement the real LogsSnapshot on Manager's mgmt API**

Open `pkg/cliwrap/mgmt_api.go`. Add this method after the existing `Stop` method:

```go
// LogsSnapshot is the mgmt API entry that returns the current ring-buffer
// contents for a (process, stream) pair. Returns nil if no logs have been
// recorded for that pair yet.
func (m *Manager) LogsSnapshot(id string, stream uint8) []byte {
	// Thin wrapper — the real implementation lives in manager_logs.go
	// where the Collector access is co-located with the write path.
	return m.logsSnapshotImpl(id, stream)
}
```

Now open `pkg/cliwrap/manager_logs.go` and rename the existing `LogsSnapshot` method to `logsSnapshotImpl`:

```go
// logsSnapshotImpl is the internal backing implementation used by the
// mgmt API wrapper in mgmt_api.go.
func (m *Manager) logsSnapshotImpl(processID string, stream uint8) []byte {
	return m.ensureCollector().Snapshot(processID, stream)
}
```

Also update the test in `pkg/cliwrap/manager_logs_test.go` — the test calls `m.LogsSnapshot(...)` which still exists (we only renamed the internal). Verify by running the test.

- [ ] **Step 6.5: Run the tests to verify they pass**

```bash
go test ./internal/mgmt -run TestServer_LogsRequest -v
go test ./pkg/cliwrap -run TestManager_LogsSnapshot -v
go build ./...
```

Expected: all pass; build clean.

- [ ] **Step 6.6: Run the full mgmt + cliwrap suites for regressions**

```bash
go test -race -count=1 ./internal/mgmt/... ./pkg/cliwrap/...
```

Expected: all green.

- [ ] **Step 6.7: Commit**

```bash
git add internal/mgmt/server.go internal/mgmt/server_test.go pkg/cliwrap/mgmt_api.go pkg/cliwrap/manager_logs.go
git commit -s -m "feat(mgmt): handle MsgLogsRequest returning ring-buffer snapshot

Extend the ManagerAPI interface with a LogsSnapshot method and plumb
it through the mgmt server. On receiving an MsgLogsRequest frame, the
server now calls Manager.LogsSnapshot(id, stream) and returns exactly
one MsgLogsStream response frame carrying the full ring-buffer
contents with EOF=true.

The single-frame response shape is forward-compatible with a future
follow mode that would emit multiple MsgLogsStream frames ending with
EOF=true. For the snapshot MVP, EOF is always set on the first frame.

The Manager.LogsSnapshot method in pkg/cliwrap/mgmt_api.go is a thin
wrapper delegating to logsSnapshotImpl in manager_logs.go, keeping all
Collector access co-located in a single file."
```

---

## Task 7: Implement `cliwrap logs` CLI client

Replace the `cmd_logs.go` stub with a full command that parses args, dials the mgmt socket, sends `MsgLogsRequest` for the requested stream(s), and prints the bytes.

**Files:**
- Modify: `cmd/cliwrap/cmd_logs.go` (full replacement)

- [ ] **Step 7.1: Write the replacement**

Replace the entire contents of `cmd/cliwrap/cmd_logs.go` with:

```go
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/0xmhha/cli-wrapper/internal/ipc"
	"github.com/0xmhha/cli-wrapper/internal/mgmt"
)

func logsCommand(args []string) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	runtimeDir := fs.String("runtime-dir", "", "override runtime directory")
	stream := fs.String("stream", "all", "which stream to show: stdout | stderr | all")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: cliwrap logs [--stream stdout|stderr|all] <id>")
		return 2
	}
	id := fs.Arg(0)

	// Resolve which streams to request.
	streams, err := parseStreamFlag(*stream)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap logs: %v\n", err)
		return 2
	}

	sock := managerSocketPath(*runtimeDir)
	cli, err := mgmt.Dial(sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap logs: %v\n", err)
		return 1
	}
	defer func() { _ = cli.Close() }()

	for _, s := range streams {
		if err := printOneStream(cli, id, s); err != nil {
			fmt.Fprintf(os.Stderr, "cliwrap logs: %v\n", err)
			return 1
		}
	}
	return 0
}

// parseStreamFlag converts the --stream flag value into one or two
// stream ids to query. "all" returns [0, 1] (stdout then stderr).
func parseStreamFlag(v string) ([]uint8, error) {
	switch v {
	case "stdout":
		return []uint8{0}, nil
	case "stderr":
		return []uint8{1}, nil
	case "all", "":
		return []uint8{0, 1}, nil
	default:
		return nil, fmt.Errorf("unknown --stream value %q (want stdout, stderr, or all)", v)
	}
}

// printOneStream issues a single MsgLogsRequest and writes the
// returned bytes to stdout (for stream 0) or stderr (for stream 1).
// The stderr routing matches user expectation: `cliwrap logs p1 2>/dev/null`
// should hide the stderr side of the supervised child.
func printOneStream(cli *mgmt.Client, id string, stream uint8) error {
	_, body, err := cli.Call(mgmt.MsgLogsRequest, mgmt.LogsRequestPayload{
		ID:     id,
		Stream: stream,
	})
	if err != nil {
		return fmt.Errorf("call: %w", err)
	}
	var resp mgmt.LogsStreamPayload
	if err := ipc.DecodePayload(body, &resp); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil
	}
	var sink *os.File
	if stream == 1 {
		sink = os.Stderr
	} else {
		sink = os.Stdout
	}
	if _, err := sink.Write(resp.Data); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}
```

- [ ] **Step 7.2: Build and spot-check manually**

```bash
go build ./...
```

Expected: clean.

```bash
go run ./cmd/cliwrap logs
```

Expected output (on stderr): `usage: cliwrap logs [--stream stdout|stderr|all] <id>` and exit 2. This confirms the argparse path.

```bash
go run ./cmd/cliwrap logs --stream foo p1
```

Expected: `cliwrap logs: unknown --stream value "foo" (want stdout, stderr, or all)` on stderr, exit 2.

- [ ] **Step 7.3: Commit**

```bash
git add cmd/cliwrap/cmd_logs.go
git commit -s -m "feat(cli): implement 'cliwrap logs' snapshot command

Replace the 'streaming not yet implemented' stub in cmd_logs.go with
a working snapshot client. The command parses flags (--runtime-dir,
--stream stdout|stderr|all), validates the stream selector, dials the
management socket, and issues one MsgLogsRequest per requested stream.

Response bytes are written to the host's stdout for stream=0 and to
the host's stderr for stream=1, so shell redirection (`cliwrap logs
p1 2>/dev/null`) correctly hides the supervised child's stderr side.

The --stream default is 'all', which makes two sequential mgmt calls
and prints stdout first, then stderr. This is snapshot-only; --follow
is deliberately out of scope for this MVP and will be a follow-up."
```

---

## Task 8: Integration test — spawn fixture-noisy, read logs end-to-end

Confirm the full pipeline works by spawning the existing `fixture-noisy` binary (which writes `out N` / `err N` every 100 ms) and calling `Manager.LogsSnapshot` after a short settling period.

**Files:**
- Create: `test/integration/logs_test.go`

- [ ] **Step 8.1: Write the integration test**

```go
// test/integration/logs_test.go
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// TestIntegration_LogsSnapshotCapturesChildOutput confirms that child
// stdout and stderr bytes flow end-to-end through the agent → controller
// → manager pipeline and become readable via Manager.LogsSnapshot.
func TestIntegration_LogsSnapshotCapturesChildOutput(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)
	root := findRoot(t)
	noisy := filepath.Join(root, "test/fixtures/bin/fixture-noisy")

	mgr, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	spec, err := cliwrap.NewSpec("noisy", noisy).
		WithStopTimeout(2 * time.Second).
		Build()
	require.NoError(t, err)

	h, err := mgr.Register(spec)
	require.NoError(t, err)
	require.NoError(t, h.Start(ctx))

	// fixture-noisy writes "out N" / "err N" every 100 ms. Wait until
	// we can observe both streams in the ring buffer (bounded by 5 s).
	require.Eventually(t, func() bool {
		stdout := mgr.LogsSnapshot("noisy", 0)
		stderr := mgr.LogsSnapshot("noisy", 1)
		return bytes.Contains(stdout, []byte("out 0")) &&
			bytes.Contains(stderr, []byte("err 0"))
	}, 5*time.Second, 100*time.Millisecond,
		"expected fixture-noisy output to appear in log ring buffer within 5 s")

	// Snapshot both streams and assert sanity:
	// stdout should contain "out " prefix lines, stderr should contain "err ".
	stdout := mgr.LogsSnapshot("noisy", 0)
	stderr := mgr.LogsSnapshot("noisy", 1)

	require.Contains(t, string(stdout), "out 0")
	require.NotContains(t, string(stdout), "err ", "stdout stream must not contain stderr lines")

	require.Contains(t, string(stderr), "err 0")
	require.NotContains(t, string(stderr), "out ", "stderr stream must not contain stdout lines")

	require.NoError(t, mgr.Shutdown(ctx))
}
```

The test uses two existing helpers from the integration package:
- `supervise.BuildAgentForTest(t)` — rebuilds `cliwrap-agent` to a temp path
- `findRoot(t)` — locates the repo root to resolve the fixture binary path

Both are already imported by `test/integration/crash_test.go`, so this new file needs no additional setup.

- [ ] **Step 8.2: Ensure the fixture binary is built**

```bash
make fixtures
```

Expected: rebuilds `test/fixtures/bin/fixture-{echo,noisy,crasher,hanger}`.

- [ ] **Step 8.3: Run the new integration test**

```bash
go test -race -count=1 -v -run TestIntegration_LogsSnapshotCapturesChildOutput ./test/integration/
```

Expected: `PASS` within ~1–2 seconds (fixture-noisy produces output at 10 Hz, so "out 0" / "err 0" should arrive in the first ~200 ms).

- [ ] **Step 8.4: Run the full integration suite for regressions**

```bash
make integration
```

Expected: all integration tests pass, including the pre-existing `TestIntegration_CrashDetection`, `TestIntegration_Heartbeat*`, `TestIntegration_Restart*`, `TestIntegration_Shutdown*`, and the new `TestIntegration_LogsSnapshotCapturesChildOutput`.

- [ ] **Step 8.5: Commit**

```bash
git add test/integration/logs_test.go
git commit -s -m "test(integration): verify end-to-end log snapshot capture

Add a new integration test that spawns fixture-noisy (writes 'out N'
and 'err N' every 100 ms to stdout and stderr respectively) under the
full Manager stack and asserts that Manager.LogsSnapshot returns the
expected content for both streams within a 5 s budget.

This exercises the full pipeline end-to-end for the first time:

  fixture-noisy (child)
    → Runner stdout/stderr pipe
    → chunkWriter (MsgLogChunk frames)
    → ipc.Conn outbox → agent socket
    → host controller handleMessage → OnLogChunk callback
    → Manager.emitLogChunk → Collector.Write
    → RingBufferSink
    → Manager.LogsSnapshot readback

The test also asserts stream isolation: stdout snapshot must not
contain any 'err ' lines, and stderr snapshot must not contain any
'out ' lines, guarding against a stream-id mixup anywhere in the
chain."
```

---

## Task 9: CHANGELOG entry + help text update

Move `cliwrap logs` out of "Known Limitations" and into the `[Unreleased]` `Added` section of `CHANGELOG.md`, and update the `cliwrap` help output to reflect the new snapshot capability.

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `cmd/cliwrap/main.go` (help text)

- [ ] **Step 9.1: Update CHANGELOG**

Open `CHANGELOG.md`. At the top of the file, add a new `[Unreleased]` section (if one does not already exist from prior work):

```markdown
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

## [0.1.1] - 2026-04-08
```

Then find the existing "Known Limitations" section under v0.1.0 (or wherever it lives) and remove the `cliwrap logs` bullet. If the section becomes empty for a version, leave the remaining bullets alone but drop the logs one.

- [ ] **Step 9.2: Update `cliwrap` help text**

Open `cmd/cliwrap/main.go`. Find:

```go
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: cliwrap <command> [args...]")
		fmt.Fprintln(os.Stderr, "commands: run, validate, list, status, stop, logs, events, version")
		os.Exit(2)
	}
```

The existing help line already mentions `logs`, so no change needed here. But also find:

```go
	case "-h", "--help", "help":
		fmt.Println("cliwrap: manage background CLI processes")
		exit = 0
```

Replace with a slightly more detailed help output so users discover the `logs` subcommand:

```go
	case "-h", "--help", "help":
		fmt.Println("cliwrap: manage background CLI processes")
		fmt.Println("")
		fmt.Println("Commands:")
		fmt.Println("  run        -f config.yaml      start all processes in a config")
		fmt.Println("  validate   -f config.yaml      check a config without running")
		fmt.Println("  list                           list supervised processes")
		fmt.Println("  status     <id>                show one process's status")
		fmt.Println("  stop       <id>                ask a process to stop")
		fmt.Println("  logs       [--stream s] <id>   show a process's log snapshot")
		fmt.Println("  events                         stream events (not yet implemented)")
		fmt.Println("  version                        print the cliwrap version")
		exit = 0
```

- [ ] **Step 9.3: Build and verify help text**

```bash
go build -o /tmp/cliwrap ./cmd/cliwrap
/tmp/cliwrap help
rm /tmp/cliwrap
```

Expected: the new multi-line help output lists `logs` with its flag syntax.

- [ ] **Step 9.4: Commit**

```bash
git add CHANGELOG.md cmd/cliwrap/main.go
git commit -s -m "docs: document cliwrap logs snapshot in CHANGELOG and help

Move the 'cliwrap logs streaming is not yet implemented' bullet out
of the Known Limitations list and into the [Unreleased] Added section
of CHANGELOG.md. The snapshot MVP is now working; --follow live tail
is still deferred and is documented as such.

Also expand the 'cliwrap help' output from a single line into a
per-subcommand summary so first-time users can discover the logs
flag syntax without reading the source."
```

---

## Final Verification

After all tasks land, run the full quality gates one more time:

- [ ] **Final step 1: Full unit test suite**

```bash
go test -race -count=1 ./...
```

Expected: every package green, including `internal/agent`, `internal/controller`, `pkg/cliwrap`, `internal/mgmt`.

- [ ] **Final step 2: Lint**

```bash
golangci-lint run ./...
```

Expected: clean. (Note: requires golangci-lint ≥ v2.1.0 locally due to the v2 config schema.)

- [ ] **Final step 3: Integration + chaos**

```bash
make integration
make chaos
```

Expected: all green.

- [ ] **Final step 4: Full vet**

```bash
go vet ./...
```

Expected: no output.

- [ ] **Final step 5: Manual smoke test (optional but recommended)**

Build binaries and exercise the feature manually:

```bash
make fixtures
go build -o /tmp/cliwrap ./cmd/cliwrap
go build -o /tmp/cliwrap-agent ./cmd/cliwrap-agent
```

Create a minimal config:

```bash
cat > /tmp/cliwrap-logs-smoke.yaml <<'EOF'
version: "1"
runtime:
  dir: /tmp/cliwrap-smoke
groups:
  - name: demo
    processes:
      - id: noisy
        command: $PWD/test/fixtures/bin/fixture-noisy
EOF
```

Run and query:

```bash
CLIWRAP_AGENT=/tmp/cliwrap-agent /tmp/cliwrap run -f /tmp/cliwrap-logs-smoke.yaml &
RUN_PID=$!
sleep 1
CLIWRAP_RUNTIME_DIR=/tmp/cliwrap-smoke /tmp/cliwrap logs noisy --stream stdout | head -5
kill $RUN_PID
wait $RUN_PID 2>/dev/null
rm -rf /tmp/cliwrap /tmp/cliwrap-agent /tmp/cliwrap-smoke /tmp/cliwrap-logs-smoke.yaml
```

Expected: lines like `out 0`, `out 1`, ... appear on stdout.

---

## Post-Merge Follow-ups (Not in This Plan)

- `cliwrap logs --follow` / `-f` live tailing (requires a persistent mgmt connection and server-push streaming of multiple `MsgLogsStream` frames)
- `cliwrap events` implementation (reuses the same streaming pattern, but subscribes to `pkg/event.Bus`)
- `--since <duration>` and `--lines N` filters (requires the ring buffer to track line boundaries or timestamps)
- Rotator / file-backed log persistence (the `logcollect.Rotator` type exists but is currently unused)
- Configurable ring buffer size (currently hardcoded at 1 MiB per stream in `manager_logs.go`)
