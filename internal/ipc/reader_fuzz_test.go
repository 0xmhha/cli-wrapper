// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"bytes"
	"testing"
)

func FuzzFrameReader(f *testing.F) {
	// Seed corpus: one valid frame with small payload.
	valid := encodeFrameForFuzz(Header{MsgType: MsgPing, Length: 4}, []byte("data"))
	f.Add(valid)
	// Seed corpus: just a header with no body.
	empty := encodeFrameForFuzz(Header{MsgType: MsgShutdown, Length: 0}, nil)
	f.Add(empty)

	f.Fuzz(func(t *testing.T, data []byte) {
		r := NewFrameReader(bytes.NewReader(data), MaxPayloadSize)
		for i := 0; i < 8; i++ {
			_, _, err := r.ReadFrame()
			if err != nil {
				return
			}
		}
	})
}

func encodeFrameForFuzz(h Header, payload []byte) []byte {
	var buf bytes.Buffer
	w := NewFrameWriter(&buf)
	_, _ = w.WriteFrame(h, payload)
	return buf.Bytes()
}
