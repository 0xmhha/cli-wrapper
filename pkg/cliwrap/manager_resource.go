package cliwrap

// ChildPIDs returns a snapshot of running (process ID -> child PID) pairs.
// Processes that are not in StateRunning are omitted.
func (m *Manager) ChildPIDs() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int, len(m.handles))
	for id, h := range m.handles {
		h.mu.Lock()
		if h.ctrl != nil && h.ctrl.State() == StateRunning {
			out[id] = h.ctrl.ChildPID()
		}
		h.mu.Unlock()
	}
	return out
}
