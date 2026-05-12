// SPDX-License-Identifier: Apache-2.0

package agent

import "sync"

// ringBuffer is a fixed-capacity byte ring buffer used by persistent
// agents to retain recent PTY output for redraw on reattach.
//
// Writes are O(len(p)) under a mutex. Snapshot is O(size). The hot path
// (PTY OnData callback) acquires the mutex once per chunk; under bench's
// output_throughput workload (~65 MB/s through cliwrap), this is well
// below saturation.
type ringBuffer struct {
	mu   sync.Mutex
	data []byte // capacity == size
	head int    // next write position
	full bool   // wraparound has occurred
}

// newRingBuffer allocates a ring buffer of the given size in bytes.
// Panics if size <= 0; callers (Spec.Validate, persistent bootstrap)
// must ensure size > 0.
func newRingBuffer(size int) *ringBuffer {
	if size <= 0 {
		panic("ringbuffer: size must be > 0")
	}
	return &ringBuffer{data: make([]byte, size)}
}

// Write appends p to the buffer. If len(p) > cap, only the tail of p
// is retained (oldest bytes silently dropped).
func (rb *ringBuffer) Write(p []byte) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	capacity := len(rb.data)
	if len(p) >= capacity {
		// p alone fills or exceeds buffer; copy only the last `capacity` bytes.
		copy(rb.data, p[len(p)-capacity:])
		rb.head = 0
		rb.full = true
		return
	}

	first := copy(rb.data[rb.head:], p)
	if first < len(p) {
		// wraparound: write the remainder at the start
		copy(rb.data, p[first:])
		rb.full = true
	}
	rb.head = (rb.head + len(p)) % capacity
	if rb.head == 0 && len(p) > 0 {
		rb.full = true
	}
}

// Snapshot returns a copy of the buffer contents in chronological order
// (oldest byte first). Length is min(bytes-written, cap).
func (rb *ringBuffer) Snapshot() []byte {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if !rb.full {
		out := make([]byte, rb.head)
		copy(out, rb.data[:rb.head])
		return out
	}
	out := make([]byte, len(rb.data))
	// chronological: head .. end of buffer, then start of buffer .. head
	n := copy(out, rb.data[rb.head:])
	copy(out[n:], rb.data[:rb.head])
	return out
}
