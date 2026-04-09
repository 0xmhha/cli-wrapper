// internal/agent/logchunk_writer.go
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"sync/atomic"

	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

// chunkSender is the minimal interface chunkWriter needs from a Dispatcher.
// Using an interface keeps the writer testable without spinning up a real
// IPC Conn.
type chunkSender interface {
	SendControl(t ipc.MsgType, payload any, ack bool) error
}

// chunkWriter is an io.Writer that wraps each Write call into one
// ipc.MsgLogChunk frame carrying the bytes as the payload Data.
//
// chunkWriter is safe for use from a single goroutine (the io.Copy worker
// inside Runner.Run). Per-write SeqNo is monotonic; the stream id is fixed
// for the lifetime of the writer.
type chunkWriter struct {
	sender chunkSender
	stream uint8
	seq    atomic.Uint64
}

// newChunkWriter returns a chunkWriter that emits MsgLogChunk frames
// tagged with the given stream id (0=stdout, 1=stderr, 2=diag).
func newChunkWriter(sender chunkSender, stream uint8) *chunkWriter {
	return &chunkWriter{sender: sender, stream: stream}
}

// Write implements io.Writer. Empty writes are a no-op and return (0, nil).
// A non-empty write always produces exactly one MsgLogChunk frame.
func (w *chunkWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	// Defensive copy: io.Copy reuses its internal buffer across calls, so
	// retaining the original slice would cause later writes to corrupt the
	// payload of earlier MsgLogChunk frames while they sit in the outbox.
	buf := make([]byte, len(p))
	copy(buf, p)

	next := w.seq.Add(1)
	payload := ipc.LogChunkPayload{
		Stream: w.stream,
		SeqNo:  next,
		Data:   buf,
	}
	if err := w.sender.SendControl(ipc.MsgLogChunk, payload, false); err != nil {
		return 0, err
	}
	return len(p), nil
}
