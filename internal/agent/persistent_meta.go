// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

// PersistentMeta is the on-disk schema for persistent-session metadata.
// Stored at <sessionDir>/meta.json with mode 0600.
//
// The Version field is for future schema migrations. v1 is "1.0".
//
type PersistentMeta struct {
	Version   string       `json:"version"`
	ID        string       `json:"id"`
	Spec      cwtypes.Spec `json:"spec"`
	AgentPID  int          `json:"agentPID"`
	StartedAt time.Time    `json:"startedAt"`
}

// WritePersistentMeta serializes meta to <dir>/meta.json with mode 0600.
// Caller is responsible for creating <dir> with appropriate permissions
// before calling.
func WritePersistentMeta(dir string, meta PersistentMeta) error {
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("persistent: marshal meta: %w", err)
	}
	path := filepath.Join(dir, "meta.json")
	return os.WriteFile(path, b, 0o600)
}

// ReadPersistentMeta loads <dir>/meta.json. Returns wrapped err if missing
// or unparseable; callers should treat any error as "session not loadable".
func ReadPersistentMeta(dir string) (PersistentMeta, error) {
	var meta PersistentMeta
	path := filepath.Join(dir, "meta.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return meta, fmt.Errorf("persistent: read meta: %w", err)
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		return meta, fmt.Errorf("persistent: unmarshal meta: %w", err)
	}
	return meta, nil
}

// WritePidFile writes <dir>/pid as a single decimal line, mode 0600.
func WritePidFile(dir string, pid int) error {
	path := filepath.Join(dir, "pid")
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o600)
}

// ReadPidFile parses the integer in <dir>/pid.
func ReadPidFile(dir string) (int, error) {
	path := filepath.Join(dir, "pid")
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("persistent: read pid: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("persistent: parse pid %q: %w", string(b), err)
	}
	return pid, nil
}

// IsPidAlive returns true if the process exists and is reachable via signal 0.
//
// Caller must additionally compare meta.StartedAt to the agent's reported
// startup time to defend against PID rollover (handled in Manager.Reattach,
// CW-G4 Task 14).
func IsPidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 on POSIX: existence check. Returns nil if the process
	// exists and we have permission to signal it.
	return proc.Signal(syscall.Signal(0)) == nil
}
