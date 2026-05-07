// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/agent"
	"github.com/0xmhha/cli-wrapper/internal/controller"
	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

// PersistentSessionInfo summarizes a persistent session discovered on disk.
//
// CW-G4 §"Public API surface".
type PersistentSessionInfo struct {
	// ID is the session identifier (matches Spec.ID).
	ID string

	// Spec is the original cwtypes.Spec recorded at agent startup.
	Spec cwtypes.Spec

	// AgentPID is the agent process PID at startup. May be stale; use
	// Alive to interpret.
	AgentPID int

	// StartedAt is the agent's startup time (UTC nanoseconds).
	StartedAt time.Time

	// Alive is true iff kill -0 AgentPID succeeds. Best-effort: under
	// PID rollover, this can be a false positive — the actual Reattach
	// performs an additional StartedAt comparison via MsgHello.
	Alive bool

	// Attached is true iff the .attached flag file exists in the session
	// directory (best-effort indicator that another host is currently
	// connected). The actual Reattach attempt is the source of truth.
	Attached bool
}

// ListPersistent scans WithPersistentDir for valid sessions. Stale entries
// (Alive=false) are returned but not auto-cleaned; the user removes them
// via `cliwrap kill <id>` or `rm -rf <dir>/<id>`. Sessions are returned
// sorted by ID.
//
// CW-G4 Task 13.
func (m *Manager) ListPersistent() ([]PersistentSessionInfo, error) {
	entries, err := os.ReadDir(m.persistentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cliwrap: list persistent: %w", err)
	}
	var out []PersistentSessionInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sessionDir := filepath.Join(m.persistentDir, e.Name())
		meta, err := agent.ReadPersistentMeta(sessionDir)
		if err != nil {
			// Partially-written or unrelated dir; skip silently.
			continue
		}
		pid, err := agent.ReadPidFile(sessionDir)
		if err != nil {
			// pid file may have been removed by agent self-exit (post-mortem
			// state). Use AgentPID from meta as best-known.
			pid = meta.AgentPID
		}
		_, attachedErr := os.Stat(filepath.Join(sessionDir, ".attached"))
		out = append(out, PersistentSessionInfo{
			ID:        meta.ID,
			Spec:      meta.Spec,
			AgentPID:  pid,
			StartedAt: meta.StartedAt,
			Alive:     agent.IsPidAlive(pid),
			Attached:  attachedErr == nil,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Reattach connects to a previously-spawned persistent session by ID.
// On success, returns a ProcessHandle equivalent to one returned by
// Manager.Register followed by Start: WriteInput, SubscribePTYData,
// Signal, Resize, Status, Stop, and Close all behave identically.
//
// SubscribePTYData on a Reattach-derived handle yields the ring buffer
// snapshot first (as one or more chunks in chronological order), then
// transitions to live PTY data.
//
// Errors (see spec §"Error Matrix"):
//   - ErrSessionNotFound: <persistentDir>/<id>/ does not exist (or sock missing)
//   - ErrAgentDead: pid file points to a dead PID, or Hello.StartedAt does
//     not match meta.json.StartedAt (PID rollover defense)
//   - ErrAlreadyAttached: another host is currently connected
//   - ErrIncompatibleAgent: handshake parsing failure
//   - ctx.Err(): ctx canceled / deadline exceeded
//
// CW-G4 Task 14.
func (m *Manager) Reattach(ctx context.Context, id string) (ProcessHandle, error) {
	sessionDir := filepath.Join(m.persistentDir, id)

	// 1. Read and verify metadata.
	meta, err := agent.ReadPersistentMeta(sessionDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err) {
			return nil, ErrSessionNotFound
		}
		return nil, errors.Join(ErrSessionNotFound, err)
	}

	// 2. PID alive (kill -0). Stale pid → ErrAgentDead. Use AgentPID from
	// meta as fallback if pid file was already cleaned up by agent self-exit.
	pid, perr := agent.ReadPidFile(sessionDir)
	if perr != nil {
		pid = meta.AgentPID
	}
	if !agent.IsPidAlive(pid) {
		return nil, ErrAgentDead
	}

	// 3. Dial the per-session UNIX socket. Honor ctx for deadline.
	sockPath := filepath.Join(sessionDir, "sock")
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", sockPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrSessionNotFound
		}
		// ECONNREFUSED with a live PID means the agent crashed mid-shutdown
		// or hasn't started its listener yet. Either way, treat as dead.
		return nil, errors.Join(ErrAgentDead, err)
	}

	// 4. Read MsgHello + verify StartedAt (PID rollover defense).
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	hello, err := controller.ReadHandshakeHello(conn)
	if err != nil {
		_ = conn.Close()
		return nil, errors.Join(ErrIncompatibleAgent, err)
	}
	helloStartedAt := time.Unix(0, hello.StartedAt).UTC()
	if !meta.StartedAt.Equal(helloStartedAt) {
		_ = conn.Close()
		return nil, ErrAgentDead
	}

	// 5. Read MsgPTYRingDump.
	dump, err := controller.ReadHandshakePTYRingDump(conn)
	if err != nil {
		_ = conn.Close()
		return nil, errors.Join(ErrIncompatibleAgent, err)
	}
	// Clear the read deadline before handing to ipc.Conn.
	_ = conn.SetDeadline(time.Time{})

	// 6. Construct controller around the post-handshake conn.
	ctrl, err := controller.NewControllerFromReattach(controller.ReattachOptions{
		Spec:          meta.Spec,
		Conn:          conn,
		InitialBuffer: dump.Bytes,
		RuntimeDir:    m.runtimeDir,
		PersistentDir: m.persistentDir,
		OnLogChunk: func(stream uint8, data []byte) {
			m.emitLogChunk(meta.ID, stream, data)
		},
	})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("cliwrap: reattach: %w", err)
	}

	// 7. Build a processHandle and register with the manager.
	m.mu.Lock()
	if _, exists := m.handles[meta.ID]; exists {
		m.mu.Unlock()
		_ = ctrl.Close(context.Background())
		return nil, ErrProcessAlreadyExists
	}
	h := &processHandle{spec: meta.Spec, mgr: m, ctrl: ctrl}
	m.handles[meta.ID] = h
	m.startWatcherOnce()
	m.mu.Unlock()

	return h, nil
}
