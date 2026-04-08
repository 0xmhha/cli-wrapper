package cliwrap

import (
	"context"
	"errors"
	"sync"

	"github.com/cli-wrapper/cli-wrapper/internal/controller"
	"github.com/cli-wrapper/cli-wrapper/internal/eventbus"
	"github.com/cli-wrapper/cli-wrapper/internal/supervise"
)

// Manager is the top-level library entry point.
type Manager struct {
	agentPath  string
	runtimeDir string
	spawner    *supervise.Spawner

	mu      sync.Mutex
	handles map[string]*processHandle
	closed  bool

	// Plan 05 additions
	watcherOnce sync.Once
	watchStop   chan struct{}
	watchWG     sync.WaitGroup

	busOnce sync.Once
	bus     *eventbus.Bus
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
	m.spawner = supervise.NewSpawner(supervise.SpawnerOptions{
		AgentPath:  m.agentPath,
		RuntimeDir: m.runtimeDir,
	})
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

	for _, h := range handles {
		_ = h.Stop(ctx)
	}
	for _, h := range handles {
		_ = h.Close(ctx)
	}

	m.watchWG.Wait()
	if m.bus != nil {
		m.bus.Close()
	}
	return nil
}

func (m *Manager) newController(spec Spec) (*controller.Controller, error) {
	return controller.NewController(controller.ControllerOptions{
		Spec:       spec,
		Spawner:    m.spawner,
		RuntimeDir: m.runtimeDir,
	})
}
