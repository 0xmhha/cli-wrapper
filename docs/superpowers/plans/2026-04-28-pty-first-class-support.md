# PTY First-Class Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `PTY` spawn mode to cli-wrapper so it can host interactive TUI children (Claude Code, bash, vim, etc.), with bidirectional byte streaming, window-resize propagation, signal delivery, capability handshake, and log persistence preserved across modes.

**Architecture:** Spawn branch in `internal/agent/runner.go` opens a PTY pair via `creack/pty`, attaches the slave to the child's stdio with `Setsid+Setctty`, and runs two pumps (PTY→host as `MsgPTYData`, host→PTY as `MsgPTYWrite`). Four new wire messages plus a capability handshake guarantee version-safe rollout. The existing log rotator and ring buffer continue to receive the same bytes via tee.

**Tech Stack:** Go 1.22, `github.com/creack/pty`, `golang.org/x/sys/unix`, MessagePack frames over Unix socket pair, `stretchr/testify` + `goleak` for tests.

**Spec:** `docs/superpowers/specs/2026-04-28-pty-first-class-support-design.md`

---

## Project Conventions (apply to EVERY task)

These are non-negotiable conventions for this repository. The implementing agent must apply them in every task without being told again.

### Commits
- **DCO sign-off mandatory**: every `git commit` uses `-s`. Example: `git commit -s -m "feat(ipc): ..."`. CI rejects unsigned commits.
- **Conventional Commits**: `feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `style:`, `chore:`, `build:`, `bench:` (with optional scope). Subject ≤72 chars. Body explains the *why*.
- **Forbidden trailers**: never add `Co-Authored-By:` or "Generated with Claude" lines (project policy).

### Test naming
- Function names use `TestSubject_Behavior` (underscore separator). Examples: `TestPTY_DataRoundTrip`, `TestRunner_SendsMsgPTYData`, `TestController_RefusesPTYWhenAgentLacksFeature`.
- Wherever this plan shows test names without an underscore (e.g. `TestPTYDataRoundTrip`), translate to the underscore form when writing the actual code.

### Goroutine hygiene
- Every test package that spawns goroutines includes a `TestMain` with `goleak.VerifyTestMain(m)`:
  ```go
  package <pkg>
  import (
      "testing"
      "go.uber.org/goleak"
  )
  func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }
  ```
- For tests that spawn goroutines outside `TestMain` coverage, add `defer goleak.VerifyNone(t)` at the top of the test function.
- A `TestMain` skeleton must be added the first time a test file in a new package is created.

### errcheck (golangci-lint v2)
- Bare `defer X(...)` on a function that returns an error fails lint. Use:
  ```go
  defer func() { _ = h.Stop(ctx, 1*time.Second) }()
  ```
- This applies to `Stop`, `Close`, `Terminate`, `Unsubscribe`, etc.

### Wire protocol convention
- Host → agent message type IDs use the **low half** (`< 0x80`).
- Agent → host message type IDs use the **high half** (`≥ 0x80`).
- This plan reserves: `MsgPTYWrite=0x20`, `MsgPTYResize=0x22`, `MsgPTYSignal=0x23`, `MsgCapabilityQuery=0x30` (host→agent); `MsgPTYData=0x90`, `MsgCapabilityReply=0xB0` (agent→host).

---

## File Structure

| Path | Responsibility |
|---|---|
| `go.mod` | Add `github.com/creack/pty` |
| `pkg/cliwrap/types.go` | Add `PTYConfig` to `Spec` |
| `pkg/cliwrap/manager.go` | Add `WriteInput`/`Resize`/`Signal`/`SubscribePTYData` on `ProcessHandle` |
| `pkg/cliwrap/errors.go` | Add `ErrPTYUnsupportedByAgent`, `ErrPTYNotConfigured` |
| `internal/ipc/messages.go` | Add 6 new message type IDs (4 PTY + 2 capability) |
| `internal/ipc/codec_pty.go` | NEW — encode/decode for new messages |
| `internal/ipc/wal.go` | New channel ID for PTY data |
| `internal/agent/dispatcher.go` | Route 6 new messages |
| `internal/agent/runner.go` | PTY spawn branch + pumps |
| `internal/agent/runner_pty.go` | NEW — PTY-specific spawn helpers (keep runner.go small) |
| `internal/agent/capability.go` | NEW — capability handshake (agent side) |
| `internal/controller/controller.go` | Capability handshake (host side); PTY message subscription |
| `internal/controller/pty.go` | NEW — host pump glue (subscription channel, write queue) |
| `internal/logcollect/collector.go` | (no API change) Tee writes from PTY pump |
| `test/integration/pty_*_test.go` | NEW — 5 integration tests |
| `test/chaos/pty_*_test.go` | NEW — 3 chaos tests |
| `test/bench/pty_*_test.go` | NEW — throughput + latency benches |
| `CHANGELOG.md` | New entry under [Unreleased] |

---

## Task 1: Add `creack/pty` dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the dependency**

```bash
cd /Users/wm-it-22-00661/Work/github/study/ai/harness/cli-wrapper
go get github.com/creack/pty@v1.1.21
```

- [ ] **Step 2: Verify it builds**

```bash
go build ./...
```
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -s -m "build: add creack/pty dependency for PTY support"
```

---

## Task 2: Define new wire message type IDs

**Files:**
- Modify: `internal/ipc/messages.go`
- Test: `internal/ipc/messages_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/ipc/messages_test.go`:

```go
func TestNewMessageTypeIDs_RespectDirectionConvention(t *testing.T) {
    // host→agent: low half (< 0x80)
    require.Equal(t, MsgType(0x20), MsgTypePTYWrite)
    require.Equal(t, MsgType(0x22), MsgTypePTYResize)
    require.Equal(t, MsgType(0x23), MsgTypePTYSignal)
    require.Equal(t, MsgType(0x30), MsgTypeCapabilityQuery)
    // agent→host: high half (>= 0x80)
    require.Equal(t, MsgType(0x90), MsgTypePTYData)
    require.Equal(t, MsgType(0xB0), MsgTypeCapabilityReply)

    // Ensure no collision with existing IDs
    used := map[MsgType]bool{}
    for _, id := range AllMessageTypes() {
        require.False(t, used[id], "duplicate message type %d", id)
        used[id] = true
    }
}
```

- [ ] **Step 2: Run to fail**

```bash
go test ./internal/ipc/ -run TestNewMessageTypeIDs -v
```
Expected: undefined references.

- [ ] **Step 3: Add the constants**

In `internal/ipc/frame.go` (where the existing MsgType constants live):

```go
const (
    // ... existing host→agent IDs (0x01-0x0B) ...

    // PTY mode (host → agent)
    MsgTypePTYWrite        MsgType = 0x20
    MsgTypePTYResize       MsgType = 0x22
    MsgTypePTYSignal       MsgType = 0x23

    // Capability handshake (host → agent)
    MsgTypeCapabilityQuery MsgType = 0x30

    // ... existing agent→host IDs (0x81-0x8A) ...

    // PTY mode (agent → host)
    MsgTypePTYData         MsgType = 0x90

    // Capability handshake (agent → host)
    MsgTypeCapabilityReply MsgType = 0xB0
)

// Add the new IDs to AllMessageTypes() helper (if it exists; create if not).
func AllMessageTypes() []MsgType {
    return []MsgType{
        // ... existing IDs ...
        MsgTypePTYData, MsgTypePTYWrite, MsgTypePTYResize, MsgTypePTYSignal,
        MsgTypeCapabilityQuery, MsgTypeCapabilityReply,
    }
}
```

- [ ] **Step 4: Run to pass**

```bash
go test ./internal/ipc/ -run TestNewMessageTypeIDs -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc/
git commit -s -m "feat(ipc): reserve message type IDs for PTY and capability"
```

---

## Task 3: Implement codec for new messages

**Files:**
- Create: `internal/ipc/codec_pty.go`
- Test: `internal/ipc/codec_pty_test.go`

- [ ] **Step 1: Write failing tests**

`internal/ipc/codec_pty_test.go`:

```go
package ipc

import (
    "testing"
    "github.com/stretchr/testify/require"
)

func TestPTYDataRoundTrip(t *testing.T) {
    in := PTYData{Seq: 42, Bytes: []byte("hello\n")}
    raw, err := EncodePTYData(in)
    require.NoError(t, err)
    out, err := DecodePTYData(raw)
    require.NoError(t, err)
    require.Equal(t, in, out)
}

func TestPTYWriteRoundTrip(t *testing.T) {
    in := PTYWrite{Seq: 7, Bytes: []byte{0x03}}
    raw, _ := EncodePTYWrite(in)
    out, err := DecodePTYWrite(raw)
    require.NoError(t, err)
    require.Equal(t, in, out)
}

func TestPTYResizeRoundTrip(t *testing.T) {
    in := PTYResize{Cols: 120, Rows: 40}
    raw, _ := EncodePTYResize(in)
    out, err := DecodePTYResize(raw)
    require.NoError(t, err)
    require.Equal(t, in, out)
}

func TestPTYSignalRoundTrip(t *testing.T) {
    in := PTYSignal{Signum: 2}
    raw, _ := EncodePTYSignal(in)
    out, err := DecodePTYSignal(raw)
    require.NoError(t, err)
    require.Equal(t, in, out)
}

func TestCapabilityRoundTrip(t *testing.T) {
    in := CapabilityReply{Features: []string{"pty", "logs"}}
    raw, _ := EncodeCapabilityReply(in)
    out, err := DecodeCapabilityReply(raw)
    require.NoError(t, err)
    require.Equal(t, in, out)
}
```

- [ ] **Step 2: Run to fail**

```bash
go test ./internal/ipc/ -run TestPTY -v
```
Expected: undefined references.

- [ ] **Step 3: Implement codec**

`internal/ipc/codec_pty.go`:

```go
package ipc

import (
    "github.com/vmihailenco/msgpack/v5"
)

type PTYData struct {
    Seq   uint64 `msgpack:"seq"`
    Bytes []byte `msgpack:"bytes"`
}

type PTYWrite struct {
    Seq   uint64 `msgpack:"seq"`
    Bytes []byte `msgpack:"bytes"`
}

type PTYResize struct {
    Cols uint16 `msgpack:"cols"`
    Rows uint16 `msgpack:"rows"`
}

type PTYSignal struct {
    Signum int32 `msgpack:"signum"`
}

type CapabilityQuery struct{}

type CapabilityReply struct {
    Features []string `msgpack:"features"`
}

func EncodePTYData(d PTYData) ([]byte, error)         { return msgpack.Marshal(d) }
func DecodePTYData(b []byte) (PTYData, error)         { var d PTYData; return d, msgpack.Unmarshal(b, &d) }
func EncodePTYWrite(d PTYWrite) ([]byte, error)       { return msgpack.Marshal(d) }
func DecodePTYWrite(b []byte) (PTYWrite, error)       { var d PTYWrite; return d, msgpack.Unmarshal(b, &d) }
func EncodePTYResize(d PTYResize) ([]byte, error)     { return msgpack.Marshal(d) }
func DecodePTYResize(b []byte) (PTYResize, error)     { var d PTYResize; return d, msgpack.Unmarshal(b, &d) }
func EncodePTYSignal(d PTYSignal) ([]byte, error)     { return msgpack.Marshal(d) }
func DecodePTYSignal(b []byte) (PTYSignal, error)     { var d PTYSignal; return d, msgpack.Unmarshal(b, &d) }
func EncodeCapabilityReply(r CapabilityReply) ([]byte, error) { return msgpack.Marshal(r) }
func DecodeCapabilityReply(b []byte) (CapabilityReply, error) { var r CapabilityReply; return r, msgpack.Unmarshal(b, &r) }
```

- [ ] **Step 4: Run to pass**

```bash
go test ./internal/ipc/ -run TestPTY -v && go test ./internal/ipc/ -run TestCapability -v
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc/codec_pty.go internal/ipc/codec_pty_test.go
git commit -s -m "feat(ipc): add codec for PTY and capability messages"
```

---

## Task 4: WAL channel ID for PTY data

**Files:**
- Modify: `internal/ipc/wal.go`
- Test: `internal/ipc/wal_test.go`

- [ ] **Step 1: Write failing test**

Add to `internal/ipc/wal_test.go`:

```go
func TestWALReplaysPTYDataChannelInOrder(t *testing.T) {
    dir := t.TempDir()
    wal, err := OpenWAL(dir + "/outbox.wal")
    require.NoError(t, err)
    defer wal.Close()

    // Interleave LogChunk + PTYData on same seq space
    require.NoError(t, wal.Append(ChannelLogChunk, 1, []byte("log1")))
    require.NoError(t, wal.Append(ChannelPTYData,  2, []byte("pty1")))
    require.NoError(t, wal.Append(ChannelLogChunk, 3, []byte("log2")))

    var got []string
    require.NoError(t, wal.Replay(func(ch Channel, seq uint64, b []byte) error {
        got = append(got, fmt.Sprintf("%d:%d:%s", ch, seq, b))
        return nil
    }))
    require.Equal(t, []string{
        fmt.Sprintf("%d:1:log1", ChannelLogChunk),
        fmt.Sprintf("%d:2:pty1", ChannelPTYData),
        fmt.Sprintf("%d:3:log2", ChannelLogChunk),
    }, got)
}
```

- [ ] **Step 2: Run to fail**

```bash
go test ./internal/ipc/ -run TestWALReplaysPTYDataChannelInOrder -v
```
Expected: undefined `ChannelPTYData`.

- [ ] **Step 3: Add channel constant**

In `internal/ipc/wal.go` (or wherever `Channel` constants live):

```go
const (
    ChannelLogChunk Channel = iota + 1
    ChannelEvent
    ChannelPTYData
)
```

- [ ] **Step 4: Run to pass**

```bash
go test ./internal/ipc/ -run TestWALReplaysPTYDataChannelInOrder -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc/
git commit -s -m "feat(ipc): add WAL channel for PTY data with global seq ordering"
```

---

## Task 5: Add `PTYConfig` to public Spec

**Files:**
- Modify: `pkg/cliwrap/types.go`
- Test: `pkg/cliwrap/types_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestSpecPTYDefaultsZero(t *testing.T) {
    s := Spec{Program: "bash"}
    require.Nil(t, s.PTY)
}

func TestPTYConfigDefaultsApplied(t *testing.T) {
    c := PTYConfig{} // zero
    require.NoError(t, ApplyPTYDefaults(&c))
    require.Equal(t, uint16(80), c.InitialCols)
    require.Equal(t, uint16(24), c.InitialRows)
    require.False(t, c.Echo)
}

func TestPTYConfigRespectsExplicitValues(t *testing.T) {
    c := PTYConfig{InitialCols: 132, InitialRows: 50, Echo: true}
    require.NoError(t, ApplyPTYDefaults(&c))
    require.Equal(t, uint16(132), c.InitialCols)
    require.Equal(t, uint16(50), c.InitialRows)
    require.True(t, c.Echo)
}
```

- [ ] **Step 2: Run to fail**

```bash
go test ./pkg/cliwrap/ -run TestSpecPTY -v && go test ./pkg/cliwrap/ -run TestPTYConfig -v
```
Expected: undefined.

- [ ] **Step 3: Add types**

`pkg/cliwrap/types.go`:

```go
type Spec struct {
    // ... existing fields ...
    PTY *PTYConfig `yaml:"pty,omitempty"`
}

type PTYConfig struct {
    InitialCols uint16 `yaml:"initial_cols"`
    InitialRows uint16 `yaml:"initial_rows"`
    Echo        bool   `yaml:"echo"`
}

func ApplyPTYDefaults(c *PTYConfig) error {
    if c == nil {
        return nil
    }
    if c.InitialCols == 0 {
        c.InitialCols = 80
    }
    if c.InitialRows == 0 {
        c.InitialRows = 24
    }
    return nil
}
```

- [ ] **Step 4: Run to pass**

```bash
go test ./pkg/cliwrap/ -run TestSpec -v && go test ./pkg/cliwrap/ -run TestPTYConfig -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/cliwrap/
git commit -s -m "feat(cliwrap): add PTYConfig to Spec public API"
```

---

## Task 6: Public errors

**Files:**
- Modify: `pkg/cliwrap/errors.go` (create if absent)

- [ ] **Step 1: Write failing test**

`pkg/cliwrap/errors_test.go`:

```go
func TestPublicErrorsDistinct(t *testing.T) {
    require.NotEqual(t, ErrPTYUnsupportedByAgent, ErrPTYNotConfigured)
    require.NotEmpty(t, ErrPTYUnsupportedByAgent.Error())
    require.NotEmpty(t, ErrPTYNotConfigured.Error())
}
```

- [ ] **Step 2: Run to fail**

```bash
go test ./pkg/cliwrap/ -run TestPublicErrors -v
```

- [ ] **Step 3: Add errors**

In `pkg/cliwrap/errors.go`:

```go
package cliwrap

import "errors"

var (
    ErrPTYUnsupportedByAgent = errors.New("cliwrap: agent does not support PTY mode")
    ErrPTYNotConfigured      = errors.New("cliwrap: PTY operations require Spec.PTY to be set")
)
```

- [ ] **Step 4: Run to pass + commit**

```bash
go test ./pkg/cliwrap/ -run TestPublicErrors -v
git add pkg/cliwrap/errors.go pkg/cliwrap/errors_test.go
git commit -s -m "feat(cliwrap): add public errors for PTY mode"
```

---

## Task 7: Capability handshake (agent side)

**Files:**
- Create: `internal/agent/capability.go`
- Test: `internal/agent/capability_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestAgentReplyContainsPTYFeature(t *testing.T) {
    reply := BuildCapabilityReply()
    require.Contains(t, reply.Features, "pty")
}
```

- [ ] **Step 2: Run to fail + implement**

```bash
go test ./internal/agent/ -run TestAgentReply -v
```

`internal/agent/capability.go`:

```go
package agent

import "github.com/0xmhha/cli-wrapper/internal/ipc"

const FeaturePTY = "pty"

func BuildCapabilityReply() ipc.CapabilityReply {
    return ipc.CapabilityReply{Features: []string{FeaturePTY}}
}
```

- [ ] **Step 3: Wire into dispatcher**

In `internal/agent/dispatcher.go` `Handle()` switch, add:

```go
case ipc.MsgTypeCapabilityQuery:
    reply := BuildCapabilityReply()
    payload, _ := ipc.EncodeCapabilityReply(reply)
    return d.conn.Send(ipc.MsgTypeCapabilityReply, payload)
```

- [ ] **Step 4: Run all agent tests**

```bash
go test ./internal/agent/ -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/
git commit -s -m "feat(agent): respond to capability query with pty feature"
```

---

## Task 8: Capability handshake (host side)

**Files:**
- Modify: `internal/controller/controller.go`
- Test: `internal/controller/controller_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestControllerNegotiatesPTY(t *testing.T) {
    h, agent := newTestController(t) // existing helper that gives a fake agent
    agent.SetCapabilityFeatures([]string{"pty"})

    err := h.Start(context.Background(), Spec{ /* ... */ })
    require.NoError(t, err)
    require.True(t, h.AgentSupportsPTY())
}

func TestControllerRefusesPTYWhenAgentLacksFeature(t *testing.T) {
    h, agent := newTestController(t)
    agent.SetCapabilityFeatures([]string{}) // no pty

    err := h.Start(context.Background(), Spec{PTY: &PTYConfig{}})
    require.ErrorIs(t, err, ErrPTYUnsupportedByAgent)
}
```

- [ ] **Step 2: Run to fail**

```bash
go test ./internal/controller/ -run TestController.*PTY -v
```

- [ ] **Step 3: Implement handshake**

In `internal/controller/controller.go` `Start`, after socket setup but before child spawn, send `MsgTypeCapabilityQuery`, receive `MsgTypeCapabilityReply`, store features. Before spawning a `Spec.PTY != nil` child, check feature presence.

```go
func (c *Controller) negotiateCapabilities(ctx context.Context) error {
    if err := c.conn.Send(ipc.MsgTypeCapabilityQuery, nil); err != nil {
        return err
    }
    msgType, payload, err := c.conn.RecvWithType(ctx, 5*time.Second)
    if err != nil { return err }
    if msgType != ipc.MsgTypeCapabilityReply {
        // Older agent: no support
        c.features = nil
        return nil
    }
    reply, err := ipc.DecodeCapabilityReply(payload)
    if err != nil { return err }
    c.features = reply.Features
    return nil
}

func (c *Controller) AgentSupportsPTY() bool {
    for _, f := range c.features {
        if f == "pty" { return true }
    }
    return false
}
```

In `Start`, after `negotiateCapabilities`:

```go
if spec.PTY != nil && !c.AgentSupportsPTY() {
    return ErrPTYUnsupportedByAgent
}
```

- [ ] **Step 4: Run to pass**

```bash
go test ./internal/controller/ -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/controller/
git commit -s -m "feat(controller): capability handshake refuses PTY on incompatible agent"
```

---

## Task 9: PTY spawn helpers

**Files:**
- Create: `internal/agent/runner_pty.go`
- Test: `internal/agent/runner_pty_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestSpawnPTYBashEcho(t *testing.T) {
    spec := Spec{
        Program: "/bin/bash", Args: []string{"-c", "echo hello && exit 0"},
        PTY: &PTYConfig{InitialCols: 80, InitialRows: 24},
    }
    p, err := spawnPTY(context.Background(), spec)
    require.NoError(t, err)
    defer p.Close()

    var sink bytes.Buffer
    p.OnData(func(b []byte) { sink.Write(b) })

    waitForExit(t, p, 3*time.Second)
    require.Contains(t, sink.String(), "hello")
}
```

- [ ] **Step 2: Run to fail**

```bash
go test ./internal/agent/ -run TestSpawnPTYBashEcho -v
```

- [ ] **Step 3: Implement spawn**

`internal/agent/runner_pty.go`:

```go
package agent

import (
    "context"
    "os/exec"
    "syscall"

    "github.com/creack/pty"
    "github.com/0xmhha/cli-wrapper/pkg/cliwrap"
    "golang.org/x/sys/unix"
)

type ptyProc struct {
    cmd     *exec.Cmd
    ptmx    *os.File
    pgid    int
    onData  func([]byte)
    closeCh chan struct{}
}

func spawnPTY(ctx context.Context, spec Spec) (*ptyProc, error) {
    if err := cliwrap.ApplyPTYDefaults(spec.PTY); err != nil {
        return nil, err
    }
    ptmx, tty, err := pty.Open()
    if err != nil { return nil, err }

    cmd := exec.CommandContext(ctx, spec.Program, spec.Args...)
    cmd.Dir = spec.Cwd
    cmd.Env = spec.Env
    cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
    cmd.SysProcAttr = &syscall.SysProcAttr{
        Setsid: true, Setctty: true, Ctty: int(tty.Fd()),
    }

    if err := setWinsize(ptmx, spec.PTY.InitialCols, spec.PTY.InitialRows); err != nil {
        ptmx.Close(); tty.Close(); return nil, err
    }

    if err := cmd.Start(); err != nil {
        ptmx.Close(); tty.Close(); return nil, err
    }
    tty.Close()

    pgid, _ := syscall.Getpgid(cmd.Process.Pid)
    return &ptyProc{cmd: cmd, ptmx: ptmx, pgid: pgid, closeCh: make(chan struct{})}, nil
}

func setWinsize(f *os.File, cols, rows uint16) error {
    return unix.IoctlSetWinsize(int(f.Fd()), unix.TIOCSWINSZ, &unix.Winsize{
        Col: cols, Row: rows,
    })
}

func (p *ptyProc) OnData(cb func([]byte)) { p.onData = cb }
func (p *ptyProc) Close() error            { close(p.closeCh); return p.ptmx.Close() }
```

- [ ] **Step 4: Add the read pump**

Append to `runner_pty.go`:

```go
func (p *ptyProc) startReadPump() {
    go func() {
        buf := make([]byte, 32<<10)
        for {
            n, err := p.ptmx.Read(buf)
            if n > 0 && p.onData != nil {
                cp := make([]byte, n); copy(cp, buf[:n])
                p.onData(cp)
            }
            if err != nil { return }
        }
    }()
}
```

Update `spawnPTY` to call `proc.startReadPump()` before returning.

- [ ] **Step 5: Run to pass + commit**

```bash
go test ./internal/agent/ -run TestSpawnPTYBashEcho -v
git add internal/agent/runner_pty.go internal/agent/runner_pty_test.go
git commit -s -m "feat(agent): spawn child with PTY and stream output via callback"
```

---

## Task 10: Wire PTY into runner branch

**Files:**
- Modify: `internal/agent/runner.go`
- Modify: `internal/agent/dispatcher.go`
- Test: `internal/agent/runner_test.go`

- [ ] **Step 1: Write failing integration-ish test**

```go
func TestRunnerSendsMsgPTYData(t *testing.T) {
    fakeConn := newFakeConn()
    r := NewRunner(fakeConn, /*logCollector*/ newFakeLogger())

    err := r.StartChild(context.Background(), Spec{
        Program: "/bin/echo", Args: []string{"pty-output-token"},
        PTY: &PTYConfig{InitialCols: 80, InitialRows: 24},
    })
    require.NoError(t, err)

    msg := waitForMsg(t, fakeConn, ipc.MsgTypePTYData, 2*time.Second)
    payload, _ := ipc.DecodePTYData(msg)
    require.Contains(t, string(payload.Bytes), "pty-output-token")
}
```

- [ ] **Step 2: Run to fail**

```bash
go test ./internal/agent/ -run TestRunnerSendsMsgPTYData -v
```

- [ ] **Step 3: Implement runner branch**

In `internal/agent/runner.go` `StartChild`, before the existing pipe-mode spawn:

```go
if spec.PTY != nil {
    return r.startChildPTY(ctx, spec)
}
// ... existing pipe-mode code unchanged ...
```

Then `startChildPTY`:

```go
func (r *Runner) startChildPTY(ctx context.Context, spec Spec) error {
    proc, err := spawnPTY(ctx, spec)
    if err != nil { return err }

    var seq uint64
    proc.OnData(func(b []byte) {
        s := atomic.AddUint64(&seq, 1)
        payload, _ := ipc.EncodePTYData(ipc.PTYData{Seq: s, Bytes: b})
        _ = r.conn.Send(ipc.MsgTypePTYData, payload)
        // tee to log collector regardless of host attachment state
        r.logCollector.Write(b)
    })

    r.recordProc(proc)
    go r.waitChild(proc)
    return nil
}
```

- [ ] **Step 4: Run to pass**

```bash
go test ./internal/agent/ -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/agent/
git commit -s -m "feat(agent): branch runner into PTY mode and emit MsgPTYData"
```

---

## Task 11: Host→PTY write pump

**Files:**
- Modify: `internal/agent/runner_pty.go`
- Modify: `internal/agent/dispatcher.go`
- Test: `internal/agent/runner_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestRunnerForwardsPTYWriteToChild(t *testing.T) {
    fakeConn := newFakeConn()
    r := NewRunner(fakeConn, newFakeLogger())

    require.NoError(t, r.StartChild(context.Background(), Spec{
        Program: "/bin/cat", PTY: &PTYConfig{},
    }))

    // Send input
    write, _ := ipc.EncodePTYWrite(ipc.PTYWrite{Seq: 1, Bytes: []byte("ping\n")})
    require.NoError(t, r.HandleHostMsg(ipc.MsgTypePTYWrite, write))

    // Expect echoed back via MsgPTYData
    msg := waitForMsg(t, fakeConn, ipc.MsgTypePTYData, 2*time.Second)
    payload, _ := ipc.DecodePTYData(msg)
    require.Contains(t, string(payload.Bytes), "ping")
}
```

- [ ] **Step 2: Run to fail + implement**

Add `WriteInput` to `ptyProc`:

```go
func (p *ptyProc) WriteInput(b []byte) error {
    _, err := p.ptmx.Write(b)
    return err
}
```

In `dispatcher.go` add a case:

```go
case ipc.MsgTypePTYWrite:
    w, err := ipc.DecodePTYWrite(payload)
    if err != nil { return err }
    return d.runner.WriteToActivePTY(w.Bytes)
```

In `runner.go`:

```go
func (r *Runner) WriteToActivePTY(b []byte) error {
    p := r.activePTYProc()
    if p == nil { return ErrNoActivePTY }
    return p.WriteInput(b)
}
```

- [ ] **Step 3: Run to pass + commit**

```bash
go test ./internal/agent/ -run TestRunnerForwardsPTYWrite -v
git add internal/agent/
git commit -s -m "feat(agent): forward MsgPTYWrite to PTY master"
```

---

## Task 12: Resize handler

**Files:**
- Modify: `internal/agent/runner_pty.go`, `internal/agent/dispatcher.go`
- Test: `internal/agent/runner_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestRunnerHandlesResize(t *testing.T) {
    fakeConn := newFakeConn()
    r := NewRunner(fakeConn, newFakeLogger())
    require.NoError(t, r.StartChild(context.Background(), Spec{
        Program: "/bin/bash", Args: []string{"-c", "tput cols; tput lines; sleep 1"},
        PTY: &PTYConfig{InitialCols: 80, InitialRows: 24},
    }))

    payload, _ := ipc.EncodePTYResize(ipc.PTYResize{Cols: 132, Rows: 50})
    require.NoError(t, r.HandleHostMsg(ipc.MsgTypePTYResize, payload))

    // Verify by re-running tput in same shell? Simpler: assert ioctl succeeded.
    // We expose Winsize for testability:
    cols, rows, err := r.activePTYProc().Winsize()
    require.NoError(t, err)
    require.Equal(t, uint16(132), cols)
    require.Equal(t, uint16(50), rows)
}
```

- [ ] **Step 2: Run to fail + implement**

In `runner_pty.go`:

```go
func (p *ptyProc) Resize(cols, rows uint16) error {
    return setWinsize(p.ptmx, cols, rows)
}

func (p *ptyProc) Winsize() (uint16, uint16, error) {
    ws, err := unix.IoctlGetWinsize(int(p.ptmx.Fd()), unix.TIOCGWINSZ)
    if err != nil { return 0, 0, err }
    return ws.Col, ws.Row, nil
}
```

In `dispatcher.go`:

```go
case ipc.MsgTypePTYResize:
    rz, err := ipc.DecodePTYResize(payload)
    if err != nil { return err }
    return d.runner.ResizeActivePTY(rz.Cols, rz.Rows)
```

- [ ] **Step 3: Run to pass + commit**

```bash
go test ./internal/agent/ -run TestRunnerHandlesResize -v
git add internal/agent/
git commit -s -m "feat(agent): handle MsgPTYResize via TIOCSWINSZ"
```

---

## Task 13: Signal handler

**Files:**
- Modify: `internal/agent/runner_pty.go`, `internal/agent/dispatcher.go`
- Test: `internal/agent/runner_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestRunnerSignalSIGINTKillsForegroundPG(t *testing.T) {
    fakeConn := newFakeConn()
    r := NewRunner(fakeConn, newFakeLogger())
    // sleep traps SIGINT and exits
    require.NoError(t, r.StartChild(context.Background(), Spec{
        Program: "/bin/sleep", Args: []string{"100"},
        PTY: &PTYConfig{},
    }))

    payload, _ := ipc.EncodePTYSignal(ipc.PTYSignal{Signum: int32(syscall.SIGINT)})
    require.NoError(t, r.HandleHostMsg(ipc.MsgTypePTYSignal, payload))

    select {
    case <-r.activePTYProc().Done():
    case <-time.After(2 * time.Second):
        t.Fatal("child did not exit on SIGINT")
    }
}
```

- [ ] **Step 2: Run to fail + implement**

```go
func (p *ptyProc) Signal(sig syscall.Signal) error {
    return syscall.Kill(-p.pgid, sig)
}

func (p *ptyProc) Done() <-chan struct{} {
    return p.closeCh
}
```

In dispatcher:

```go
case ipc.MsgTypePTYSignal:
    sg, err := ipc.DecodePTYSignal(payload)
    if err != nil { return err }
    return d.runner.SignalActivePTY(syscall.Signal(sg.Signum))
```

Ensure `closeCh` is closed when `cmd.Wait` returns (in `waitChild`).

- [ ] **Step 3: Run to pass + commit**

```bash
go test ./internal/agent/ -run TestRunnerSignalSIGINTKillsForegroundPG -v
git add internal/agent/
git commit -s -m "feat(agent): deliver MsgPTYSignal to foreground process group"
```

---

## Task 14: Public API additions

**Files:**
- Modify: `pkg/cliwrap/manager.go`
- Test: `pkg/cliwrap/manager_pty_test.go`

- [ ] **Step 1: Write failing test (uses real spawn through Manager)**

```go
//go:build integration
package cliwrap_test

func TestManagerSpawnPTYAndEcho(t *testing.T) {
    m, err := NewManager(t.TempDir())
    require.NoError(t, err)
    defer m.Close()

    h, err := m.Spawn(context.Background(), Spec{
        ID: "test", Program: "/bin/cat",
        PTY: &PTYConfig{InitialCols: 80, InitialRows: 24},
    })
    require.NoError(t, err)
    defer h.Stop(context.Background(), 2*time.Second)

    ch, unsub := h.SubscribePTYData()
    defer unsub()

    require.NoError(t, h.WriteInput(context.Background(), []byte("hello\n")))

    select {
    case d := <-ch:
        require.Contains(t, string(d.Bytes), "hello")
    case <-time.After(3 * time.Second):
        t.Fatal("no PTY data received")
    }
}

func TestManagerWriteInputErrorsForPipeMode(t *testing.T) {
    m, _ := NewManager(t.TempDir())
    defer m.Close()
    h, _ := m.Spawn(context.Background(), Spec{ID: "p", Program: "/bin/sleep", Args: []string{"1"}})
    err := h.WriteInput(context.Background(), []byte("x"))
    require.ErrorIs(t, err, ErrPTYNotConfigured)
}
```

- [ ] **Step 2: Run to fail + implement**

In `pkg/cliwrap/manager.go`:

```go
type PTYData struct {
    Seq   uint64
    Bytes []byte
}

func (h *ProcessHandle) WriteInput(ctx context.Context, b []byte) error {
    if h.spec.PTY == nil { return ErrPTYNotConfigured }
    payload, _ := ipc.EncodePTYWrite(ipc.PTYWrite{Seq: h.nextWriteSeq(), Bytes: b})
    return h.ctrl.SendWithCtx(ctx, ipc.MsgTypePTYWrite, payload)
}

func (h *ProcessHandle) Resize(ctx context.Context, cols, rows uint16) error {
    if h.spec.PTY == nil { return ErrPTYNotConfigured }
    payload, _ := ipc.EncodePTYResize(ipc.PTYResize{Cols: cols, Rows: rows})
    return h.ctrl.SendWithCtx(ctx, ipc.MsgTypePTYResize, payload)
}

func (h *ProcessHandle) Signal(ctx context.Context, sig syscall.Signal) error {
    if h.spec.PTY == nil { return ErrPTYNotConfigured }
    payload, _ := ipc.EncodePTYSignal(ipc.PTYSignal{Signum: int32(sig)})
    return h.ctrl.SendWithCtx(ctx, ipc.MsgTypePTYSignal, payload)
}

func (h *ProcessHandle) SubscribePTYData() (<-chan PTYData, func()) {
    if h.spec.PTY == nil {
        ch := make(chan PTYData); close(ch)
        return ch, func(){}
    }
    return h.ctrl.SubscribePTYData()
}
```

In `internal/controller/pty.go` (NEW):

```go
package controller

func (c *Controller) SubscribePTYData() (<-chan cliwrap.PTYData, func()) {
    ch := make(chan cliwrap.PTYData, 64)
    id := c.bus.SubscribePTY(ch)
    return ch, func() { c.bus.Unsubscribe(id); close(ch) }
}
```

(Implementation of `bus.SubscribePTY`/`Unsubscribe` follows the existing log subscription pattern in `internal/eventbus`.)

- [ ] **Step 3: Run to pass + commit**

```bash
go test ./pkg/cliwrap/ -tags integration -run TestManagerSpawnPTY -v
git add pkg/cliwrap/ internal/controller/
git commit -s -m "feat(cliwrap): expose PTY operations on ProcessHandle"
```

---

## Task 15: Integration test — echo loop

**Files:**
- Create: `test/integration/pty_echo_test.go`

- [ ] **Step 1: Write the test**

```go
//go:build integration
package integration

func TestPTYEchoLoop(t *testing.T) {
    m := setupManager(t)
    h, err := m.Spawn(ctx(t), cliwrap.Spec{
        ID: "echo", Program: "/bin/cat", PTY: &cliwrap.PTYConfig{},
    })
    require.NoError(t, err)
    defer h.Stop(ctx(t), 2*time.Second)

    ch, unsub := h.SubscribePTYData()
    defer unsub()

    for i := 0; i < 100; i++ {
        line := fmt.Sprintf("line-%d\n", i)
        require.NoError(t, h.WriteInput(ctx(t), []byte(line)))
        select {
        case d := <-ch:
            require.Contains(t, string(d.Bytes), fmt.Sprintf("line-%d", i))
        case <-time.After(2 * time.Second):
            t.Fatalf("missed echo for line %d", i)
        }
    }
}
```

- [ ] **Step 2: Run + commit**

```bash
go test -tags integration ./test/integration/ -run TestPTYEchoLoop -v
git add test/integration/pty_echo_test.go
git commit -s -m "test(integration): PTY echo loop with 100 iterations"
```

---

## Task 16: Integration test — resize

**Files:**
- Create: `test/integration/pty_resize_test.go`

- [ ] **Step 1: Write + run + commit**

```go
//go:build integration
package integration

func TestPTYResizeChangesChildView(t *testing.T) {
    m := setupManager(t)
    h, _ := m.Spawn(ctx(t), cliwrap.Spec{
        ID: "rsz", Program: "/bin/bash",
        Args: []string{"-c", "while :; do tput cols; tput lines; sleep 0.1; done"},
        PTY:  &cliwrap.PTYConfig{InitialCols: 80, InitialRows: 24},
    })
    defer h.Stop(ctx(t), 2*time.Second)

    ch, unsub := h.SubscribePTYData()
    defer unsub()

    // Wait for initial 80/24
    require.True(t, drainContains(t, ch, "80", 2*time.Second))

    require.NoError(t, h.Resize(ctx(t), 132, 50))

    // Allow the loop to pick up new SIGWINCH
    require.True(t, drainContains(t, ch, "132", 3*time.Second))
    require.True(t, drainContains(t, ch, "50",  3*time.Second))
}
```

```bash
go test -tags integration ./test/integration/ -run TestPTYResize -v
git add test/integration/pty_resize_test.go
git commit -s -m "test(integration): PTY resize propagates to child via SIGWINCH"
```

---

## Task 17: Integration test — signal delivery

**Files:**
- Create: `test/integration/pty_signal_test.go`

- [ ] **Step 1: Write + run + commit**

```go
//go:build integration
package integration

func TestPTYSignalSIGINTTerminatesSleep(t *testing.T) {
    m := setupManager(t)
    h, _ := m.Spawn(ctx(t), cliwrap.Spec{
        ID: "sig", Program: "/bin/sleep", Args: []string{"100"},
        PTY: &cliwrap.PTYConfig{},
    })

    require.NoError(t, h.Signal(ctx(t), syscall.SIGINT))

    select {
    case <-h.Done():
    case <-time.After(2 * time.Second):
        t.Fatal("child did not exit on SIGINT")
    }
}
```

```bash
go test -tags integration ./test/integration/ -run TestPTYSignal -v
git add test/integration/pty_signal_test.go
git commit -s -m "test(integration): SIGINT via PTY signal exits sleep within 2s"
```

---

## Task 18: Integration test — log persistence under PTY

**Files:**
- Create: `test/integration/pty_log_persistence_test.go`

- [ ] **Step 1: Write + run + commit**

```go
//go:build integration
package integration

func TestPTYDataPersistedToRotatorFile(t *testing.T) {
    workdir := t.TempDir()
    m := setupManagerInDir(t, workdir)
    h, _ := m.Spawn(ctx(t), cliwrap.Spec{
        ID: "logpersist", Program: "/bin/echo", Args: []string{"persisted-token"},
        PTY: &cliwrap.PTYConfig{},
    })
    waitExit(t, h, 2*time.Second)

    logPath := filepath.Join(workdir, "logpersist", "stdout.log")
    data, err := os.ReadFile(logPath)
    require.NoError(t, err)
    require.Contains(t, string(data), "persisted-token")
}
```

```bash
go test -tags integration ./test/integration/ -run TestPTYDataPersistedToRotatorFile -v
git add test/integration/pty_log_persistence_test.go
git commit -s -m "test(integration): PTY data also written to rotator file"
```

---

## Task 19: Integration test — capability handshake

**Files:**
- Create: `test/integration/pty_capability_test.go`

- [ ] **Step 1: Write + run + commit**

```go
//go:build integration
package integration

func TestSpawnPTYRefusedByOlderAgent(t *testing.T) {
    // Use a build tag or env var to start an "old" agent that does not handle MsgTypeCapabilityQuery.
    t.Setenv("CLIWRAP_AGENT_NO_CAPABILITY", "1")
    m := setupManager(t)
    _, err := m.Spawn(ctx(t), cliwrap.Spec{
        ID: "cap", Program: "/bin/cat", PTY: &cliwrap.PTYConfig{},
    })
    require.ErrorIs(t, err, cliwrap.ErrPTYUnsupportedByAgent)
}
```

The agent honors `CLIWRAP_AGENT_NO_CAPABILITY` by short-circuiting `Handle(MsgTypeCapabilityQuery)` to return `ErrUnknownMsgType`. Add this guard inside `internal/agent/dispatcher.go`'s `MsgTypeCapabilityQuery` case (test-only, documented in code):

```go
case ipc.MsgTypeCapabilityQuery:
    if os.Getenv("CLIWRAP_AGENT_NO_CAPABILITY") == "1" {
        return ipc.ErrUnknownMsgType // simulate older agent (test-only)
    }
    reply := BuildCapabilityReply()
    payload, _ := ipc.EncodeCapabilityReply(reply)
    return d.conn.Send(ipc.MsgTypeCapabilityReply, payload)
```

```bash
go test -tags integration ./test/integration/ -run TestSpawnPTYRefusedByOlderAgent -v
git add test/integration/pty_capability_test.go internal/agent/
git commit -s -m "test(integration): host refuses PTY when agent lacks capability"
```

---

## Task 20: Chaos — slow stdout consumer

**Files:**
- Create: `test/chaos/pty_slow_consumer_test.go`

- [ ] **Step 1: Write + run + commit**

```go
//go:build chaos
package chaos

func TestPTYSlowSubscriberDoesNotBlockAgent(t *testing.T) {
    m := setupManager(t)
    h, _ := m.Spawn(ctx(t), cliwrap.Spec{
        ID: "yes", Program: "/usr/bin/yes", PTY: &cliwrap.PTYConfig{},
    })
    defer h.Stop(ctx(t), 1*time.Second)

    ch, unsub := h.SubscribePTYData()
    defer unsub()

    // Slow consumer: read 1 chunk per 100 ms for 2 s; producer is unbounded.
    deadline := time.Now().Add(2 * time.Second)
    for time.Now().Before(deadline) {
        select {
        case <-ch:
        default:
        }
        time.Sleep(100 * time.Millisecond)
    }

    // Agent should still be alive and responsive — request status.
    st, err := h.Status(ctx(t))
    require.NoError(t, err)
    require.Equal(t, "running", string(st.State))
}
```

```bash
go test -tags chaos ./test/chaos/ -run TestPTYSlowSubscriber -v
git add test/chaos/pty_slow_consumer_test.go
git commit -s -m "test(chaos): slow PTY subscriber does not block agent"
```

---

## Task 21: Chaos — agent crash mid-stream

**Files:**
- Create: `test/chaos/pty_agent_crash_test.go`

- [ ] **Step 1: Write + run + commit**

```go
//go:build chaos
package chaos

func TestPTYAgentCrashMidStream(t *testing.T) {
    m := setupManager(t)
    h, _ := m.Spawn(ctx(t), cliwrap.Spec{
        ID: "crash", Program: "/usr/bin/yes", PTY: &cliwrap.PTYConfig{},
    })

    // SIGKILL the agent process
    require.NoError(t, killAgentFor(t, h))

    // Status should transition to Crashed within 5 s
    require.Eventually(t, func() bool {
        st, _ := h.Status(ctx(t))
        return string(st.State) == "crashed" || string(st.State) == "exited"
    }, 5*time.Second, 100*time.Millisecond)
}
```

```bash
go test -tags chaos ./test/chaos/ -run TestPTYAgentCrash -v
git add test/chaos/pty_agent_crash_test.go
git commit -s -m "test(chaos): agent crash detected within 5s on PTY stream"
```

---

## Task 22: Chaos — child SIGKILL externally

**Files:**
- Create: `test/chaos/pty_child_killed_test.go`

- [ ] **Step 1: Write + run + commit**

```go
//go:build chaos
package chaos

func TestPTYChildSIGKILLDetected(t *testing.T) {
    m := setupManager(t)
    h, _ := m.Spawn(ctx(t), cliwrap.Spec{
        ID: "extkill", Program: "/bin/sleep", Args: []string{"100"},
        PTY: &cliwrap.PTYConfig{},
    })

    pid, _ := h.ChildPID(ctx(t))
    require.NoError(t, syscall.Kill(pid, syscall.SIGKILL))

    require.Eventually(t, func() bool {
        st, _ := h.Status(ctx(t))
        return string(st.State) == "exited"
    }, 3*time.Second, 50*time.Millisecond)
}
```

```bash
go test -tags chaos ./test/chaos/ -run TestPTYChildSIGKILL -v
git add test/chaos/pty_child_killed_test.go
git commit -s -m "test(chaos): external SIGKILL of PTY child surfaces as exited"
```

---

## Task 23: Benchmark — PTY throughput

**Files:**
- Create: `test/bench/pty_throughput_test.go`

- [ ] **Step 1: Write + run + commit**

```go
//go:build bench
package bench

func BenchmarkPTYThroughput(b *testing.B) {
    m := setupManager(b)
    h, _ := m.Spawn(ctxB(b), cliwrap.Spec{
        ID: "tp", Program: "/usr/bin/yes",
        PTY: &cliwrap.PTYConfig{},
    })
    defer h.Stop(ctxB(b), 1*time.Second)

    ch, unsub := h.SubscribePTYData()
    defer unsub()

    var bytesRead int64
    start := time.Now()
    deadline := start.Add(5 * time.Second)
    for time.Now().Before(deadline) {
        select {
        case d := <-ch:
            bytesRead += int64(len(d.Bytes))
        case <-time.After(100 * time.Millisecond):
        }
    }
    sec := time.Since(start).Seconds()
    b.ReportMetric(float64(bytesRead)/sec/1e6, "MB/s")
}
```

```bash
go test -tags bench ./test/bench/ -run x -bench BenchmarkPTYThroughput -v
git add test/bench/pty_throughput_test.go
git commit -s -m "bench: PTY throughput (MB/s reported)"
```

---

## Task 24: Benchmark — keystroke latency

**Files:**
- Create: `test/bench/pty_latency_test.go`

- [ ] **Step 1: Write + run + commit**

```go
//go:build bench
package bench

func BenchmarkPTYKeystrokeLatency(b *testing.B) {
    m := setupManager(b)
    h, _ := m.Spawn(ctxB(b), cliwrap.Spec{
        ID: "lat", Program: "/bin/cat",
        PTY: &cliwrap.PTYConfig{},
    })
    defer h.Stop(ctxB(b), 1*time.Second)

    ch, unsub := h.SubscribePTYData()
    defer unsub()

    var samples []time.Duration
    for i := 0; i < b.N; i++ {
        marker := fmt.Sprintf("k%d\n", i)
        t0 := time.Now()
        _ = h.WriteInput(ctxB(b), []byte(marker))
        for {
            d := <-ch
            if bytes.Contains(d.Bytes, []byte(marker)) {
                samples = append(samples, time.Since(t0))
                break
            }
        }
    }
    sort.Slice(samples, func(i,j int) bool { return samples[i] < samples[j] })
    b.ReportMetric(float64(samples[len(samples)*50/100].Microseconds()), "p50_us")
    b.ReportMetric(float64(samples[len(samples)*95/100].Microseconds()), "p95_us")
    b.ReportMetric(float64(samples[len(samples)*99/100].Microseconds()), "p99_us")
}
```

```bash
go test -tags bench ./test/bench/ -run x -bench BenchmarkPTYKeystrokeLatency -v -benchtime=200x
git add test/bench/pty_latency_test.go
git commit -s -m "bench: PTY keystroke round-trip latency p50/p95/p99"
```

---

## Task 25: goleak verification

**Files:**
- Modify: existing `TestMain` in `internal/agent/`, `internal/controller/`, `pkg/cliwrap/`
- Test: rerun all suites

- [ ] **Step 1: Confirm goleak is wired**

If `internal/agent/main_test.go` already uses `goleak.VerifyTestMain(m)`, no change. Otherwise:

```go
package agent

import (
    "testing"
    "go.uber.org/goleak"
)

func TestMain(m *testing.M) {
    goleak.VerifyTestMain(m)
}
```

- [ ] **Step 2: Run the whole suite**

```bash
go test ./... && go test -tags integration ./...
```
Expected: green; no goleak failures from the new PTY paths.

- [ ] **Step 3: Commit if any TestMain added**

```bash
git add .
git commit -s -m "test: ensure goleak verifies new PTY goroutines"
```

(If nothing changed, skip commit.)

---

## Task 26: CHANGELOG entry

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Add under [Unreleased]**

```
## [Unreleased]

### Added
- First-class PTY support for child processes via `Spec.PTY`.
- New IPC messages: `MsgPTYData`, `MsgPTYWrite`, `MsgPTYResize`, `MsgPTYSignal`.
- Capability negotiation handshake (`MsgCapabilityQuery`/`Reply`); host refuses PTY against incompatible agents with `ErrPTYUnsupportedByAgent`.
- `ProcessHandle.WriteInput`, `Resize`, `Signal`, `SubscribePTYData`.
- Log rotator continues to record output regardless of spawn mode.
```

- [ ] **Step 2: Commit**

```bash
git add CHANGELOG.md
git commit -s -m "docs(changelog): record PTY first-class support"
```

---

## Task 27: Final verification

- [ ] **Step 1: Full test matrix**

```bash
go test ./...
go test -tags integration ./...
go test -tags chaos ./...
go test -tags bench ./test/bench/ -bench . -benchtime=100x
```
Expected: all green; benches print metrics.

- [ ] **Step 2: Push branch (if applicable)**

```bash
git push
```

- [ ] **Step 3: Open PR / mark done**

When done, ai-m's plan can begin Phase 1.3 (which depends on this work).
