// SPDX-License-Identifier: Apache-2.0

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
	// ctx is only used for the handshake timeout (which the caller enforces).
	_ = ctx
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
