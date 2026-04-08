# cli-wrapper Plan 04 — Config System & Management CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the YAML configuration loader, the `cliwrap` management CLI, and the manager control socket that CLI subcommands use to talk to a running `cliwrap run` process.

**Architecture:** The config loader parses a single top-level `Config` struct, resolves environment variables, and converts it into the same `cliwrap.Spec` values used by the Go API. The `cliwrap` CLI is a thin main that owns the Manager for `cliwrap run` and acts as a client for all other subcommands via a Unix domain socket (`manager.sock`) that reuses the framed MessagePack protocol from Plan 01.

**Tech Stack:**
- Go 1.22+
- `gopkg.in/yaml.v3` for YAML parsing
- Builds on Plans 01, 02, 03 (Manager, ipc frame/codec, sandbox, resource)

**Context for the implementer:**
- YAML only — no TOML/JSON loaders in v1.
- The management socket is local-only, mode `0600`. No authentication beyond filesystem permissions.
- Every subcommand must exit with an appropriate code and a clean error message.
- Commits follow conventional commits.

---

## File Structure

| File | Responsibility |
|------|----------------|
| `pkg/config/config.go` | Config, RuntimeConfig, ProcessGroup structs |
| `pkg/config/loader.go` | Load, LoadFile, toCanonical |
| `pkg/config/env.go` | Environment variable expansion |
| `pkg/config/size.go` | Byte size parsing (`"256MiB"`) |
| `pkg/config/loader_test.go` | Loader tests |
| `pkg/config/env_test.go` | Env expansion tests |
| `pkg/config/size_test.go` | Byte size tests |
| `internal/mgmt/proto.go` | Management protocol message types and payloads |
| `internal/mgmt/server.go` | Manager-side listener |
| `internal/mgmt/client.go` | CLI-side client helper |
| `internal/mgmt/server_test.go` | Server round-trip tests |
| `cmd/cliwrap/main.go` | CLI entrypoint, flag parsing, subcommand dispatch |
| `cmd/cliwrap/cmd_run.go` | `cliwrap run` |
| `cmd/cliwrap/cmd_validate.go` | `cliwrap validate` |
| `cmd/cliwrap/cmd_list.go` | `cliwrap list` |
| `cmd/cliwrap/cmd_status.go` | `cliwrap status` |
| `cmd/cliwrap/cmd_stop.go` | `cliwrap stop` |
| `cmd/cliwrap/cmd_logs.go` | `cliwrap logs` |
| `cmd/cliwrap/cmd_events.go` | `cliwrap events` |

---

## Task 1: Byte Size Parser

**Files:**
- Create: `pkg/config/size.go`
- Create: `pkg/config/size_test.go`

- [x] **Step 1: Write the failing test**

Create `pkg/config/size_test.go`:

```go
package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseByteSize(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
		err  bool
	}{
		{"0", 0, false},
		{"1024", 1024, false},
		{"1KiB", 1024, false},
		{"1KB", 1000, false},
		{"1MiB", 1024 * 1024, false},
		{"256MiB", 256 * 1024 * 1024, false},
		{"1 GiB", 1024 * 1024 * 1024, false},
		{"1GB", 1_000_000_000, false},
		{"garbage", 0, true},
		{"1XB", 0, true},
	}
	for _, c := range cases {
		got, err := ParseByteSize(c.in)
		if c.err {
			require.Error(t, err, c.in)
			continue
		}
		require.NoError(t, err, c.in)
		require.Equal(t, c.want, got, c.in)
	}
}
```

- [x] **Step 2: Run to confirm it fails**

Run:

```bash
go test ./pkg/config/ -count=1
```

Expected: `undefined: ParseByteSize`.

- [x] **Step 3: Implement the parser**

Create `pkg/config/size.go`:

```go
// Package config loads and validates cliwrap YAML configuration.
package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseByteSize parses strings like "256MiB" or "1GB" into a byte count.
// Supported suffixes:
//
//	B, KB, MB, GB, TB        (powers of 1000)
//	KiB, MiB, GiB, TiB       (powers of 1024)
//
// An unsuffixed integer is interpreted as bytes. Whitespace between the
// number and suffix is allowed.
func ParseByteSize(s string) (uint64, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, fmt.Errorf("config: empty byte size")
	}

	// Split number and suffix.
	i := 0
	for i < len(trimmed) && (trimmed[i] >= '0' && trimmed[i] <= '9') {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("config: invalid byte size %q", s)
	}
	numPart := trimmed[:i]
	suffix := strings.TrimSpace(trimmed[i:])

	n, err := strconv.ParseUint(numPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("config: parse number in %q: %w", s, err)
	}

	mult, ok := byteMultiplier(suffix)
	if !ok {
		return 0, fmt.Errorf("config: unknown byte suffix %q", suffix)
	}
	return n * mult, nil
}

func byteMultiplier(suffix string) (uint64, bool) {
	switch suffix {
	case "", "B":
		return 1, true
	case "KB":
		return 1_000, true
	case "MB":
		return 1_000_000, true
	case "GB":
		return 1_000_000_000, true
	case "TB":
		return 1_000_000_000_000, true
	case "KiB":
		return 1024, true
	case "MiB":
		return 1024 * 1024, true
	case "GiB":
		return 1024 * 1024 * 1024, true
	case "TiB":
		return 1024 * 1024 * 1024 * 1024, true
	default:
		return 0, false
	}
}
```

- [x] **Step 4: Run the tests**

Run:

```bash
go test ./pkg/config/ -count=1 -v
```

Expected: 10 cases pass.

- [x] **Step 5: Commit**

```bash
git add pkg/config/size.go pkg/config/size_test.go
git commit -m "feat(config): add byte size parser for YAML values"
```

---

## Task 2: Environment Variable Expansion

**Files:**
- Create: `pkg/config/env.go`
- Create: `pkg/config/env_test.go`

- [x] **Step 1: Write the failing test**

Create `pkg/config/env_test.go`:

```go
package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExpandEnv_Simple(t *testing.T) {
	t.Setenv("FOO", "bar")
	got, err := ExpandEnv("hello ${FOO}")
	require.NoError(t, err)
	require.Equal(t, "hello bar", got)
}

func TestExpandEnv_MissingIsError(t *testing.T) {
	_, err := ExpandEnv("hello ${NOT_SET}")
	require.Error(t, err)
}

func TestExpandEnv_DefaultFallback(t *testing.T) {
	got, err := ExpandEnv("hello ${NOT_SET:-world}")
	require.NoError(t, err)
	require.Equal(t, "hello world", got)
}

func TestExpandEnv_MultipleVars(t *testing.T) {
	t.Setenv("A", "1")
	t.Setenv("B", "2")
	got, err := ExpandEnv("${A}+${B}=${C:-3}")
	require.NoError(t, err)
	require.Equal(t, "1+2=3", got)
}

func TestExpandEnv_NoVarsIsPassThrough(t *testing.T) {
	got, err := ExpandEnv("plain text")
	require.NoError(t, err)
	require.Equal(t, "plain text", got)
}

func TestExpandEnv_EscapeDollar(t *testing.T) {
	got, err := ExpandEnv("price is $$5")
	require.NoError(t, err)
	require.Equal(t, "price is $5", got)
}
```

- [x] **Step 2: Run and confirm it fails**

Run:

```bash
go test ./pkg/config/ -run TestExpandEnv -count=1
```

Expected: undefined.

- [x] **Step 3: Implement the expander**

Create `pkg/config/env.go`:

```go
package config

import (
	"fmt"
	"os"
	"strings"
)

// ExpandEnv replaces ${VAR} and ${VAR:-default} occurrences in s.
// An unresolved ${VAR} without a default returns an error.
func ExpandEnv(s string) (string, error) {
	var out strings.Builder
	i := 0
	for i < len(s) {
		// $$ → literal $
		if i+1 < len(s) && s[i] == '$' && s[i+1] == '$' {
			out.WriteByte('$')
			i += 2
			continue
		}
		if i+1 < len(s) && s[i] == '$' && s[i+1] == '{' {
			end := strings.Index(s[i+2:], "}")
			if end < 0 {
				return "", fmt.Errorf("config: unterminated ${ at %d", i)
			}
			expr := s[i+2 : i+2+end]
			val, err := resolveEnv(expr)
			if err != nil {
				return "", err
			}
			out.WriteString(val)
			i += 2 + end + 1
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String(), nil
}

func resolveEnv(expr string) (string, error) {
	if idx := strings.Index(expr, ":-"); idx >= 0 {
		name := expr[:idx]
		def := expr[idx+2:]
		if v, ok := os.LookupEnv(name); ok {
			return v, nil
		}
		return def, nil
	}
	v, ok := os.LookupEnv(expr)
	if !ok {
		return "", fmt.Errorf("config: env var %q not set", expr)
	}
	return v, nil
}
```

- [x] **Step 4: Run the tests**

Run:

```bash
go test ./pkg/config/ -run TestExpandEnv -count=1 -v
```

Expected: six tests pass.

- [x] **Step 5: Commit**

```bash
git add pkg/config/env.go pkg/config/env_test.go
git commit -m "feat(config): add ${VAR} / ${VAR:-default} expansion"
```

---

## Task 3: Config Types & Loader

**Files:**
- Create: `pkg/config/config.go`
- Create: `pkg/config/loader.go`
- Create: `pkg/config/loader_test.go`

- [x] **Step 1: Add yaml.v3 dependency**

Run:

```bash
go get gopkg.in/yaml.v3@v3.0.1
go mod tidy
```

Expected: dependency added.

- [x] **Step 2: Write the failing test**

Create `pkg/config/loader_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cli-wrapper/cli-wrapper/pkg/cliwrap"
)

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoader_HappyPath(t *testing.T) {
	yamlBody := `
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
`
	path := writeTempFile(t, "config.yaml", yamlBody)
	cfg, err := LoadFile(path)
	require.NoError(t, err)
	require.Equal(t, "1", cfg.Version)
	require.Equal(t, "/tmp/cliwrap", cfg.Runtime.Dir)
	require.Len(t, cfg.Groups, 1)
	require.Len(t, cfg.Groups[0].Processes, 1)

	p := cfg.Groups[0].Processes[0]
	require.Equal(t, "redis", p.ID)
	require.Equal(t, "/usr/bin/redis-server", p.Command)
	require.Equal(t, cliwrap.RestartOnFailure, p.Restart)
	require.Equal(t, uint64(256*1024*1024), p.Resources.MaxRSS)
	require.Equal(t, cliwrap.ExceedKill, p.Resources.OnExceed)
}

func TestLoader_DuplicateIDsRejected(t *testing.T) {
	yamlBody := `
version: "1"
groups:
  - name: a
    processes:
      - id: dup
        command: /bin/echo
      - id: dup
        command: /bin/echo
`
	path := writeTempFile(t, "config.yaml", yamlBody)
	_, err := LoadFile(path)
	require.Error(t, err)
}

func TestLoader_EnvExpansion(t *testing.T) {
	t.Setenv("BIN", "/bin/echo")
	yamlBody := `
version: "1"
groups:
  - name: a
    processes:
      - id: e
        command: "${BIN}"
`
	path := writeTempFile(t, "config.yaml", yamlBody)
	cfg, err := LoadFile(path)
	require.NoError(t, err)
	require.Equal(t, "/bin/echo", cfg.Groups[0].Processes[0].Command)
}
```

- [x] **Step 3: Run to confirm it fails**

Run:

```bash
go test ./pkg/config/ -run TestLoader -count=1
```

Expected: undefined.

- [x] **Step 4: Implement the types and loader**

Create `pkg/config/config.go`:

```go
package config

import (
	"github.com/cli-wrapper/cli-wrapper/pkg/cliwrap"
)

// Config is the canonical in-memory representation of a loaded YAML file.
type Config struct {
	Version          string
	Runtime          RuntimeConfig
	SystemBudget     SystemBudgetConfig
	SandboxProviders []string
	Groups           []ProcessGroup
}

// RuntimeConfig mirrors the "runtime" YAML block.
type RuntimeConfig struct {
	Dir       string
	Debug     string // off | info | verbose | trace
	AgentPath string
}

// SystemBudgetConfig mirrors the "system_budget" YAML block.
type SystemBudgetConfig struct {
	MaxMemoryPercent float64
	MinFreeMemory    uint64
	MaxLoadAvg       float64
}

// ProcessGroup is a named collection of process specs.
type ProcessGroup struct {
	Name      string
	Processes []cliwrap.Spec
}
```

Create `pkg/config/loader.go`:

```go
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/cli-wrapper/cli-wrapper/pkg/cliwrap"
)

// LoadFile reads and parses a YAML config at path.
func LoadFile(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return Load(b)
}

// Load parses raw YAML bytes.
func Load(data []byte) (*Config, error) {
	expanded, err := ExpandEnv(string(data))
	if err != nil {
		return nil, err
	}

	var raw rawConfig
	if err := yaml.Unmarshal([]byte(expanded), &raw); err != nil {
		return nil, fmt.Errorf("config: yaml: %w", err)
	}

	cfg, err := raw.toCanonical()
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate checks for duplicate IDs and required fields across all groups.
func (c *Config) Validate() error {
	seen := map[string]struct{}{}
	for gi, g := range c.Groups {
		for pi, p := range g.Processes {
			if err := p.Validate(); err != nil {
				return fmt.Errorf("config: groups[%d].processes[%d]: %w", gi, pi, err)
			}
			if _, dup := seen[p.ID]; dup {
				return fmt.Errorf("config: duplicate process id %q", p.ID)
			}
			seen[p.ID] = struct{}{}
		}
	}
	return nil
}

type rawConfig struct {
	Version          string             `yaml:"version"`
	Runtime          rawRuntime         `yaml:"runtime"`
	SystemBudget     rawSystemBudget    `yaml:"system_budget"`
	SandboxProviders []rawSandboxProv   `yaml:"sandbox_providers"`
	Groups           []rawProcessGroup  `yaml:"groups"`
}

type rawRuntime struct {
	Dir       string `yaml:"dir"`
	Debug     string `yaml:"debug"`
	AgentPath string `yaml:"agent_path"`
}

type rawSystemBudget struct {
	MaxMemoryPercent float64 `yaml:"max_memory_percent"`
	MinFreeMemory    string  `yaml:"min_free_memory"`
	MaxLoadAvg       float64 `yaml:"max_load_avg"`
}

type rawSandboxProv struct {
	Name string `yaml:"name"`
}

type rawProcessGroup struct {
	Name      string       `yaml:"name"`
	Processes []rawProcess `yaml:"processes"`
}

type rawProcess struct {
	ID             string            `yaml:"id"`
	Name           string            `yaml:"name"`
	Command        string            `yaml:"command"`
	Args           []string          `yaml:"args"`
	Env            map[string]string `yaml:"env"`
	WorkDir        string            `yaml:"workdir"`
	Restart        string            `yaml:"restart"`
	MaxRestarts    int               `yaml:"max_restarts"`
	RestartBackoff string            `yaml:"restart_backoff"`
	StopTimeout    string            `yaml:"stop_timeout"`
	Stdin          string            `yaml:"stdin"`
	Resources      rawResources      `yaml:"resources"`
}

type rawResources struct {
	MaxRSS        string  `yaml:"max_rss"`
	MaxCPUPercent float64 `yaml:"max_cpu_percent"`
	OnExceed      string  `yaml:"on_exceed"`
}

func (r rawConfig) toCanonical() (*Config, error) {
	cfg := &Config{
		Version: r.Version,
		Runtime: RuntimeConfig{
			Dir:       r.Runtime.Dir,
			Debug:     r.Runtime.Debug,
			AgentPath: r.Runtime.AgentPath,
		},
	}

	if r.SystemBudget.MinFreeMemory != "" {
		v, err := ParseByteSize(r.SystemBudget.MinFreeMemory)
		if err != nil {
			return nil, err
		}
		cfg.SystemBudget.MinFreeMemory = v
	}
	cfg.SystemBudget.MaxMemoryPercent = r.SystemBudget.MaxMemoryPercent
	cfg.SystemBudget.MaxLoadAvg = r.SystemBudget.MaxLoadAvg

	for _, p := range r.SandboxProviders {
		cfg.SandboxProviders = append(cfg.SandboxProviders, p.Name)
	}

	for _, g := range r.Groups {
		group := ProcessGroup{Name: g.Name}
		for _, raw := range g.Processes {
			spec, err := rawProcessToSpec(raw)
			if err != nil {
				return nil, err
			}
			group.Processes = append(group.Processes, spec)
		}
		cfg.Groups = append(cfg.Groups, group)
	}
	return cfg, nil
}

func rawProcessToSpec(r rawProcess) (cliwrap.Spec, error) {
	spec := cliwrap.Spec{
		ID:          r.ID,
		Name:        r.Name,
		Command:     r.Command,
		Args:        r.Args,
		Env:         r.Env,
		WorkDir:     r.WorkDir,
		MaxRestarts: r.MaxRestarts,
	}

	if r.Restart != "" {
		p, err := parseRestart(r.Restart)
		if err != nil {
			return spec, err
		}
		spec.Restart = p
	}
	if r.RestartBackoff != "" {
		d, err := time.ParseDuration(r.RestartBackoff)
		if err != nil {
			return spec, fmt.Errorf("config: restart_backoff: %w", err)
		}
		spec.RestartBackoff = d
	}
	if r.StopTimeout != "" {
		d, err := time.ParseDuration(r.StopTimeout)
		if err != nil {
			return spec, fmt.Errorf("config: stop_timeout: %w", err)
		}
		spec.StopTimeout = d
	}
	if r.Stdin != "" {
		m, err := parseStdin(r.Stdin)
		if err != nil {
			return spec, err
		}
		spec.Stdin = m
	}
	if r.Resources.MaxRSS != "" {
		v, err := ParseByteSize(r.Resources.MaxRSS)
		if err != nil {
			return spec, err
		}
		spec.Resources.MaxRSS = v
	}
	spec.Resources.MaxCPUPercent = r.Resources.MaxCPUPercent
	if r.Resources.OnExceed != "" {
		a, err := parseExceed(r.Resources.OnExceed)
		if err != nil {
			return spec, err
		}
		spec.Resources.OnExceed = a
	}
	return spec, nil
}

func parseRestart(s string) (cliwrap.RestartPolicy, error) {
	switch s {
	case "never":
		return cliwrap.RestartNever, nil
	case "on_failure":
		return cliwrap.RestartOnFailure, nil
	case "always":
		return cliwrap.RestartAlways, nil
	}
	return 0, fmt.Errorf("config: unknown restart policy %q", s)
}

func parseStdin(s string) (cliwrap.StdinMode, error) {
	switch s {
	case "none":
		return cliwrap.StdinNone, nil
	case "pipe":
		return cliwrap.StdinPipe, nil
	case "inherit":
		return cliwrap.StdinInherit, nil
	}
	return 0, fmt.Errorf("config: unknown stdin mode %q", s)
}

func parseExceed(s string) (cliwrap.ExceedAction, error) {
	switch s {
	case "warn":
		return cliwrap.ExceedWarn, nil
	case "throttle":
		return cliwrap.ExceedThrottle, nil
	case "kill":
		return cliwrap.ExceedKill, nil
	}
	return 0, fmt.Errorf("config: unknown on_exceed %q", s)
}
```

- [x] **Step 5: Run the tests**

Run:

```bash
go test ./pkg/config/ -count=1 -race -v
```

Expected: all loader tests pass.

- [x] **Step 6: Commit**

```bash
git add pkg/config/ go.mod go.sum
git commit -m "feat(config): add YAML loader with env expansion and validation"
```

---

## Task 4: Management Protocol Messages

**Files:**
- Create: `internal/mgmt/proto.go`
- Create: `internal/mgmt/proto_test.go`

- [x] **Step 1: Write the failing test**

Create `internal/mgmt/proto_test.go`:

```go
package mgmt

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cli-wrapper/cli-wrapper/internal/ipc"
)

func TestProto_MessageTypesAreUnique(t *testing.T) {
	seen := map[ipc.MsgType]struct{}{}
	types := []ipc.MsgType{
		MsgListRequest, MsgListResponse,
		MsgStatusRequest, MsgStatusResponse,
		MsgStartRequest, MsgStopRequest,
		MsgLogsRequest, MsgLogsStream,
		MsgEventsSubscribe, MsgEventsStream,
	}
	for _, t := range types {
		_, dup := seen[t]
		require.False(t, dup, "duplicate type: %d", t)
		seen[t] = struct{}{}
	}
}

func TestProto_PayloadRoundTrip(t *testing.T) {
	in := ListResponsePayload{
		Entries: []ListEntry{
			{ID: "p1", Name: "proc 1", State: "running", ChildPID: 1000},
		},
	}
	data, err := ipc.EncodePayload(in)
	require.NoError(t, err)
	var out ListResponsePayload
	require.NoError(t, ipc.DecodePayload(data, &out))
	require.Equal(t, in, out)
}
```

- [x] **Step 2: Run to confirm it fails**

Run:

```bash
go test ./internal/mgmt/ -count=1
```

Expected: undefined.

- [x] **Step 3: Implement the types**

Create `internal/mgmt/proto.go`:

```go
// Package mgmt defines the control-socket protocol between the cliwrap
// management CLI and a running Manager.
package mgmt

import "github.com/cli-wrapper/cli-wrapper/internal/ipc"

// Management-specific message types. They reuse the ipc framing layer but
// use distinct numeric IDs in the 0xA0..0xAF range.
const (
	MsgListRequest     ipc.MsgType = 0xA0
	MsgListResponse    ipc.MsgType = 0xA1
	MsgStatusRequest   ipc.MsgType = 0xA2
	MsgStatusResponse  ipc.MsgType = 0xA3
	MsgLogsRequest     ipc.MsgType = 0xA4
	MsgLogsStream      ipc.MsgType = 0xA5
	MsgEventsSubscribe ipc.MsgType = 0xA6
	MsgEventsStream    ipc.MsgType = 0xA7
	MsgStartRequest    ipc.MsgType = 0xA8
	MsgStopRequest     ipc.MsgType = 0xA9
)

// ListEntry is one row in a list response.
type ListEntry struct {
	ID       string `msgpack:"id"`
	Name     string `msgpack:"name"`
	State    string `msgpack:"state"`
	ChildPID int    `msgpack:"pid"`
	RSS      uint64 `msgpack:"rss"`
}

// ListResponsePayload is returned for MsgListResponse.
type ListResponsePayload struct {
	Entries []ListEntry `msgpack:"entries"`
}

// StatusRequestPayload asks for a specific process's status.
type StatusRequestPayload struct {
	ID string `msgpack:"id"`
}

// StatusResponsePayload summarizes one process.
type StatusResponsePayload struct {
	Entry ListEntry `msgpack:"entry"`
	Err   string    `msgpack:"err"`
}

// StartRequestPayload requests that a registered process be started.
type StartRequestPayload struct {
	ID string `msgpack:"id"`
}

// StopRequestPayload requests that a running process be stopped.
type StopRequestPayload struct {
	ID string `msgpack:"id"`
}

// LogsRequestPayload asks for the ring-buffer snapshot of a process.
type LogsRequestPayload struct {
	ID     string `msgpack:"id"`
	Stream uint8  `msgpack:"s"`
}

// LogsStreamPayload carries a single chunk of streamed log bytes.
type LogsStreamPayload struct {
	ID     string `msgpack:"id"`
	Stream uint8  `msgpack:"s"`
	Data   []byte `msgpack:"d"`
	EOF    bool   `msgpack:"eof"`
}

// EventsSubscribePayload has no fields yet; included for future filters.
type EventsSubscribePayload struct{}

// EventsStreamPayload carries a serialized event summary.
type EventsStreamPayload struct {
	ProcessID string `msgpack:"pid"`
	Type      string `msgpack:"t"`
	Timestamp int64  `msgpack:"ts"`
	Summary   string `msgpack:"sum"`
}
```

- [x] **Step 4: Run the tests**

Run:

```bash
go test ./internal/mgmt/ -count=1 -v
```

Expected: two tests pass.

- [x] **Step 5: Commit**

```bash
git add internal/mgmt/proto.go internal/mgmt/proto_test.go
git commit -m "feat(mgmt): add management protocol message types and payloads"
```

---

## Task 5: Management Server

**Files:**
- Create: `internal/mgmt/server.go`
- Create: `internal/mgmt/server_test.go`

- [x] **Step 1: Write the failing test**

Create `internal/mgmt/server_test.go`:

```go
package mgmt

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/cli-wrapper/cli-wrapper/internal/ipc"
)

type fakeManager struct{}

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

var errNotFound = &notFoundError{}

type notFoundError struct{}

func (n *notFoundError) Error() string { return "not found" }

func TestServer_ListRoundTrip(t *testing.T) {
	defer goleak.VerifyNone(t)

	sockPath := filepath.Join(t.TempDir(), "mgr.sock")
	srv, err := NewServer(ServerOptions{
		SocketPath: sockPath,
		Manager:    &fakeManager{},
		SpillerDir: t.TempDir(),
	})
	require.NoError(t, err)
	require.NoError(t, srv.Start())
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Close(ctx)
	}()

	// Dial and send a LIST_REQUEST manually.
	conn, err := net.Dial("unix", sockPath)
	require.NoError(t, err)
	defer conn.Close()

	w := ipc.NewFrameWriter(conn)
	_, err = w.WriteFrame(ipc.Header{MsgType: MsgListRequest, SeqNo: 1, Length: 0}, nil)
	require.NoError(t, err)

	r := ipc.NewFrameReader(conn, ipc.MaxPayloadSize)
	h, body, err := r.ReadFrame()
	require.NoError(t, err)
	require.Equal(t, MsgListResponse, h.MsgType)

	var resp ListResponsePayload
	require.NoError(t, ipc.DecodePayload(body, &resp))
	require.Len(t, resp.Entries, 1)
	require.Equal(t, "p1", resp.Entries[0].ID)
}
```

- [x] **Step 2: Run to confirm it fails**

Run:

```bash
go test ./internal/mgmt/ -run TestServer -count=1
```

Expected: undefined.

- [x] **Step 3: Implement the server**

Create `internal/mgmt/server.go`:

```go
package mgmt

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/cli-wrapper/cli-wrapper/internal/ipc"
)

// ManagerAPI is the subset of Manager functionality the server exposes.
// Using an interface keeps tests decoupled from the concrete Manager.
type ManagerAPI interface {
	List() []ListEntry
	StatusOf(id string) (ListEntry, error)
	Stop(ctx context.Context, id string) error
}

// ServerOptions configures the management server.
type ServerOptions struct {
	SocketPath string
	Manager    ManagerAPI
	SpillerDir string
}

// Server listens on a Unix domain socket and handles management requests.
type Server struct {
	opts     ServerOptions
	listener net.Listener
	wg       sync.WaitGroup
	closed   bool
	mu       sync.Mutex
}

// NewServer returns a Server; call Start to begin accepting connections.
func NewServer(opts ServerOptions) (*Server, error) {
	if opts.SocketPath == "" {
		return nil, errors.New("mgmt: SocketPath required")
	}
	if opts.Manager == nil {
		return nil, errors.New("mgmt: Manager required")
	}
	if err := os.MkdirAll(filepath.Dir(opts.SocketPath), 0o700); err != nil {
		return nil, fmt.Errorf("mgmt: mkdir: %w", err)
	}
	_ = os.Remove(opts.SocketPath)
	ln, err := net.Listen("unix", opts.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("mgmt: listen: %w", err)
	}
	if err := os.Chmod(opts.SocketPath, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("mgmt: chmod socket: %w", err)
	}
	return &Server{opts: opts, listener: ln}, nil
}

// Start begins accepting connections in a background goroutine.
func (s *Server) Start() error {
	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	reader := ipc.NewFrameReader(conn, ipc.MaxPayloadSize)
	writer := ipc.NewFrameWriter(conn)

	for {
		h, body, err := reader.ReadFrame()
		if err != nil {
			return
		}
		resp, respType, rerr := s.handleRequest(h, body)
		if rerr != nil {
			return
		}
		data, err := ipc.EncodePayload(resp)
		if err != nil {
			return
		}
		_, err = writer.WriteFrame(ipc.Header{
			MsgType: respType,
			SeqNo:   h.SeqNo,
			Length:  uint32(len(data)),
		}, data)
		if err != nil {
			return
		}
	}
}

func (s *Server) handleRequest(h ipc.Header, body []byte) (any, ipc.MsgType, error) {
	ctx := context.Background()
	switch h.MsgType {
	case MsgListRequest:
		return ListResponsePayload{Entries: s.opts.Manager.List()}, MsgListResponse, nil
	case MsgStatusRequest:
		var req StatusRequestPayload
		_ = ipc.DecodePayload(body, &req)
		entry, err := s.opts.Manager.StatusOf(req.ID)
		resp := StatusResponsePayload{Entry: entry}
		if err != nil {
			resp.Err = err.Error()
		}
		return resp, MsgStatusResponse, nil
	case MsgStopRequest:
		var req StopRequestPayload
		_ = ipc.DecodePayload(body, &req)
		err := s.opts.Manager.Stop(ctx, req.ID)
		resp := StatusResponsePayload{}
		if err != nil {
			resp.Err = err.Error()
		}
		return resp, MsgStatusResponse, nil
	default:
		return StatusResponsePayload{Err: fmt.Sprintf("unknown request type 0x%02x", h.MsgType)}, MsgStatusResponse, nil
	}
}

// Close stops the listener and waits for goroutines to exit.
func (s *Server) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	_ = s.listener.Close()
	_ = os.Remove(s.opts.SocketPath)

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
```

- [x] **Step 4: Run the tests**

Run:

```bash
go test ./internal/mgmt/ -count=1 -race -v
```

Expected: tests pass with no goroutine leaks.

- [x] **Step 5: Commit**

```bash
git add internal/mgmt/server.go internal/mgmt/server_test.go
git commit -m "feat(mgmt): add management socket server"
```

---

## Task 6: Management Client Helper

**Files:**
- Create: `internal/mgmt/client.go`

- [x] **Step 1: Write the implementation (simple wrapper)**

Create `internal/mgmt/client.go`:

```go
package mgmt

import (
	"fmt"
	"net"

	"github.com/cli-wrapper/cli-wrapper/internal/ipc"
)

// Client is a synchronous client for the management socket. It is not
// safe for concurrent use — callers should open one per goroutine.
type Client struct {
	conn net.Conn
	r    *ipc.FrameReader
	w    *ipc.FrameWriter
	seq  uint64
}

// Dial opens a connection to the manager at socketPath.
func Dial(socketPath string) (*Client, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("mgmt: dial: %w", err)
	}
	return &Client{
		conn: conn,
		r:    ipc.NewFrameReader(conn, ipc.MaxPayloadSize),
		w:    ipc.NewFrameWriter(conn),
	}, nil
}

// Close releases the connection.
func (c *Client) Close() error { return c.conn.Close() }

// Call sends a request and returns the response header and body.
func (c *Client) Call(t ipc.MsgType, payload any) (ipc.Header, []byte, error) {
	c.seq++
	var data []byte
	if payload != nil {
		b, err := ipc.EncodePayload(payload)
		if err != nil {
			return ipc.Header{}, nil, err
		}
		data = b
	}
	_, err := c.w.WriteFrame(ipc.Header{
		MsgType: t,
		SeqNo:   c.seq,
		Length:  uint32(len(data)),
	}, data)
	if err != nil {
		return ipc.Header{}, nil, err
	}
	return c.r.ReadFrame()
}
```

- [x] **Step 2: Verify the package still builds**

Run:

```bash
go build ./internal/mgmt/
```

Expected: no errors.

- [x] **Step 3: Commit**

```bash
git add internal/mgmt/client.go
git commit -m "feat(mgmt): add client helper for management socket"
```

---

## Task 7: Manager ListEntry Adapter

**Files:**
- Create: `pkg/cliwrap/mgmt_api.go`
- Create: `pkg/cliwrap/mgmt_api_test.go`

- [x] **Step 1: Write the failing test**

Create `pkg/cliwrap/mgmt_api_test.go`:

```go
package cliwrap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cli-wrapper/cli-wrapper/internal/mgmt"
	"github.com/cli-wrapper/cli-wrapper/internal/supervise"
)

func TestManager_ListReturnsRegisteredProcesses(t *testing.T) {
	agentBin := supervise.BuildAgentForTest(t)
	mgr, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = mgr.Shutdown(ctx)
	}()

	spec, err := NewSpec("listed", "/bin/sh", "-c", "sleep 0.1").Build()
	require.NoError(t, err)
	_, err = mgr.Register(spec)
	require.NoError(t, err)

	var _ mgmt.ManagerAPI = mgr // compile-time assertion
	entries := mgr.List()
	require.Len(t, entries, 1)
	require.Equal(t, "listed", entries[0].ID)
}
```

- [x] **Step 2: Run to confirm it fails**

Run:

```bash
go test ./pkg/cliwrap/ -run TestManager_ListReturnsRegisteredProcesses -count=1
```

Expected: `mgr.List undefined`.

- [x] **Step 3: Implement the adapter**

Create `pkg/cliwrap/mgmt_api.go`:

```go
package cliwrap

import (
	"context"

	"github.com/cli-wrapper/cli-wrapper/internal/mgmt"
)

// List returns a snapshot of every registered process for the mgmt API.
func (m *Manager) List() []mgmt.ListEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]mgmt.ListEntry, 0, len(m.handles))
	for _, h := range m.handles {
		out = append(out, toListEntry(h))
	}
	return out
}

// StatusOf returns the status of a specific process for the mgmt API.
func (m *Manager) StatusOf(id string) (mgmt.ListEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.handles[id]
	if !ok {
		return mgmt.ListEntry{}, ErrProcessNotFound
	}
	return toListEntry(h), nil
}

// Stop is the mgmt API entry that routes a stop request to the handle.
func (m *Manager) Stop(ctx context.Context, id string) error {
	m.mu.Lock()
	h, ok := m.handles[id]
	m.mu.Unlock()
	if !ok {
		return ErrProcessNotFound
	}
	return h.Stop(ctx)
}

func toListEntry(h *processHandle) mgmt.ListEntry {
	st := h.Status()
	return mgmt.ListEntry{
		ID:       h.spec.ID,
		Name:     h.spec.Name,
		State:    st.State.String(),
		ChildPID: st.ChildPID,
		RSS:      st.RSS,
	}
}
```

- [x] **Step 4: Run the test**

Run:

```bash
go test ./pkg/cliwrap/ -run TestManager_ListReturnsRegisteredProcesses -count=1 -v
```

Expected: test passes.

- [x] **Step 5: Commit**

```bash
git add pkg/cliwrap/mgmt_api.go pkg/cliwrap/mgmt_api_test.go
git commit -m "feat(cliwrap): expose List/StatusOf/Stop to mgmt API"
```

---

## Task 8: cliwrap CLI Entry Point

**Files:**
- Create: `cmd/cliwrap/main.go`

- [x] **Step 1: Create the main file**

Create `cmd/cliwrap/main.go`:

```go
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: cliwrap <command> [args...]")
		fmt.Fprintln(os.Stderr, "commands: run, validate, list, status, stop, logs, events, version")
		os.Exit(2)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var exit int
	switch cmd {
	case "run":
		exit = runCommand(args)
	case "validate":
		exit = validateCommand(args)
	case "list":
		exit = listCommand(args)
	case "status":
		exit = statusCommand(args)
	case "stop":
		exit = stopCommand(args)
	case "logs":
		exit = logsCommand(args)
	case "events":
		exit = eventsCommand(args)
	case "version":
		fmt.Println("cliwrap dev")
		exit = 0
	case "-h", "--help", "help":
		fmt.Println("cliwrap: manage background CLI processes")
		exit = 0
	default:
		fmt.Fprintf(os.Stderr, "cliwrap: unknown command %q\n", cmd)
		exit = 2
	}
	os.Exit(exit)
}
```

- [x] **Step 2: Verify it builds (expects subcommand stubs to be created)**

Run:

```bash
go build ./cmd/cliwrap
```

Expected: fails because subcommand functions are not defined.

- [x] **Step 3: Commit scaffolding so subsequent tasks can fill in commands**

At this point the CLI won't compile until Tasks 9–12 create the subcommand files. That's fine — we commit the main stub separately.

Create `cmd/cliwrap/stubs.go` as a temporary placeholder:

```go
package main

// Placeholder stubs replaced in later tasks.
func runCommand(args []string) int      { return 0 }
func validateCommand(args []string) int { return 0 }
func listCommand(args []string) int     { return 0 }
func statusCommand(args []string) int   { return 0 }
func stopCommand(args []string) int     { return 0 }
func logsCommand(args []string) int     { return 0 }
func eventsCommand(args []string) int   { return 0 }
```

Run:

```bash
go build ./cmd/cliwrap
```

Expected: builds.

```bash
git add cmd/cliwrap/main.go cmd/cliwrap/stubs.go
git commit -m "feat(cli): scaffold cliwrap entry point with subcommand stubs"
```

---

## Task 9: `cliwrap run` and `cliwrap validate`

**Files:**
- Create: `cmd/cliwrap/cmd_run.go`
- Create: `cmd/cliwrap/cmd_validate.go`
- Modify: `cmd/cliwrap/stubs.go` (remove `runCommand` and `validateCommand`)

- [x] **Step 1: Implement `cliwrap validate`**

Create `cmd/cliwrap/cmd_validate.go`:

```go
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/cli-wrapper/cli-wrapper/pkg/config"
)

func validateCommand(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	path := fs.String("f", "cliwrap.yaml", "path to config file")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.LoadFile(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap: validate: %v\n", err)
		return 1
	}
	n := 0
	for _, g := range cfg.Groups {
		n += len(g.Processes)
	}
	fmt.Printf("OK: %d groups, %d processes\n", len(cfg.Groups), n)
	return 0
}
```

- [x] **Step 2: Implement `cliwrap run`**

Create `cmd/cliwrap/cmd_run.go`:

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cli-wrapper/cli-wrapper/internal/mgmt"
	"github.com/cli-wrapper/cli-wrapper/pkg/cliwrap"
	"github.com/cli-wrapper/cli-wrapper/pkg/config"
)

func runCommand(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	path := fs.String("f", "cliwrap.yaml", "path to config file")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.LoadFile(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap run: %v\n", err)
		return 1
	}
	if cfg.Runtime.Dir == "" {
		fmt.Fprintln(os.Stderr, "cliwrap run: runtime.dir is required")
		return 1
	}
	agentPath := cfg.Runtime.AgentPath
	if agentPath == "" {
		// Fall back to binary alongside this one.
		self, _ := os.Executable()
		agentPath = filepath.Join(filepath.Dir(self), "cliwrap-agent")
	}

	mgr, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentPath),
		cliwrap.WithRuntimeDir(cfg.Runtime.Dir),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap run: %v\n", err)
		return 1
	}

	// Write the runtime dir to a well-known cache file so that other
	// subcommands (e.g. cliwrap list) can discover the manager socket
	// without requiring the --runtime-dir flag.
	writeRuntimeHint(cfg.Runtime.Dir)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	handles := make([]cliwrap.ProcessHandle, 0)
	for _, g := range cfg.Groups {
		for _, spec := range g.Processes {
			h, err := mgr.Register(spec)
			if err != nil {
				fmt.Fprintf(os.Stderr, "cliwrap run: register %s: %v\n", spec.ID, err)
				_ = mgr.Shutdown(ctx)
				return 1
			}
			handles = append(handles, h)
		}
	}

	// Start management socket.
	srv, err := mgmt.NewServer(mgmt.ServerOptions{
		SocketPath: filepath.Join(cfg.Runtime.Dir, "manager.sock"),
		Manager:    mgr,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap run: mgmt server: %v\n", err)
		_ = mgr.Shutdown(ctx)
		return 1
	}
	_ = srv.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Close(ctx)
	}()

	for _, h := range handles {
		if err := h.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "cliwrap run: start %s: %v\n", h.ID(), err)
		}
	}

	fmt.Printf("cliwrap: running %d processes; Ctrl-C to exit\n", len(handles))
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = mgr.Shutdown(shutdownCtx)
	return 0
}

// writeRuntimeHint persists the runtime directory to a well-known cache path
// so that client commands (list, status, stop) running in a different terminal
// can locate the manager socket without explicit --runtime-dir.
func writeRuntimeHint(dir string) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return
	}
	hintDir := filepath.Join(cacheDir, "cliwrap")
	_ = os.MkdirAll(hintDir, 0o700)
	_ = os.WriteFile(filepath.Join(hintDir, "runtime-dir"), []byte(dir), 0o600)
}
```

- [x] **Step 3: Remove stubs for run/validate**

Edit `cmd/cliwrap/stubs.go` so it no longer declares these two functions:

```go
package main

// Placeholder stubs replaced in later tasks.
func listCommand(args []string) int   { return 0 }
func statusCommand(args []string) int { return 0 }
func stopCommand(args []string) int   { return 0 }
func logsCommand(args []string) int   { return 0 }
func eventsCommand(args []string) int { return 0 }
```

- [x] **Step 4: Build the CLI and run `validate` against a sample config**

Run:

```bash
go build -o ./bin/cliwrap ./cmd/cliwrap

cat > /tmp/cliwrap-test.yaml <<'YAML'
version: "1"
runtime:
  dir: /tmp/cliwrap-test
  debug: info
groups:
  - name: demo
    processes:
      - id: hello
        command: /bin/echo
        args: ["hello"]
        stop_timeout: 2s
YAML

./bin/cliwrap validate -f /tmp/cliwrap-test.yaml
```

Expected: `OK: 1 groups, 1 processes`.

- [x] **Step 5: Commit**

```bash
git add cmd/cliwrap/cmd_run.go cmd/cliwrap/cmd_validate.go cmd/cliwrap/stubs.go
git commit -m "feat(cli): implement cliwrap run and cliwrap validate"
```

---

## Task 10: `cliwrap list`, `status`, `stop`

**Files:**
- Create: `cmd/cliwrap/cmd_list.go`
- Create: `cmd/cliwrap/cmd_status.go`
- Create: `cmd/cliwrap/cmd_stop.go`
- Modify: `cmd/cliwrap/stubs.go`

- [x] **Step 1: Implement `cliwrap list`**

Create `cmd/cliwrap/cmd_list.go`:

```go
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/cli-wrapper/cli-wrapper/internal/ipc"
	"github.com/cli-wrapper/cli-wrapper/internal/mgmt"
)

func listCommand(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	runtimeDir := fs.String("runtime-dir", "", "override runtime directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	sock := managerSocketPath(*runtimeDir)
	cli, err := mgmt.Dial(sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap list: %v\n", err)
		return 1
	}
	defer cli.Close()

	_, body, err := cli.Call(mgmt.MsgListRequest, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap list: %v\n", err)
		return 1
	}

	var resp mgmt.ListResponsePayload
	if err := ipc.DecodePayload(body, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap list: decode: %v\n", err)
		return 1
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATE\tPID")
	for _, e := range resp.Entries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", e.ID, e.Name, e.State, e.ChildPID)
	}
	_ = tw.Flush()
	return 0
}

func managerSocketPath(override string) string {
	if override != "" {
		return filepath.Join(override, "manager.sock")
	}
	if v := os.Getenv("CLIWRAP_RUNTIME_DIR"); v != "" {
		return filepath.Join(v, "manager.sock")
	}
	// Read the runtime-dir hint file written by `cliwrap run`.
	if cacheDir, err := os.UserCacheDir(); err == nil {
		if b, err := os.ReadFile(filepath.Join(cacheDir, "cliwrap", "runtime-dir")); err == nil {
			return filepath.Join(strings.TrimSpace(string(b)), "manager.sock")
		}
	}
	return filepath.Join(os.TempDir(), "cliwrap", "manager.sock")
}
```

- [x] **Step 2: Implement `cliwrap status`**

Create `cmd/cliwrap/cmd_status.go`:

```go
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/cli-wrapper/cli-wrapper/internal/ipc"
	"github.com/cli-wrapper/cli-wrapper/internal/mgmt"
)

func statusCommand(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	runtimeDir := fs.String("runtime-dir", "", "override runtime directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: cliwrap status <id>")
		return 2
	}

	sock := managerSocketPath(*runtimeDir)
	cli, err := mgmt.Dial(sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap status: %v\n", err)
		return 1
	}
	defer cli.Close()

	_, body, err := cli.Call(mgmt.MsgStatusRequest, mgmt.StatusRequestPayload{ID: fs.Arg(0)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap status: %v\n", err)
		return 1
	}
	var resp mgmt.StatusResponsePayload
	if err := ipc.DecodePayload(body, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap status: decode: %v\n", err)
		return 1
	}
	if resp.Err != "" {
		fmt.Fprintln(os.Stderr, resp.Err)
		return 1
	}
	fmt.Printf("ID:      %s\n", resp.Entry.ID)
	fmt.Printf("Name:    %s\n", resp.Entry.Name)
	fmt.Printf("State:   %s\n", resp.Entry.State)
	fmt.Printf("ChildPID:%d\n", resp.Entry.ChildPID)
	return 0
}
```

- [x] **Step 3: Implement `cliwrap stop`**

Create `cmd/cliwrap/cmd_stop.go`:

```go
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/cli-wrapper/cli-wrapper/internal/ipc"
	"github.com/cli-wrapper/cli-wrapper/internal/mgmt"
)

func stopCommand(args []string) int {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	runtimeDir := fs.String("runtime-dir", "", "override runtime directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: cliwrap stop <id> [<id>...]")
		return 2
	}

	sock := managerSocketPath(*runtimeDir)
	cli, err := mgmt.Dial(sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cliwrap stop: %v\n", err)
		return 1
	}
	defer cli.Close()

	rc := 0
	for _, id := range fs.Args() {
		_, body, err := cli.Call(mgmt.MsgStopRequest, mgmt.StopRequestPayload{ID: id})
		if err != nil {
			fmt.Fprintf(os.Stderr, "cliwrap stop %s: %v\n", id, err)
			rc = 1
			continue
		}
		var resp mgmt.StatusResponsePayload
		_ = ipc.DecodePayload(body, &resp)
		if resp.Err != "" {
			fmt.Fprintf(os.Stderr, "cliwrap stop %s: %s\n", id, resp.Err)
			rc = 1
		} else {
			fmt.Printf("stopped %s\n", id)
		}
	}
	return rc
}
```

- [x] **Step 4: Remove the corresponding stubs**

Edit `cmd/cliwrap/stubs.go`:

```go
package main

// Remaining stubs. Replaced by Task 11.
func logsCommand(args []string) int   { return 0 }
func eventsCommand(args []string) int { return 0 }
```

- [x] **Step 5: Build and commit**

Run:

```bash
go build ./cmd/cliwrap
git add cmd/cliwrap/cmd_list.go cmd/cliwrap/cmd_status.go cmd/cliwrap/cmd_stop.go cmd/cliwrap/stubs.go
git commit -m "feat(cli): implement list, status, and stop subcommands"
```

---

## Task 11: `cliwrap logs` and `cliwrap events` (Stubs)

**Files:**
- Create: `cmd/cliwrap/cmd_logs.go`
- Create: `cmd/cliwrap/cmd_events.go`
- Modify: `cmd/cliwrap/stubs.go`

These commands remain skeletons because the server-side streaming is a Plan 05 concern. For v1, they print a friendly notice so the CLI surface is complete.

- [x] **Step 1: Create logs command**

Create `cmd/cliwrap/cmd_logs.go`:

```go
package main

import "fmt"

func logsCommand(args []string) int {
	fmt.Println("cliwrap logs: streaming not yet implemented (Plan 05)")
	return 0
}
```

- [x] **Step 2: Create events command**

Create `cmd/cliwrap/cmd_events.go`:

```go
package main

import "fmt"

func eventsCommand(args []string) int {
	fmt.Println("cliwrap events: streaming not yet implemented (Plan 05)")
	return 0
}
```

- [x] **Step 3: Delete `cmd/cliwrap/stubs.go`**

Run:

```bash
rm cmd/cliwrap/stubs.go
```

- [x] **Step 4: Build**

Run:

```bash
go build ./cmd/cliwrap
```

Expected: clean build.

- [x] **Step 5: Commit**

```bash
git add cmd/cliwrap/cmd_logs.go cmd/cliwrap/cmd_events.go
git rm cmd/cliwrap/stubs.go
git commit -m "feat(cli): stub logs and events subcommands"
```

---

## Verification Checklist

- [ ] `go test ./pkg/config/... -race` passes.
- [ ] `go test ./internal/mgmt/... -race` passes.
- [ ] `cliwrap validate -f config.yaml` returns 0 for valid configs and non-zero for invalid ones.
- [ ] `cliwrap run` starts every configured process and the management socket is created at `<runtime_dir>/manager.sock` with mode `0600`.
- [ ] `cliwrap list` against a running manager shows expected rows.

## What This Plan Does NOT Cover

- **Log streaming over the management socket** — Plan 05.
- **Event subscription streaming** — Plan 05.
- **`cliwrap attach`** — Plan 05.
- **Foreground TTY handling** beyond simple stdin forwarding — future work.
- **Sandbox provider registration from config file** (Plan 05 wires this to the existing `pkg/sandbox/providers`).
