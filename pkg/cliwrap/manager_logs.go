// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"sync"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/logcollect"
)

// logsMu guards collectorOnce/collector so the lazy init is safe under
// concurrent callbacks arriving from multiple controllers.
//
// The collector itself is concurrency-safe (sync.RWMutex), but the
// pointer assignment during lazy init must be synchronized.
type managerLogs struct {
	collectorOnce sync.Once
	collector     *logcollect.Collector
}

// ensureCollector returns the lazily-constructed Collector. Thread-safe.
func (m *Manager) ensureCollector() *logcollect.Collector {
	m.logs.collectorOnce.Do(func() {
		m.logs.collector = logcollect.NewCollector(logcollect.CollectorOptions{
			// 1 MiB per (process, stream) — enough for several thousand
			// short log lines without unbounded memory growth.
			RingBufferBytes: 1 << 20,
		})
	})
	return m.logs.collector
}

// emitLogChunk is the hook wired into each Controller's OnLogChunk
// callback. It captures the processID from the closure and forwards
// the bytes into the shared Collector.
func (m *Manager) emitLogChunk(processID string, stream uint8, data []byte) {
	if len(data) == 0 {
		return
	}
	m.ensureCollector().Write(logcollect.LogEntry{
		ProcessID: processID,
		Stream:    stream,
		Timestamp: time.Now(),
		Data:      data,
	})
}

// LogsSnapshot returns the current ring-buffer contents for the given
// (processID, stream). Returns nil if no bytes have been recorded for
// that pair.
func (m *Manager) LogsSnapshot(processID string, stream uint8) []byte {
	// Read through ensureCollector so the first caller (typically a
	// mgmt client asking for logs of a process that hasn't written
	// anything yet) still sees nil instead of a panic.
	return m.ensureCollector().Snapshot(processID, stream)
}
