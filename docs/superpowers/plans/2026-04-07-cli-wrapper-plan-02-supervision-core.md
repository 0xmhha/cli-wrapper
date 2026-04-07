# cli-wrapper Plan 02 — Process Supervision Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the process supervision core: the `cliwrap-agent` binary, the host-side controller, the event bus, log collection pipeline, and the Manager public API so that a host application can start, observe, and stop child CLI processes with guaranteed crash detection and restart behavior.

**Architecture:** The Manager in the host process spawns `cliwrap-agent` subprocesses over a `socketpair`. Each agent forks the real CLI as its child, captures stdout/stderr, and relays structured messages over the IPC layer built in Plan 01. A Controller object wraps each agent connection, runs reader/writer/heartbeat/watcher goroutines, and feeds state transitions to the Registry via the EventBus. Log payloads flow into per-process ring buffers and rotated log files.

**Tech Stack:**
- Go 1.22+
- Builds on `internal/ipc` from Plan 01 (Header, Conn, Spiller, MsgType, etc.)
- Standard library only for process, signals, file IO
- `go.uber.org/goleak` for leak detection in every test
- `golang.org/x/sync/errgroup` for bulk shutdown

**Context for the implementer:**
- The IPC layer is complete and exported from `github.com/cli-wrapper/cli-wrapper/internal/ipc`.
- The spec's single source of truth is `docs/superpowers/specs/2026-04-07-cli-wrapper-design.md`; when anything here is ambiguous, defer to the spec.
- Strict TDD: failing test first, then implementation.
- Every long-lived goroutine must honor a `context.Context`; every test must call `goleak.VerifyNone(t)`.
- Commits follow conventional commits.

---

## File Structure

| File | Responsibility |
|------|----------------|
| `internal/platform/platform.go` | OS abstraction interface (FindExecutable, SendSignal, Kill) |
| `internal/platform/platform_linux.go` | Linux implementation |
| `internal/platform/platform_darwin.go` | macOS implementation |
| `pkg/event/event.go` | Event interface, Type enum, baseEvent, concrete structs |
| `pkg/event/bus.go` | Bus interface, Subscription, Filter |
| `internal/eventbus/bus.go` | In-memory dispatcher with slow-subscriber policy |
| `internal/logcollect/ringbuf.go` | Per-stream ring buffer with Snapshot/Tail/Follow |
| `internal/logcollect/rotator.go` | File rotator with size-based rotation |
| `internal/logcollect/sink.go` | Sink interface + RingBufferSink + FileSink |
| `internal/logcollect/collector.go` | LogCollector: wires incoming ipc.LogChunkPayload to sinks |
| `internal/cwtypes/types.go` | Shared types: State, Status, Spec, RestartPolicy, ResourceLimits, etc. (leaf package) |
| `pkg/cliwrap/types.go` | Re-exports cwtypes via type aliases for public API stability |
| `pkg/cliwrap/errors.go` | Sentinel + structured errors |
| `pkg/cliwrap/process.go` | ProcessHandle interface + implementation |
| `pkg/cliwrap/options.go` | Functional options for NewManager |
| `pkg/cliwrap/spec_builder.go` | SpecBuilder fluent API |
| `pkg/cliwrap/manager.go` | Manager struct + NewManager + public methods |
| `internal/registry/registry.go` | Process registry map protected by RWMutex |
| `internal/supervise/spawner.go` | Agent spawner: socketpair + exec |
| `internal/supervise/watcher.go` | OS process wait watcher |
| `internal/supervise/restart.go` | RestartPolicy + ExponentialBackoff |
| `internal/controller/controller.go` | Controller struct + lifecycle |
| `internal/controller/crash.go` | 4-layer crash detection + CrashInfo |
| `internal/controller/heartbeat.go` | Heartbeat goroutine |
| `internal/agent/main.go` | Agent entry point logic (called from cmd/cliwrap-agent) |
| `internal/agent/dispatcher.go` | Agent side message routing |
| `internal/agent/runner.go` | Child fork/exec + waitpid |
| `cmd/cliwrap-agent/main.go` | Agent binary entrypoint |
| `test/fixtures/echo/main.go` | fixture-echo |
| `test/fixtures/noisy/main.go` | fixture-noisy |
| `test/fixtures/crasher/main.go` | fixture-crasher |
| `test/fixtures/hanger/main.go` | fixture-hanger |
| `test/integration/lifecycle_test.go` | End-to-end integration tests |

---

## Task 1: Platform Abstraction

**Files:**
- Create: `internal/platform/platform.go`
- Create: `internal/platform/platform_linux.go`
- Create: `internal/platform/platform_darwin.go`
- Create: `internal/platform/platform_test.go`

- [x] **Step 1: Write the failing test**

Create `internal/platform/platform_test.go`:

```go
package platform

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFindExecutable_LocatesSystemBinary(t *testing.T) {
	path, err := Default().FindExecutable("sh")
	require.NoError(t, err)
	require.NotEmpty(t, path)
}

func TestFindExecutable_ReturnsErrorForMissing(t *testing.T) {
	_, err := Default().FindExecutable("this-binary-does-not-exist-xyzzy")
	require.Error(t, err)
}

func TestSendSignalAndKill(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	require.NoError(t, cmd.Start())
	defer cmd.Wait()

	time.Sleep(50 * time.Millisecond)
	require.NoError(t, Default().SendSignal(cmd.Process.Pid, syscall.SIGTERM))

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = Default().Kill(cmd.Process.Pid)
		t.Fatal("process did not exit after SIGTERM")
	}
	_ = os.Getpid() // keep os import used
}
```

- [x] **Step 2: Run the test and confirm it fails to compile**

Run:

```bash
go test ./internal/platform/ -count=1
```

Expected: `undefined: Default`.

- [x] **Step 3: Implement the interface**

Create `internal/platform/platform.go`:

```go
package platform

import (
	"os"
	"os/exec"
)

// Platform abstracts OS-specific operations so the rest of the code base
// never imports syscall or os directly for process management.
type Platform interface {
	// FindExecutable locates cmd using $PATH semantics.
	FindExecutable(cmd string) (string, error)

	// SendSignal delivers sig to pid. Returns an error if the process
	// does not exist or the caller lacks permission.
	SendSignal(pid int, sig os.Signal) error

	// Kill is shorthand for SendSignal(pid, SIGKILL).
	Kill(pid int) error
}

// Default returns the Platform implementation for the current OS.
func Default() Platform { return defaultImpl }

type basePlatform struct{}

func (basePlatform) FindExecutable(cmd string) (string, error) {
	return exec.LookPath(cmd)
}

func (basePlatform) SendSignal(pid int, sig os.Signal) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(sig)
}

func (p basePlatform) Kill(pid int) error {
	return p.SendSignal(pid, os.Kill)
}
```

- [x] **Step 4: Create Linux specialization**

Create `internal/platform/platform_linux.go`:

```go
//go:build linux

package platform

var defaultImpl Platform = basePlatform{}
```

- [x] **Step 5: Create Darwin specialization**

Create `internal/platform/platform_darwin.go`:

```go
//go:build darwin

package platform

var defaultImpl Platform = basePlatform{}
```

- [x] **Step 6: Run the tests to verify they pass**

Run:

```bash
go test ./internal/platform/ -count=1 -race -v
```

Expected: three tests pass.

- [x] **Step 7: Commit**

```bash
git add internal/platform/
git commit -m "feat(platform): add OS abstraction for executable lookup and signaling"
```

---

## Task 2: Event Types

**Files:**
- Create: `pkg/event/event.go`
- Create: `pkg/event/event_test.go`

- [x] **Step 1: Write the failing test**

Create `pkg/event/event_test.go`:

```go
package event

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEventInterface_Implementations(t *testing.T) {
	now := time.Now()

	events := []Event{
		NewProcessStarted("p1", now, 100, 200),
		NewProcessStopped("p1", now, 0),
		NewProcessCrashed("p1", now, CrashContext{Reason: "oom"}, true, 2*time.Second),
		NewAgentFatal("p1", now, "panic in reader"),
		NewLogOverflow("p1", now, "rotator full"),
		NewBackpressureStall("p1", now, "host", 1024, 2048, 3),
	}
	for _, e := range events {
		require.Equal(t, "p1", e.ProcessID())
		require.NotEmpty(t, string(e.EventType()))
		require.False(t, e.Timestamp().IsZero())
	}
}
```

- [x] **Step 2: Run the test and confirm it fails**

Run:

```bash
go test ./pkg/event/ -count=1
```

Expected: undefined types.

- [x] **Step 3: Implement event types**

Create `pkg/event/event.go`:

```go
package event

import "time"

// Type identifies an event kind.
type Type string

// Defined event types.
const (
	TypeProcessRegistered Type = "process.registered"
	TypeProcessStarting   Type = "process.starting"
	TypeProcessStarted    Type = "process.started"
	TypeProcessStopping   Type = "process.stopping"
	TypeProcessStopped    Type = "process.stopped"
	TypeProcessCrashed    Type = "process.crashed"
	TypeProcessRestarting Type = "process.restarting"
	TypeProcessFailed     Type = "process.failed"

	TypeAgentSpawned      Type = "agent.spawned"
	TypeAgentFatal        Type = "agent.fatal"
	TypeAgentDisconnected Type = "agent.disconnected"

	TypeLogChunk    Type = "log.chunk"
	TypeLogFileRef  Type = "log.file_ref"
	TypeLogOverflow Type = "log.overflow"

	TypeBackpressureStall Type = "reliability.backpressure_stall"

	TypeShutdownRequested  Type = "system.shutdown_requested"
	TypeShutdownIncomplete Type = "system.shutdown_incomplete"
)

// Event is implemented by every concrete event type in this package.
// External packages can only consume events; they cannot implement Event.
type Event interface {
	EventType() Type
	ProcessID() string
	Timestamp() time.Time
	sealed()
}

type baseEvent struct {
	processID string
	ts        time.Time
}

func (e baseEvent) ProcessID() string    { return e.processID }
func (e baseEvent) Timestamp() time.Time { return e.ts }
func (baseEvent) sealed()                 {}

// --- Process lifecycle events ---

type ProcessStartedEvent struct {
	baseEvent
	AgentPID int
	ChildPID int
}

func (ProcessStartedEvent) EventType() Type { return TypeProcessStarted }

func NewProcessStarted(id string, at time.Time, agentPID, childPID int) ProcessStartedEvent {
	return ProcessStartedEvent{baseEvent: baseEvent{id, at}, AgentPID: agentPID, ChildPID: childPID}
}

type ProcessStoppedEvent struct {
	baseEvent
	ExitCode int
}

func (ProcessStoppedEvent) EventType() Type { return TypeProcessStopped }

func NewProcessStopped(id string, at time.Time, exitCode int) ProcessStoppedEvent {
	return ProcessStoppedEvent{baseEvent: baseEvent{id, at}, ExitCode: exitCode}
}

// CrashContext summarizes the unified crash information reported to
// subscribers. It mirrors controller.CrashInfo without creating an
// import cycle.
type CrashContext struct {
	Source     string
	AgentExit  int
	AgentSig   int
	ChildExit  int
	ChildSig   int
	Reason     string
}

type ProcessCrashedEvent struct {
	baseEvent
	CrashContext  CrashContext
	WillRestart   bool
	NextAttemptIn time.Duration
}

func (ProcessCrashedEvent) EventType() Type { return TypeProcessCrashed }

func NewProcessCrashed(id string, at time.Time, c CrashContext, willRestart bool, backoff time.Duration) ProcessCrashedEvent {
	return ProcessCrashedEvent{
		baseEvent:     baseEvent{id, at},
		CrashContext:  c,
		WillRestart:   willRestart,
		NextAttemptIn: backoff,
	}
}

// --- Agent events ---

type AgentFatalEvent struct {
	baseEvent
	Reason string
}

func (AgentFatalEvent) EventType() Type { return TypeAgentFatal }

func NewAgentFatal(id string, at time.Time, reason string) AgentFatalEvent {
	return AgentFatalEvent{baseEvent: baseEvent{id, at}, Reason: reason}
}

// --- Log events ---

type LogChunkEvent struct {
	baseEvent
	Stream uint8
	Size   int
}

func (LogChunkEvent) EventType() Type { return TypeLogChunk }

func NewLogChunk(id string, at time.Time, stream uint8, size int) LogChunkEvent {
	return LogChunkEvent{baseEvent: baseEvent{id, at}, Stream: stream, Size: size}
}

type LogOverflowEvent struct {
	baseEvent
	Reason string
}

func (LogOverflowEvent) EventType() Type { return TypeLogOverflow }

func NewLogOverflow(id string, at time.Time, reason string) LogOverflowEvent {
	return LogOverflowEvent{baseEvent: baseEvent{id, at}, Reason: reason}
}

// --- Reliability events ---

type BackpressureStallEvent struct {
	baseEvent
	Endpoint    string
	WALSize     uint64
	WALSizeMax  uint64
	DroppedMsgs int
}

func (BackpressureStallEvent) EventType() Type { return TypeBackpressureStall }

func NewBackpressureStall(id string, at time.Time, endpoint string, size, max uint64, dropped int) BackpressureStallEvent {
	return BackpressureStallEvent{
		baseEvent:   baseEvent{id, at},
		Endpoint:    endpoint,
		WALSize:     size,
		WALSizeMax:  max,
		DroppedMsgs: dropped,
	}
}
```

- [x] **Step 4: Run the tests**

Run:

```bash
go test ./pkg/event/ -count=1 -race -v
```

Expected: `TestEventInterface_Implementations` passes.

- [x] **Step 5: Commit**

```bash
git add pkg/event/
git commit -m "feat(event): add event types with sealed interface pattern"
```

---

## Task 3: EventBus Interface

**Files:**
- Create: `pkg/event/bus.go`
- Create: `pkg/event/bus_test.go`

- [x] **Step 1: Write the failing interface contract test**

Create `pkg/event/bus_test.go`:

```go
package event

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A minimal in-package fake, used only to test the types & contract.
type fakeBus struct{ published []Event }

func (f *fakeBus) Publish(e Event) { f.published = append(f.published, e) }
func (f *fakeBus) Subscribe(filter Filter) Subscription {
	ch := make(chan Event, 1)
	return &fakeSub{ch: ch}
}

type fakeSub struct{ ch chan Event }

func (f *fakeSub) Events() <-chan Event { return f.ch }
func (f *fakeSub) Close() error          { close(f.ch); return nil }

func TestBusInterface_AcceptsFakeImplementation(t *testing.T) {
	var b Bus = &fakeBus{}
	b.Publish(NewProcessStopped("p1", time.Now(), 0))
	sub := b.Subscribe(Filter{})
	require.NoError(t, sub.Close())
}

func TestFilter_MatchesAllWhenEmpty(t *testing.T) {
	f := Filter{}
	require.True(t, f.Matches(NewProcessStopped("p1", time.Now(), 0)))
}

func TestFilter_MatchesByType(t *testing.T) {
	f := Filter{Types: []Type{TypeProcessStopped}}
	require.True(t, f.Matches(NewProcessStopped("p1", time.Now(), 0)))
	require.False(t, f.Matches(NewProcessStarted("p1", time.Now(), 1, 2)))
}

func TestFilter_MatchesByProcessID(t *testing.T) {
	f := Filter{ProcessIDs: []string{"p1"}}
	require.True(t, f.Matches(NewProcessStopped("p1", time.Now(), 0)))
	require.False(t, f.Matches(NewProcessStopped("p2", time.Now(), 0)))
}
```

- [x] **Step 2: Run the tests and confirm they fail**

Run:

```bash
go test ./pkg/event/ -run TestBus -count=1
```

Expected: undefined types.

- [x] **Step 3: Implement Bus, Filter, Subscription**

Create `pkg/event/bus.go`:

```go
package event

// Bus is the publish/subscribe API for cliwrap events.
//
// Publish is non-blocking. Subscribe returns a Subscription whose Events
// channel the caller must either drain or Close. Slow subscribers are
// disconnected after repeated drops by the underlying implementation.
type Bus interface {
	Publish(e Event)
	Subscribe(filter Filter) Subscription
}

// Subscription represents one active subscription. Close releases the
// underlying channel and goroutine references.
type Subscription interface {
	Events() <-chan Event
	Close() error
}

// Filter is applied by the bus to decide which events a subscriber
// receives. Empty slices mean "any" — a zero-value Filter matches all
// events.
type Filter struct {
	Types      []Type
	ProcessIDs []string
}

// Matches reports whether e satisfies the filter.
func (f Filter) Matches(e Event) bool {
	if len(f.Types) > 0 {
		found := false
		for _, t := range f.Types {
			if t == e.EventType() {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(f.ProcessIDs) > 0 {
		found := false
		for _, id := range f.ProcessIDs {
			if id == e.ProcessID() {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
```

- [x] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./pkg/event/ -count=1 -race -v
```

Expected: all four tests pass.

- [x] **Step 5: Commit**

```bash
git add pkg/event/bus.go pkg/event/bus_test.go
git commit -m "feat(event): add Bus interface and Filter matching"
```

---

## Task 4: EventBus Implementation

**Files:**
- Create: `internal/eventbus/bus.go`
- Create: `internal/eventbus/bus_test.go`

- [x] **Step 1: Write the failing tests**

Create `internal/eventbus/bus_test.go`:

```go
package eventbus

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/cli-wrapper/cli-wrapper/pkg/event"
)

func TestBus_PublishAndReceive(t *testing.T) {
	defer goleak.VerifyNone(t)

	b := New(64)
	defer b.Close()

	sub := b.Subscribe(event.Filter{})
	defer sub.Close()

	b.Publish(event.NewProcessStopped("p1", time.Now(), 0))

	select {
	case ev := <-sub.Events():
		require.Equal(t, event.TypeProcessStopped, ev.EventType())
	case <-time.After(time.Second):
		t.Fatal("event not received")
	}
}

func TestBus_FilterByType(t *testing.T) {
	defer goleak.VerifyNone(t)

	b := New(64)
	defer b.Close()

	sub := b.Subscribe(event.Filter{Types: []event.Type{event.TypeProcessCrashed}})
	defer sub.Close()

	b.Publish(event.NewProcessStopped("p1", time.Now(), 0)) // should be filtered
	b.Publish(event.NewProcessCrashed("p1", time.Now(), event.CrashContext{Reason: "x"}, false, 0))

	select {
	case ev := <-sub.Events():
		require.Equal(t, event.TypeProcessCrashed, ev.EventType())
	case <-time.After(time.Second):
		t.Fatal("event not received")
	}

	select {
	case ev := <-sub.Events():
		t.Fatalf("unexpected second event: %v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestBus_SlowSubscriberIsDropped(t *testing.T) {
	defer goleak.VerifyNone(t)

	b := NewWithPolicy(4, SlowPolicy{DropThreshold: 3})
	defer b.Close()

	sub := b.Subscribe(event.Filter{})
	// Do not drain.

	// Publish many events to exceed both channel capacity and drop threshold.
	for i := 0; i < 20; i++ {
		b.Publish(event.NewProcessStopped("p1", time.Now(), 0))
	}

	// Subscription should eventually close itself.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-sub.Events():
			if !ok {
				return // channel closed, success
			}
		case <-deadline:
			t.Fatal("slow subscriber was not disconnected")
		}
	}
}
```

- [x] **Step 2: Run the tests and confirm they fail**

Run:

```bash
go test ./internal/eventbus/ -count=1
```

Expected: `undefined: New`, etc.

- [x] **Step 3: Implement the bus**

Create `internal/eventbus/bus.go`:

```go
package eventbus

import (
	"sync"

	"github.com/cli-wrapper/cli-wrapper/pkg/event"
)

// SlowPolicy controls how the bus reacts to slow subscribers.
type SlowPolicy struct {
	// DropThreshold is the number of consecutive dropped events after
	// which the subscription is forcibly closed. Zero means "never close".
	DropThreshold int
}

// Bus is the default in-memory implementation of event.Bus.
// It is safe for concurrent use.
type Bus struct {
	mu     sync.RWMutex
	subs   map[*subscription]struct{}
	closed bool
	policy SlowPolicy
	chCap  int
}

// New returns a Bus with the default slow-subscriber policy.
// chCap is the per-subscription channel capacity.
func New(chCap int) *Bus {
	return NewWithPolicy(chCap, SlowPolicy{DropThreshold: 10})
}

// NewWithPolicy returns a Bus with the given policy.
func NewWithPolicy(chCap int, p SlowPolicy) *Bus {
	if chCap < 1 {
		chCap = 1
	}
	return &Bus{
		subs:   make(map[*subscription]struct{}),
		policy: p,
		chCap:  chCap,
	}
}

// Publish delivers e to every matching subscription. Non-blocking.
func (b *Bus) Publish(e event.Event) {
	b.mu.RLock()
	subs := make([]*subscription, 0, len(b.subs))
	for s := range b.subs {
		subs = append(subs, s)
	}
	closed := b.closed
	b.mu.RUnlock()
	if closed {
		return
	}

	for _, s := range subs {
		if !s.filter.Matches(e) {
			continue
		}
		select {
		case s.ch <- e:
			s.resetDrops()
		default:
			s.recordDrop()
			if b.policy.DropThreshold > 0 && s.drops() >= b.policy.DropThreshold {
				b.disconnect(s)
			}
		}
	}
}

// Subscribe creates a new subscription.
func (b *Bus) Subscribe(filter event.Filter) event.Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &subscription{
		ch:     make(chan event.Event, b.chCap),
		filter: filter,
		bus:    b,
	}
	if b.closed {
		close(s.ch)
		return s
	}
	b.subs[s] = struct{}{}
	return s
}

// Close disconnects every subscription. Idempotent.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for s := range b.subs {
		s.closeChannel()
		delete(b.subs, s)
	}
}

func (b *Bus) disconnect(s *subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[s]; !ok {
		return
	}
	delete(b.subs, s)
	s.closeChannel()
}

type subscription struct {
	mu       sync.Mutex
	ch       chan event.Event
	filter   event.Filter
	bus      *Bus
	dropCnt  int
	chClosed bool
}

func (s *subscription) Events() <-chan event.Event { return s.ch }

func (s *subscription) Close() error {
	s.bus.disconnect(s)
	return nil
}

func (s *subscription) recordDrop() {
	s.mu.Lock()
	s.dropCnt++
	s.mu.Unlock()
}

func (s *subscription) resetDrops() {
	s.mu.Lock()
	s.dropCnt = 0
	s.mu.Unlock()
}

func (s *subscription) drops() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropCnt
}

func (s *subscription) closeChannel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chClosed {
		return
	}
	close(s.ch)
	s.chClosed = true
}
```

- [x] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/eventbus/ -count=1 -race -v
```

Expected: all three tests pass with `goleak.VerifyNone`.

- [x] **Step 5: Commit**

```bash
git add internal/eventbus/
git commit -m "feat(eventbus): add in-memory bus with slow-subscriber policy"
```

---

## Task 5: Log Ring Buffer

**Files:**
- Create: `internal/logcollect/ringbuf.go`
- Create: `internal/logcollect/ringbuf_test.go`

- [x] **Step 1: Write the failing tests**

Create `internal/logcollect/ringbuf_test.go`:

```go
package logcollect

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRingBuffer_WriteAndSnapshot(t *testing.T) {
	rb := NewRingBuffer(16)
	rb.Write([]byte("hello "))
	rb.Write([]byte("world"))

	snap := rb.Snapshot()
	require.Equal(t, []byte("hello world"), snap)
}

func TestRingBuffer_OverflowsOldestFirst(t *testing.T) {
	rb := NewRingBuffer(8)
	rb.Write([]byte("AAAA"))
	rb.Write([]byte("BBBB"))
	rb.Write([]byte("CCCC"))

	// After 12 bytes in an 8-byte buffer, we keep only the last 8.
	snap := rb.Snapshot()
	require.Equal(t, []byte("BBBBCCCC"), snap)
}

func TestRingBuffer_WriteLargerThanCapacityKeepsSuffix(t *testing.T) {
	rb := NewRingBuffer(4)
	rb.Write([]byte("abcdefgh"))
	require.Equal(t, []byte("efgh"), rb.Snapshot())
}

func TestRingBuffer_Size(t *testing.T) {
	rb := NewRingBuffer(8)
	require.Equal(t, 0, rb.Size())
	rb.Write([]byte("abc"))
	require.Equal(t, 3, rb.Size())
	rb.Write([]byte("defghijk"))
	require.Equal(t, 8, rb.Size())
}
```

- [x] **Step 2: Run the tests and confirm they fail**

Run:

```bash
go test ./internal/logcollect/ -run TestRingBuffer -count=1
```

Expected: `undefined: NewRingBuffer`.

- [x] **Step 3: Implement the ring buffer**

Create `internal/logcollect/ringbuf.go`:

```go
package logcollect

import "sync"

// RingBuffer is a byte-oriented circular buffer with FIFO eviction.
// All methods are safe for concurrent use.
type RingBuffer struct {
	mu       sync.RWMutex
	data     []byte
	capacity int
	head     int // next write position
	size     int // bytes currently stored
}

// NewRingBuffer returns a ring buffer with the given byte capacity.
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity < 1 {
		capacity = 1
	}
	return &RingBuffer{data: make([]byte, capacity), capacity: capacity}
}

// Write appends p to the buffer, evicting old bytes if necessary.
// It always returns len(p), nil to satisfy io.Writer.
func (r *RingBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := len(p)
	if n == 0 {
		return 0, nil
	}

	// If p is larger than capacity, only keep the suffix.
	if n >= r.capacity {
		copy(r.data, p[n-r.capacity:])
		r.head = 0
		r.size = r.capacity
		return n, nil
	}

	// Copy in, wrapping at capacity.
	for i := 0; i < n; i++ {
		r.data[r.head] = p[i]
		r.head = (r.head + 1) % r.capacity
	}
	r.size += n
	if r.size > r.capacity {
		r.size = r.capacity
	}
	return n, nil
}

// Snapshot returns a copy of the currently buffered bytes in FIFO order.
func (r *RingBuffer) Snapshot() []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]byte, r.size)
	if r.size < r.capacity {
		// Not yet wrapped: data is in [0, size).
		copy(out, r.data[:r.size])
		return out
	}
	// Wrapped: oldest byte is at head.
	start := r.head
	copy(out, r.data[start:])
	copy(out[r.capacity-start:], r.data[:start])
	return out
}

// Size returns the number of bytes currently buffered.
func (r *RingBuffer) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.size
}
```

- [x] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/logcollect/ -run TestRingBuffer -count=1 -v -race
```

Expected: four tests pass.

- [x] **Step 5: Commit**

```bash
git add internal/logcollect/ringbuf.go internal/logcollect/ringbuf_test.go
git commit -m "feat(logcollect): add byte ring buffer with FIFO eviction"
```

---

## Task 6: File Rotator

**Files:**
- Create: `internal/logcollect/rotator.go`
- Create: `internal/logcollect/rotator_test.go`

- [x] **Step 1: Write the failing tests**

Create `internal/logcollect/rotator_test.go`:

```go
package logcollect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRotator_WritesAndRotates(t *testing.T) {
	dir := t.TempDir()
	r, err := NewFileRotator(RotatorOptions{
		Dir:      dir,
		BaseName: "test",
		MaxSize:  16,
		MaxFiles: 3,
	})
	require.NoError(t, err)
	defer r.Close()

	// Each write is 8 bytes; after two writes the rotator should rotate.
	_, err = r.Write([]byte("AAAABBBB"))
	require.NoError(t, err)
	_, err = r.Write([]byte("CCCCDDDD"))
	require.NoError(t, err)
	_, err = r.Write([]byte("EEEEFFFF")) // triggers rotation
	require.NoError(t, err)

	files, err := filepath.Glob(filepath.Join(dir, "test*.log"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(files), 2, "should have rotated at least once")
}

func TestRotator_EnforcesMaxFiles(t *testing.T) {
	dir := t.TempDir()
	r, err := NewFileRotator(RotatorOptions{
		Dir:      dir,
		BaseName: "t",
		MaxSize:  4,
		MaxFiles: 2,
	})
	require.NoError(t, err)
	defer r.Close()

	for i := 0; i < 10; i++ {
		_, err := r.Write([]byte("abcde"))
		require.NoError(t, err)
	}

	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.LessOrEqual(t, len(files), 2)
}
```

- [x] **Step 2: Run the tests and confirm they fail**

Run:

```bash
go test ./internal/logcollect/ -run TestRotator -count=1
```

Expected: `undefined: NewFileRotator`.

- [x] **Step 3: Implement the rotator**

Create `internal/logcollect/rotator.go`:

```go
package logcollect

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// RotatorOptions configures a FileRotator.
type RotatorOptions struct {
	Dir      string
	BaseName string
	MaxSize  int64 // rotate when current file exceeds this many bytes
	MaxFiles int   // keep at most this many files (including current)
}

// FileRotator writes to an active log file and rotates it when the size cap
// is exceeded. Rotated files are named "<base>.<n>.log" for n = 1..MaxFiles-1.
type FileRotator struct {
	opts RotatorOptions
	mu   sync.Mutex
	f    *os.File
	size int64
}

// NewFileRotator opens (creating if necessary) the active log file.
func NewFileRotator(opts RotatorOptions) (*FileRotator, error) {
	if opts.MaxSize <= 0 {
		opts.MaxSize = 64 * 1024 * 1024
	}
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = 7
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("rotator: mkdir: %w", err)
	}
	f, err := openActive(opts)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &FileRotator{opts: opts, f: f, size: info.Size()}, nil
}

// Write appends data, rotating if needed.
func (r *FileRotator) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.size+int64(len(p)) > r.opts.MaxSize {
		if err := r.rotateLocked(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	if err != nil {
		return n, err
	}
	r.size += int64(n)
	return n, nil
}

// Close flushes and closes the active file.
func (r *FileRotator) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

func (r *FileRotator) rotateLocked() error {
	if err := r.f.Close(); err != nil {
		return fmt.Errorf("rotator: close active: %w", err)
	}
	// Shift: base.(N-1).log -> base.N.log ... base.1.log -> base.2.log
	for i := r.opts.MaxFiles - 1; i >= 1; i-- {
		from := rotatedPath(r.opts, i)
		to := rotatedPath(r.opts, i+1)
		if _, err := os.Stat(from); err == nil {
			if i+1 >= r.opts.MaxFiles {
				_ = os.Remove(from)
				continue
			}
			if err := os.Rename(from, to); err != nil {
				return fmt.Errorf("rotator: rename: %w", err)
			}
		}
	}
	if err := os.Rename(activePath(r.opts), rotatedPath(r.opts, 1)); err != nil {
		return fmt.Errorf("rotator: rename active: %w", err)
	}
	r.pruneLocked()
	f, err := openActive(r.opts)
	if err != nil {
		return err
	}
	r.f = f
	r.size = 0
	return nil
}

func (r *FileRotator) pruneLocked() {
	entries, err := os.ReadDir(r.opts.Dir)
	if err != nil {
		return
	}
	type entry struct {
		name string
	}
	var files []entry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		files = append(files, entry{name: e.Name()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	if len(files) <= r.opts.MaxFiles {
		return
	}
	for _, f := range files[:len(files)-r.opts.MaxFiles] {
		_ = os.Remove(filepath.Join(r.opts.Dir, f.name))
	}
}

func openActive(opts RotatorOptions) (*os.File, error) {
	return os.OpenFile(activePath(opts), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
}

func activePath(opts RotatorOptions) string {
	return filepath.Join(opts.Dir, opts.BaseName+".log")
}

func rotatedPath(opts RotatorOptions, n int) string {
	return filepath.Join(opts.Dir, fmt.Sprintf("%s.%d.log", opts.BaseName, n))
}
```

- [x] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/logcollect/ -run TestRotator -count=1 -v -race
```

Expected: two tests pass.

- [x] **Step 5: Commit**

```bash
git add internal/logcollect/rotator.go internal/logcollect/rotator_test.go
git commit -m "feat(logcollect): add FileRotator with size-based rotation"
```

---

## Task 7: Log Sink Interface & Collector

**Files:**
- Create: `internal/logcollect/sink.go`
- Create: `internal/logcollect/collector.go`
- Create: `internal/logcollect/collector_test.go`

- [x] **Step 1: Write the failing tests**

Create `internal/logcollect/collector_test.go`:

```go
package logcollect

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCollector_RoutesByProcessAndStream(t *testing.T) {
	c := NewCollector(CollectorOptions{RingBufferBytes: 1024})
	defer c.Close()

	c.Write(LogEntry{ProcessID: "p1", Stream: 0, Timestamp: time.Now(), Data: []byte("hello ")})
	c.Write(LogEntry{ProcessID: "p1", Stream: 0, Timestamp: time.Now(), Data: []byte("world")})
	c.Write(LogEntry{ProcessID: "p1", Stream: 1, Timestamp: time.Now(), Data: []byte("err")})
	c.Write(LogEntry{ProcessID: "p2", Stream: 0, Timestamp: time.Now(), Data: []byte("other")})

	require.Equal(t, []byte("hello world"), c.Snapshot("p1", 0))
	require.Equal(t, []byte("err"), c.Snapshot("p1", 1))
	require.Equal(t, []byte("other"), c.Snapshot("p2", 0))
}

func TestCollector_UnknownProcessReturnsNil(t *testing.T) {
	c := NewCollector(CollectorOptions{RingBufferBytes: 64})
	defer c.Close()
	require.Nil(t, c.Snapshot("missing", 0))
}
```

- [x] **Step 2: Run the tests and confirm they fail**

Run:

```bash
go test ./internal/logcollect/ -run TestCollector -count=1
```

Expected: undefined types.

- [x] **Step 3: Implement Sink interface and Collector**

Create `internal/logcollect/sink.go`:

```go
package logcollect

import "time"

// LogEntry is one logical chunk of log data delivered to a Sink.
type LogEntry struct {
	ProcessID string
	Stream    uint8 // 0=stdout, 1=stderr, 2=diag
	SeqNo     uint64
	Timestamp time.Time
	Data      []byte
}

// Sink is a consumer of LogEntry values.
type Sink interface {
	Write(entry LogEntry) error
	Flush() error
	Close() error
}

// RingBufferSink stores data per (process, stream) in RingBuffers.
type RingBufferSink struct {
	capacity int
	buffers  map[string]*RingBuffer // key = processID + "/" + stream
}

// NewRingBufferSink returns a sink whose buffers have the given byte capacity.
func NewRingBufferSink(capacity int) *RingBufferSink {
	return &RingBufferSink{capacity: capacity, buffers: make(map[string]*RingBuffer)}
}

// Write routes e to the appropriate ring buffer.
func (s *RingBufferSink) Write(e LogEntry) error {
	key := bufferKey(e.ProcessID, e.Stream)
	buf, ok := s.buffers[key]
	if !ok {
		buf = NewRingBuffer(s.capacity)
		s.buffers[key] = buf
	}
	_, err := buf.Write(e.Data)
	return err
}

// Snapshot returns the current contents for (processID, stream) or nil.
func (s *RingBufferSink) Snapshot(processID string, stream uint8) []byte {
	buf, ok := s.buffers[bufferKey(processID, stream)]
	if !ok {
		return nil
	}
	return buf.Snapshot()
}

func (s *RingBufferSink) Flush() error { return nil }
func (s *RingBufferSink) Close() error { return nil }

func bufferKey(processID string, stream uint8) string {
	return processID + "/" + string(rune('0'+stream))
}
```

Create `internal/logcollect/collector.go`:

```go
package logcollect

import "sync"

// CollectorOptions configures the LogCollector.
type CollectorOptions struct {
	RingBufferBytes int
	ExtraSinks      []Sink
}

// Collector is the top-level log ingestion entry point. It routes writes
// to an embedded RingBufferSink plus any extra sinks the caller provides.
type Collector struct {
	mu    sync.RWMutex
	ring  *RingBufferSink
	extra []Sink
}

// NewCollector returns a Collector with the given capacity per stream.
func NewCollector(opts CollectorOptions) *Collector {
	cap := opts.RingBufferBytes
	if cap <= 0 {
		cap = 4 * 1024 * 1024
	}
	return &Collector{
		ring:  NewRingBufferSink(cap),
		extra: append([]Sink(nil), opts.ExtraSinks...),
	}
}

// Write ingests a log entry.
func (c *Collector) Write(e LogEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.ring.Write(e)
	for _, s := range c.extra {
		_ = s.Write(e)
	}
}

// Snapshot returns the ring buffer for (processID, stream).
func (c *Collector) Snapshot(processID string, stream uint8) []byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ring.Snapshot(processID, stream)
}

// Close releases resources.
func (c *Collector) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.extra {
		_ = s.Close()
	}
	_ = c.ring.Close()
}
```

- [x] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/logcollect/ -run TestCollector -count=1 -v -race
```

Expected: two tests pass.

- [x] **Step 5: Commit**

```bash
git add internal/logcollect/sink.go internal/logcollect/collector.go internal/logcollect/collector_test.go
git commit -m "feat(logcollect): add Sink interface and Collector dispatcher"
```

---

## Task 7b: Shared Types Leaf Package (cwtypes)

> **Why this task exists:** Both `internal/controller` and `pkg/cliwrap` need to
> reference the same types (`State`, `Spec`, `Status`, etc.). Placing the
> canonical definitions in a leaf package (`internal/cwtypes`) that neither
> `pkg/cliwrap` nor `internal/controller` depends on breaks the import cycle
> (B06). `pkg/cliwrap` re-exports them via type aliases so the public API is
> unchanged.

**Files:**
- Create: `internal/cwtypes/types.go`
- Create: `internal/cwtypes/types_test.go`

- [x] **Step 1: Write the failing test**

Create `internal/cwtypes/types_test.go`:

```go
package cwtypes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSpec_Validate_OK(t *testing.T) {
	s := Spec{
		ID:          "echo",
		Command:     "echo",
		Args:        []string{"hi"},
		Restart:     RestartNever,
		StopTimeout: 1 * time.Second,
	}
	require.NoError(t, s.Validate())
}

func TestSpec_Validate_MissingID(t *testing.T) {
	s := Spec{Command: "x"}
	err := s.Validate()
	require.Error(t, err)
	var ve *SpecValidationError
	require.ErrorAs(t, err, &ve)
	require.Equal(t, "ID", ve.Field)
}

func TestSpec_Validate_BadID(t *testing.T) {
	s := Spec{ID: "bad id!", Command: "x"}
	err := s.Validate()
	require.Error(t, err)
}

func TestSpec_Validate_MissingCommand(t *testing.T) {
	s := Spec{ID: "a"}
	err := s.Validate()
	require.Error(t, err)
}

func TestState_String(t *testing.T) {
	cases := []struct {
		s State
		w string
	}{
		{StatePending, "pending"},
		{StateStarting, "starting"},
		{StateRunning, "running"},
		{StateStopping, "stopping"},
		{StateStopped, "stopped"},
		{StateCrashed, "crashed"},
		{StateRestarting, "restarting"},
		{StateFailed, "failed"},
	}
	for _, c := range cases {
		require.Equal(t, c.w, c.s.String())
	}
}
```

- [x] **Step 2: Run the tests and confirm they fail**

Run:

```bash
go test ./internal/cwtypes/ -count=1
```

Expected: undefined types.

- [x] **Step 3: Implement the types**

Create `internal/cwtypes/types.go`:

```go
// Package cwtypes contains the shared type definitions used by both
// pkg/cliwrap (the public API) and internal/controller. Placing them in
// a leaf package avoids an import cycle.
package cwtypes

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// RestartPolicy decides what happens after a child exits.
type RestartPolicy int

const (
	RestartNever RestartPolicy = iota
	RestartOnFailure
	RestartAlways
)

// ExceedAction is the action taken when a resource limit is breached.
type ExceedAction int

const (
	ExceedWarn ExceedAction = iota
	ExceedThrottle
	ExceedKill
)

// StdinMode controls how the agent connects the child's stdin.
type StdinMode int

const (
	StdinNone    StdinMode = iota
	StdinPipe              // host may call ProcessHandle.SendStdin
	StdinInherit           // inherit agent's stdin (rarely useful)
)

// State is the runtime state of a managed process.
type State int

const (
	StatePending State = iota
	StateStarting
	StateRunning
	StateStopping
	StateStopped
	StateCrashed
	StateRestarting
	StateFailed
)

// String returns the lowercase name of a State.
func (s State) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	case StateStopped:
		return "stopped"
	case StateCrashed:
		return "crashed"
	case StateRestarting:
		return "restarting"
	case StateFailed:
		return "failed"
	default:
		return fmt.Sprintf("state(%d)", int(s))
	}
}

// ResourceLimits express per-process resource caps.
type ResourceLimits struct {
	MaxRSS        uint64  // bytes
	MaxCPUPercent float64 // 0-100
	OnExceed      ExceedAction
}

// LogOptions controls per-process log storage.
type LogOptions struct {
	RingBufferBytes       int
	RotationDir           string
	RotationMaxSize       int64
	RotationMaxFiles      int
	PreserveWALOnShutdown bool
}

// FileRotationOptions is an alias kept for backward compatibility.
type FileRotationOptions = LogOptions

// SandboxSpec selects a sandbox provider and its configuration.
type SandboxSpec struct {
	Provider string
	Config   map[string]any
}

// Spec is the immutable description of a managed process.
type Spec struct {
	ID             string
	Name           string
	Command        string
	Args           []string
	Env            map[string]string
	WorkDir        string
	Restart        RestartPolicy
	MaxRestarts    int
	RestartBackoff time.Duration
	StopTimeout    time.Duration
	Stdin          StdinMode
	Sandbox        *SandboxSpec
	Resources      ResourceLimits
	LogOptions     LogOptions
}

// Status is a snapshot of the runtime state.
type Status struct {
	State         State
	AgentPID      int
	ChildPID      int
	StartedAt     time.Time
	LastExitAt    time.Time
	LastExitCode  int
	RestartCount  int
	LastError     string
	PendingReason string
	RSS           uint64
	CPUPercent    float64
}

var validID = regexp.MustCompile(`^[A-Za-z0-9_\-]+$`)

// Validate returns an error if s is missing required fields or has bad values.
func (s *Spec) Validate() error {
	if s.ID == "" {
		return &SpecValidationError{Field: "ID", Reason: "required"}
	}
	if !validID.MatchString(s.ID) {
		return &SpecValidationError{Field: "ID", Value: s.ID, Reason: "must match [A-Za-z0-9_-]+"}
	}
	if s.Command == "" {
		return &SpecValidationError{Field: "Command", Reason: "required"}
	}
	if s.StopTimeout < 0 {
		return &SpecValidationError{Field: "StopTimeout", Value: s.StopTimeout, Reason: "must be >= 0"}
	}
	if s.MaxRestarts < 0 {
		return &SpecValidationError{Field: "MaxRestarts", Value: s.MaxRestarts, Reason: "must be >= 0"}
	}
	return nil
}

// ErrInvalidSpec is the sentinel that SpecValidationError.Is maps to.
var ErrInvalidSpec = errors.New("cliwrap: invalid spec")

// SpecValidationError describes a field-level validation failure.
type SpecValidationError struct {
	Field  string
	Value  any
	Reason string
}

func (e *SpecValidationError) Error() string {
	if e.Value != nil {
		return fmt.Sprintf("cliwrap: invalid spec field %q (value=%v): %s", e.Field, e.Value, e.Reason)
	}
	return fmt.Sprintf("cliwrap: invalid spec field %q: %s", e.Field, e.Reason)
}

func (e *SpecValidationError) Is(target error) bool {
	return target == ErrInvalidSpec
}
```

- [x] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/cwtypes/ -count=1 -v -race
```

Expected: five tests pass.

- [x] **Step 5: Commit**

```bash
git add internal/cwtypes/
git commit -m "feat(cwtypes): add shared types leaf package to break import cycle"
```

---

## Task 8: Public Types — Spec, Status, State (Re-export Layer)

> **Note:** The canonical type definitions live in `internal/cwtypes` (Task 7b).
> This task creates `pkg/cliwrap/types.go` as a thin re-export layer using Go
> type aliases, so the public API is unchanged (`cliwrap.State`, `cliwrap.Spec`,
> etc.).

**Files:**
- Create: `pkg/cliwrap/types.go`
- Create: `pkg/cliwrap/types_test.go`

- [x] **Step 1: Write the failing test**

Create `pkg/cliwrap/types_test.go`:

```go
package cliwrap

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSpec_Validate_OK(t *testing.T) {
	s := Spec{
		ID:          "echo",
		Command:     "echo",
		Args:        []string{"hi"},
		Restart:     RestartNever,
		StopTimeout: 1 * time.Second,
	}
	require.NoError(t, s.Validate())
}

func TestSpec_Validate_MissingID(t *testing.T) {
	s := Spec{Command: "x"}
	err := s.Validate()
	require.Error(t, err)
	var ve *SpecValidationError
	require.ErrorAs(t, err, &ve)
	require.Equal(t, "ID", ve.Field)
}

func TestSpec_Validate_BadID(t *testing.T) {
	s := Spec{ID: "bad id!", Command: "x"}
	err := s.Validate()
	require.Error(t, err)
}

func TestSpec_Validate_MissingCommand(t *testing.T) {
	s := Spec{ID: "a"}
	err := s.Validate()
	require.Error(t, err)
}

func TestState_String(t *testing.T) {
	cases := []struct {
		s State
		w string
	}{
		{StatePending, "pending"},
		{StateStarting, "starting"},
		{StateRunning, "running"},
		{StateStopping, "stopping"},
		{StateStopped, "stopped"},
		{StateCrashed, "crashed"},
		{StateRestarting, "restarting"},
		{StateFailed, "failed"},
	}
	for _, c := range cases {
		require.Equal(t, c.w, c.s.String())
	}
}
```

- [x] **Step 2: Run the tests and confirm they fail**

Run:

```bash
go test ./pkg/cliwrap/ -count=1
```

Expected: undefined types.

- [x] **Step 3: Implement the re-export layer**

Create `pkg/cliwrap/types.go`:

```go
// Package cliwrap re-exports the canonical types from internal/cwtypes
// via Go type aliases, so the public API surface is unchanged.
package cliwrap

import (
	"github.com/cli-wrapper/cli-wrapper/internal/cwtypes"
)

// Type aliases — these are transparent to callers. Users continue to
// write cliwrap.State, cliwrap.Spec, etc.
type (
	State              = cwtypes.State
	RestartPolicy      = cwtypes.RestartPolicy
	ExceedAction       = cwtypes.ExceedAction
	StdinMode          = cwtypes.StdinMode
	ResourceLimits     = cwtypes.ResourceLimits
	LogOptions         = cwtypes.LogOptions
	SandboxSpec        = cwtypes.SandboxSpec
	Spec               = cwtypes.Spec
	Status             = cwtypes.Status
	SpecValidationError = cwtypes.SpecValidationError
)

// Re-exported constants.
const (
	RestartNever     = cwtypes.RestartNever
	RestartOnFailure = cwtypes.RestartOnFailure
	RestartAlways    = cwtypes.RestartAlways

	ExceedWarn     = cwtypes.ExceedWarn
	ExceedThrottle = cwtypes.ExceedThrottle
	ExceedKill     = cwtypes.ExceedKill

	StdinNone    = cwtypes.StdinNone
	StdinPipe    = cwtypes.StdinPipe
	StdinInherit = cwtypes.StdinInherit

	StatePending    = cwtypes.StatePending
	StateStarting   = cwtypes.StateStarting
	StateRunning    = cwtypes.StateRunning
	StateStopping   = cwtypes.StateStopping
	StateStopped    = cwtypes.StateStopped
	StateCrashed    = cwtypes.StateCrashed
	StateRestarting = cwtypes.StateRestarting
	StateFailed     = cwtypes.StateFailed
)
```

- [x] **Step 4: Create errors file (depends on SpecValidationError)**

Create `pkg/cliwrap/errors.go`:

```go
package cliwrap

import (
	"errors"

	"github.com/cli-wrapper/cli-wrapper/internal/cwtypes"
)

// Sentinel errors.
var (
	ErrProcessNotFound         = errors.New("cliwrap: process not found")
	ErrProcessAlreadyExists    = errors.New("cliwrap: process already exists")
	ErrProcessNotRunning       = errors.New("cliwrap: process not running")
	ErrProcessAlreadyRunning   = errors.New("cliwrap: process already running")
	ErrManagerShuttingDown     = errors.New("cliwrap: manager is shutting down")
	ErrManagerClosed           = errors.New("cliwrap: manager is closed")
	ErrSandboxProviderNotFound = errors.New("cliwrap: sandbox provider not registered")
	ErrAgentSpawnFailed        = errors.New("cliwrap: failed to spawn agent")
	ErrHandshakeTimeout        = errors.New("cliwrap: handshake timeout")
	ErrStopTimeout             = errors.New("cliwrap: stop timeout")

	// ErrInvalidSpec is re-exported from cwtypes so that
	// SpecValidationError.Is(ErrInvalidSpec) works across both packages.
	ErrInvalidSpec = cwtypes.ErrInvalidSpec
)
```

- [x] **Step 5: Run the tests to verify they pass**

Run:

```bash
go test ./pkg/cliwrap/ -count=1 -v
```

Expected: five tests pass.

- [x] **Step 6: Commit**

```bash
git add pkg/cliwrap/types.go pkg/cliwrap/errors.go pkg/cliwrap/types_test.go
git commit -m "feat(cliwrap): add Spec, Status, State types and validation"
```

---

## Task 9: SpecBuilder

**Files:**
- Create: `pkg/cliwrap/spec_builder.go`
- Create: `pkg/cliwrap/spec_builder_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/cliwrap/spec_builder_test.go`:

```go
package cliwrap

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSpecBuilder_BuildsValidSpec(t *testing.T) {
	s, err := NewSpec("echo", "echo", "hi", "world").
		WithEnv("X", "1").
		WithWorkDir("/tmp").
		WithRestart(RestartOnFailure).
		WithMaxRestarts(5).
		WithRestartBackoff(2 * time.Second).
		WithStopTimeout(10 * time.Second).
		WithResourceLimits(ResourceLimits{MaxRSS: 1 << 20, OnExceed: ExceedKill}).
		Build()

	require.NoError(t, err)
	require.Equal(t, "echo", s.ID)
	require.Equal(t, []string{"hi", "world"}, s.Args)
	require.Equal(t, map[string]string{"X": "1"}, s.Env)
	require.Equal(t, RestartOnFailure, s.Restart)
	require.Equal(t, 5, s.MaxRestarts)
}

func TestSpecBuilder_FailsOnInvalidID(t *testing.T) {
	_, err := NewSpec("", "echo").Build()
	require.Error(t, err)
}
```

- [ ] **Step 2: Run the test to confirm it fails**

Run:

```bash
go test ./pkg/cliwrap/ -run TestSpecBuilder -count=1
```

Expected: `undefined: NewSpec`.

- [ ] **Step 3: Implement the builder**

Create `pkg/cliwrap/spec_builder.go`:

```go
package cliwrap

import "time"

// SpecBuilder is a fluent constructor for Spec. Errors surface only from Build.
type SpecBuilder struct {
	spec Spec
}

// NewSpec starts a new SpecBuilder for the given ID, command, and arguments.
func NewSpec(id, cmd string, args ...string) *SpecBuilder {
	return &SpecBuilder{spec: Spec{
		ID:             id,
		Command:        cmd,
		Args:           append([]string(nil), args...),
		Env:            map[string]string{},
		StopTimeout:    10 * time.Second,
		RestartBackoff: time.Second,
	}}
}

// WithEnv sets a single environment variable.
func (b *SpecBuilder) WithEnv(k, v string) *SpecBuilder {
	if b.spec.Env == nil {
		b.spec.Env = map[string]string{}
	}
	b.spec.Env[k] = v
	return b
}

// WithWorkDir sets the working directory for the child process.
func (b *SpecBuilder) WithWorkDir(path string) *SpecBuilder {
	b.spec.WorkDir = path
	return b
}

// WithRestart sets the restart policy.
func (b *SpecBuilder) WithRestart(p RestartPolicy) *SpecBuilder {
	b.spec.Restart = p
	return b
}

// WithMaxRestarts limits how many restarts will be attempted.
func (b *SpecBuilder) WithMaxRestarts(n int) *SpecBuilder {
	b.spec.MaxRestarts = n
	return b
}

// WithRestartBackoff sets the initial backoff delay between restarts.
func (b *SpecBuilder) WithRestartBackoff(d time.Duration) *SpecBuilder {
	b.spec.RestartBackoff = d
	return b
}

// WithStopTimeout sets the SIGTERM → SIGKILL grace period.
func (b *SpecBuilder) WithStopTimeout(d time.Duration) *SpecBuilder {
	b.spec.StopTimeout = d
	return b
}

// WithStdin selects how the child's stdin is wired.
func (b *SpecBuilder) WithStdin(m StdinMode) *SpecBuilder {
	b.spec.Stdin = m
	return b
}

// WithResourceLimits attaches resource caps.
func (b *SpecBuilder) WithResourceLimits(l ResourceLimits) *SpecBuilder {
	b.spec.Resources = l
	return b
}

// WithLogOptions attaches log storage options.
func (b *SpecBuilder) WithLogOptions(o LogOptions) *SpecBuilder {
	b.spec.LogOptions = o
	return b
}

// WithSandbox attaches a sandbox spec.
func (b *SpecBuilder) WithSandbox(s *SandboxSpec) *SpecBuilder {
	b.spec.Sandbox = s
	return b
}

// Build validates and returns the Spec.
func (b *SpecBuilder) Build() (Spec, error) {
	if err := b.spec.Validate(); err != nil {
		return Spec{}, err
	}
	return b.spec, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./pkg/cliwrap/ -run TestSpecBuilder -count=1 -v
```

Expected: two tests pass.

- [ ] **Step 5: Commit**

```bash
git add pkg/cliwrap/spec_builder.go pkg/cliwrap/spec_builder_test.go
git commit -m "feat(cliwrap): add SpecBuilder fluent API"
```

---

## Task 10: Restart Policy & Backoff

**Files:**
- Create: `internal/supervise/restart.go`
- Create: `internal/supervise/restart_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/supervise/restart_test.go`:

```go
package supervise

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExponentialBackoff(t *testing.T) {
	b := ExponentialBackoff{
		Initial:    100 * time.Millisecond,
		Max:        2 * time.Second,
		Multiplier: 2.0,
		Jitter:     0, // deterministic
	}
	require.Equal(t, 100*time.Millisecond, b.Next(0))
	require.Equal(t, 200*time.Millisecond, b.Next(1))
	require.Equal(t, 400*time.Millisecond, b.Next(2))
	require.Equal(t, 800*time.Millisecond, b.Next(3))
	require.Equal(t, 1600*time.Millisecond, b.Next(4))
	require.Equal(t, 2*time.Second, b.Next(5))  // capped
	require.Equal(t, 2*time.Second, b.Next(10)) // still capped
}

func TestExponentialBackoff_WithJitterProducesVariance(t *testing.T) {
	b := ExponentialBackoff{
		Initial:    100 * time.Millisecond,
		Max:        10 * time.Second,
		Multiplier: 2.0,
		Jitter:     0.5,
	}
	// Pull several values and assert they are not all identical.
	d := b.Next(3)
	seenDifferent := false
	for i := 0; i < 5; i++ {
		if b.Next(3) != d {
			seenDifferent = true
			break
		}
	}
	require.True(t, seenDifferent, "jitter should introduce variance")
}
```

- [ ] **Step 2: Run the tests to confirm they fail**

Run:

```bash
go test ./internal/supervise/ -count=1
```

Expected: `undefined: ExponentialBackoff`.

- [ ] **Step 3: Implement the backoff**

Create `internal/supervise/restart.go`:

```go
package supervise

import (
	"math"
	"math/rand"
	"time"
)

// BackoffStrategy computes the delay before the Nth restart attempt.
type BackoffStrategy interface {
	Next(attempt int) time.Duration
}

// ExponentialBackoff is an exponential backoff with optional jitter.
type ExponentialBackoff struct {
	Initial    time.Duration
	Max        time.Duration
	Multiplier float64
	Jitter     float64 // 0..1, relative jitter range

	rng *rand.Rand
}

// Next returns the delay before the (attempt)th retry. Attempt 0 returns Initial.
func (b *ExponentialBackoff) Next(attempt int) time.Duration {
	if b.Initial <= 0 {
		b.Initial = time.Second
	}
	if b.Max <= 0 {
		b.Max = time.Minute
	}
	if b.Multiplier <= 1 {
		b.Multiplier = 2
	}

	d := float64(b.Initial) * math.Pow(b.Multiplier, float64(attempt))
	if d > float64(b.Max) {
		d = float64(b.Max)
	}

	if b.Jitter > 0 {
		if b.rng == nil {
			b.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
		}
		delta := d * b.Jitter
		d += (b.rng.Float64()*2 - 1) * delta
		if d < 0 {
			d = 0
		}
	}
	return time.Duration(d)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/supervise/ -count=1 -v
```

Expected: two tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/supervise/
git commit -m "feat(supervise): add ExponentialBackoff strategy"
```

---

## Task 11: Test Fixtures (echo, noisy, crasher, hanger)

**Files:**
- Create: `test/fixtures/echo/main.go`
- Create: `test/fixtures/noisy/main.go`
- Create: `test/fixtures/crasher/main.go`
- Create: `test/fixtures/hanger/main.go`
- Create: `test/fixtures/build.go`

- [ ] **Step 1: Create fixture-echo**

Create `test/fixtures/echo/main.go`:

```go
// fixture-echo reads stdin line by line and writes each line to stdout.
package main

import (
	"bufio"
	"fmt"
	"os"
)

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}
}
```

- [ ] **Step 2: Create fixture-noisy**

Create `test/fixtures/noisy/main.go`:

```go
// fixture-noisy writes a heartbeat line to stdout and stderr every 100ms.
package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for i := 0; ; i++ {
		select {
		case <-t.C:
			fmt.Fprintf(os.Stdout, "out %d\n", i)
			fmt.Fprintf(os.Stderr, "err %d\n", i)
		}
	}
}
```

- [ ] **Step 3: Create fixture-crasher**

Create `test/fixtures/crasher/main.go`:

```go
// fixture-crasher exits with a specific code after a delay.
package main

import (
	"flag"
	"os"
	"time"
)

func main() {
	after := flag.Duration("after", 500*time.Millisecond, "exit after this delay")
	code := flag.Int("code", 99, "exit code")
	flag.Parse()
	time.Sleep(*after)
	os.Exit(*code)
}
```

- [ ] **Step 4: Create fixture-hanger**

Create `test/fixtures/hanger/main.go`:

```go
// fixture-hanger ignores SIGTERM until it receives SIGKILL.
package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	for {
		select {
		case <-sigs:
			// Ignore and keep running.
		case <-time.After(10 * time.Second):
			os.Exit(0)
		}
	}
}
```

- [ ] **Step 5: Add a build helper**

Create `test/fixtures/build.go`:

```go
// Package fixtures provides a go:generate-friendly entry point for
// compiling the test binaries. To build them all:
//
//	go build -o test/fixtures/bin/fixture-echo   ./test/fixtures/echo
//	go build -o test/fixtures/bin/fixture-noisy  ./test/fixtures/noisy
//	go build -o test/fixtures/bin/fixture-crasher ./test/fixtures/crasher
//	go build -o test/fixtures/bin/fixture-hanger ./test/fixtures/hanger
//
package fixtures
```

- [ ] **Step 6: Add a Makefile target**

Open `Makefile` and append:

```make
.PHONY: fixtures
fixtures:
	mkdir -p test/fixtures/bin
	$(GO) build -o test/fixtures/bin/fixture-echo   ./test/fixtures/echo
	$(GO) build -o test/fixtures/bin/fixture-noisy  ./test/fixtures/noisy
	$(GO) build -o test/fixtures/bin/fixture-crasher ./test/fixtures/crasher
	$(GO) build -o test/fixtures/bin/fixture-hanger ./test/fixtures/hanger
```

- [ ] **Step 7: Build the fixtures and verify**

Run:

```bash
make fixtures
ls -1 test/fixtures/bin/
```

Expected: four binaries listed.

- [ ] **Step 8: Commit**

```bash
git add test/fixtures/ Makefile
git commit -m "test: add fixture binaries for integration tests"
```

---

## Task 12: Agent Binary Skeleton

**Files:**
- Create: `cmd/cliwrap-agent/main.go`
- Create: `internal/agent/main.go`
- Create: `internal/agent/main_test.go`

- [ ] **Step 1: Write a sanity test**

Create `internal/agent/main_test.go`:

```go
package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	require.Equal(t, 3, cfg.IPCFD)
	require.Greater(t, cfg.OutboxCapacity, 0)
}
```

- [ ] **Step 2: Run and confirm it fails**

Run:

```bash
go test ./internal/agent/ -count=1
```

Expected: `undefined: DefaultConfig`.

- [ ] **Step 3: Implement the config and agent entry**

Create `internal/agent/main.go`:

```go
// Package agent contains the logic that runs inside the cliwrap-agent
// subprocess. It owns the child CLI process and the IPC connection back
// to the host.
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cli-wrapper/cli-wrapper/internal/ipc"
)

// Config is the runtime configuration passed from the host via env vars
// and argv. For v1, the fd index is fixed at 3.
type Config struct {
	IPCFD           int
	AgentID         string
	RuntimeDir      string
	OutboxCapacity  int
	WALBytes        int64
	MaxRecvPayload  uint32
}

// DefaultConfig returns sensible defaults, reading host-supplied env vars
// CLIWRAP_AGENT_ID and CLIWRAP_AGENT_RUNTIME when present.
func DefaultConfig() Config {
	cfg := Config{
		IPCFD:          3,
		OutboxCapacity: 1024,
		WALBytes:       256 * 1024 * 1024,
		MaxRecvPayload: ipc.MaxPayloadSize,
	}
	if v := os.Getenv("CLIWRAP_AGENT_ID"); v != "" {
		cfg.AgentID = v
	}
	if v := os.Getenv("CLIWRAP_AGENT_RUNTIME"); v != "" {
		cfg.RuntimeDir = v
	}
	return cfg
}

// Run is the main entry point invoked from cmd/cliwrap-agent/main.go.
// It blocks until the IPC connection closes or ctx is canceled.
func Run(ctx context.Context, cfg Config) error {
	if cfg.AgentID == "" {
		cfg.AgentID = fmt.Sprintf("agent-%d", os.Getpid())
	}
	if cfg.RuntimeDir == "" {
		cfg.RuntimeDir = filepath.Join(os.TempDir(), "cliwrap-agent-"+cfg.AgentID)
	}
	if err := os.MkdirAll(cfg.RuntimeDir, 0o700); err != nil {
		return fmt.Errorf("agent: mkdir runtime: %w", err)
	}

	f := os.NewFile(uintptr(cfg.IPCFD), "ipc")
	if f == nil {
		return fmt.Errorf("agent: ipc fd %d is not valid", cfg.IPCFD)
	}

	conn, err := ipc.NewConn(ipc.ConnConfig{
		RWC:            f,
		SpillerDir:     filepath.Join(cfg.RuntimeDir, "outbox"),
		Capacity:       cfg.OutboxCapacity,
		WALBytes:       cfg.WALBytes,
		MaxRecvPayload: cfg.MaxRecvPayload,
	})
	if err != nil {
		return fmt.Errorf("agent: ipc conn: %w", err)
	}

	d := NewDispatcher(conn)
	conn.OnMessage(d.Handle)
	conn.Start()

	// Send initial HELLO.
	helloPayload := ipc.HelloPayload{ProtocolVersion: ipc.ProtocolVersion, AgentID: cfg.AgentID}
	if err := d.SendControl(ipc.MsgHello, helloPayload, false); err != nil {
		return fmt.Errorf("agent: send hello: %w", err)
	}

	<-ctx.Done()

	shutdownCtx, cancel := ctxWithTimeoutSeconds(5)
	defer cancel()
	_ = conn.Close(shutdownCtx)
	return nil
}
```

Create `cmd/cliwrap-agent/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/cli-wrapper/cli-wrapper/internal/agent"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()

	if err := agent.Run(ctx, agent.DefaultConfig()); err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap-agent: %v\n", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Add context helper (placeholder dispatcher)**

Create `internal/agent/dispatcher.go` with a minimal placeholder that Task 13 will flesh out:

```go
package agent

import (
	"context"
	"sync"
	"time"

	"github.com/cli-wrapper/cli-wrapper/internal/ipc"
)

// Dispatcher routes inbound IPC messages and owns outbound message sending.
// It will be expanded in Task 13.
type Dispatcher struct {
	conn *ipc.Conn
	mu   sync.Mutex
}

// NewDispatcher constructs a Dispatcher bound to conn.
func NewDispatcher(conn *ipc.Conn) *Dispatcher {
	return &Dispatcher{conn: conn}
}

// Handle processes an inbound message. Task 13 adds real routing.
func (d *Dispatcher) Handle(msg ipc.OutboxMessage) {
	// no-op for now
}

// SendControl serializes and enqueues a control-plane message.
func (d *Dispatcher) SendControl(t ipc.MsgType, payload any, ackRequired bool) error {
	data, err := ipc.EncodePayload(payload)
	if err != nil {
		return err
	}
	h := ipc.Header{
		MsgType: t,
		SeqNo:   d.conn.Seqs().Next(),
		Length:  uint32(len(data)),
	}
	if ackRequired {
		h.Flags |= ipc.FlagAckRequired
	}
	d.conn.Send(ipc.OutboxMessage{Header: h, Payload: data})
	return nil
}

func ctxWithTimeoutSeconds(n int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(n)*time.Second)
}
```

- [ ] **Step 5: Run the tests and verify the binary builds**

Run:

```bash
go test ./internal/agent/ -count=1 -v
go build ./cmd/cliwrap-agent
```

Expected: test passes, `cliwrap-agent` binary is produced in the working directory.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/ cmd/cliwrap-agent/
git commit -m "feat(agent): add agent binary skeleton with ipc wiring"
```

---

## Task 13: Agent Runner — Child Fork/Exec & Wait

**Files:**
- Create: `internal/agent/runner.go`
- Create: `internal/agent/runner_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/agent/runner_test.go`:

```go
package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestRunner_RunAndExit(t *testing.T) {
	defer goleak.VerifyNone(t)

	r := NewRunner()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := r.Run(ctx, RunSpec{
		Command: "/bin/sh",
		Args:    []string{"-c", "exit 0"},
	})
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
	require.Equal(t, 0, result.Signal)
	require.NotZero(t, result.PID)
}

func TestRunner_NonZeroExit(t *testing.T) {
	defer goleak.VerifyNone(t)

	r := NewRunner()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := r.Run(ctx, RunSpec{
		Command: "/bin/sh",
		Args:    []string{"-c", "exit 42"},
	})
	require.NoError(t, err)
	require.Equal(t, 42, result.ExitCode)
}

func TestRunner_CancelTerminatesProcess(t *testing.T) {
	defer goleak.VerifyNone(t)

	r := NewRunner()
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		_, err := r.Run(ctx, RunSpec{
			Command: "/bin/sh",
			Args:    []string{"-c", "sleep 10"},
		})
		errCh <- err
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err, "Run should return cleanly after ctx cancel")
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}
```

- [ ] **Step 2: Run the test to confirm it fails**

Run:

```bash
go test ./internal/agent/ -run TestRunner -count=1
```

Expected: `undefined: NewRunner`.

- [ ] **Step 3: Implement the runner**

Create `internal/agent/runner.go`:

```go
package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// RunSpec describes the child process to fork/exec.
type RunSpec struct {
	Command     string
	Args        []string
	Env         map[string]string
	WorkDir     string
	StopTimeout time.Duration
	OnStarted   func(pid int) // called after cmd.Start() succeeds, before Wait()
}

// RunResult summarizes the child's exit.
type RunResult struct {
	PID      int
	ExitCode int
	Signal   int
	ExitedAt time.Time
	Reason   string
}

// Runner starts a single child process and blocks until it exits.
type Runner struct {
	// StdoutSink, if non-nil, is used to tee the child's stdout bytes.
	StdoutSink io.Writer
	// StderrSink, if non-nil, is used to tee the child's stderr bytes.
	StderrSink io.Writer
}

// NewRunner returns a Runner with default sinks set to io.Discard.
func NewRunner() *Runner {
	return &Runner{
		StdoutSink: io.Discard,
		StderrSink: io.Discard,
	}
}

// Run forks/execs the child and blocks until it exits or ctx is canceled.
// On ctx cancel it sends SIGTERM, waits StopTimeout, then SIGKILL.
func (r *Runner) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
	cmd := exec.Command(spec.Command, spec.Args...)
	cmd.Env = flattenEnv(spec.Env)
	if spec.WorkDir != "" {
		cmd.Dir = spec.WorkDir
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{}, fmt.Errorf("runner: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return RunResult{}, fmt.Errorf("runner: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return RunResult{}, fmt.Errorf("runner: start: %w", err)
	}
	pid := cmd.Process.Pid

	// Notify caller that the child is running (before Wait).
	if spec.OnStarted != nil {
		spec.OnStarted(pid)
	}

	// Pump stdout and stderr concurrently.
	copyDone := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(r.StdoutSink, stdout); copyDone <- struct{}{} }()
	go func() { _, _ = io.Copy(r.StderrSink, stderr); copyDone <- struct{}{} }()

	// Kick off a goroutine that escalates on ctx cancel.
	stopSignal := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Signal(syscall.SIGTERM)
			timeout := spec.StopTimeout
			if timeout <= 0 {
				timeout = 5 * time.Second
			}
			select {
			case <-stopSignal:
			case <-time.After(timeout):
				_ = cmd.Process.Kill()
			}
		case <-stopSignal:
		}
	}()

	waitErr := cmd.Wait()
	close(stopSignal)
	<-copyDone
	<-copyDone

	var exitCode int
	var sig int
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
				if ws.Exited() {
					exitCode = ws.ExitStatus()
				}
				if ws.Signaled() {
					sig = int(ws.Signal())
					exitCode = 128 + sig
				}
			} else {
				exitCode = -1
			}
		} else {
			return RunResult{PID: pid}, fmt.Errorf("runner: wait: %w", waitErr)
		}
	}

	return RunResult{
		PID:      pid,
		ExitCode: exitCode,
		Signal:   sig,
		ExitedAt: time.Now(),
		Reason:   "exited",
	}, nil
}

func flattenEnv(env map[string]string) []string {
	if len(env) == 0 {
		return os.Environ()
	}
	base := os.Environ()
	for k, v := range env {
		base = append(base, k+"="+v)
	}
	return base
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/agent/ -run TestRunner -count=1 -v -race
```

Expected: three tests pass with no goroutine leaks.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/runner.go internal/agent/runner_test.go
git commit -m "feat(agent): add Runner with SIGTERM→SIGKILL escalation"
```

---

## Task 14: Agent Dispatcher — START_CHILD / STOP_CHILD Handling

**Files:**
- Modify: `internal/agent/dispatcher.go`
- Create: `internal/agent/dispatcher_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/agent/dispatcher_test.go`:

```go
package agent

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/cli-wrapper/cli-wrapper/internal/ipc"
)

func TestDispatcher_HandlesStartChildAndExit(t *testing.T) {
	defer goleak.VerifyNone(t)

	a, b := net.Pipe()
	dirA := t.TempDir()
	dirB := t.TempDir()

	agentConn, err := ipc.NewConn(ipc.ConnConfig{RWC: a, SpillerDir: dirA, Capacity: 64, WALBytes: 1 << 20})
	require.NoError(t, err)
	hostConn, err := ipc.NewConn(ipc.ConnConfig{RWC: b, SpillerDir: dirB, Capacity: 64, WALBytes: 1 << 20})
	require.NoError(t, err)

	d := NewDispatcher(agentConn)
	agentConn.OnMessage(d.Handle)
	agentConn.Start()

	received := make(chan ipc.OutboxMessage, 4)
	hostConn.OnMessage(func(m ipc.OutboxMessage) { received <- m })
	hostConn.Start()

	// Host sends START_CHILD for /bin/sh -c 'exit 7'.
	sc := ipc.StartChildPayload{
		Command: "/bin/sh",
		Args:    []string{"-c", "exit 7"},
	}
	data, err := ipc.EncodePayload(sc)
	require.NoError(t, err)
	hostConn.Send(ipc.OutboxMessage{
		Header:  ipc.Header{MsgType: ipc.MsgStartChild, SeqNo: 1, Length: uint32(len(data))},
		Payload: data,
	})

	// Expect CHILD_STARTED then CHILD_EXITED.
	var startedSeen, exitedSeen bool
	var exitedPayload ipc.ChildExitedPayload
	timeout := time.After(3 * time.Second)
	for !exitedSeen {
		select {
		case m := <-received:
			switch m.Header.MsgType {
			case ipc.MsgChildStarted:
				startedSeen = true
			case ipc.MsgChildExited:
				exitedSeen = true
				require.NoError(t, ipc.DecodePayload(m.Payload, &exitedPayload))
			}
		case <-timeout:
			t.Fatalf("timed out waiting for child lifecycle events (started=%v exited=%v)", startedSeen, exitedSeen)
		}
	}
	require.True(t, startedSeen)
	require.Equal(t, int32(7), exitedPayload.ExitCode)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = agentConn.Close(ctx)
	_ = hostConn.Close(ctx)
}
```

- [ ] **Step 2: Run the test to confirm it fails**

Run:

```bash
go test ./internal/agent/ -run TestDispatcher -count=1
```

Expected: the dispatcher's `Handle` is still a no-op, so no lifecycle messages arrive.

- [ ] **Step 3: Extend the Dispatcher**

Replace `internal/agent/dispatcher.go` with:

```go
package agent

import (
	"context"
	"sync"
	"time"

	"github.com/cli-wrapper/cli-wrapper/internal/ipc"
)

// Dispatcher routes inbound IPC messages and coordinates child lifecycles.
type Dispatcher struct {
	conn   *ipc.Conn
	runner *Runner

	mu         sync.Mutex
	cancelFn   context.CancelFunc
	runningPID int
	wg         sync.WaitGroup
}

// NewDispatcher constructs a Dispatcher bound to conn.
func NewDispatcher(conn *ipc.Conn) *Dispatcher {
	return &Dispatcher{conn: conn, runner: NewRunner()}
}

// Handle processes an inbound message from the host.
func (d *Dispatcher) Handle(msg ipc.OutboxMessage) {
	switch msg.Header.MsgType {
	case ipc.MsgHello:
		_ = d.SendControl(ipc.MsgHelloAck, ipc.HelloAckPayload{ProtocolVersion: ipc.ProtocolVersion}, false)
	case ipc.MsgStartChild:
		var p ipc.StartChildPayload
		if err := ipc.DecodePayload(msg.Payload, &p); err != nil {
			_ = d.SendControl(ipc.MsgChildError, ipc.ChildErrorPayload{Phase: "decode", Message: err.Error()}, false)
			return
		}
		d.startChild(p)
	case ipc.MsgStopChild:
		var p ipc.StopChildPayload
		_ = ipc.DecodePayload(msg.Payload, &p)
		d.stopChild(time.Duration(p.TimeoutMs) * time.Millisecond)
	case ipc.MsgPing:
		_ = d.SendControl(ipc.MsgPong, nil, false)
	case ipc.MsgShutdown:
		d.stopChild(5 * time.Second)
	}
}

func (d *Dispatcher) startChild(p ipc.StartChildPayload) {
	d.mu.Lock()
	if d.cancelFn != nil {
		d.mu.Unlock()
		_ = d.SendControl(ipc.MsgChildError, ipc.ChildErrorPayload{Phase: "start", Message: "child already running"}, false)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancelFn = cancel
	d.mu.Unlock()

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		res, err := d.runner.Run(ctx, RunSpec{
			Command:     p.Command,
			Args:        p.Args,
			Env:         p.Env,
			WorkDir:     p.WorkDir,
			StopTimeout: time.Duration(p.StopTimeout) * time.Millisecond,
			OnStarted: func(pid int) {
				_ = d.SendControl(ipc.MsgChildStarted, ipc.ChildStartedPayload{
					PID: int32(pid), StartedAt: time.Now().UnixNano(),
				}, false)
			},
		})
		if err != nil {
			_ = d.SendControl(ipc.MsgChildError, ipc.ChildErrorPayload{Phase: "exec", Message: err.Error()}, false)
			d.clearRunning()
			return
		}

		_ = d.SendControl(ipc.MsgChildExited, ipc.ChildExitedPayload{
			PID:      int32(res.PID),
			ExitCode: int32(res.ExitCode),
			Signal:   int32(res.Signal),
			ExitedAt: res.ExitedAt.UnixNano(),
			Reason:   res.Reason,
		}, false)

		d.clearRunning()
	}()
}

func (d *Dispatcher) stopChild(timeout time.Duration) {
	d.mu.Lock()
	cancel := d.cancelFn
	d.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	// cancel() triggers Runner's SIGTERM→SIGKILL escalation inside the
	// RunSpec.StopTimeout window. After that, Runner.Run returns and
	// wg.Done() fires. So wg.Wait() is bounded by StopTimeout.
	d.wg.Wait()
}

func (d *Dispatcher) clearRunning() {
	d.mu.Lock()
	d.cancelFn = nil
	d.runningPID = 0
	d.mu.Unlock()
}

// SendControl serializes payload and enqueues it as an outbound frame.
func (d *Dispatcher) SendControl(t ipc.MsgType, payload any, ackRequired bool) error {
	var data []byte
	var err error
	if payload != nil {
		data, err = ipc.EncodePayload(payload)
		if err != nil {
			return err
		}
	}
	h := ipc.Header{
		MsgType: t,
		SeqNo:   d.conn.Seqs().Next(),
		Length:  uint32(len(data)),
	}
	if ackRequired {
		h.Flags |= ipc.FlagAckRequired
	}
	d.conn.Send(ipc.OutboxMessage{Header: h, Payload: data})
	return nil
}

func ctxWithTimeoutSeconds(n int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(n)*time.Second)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/agent/ -run TestDispatcher -count=1 -v -race
```

Expected: the dispatcher test sees `CHILD_STARTED` then `CHILD_EXITED` with exit code 7.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/dispatcher.go internal/agent/dispatcher_test.go
git commit -m "feat(agent): dispatcher handles START_CHILD / STOP_CHILD / PING"
```

---

## Task 15: Spawner — Host-Side Agent Subprocess Launcher

**Files:**
- Create: `internal/supervise/spawner.go`
- Create: `internal/supervise/spawner_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/supervise/spawner_test.go`:

```go
package supervise

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// buildAgent compiles the cliwrap-agent binary once per test run.
func buildAgent(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	out := filepath.Join(tmp, "cliwrap-agent")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/cliwrap-agent")
	cmd.Dir = projectRoot(t)
	b, err := cmd.CombinedOutput()
	require.NoError(t, err, string(b))
	return out
}

func projectRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatal("go.mod not found")
		}
		wd = parent
	}
}

func TestSpawner_SpawnsAgentAndReceivesHello(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := buildAgent(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	spawner := NewSpawner(SpawnerOptions{
		AgentPath: agentBin,
	})
	h, err := spawner.Spawn(ctx, "test-agent-1")
	require.NoError(t, err)
	defer h.Close()

	// The host side of the socket pair is h.Socket; verify it is readable.
	require.NotNil(t, h.Socket)
	require.Greater(t, h.PID, 0)
}
```

- [ ] **Step 2: Run the test to confirm it fails**

Run:

```bash
go test ./internal/supervise/ -run TestSpawner -count=1
```

Expected: `undefined: NewSpawner`.

- [ ] **Step 3: Implement the spawner**

Create `internal/supervise/spawner.go`:

```go
package supervise

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// SpawnerOptions configures a Spawner.
type SpawnerOptions struct {
	// AgentPath is the path to the cliwrap-agent binary. Required.
	AgentPath string

	// RuntimeDir is the parent directory for per-agent state.
	RuntimeDir string

	// ExtraEnv entries are appended to the agent's environment.
	ExtraEnv []string
}

// AgentHandle is the result of spawning an agent: one half of a connected
// socket pair plus the OS process handle.
type AgentHandle struct {
	Socket  *net.UnixConn
	Process *os.Process
	PID     int

	mu     sync.Mutex
	closed bool
}

// Close shuts the host side of the socket, sends SIGTERM to the agent,
// waits briefly, then SIGKILL if still alive. It is idempotent.
func (h *AgentHandle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	if h.Socket != nil {
		_ = h.Socket.Close()
	}
	if h.Process != nil {
		_ = h.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { h.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = h.Process.Kill()
			<-done
		}
	}
	return nil
}

// Spawner starts agent subprocesses.
type Spawner struct {
	opts SpawnerOptions
}

// NewSpawner returns a Spawner.
func NewSpawner(opts SpawnerOptions) *Spawner {
	return &Spawner{opts: opts}
}

// Spawn creates a socket pair, execs the agent with one fd, and returns the
// host side of the socket wrapped in a net.UnixConn.
func (s *Spawner) Spawn(ctx context.Context, agentID string) (*AgentHandle, error) {
	if s.opts.AgentPath == "" {
		return nil, errors.New("supervise: AgentPath is required")
	}

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("supervise: socketpair: %w", err)
	}
	hostFD := os.NewFile(uintptr(fds[0]), "host-ipc")
	agentFD := os.NewFile(uintptr(fds[1]), "agent-ipc")

	// Use exec.Command (not CommandContext) so the agent's lifetime is
	// controlled solely by AgentHandle.Close, not the spawn ctx. The spawn
	// ctx is only used for the handshake timeout.
	cmd := exec.Command(s.opts.AgentPath)
	cmd.ExtraFiles = []*os.File{agentFD}
	cmd.Env = append(os.Environ(), "CLIWRAP_AGENT_ID="+agentID)
	cmd.Env = append(cmd.Env, s.opts.ExtraEnv...)
	if s.opts.RuntimeDir != "" {
		cmd.Env = append(cmd.Env, "CLIWRAP_AGENT_RUNTIME="+s.opts.RuntimeDir)
	}

	if err := cmd.Start(); err != nil {
		_ = hostFD.Close()
		_ = agentFD.Close()
		return nil, fmt.Errorf("supervise: start agent: %w", err)
	}
	// The agent now owns its fd; close our copy.
	_ = agentFD.Close()

	conn, err := net.FileConn(hostFD)
	if err != nil {
		_ = hostFD.Close()
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("supervise: file conn: %w", err)
	}
	_ = hostFD.Close()

	uconn, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("supervise: unexpected conn type %T", conn)
	}

	return &AgentHandle{
		Socket:  uconn,
		Process: cmd.Process,
		PID:     cmd.Process.Pid,
	}, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/supervise/ -count=1 -v -race
```

Expected: test passes; agent binary is built once and forked.

- [ ] **Step 5: Commit**

```bash
git add internal/supervise/spawner.go internal/supervise/spawner_test.go
git commit -m "feat(supervise): add Spawner for agent subprocess via socketpair"
```

---

## Task 16: OS Watcher

**Files:**
- Create: `internal/supervise/watcher.go`
- Create: `internal/supervise/watcher_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/supervise/watcher_test.go`:

```go
package supervise

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWatcher_EmitsExitCode(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 3")
	require.NoError(t, cmd.Start())

	w := NewWatcher(cmd.Process)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := w.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, res.ExitCode)
	require.Equal(t, cmd.Process.Pid, res.PID)
}
```

- [ ] **Step 2: Run and confirm it fails**

Run:

```bash
go test ./internal/supervise/ -run TestWatcher -count=1
```

Expected: undefined.

- [ ] **Step 3: Implement the watcher**

Create `internal/supervise/watcher.go`:

```go
package supervise

import (
	"context"
	"errors"
	"os"
	"syscall"
)

// WatchResult is the OS-level summary of a child's exit.
type WatchResult struct {
	PID      int
	ExitCode int
	Signal   int
}

// Watcher wraps os.Process.Wait in a context-aware API.
type Watcher struct {
	proc *os.Process
}

// NewWatcher returns a Watcher for proc.
func NewWatcher(proc *os.Process) *Watcher {
	return &Watcher{proc: proc}
}

// Wait blocks until the process exits or ctx is canceled. On ctx cancel
// it returns ctx.Err() without killing the process — the caller must
// decide how to escalate.
func (w *Watcher) Wait(ctx context.Context) (WatchResult, error) {
	type waitOut struct {
		state *os.ProcessState
		err   error
	}
	ch := make(chan waitOut, 1)
	go func() {
		st, err := w.proc.Wait()
		ch <- waitOut{st, err}
	}()

	select {
	case out := <-ch:
		if out.err != nil {
			return WatchResult{PID: w.proc.Pid}, out.err
		}
		st := out.state
		res := WatchResult{PID: w.proc.Pid, ExitCode: st.ExitCode()}
		if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			res.Signal = int(ws.Signal())
		}
		return res, nil
	case <-ctx.Done():
		return WatchResult{PID: w.proc.Pid}, errors.New("watcher: context canceled")
	}
}
```

- [ ] **Step 4: Run and verify**

Run:

```bash
go test ./internal/supervise/ -run TestWatcher -count=1 -v
```

Expected: test passes.

- [ ] **Step 5: Commit**

```bash
git add internal/supervise/watcher.go internal/supervise/watcher_test.go
git commit -m "feat(supervise): add context-aware OS process watcher"
```

---

## Task 17: Controller — Skeleton & Lifecycle

**Files:**
- Create: `internal/controller/controller.go`
- Create: `internal/controller/controller_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/controller/controller_test.go`:

```go
package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/cli-wrapper/cli-wrapper/internal/cwtypes"
	"github.com/cli-wrapper/cli-wrapper/internal/supervise"
)

func TestController_StartAgentAndStop(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)

	spawner := supervise.NewSpawner(supervise.SpawnerOptions{AgentPath: agentBin})

	spec := cwtypes.Spec{
		ID:          "echo-test",
		Command:     "/bin/sh",
		Args:        []string{"-c", "exit 0"},
		StopTimeout: 2 * time.Second,
	}

	ctrl, err := NewController(ControllerOptions{
		Spec:       spec,
		Spawner:    spawner,
		RuntimeDir: t.TempDir(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, ctrl.Start(ctx))

	// Wait for natural exit.
	require.Eventually(t, func() bool {
		return ctrl.State() == cwtypes.StateStopped || ctrl.State() == cwtypes.StateCrashed
	}, 3*time.Second, 20*time.Millisecond)

	require.NoError(t, ctrl.Close(ctx))
}
```

- [ ] **Step 2: Expose a test helper in supervise**

Create `internal/supervise/testhelpers.go`:

```go
package supervise

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	testAgentOnce sync.Once
	testAgentPath string
)

// BuildAgentForTest compiles the cliwrap-agent binary once per test process
// and returns its path.
func BuildAgentForTest(t *testing.T) string {
	t.Helper()
	testAgentOnce.Do(func() {
		tmp, err := os.MkdirTemp("", "cliwrap-agent-*")
		require.NoError(t, err)
		testAgentPath = filepath.Join(tmp, "cliwrap-agent")
		cmd := exec.Command("go", "build", "-o", testAgentPath, "./cmd/cliwrap-agent")
		cmd.Dir = findModuleRoot(t)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	})
	return testAgentPath
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatal("go.mod not found")
		}
		wd = parent
	}
}
```

- [ ] **Step 3: Implement the Controller**

Create `internal/controller/controller.go`:

```go
package controller

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/cli-wrapper/cli-wrapper/internal/cwtypes"
	"github.com/cli-wrapper/cli-wrapper/internal/ipc"
	"github.com/cli-wrapper/cli-wrapper/internal/supervise"
)

// ControllerOptions configures a Controller.
type ControllerOptions struct {
	Spec       cwtypes.Spec
	Spawner    *supervise.Spawner
	RuntimeDir string
}

// Controller manages exactly one agent subprocess and its child.
type Controller struct {
	opts   ControllerOptions
	state  atomic.Int32
	handle *supervise.AgentHandle
	conn   *ipc.Conn

	mu       sync.Mutex
	childPID int
	closed   atomic.Bool
	closeErr error
}

// NewController constructs a Controller.
func NewController(opts ControllerOptions) (*Controller, error) {
	if opts.Spawner == nil {
		return nil, errors.New("controller: Spawner is required")
	}
	if opts.RuntimeDir == "" {
		return nil, errors.New("controller: RuntimeDir is required")
	}
	c := &Controller{opts: opts}
	c.state.Store(int32(cwtypes.StatePending))
	return c, nil
}

// State returns the current state.
func (c *Controller) State() cwtypes.State {
	return cwtypes.State(c.state.Load())
}

// ChildPID returns the PID of the running child (0 if none).
func (c *Controller) ChildPID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.childPID
}

// Start spawns the agent and issues START_CHILD.
func (c *Controller) Start(ctx context.Context) error {
	c.state.Store(int32(cwtypes.StateStarting))

	handle, err := c.opts.Spawner.Spawn(ctx, c.opts.Spec.ID)
	if err != nil {
		c.state.Store(int32(cwtypes.StateCrashed))
		return fmt.Errorf("controller start: %w", err)
	}
	c.handle = handle

	conn, err := ipc.NewConn(ipc.ConnConfig{
		RWC:        handle.Socket,
		SpillerDir: filepath.Join(c.opts.RuntimeDir, "outbox-"+c.opts.Spec.ID),
		Capacity:   1024,
		WALBytes:   256 * 1024 * 1024,
	})
	if err != nil {
		_ = handle.Close()
		c.state.Store(int32(cwtypes.StateCrashed))
		return fmt.Errorf("controller conn: %w", err)
	}
	c.conn = conn

	conn.OnMessage(c.handleMessage)
	conn.Start()

	// Send HELLO to prompt agent's HELLO_ACK.
	_ = c.send(ipc.MsgHello, ipc.HelloPayload{ProtocolVersion: ipc.ProtocolVersion, AgentID: c.opts.Spec.ID}, false)
	_ = c.send(ipc.MsgStartChild, ipc.StartChildPayload{
		Command:     c.opts.Spec.Command,
		Args:        c.opts.Spec.Args,
		Env:         c.opts.Spec.Env,
		WorkDir:     c.opts.Spec.WorkDir,
		StopTimeout: c.opts.Spec.StopTimeout.Milliseconds(),
	}, true)
	return nil
}

// Stop sends STOP_CHILD.
func (c *Controller) Stop(ctx context.Context) error {
	cur := cwtypes.State(c.state.Load())
	if cur == cwtypes.StateStopped || cur == cwtypes.StateFailed {
		return nil
	}
	c.state.Store(int32(cwtypes.StateStopping))
	return c.send(ipc.MsgStopChild, ipc.StopChildPayload{TimeoutMs: c.opts.Spec.StopTimeout.Milliseconds()}, true)
}

// Close tears everything down. Idempotent.
func (c *Controller) Close(ctx context.Context) error {
	if c.closed.Swap(true) {
		return c.closeErr
	}
	if c.conn != nil {
		c.closeErr = c.conn.Close(ctx)
	}
	if c.handle != nil {
		_ = c.handle.Close()
	}
	return c.closeErr
}

func (c *Controller) send(t ipc.MsgType, payload any, ack bool) error {
	var data []byte
	if payload != nil {
		b, err := ipc.EncodePayload(payload)
		if err != nil {
			return err
		}
		data = b
	}
	h := ipc.Header{MsgType: t, SeqNo: c.conn.Seqs().Next(), Length: uint32(len(data))}
	if ack {
		h.Flags |= ipc.FlagAckRequired
	}
	c.conn.Send(ipc.OutboxMessage{Header: h, Payload: data})
	return nil
}

func (c *Controller) handleMessage(msg ipc.OutboxMessage) {
	switch msg.Header.MsgType {
	case ipc.MsgHelloAck:
		// Handshake complete; nothing to record for now.
	case ipc.MsgChildStarted:
		var p ipc.ChildStartedPayload
		if err := ipc.DecodePayload(msg.Payload, &p); err == nil {
			c.mu.Lock()
			c.childPID = int(p.PID)
			c.mu.Unlock()
			c.state.Store(int32(cwtypes.StateRunning))
		}
	case ipc.MsgChildExited:
		var p ipc.ChildExitedPayload
		if err := ipc.DecodePayload(msg.Payload, &p); err == nil {
			if cwtypes.State(c.state.Load()) == cwtypes.StateStopping || p.ExitCode == 0 {
				c.state.Store(int32(cwtypes.StateStopped))
			} else {
				c.state.Store(int32(cwtypes.StateCrashed))
			}
		}
	case ipc.MsgChildError:
		c.state.Store(int32(cwtypes.StateCrashed))
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/controller/ -count=1 -race -v
```

Expected: the controller starts, runs `/bin/sh -c 'exit 0'`, and settles in `StateStopped`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/ internal/supervise/testhelpers.go
git commit -m "feat(controller): add Controller skeleton and start/stop flow"
```

---

## Task 18: Manager — Minimal Public API

**Files:**
- Create: `pkg/cliwrap/options.go`
- Create: `pkg/cliwrap/manager.go`
- Create: `pkg/cliwrap/process.go`
- Create: `pkg/cliwrap/manager_test.go`

- [ ] **Step 1: Write the failing integration test**

Create `pkg/cliwrap/manager_test.go`:

```go
package cliwrap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/cli-wrapper/cli-wrapper/internal/supervise"
)

func TestManager_RegisterAndRun(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)

	mgr, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	spec, err := NewSpec("exit0", "/bin/sh", "-c", "exit 0").
		WithStopTimeout(2 * time.Second).
		Build()
	require.NoError(t, err)

	h, err := mgr.Register(spec)
	require.NoError(t, err)

	require.NoError(t, h.Start(ctx))

	require.Eventually(t, func() bool {
		return h.Status().State == StateStopped
	}, 3*time.Second, 20*time.Millisecond)

	require.NoError(t, mgr.Shutdown(ctx))
}
```

- [ ] **Step 2: Run and confirm it fails**

Run:

```bash
go test ./pkg/cliwrap/ -run TestManager -count=1
```

Expected: undefined types.

- [ ] **Step 3: Implement Manager, options, and ProcessHandle**

Create `pkg/cliwrap/options.go`:

```go
package cliwrap

// ManagerOption mutates a Manager during construction.
type ManagerOption func(*Manager)

// WithAgentPath sets the absolute path to the cliwrap-agent binary.
func WithAgentPath(path string) ManagerOption {
	return func(m *Manager) { m.agentPath = path }
}

// WithRuntimeDir sets the directory for per-process state (WAL, logs).
func WithRuntimeDir(path string) ManagerOption {
	return func(m *Manager) { m.runtimeDir = path }
}
```

Create `pkg/cliwrap/process.go`:

```go
package cliwrap

import (
	"context"
	"sync"

	"github.com/cli-wrapper/cli-wrapper/internal/controller"
)

// ProcessHandle is the user-facing handle to a managed process.
type ProcessHandle interface {
	ID() string
	Spec() Spec
	Status() Status
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Close(ctx context.Context) error
}

type processHandle struct {
	spec Spec
	mgr  *Manager

	mu         sync.Mutex
	ctrl       *controller.Controller
	lastStatus Status
}

func (p *processHandle) ID() string { return p.spec.ID }

func (p *processHandle) Spec() Spec { return p.spec }

func (p *processHandle) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctrl == nil {
		if p.lastStatus.State != 0 {
			return p.lastStatus
		}
		return Status{State: StatePending}
	}
	return Status{
		State:    p.ctrl.State(),
		ChildPID: p.ctrl.ChildPID(),
	}
}

func (p *processHandle) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctrl != nil {
		return ErrProcessAlreadyRunning
	}
	ctrl, err := p.mgr.newController(p.spec)
	if err != nil {
		return err
	}
	p.ctrl = ctrl
	return ctrl.Start(ctx)
}

func (p *processHandle) Stop(ctx context.Context) error {
	p.mu.Lock()
	ctrl := p.ctrl
	p.mu.Unlock()
	if ctrl == nil {
		return ErrProcessNotRunning
	}
	return ctrl.Stop(ctx)
}

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
	if ctrl == nil {
		return nil
	}
	return ctrl.Close(ctx)
}
```

Create `pkg/cliwrap/manager.go`:

```go
package cliwrap

import (
	"context"
	"errors"
	"sync"

	"github.com/cli-wrapper/cli-wrapper/internal/controller"
	"github.com/cli-wrapper/cli-wrapper/internal/supervise"
)

// Manager is the top-level library entry point.
type Manager struct {
	agentPath  string
	runtimeDir string
	spawner    *supervise.Spawner

	mu      sync.Mutex
	handles map[string]*processHandle
	closed  bool
}

// NewManager constructs a Manager with the given options.
func NewManager(opts ...ManagerOption) (*Manager, error) {
	m := &Manager{handles: map[string]*processHandle{}}
	for _, opt := range opts {
		opt(m)
	}
	if m.agentPath == "" {
		return nil, errors.New("cliwrap: WithAgentPath is required")
	}
	if m.runtimeDir == "" {
		return nil, errors.New("cliwrap: WithRuntimeDir is required")
	}
	m.spawner = supervise.NewSpawner(supervise.SpawnerOptions{
		AgentPath:  m.agentPath,
		RuntimeDir: m.runtimeDir,
	})
	return m, nil
}

// Register validates and registers a process spec.
func (m *Manager) Register(spec Spec) (ProcessHandle, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrManagerClosed
	}
	if _, exists := m.handles[spec.ID]; exists {
		return nil, ErrProcessAlreadyExists
	}
	h := &processHandle{spec: spec, mgr: m}
	m.handles[spec.ID] = h
	return h, nil
}

// Shutdown stops every process and releases resources.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	handles := make([]*processHandle, 0, len(m.handles))
	for _, h := range m.handles {
		handles = append(handles, h)
	}
	m.mu.Unlock()

	for _, h := range handles {
		_ = h.Stop(ctx)
	}
	for _, h := range handles {
		_ = h.Close(ctx)
	}
	return nil
}

func (m *Manager) newController(spec Spec) (*controller.Controller, error) {
	return controller.NewController(controller.ControllerOptions{
		Spec:       spec,
		Spawner:    m.spawner,
		RuntimeDir: m.runtimeDir,
	})
}
```

- [ ] **Step 4: Run the integration test to verify it passes**

Run:

```bash
go test ./pkg/cliwrap/ -run TestManager -count=1 -race -v
```

Expected: the test starts an agent, runs `exit 0`, observes `StateStopped`, and shuts down cleanly.

- [ ] **Step 5: Commit**

```bash
git add pkg/cliwrap/options.go pkg/cliwrap/manager.go pkg/cliwrap/process.go pkg/cliwrap/manager_test.go
git commit -m "feat(cliwrap): add Manager public API with Register/Shutdown"
```

---

## Task 19: Integration Test — Crash Detection

**Files:**
- Create: `test/integration/crash_test.go`

- [ ] **Step 1: Write the test**

Create `test/integration/crash_test.go`:

```go
package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/cli-wrapper/cli-wrapper/internal/supervise"
	"github.com/cli-wrapper/cli-wrapper/pkg/cliwrap"
)

func TestIntegration_CrashDetection(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)
	root := findRoot(t)
	crasher := filepath.Join(root, "test/fixtures/bin/fixture-crasher")

	mgr, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	spec, err := cliwrap.NewSpec("crasher", crasher, "--after", "200ms", "--code", "17").
		WithStopTimeout(2 * time.Second).
		Build()
	require.NoError(t, err)

	h, err := mgr.Register(spec)
	require.NoError(t, err)
	require.NoError(t, h.Start(ctx))

	require.Eventually(t, func() bool {
		return h.Status().State == cliwrap.StateCrashed
	}, 3*time.Second, 20*time.Millisecond, "process should transition to Crashed after exit code 17")

	require.NoError(t, mgr.Shutdown(ctx))
}
```

Create `test/integration/helpers_test.go`:

```go
package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func findRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatal("go.mod not found")
		}
		wd = parent
	}
}
```

- [ ] **Step 2: Build fixtures first**

Run:

```bash
make fixtures
```

Expected: `test/fixtures/bin/fixture-crasher` exists.

- [ ] **Step 3: Run the integration test**

Run:

```bash
go test ./test/integration/ -run TestIntegration_CrashDetection -count=1 -race -v
```

Expected: test passes, crasher exits with code 17, process transitions to `StateCrashed`.

- [ ] **Step 4: Commit**

```bash
git add test/integration/
git commit -m "test(integration): add crash-detection integration test"
```

---

## Task 20: Integration Test — Leak Stress

**Files:**
- Create: `test/integration/leak_test.go`

- [ ] **Step 1: Write the stress test**

Create `test/integration/leak_test.go`:

```go
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/cli-wrapper/cli-wrapper/internal/supervise"
	"github.com/cli-wrapper/cli-wrapper/pkg/cliwrap"
)

func TestIntegration_NoLeaksAcrossCycles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping leak stress in -short mode")
	}
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)

	mgr, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, mgr.Shutdown(ctx))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for i := 0; i < 20; i++ {
		spec, err := cliwrap.NewSpec("leak-test", "/bin/sh", "-c", "exit 0").
			WithStopTimeout(time.Second).
			Build()
		require.NoError(t, err)

		h, err := mgr.Register(spec)
		require.NoError(t, err)
		require.NoError(t, h.Start(ctx))
		require.Eventually(t, func() bool {
			return h.Status().State == cliwrap.StateStopped
		}, 3*time.Second, 20*time.Millisecond)
		require.NoError(t, h.Close(ctx))

		// Unregister by re-creating the map entry on next iteration — for
		// the purpose of this leak test we reuse the same ID.
		mgr2 := mgr.(*cliwrap.Manager) // compile check
		_ = mgr2
	}
}
```

- [ ] **Step 2: Remove the compile-check hack**

The inline `mgr2 := mgr.(*cliwrap.Manager)` only works if `NewManager` returns the concrete type. Update the test to remove the hack and instead use a fresh ID for each iteration:

```go
package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/cli-wrapper/cli-wrapper/internal/supervise"
	"github.com/cli-wrapper/cli-wrapper/pkg/cliwrap"
)

func TestIntegration_NoLeaksAcrossCycles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping leak stress in -short mode")
	}
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)

	mgr, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, mgr.Shutdown(ctx))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("leak-test-%d", i)
		spec, err := cliwrap.NewSpec(id, "/bin/sh", "-c", "exit 0").
			WithStopTimeout(time.Second).
			Build()
		require.NoError(t, err)

		h, err := mgr.Register(spec)
		require.NoError(t, err)
		require.NoError(t, h.Start(ctx))
		require.Eventually(t, func() bool {
			return h.Status().State == cliwrap.StateStopped
		}, 3*time.Second, 20*time.Millisecond)
		require.NoError(t, h.Close(ctx))
	}
}
```

- [ ] **Step 3: Run the stress test**

Run:

```bash
go test ./test/integration/ -run TestIntegration_NoLeaksAcrossCycles -count=1 -race -v
```

Expected: 20 cycles complete with no goroutine leaks.

- [ ] **Step 4: Commit**

```bash
git add test/integration/leak_test.go
git commit -m "test(integration): add goroutine leak stress test"
```

---

## Verification Checklist

- [ ] `make fixtures && make test` passes on both Linux and macOS.
- [ ] Every integration test calls `goleak.VerifyNone(t)`.
- [ ] Controller state transitions match the spec table in Section 3.1.
- [ ] The agent dispatcher handles `MsgHello`, `MsgStartChild`, `MsgStopChild`, `MsgPing`, `MsgShutdown`.
- [ ] No `panic()` in production code; all errors wrap context.

## What This Plan Does NOT Cover

Deferred to later plans:

- **Full 4-layer crash unification** with explicit `CrashInfo` routing (kept minimal here).
- **Heartbeat timeout** detection (Plan 05).
- **Stream reader with burst/file mode** (Plan 05 hardening).
- **Restart policy loop** driven by the Manager (Plan 05).
- **EventBus wired through the Manager** and surfaced via `Subscribe` (Plan 03 or 05).
- **Resource monitoring and sandbox providers** (Plan 03).
- **Config loader and management CLI** (Plan 04).

Each follow-up plan will build on the stable contract established here.
