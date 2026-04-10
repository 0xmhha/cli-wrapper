// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"sync"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/internal/logcollect"
)

// LogChunk is an alias for cwtypes.LogChunk, re-exported so public
// callers of pkg/cliwrap see a cliwrap.LogChunk type while the
// canonical definition lives in the leaf cwtypes package (which
// internal/mgmt can import without creating a cycle).
type LogChunk = cwtypes.LogChunk

// managerLogs holds the per-Manager log pipeline state:
//
//   - collectorOnce / collector: lazy-initialized ring-buffer sink that
//     receives every byte emitted by child processes. sync.Once guards
//     the one-time construction; the Collector itself is internally
//     concurrency-safe.
//
//   - watchersMu / watchers: registry of live follow-mode subscribers
//     (see WatchLogs). emitLogChunk fans out each chunk to every
//     matching watcher before returning. Writes into a watcher channel
//     are non-blocking; a full channel drops the chunk for that
//     watcher.
type managerLogs struct {
	collectorOnce sync.Once
	collector     *logcollect.Collector

	watchersMu sync.RWMutex
	watchers   []*logWatcher
}

// logWatcher is the internal registration record for a WatchLogs caller.
type logWatcher struct {
	processID string // "" = match any process
	ch        chan LogChunk
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
// callback. It writes the bytes into the shared Collector AND fans
// them out to any active WatchLogs subscribers.
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

	// Fan out to live follow-mode watchers. Snapshot the watcher
	// slice under the read lock so the actual send happens outside
	// the lock (non-blocking; a full channel just drops the chunk
	// for that watcher rather than stalling the emit path).
	m.logs.watchersMu.RLock()
	var active []*logWatcher
	for _, w := range m.logs.watchers {
		if w.processID == "" || w.processID == processID {
			active = append(active, w)
		}
	}
	m.logs.watchersMu.RUnlock()

	if len(active) == 0 {
		return
	}
	// Defensive copy once — the underlying byte slice comes from the
	// controller's OnLogChunk callback which itself already copied,
	// but since we may fan out to multiple receivers we copy once
	// more here so callers cannot race with each other.
	buf := make([]byte, len(data))
	copy(buf, data)
	chunk := LogChunk{ProcessID: processID, Stream: stream, Data: buf}
	for _, w := range active {
		select {
		case w.ch <- chunk:
		default:
			// Watcher channel full — drop the chunk for this watcher.
			// Follow mode is best-effort; the client sees a gap.
		}
	}
}

// WatchLogs registers a follow-mode subscription. The returned channel
// receives LogChunk values for every subsequent call to emitLogChunk
// whose processID matches the filter. An empty processID filter
// matches every process.
//
// The caller MUST invoke the returned unregister function exactly once
// when done — this is typically done via defer. Failing to unregister
// leaks the subscription and the goroutine that reads from it.
//
// The channel is buffered; emit-side fan-out is non-blocking, so slow
// consumers lose chunks rather than backpressuring the producer.
func (m *Manager) WatchLogs(processID string) (chunks <-chan LogChunk, unregister func()) {
	w := &logWatcher{
		processID: processID,
		ch:        make(chan LogChunk, 256),
	}
	m.logs.watchersMu.Lock()
	m.logs.watchers = append(m.logs.watchers, w)
	m.logs.watchersMu.Unlock()

	unregister = func() {
		m.logs.watchersMu.Lock()
		defer m.logs.watchersMu.Unlock()
		for i, x := range m.logs.watchers {
			if x == w {
				m.logs.watchers = append(m.logs.watchers[:i], m.logs.watchers[i+1:]...)
				close(w.ch)
				return
			}
		}
	}
	chunks = w.ch
	return
}

// closeLogWatchers closes every registered log watcher's channel.
// Called during Manager.Shutdown so follow-mode server handlers can
// detect the shutdown via a closed channel and exit their loops.
func (m *Manager) closeLogWatchers() {
	m.logs.watchersMu.Lock()
	defer m.logs.watchersMu.Unlock()
	for _, w := range m.logs.watchers {
		close(w.ch)
	}
	m.logs.watchers = nil
}

// logsSnapshotImpl is the internal backing implementation used by the
// mgmt API wrapper in mgmt_api.go.
func (m *Manager) logsSnapshotImpl(processID string, stream uint8) []byte {
	// Read through ensureCollector so the first caller (typically a
	// mgmt client asking for logs of a process that hasn't written
	// anything yet) still sees nil instead of a panic.
	return m.ensureCollector().Snapshot(processID, stream)
}
