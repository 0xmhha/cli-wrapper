// internal/agent/logchunk_writer_test.go
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

type recordingSender struct {
	calls []recordedCall
}

type recordedCall struct {
	msgType ipc.MsgType
	payload ipc.LogChunkPayload
}

func (r *recordingSender) SendControl(t ipc.MsgType, payload any, ack bool) error {
	if p, ok := payload.(ipc.LogChunkPayload); ok {
		r.calls = append(r.calls, recordedCall{msgType: t, payload: p})
	}
	return nil
}

func TestChunkWriter_WriteEmitsLogChunkWithStream(t *testing.T) {
	rec := &recordingSender{}
	w := newChunkWriter(rec, 0) // stream 0 = stdout

	n, err := w.Write([]byte("hello world"))
	require.NoError(t, err)
	require.Equal(t, 11, n)

	require.Len(t, rec.calls, 1)
	require.Equal(t, ipc.MsgLogChunk, rec.calls[0].msgType)
	require.Equal(t, uint8(0), rec.calls[0].payload.Stream)
	require.Equal(t, []byte("hello world"), rec.calls[0].payload.Data)
}

func TestChunkWriter_SequenceNumbersIncrement(t *testing.T) {
	rec := &recordingSender{}
	w := newChunkWriter(rec, 1) // stream 1 = stderr

	_, _ = w.Write([]byte("a"))
	_, _ = w.Write([]byte("b"))
	_, _ = w.Write([]byte("c"))

	require.Len(t, rec.calls, 3)
	require.Equal(t, uint64(1), rec.calls[0].payload.SeqNo)
	require.Equal(t, uint64(2), rec.calls[1].payload.SeqNo)
	require.Equal(t, uint64(3), rec.calls[2].payload.SeqNo)
	for _, c := range rec.calls {
		require.Equal(t, uint8(1), c.payload.Stream)
	}
}

func TestChunkWriter_EmptyWriteIsNoop(t *testing.T) {
	rec := &recordingSender{}
	w := newChunkWriter(rec, 0)

	n, err := w.Write(nil)
	require.NoError(t, err)
	require.Equal(t, 0, n)

	n, err = w.Write([]byte{})
	require.NoError(t, err)
	require.Equal(t, 0, n)

	require.Empty(t, rec.calls, "empty writes must not emit frames")
}
