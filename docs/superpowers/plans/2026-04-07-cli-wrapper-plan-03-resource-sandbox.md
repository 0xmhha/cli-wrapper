# cli-wrapper Plan 03 — Resource Monitoring & Sandbox Plugin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add resource monitoring (per-process RSS / CPU + system-wide memory / load), enforcement actions, and the sandbox plugin interface with a noop provider and a `scriptdir` reference implementation.

**Architecture:** A `ResourceMonitor` owns a ticker goroutine that polls OS-specific collectors for every running child and the host itself. Samples flow through an `Evaluator` that tracks sustained breaches and dispatches actions (warn/throttle/kill) to the Manager. The sandbox system is a simple `Provider` interface plus a `scriptdir` implementation that exercises the script-injection pattern end-to-end.

**Tech Stack:**
- Go 1.22+
- Builds on Plans 01 and 02 (ipc, controller, manager, event bus)
- Linux: `/proc/*` parsing only
- macOS: `libproc` via cgo-free pure Go interfaces where possible; falls back to `ps` command in v1
- No external dependencies

**Context for the implementer:**
- The Manager must expose a thread-safe `ChildPIDs()` method returning running child PIDs; this plan adds it.
- `ResourceLimits` and `ExceedAction` were defined in Plan 02 (`pkg/cliwrap/types.go`).
- Strict TDD; every test calls `goleak.VerifyNone(t)` where goroutines exist.
- Commits follow conventional commits.

---

## File Structure

| File | Responsibility |
|------|----------------|
| `internal/resource/sample.go` | `Sample`, `SystemSample`, collector interfaces |
| `internal/resource/collector_linux.go` | `/proc`-based implementation |
| `internal/resource/collector_darwin.go` | `ps`-based fallback for macOS |
| `internal/resource/collector_test.go` | Platform-agnostic smoke tests |
| `internal/resource/evaluator.go` | Sustained-breach logic + action dispatch |
| `internal/resource/evaluator_test.go` | Table-driven breach tests |
| `internal/resource/monitor.go` | Ticker + registry integration |
| `internal/resource/monitor_test.go` | Monitor lifecycle tests |
| `pkg/sandbox/provider.go` | `Provider` and `Instance` interfaces |
| `pkg/sandbox/script.go` | `Script`, `NewEntrypointScript`, `shellQuote` |
| `pkg/sandbox/providers/noop/noop.go` | Pass-through provider |
| `pkg/sandbox/providers/scriptdir/scriptdir.go` | Temp-dir reference provider |
| `pkg/sandbox/providers/noop/noop_test.go` | Unit tests |
| `pkg/sandbox/providers/scriptdir/scriptdir_test.go` | Unit tests |
| `pkg/cliwrap/manager_resource.go` | Manager hooks for ChildPIDs + action callback |

---

## Task 1: Resource Sample Types & Interfaces

**Files:**
- Create: `internal/resource/sample.go`
- Create: `internal/resource/sample_test.go`

- [x] **Step 1: Write the failing test**

Create `internal/resource/sample_test.go`:

```go
package resource

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSample_Zero(t *testing.T) {
	s := Sample{}
	require.Equal(t, 0, s.PID)
	require.Equal(t, uint64(0), s.RSS)
}

func TestSystemSample_AvailableRatio(t *testing.T) {
	s := SystemSample{TotalMemory: 1000, AvailableMemory: 250}
	require.InDelta(t, 0.25, s.AvailableRatio(), 0.0001)
}

func TestSystemSample_AvailableRatio_ZeroTotal(t *testing.T) {
	require.Equal(t, 0.0, SystemSample{}.AvailableRatio())
}

func TestSample_Validate_Timestamp(t *testing.T) {
	s := Sample{SampledAt: time.Now()}
	require.False(t, s.SampledAt.IsZero())
}
```

- [x] **Step 2: Run and confirm it fails**

Run:

```bash
go test ./internal/resource/ -count=1
```

Expected: undefined types.

- [x] **Step 3: Implement the types**

Create `internal/resource/sample.go`:

```go
// Package resource implements per-process and system-wide resource sampling
// and enforcement.
package resource

import "time"

// Sample is one point-in-time reading for a single process.
type Sample struct {
	PID        int
	RSS        uint64  // bytes
	VMS        uint64  // bytes
	CPUPercent float64 // 0-100 normalized per core
	NumThreads int
	NumFDs     int
	SampledAt  time.Time
}

// ProcessCollector returns a Sample for the given PID.
type ProcessCollector interface {
	Collect(pid int) (Sample, error)
}

// SystemSample is one host-wide reading.
type SystemSample struct {
	TotalMemory     uint64
	AvailableMemory uint64
	LoadAvg1        float64
	LoadAvg5        float64
	NumCPU          int
	SampledAt       time.Time
}

// AvailableRatio returns AvailableMemory / TotalMemory, or 0 if TotalMemory is 0.
func (s SystemSample) AvailableRatio() float64 {
	if s.TotalMemory == 0 {
		return 0
	}
	return float64(s.AvailableMemory) / float64(s.TotalMemory)
}

// SystemCollector returns a system-wide sample.
type SystemCollector interface {
	Collect() (SystemSample, error)
}

// DefaultProcessCollector returns the platform's default process collector.
func DefaultProcessCollector() ProcessCollector { return defaultProcessCollector }

// DefaultSystemCollector returns the platform's default system collector.
func DefaultSystemCollector() SystemCollector { return defaultSystemCollector }
```

- [x] **Step 4: Run the tests**

Run:

```bash
go test ./internal/resource/ -count=1 -v
```

Expected: four tests pass.

- [x] **Step 5: Commit**

```bash
git add internal/resource/sample.go internal/resource/sample_test.go
git commit -m "feat(resource): add Sample, SystemSample and collector interfaces"
```

---

## Task 2: Linux Collectors (`/proc` Parsing)

**Files:**
- Create: `internal/resource/collector_linux.go`
- Create: `internal/resource/collector_linux_test.go`

- [x] **Step 1: Write the failing test**

Create `internal/resource/collector_linux_test.go`:

```go
//go:build linux

package resource

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessCollectorLinux_SelfPID(t *testing.T) {
	pc := DefaultProcessCollector()
	s, err := pc.Collect(os.Getpid())
	require.NoError(t, err)
	require.Greater(t, s.RSS, uint64(0))
	require.Greater(t, s.NumThreads, 0)
}

func TestSystemCollectorLinux_ReadsMeminfo(t *testing.T) {
	sc := DefaultSystemCollector()
	s, err := sc.Collect()
	require.NoError(t, err)
	require.Greater(t, s.TotalMemory, uint64(0))
	require.GreaterOrEqual(t, s.NumCPU, 1)
}
```

- [x] **Step 2: Run the test on Linux (and confirm it fails)**

Run:

```bash
go test ./internal/resource/ -count=1 -run TestProcessCollectorLinux
```

On Linux, expected: undefined. On macOS, the test is excluded by build tag.

- [x] **Step 3: Implement the Linux collectors**

Create `internal/resource/collector_linux.go`:

```go
//go:build linux

package resource

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	defaultProcessCollector ProcessCollector = linuxProcessCollector{}
	defaultSystemCollector  SystemCollector  = linuxSystemCollector{}
)

type linuxProcessCollector struct{}

func (linuxProcessCollector) Collect(pid int) (Sample, error) {
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	statusPath := fmt.Sprintf("/proc/%d/status", pid)

	statBytes, err := os.ReadFile(statPath)
	if err != nil {
		return Sample{}, fmt.Errorf("resource: read %s: %w", statPath, err)
	}
	// /proc/<pid>/stat: pid (comm) state ... the comm field may contain spaces,
	// so we parse relative to the LAST ')'.
	raw := string(statBytes)
	rParen := strings.LastIndex(raw, ")")
	if rParen < 0 {
		return Sample{}, fmt.Errorf("resource: malformed stat line for pid %d", pid)
	}
	fields := strings.Fields(raw[rParen+1:])
	if len(fields) < 22 {
		return Sample{}, fmt.Errorf("resource: stat line too short for pid %d", pid)
	}
	rssPages, _ := strconv.ParseUint(fields[21], 10, 64)
	numThreads, _ := strconv.Atoi(fields[17])

	s := Sample{
		PID:        pid,
		RSS:        rssPages * uint64(os.Getpagesize()),
		NumThreads: numThreads,
		SampledAt:  time.Now(),
	}

	// VmSize and open FDs from status and /proc/<pid>/fd.
	if status, err := os.Open(statusPath); err == nil {
		scanner := bufio.NewScanner(status)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "VmSize:") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					if kb, err := strconv.ParseUint(parts[1], 10, 64); err == nil {
						s.VMS = kb * 1024
					}
				}
				break
			}
		}
		_ = status.Close()
	}

	if entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid)); err == nil {
		s.NumFDs = len(entries)
	}

	return s, nil
}

type linuxSystemCollector struct{}

func (linuxSystemCollector) Collect() (SystemSample, error) {
	s := SystemSample{NumCPU: runtime.NumCPU(), SampledAt: time.Now()}

	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return s, fmt.Errorf("resource: open meminfo: %w", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		val, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			continue
		}
		switch parts[0] {
		case "MemTotal:":
			s.TotalMemory = val * 1024
		case "MemAvailable:":
			s.AvailableMemory = val * 1024
		}
	}

	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		parts := strings.Fields(string(b))
		if len(parts) >= 2 {
			if v, err := strconv.ParseFloat(parts[0], 64); err == nil {
				s.LoadAvg1 = v
			}
			if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
				s.LoadAvg5 = v
			}
		}
	}

	return s, nil
}
```

- [x] **Step 4: Run the tests on Linux**

Run (on Linux):

```bash
go test ./internal/resource/ -count=1 -v
```

Expected: two linux-only tests pass.

- [x] **Step 5: Commit**

```bash
git add internal/resource/collector_linux.go internal/resource/collector_linux_test.go
git commit -m "feat(resource): add Linux /proc-based collectors"
```

---

## Task 3: Darwin Collectors

**Files:**
- Create: `internal/resource/collector_darwin.go`
- Create: `internal/resource/collector_darwin_test.go`

- [x] **Step 1: Write the failing test**

Create `internal/resource/collector_darwin_test.go`:

```go
//go:build darwin

package resource

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessCollectorDarwin_SelfPID(t *testing.T) {
	pc := DefaultProcessCollector()
	s, err := pc.Collect(os.Getpid())
	require.NoError(t, err)
	require.Greater(t, s.RSS, uint64(0))
}

func TestSystemCollectorDarwin_SysctlMemory(t *testing.T) {
	sc := DefaultSystemCollector()
	s, err := sc.Collect()
	require.NoError(t, err)
	require.Greater(t, s.TotalMemory, uint64(0))
}
```

- [x] **Step 2: Run and confirm it fails**

Run (on macOS):

```bash
go test ./internal/resource/ -count=1 -run TestProcessCollectorDarwin
```

- [x] **Step 3: Implement Darwin collectors**

Create `internal/resource/collector_darwin.go`:

```go
//go:build darwin

package resource

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	defaultProcessCollector ProcessCollector = darwinProcessCollector{}
	defaultSystemCollector  SystemCollector  = darwinSystemCollector{}
)

type darwinProcessCollector struct{}

// Collect shells out to ps for v1. A follow-up plan may replace this with
// libproc via cgo for lower overhead.
func (darwinProcessCollector) Collect(pid int) (Sample, error) {
	// Note: macOS ps(1) does not support nlwp. Thread count requires
	// proc_pidinfo (cgo); deferred to v2.
	cmd := exec.Command("ps", "-o", "rss=,vsz=,%cpu=", "-p", strconv.Itoa(pid))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return Sample{}, fmt.Errorf("resource: ps: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(stdout.String()))
	if len(fields) < 3 {
		return Sample{}, fmt.Errorf("resource: ps output too short: %q", stdout.String())
	}
	rssKB, _ := strconv.ParseUint(fields[0], 10, 64)
	vszKB, _ := strconv.ParseUint(fields[1], 10, 64)
	cpu, _ := strconv.ParseFloat(fields[2], 64)
	return Sample{
		PID:        pid,
		RSS:        rssKB * 1024,
		VMS:        vszKB * 1024,
		CPUPercent: cpu,
		NumThreads: 0, // Thread count requires proc_pidinfo (cgo); deferred to v2
		SampledAt:  time.Now(),
	}, nil
}

type darwinSystemCollector struct{}

func (darwinSystemCollector) Collect() (SystemSample, error) {
	s := SystemSample{NumCPU: runtime.NumCPU(), SampledAt: time.Now()}

	// Total memory via sysctl
	if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
		if v, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64); err == nil {
			s.TotalMemory = v
		}
	}

	// Available memory via vm_stat (free + inactive + speculative).
	// Using vm.page_free_count alone underestimates available memory on macOS
	// because it ignores inactive and speculative pages that the OS can reclaim.
	if out, err := exec.Command("vm_stat").Output(); err == nil {
		s.AvailableMemory = parseVMStat(string(out))
	}

	// Load average
	if out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output(); err == nil {
		// format: "{ 1.23 0.45 0.67 }"
		fields := strings.Fields(strings.Trim(strings.TrimSpace(string(out)), "{}"))
		if len(fields) >= 2 {
			if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
				s.LoadAvg1 = v
			}
			if v, err := strconv.ParseFloat(fields[1], 64); err == nil {
				s.LoadAvg5 = v
			}
		}
	}

	return s, nil
}

// parseVMStat parses vm_stat output and sums free + inactive + speculative pages.
func parseVMStat(output string) uint64 {
	pageSize := uint64(4096)
	var free, inactive, speculative uint64
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "Pages free:") {
			free = extractVMStatValue(line)
		} else if strings.Contains(line, "Pages inactive:") {
			inactive = extractVMStatValue(line)
		} else if strings.Contains(line, "Pages speculative:") {
			speculative = extractVMStatValue(line)
		}
	}
	return (free + inactive + speculative) * pageSize
}

// extractVMStatValue extracts the numeric value from a vm_stat line.
func extractVMStatValue(line string) uint64 {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return 0
	}
	s := strings.TrimRight(parts[len(parts)-1], ".")
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}
```

- [x] **Step 4: Run the tests on macOS**

Run (on macOS):

```bash
go test ./internal/resource/ -count=1 -v
```

Expected: darwin-only tests pass.

- [x] **Step 5: Commit**

```bash
git add internal/resource/collector_darwin.go internal/resource/collector_darwin_test.go
git commit -m "feat(resource): add macOS collectors via sysctl and ps"
```

---

## Task 4: Evaluator — Sustained Breach Detection

**Files:**
- Create: `internal/resource/evaluator.go`
- Create: `internal/resource/evaluator_test.go`

- [x] **Step 1: Write the failing tests**

Create `internal/resource/evaluator_test.go`:

```go
package resource

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cli-wrapper/cli-wrapper/pkg/cliwrap"
)

func TestEvaluator_EmitsActionAfterSustainedBreach(t *testing.T) {
	var actions []Action
	ev := NewEvaluator(EvaluatorOptions{
		OnAction: func(a Action) { actions = append(actions, a) },
	})

	ev.RegisterLimits("p1", cliwrap.ResourceLimits{MaxRSS: 100, OnExceed: cliwrap.ExceedKill})

	// Two breaches are below the threshold of 3.
	ev.Evaluate("p1", Sample{PID: 1, RSS: 500})
	ev.Evaluate("p1", Sample{PID: 1, RSS: 500})
	require.Empty(t, actions)

	// Third breach should trigger.
	ev.Evaluate("p1", Sample{PID: 1, RSS: 500})
	require.Len(t, actions, 1)
	require.Equal(t, "p1", actions[0].ProcessID)
	require.Equal(t, cliwrap.ExceedKill, actions[0].ExceedAction)
	require.Equal(t, "rss", actions[0].Kind)
}

func TestEvaluator_ResetsOnGoodSample(t *testing.T) {
	var actions []Action
	ev := NewEvaluator(EvaluatorOptions{
		OnAction: func(a Action) { actions = append(actions, a) },
	})
	ev.RegisterLimits("p1", cliwrap.ResourceLimits{MaxRSS: 100, OnExceed: cliwrap.ExceedKill})

	ev.Evaluate("p1", Sample{RSS: 500})
	ev.Evaluate("p1", Sample{RSS: 500})
	ev.Evaluate("p1", Sample{RSS: 50}) // resets
	ev.Evaluate("p1", Sample{RSS: 500})
	require.Empty(t, actions)
}

func TestEvaluator_CPUNeedsMoreBreaches(t *testing.T) {
	var actions []Action
	ev := NewEvaluator(EvaluatorOptions{
		OnAction: func(a Action) { actions = append(actions, a) },
	})
	ev.RegisterLimits("p1", cliwrap.ResourceLimits{MaxCPUPercent: 50, OnExceed: cliwrap.ExceedWarn})

	// 9 breaches should still be below the CPU threshold of 10.
	for i := 0; i < 9; i++ {
		ev.Evaluate("p1", Sample{CPUPercent: 99})
	}
	require.Empty(t, actions)

	ev.Evaluate("p1", Sample{CPUPercent: 99}) // 10th
	require.Len(t, actions, 1)
	require.Equal(t, "cpu", actions[0].Kind)
}
```

- [x] **Step 2: Run and confirm it fails**

Run:

```bash
go test ./internal/resource/ -run TestEvaluator -count=1
```

Expected: undefined.

- [x] **Step 3: Implement the Evaluator**

Create `internal/resource/evaluator.go`:

```go
package resource

import (
	"sync"

	"github.com/cli-wrapper/cli-wrapper/pkg/cliwrap"
)

// Action describes an enforcement decision.
type Action struct {
	ProcessID    string
	Kind         string // "rss" | "cpu"
	Current      float64
	Limit        float64
	ExceedAction cliwrap.ExceedAction
}

// EvaluatorOptions parameterises NewEvaluator.
type EvaluatorOptions struct {
	// OnAction is invoked (synchronously) when a sustained breach is detected.
	OnAction func(Action)

	// RSSBreachThreshold sets the consecutive breach count for memory.
	// Default 3.
	RSSBreachThreshold int

	// CPUBreachThreshold sets the consecutive breach count for CPU.
	// Default 10 (CPU samples are noisier).
	CPUBreachThreshold int
}

// Evaluator tracks per-process limit breaches and fires actions.
type Evaluator struct {
	mu        sync.Mutex
	opts      EvaluatorOptions
	limits    map[string]cliwrap.ResourceLimits
	rssCount  map[string]int
	cpuCount  map[string]int
}

// NewEvaluator returns an Evaluator.
func NewEvaluator(opts EvaluatorOptions) *Evaluator {
	if opts.RSSBreachThreshold <= 0 {
		opts.RSSBreachThreshold = 3
	}
	if opts.CPUBreachThreshold <= 0 {
		opts.CPUBreachThreshold = 10
	}
	return &Evaluator{
		opts:     opts,
		limits:   make(map[string]cliwrap.ResourceLimits),
		rssCount: make(map[string]int),
		cpuCount: make(map[string]int),
	}
}

// RegisterLimits associates limits with a process ID.
func (e *Evaluator) RegisterLimits(id string, l cliwrap.ResourceLimits) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.limits[id] = l
}

// Unregister removes a process ID.
func (e *Evaluator) Unregister(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.limits, id)
	delete(e.rssCount, id)
	delete(e.cpuCount, id)
}

// Evaluate checks sample against the limits of id and may fire actions.
func (e *Evaluator) Evaluate(id string, sample Sample) {
	e.mu.Lock()
	limits, ok := e.limits[id]
	if !ok {
		e.mu.Unlock()
		return
	}

	var fired []Action

	if limits.MaxRSS > 0 {
		if sample.RSS > limits.MaxRSS {
			e.rssCount[id]++
			if e.rssCount[id] >= e.opts.RSSBreachThreshold {
				fired = append(fired, Action{
					ProcessID:    id,
					Kind:         "rss",
					Current:      float64(sample.RSS),
					Limit:        float64(limits.MaxRSS),
					ExceedAction: limits.OnExceed,
				})
				e.rssCount[id] = 0
			}
		} else {
			e.rssCount[id] = 0
		}
	}

	if limits.MaxCPUPercent > 0 {
		if sample.CPUPercent > limits.MaxCPUPercent {
			e.cpuCount[id]++
			if e.cpuCount[id] >= e.opts.CPUBreachThreshold {
				fired = append(fired, Action{
					ProcessID:    id,
					Kind:         "cpu",
					Current:      sample.CPUPercent,
					Limit:        limits.MaxCPUPercent,
					ExceedAction: limits.OnExceed,
				})
				e.cpuCount[id] = 0
			}
		} else {
			e.cpuCount[id] = 0
		}
	}

	e.mu.Unlock()

	if e.opts.OnAction != nil {
		for _, a := range fired {
			e.opts.OnAction(a)
		}
	}
}
```

- [x] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/resource/ -run TestEvaluator -count=1 -v -race
```

Expected: three tests pass.

- [x] **Step 5: Commit**

```bash
git add internal/resource/evaluator.go internal/resource/evaluator_test.go
git commit -m "feat(resource): add Evaluator with sustained-breach detection"
```

---

## Task 5: Monitor — Ticker & Lifecycle

**Files:**
- Create: `internal/resource/monitor.go`
- Create: `internal/resource/monitor_test.go`

- [x] **Step 1: Write the failing test**

Create `internal/resource/monitor_test.go`:

```go
package resource

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

type fakeSource struct{ pids []int }

func (f *fakeSource) ChildPIDs() map[string]int {
	out := make(map[string]int, len(f.pids))
	for i, p := range f.pids {
		out[idFor(i)] = p
	}
	return out
}

func idFor(i int) string { return "p" + strconv.Itoa(i) }

func TestMonitor_PollsAndStops(t *testing.T) {
	defer goleak.VerifyNone(t)

	var samples int
	src := &fakeSource{pids: []int{os.Getpid()}}

	m := NewMonitor(MonitorOptions{
		Interval:        20 * time.Millisecond,
		Source:          src,
		ProcessCollector: DefaultProcessCollector(),
		SystemCollector:  DefaultSystemCollector(),
		OnSample: func(id string, s Sample) { samples++ },
	})

	m.Start()
	time.Sleep(80 * time.Millisecond)
	m.Stop()

	require.GreaterOrEqual(t, samples, 2)
}
```

- [x] **Step 2: Run to confirm it fails**

Run:

```bash
go test ./internal/resource/ -run TestMonitor -count=1
```

Expected: undefined.

- [x] **Step 3: Implement Monitor**

Create `internal/resource/monitor.go`:

```go
package resource

import (
	"sync"
	"time"
)

// PIDSource is any object that can report the current set of (id, PID) pairs
// that should be sampled.
type PIDSource interface {
	ChildPIDs() map[string]int
}

// MonitorOptions configures a Monitor.
type MonitorOptions struct {
	Interval         time.Duration
	Source           PIDSource
	ProcessCollector ProcessCollector
	SystemCollector  SystemCollector
	OnSample         func(id string, s Sample)
	OnSystem         func(s SystemSample)
}

// Monitor periodically polls every child process in its Source.
type Monitor struct {
	opts MonitorOptions
	stop chan struct{}
	wg   sync.WaitGroup
	once sync.Once
}

// NewMonitor returns a Monitor.
func NewMonitor(opts MonitorOptions) *Monitor {
	if opts.Interval <= 0 {
		opts.Interval = 5 * time.Second
	}
	return &Monitor{opts: opts, stop: make(chan struct{})}
}

// Start launches the ticker goroutine. Calling Start more than once is a no-op.
func (m *Monitor) Start() {
	m.once.Do(func() {
		m.wg.Add(1)
		go m.loop()
	})
}

// Stop halts the ticker and waits for the goroutine to exit.
func (m *Monitor) Stop() {
	close(m.stop)
	m.wg.Wait()
}

func (m *Monitor) loop() {
	defer m.wg.Done()
	t := time.NewTicker(m.opts.Interval)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			m.tick()
		}
	}
}

func (m *Monitor) tick() {
	if m.opts.Source == nil {
		return
	}
	for id, pid := range m.opts.Source.ChildPIDs() {
		if pid <= 0 || m.opts.ProcessCollector == nil {
			continue
		}
		s, err := m.opts.ProcessCollector.Collect(pid)
		if err != nil {
			continue
		}
		if m.opts.OnSample != nil {
			m.opts.OnSample(id, s)
		}
	}
	if m.opts.SystemCollector != nil {
		if s, err := m.opts.SystemCollector.Collect(); err == nil && m.opts.OnSystem != nil {
			m.opts.OnSystem(s)
		}
	}
}
```

- [x] **Step 4: Run the tests**

Run:

```bash
go test ./internal/resource/ -run TestMonitor -count=1 -v -race
```

Expected: test passes, no goroutine leaks.

- [x] **Step 5: Commit**

```bash
git add internal/resource/monitor.go internal/resource/monitor_test.go
git commit -m "feat(resource): add Monitor with ticker-driven sampling"
```

---

## Task 6: Manager Integration — ChildPIDs

**Files:**
- Modify: `pkg/cliwrap/manager.go`
- Create: `pkg/cliwrap/manager_resource.go`
- Create: `pkg/cliwrap/manager_resource_test.go`

- [x] **Step 1: Write the failing test**

Create `pkg/cliwrap/manager_resource_test.go`:

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

func TestManager_ChildPIDs(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)

	mgr, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	spec, err := NewSpec("sleeper", "/bin/sh", "-c", "sleep 0.5").
		WithStopTimeout(2 * time.Second).
		Build()
	require.NoError(t, err)

	h, err := mgr.Register(spec)
	require.NoError(t, err)
	require.NoError(t, h.Start(ctx))

	require.Eventually(t, func() bool {
		return len(mgr.ChildPIDs()) > 0
	}, 2*time.Second, 20*time.Millisecond)

	require.Eventually(t, func() bool {
		return h.Status().State == StateStopped
	}, 3*time.Second, 20*time.Millisecond)

	require.NoError(t, mgr.Shutdown(ctx))
}
```

- [x] **Step 2: Run to confirm it fails**

Run:

```bash
go test ./pkg/cliwrap/ -run TestManager_ChildPIDs -count=1
```

Expected: `mgr.ChildPIDs undefined`.

- [x] **Step 3: Add `ChildPIDs` to the Manager**

Create `pkg/cliwrap/manager_resource.go`:

```go
package cliwrap

// ChildPIDs returns a snapshot of running (process ID → child PID) pairs.
// Processes that are not in StateRunning are omitted.
func (m *Manager) ChildPIDs() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int, len(m.handles))
	for id, h := range m.handles {
		h.mu.Lock()
		if h.ctrl != nil && h.ctrl.State() == StateRunning {
			out[id] = h.ctrl.ChildPID()
		}
		h.mu.Unlock()
	}
	return out
}
```

- [x] **Step 4: Run the test**

Run:

```bash
go test ./pkg/cliwrap/ -run TestManager_ChildPIDs -count=1 -v -race
```

Expected: test passes.

- [x] **Step 5: Commit**

```bash
git add pkg/cliwrap/manager_resource.go pkg/cliwrap/manager_resource_test.go
git commit -m "feat(cliwrap): expose ChildPIDs for resource monitor integration"
```

---

## Task 7: Sandbox Provider Interface

**Files:**
- Create: `pkg/sandbox/provider.go`
- Create: `pkg/sandbox/provider_test.go`

- [x] **Step 1: Write the failing test**

Create `pkg/sandbox/provider_test.go`:

```go
package sandbox

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cli-wrapper/cli-wrapper/internal/cwtypes"
)

type fakeInstance struct {
	id string
	cmd *exec.Cmd
}

func (f *fakeInstance) ID() string { return f.id }
func (f *fakeInstance) Exec(cmd string, args []string, env map[string]string) (*exec.Cmd, error) {
	return exec.Command(cmd, args...), nil
}
func (f *fakeInstance) Teardown(ctx context.Context) error { return nil }

type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }
func (fakeProvider) Prepare(ctx context.Context, spec cwtypes.SandboxSpec, scripts []Script) (Instance, error) {
	return &fakeInstance{id: "fake-1"}, nil
}

func TestProviderInterface_AcceptsFake(t *testing.T) {
	var p Provider = fakeProvider{}
	inst, err := p.Prepare(context.Background(), cwtypes.SandboxSpec{}, nil)
	require.NoError(t, err)
	require.Equal(t, "fake-1", inst.ID())

	cmd, err := inst.Exec("/bin/echo", []string{"hi"}, nil)
	require.NoError(t, err)
	require.NotNil(t, cmd)
	require.NoError(t, inst.Teardown(context.Background()))
}
```

- [x] **Step 2: Run and confirm it fails**

Run:

```bash
go test ./pkg/sandbox/ -count=1
```

Expected: undefined.

- [x] **Step 3: Implement the interface**

Create `pkg/sandbox/provider.go`:

```go
// Package sandbox defines the plugin interface for isolating managed CLI processes.
package sandbox

import (
	"context"
	"os/exec"

	"github.com/cli-wrapper/cli-wrapper/internal/cwtypes"
)

// Provider prepares isolated environments in which child CLI processes can run.
// Implementations MUST be safe for concurrent use.
type Provider interface {
	// Name identifies the provider; must be unique at the Manager level.
	Name() string

	// Prepare creates a new isolated environment and returns an Instance.
	// The Instance is owned by the caller, which must call Teardown after use.
	Prepare(ctx context.Context, spec cwtypes.SandboxSpec, scripts []Script) (Instance, error)
}

// Instance is a prepared sandbox that can execute exactly one CLI invocation.
type Instance interface {
	// ID identifies this instance (provider-defined).
	ID() string

	// Exec returns an exec.Cmd that, when run OUTSIDE the sandbox by the agent,
	// launches cmd/args inside the isolated environment.
	Exec(cmd string, args []string, env map[string]string) (*exec.Cmd, error)

	// Teardown releases resources associated with this instance.
	Teardown(ctx context.Context) error
}
```

- [x] **Step 4: Run the test**

Run:

```bash
go test ./pkg/sandbox/ -count=1 -v
```

Expected: test passes.

- [x] **Step 5: Commit**

```bash
git add pkg/sandbox/provider.go pkg/sandbox/provider_test.go
git commit -m "feat(sandbox): add Provider and Instance interfaces"
```

---

## Task 8: Script Injection Helpers

**Files:**
- Create: `pkg/sandbox/script.go`
- Create: `pkg/sandbox/script_test.go`

- [x] **Step 1: Write the failing test**

Create `pkg/sandbox/script_test.go`:

```go
package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewEntrypointScript(t *testing.T) {
	s := NewEntrypointScript("/bin/echo", []string{"hello", "world"}, map[string]string{"X": "1"})
	body := string(s.Contents)

	require.True(t, strings.HasPrefix(body, "#!/bin/sh"))
	require.Contains(t, body, "export X=1")
	require.Contains(t, body, "exec /bin/echo hello world")
	require.Equal(t, "/cliwrap/entrypoint.sh", s.InnerPath)
	require.Equal(t, os.FileMode(0o755), s.Mode)
}

func TestShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"simple", "simple"},
		{"has space", "'has space'"},
		{"has'quote", `'has'\''quote'`},
		{"", "''"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, shellQuote(c.in))
	}
}

func TestWriteInto(t *testing.T) {
	dir := t.TempDir()
	s := Script{InnerPath: "/cliwrap/run.sh", Mode: 0o755, Contents: []byte("echo ok\n")}

	require.NoError(t, s.WriteInto(dir))
	b, err := os.ReadFile(filepath.Join(dir, "cliwrap/run.sh"))
	require.NoError(t, err)
	require.Equal(t, "echo ok\n", string(b))
}
```

- [x] **Step 2: Run and confirm it fails**

Run:

```bash
go test ./pkg/sandbox/ -run TestNewEntrypointScript -count=1
```

Expected: undefined.

- [x] **Step 3: Implement the helpers**

Create `pkg/sandbox/script.go`:

```go
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Script is a file that a Provider copies into the sandbox before execution.
type Script struct {
	InnerPath string      // path relative to the sandbox root
	Mode      os.FileMode // file mode
	Contents  []byte      // literal bytes
}

// WriteInto writes the script into root, creating parent directories as needed.
// InnerPath is interpreted relative to root; a leading slash is ignored.
func (s Script) WriteInto(root string) error {
	rel := strings.TrimPrefix(s.InnerPath, "/")
	dst := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return fmt.Errorf("sandbox: mkdir %s: %w", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, s.Contents, s.Mode); err != nil {
		return fmt.Errorf("sandbox: write %s: %w", dst, err)
	}
	// Ensure the executable bit is honored even if umask would clip it.
	if err := os.Chmod(dst, s.Mode); err != nil {
		return fmt.Errorf("sandbox: chmod %s: %w", dst, err)
	}
	return nil
}

// NewEntrypointScript builds a POSIX-sh script that exports env vars and
// execs cmd with args.
func NewEntrypointScript(cmd string, args []string, env map[string]string) Script {
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -e\n")
	for k, v := range env {
		fmt.Fprintf(&b, "export %s=%s\n", k, shellQuote(v))
	}
	b.WriteString("exec ")
	b.WriteString(cmd)
	for _, a := range args {
		b.WriteString(" ")
		b.WriteString(shellQuote(a))
	}
	b.WriteString("\n")
	return Script{
		InnerPath: "/cliwrap/entrypoint.sh",
		Mode:      0o755,
		Contents:  []byte(b.String()),
	}
}

// shellQuote wraps s in single quotes, escaping any embedded single quotes.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n'\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
```

- [x] **Step 4: Run the tests**

Run:

```bash
go test ./pkg/sandbox/ -count=1 -v
```

Expected: all tests pass.

- [x] **Step 5: Commit**

```bash
git add pkg/sandbox/script.go pkg/sandbox/script_test.go
git commit -m "feat(sandbox): add Script type and shell-quoting helpers"
```

---

## Task 9: Noop Provider

**Files:**
- Create: `pkg/sandbox/providers/noop/noop.go`
- Create: `pkg/sandbox/providers/noop/noop_test.go`

- [x] **Step 1: Write the failing test**

Create `pkg/sandbox/providers/noop/noop_test.go`:

```go
package noop

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cli-wrapper/cli-wrapper/internal/cwtypes"
	"github.com/cli-wrapper/cli-wrapper/pkg/sandbox"
)

func TestNoop_Lifecycle(t *testing.T) {
	p := New()
	require.Equal(t, "noop", p.Name())

	inst, err := p.Prepare(context.Background(), cwtypes.SandboxSpec{}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, inst.ID())

	cmd, err := inst.Exec("/bin/echo", []string{"hello"}, map[string]string{"X": "1"})
	require.NoError(t, err)
	require.Equal(t, "/bin/echo", cmd.Path)
	require.Contains(t, cmd.Env, "X=1")

	require.NoError(t, inst.Teardown(context.Background()))
}
```

- [x] **Step 2: Run to confirm it fails**

Run:

```bash
go test ./pkg/sandbox/providers/noop/ -count=1
```

Expected: `undefined: New`.

- [x] **Step 3: Implement noop**

Create `pkg/sandbox/providers/noop/noop.go`:

```go
// Package noop provides a pass-through sandbox provider.
package noop

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"

	"github.com/cli-wrapper/cli-wrapper/internal/cwtypes"
	"github.com/cli-wrapper/cli-wrapper/pkg/sandbox"
)

// Provider is the no-op provider. It performs no isolation and exists only
// to satisfy the interface when no sandbox is requested.
type Provider struct {
	counter atomic.Int64
}

// New returns a Provider.
func New() *Provider { return &Provider{} }

// Name returns "noop".
func (p *Provider) Name() string { return "noop" }

// Prepare returns an Instance that runs commands directly.
func (p *Provider) Prepare(ctx context.Context, spec cwtypes.SandboxSpec, scripts []sandbox.Script) (sandbox.Instance, error) {
	id := "noop-" + strconv.FormatInt(p.counter.Add(1), 10)
	return &instance{id: id}, nil
}

type instance struct {
	id string
}

func (i *instance) ID() string { return i.id }

func (i *instance) Exec(cmd string, args []string, env map[string]string) (*exec.Cmd, error) {
	c := exec.Command(cmd, args...)
	c.Env = append(os.Environ(), envSlice(env)...)
	return c, nil
}

func (i *instance) Teardown(ctx context.Context) error { return nil }

func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
```

- [x] **Step 4: Run the tests**

Run:

```bash
go test ./pkg/sandbox/providers/noop/ -count=1 -v
```

Expected: test passes.

- [x] **Step 5: Commit**

```bash
git add pkg/sandbox/providers/noop/
git commit -m "feat(sandbox): add noop provider"
```

---

## Task 10: Scriptdir Reference Provider

**Files:**
- Create: `pkg/sandbox/providers/scriptdir/scriptdir.go`
- Create: `pkg/sandbox/providers/scriptdir/scriptdir_test.go`

- [x] **Step 1: Write the failing test**

Create `pkg/sandbox/providers/scriptdir/scriptdir_test.go`:

```go
package scriptdir

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cli-wrapper/cli-wrapper/internal/cwtypes"
	"github.com/cli-wrapper/cli-wrapper/pkg/sandbox"
)

func TestScriptdir_EndToEnd(t *testing.T) {
	p := New(Options{BaseDir: t.TempDir()})
	require.Equal(t, "scriptdir", p.Name())

	script := sandbox.NewEntrypointScript("/bin/echo", []string{"hello", "sandbox"}, map[string]string{"X": "1"})

	inst, err := p.Prepare(context.Background(), cwtypes.SandboxSpec{}, []sandbox.Script{script})
	require.NoError(t, err)
	defer inst.Teardown(context.Background())

	// The script file must exist inside the instance dir.
	dir := inst.(*instance).dir
	require.FileExists(t, filepath.Join(dir, "cliwrap/entrypoint.sh"))

	cmd, err := inst.Exec("/bin/echo", []string{"hello", "sandbox"}, map[string]string{"X": "1"})
	require.NoError(t, err)

	var out bytes.Buffer
	cmd.Stdout = &out
	require.NoError(t, cmd.Run())
	require.Contains(t, out.String(), "hello sandbox")
}
```

- [x] **Step 2: Run to confirm it fails**

Run:

```bash
go test ./pkg/sandbox/providers/scriptdir/ -count=1
```

Expected: undefined.

- [x] **Step 3: Implement scriptdir**

Create `pkg/sandbox/providers/scriptdir/scriptdir.go`:

```go
// Package scriptdir is a reference sandbox provider that writes scripts into
// a temp directory and runs them via /bin/sh. It does NOT provide isolation
// and exists only to demonstrate the script-injection pattern.
package scriptdir

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cli-wrapper/cli-wrapper/internal/cwtypes"
	"github.com/cli-wrapper/cli-wrapper/pkg/sandbox"
)

// Options configures a scriptdir.Provider.
type Options struct {
	BaseDir string
}

// Provider is the scriptdir reference provider.
type Provider struct {
	opts Options
}

// New returns a scriptdir provider.
func New(opts Options) *Provider {
	if opts.BaseDir == "" {
		opts.BaseDir = os.TempDir()
	}
	return &Provider{opts: opts}
}

// Name returns "scriptdir".
func (p *Provider) Name() string { return "scriptdir" }

// Prepare creates a temp directory, writes every script into it, and returns
// an Instance whose Exec runs the entrypoint via /bin/sh.
func (p *Provider) Prepare(ctx context.Context, spec cwtypes.SandboxSpec, scripts []sandbox.Script) (sandbox.Instance, error) {
	if err := os.MkdirAll(p.opts.BaseDir, 0o700); err != nil {
		return nil, fmt.Errorf("scriptdir: mkdir base: %w", err)
	}
	dir, err := os.MkdirTemp(p.opts.BaseDir, "scriptdir-*")
	if err != nil {
		return nil, fmt.Errorf("scriptdir: mkdtemp: %w", err)
	}

	var entrypoint string
	for _, s := range scripts {
		if err := s.WriteInto(dir); err != nil {
			_ = os.RemoveAll(dir)
			return nil, err
		}
		if s.InnerPath == "/cliwrap/entrypoint.sh" {
			entrypoint = filepath.Join(dir, "cliwrap/entrypoint.sh")
		}
	}
	if entrypoint == "" {
		_ = os.RemoveAll(dir)
		return nil, errors.New("scriptdir: no entrypoint script provided")
	}

	return &instance{dir: dir, entrypoint: entrypoint}, nil
}

type instance struct {
	dir        string
	entrypoint string
}

func (i *instance) ID() string { return filepath.Base(i.dir) }

func (i *instance) Exec(cmd string, args []string, env map[string]string) (*exec.Cmd, error) {
	// The entrypoint script encodes cmd/args/env; callers ignore its inputs.
	c := exec.Command("/bin/sh", i.entrypoint)
	c.Env = append(os.Environ(), envSlice(env)...)
	return c, nil
}

func (i *instance) Teardown(ctx context.Context) error {
	return os.RemoveAll(i.dir)
}

func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
```

- [x] **Step 4: Run the tests**

Run:

```bash
go test ./pkg/sandbox/providers/scriptdir/ -count=1 -v
```

Expected: test passes, "hello sandbox" is captured.

- [x] **Step 5: Commit**

```bash
git add pkg/sandbox/providers/scriptdir/
git commit -m "feat(sandbox): add scriptdir reference provider"
```

---

## Verification Checklist

- [ ] `go test ./internal/resource/... -race -v` passes on the host OS.
- [ ] `go test ./pkg/sandbox/... -race -v` passes on all platforms.
- [ ] Manager's `ChildPIDs()` returns an up-to-date map during a long-running child.
- [ ] The `scriptdir` provider leaves no temp directory behind after `Teardown`.
- [ ] Evaluator's sustained-breach thresholds match the spec (3 for RSS, 10 for CPU).

## What This Plan Does NOT Cover

- **Throttle action implementation** (SIGSTOP/SIGCONT loop) — Plan 05.
- **System budget enforcement / StartDeferred path** — Plan 05.
- **Manager integration for Monitor, Evaluator, and SandboxProvider** — wiring occurs in Plan 05.
- **Resource collectors using libproc** — future optimization.
- **Sandbox providers beyond noop/scriptdir** (bwrap, docker, nsjail) — external modules; out of scope.
