// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFrameWriter_WriteFrame(t *testing.T) {
	var buf bytes.Buffer
	w := NewFrameWriter(&buf)

	payload := []byte("hello world")
	n, err := w.WriteFrame(Header{
		MsgType: MsgPing,
		Flags:   0,
		SeqNo:   42,
		Length:  uint32(len(payload)),
	}, payload)
	require.NoError(t, err)
	require.Equal(t, HeaderSize+len(payload), n)

	raw := buf.Bytes()
	require.Len(t, raw, HeaderSize+len(payload))

	var h Header
	require.NoError(t, h.Decode(raw[:HeaderSize]))
	require.Equal(t, MsgPing, h.MsgType)
	require.Equal(t, uint64(42), h.SeqNo)
	require.Equal(t, uint32(len(payload)), h.Length)
	require.Equal(t, payload, raw[HeaderSize:])
}

func TestFrameWriter_RejectsOversizedPayload(t *testing.T) {
	var buf bytes.Buffer
	w := NewFrameWriter(&buf)

	hugeLen := MaxPayloadSize + 1
	_, err := w.WriteFrame(Header{MsgType: MsgPing, Length: hugeLen}, make([]byte, hugeLen))
	require.ErrorIs(t, err, ErrFrameTooLarge)
	require.Equal(t, 0, buf.Len(), "nothing should have been written")
}

func TestFrameWriter_LengthMustMatchPayload(t *testing.T) {
	var buf bytes.Buffer
	w := NewFrameWriter(&buf)

	_, err := w.WriteFrame(Header{MsgType: MsgPing, Length: 10}, []byte("short"))
	require.ErrorIs(t, err, ErrLengthMismatch)
}
