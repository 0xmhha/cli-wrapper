package logcollect

import "sync"

// CollectorOptions configures the LogCollector.
type CollectorOptions struct {
	RingBufferBytes int
	ExtraSinks      []Sink
}

// Collector is the top-level log ingestion entry point. It routes writes
// to an embedded RingBufferSink plus any extra sinks the caller provides.
type Collector struct {
	mu    sync.RWMutex
	ring  *RingBufferSink
	extra []Sink
}

// NewCollector returns a Collector with the given capacity per stream.
func NewCollector(opts CollectorOptions) *Collector {
	ringCap := opts.RingBufferBytes
	if ringCap <= 0 {
		ringCap = 4 * 1024 * 1024
	}
	return &Collector{
		ring:  NewRingBufferSink(ringCap),
		extra: append([]Sink(nil), opts.ExtraSinks...),
	}
}

// Write ingests a log entry.
func (c *Collector) Write(e LogEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.ring.Write(e)
	for _, s := range c.extra {
		_ = s.Write(e)
	}
}

// Snapshot returns the ring buffer for (processID, stream).
func (c *Collector) Snapshot(processID string, stream uint8) []byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ring.Snapshot(processID, stream)
}

// Close releases resources.
func (c *Collector) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.extra {
		_ = s.Close()
	}
	_ = c.ring.Close()
}
