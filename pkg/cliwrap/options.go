// SPDX-License-Identifier: Apache-2.0

package cliwrap

// ManagerOption mutates a Manager during construction.
type ManagerOption func(*Manager)

// WithAgentPath sets the absolute path to the cliwrap-agent binary.
func WithAgentPath(path string) ManagerOption {
	return func(m *Manager) { m.agentPath = path }
}

// WithRuntimeDir sets the directory for per-process state (WAL, logs).
func WithRuntimeDir(path string) ManagerOption {
	return func(m *Manager) { m.runtimeDir = path }
}

// WithPersistentDir overrides the directory where persistent-session
// metadata (sock, pid, meta.json, agent.log) is stored. Default:
// $HOME/.cliwrap/sessions/. The directory is created with mode 0700
// lazily on first persistent Spawn; per-session subdirectories are
// 0700 with files 0600.
//
// Tests should pass t.TempDir() to keep state isolated. CW-G4.
func WithPersistentDir(path string) ManagerOption {
	return func(m *Manager) { m.persistentDir = path }
}
