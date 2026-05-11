// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSpiller_OverflowAndDrain(t *testing.T) {
	dir := t.TempDir()
	sp, err := NewSpiller(dir, 3, 1<<20)
	require.NoError(t, err)
	defer sp.Close()

	// Enqueue 5 messages; in-memory capacity is 3, so 2 must spill.
	for i := 1; i <= 5; i++ {
		require.True(t, sp.Outbox().Enqueue(OutboxMessage{
			Header: Header{MsgType: MsgPing, SeqNo: uint64(i)},
		}))
	}

	// Drain the in-memory queue.
	drained := make([]uint64, 0, 5)
	for i := 0; i < 3; i++ {
		msg, ok := sp.Outbox().Dequeue(200 * time.Millisecond)
		require.True(t, ok)
		drained = append(drained, msg.Header.SeqNo)
	}
	require.Equal(t, []uint64{1, 2, 3}, drained)

	// Trigger a replay from the WAL.
	require.NoError(t, sp.ReplayInto(sp.Outbox()))

	// The remaining 2 messages must now be available in order.
	for want := uint64(4); want <= 5; want++ {
		msg, ok := sp.Outbox().Dequeue(200 * time.Millisecond)
		require.True(t, ok)
		require.Equal(t, want, msg.Header.SeqNo)
	}
}

func TestSpiller_AckRetiresWALEntries(t *testing.T) {
	dir := t.TempDir()
	// Capacity=4 so that: seq 1..4 go to channel, seq 5..6 spill to WAL.
	// After draining the channel, ReplayInto has room to inject WAL records.
	sp, err := NewSpiller(dir, 4, 1<<20)
	require.NoError(t, err)
	defer sp.Close()

	for i := 1; i <= 6; i++ {
		sp.Outbox().Enqueue(OutboxMessage{Header: Header{MsgType: MsgPing, SeqNo: uint64(i)}})
	}
	// Seqs 1..4 in the channel, seqs 5,6 in the WAL.

	// Ack up to seq 5 — retires seq 5 from the WAL, leaving only seq 6.
	require.NoError(t, sp.Ack(5))

	// Drain the in-memory channel (seqs 1..4).
	for i := 0; i < 4; i++ {
		_, ok := sp.Outbox().Dequeue(50 * time.Millisecond)
		require.True(t, ok)
	}

	// Replay; only seq 6 should come back from disk.
	require.NoError(t, sp.ReplayInto(sp.Outbox()))

	// Drain everything.
	var seqs []uint64
	for {
		msg, ok := sp.Outbox().Dequeue(50 * time.Millisecond)
		if !ok {
			break
		}
		seqs = append(seqs, msg.Header.SeqNo)
	}
	require.Contains(t, seqs, uint64(6))
	require.NotContains(t, seqs, uint64(5), "seq 5 must have been retired")
}

func TestInMemorySpiller_OverflowDrops(t *testing.T) {
	sp := NewInMemorySpiller(2)
	defer sp.Close()

	// Capacity is 2: first two enqueues succeed.
	require.True(t, sp.Outbox().Enqueue(OutboxMessage{Header: Header{MsgType: MsgPing, SeqNo: 1}}))
	require.True(t, sp.Outbox().Enqueue(OutboxMessage{Header: Header{MsgType: MsgPing, SeqNo: 2}}))

	// Third enqueue must drop because there is no spill function. Without
	// the disk-backed WAL, overflow returns false rather than fsync-ing.
	require.False(t, sp.Outbox().Enqueue(OutboxMessage{Header: Header{MsgType: MsgPing, SeqNo: 3}}),
		"in-memory spiller should drop on overflow, not block or persist")

	// Ack and ReplayInto must be no-ops, never panicking on the nil WAL.
	require.NoError(t, sp.Ack(2))
	require.NoError(t, sp.ReplayInto(sp.Outbox()))
}

// TestInMemorySpiller_SpillDirectCallNoPanic guards the Spiller.spill method
// against nil-WAL deref when called directly by Conn.writeLoop on a
// WriteFrame failure. The Outbox itself short-circuits spill in no-WAL
// mode (NewOutbox is given a nil SpillFunc), but writeLoop holds a direct
// reference to sp.spill and calls it as a last-ditch attempt to retain an
// in-flight frame after the socket dies. Before this guard, that direct
// call dereferenced a nil *WAL and crashed the process — observed when
// the host opted into WithoutWAL and the agent was SIGKILL'd mid-attach.
func TestInMemorySpiller_SpillDirectCallNoPanic(t *testing.T) {
	sp := NewInMemorySpiller(2)
	defer sp.Close()

	msg := OutboxMessage{Header: Header{MsgType: MsgPing, SeqNo: 99}}

	// Calling sp.spill directly mirrors what Conn.writeLoop does on a
	// WriteFrame failure. Must not panic; documented contract is to drop.
	require.NotPanics(t, func() {
		require.NoError(t, sp.spill(msg))
	})
}
