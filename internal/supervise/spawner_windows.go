// SPDX-License-Identifier: Apache-2.0

//go:build windows

// Package supervise's Windows port is a roadmap item. The real spawner
// uses syscall.Socketpair (Unix-only) to set up the agent IPC channel
// and Setsid for daemonization (CW-G4). On Windows the equivalents are
// Named Pipes for IPC and Job Objects for process-group control;
// neither is wired yet. Until then this stub gives the rest of the
// codebase a compile-time foothold so non-spawn-touching packages can
// build and be cross-compiled CI-checked. Calling Spawn returns
// ErrUnsupportedOnWindows immediately.

package supervise

import (
	"context"
	"errors"
	"net"
	"os"
)

// ErrUnsupportedOnWindows is returned by Spawn on Windows builds.
var ErrUnsupportedOnWindows = errors.New("supervise: agent spawn not implemented on Windows (Named Pipes + Job Objects port pending)")

// SpawnerOptions mirrors the Unix definition. Fields that are silently
// honored on Unix (RuntimeDir, ExtraEnv) are accepted on Windows too,
// just unused.
type SpawnerOptions struct {
	AgentPath  string
	RuntimeDir string
	ExtraEnv   []string
}

// SpawnOpts mirrors the Unix definition.
type SpawnOpts struct {
	Persistent bool
	SessionDir string
}

// AgentHandle mirrors the Unix shape. Socket is net.Conn here (vs
// *net.UnixConn on Unix) since the Windows IPC channel will be a Named
// Pipe wrapped in net.Conn rather than a UnixConn.
type AgentHandle struct {
	Socket  net.Conn
	Process *os.Process
	PID     int
}

// Close is a no-op on Windows: the stub never returns a populated
// AgentHandle, so there is nothing to release.
func (h *AgentHandle) Close() error { return nil }

// Spawner mirrors the Unix shape so callers compile.
type Spawner struct {
	opts SpawnerOptions
}

// NewSpawner returns a Spawner. Construction always succeeds on Windows;
// the unsupported-platform error surfaces at Spawn time so callers can
// still wire the Spawner into their Manager.
func NewSpawner(opts SpawnerOptions) *Spawner { return &Spawner{opts: opts} }

// Options mirrors the Unix accessor so cross-package tests that
// verify env-var propagation (e.g. CLIWRAP_AGENT_OUTBOX_CAPACITY)
// compile on Windows. The Spawn path itself remains unsupported.
func (s *Spawner) Options() SpawnerOptions { return s.opts }

// Spawn returns ErrUnsupportedOnWindows. Implementing the Windows port
// requires a Named Pipe IPC layer (replacing syscall.Socketpair) plus
// Job Object integration for the cascade-on-host-exit semantics that
// CW-G3 + CW-G4 rely on. Tracking issue: TODO.md "Windows 지원".
func (s *Spawner) Spawn(ctx context.Context, agentID string, opts SpawnOpts) (*AgentHandle, error) {
	return nil, ErrUnsupportedOnWindows
}

// (BuildAgentForTest stays in testhelpers.go and is gated there.)
