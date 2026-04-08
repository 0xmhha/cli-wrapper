package cliwrap

import (
	"time"

	"github.com/0xmhha/cli-wrapper/internal/eventbus"
	"github.com/0xmhha/cli-wrapper/pkg/event"
)

// Events returns the Manager's event bus. Subscribers receive lifecycle,
// log, and reliability events. The bus is lazily created on first call.
func (m *Manager) Events() event.Bus {
	m.busOnce.Do(func() {
		m.bus = eventbus.New(256)
	})
	return m.bus
}

// emit is a helper for controllers to publish events.
func (m *Manager) emit(e event.Event) {
	m.Events().(*eventbus.Bus).Publish(e)
}

// startWatcherOnce launches the state-watcher goroutine.
func (m *Manager) startWatcherOnce() {
	m.watcherOnce.Do(func() {
		m.watchWG.Add(1)
		go func() {
			defer m.watchWG.Done()
			m.watchLoop()
		}()
	})
}

// handleSeen tracks which lifecycle events we have already emitted for
// a given handle, so we can synthesize any missed intermediate
// transitions when the watcher polls slower than state changes.
type handleSeen struct {
	lastState    State
	emitStarting bool
	emitStarted  bool
	emitStopped  bool
	emitCrashed  bool
}

func (m *Manager) watchLoop() {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	seen := map[string]*handleSeen{}

	for {
		select {
		case <-m.watchStop:
			return
		case <-ticker.C:
		}

		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return
		}
		// Snapshot handles to release the lock before calling Status() (which
		// itself takes h.mu). This avoids a deadlock if Status() ever calls
		// back into the manager.
		snapshot := make(map[string]*processHandle, len(m.handles))
		for id, h := range m.handles {
			snapshot[id] = h
		}
		m.mu.Unlock()

		for id, h := range snapshot {
			st := h.Status().State
			s, ok := seen[id]
			if !ok {
				s = &handleSeen{}
				seen[id] = s
			}
			if st != s.lastState {
				m.publishTransition(id, st, s)
				s.lastState = st
			}
		}
	}
}

// publishTransition emits lifecycle events for the current state,
// synthesizing any intermediate events that we never observed. This
// guarantees callers see Started before Stopped even when the underlying
// process transitions faster than the poll interval.
func (m *Manager) publishTransition(id string, st State, s *handleSeen) {
	now := time.Now()
	switch st {
	case StateStarting:
		if !s.emitStarting {
			m.emit(event.NewProcessStarting(id, now))
			s.emitStarting = true
		}
	case StateRunning:
		if !s.emitStarting {
			m.emit(event.NewProcessStarting(id, now))
			s.emitStarting = true
		}
		if !s.emitStarted {
			m.emit(event.NewProcessStarted(id, now, 0, 0))
			s.emitStarted = true
		}
	case StateStopped:
		if !s.emitStarting {
			m.emit(event.NewProcessStarting(id, now))
			s.emitStarting = true
		}
		if !s.emitStarted {
			m.emit(event.NewProcessStarted(id, now, 0, 0))
			s.emitStarted = true
		}
		if !s.emitStopped {
			m.emit(event.NewProcessStopped(id, now, 0))
			s.emitStopped = true
		}
	case StateCrashed:
		if !s.emitStarting {
			m.emit(event.NewProcessStarting(id, now))
			s.emitStarting = true
		}
		if !s.emitStarted {
			m.emit(event.NewProcessStarted(id, now, 0, 0))
			s.emitStarted = true
		}
		if !s.emitCrashed {
			m.emit(event.NewProcessCrashed(id, now, event.CrashContext{Source: "unknown"}, false, 0))
			s.emitCrashed = true
		}
	}
}
