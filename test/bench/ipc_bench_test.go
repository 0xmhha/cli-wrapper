package bench

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/cli-wrapper/cli-wrapper/internal/ipc"
)

func BenchmarkIPC_PingPong(b *testing.B) {
	sa, sb := net.Pipe()
	dirA := b.TempDir()
	dirB := b.TempDir()

	ca, _ := ipc.NewConn(ipc.ConnConfig{RWC: sa, SpillerDir: dirA, Capacity: 1024, WALBytes: 1 << 20})
	cb, _ := ipc.NewConn(ipc.ConnConfig{RWC: sb, SpillerDir: dirB, Capacity: 1024, WALBytes: 1 << 20})

	done := make(chan struct{}, b.N)
	cb.OnMessage(func(ipc.OutboxMessage) { done <- struct{}{} })

	ca.Start()
	cb.Start()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ca.Send(ipc.OutboxMessage{Header: ipc.Header{MsgType: ipc.MsgPing, SeqNo: uint64(i + 1)}})
		<-done
	}
	b.StopTimer()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = ca.Close(ctx)
	_ = cb.Close(ctx)
}
