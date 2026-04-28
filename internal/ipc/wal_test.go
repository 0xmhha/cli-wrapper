// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWAL_AppendAndIterate(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenWAL(dir, 1<<20)
	require.NoError(t, err)
	defer wal.Close()

	msgs := []OutboxMessage{
		{Header: Header{MsgType: MsgPing, SeqNo: 1, Length: 0}, Payload: nil},
		{Header: Header{MsgType: MsgLogChunk, SeqNo: 2, Length: 5}, Payload: []byte("hello")},
		{Header: Header{MsgType: MsgLogChunk, SeqNo: 3, Length: 3}, Payload: []byte("abc")},
	}
	for _, m := range msgs {
		require.NoError(t, wal.Append(m))
	}

	got, err := wal.Replay()
	require.NoError(t, err)
	require.Equal(t, msgs, got)
}

func TestWAL_ReplayAfterReopen(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenWAL(dir, 1<<20)
	require.NoError(t, err)

	msg := OutboxMessage{
		Header:  Header{MsgType: MsgPing, SeqNo: 99, Length: 2},
		Payload: []byte("hi"),
	}
	require.NoError(t, wal.Append(msg))
	require.NoError(t, wal.Close())

	wal2, err := OpenWAL(dir, 1<<20)
	require.NoError(t, err)
	defer wal2.Close()

	got, err := wal2.Replay()
	require.NoError(t, err)
	require.Equal(t, []OutboxMessage{msg}, got)
}

func TestWAL_RetireRemovesEntries(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenWAL(dir, 1<<20)
	require.NoError(t, err)
	defer wal.Close()

	for i := 1; i <= 5; i++ {
		require.NoError(t, wal.Append(OutboxMessage{
			Header: Header{MsgType: MsgPing, SeqNo: uint64(i)},
		}))
	}

	require.NoError(t, wal.Retire(3))

	got, err := wal.Replay()
	require.NoError(t, err)
	seqs := make([]uint64, len(got))
	for i, m := range got {
		seqs[i] = m.Header.SeqNo
	}
	require.Equal(t, []uint64{4, 5}, seqs)
}

func TestWAL_EnforcesSizeCap(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenWAL(dir, 64) // tiny cap
	require.NoError(t, err)
	defer wal.Close()

	require.NoError(t, wal.Append(OutboxMessage{
		Header:  Header{MsgType: MsgPing, SeqNo: 1, Length: 4},
		Payload: []byte("data"),
	}))

	err = wal.Append(OutboxMessage{
		Header:  Header{MsgType: MsgLogChunk, SeqNo: 2, Length: 1024},
		Payload: make([]byte, 1024),
	})
	require.ErrorIs(t, err, ErrWALFull)
}

func TestWAL_ReplaysPTYDataChannelInOrder(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenWAL(dir, 1<<20)
	require.NoError(t, err)
	defer func() { _ = wal.Close() }()

	// Interleave MsgLogChunk + MsgTypePTYData on the same seq space.
	require.NoError(t, wal.Append(OutboxMessage{
		Header:  Header{MsgType: MsgLogChunk, SeqNo: 1, Length: 4},
		Payload: []byte("log1"),
	}))
	require.NoError(t, wal.Append(OutboxMessage{
		Header:  Header{MsgType: MsgTypePTYData, SeqNo: 2, Length: 4},
		Payload: []byte("pty1"),
	}))
	require.NoError(t, wal.Append(OutboxMessage{
		Header:  Header{MsgType: MsgLogChunk, SeqNo: 3, Length: 4},
		Payload: []byte("log2"),
	}))

	got, err := wal.Replay()
	require.NoError(t, err)
	require.Len(t, got, 3)

	var summaries []string
	for _, m := range got {
		summaries = append(summaries, fmt.Sprintf("%d:%d:%s", m.Header.MsgType, m.Header.SeqNo, m.Payload))
	}
	require.Equal(t, []string{
		fmt.Sprintf("%d:1:log1", MsgLogChunk),
		fmt.Sprintf("%d:2:pty1", MsgTypePTYData),
		fmt.Sprintf("%d:3:log2", MsgLogChunk),
	}, summaries)
}

func TestWAL_FileLayoutIsStable(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenWAL(dir, 1<<20)
	require.NoError(t, err)
	defer wal.Close()

	require.NoError(t, wal.Append(OutboxMessage{
		Header:  Header{MsgType: MsgPing, SeqNo: 1, Length: 0},
		Payload: nil,
	}))

	// Exactly one file should exist in the WAL directory.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Contains(t, entries[0].Name(), "wal")

	info, err := os.Stat(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)
	require.Greater(t, info.Size(), int64(0))
}
