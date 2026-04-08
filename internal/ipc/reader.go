// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"io"
)

// FrameReader reads framed messages from an io.Reader.
// It is NOT safe for concurrent use; callers must serialize ReadFrame calls.
type FrameReader struct {
	r       io.Reader
	cap     uint32
	scratch [HeaderSize]byte
}

// NewFrameReader returns a FrameReader that reads from r and rejects frames
// with Length > maxPayload. Callers should pass MaxPayloadSize unless they
// want an even tighter bound.
func NewFrameReader(r io.Reader, maxPayload uint32) *FrameReader {
	if maxPayload == 0 || maxPayload > MaxPayloadSize {
		maxPayload = MaxPayloadSize
	}
	return &FrameReader{r: r, cap: maxPayload}
}

// ReadFrame reads exactly one frame from the underlying reader.
// It returns the decoded header, the payload bytes (may be empty), and an
// error. On io.EOF with no bytes consumed the error is io.EOF. On partial
// consumption the error is io.ErrUnexpectedEOF.
func (fr *FrameReader) ReadFrame() (Header, []byte, error) {
	var h Header
	if _, err := io.ReadFull(fr.r, fr.scratch[:]); err != nil {
		if err == io.ErrUnexpectedEOF {
			return h, nil, io.ErrUnexpectedEOF
		}
		return h, nil, err
	}
	if err := h.Decode(fr.scratch[:]); err != nil {
		return h, nil, err
	}
	if h.Length > fr.cap {
		return h, nil, ErrFrameTooLarge
	}
	if h.Length == 0 {
		return h, nil, nil
	}
	body := make([]byte, h.Length)
	if _, err := io.ReadFull(fr.r, body); err != nil {
		if err == io.EOF {
			return h, nil, io.ErrUnexpectedEOF
		}
		return h, nil, err
	}
	return h, body, nil
}
