// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOutbox_EnqueueAndDequeue(t *testing.T) {
	ob := NewOutbox(4, nil)
	defer ob.Close()

	msg := OutboxMessage{
		Header:  Header{MsgType: MsgPing, SeqNo: 1, Length: 4},
		Payload: []byte("ping"),
	}
	require.True(t, ob.Enqueue(msg))

	got, ok := ob.Dequeue(100 * time.Millisecond)
	require.True(t, ok)
	require.Equal(t, msg, got)
}

func TestOutbox_BoundedCapacityTriggersSpill(t *testing.T) {
	var spilled []OutboxMessage
	spill := func(m OutboxMessage) error {
		spilled = append(spilled, m)
		return nil
	}

	ob := NewOutbox(2, spill)
	defer ob.Close()

	for i := 1; i <= 4; i++ {
		ok := ob.Enqueue(OutboxMessage{
			Header:  Header{MsgType: MsgPing, SeqNo: uint64(i)},
			Payload: nil,
		})
		require.True(t, ok, "enqueue #%d must succeed even when in-memory queue is full", i)
	}

	require.Len(t, spilled, 2, "overflow messages must be spilled")
	require.Equal(t, uint64(3), spilled[0].Header.SeqNo)
	require.Equal(t, uint64(4), spilled[1].Header.SeqNo)
}

func TestOutbox_EnqueueAfterClose(t *testing.T) {
	ob := NewOutbox(2, nil)
	ob.Close()

	require.False(t, ob.Enqueue(OutboxMessage{Header: Header{SeqNo: 1}}))
}

func TestOutbox_DequeueReturnsFalseOnClose(t *testing.T) {
	ob := NewOutbox(2, nil)
	go func() {
		time.Sleep(20 * time.Millisecond)
		ob.Close()
	}()
	_, ok := ob.Dequeue(500 * time.Millisecond)
	require.False(t, ok)
}
