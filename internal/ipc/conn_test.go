// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestConn_EndToEnd_OverPipe(t *testing.T) {
	defer goleak.VerifyNone(t)

	a, b := net.Pipe()

	dirA := t.TempDir()
	dirB := t.TempDir()

	connA, err := NewConn(ConnConfig{
		RWC:        a,
		SpillerDir: dirA,
		Capacity:   8,
		WALBytes:   1 << 20,
	})
	require.NoError(t, err)

	connB, err := NewConn(ConnConfig{
		RWC:        b,
		SpillerDir: dirB,
		Capacity:   8,
		WALBytes:   1 << 20,
	})
	require.NoError(t, err)

	received := make(chan OutboxMessage, 1)
	connB.OnMessage(func(msg OutboxMessage) {
		received <- msg
	})

	connA.Start()
	connB.Start()

	ping := OutboxMessage{
		Header:  Header{MsgType: MsgPing, SeqNo: 1, Length: 4},
		Payload: []byte("ping"),
	}
	require.True(t, connA.Send(ping))

	select {
	case got := <-received:
		require.Equal(t, ping.Header.MsgType, got.Header.MsgType)
		require.Equal(t, ping.Header.SeqNo, got.Header.SeqNo)
		require.Equal(t, ping.Payload, got.Payload)
	case <-time.After(2 * time.Second):
		t.Fatal("message not received in time")
	}

	// Clean shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, connA.Close(ctx))
	require.NoError(t, connB.Close(ctx))
}

func TestConn_CloseIsIdempotentAndLeakFree(t *testing.T) {
	defer goleak.VerifyNone(t)

	a, b := net.Pipe()
	dirA := t.TempDir()
	dirB := t.TempDir()

	ca, err := NewConn(ConnConfig{RWC: a, SpillerDir: dirA, Capacity: 4, WALBytes: 1 << 20})
	require.NoError(t, err)
	cb, err := NewConn(ConnConfig{RWC: b, SpillerDir: dirB, Capacity: 4, WALBytes: 1 << 20})
	require.NoError(t, err)

	ca.Start()
	cb.Start()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, ca.Close(ctx))
	require.NoError(t, ca.Close(ctx)) // second close is no-op
	require.NoError(t, cb.Close(ctx))
}

func BenchmarkConn_Throughput(b *testing.B) {
	sa, sb := net.Pipe()
	dirA := b.TempDir()
	dirB := b.TempDir()

	ca, _ := NewConn(ConnConfig{RWC: sa, SpillerDir: dirA, Capacity: 1024, WALBytes: 1 << 20})
	cb, _ := NewConn(ConnConfig{RWC: sb, SpillerDir: dirB, Capacity: 1024, WALBytes: 1 << 20})

	done := make(chan struct{}, b.N)
	cb.OnMessage(func(OutboxMessage) { done <- struct{}{} })

	ca.Start()
	cb.Start()

	payload := make([]byte, 256)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ca.Send(OutboxMessage{
			Header:  Header{MsgType: MsgLogChunk, SeqNo: uint64(i + 1), Length: uint32(len(payload))},
			Payload: payload,
		})
		<-done
	}
	b.StopTimer()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = ca.Close(ctx)
	_ = cb.Close(ctx)
}
