// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/0xmhha/cli-wrapper/internal/controller"
	"github.com/0xmhha/cli-wrapper/internal/eventbus"
	"github.com/0xmhha/cli-wrapper/internal/supervise"
)

// Manager is the top-level library entry point.
type Manager struct {
	agentPath          string
	runtimeDir         string
	persistentDir      string // CW-G4: dir holding per-session metadata for Persistent=true sessions
	logRingBufferBytes int    // 0 = DefaultLogRingBufferBytes
	logFileDir         string // "" = disabled
	logFileMaxSize     int64  // 0 = FileRotator default
	logFileMaxFiles    int    // 0 = FileRotator default
	outboxCapacity     int    // 0 = controller default (1024)
	// agentOutboxCapacity, when > 0, is propagated to the agent process
	// via the CLIWRAP_AGENT_OUTBOX_CAPACITY env var. Mirrors
	// outboxCapacity for the host side; lets high-throughput cases tune
	// the agent's outbox to avoid agent-side spills.
	agentOutboxCapacity int
	disableWAL          bool // false = WAL on (legacy default)
	spawner             *supervise.Spawner

	mu      sync.Mutex
	handles map[string]*processHandle
	closed  bool

	// Plan 05 additions
	watcherOnce sync.Once
	watchStop   chan struct{}
	watchWG     sync.WaitGroup

	busOnce sync.Once
	bus     *eventbus.Bus

	// cliwrap logs pipeline — Collector is lazy-constructed.
	logs managerLogs
}

// NewManager constructs a Manager with the given options.
func NewManager(opts ...ManagerOption) (*Manager, error) {
	m := &Manager{
		handles:   map[string]*processHandle{},
		watchStop: make(chan struct{}),
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.agentPath == "" {
		return nil, errors.New("cliwrap: WithAgentPath is required")
	}
	if m.runtimeDir == "" {
		return nil, errors.New("cliwrap: WithRuntimeDir is required")
	}
	// CW-G4: persistentDir defaults to $HOME/.cliwrap/sessions/. Created
	// lazily on first Persistent=true Spawn (mode 0700).
	if m.persistentDir == "" {
		m.persistentDir = filepath.Join(os.Getenv("HOME"), ".cliwrap", "sessions")
	}
	spawnOpts := supervise.SpawnerOptions{
		AgentPath:  m.agentPath,
		RuntimeDir: m.runtimeDir,
	}
	// Propagate agent-side outbox tuning via env var. The agent's
	// DefaultConfig reads CLIWRAP_AGENT_OUTBOX_CAPACITY and overrides
	// its hardcoded 1024 default when the value parses as a positive
	// integer. Mirrors the host-side WithOutboxCapacity surface
	// introduced for v0.4.0.
	if m.agentOutboxCapacity > 0 {
		spawnOpts.ExtraEnv = append(spawnOpts.ExtraEnv,
			fmt.Sprintf("CLIWRAP_AGENT_OUTBOX_CAPACITY=%d", m.agentOutboxCapacity))
	}
	m.spawner = supervise.NewSpawner(spawnOpts)
	return m, nil
}

// Register validates and registers a process spec.
func (m *Manager) Register(spec Spec) (ProcessHandle, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrManagerClosed
	}
	if _, exists := m.handles[spec.ID]; exists {
		return nil, ErrProcessAlreadyExists
	}
	h := &processHandle{spec: spec, mgr: m}
	m.handles[spec.ID] = h
	m.startWatcherOnce()
	return h, nil
}

// Shutdown stops every process and releases resources.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	handles := make([]*processHandle, 0, len(m.handles))
	for _, h := range m.handles {
		handles = append(handles, h)
	}
	m.mu.Unlock()

	// Stop the watcher loop (but do not wait yet; the watcher may be
	// mid-iteration and must finish its current pass before we close
	// handles).
	select {
	case <-m.watchStop:
	default:
		close(m.watchStop)
	}

	// CW-G4: persistent sessions are designed to outlive Manager.Shutdown.
	// For them, skip Stop (which would terminate the session) and only
	// Close the handle (which releases the host's conn but leaves the
	// agent running). Non-persistent sessions follow the historical
	// Stop+Close path.
	for _, h := range handles {
		if h.spec.Persistent {
			continue
		}
		_ = h.Stop(ctx)
	}
	for _, h := range handles {
		_ = h.Close(ctx)
	}

	m.watchWG.Wait()
	// Release any server-side follow handlers blocked on WatchLogs
	// channels so they can exit cleanly before we tear down the
	// rest of the manager. Done before bus.Close() so the ordering
	// between log-watcher shutdown and event-bus shutdown stays
	// symmetrical with the ordering in which they were lazily
	// constructed (collector first via emitLogChunk, then the bus
	// via Events()/watchLoop).
	m.closeLogWatchers()
	if m.bus != nil {
		m.bus.Close()
	}
	return nil
}

func (m *Manager) newController(spec Spec) (*controller.Controller, error) {
	id := spec.ID
	return controller.NewController(controller.ControllerOptions{
		Spec:           spec,
		Spawner:        m.spawner,
		RuntimeDir:     m.runtimeDir,
		PersistentDir:  m.persistentDir,
		OutboxCapacity: m.outboxCapacity,
		DisableWAL:     m.disableWAL,
		OnLogChunk: func(stream uint8, data []byte) {
			m.emitLogChunk(id, stream, data)
		},
	})
}
