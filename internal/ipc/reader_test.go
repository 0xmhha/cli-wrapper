package ipc

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func encodeFrame(t *testing.T, h Header, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := NewFrameWriter(&buf)
	_, err := w.WriteFrame(h, payload)
	require.NoError(t, err)
	return buf.Bytes()
}

func TestFrameReader_ReadFrame(t *testing.T) {
	payload := []byte("hello world")
	data := encodeFrame(t, Header{
		MsgType: MsgPing,
		SeqNo:   7,
		Length:  uint32(len(payload)),
	}, payload)

	r := NewFrameReader(bytes.NewReader(data), MaxPayloadSize)
	h, body, err := r.ReadFrame()
	require.NoError(t, err)
	require.Equal(t, MsgPing, h.MsgType)
	require.Equal(t, uint64(7), h.SeqNo)
	require.Equal(t, payload, body)

	// EOF on subsequent read
	_, _, err = r.ReadFrame()
	require.ErrorIs(t, err, io.EOF)
}

func TestFrameReader_CorruptMagic(t *testing.T) {
	data := encodeFrame(t, Header{MsgType: MsgPing, Length: 0}, nil)
	data[0] = 0x00

	r := NewFrameReader(bytes.NewReader(data), MaxPayloadSize)
	_, _, err := r.ReadFrame()
	require.ErrorIs(t, err, ErrInvalidMagic)
}

func TestFrameReader_TruncatedHeader(t *testing.T) {
	data := encodeFrame(t, Header{MsgType: MsgPing, Length: 0}, nil)
	r := NewFrameReader(bytes.NewReader(data[:5]), MaxPayloadSize)
	_, _, err := r.ReadFrame()
	require.True(t, errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF))
}

func TestFrameReader_TruncatedPayload(t *testing.T) {
	payload := []byte("hello world")
	data := encodeFrame(t, Header{MsgType: MsgPing, Length: uint32(len(payload))}, payload)

	r := NewFrameReader(bytes.NewReader(data[:HeaderSize+3]), MaxPayloadSize)
	_, _, err := r.ReadFrame()
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestFrameReader_EnforcesMaxPayloadSize(t *testing.T) {
	// Craft a header with a length larger than the reader's cap.
	h := Header{MsgType: MsgPing, Length: 1024}
	var hdr [HeaderSize]byte
	h.Encode(hdr[:])

	r := NewFrameReader(bytes.NewReader(hdr[:]), 512)
	_, _, err := r.ReadFrame()
	require.ErrorIs(t, err, ErrFrameTooLarge)
}
