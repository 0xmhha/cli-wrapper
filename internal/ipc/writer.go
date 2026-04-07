package ipc

import (
	"errors"
	"io"
)

// ErrLengthMismatch indicates that the header's Length field does not match
// the size of the payload slice passed to WriteFrame.
var ErrLengthMismatch = errors.New("ipc: header length does not match payload size")

// FrameWriter serializes frames to an io.Writer.
// It is NOT safe for concurrent use; callers must serialize WriteFrame calls.
type FrameWriter struct {
	w       io.Writer
	scratch [HeaderSize]byte
}

// NewFrameWriter returns a FrameWriter that writes to w.
func NewFrameWriter(w io.Writer) *FrameWriter {
	return &FrameWriter{w: w}
}

// WriteFrame serializes h followed by payload to the underlying writer.
// It returns the total number of bytes written (header + payload) on success.
func (fw *FrameWriter) WriteFrame(h Header, payload []byte) (int, error) {
	if h.Length > MaxPayloadSize {
		return 0, ErrFrameTooLarge
	}
	if int(h.Length) != len(payload) {
		return 0, ErrLengthMismatch
	}
	h.Encode(fw.scratch[:])
	n, err := fw.w.Write(fw.scratch[:])
	if err != nil {
		return n, err
	}
	if len(payload) == 0 {
		return n, nil
	}
	m, err := fw.w.Write(payload)
	return n + m, err
}
