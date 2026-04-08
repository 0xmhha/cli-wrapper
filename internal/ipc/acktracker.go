package ipc

import (
	"sort"
	"sync"
)

// AckTracker maintains the set of sequence numbers for which the sender
// is still awaiting receiver acknowledgment. It is safe for concurrent use.
type AckTracker struct {
	mu      sync.Mutex
	pending map[uint64]struct{}
}

// NewAckTracker returns an empty tracker.
func NewAckTracker() *AckTracker {
	return &AckTracker{pending: make(map[uint64]struct{})}
}

// MarkPending records that seq has been sent and is awaiting ACK.
func (a *AckTracker) MarkPending(seq uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending[seq] = struct{}{}
}

// Ack removes seq from the pending set if present.
func (a *AckTracker) Ack(seq uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.pending, seq)
}

// Pending returns a sorted snapshot of every seq still awaiting ACK.
func (a *AckTracker) Pending() []uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]uint64, 0, len(a.pending))
	for s := range a.pending {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// MaxPendingSeq returns the highest seq awaiting ACK, or 0 if none.
func (a *AckTracker) MaxPendingSeq() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	var maxSeq uint64
	for s := range a.pending {
		if s > maxSeq {
			maxSeq = s
		}
	}
	return maxSeq
}
