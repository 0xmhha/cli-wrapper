// SPDX-License-Identifier: Apache-2.0

package logcollect

import "time"

// LogEntry is one logical chunk of log data delivered to a Sink.
type LogEntry struct {
	ProcessID string
	Stream    uint8 // 0=stdout, 1=stderr, 2=diag
	SeqNo     uint64
	Timestamp time.Time
	Data      []byte
}

// Sink is a consumer of LogEntry values.
type Sink interface {
	Write(entry LogEntry) error
	Flush() error
	Close() error
}

// chunkRecord stores one Write's bytes alongside the timestamp at which the
// chunk arrived. Required so SnapshotFiltered can filter by --since.
type chunkRecord struct {
	Bytes []byte
	Ts    time.Time
}

// chunkBuffer is a per-(process, stream) FIFO of chunkRecords with byte-budget
// eviction. Eviction is at chunk granularity: when totalBytes exceeds capacity,
// the oldest chunks are dropped until the buffer fits or only one chunk remains.
// Retaining the newest chunk preserves the invariant that at least the most
// recent message is always observable, even if it alone exceeds capacity.
type chunkBuffer struct {
	capacity   int
	chunks     []chunkRecord
	totalBytes int
}

func newChunkBuffer(capacity int) *chunkBuffer {
	if capacity < 1 {
		capacity = 1
	}
	return &chunkBuffer{capacity: capacity}
}

// append records the chunk and evicts oldest entries while totalBytes exceeds
// capacity (always keeps at least one chunk).
func (b *chunkBuffer) append(data []byte, ts time.Time) {
	// Defensive copy: the caller's []byte may be reused by io.Copy or
	// equivalent code paths; storing the original would let later Writes
	// corrupt earlier chunk content.
	c := make([]byte, len(data))
	copy(c, data)
	b.chunks = append(b.chunks, chunkRecord{Bytes: c, Ts: ts})
	b.totalBytes += len(c)
	for b.totalBytes > b.capacity && len(b.chunks) > 1 {
		b.totalBytes -= len(b.chunks[0].Bytes)
		b.chunks = b.chunks[1:]
	}
}

// snapshot returns a flat copy of all currently buffered bytes in FIFO order.
func (b *chunkBuffer) snapshot() []byte {
	if len(b.chunks) == 0 {
		return nil
	}
	out := make([]byte, 0, b.totalBytes)
	for _, c := range b.chunks {
		out = append(out, c.Bytes...)
	}
	return out
}

// snapshotFiltered returns the bytes from chunks with Ts >= since (zero time
// disables the filter), and if lines > 0 returns only the trailing N
// newline-delimited lines.
func (b *chunkBuffer) snapshotFiltered(since time.Time, lines int) []byte {
	first := 0
	if !since.IsZero() {
		for first < len(b.chunks) && b.chunks[first].Ts.Before(since) {
			first++
		}
	}
	if first >= len(b.chunks) {
		if lines > 0 {
			return nil
		}
		return nil
	}
	var out []byte
	for _, c := range b.chunks[first:] {
		out = append(out, c.Bytes...)
	}
	if lines > 0 {
		out = lastNLines(out, lines)
	}
	return out
}

// lastNLines returns the last n newline-delimited lines from buf. A "line"
// is bytes through and including its terminating '\n'; if the buffer ends
// without a trailing newline the final partial line still counts as one
// line. If buf has fewer than n lines the entire buffer is returned.
func lastNLines(buf []byte, n int) []byte {
	if n <= 0 || len(buf) == 0 {
		return buf
	}
	// Index every '\n' so we can compute (a) the total line count and
	// (b) the cut point for "last N lines" with one pass over buf.
	var nlPositions []int
	for i, b := range buf {
		if b == '\n' {
			nlPositions = append(nlPositions, i)
		}
	}
	endsWithNL := buf[len(buf)-1] == '\n'
	lineCount := len(nlPositions)
	if !endsWithNL {
		lineCount++ // trailing partial line
	}
	if n >= lineCount {
		return buf
	}
	// Last N lines start just after the (lineCount-n)-th newline.
	skip := lineCount - n
	cut := nlPositions[skip-1] + 1
	return buf[cut:]
}

// RingBufferSink stores data per (process, stream) in chunkBuffers. Each chunk
// retains its arrival timestamp so SnapshotFiltered can serve --since.
type RingBufferSink struct {
	capacity int
	buffers  map[string]*chunkBuffer // key = processID + "/" + stream
}

// NewRingBufferSink returns a sink whose buffers have the given byte capacity.
func NewRingBufferSink(capacity int) *RingBufferSink {
	return &RingBufferSink{capacity: capacity, buffers: make(map[string]*chunkBuffer)}
}

// Write routes e to the appropriate buffer.
func (s *RingBufferSink) Write(e LogEntry) error {
	key := bufferKey(e.ProcessID, e.Stream)
	buf, ok := s.buffers[key]
	if !ok {
		buf = newChunkBuffer(s.capacity)
		s.buffers[key] = buf
	}
	ts := e.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	buf.append(e.Data, ts)
	return nil
}

// Snapshot returns the current contents for (processID, stream) or nil.
func (s *RingBufferSink) Snapshot(processID string, stream uint8) []byte {
	buf, ok := s.buffers[bufferKey(processID, stream)]
	if !ok {
		return nil
	}
	return buf.snapshot()
}

// SnapshotFiltered returns bytes from chunks with Ts >= since (zero = no
// filter); if lines > 0, returns only the trailing N newline-delimited
// lines. Returns nil if no buffer exists or no chunks pass the filter.
func (s *RingBufferSink) SnapshotFiltered(processID string, stream uint8, since time.Time, lines int) []byte {
	buf, ok := s.buffers[bufferKey(processID, stream)]
	if !ok {
		return nil
	}
	return buf.snapshotFiltered(since, lines)
}

func (s *RingBufferSink) Flush() error { return nil }
func (s *RingBufferSink) Close() error { return nil }

func bufferKey(processID string, stream uint8) string {
	return processID + "/" + string(rune('0'+stream))
}
