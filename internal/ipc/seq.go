package ipc

import (
	"sync"
	"sync/atomic"
)

// SeqGenerator produces monotonically increasing sequence numbers.
// It is safe for concurrent use.
type SeqGenerator struct {
	n atomic.Uint64
}

// NewSeqGenerator returns a generator whose first Next() returns start.
// If start is 0, it is clamped to 1 to avoid uint64 underflow.
func NewSeqGenerator(start uint64) *SeqGenerator {
	if start == 0 {
		start = 1
	}
	g := &SeqGenerator{}
	g.n.Store(start - 1)
	return g
}

// Next returns the next sequence number.
func (g *SeqGenerator) Next() uint64 {
	return g.n.Add(1)
}

// DedupTracker remembers the highest observed sequence number (watermark).
// A frame is considered duplicate iff seq <= lastSeen. This approach uses
// O(1) memory regardless of connection lifetime, which is safe for
// long-running connections (spec §2.7).
//
// It is safe for concurrent use.
type DedupTracker struct {
	mu       sync.Mutex
	lastSeen uint64
}

// NewDedupTracker returns an empty tracker.
func NewDedupTracker() *DedupTracker {
	return &DedupTracker{}
}

// Seen reports whether seq has already been observed. If seq is new
// (greater than the current watermark), it advances the watermark and
// returns false.
func (d *DedupTracker) Seen(seq uint64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if seq <= d.lastSeen {
		return true
	}
	d.lastSeen = seq
	return false
}
