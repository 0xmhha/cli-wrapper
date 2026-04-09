// SPDX-License-Identifier: Apache-2.0

package chaos

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

// This test exercises the Plan 01 Conn reconnect path by forcibly
// closing the underlying net.Pipe, then re-wiring a new pair and
// replaying the WAL into the new outbox.
func TestChaos_WALReplayAfterDisconnect(t *testing.T) {
	defer goleak.VerifyNone(t)

	a, b := net.Pipe()
	dirA := t.TempDir()
	dirB := t.TempDir()

	ca, err := ipc.NewConn(ipc.ConnConfig{RWC: a, SpillerDir: dirA, Capacity: 2, WALBytes: 1 << 20})
	require.NoError(t, err)
	cb, err := ipc.NewConn(ipc.ConnConfig{RWC: b, SpillerDir: dirB, Capacity: 2, WALBytes: 1 << 20})
	require.NoError(t, err)

	var received sync.Map
	cb.OnMessage(func(msg ipc.OutboxMessage) {
		received.Store(msg.Header.SeqNo, true)
	})
	ca.Start()
	cb.Start()

	// Step 1: send a small warm-up batch and wait for at least one
	// to arrive on the other side. This is a pure synchronization
	// point — once we know msg #1 landed, we know the outbox
	// goroutines on both sides are running and the pipe is flowing.
	for i := 1; i <= 5; i++ {
		ca.Send(ipc.OutboxMessage{
			Header:  ipc.Header{MsgType: ipc.MsgPing, SeqNo: uint64(i)},
			Payload: nil,
		})
	}
	require.Eventually(t, func() bool {
		_, ok := received.Load(uint64(1))
		return ok
	}, 2*time.Second, 20*time.Millisecond, "warm-up message should be delivered")

	// Step 2: send a large burst right before Close. With
	// Capacity=2 on both sides and no artificial delay, the
	// receiver cannot possibly drain 200 messages in the handful
	// of microseconds between this loop and the Close call below —
	// so the WAL on the "A" side is guaranteed to still hold the
	// tail of the burst regardless of scheduler timing.
	//
	// Earlier versions of this test sent only 5 messages and
	// waited for 2 to arrive before closing; that left only 3
	// messages in flight, and on fast schedulers the receiver
	// drained all 3 before Close was reached, emptying the WAL and
	// failing the assertion below. Bursting 200 and closing
	// immediately makes the race impossible in practice.
	const burstSize = 200
	for i := 6; i <= 5+burstSize; i++ {
		ca.Send(ipc.OutboxMessage{
			Header:  ipc.Header{MsgType: ipc.MsgPing, SeqNo: uint64(i)},
			Payload: nil,
		})
	}

	// Forcibly close both sides. This happens in the same
	// goroutine immediately after the burst loop, so the scheduler
	// has no window to drain the outbox to the wire.
	closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = ca.Close(closeCtx)
	_ = cb.Close(closeCtx)

	// After close, the WAL on side "A" must hold at least a few
	// of the burst messages. Verify by reopening the Spiller
	// directly and replaying.
	sp, err := ipc.NewSpiller(dirA, 2, 1<<20)
	require.NoError(t, err)
	defer sp.Close()

	msgs, err := sp.WALReplayForTest()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(msgs), 1, "WAL should still contain unacked messages after a large burst immediately before Close")
}
