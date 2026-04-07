package logcollect

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRingBuffer_WriteAndSnapshot(t *testing.T) {
	rb := NewRingBuffer(16)
	rb.Write([]byte("hello "))
	rb.Write([]byte("world"))

	snap := rb.Snapshot()
	require.Equal(t, []byte("hello world"), snap)
}

func TestRingBuffer_OverflowsOldestFirst(t *testing.T) {
	rb := NewRingBuffer(8)
	rb.Write([]byte("AAAA"))
	rb.Write([]byte("BBBB"))
	rb.Write([]byte("CCCC"))

	// After 12 bytes in an 8-byte buffer, we keep only the last 8.
	snap := rb.Snapshot()
	require.Equal(t, []byte("BBBBCCCC"), snap)
}

func TestRingBuffer_WriteLargerThanCapacityKeepsSuffix(t *testing.T) {
	rb := NewRingBuffer(4)
	rb.Write([]byte("abcdefgh"))
	require.Equal(t, []byte("efgh"), rb.Snapshot())
}

func TestRingBuffer_Size(t *testing.T) {
	rb := NewRingBuffer(8)
	require.Equal(t, 0, rb.Size())
	rb.Write([]byte("abc"))
	require.Equal(t, 3, rb.Size())
	rb.Write([]byte("defghijk"))
	require.Equal(t, 8, rb.Size())
}
