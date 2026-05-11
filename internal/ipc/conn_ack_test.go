// SPDX-License-Identifier: Apache-2.0

//go:build !race_only_skip

package ipc

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ackTestPair builds two Conns wired over a net.Pipe so one side's writes
// land in the other's reader. The "host" caller drives SendAndAwaitAck;
// the "agent" stand-in echoes MsgAckData for FlagAckRequired frames so
// the host's waiter resolves.
func ackTestPair(t *testing.T) (host, agent *Conn) {
	t.Helper()
	a, b := net.Pipe()
	host, err := NewConn(ConnConfig{
		RWC: a, SpillerDir: t.TempDir(), Capacity: 16, WALBytes: 1 << 20,
	})
	require.NoError(t, err)
	agent, err = NewConn(ConnConfig{
		RWC: b, SpillerDir: t.TempDir(), Capacity: 16, WALBytes: 1 << 20,
	})
	require.NoError(t, err)
	return host, agent
}

func TestConn_SendAndAwaitAck_ReturnsOnMatchingSeq(t *testing.T) {
	host, agent := ackTestPair(t)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = host.Close(ctx)
		_ = agent.Close(ctx)
	}()

	// Agent: on every inbound frame, emit MsgAckData with the inbound SeqNo.
	agent.OnMessage(func(msg OutboxMessage) {
		if msg.Header.Flags&FlagAckRequired != 0 {
			payload, _ := EncodePayload(AckPayload{AckedSeq: msg.Header.SeqNo})
			agent.SendWithNewSeq(MsgAckData, 0, payload)
		}
	})
	host.Start()
	agent.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := host.SendAndAwaitAck(ctx, MsgStopChild, 0, nil)
	require.NoError(t, err)
}

func TestConn_SendAndAwaitAck_TimesOutWithCtx(t *testing.T) {
	host, agent := ackTestPair(t)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = host.Close(ctx)
		_ = agent.Close(ctx)
	}()

	// Agent silently drops everything — no ack will arrive.
	agent.OnMessage(func(OutboxMessage) {})
	host.Start()
	agent.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := host.SendAndAwaitAck(ctx, MsgStopChild, 0, nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// pendingAcks must be cleaned up — leaving entries leaks slots.
	host.pendingMu.Lock()
	pending := len(host.pendingAcks)
	host.pendingMu.Unlock()
	require.Equal(t, 0, pending, "pendingAcks not cleaned up on ctx timeout")
}

func TestConn_SendAndAwaitAck_StrayAckIgnored(t *testing.T) {
	host, agent := ackTestPair(t)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = host.Close(ctx)
		_ = agent.Close(ctx)
	}()

	// Track whether the host's user handler observes the ack frame. It
	// must NOT — the readLoop filters MsgAckData before invoking the
	// user handler.
	var handlerCalled atomic.Int32
	host.OnMessage(func(OutboxMessage) {
		handlerCalled.Add(1)
	})
	host.Start()
	agent.Start()

	// Agent sends an unsolicited ack with a SeqNo that the host never
	// registered. Should be dropped silently.
	payload, _ := EncodePayload(AckPayload{AckedSeq: 999999})
	agent.SendWithNewSeq(MsgAckData, 0, payload)

	// Give the host a moment to process. Then assert nothing leaked
	// through to the user handler.
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(0), handlerCalled.Load(),
		"MsgAckData must not reach the user handler")
}
