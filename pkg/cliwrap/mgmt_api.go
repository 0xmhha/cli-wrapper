// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"context"

	"github.com/0xmhha/cli-wrapper/internal/mgmt"
)

// List returns a snapshot of every registered process for the mgmt API.
func (m *Manager) List() []mgmt.ListEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]mgmt.ListEntry, 0, len(m.handles))
	for _, h := range m.handles {
		out = append(out, toListEntry(h))
	}
	return out
}

// StatusOf returns the status of a specific process for the mgmt API.
func (m *Manager) StatusOf(id string) (mgmt.ListEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.handles[id]
	if !ok {
		return mgmt.ListEntry{}, ErrProcessNotFound
	}
	return toListEntry(h), nil
}

// Stop is the mgmt API entry that routes a stop request to the handle.
func (m *Manager) Stop(ctx context.Context, id string) error {
	m.mu.Lock()
	h, ok := m.handles[id]
	m.mu.Unlock()
	if !ok {
		return ErrProcessNotFound
	}
	return h.Stop(ctx)
}

func toListEntry(h *processHandle) mgmt.ListEntry {
	st := h.Status()
	return mgmt.ListEntry{
		ID:       h.spec.ID,
		Name:     h.spec.Name,
		State:    st.State.String(),
		ChildPID: st.ChildPID,
		RSS:      st.RSS,
	}
}
