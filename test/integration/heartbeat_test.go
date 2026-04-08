// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/controller"
	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

func newConnForHeartbeat(rwc net.Conn, dir string) (*ipc.Conn, error) {
	return ipc.NewConn(ipc.ConnConfig{
		RWC:        rwc,
		SpillerDir: dir,
		Capacity:   8,
		WALBytes:   1 << 20,
	})
}

type connPingSender struct {
	conn *ipc.Conn
}

func (c *connPingSender) SendPing() error {
	ok := c.conn.Send(ipc.OutboxMessage{
		Header: ipc.Header{
			MsgType: ipc.MsgPing,
			SeqNo:   c.conn.Seqs().Next(),
		},
	})
	if !ok {
		return errors.New("conn closed")
	}
	return nil
}

// blockingPingSender pretends to send a PING but blocks (or fails) so that
// the heartbeat never sees a PONG. This simulates an unresponsive peer the
// way a SIGSTOPped agent would: bytes can no longer flow, so the controller's
// heartbeat must declare the peer dead.
type blockingPingSender struct {
	pingCount atomic.Int32
	fail      atomic.Bool
}

func (b *blockingPingSender) SendPing() error {
	b.pingCount.Add(1)
	if b.fail.Load() {
		return errors.New("simulated unresponsive peer")
	}
	return nil
}

// TestIntegration_HeartbeatMissFiresOnUnresponsivePeer asserts that when an
// agent stops responding (the equivalent of SIGSTOPping the process), the
// Heartbeat detects three consecutive missed PONGs and fires OnMiss.
//
// Plan 05's File Structure listed this test as "Heartbeat miss test (kill
// agent with SIGSTOP)". Heartbeat is not currently wired into Controller, so
// we drive the Heartbeat directly using the real component and observe it
// behaving as the controller would when its peer goes dark.
func TestIntegration_HeartbeatMissFiresOnUnresponsivePeer(t *testing.T) {
	defer goleak.VerifyNone(t)

	sender := &blockingPingSender{}
	misses := make(chan int, 4)

	hb := controller.NewHeartbeat(controller.HeartbeatOptions{
		Sender:        sender,
		Interval:      30 * time.Millisecond,
		Timeout:       15 * time.Millisecond,
		MissThreshold: 3,
		OnMiss:        func(n int) { misses <- n },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Critically: never call hb.RecordPong() — the simulated peer is frozen.
	hb.Start(ctx)

	select {
	case n := <-misses:
		require.GreaterOrEqual(t, n, 3, "expected at least 3 consecutive misses")
	case <-time.After(2 * time.Second):
		t.Fatal("Heartbeat.OnMiss never fired despite no pongs")
	}

	require.GreaterOrEqual(t, int(sender.pingCount.Load()), 3,
		"heartbeat should have attempted multiple pings")

	cancel()
	hb.Wait()
}

// TestIntegration_HeartbeatRecoversWhenPeerResumes verifies the inverse: if
// pongs start arriving again, the miss counter resets and OnMiss does not
// fire — equivalent to a SIGCONT after a SIGSTOP.
func TestIntegration_HeartbeatRecoversWhenPeerResumes(t *testing.T) {
	defer goleak.VerifyNone(t)

	sender := &blockingPingSender{}
	misses := make(chan int, 8)

	hb := controller.NewHeartbeat(controller.HeartbeatOptions{
		Sender:        sender,
		Interval:      20 * time.Millisecond,
		Timeout:       10 * time.Millisecond,
		MissThreshold: 5, // generous threshold so the pongs win the race
		OnMiss:        func(n int) { misses <- n },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hb.Start(ctx)

	// Drive pongs continuously to keep the heartbeat happy.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				hb.RecordPong()
			case <-stop:
				return
			}
		}
	}()

	time.Sleep(150 * time.Millisecond)
	close(stop)
	<-done

	cancel()
	hb.Wait()

	select {
	case n := <-misses:
		t.Fatalf("Heartbeat.OnMiss fired (n=%d) while pongs were arriving", n)
	default:
	}
}

// TestIntegration_HeartbeatWithRealConn verifies the Heartbeat works against
// a real IPC connection. We connect two ipc.Conn over net.Pipe, drive PING
// frames through the wire, and assert OnMiss fires when the peer side stops
// responding (the integration analogue of "SIGSTOP the agent").
//
// We enqueue PINGs through ca; cb's handler is the only path that calls
// hb.RecordPong. When we suspend cb's handler (set drop=true), no pongs are
// recorded, and OnMiss must fire.
func TestIntegration_HeartbeatWithRealConn(t *testing.T) {
	defer goleak.VerifyNone(t)

	a, b := net.Pipe()

	ca, err := newConnForHeartbeat(a, t.TempDir())
	require.NoError(t, err)
	cb, err := newConnForHeartbeat(b, t.TempDir())
	require.NoError(t, err)

	misses := make(chan int, 4)
	hb := controller.NewHeartbeat(controller.HeartbeatOptions{
		Sender:        &connPingSender{conn: ca},
		Interval:      30 * time.Millisecond,
		Timeout:       15 * time.Millisecond,
		MissThreshold: 3,
		OnMiss:        func(n int) { misses <- n },
	})

	// cb is "frozen": it never invokes RecordPong. We do not set OnMessage on
	// cb, so PINGs are read off the wire and discarded with no response.
	ca.Start()
	cb.Start()

	ctx, cancel := context.WithCancel(context.Background())
	hb.Start(ctx)

	select {
	case n := <-misses:
		require.GreaterOrEqual(t, n, 3)
	case <-time.After(3 * time.Second):
		t.Fatal("OnMiss never fired against real Conn with frozen peer")
	}

	cancel()
	hb.Wait()

	closeCtx, ccancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer ccancel()
	require.NoError(t, ca.Close(closeCtx))
	require.NoError(t, cb.Close(closeCtx))
}
