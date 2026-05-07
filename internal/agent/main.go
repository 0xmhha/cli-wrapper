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
		var pErr error
		pst, pErr = initPersistent(persistentInitOpts{
			SessionDir: sessionDir,
			AgentID:    cfg.AgentID,
			Spec:       cwtypes.Spec{ID: cfg.AgentID, Persistent: true},
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

	// CW-G4: tee PTY output to ring buffer for redraw-on-reattach.
	if pst != nil {
		d.runner.SetPersistentRingBuffer(pst.ring)

		// Start the accept loop in a goroutine. Each accepted conn receives
		// the reattach handshake (Hello + PTYRingDump) and is then closed.
		// Full conn-adoption (replacing the bootstrap socketpair as the
		// primary IPC channel) is Task 12e.
		go pst.runAcceptLoop(func(c net.Conn) {
			defer pst.markDetached()
			defer func() { _ = c.Close() }()
			if err := pst.writeReattachHandshake(c, cfg.AgentID, agentStartedAt); err != nil {
				// Best-effort: hand-off failed; conn closed by defer,
				// markDetached releases the attach lock so a future reattach
				// can try again.
				return
			}
			// Task 12e will replace this with conn-adoption logic that
			// keeps the conn alive and rewires it as the primary IPC
			// channel. For now, hold briefly so the host can read the
			// handshake before EOF.
			//
			// TODO(CW-G4 Task 12e): adopt conn as primary IPC.
			time.Sleep(200 * time.Millisecond)
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

	<-ctx.Done()

	// CW-G3: block until active runners have completed cmd.Wait on their
	// children before letting the agent process exit. The drain budget is
	// strictly less than the host's AgentHandle.Close 3s grace, so we
	// still complete conn.Close + return on time. Without this drain,
	// runner goroutines mid-wait4 are killed when the agent process exits,
	// leaving children orphaned to launchd.
	//
	// Spec: docs/superpowers/specs/2026-05-04-CW-G3-supervision-leak-design.md
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	_ = d.Drain(drainCtx)
	drainCancel()

	shutdownCtx, cancel := ctxWithTimeoutSeconds(5)
	defer cancel()
	_ = conn.Close(shutdownCtx)
	return nil
}
