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

// DefaultLogRingBufferBytes is the per-(process, stream) log ring buffer
// capacity used when neither WithLogRingBufferBytes nor the YAML
// `log.ring_buffer_bytes` key is set.
const DefaultLogRingBufferBytes = 1 << 20 // 1 MiB

// WithLogRingBufferBytes overrides the per-(process, stream) log ring
// buffer capacity. Smaller values reduce memory at the cost of evicting
// older log lines sooner. Zero or negative values fall back to
// DefaultLogRingBufferBytes.
func WithLogRingBufferBytes(n int) ManagerOption {
	return func(m *Manager) { m.logRingBufferBytes = n }
}

// WithLogFileDir enables file-backed log persistence. Each (process,
// stream) pair gets its own rotated log file at
// `<dir>/<processID>.<stream>.log`, with `<processID>.<stream>.1.log`,
// `.2.log`, ... as rotated siblings. Files coexist with the in-memory
// ring buffer: live snapshots and `cliwrap logs` continue to read from
// memory; the on-disk copies survive Manager shutdown for postmortem.
//
// Empty path disables file persistence (default). Per-rotator MaxSize
// and MaxFiles use FileRotator defaults (64 MiB and 7) unless overridden
// via WithLogFileRotation.
func WithLogFileDir(dir string) ManagerOption {
	return func(m *Manager) { m.logFileDir = dir }
}

// WithLogFileRotation overrides the per-rotator size cap and retention
// count used by WithLogFileDir. Zero values fall back to FileRotator
// defaults (64 MiB, 7 files). No effect when WithLogFileDir is unset.
func WithLogFileRotation(maxSize int64, maxFiles int) ManagerOption {
	return func(m *Manager) {
		m.logFileMaxSize = maxSize
		m.logFileMaxFiles = maxFiles
	}
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

// WithOutboxCapacity overrides the IPC outbox slot count for every
// session created by this Manager. Zero or negative values fall back to
// the legacy default (1024). Larger buffers reduce overflow likelihood
// under bursty traffic at the cost of memory; for interactive PTY hosts
// values of 4096–16384 are typical.
func WithOutboxCapacity(n int) ManagerOption {
	return func(m *Manager) { m.outboxCapacity = n }
}

// WithAgentOutboxCapacity sets the IPC outbox slot count INSIDE the
// agent process. WithOutboxCapacity tunes the host's outbox (host →
// agent direction); this option tunes the agent's outbox (agent →
// host direction), where PTY output frames accumulate when the host's
// SubscribePTYData consumer is slow or backpressured.
//
// Zero or negative values keep the agent's legacy default (1024).
// Propagated via the CLIWRAP_AGENT_OUTBOX_CAPACITY env var that the
// spawner sets on every spawned agent; the agent's DefaultConfig
// honors the variable when its value parses as a positive integer.
//
// For interactive PTY hosts whose terminals briefly stall (window
// resize, paste, scrollback dump), values of 4096–16384 match the
// guidance for the host side.
func WithAgentOutboxCapacity(n int) ManagerOption {
	return func(m *Manager) { m.agentOutboxCapacity = n }
}

// WithoutWAL disables the disk-backed write-ahead log on the outgoing
// IPC direction for every session created by this Manager. When set,
// outbox overflow drops messages instead of fsync-spilling to disk —
// the host trades the legacy "messages are never lost" guarantee for
// predictable per-keystroke latency under load.
//
// Recommended for interactive PTY hosts where dropped input is recovered
// by user retry. Not recommended for batch / persistent / replay-critical
// workloads.
func WithoutWAL() ManagerOption {
	return func(m *Manager) { m.disableWAL = true }
}
