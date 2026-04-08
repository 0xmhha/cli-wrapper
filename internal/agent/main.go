// Package agent contains the logic that runs inside the cliwrap-agent
// subprocess. It owns the child CLI process and the IPC connection back
// to the host.
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
