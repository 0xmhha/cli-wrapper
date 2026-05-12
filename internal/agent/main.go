// SPDX-License-Identifier: Apache-2.0

// Package agent contains the logic that runs inside the cliwrap-agent
// subprocess. It owns the child CLI process and the IPC connection back
// to the host.
package agent

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

// Config is the runtime configuration passed from the host via env vars
// and argv. For v1, the fd index is fixed at 3.
type Config struct {
	IPCFD          int
	AgentID        string
	RuntimeDir     string
	OutboxCapacity int
	WALBytes       int64
	MaxRecvPayload uint32

	// Persistent enables CW-G4 daemon mode: SIGHUP ignored, UNIX listener
	// at <CLIWRAP_SESSION_DIR>/sock for reattach, in-memory PTY ring buffer.
	// Set by cmd/cliwrap-agent when --persistent argv flag is present.
	Persistent bool
}

// DefaultConfig returns sensible defaults, reading host-supplied env vars
// when present:
//
//   - CLIWRAP_AGENT_ID — overrides AgentID (set by spawner)
//   - CLIWRAP_AGENT_RUNTIME — overrides RuntimeDir (set by spawner)
//   - CLIWRAP_AGENT_OUTBOX_CAPACITY — overrides OutboxCapacity when set
//     and parseable as a positive integer. Lets hosts use
//     WithAgentOutboxCapacity to tune the agent's outbox for high-
//     throughput PTY scenarios without recompiling the agent binary.
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
	if v := os.Getenv("CLIWRAP_AGENT_OUTBOX_CAPACITY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.OutboxCapacity = n
		}
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

	// CW-G4: persistent bootstrap. When --persistent was passed, allocate
	// the per-session state (ring buffer, listener, meta + pid files) before
	// the IPC dispatcher is set up. The dispatcher receives a reference to
	// the ring buffer so PTY OnData tees output for redraw on reattach.
	var pst *persistentState
	agentStartedAt := time.Now().UTC()
	if cfg.Persistent {
		sessionDir := os.Getenv("CLIWRAP_SESSION_DIR")
		if sessionDir == "" {
			return fmt.Errorf("agent: --persistent set but CLIWRAP_SESSION_DIR empty")
		}
		// Spec is not yet known (delivered via MsgStartChild); use a
		// minimal Spec here. The real Spec is recorded when StartChild
		// arrives (Task 12d updates meta.json then if needed).
		// CW-G4: pass agentStartedAt so meta.json and the eventual
		// reattach Hello share the same value (PID-rollover defense).
		var pErr error
		pst, pErr = initPersistent(persistentInitOpts{
			SessionDir: sessionDir,
			AgentID:    cfg.AgentID,
			Spec:       cwtypes.Spec{ID: cfg.AgentID, Persistent: true},
			StartedAt:  agentStartedAt,
		})
		if pErr != nil {
			return fmt.Errorf("agent: init persistent: %w", pErr)
		}
		defer pst.Close()
	}

	f := os.NewFile(uintptr(cfg.IPCFD), "ipc")
	if f == nil {
		return fmt.Errorf("agent: ipc fd %d is not valid", cfg.IPCFD)
	}

	// CW-G4 fix: wrap the inherited socketpair fd in net.FileConn so the
	// runtime poller handles Read/Write. Without this, os.File-direct
	// Read uses blocking syscalls and Close cannot unblock a pending
	// Read — agent's readLoop hangs forever in Close, blocking persistent
	// session shutdown. (Non-persistent agents masked this because the
	// host SIGTERMs them and the process exits regardless.)
	netConn, err := net.FileConn(f)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("agent: file conn: %w", err)
	}
	_ = f.Close() // FileConn dups the fd; the original os.File is no longer needed.

	conn, err := ipc.NewConn(ipc.ConnConfig{
		RWC:            netConn,
		SpillerDir:     filepath.Join(cfg.RuntimeDir, "outbox"),
		Capacity:       cfg.OutboxCapacity,
		WALBytes:       cfg.WALBytes,
		MaxRecvPayload: cfg.MaxRecvPayload,
	})
	if err != nil {
		_ = netConn.Close()
		return fmt.Errorf("agent: ipc conn: %w", err)
	}

	d := NewDispatcher(conn)
	conn.OnMessage(d.Handle)

	// CW-G4: persistent-mode wiring.
	var shutdownReq chan struct{}
	if pst != nil {
		shutdownReq = make(chan struct{})
		d.SetPersistentShutdown(shutdownReq)
		d.SetPersistentSessionDir(pst.sessionDir)
		d.runner.SetPersistentRingBuffer(pst.ring)

		// Start the accept loop in a goroutine. Each accepted conn:
		// 1. Receives the reattach handshake (Hello + PTYRingDump).
		// 2. Is wrapped in a new ipc.Conn and adopted as the dispatcher's
		//    primary IPC channel (replacing the bootstrap socketpair or a
		//    prior reattach conn).
		// 3. Block until the new conn closes (host disconnect), then
		//    mark detached so the next reattach can succeed.
		go pst.runAcceptLoop(func(c net.Conn) {
			defer pst.markDetached()

			if err := pst.writeReattachHandshake(c, cfg.AgentID, agentStartedAt); err != nil {
				_ = c.Close()
				return
			}

			// Wrap in ipc.Conn — outbox dir is per-reattach so successive
			// reattachments don't clash on WAL files.
			adoptCfg := ipc.ConnConfig{
				RWC:            c,
				SpillerDir:     filepath.Join(cfg.RuntimeDir, fmt.Sprintf("outbox-reattach-%d", time.Now().UnixNano())),
				Capacity:       cfg.OutboxCapacity,
				WALBytes:       cfg.WALBytes,
				MaxRecvPayload: cfg.MaxRecvPayload,
			}
			newConn, err := ipc.NewConn(adoptCfg)
			if err != nil {
				_ = c.Close()
				return
			}
			newConn.OnMessage(d.Handle)
			d.AdoptConn(newConn)
			newConn.Start()

			// Block until the new conn's reader/writer goroutines exit
			// (host disconnected or ctx canceled). Then markDetached
			// releases the attach lock for the next reattach.
			<-newConn.Done()
		})
	}

	conn.Start()

	// Send initial HELLO. CW-G4: include StartedAt for the eventual
	// reattach handshake (PID rollover defense).
	helloPayload := ipc.HelloPayload{
		ProtocolVersion: ipc.ProtocolVersion,
		AgentID:         cfg.AgentID,
		StartedAt:       agentStartedAt.UnixNano(),
	}
	if err := d.SendControl(ipc.MsgHello, helloPayload, false); err != nil {
		return fmt.Errorf("agent: send hello: %w", err)
	}

	// CW-G4: persistent agents also exit when the child terminates
	// (Stop or natural completion). shutdownReq is non-nil only in
	// persistent mode; the nil-channel branch blocks forever, leaving
	// only ctx.Done as the wakeup for non-persistent agents.
	select {
	case <-ctx.Done():
		if os.Getenv("CLIWRAP_DEBUG") == "1" {
			fmt.Fprintf(os.Stderr, "AGENT: main woken by ctx.Done\n")
		}
	case <-shutdownReq:
		if os.Getenv("CLIWRAP_DEBUG") == "1" {
			fmt.Fprintf(os.Stderr, "AGENT: main woken by shutdownReq\n")
		}
	}

	// CW-G3: block until active runners have completed cmd.Wait on their
	// children before letting the agent process exit. The drain budget is
	// strictly less than the host's AgentHandle.Close 3s grace, so we
	// still complete conn.Close + return on time. Without this drain,
	// runner goroutines mid-wait4 are killed when the agent process exits,
	// leaving children orphaned to launchd.
	//
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	_ = d.Drain(drainCtx)
	drainCancel()
	if os.Getenv("CLIWRAP_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "AGENT: drain done, closing conn\n")
	}

	shutdownCtx, cancel := ctxWithTimeoutSeconds(5)
	defer cancel()
	_ = conn.Close(shutdownCtx)
	if os.Getenv("CLIWRAP_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "AGENT: conn closed, returning\n")
	}
	return nil
}
