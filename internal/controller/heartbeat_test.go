package controller

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeSender struct {
	count atomic.Int32
}

func (f *fakeSender) SendPing() error {
	f.count.Add(1)
	return nil
}

func TestHeartbeat_PeriodicPing(t *testing.T) {
	fs := &fakeSender{}
	hb := NewHeartbeat(HeartbeatOptions{
		Sender:   fs,
		Interval: 20 * time.Millisecond,
		Timeout:  10 * time.Millisecond,
		OnMiss:   func(int) {},
	})

	ctx, cancel := context.WithCancel(context.Background())
	hb.Start(ctx)

	time.Sleep(80 * time.Millisecond)
	cancel()
	hb.Wait()

	require.GreaterOrEqual(t, int(fs.count.Load()), 3)
}

func TestHeartbeat_TriggersOnMissAfterThreshold(t *testing.T) {
	fs := &fakeSender{}
	misses := make(chan int, 1)
	hb := NewHeartbeat(HeartbeatOptions{
		Sender:        fs,
		Interval:      20 * time.Millisecond,
		Timeout:       10 * time.Millisecond,
		MissThreshold: 3,
		OnMiss:        func(n int) { misses <- n },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hb.Start(ctx)

	// Never call RecordPong - all pings miss.
	select {
	case n := <-misses:
		require.GreaterOrEqual(t, n, 3)
	case <-time.After(1 * time.Second):
		t.Fatal("OnMiss not called")
	}

	cancel()
	hb.Wait()
}

func TestHeartbeat_ResetsOnPong(t *testing.T) {
	fs := &fakeSender{}
	misses := make(chan int, 8)
	hb := NewHeartbeat(HeartbeatOptions{
		Sender:        fs,
		Interval:      20 * time.Millisecond,
		Timeout:       10 * time.Millisecond,
		MissThreshold: 3,
		OnMiss:        func(n int) { misses <- n },
	})

	ctx, cancel := context.WithCancel(context.Background())
	hb.Start(ctx)

	// Simulate pong arrivals frequently.
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(15 * time.Millisecond)
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
	cancel()
	hb.Wait()

	select {
	case n := <-misses:
		t.Fatalf("unexpected miss with n=%d while pongs were arriving", n)
	default:
	}
}
