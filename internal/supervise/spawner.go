// SPDX-License-Identifier: Apache-2.0

package supervise

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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

// SpawnOpts is per-spawn customization passed to Spawner.Spawn.
//
// Default zero value spawns a non-persistent agent (identical to historical
// behavior; agent is in host's process group, dies with host on terminal
// close). When Persistent=true, the agent is daemonized with Setsid and
// stdio redirected to <SessionDir>/agent.log.
type SpawnOpts struct {
	// Persistent enables CW-G4 daemonization for this agent.
	Persistent bool

	// SessionDir is the per-session metadata directory (sock, pid,
	// meta.json, agent.log live here). Required when Persistent=true.
	// Created with mode 0700 if it does not exist.
	SessionDir string
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
		go func() {
			_, _ = h.Process.Wait()
			close(done)
		}()
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
//
// When opts.Persistent is true, the spawned agent is daemonized: Setsid=true
// (new POSIX session, no controlling terminal), stdio redirected to
// <opts.SessionDir>/agent.log, cwd set to "/", and --persistent argv flag
// + CLIWRAP_PERSISTENT=1 + CLIWRAP_SESSION_DIR=<dir> env vars passed.
// See spec/2026-05-07-CW-G4-persistent-reattach-design.md.
func (s *Spawner) Spawn(ctx context.Context, agentID string, opts SpawnOpts) (*AgentHandle, error) {
	if s.opts.AgentPath == "" {
		return nil, errors.New("supervise: AgentPath is required")
	}
	if opts.Persistent && opts.SessionDir == "" {
		return nil, errors.New("supervise: SessionDir required when Persistent=true")
	}

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("supervise: socketpair: %w", err)
	}
	hostFD := os.NewFile(uintptr(fds[0]), "host-ipc")
	agentFD := os.NewFile(uintptr(fds[1]), "agent-ipc")

	// Use exec.Command (not CommandContext) so the agent's lifetime is
	// controlled solely by AgentHandle.Close, not the spawn ctx. The spawn
	// ctx is only used for the handshake timeout (which the caller enforces).
	_ = ctx
	cmd := exec.Command(s.opts.AgentPath)
	cmd.ExtraFiles = []*os.File{agentFD}
	cmd.Env = append(os.Environ(), "CLIWRAP_AGENT_ID="+agentID)
	cmd.Env = append(cmd.Env, s.opts.ExtraEnv...)
	if s.opts.RuntimeDir != "" {
		cmd.Env = append(cmd.Env, "CLIWRAP_AGENT_RUNTIME="+s.opts.RuntimeDir)
	}

	// CW-G4: persistent branch. Daemonize the agent.
	var persistentLog *os.File
	if opts.Persistent {
		if err := os.MkdirAll(opts.SessionDir, 0o700); err != nil {
			_ = hostFD.Close()
			_ = agentFD.Close()
			return nil, fmt.Errorf("supervise: mkdir SessionDir: %w", err)
		}
		persistentLog, err = os.OpenFile(
			filepath.Join(opts.SessionDir, "agent.log"),
			os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			_ = hostFD.Close()
			_ = agentFD.Close()
			return nil, fmt.Errorf("supervise: open agent.log: %w", err)
		}
		cmd.Stdin = nil
		cmd.Stdout = persistentLog
		cmd.Stderr = persistentLog
		cmd.Dir = "/"
		cmd.Env = append(cmd.Env,
			"CLIWRAP_PERSISTENT=1",
			"CLIWRAP_SESSION_DIR="+opts.SessionDir,
		)
		cmd.Args = append(cmd.Args, "--persistent")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}

	if err := cmd.Start(); err != nil {
		if persistentLog != nil {
			_ = persistentLog.Close()
		}
		_ = hostFD.Close()
		_ = agentFD.Close()
		return nil, fmt.Errorf("supervise: start agent: %w", err)
	}
	if persistentLog != nil {
		// Child inherited the fd via Stdout/Stderr; parent doesn't need
		// its copy.
		_ = persistentLog.Close()
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
