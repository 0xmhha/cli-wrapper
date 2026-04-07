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

// RingBufferSink stores data per (process, stream) in RingBuffers.
type RingBufferSink struct {
	capacity int
	buffers  map[string]*RingBuffer // key = processID + "/" + stream
}

// NewRingBufferSink returns a sink whose buffers have the given byte capacity.
func NewRingBufferSink(capacity int) *RingBufferSink {
	return &RingBufferSink{capacity: capacity, buffers: make(map[string]*RingBuffer)}
}

// Write routes e to the appropriate ring buffer.
func (s *RingBufferSink) Write(e LogEntry) error {
	key := bufferKey(e.ProcessID, e.Stream)
	buf, ok := s.buffers[key]
	if !ok {
		buf = NewRingBuffer(s.capacity)
		s.buffers[key] = buf
	}
	_, err := buf.Write(e.Data)
	return err
}

// Snapshot returns the current contents for (processID, stream) or nil.
func (s *RingBufferSink) Snapshot(processID string, stream uint8) []byte {
	buf, ok := s.buffers[bufferKey(processID, stream)]
	if !ok {
		return nil
	}
	return buf.Snapshot()
}

func (s *RingBufferSink) Flush() error { return nil }
func (s *RingBufferSink) Close() error { return nil }

func bufferKey(processID string, stream uint8) string {
	return processID + "/" + string(rune('0'+stream))
}
