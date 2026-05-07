// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/agent"
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
