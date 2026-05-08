// SPDX-License-Identifier: Apache-2.0

package logcollect

import (
	"fmt"
	"sync"
)

// FileSinkOptions configures a FileSink.
type FileSinkOptions struct {
	// Dir is the directory under which per-(process, stream) log files
	// are created. The directory is created with mode 0700 lazily.
	Dir string
	// MaxSize and MaxFiles are forwarded to each backing FileRotator.
	// Zero values fall back to FileRotator defaults (64 MiB, 7 files).
	MaxSize  int64
	MaxFiles int
}

// FileSink persists LogEntries to disk via per-(process, stream) FileRotators.
// Each rotator's basename is "<processID>.<stream>" so log files are easy to
// inspect with the standard tail/grep toolchain. FileSink is safe for
// concurrent use (the protecting mutex serializes Write across all rotators).
//
// FileSink implements Sink and is intended to be passed to
// CollectorOptions.ExtraSinks alongside the in-memory RingBufferSink so
// the same chunk reaches both the live snapshot API and durable storage.
type FileSink struct {
	opts FileSinkOptions

	mu       sync.Mutex
	rotators map[string]*FileRotator // key = processID + "/" + stream
}

// NewFileSink returns a sink that lazily opens per-stream rotators in opts.Dir.
func NewFileSink(opts FileSinkOptions) *FileSink {
	return &FileSink{opts: opts, rotators: make(map[string]*FileRotator)}
}

// Write appends e.Data to the rotator for (e.ProcessID, e.Stream).
// Empty data is a no-op. Disk-full / permission errors propagate.
func (s *FileSink) Write(e LogEntry) error {
	if len(e.Data) == 0 {
		return nil
	}
	key := bufferKey(e.ProcessID, e.Stream)

	s.mu.Lock()
	r, ok := s.rotators[key]
	if !ok {
		nr, err := NewFileRotator(RotatorOptions{
			Dir:      s.opts.Dir,
			BaseName: fmt.Sprintf("%s.%d", e.ProcessID, e.Stream),
			MaxSize:  s.opts.MaxSize,
			MaxFiles: s.opts.MaxFiles,
		})
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("filesink: rotator for %s: %w", key, err)
		}
		s.rotators[key] = nr
		r = nr
	}
	s.mu.Unlock()

	_, err := r.Write(e.Data)
	return err
}

// Flush is a no-op: FileRotator writes directly to the OS via the open file
// descriptor, so each Write returning success means the byte is in the
// kernel page cache. (We deliberately don't fsync per chunk — that would
// dominate the IPC budget under a chatty child.)
func (s *FileSink) Flush() error { return nil }

// Close closes every rotator. Subsequent Writes return an error from the
// closed rotator. Idempotent: subsequent Closes return nil from each
// already-closed rotator.
func (s *FileSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var first error
	for _, r := range s.rotators {
		if err := r.Close(); err != nil && first == nil {
			first = err
		}
	}
	s.rotators = nil
	return first
}
