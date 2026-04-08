// SPDX-License-Identifier: Apache-2.0

package logcollect

import "sync"

// RingBuffer is a byte-oriented circular buffer with FIFO eviction.
// All methods are safe for concurrent use.
type RingBuffer struct {
	mu       sync.RWMutex
	data     []byte
	capacity int
	head     int // next write position
	size     int // bytes currently stored
}

// NewRingBuffer returns a ring buffer with the given byte capacity.
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity < 1 {
		capacity = 1
	}
	return &RingBuffer{data: make([]byte, capacity), capacity: capacity}
}

// Write appends p to the buffer, evicting old bytes if necessary.
// It always returns len(p), nil to satisfy io.Writer.
func (r *RingBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := len(p)
	if n == 0 {
		return 0, nil
	}

	// If p is larger than capacity, only keep the suffix.
	if n >= r.capacity {
		copy(r.data, p[n-r.capacity:])
		r.head = 0
		r.size = r.capacity
		return n, nil
	}

	// Copy in, wrapping at capacity.
	for i := 0; i < n; i++ {
		r.data[r.head] = p[i]
		r.head = (r.head + 1) % r.capacity
	}
	r.size += n
	if r.size > r.capacity {
		r.size = r.capacity
	}
	return n, nil
}

// Snapshot returns a copy of the currently buffered bytes in FIFO order.
func (r *RingBuffer) Snapshot() []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]byte, r.size)
	if r.size < r.capacity {
		// Not yet wrapped: data is in [0, size).
		copy(out, r.data[:r.size])
		return out
	}
	// Wrapped: oldest byte is at head.
	start := r.head
	copy(out, r.data[start:])
	copy(out[r.capacity-start:], r.data[:start])
	return out
}

// Size returns the number of bytes currently buffered.
func (r *RingBuffer) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.size
}
