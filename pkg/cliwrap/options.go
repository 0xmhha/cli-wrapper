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
