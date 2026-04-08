package chaos

import (
	"context"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/cli-wrapper/cli-wrapper/internal/ipc"
)

// TestChaos_MemoryBoundedUnderSustainedTraffic asserts that the IPC layer
// holds a bounded amount of memory even under a long burst of small frames.
//
// Plan 05 listed this file as the "Memory bounded stress test". The bounds
// being verified live in the IPC layer (small Capacity, small WAL cap), so
// pushing many frames through a tightly-bounded Conn pair should NOT grow
// the heap unboundedly. The threshold below is intentionally lenient — the
// goal is to detect runaway leaks, not to micro-optimize allocations.
func TestChaos_MemoryBoundedUnderSustainedTraffic(t *testing.T) {
	defer goleak.VerifyNone(t)

	const (
		messages       = 10_000
		capacity       = 64
		walBytes int64 = 1 << 20 // 1 MiB
		// 5 MiB ceiling on heap growth — generous, so it only flags blowups,
		// not normal GC churn.
		maxHeapGrowth uint64 = 5 * 1024 * 1024
	)

	a, b := net.Pipe()

	ca, err := ipc.NewConn(ipc.ConnConfig{
		RWC:        a,
		SpillerDir: t.TempDir(),
		Capacity:   capacity,
		WALBytes:   walBytes,
	})
	require.NoError(t, err)

	cb, err := ipc.NewConn(ipc.ConnConfig{
		RWC:        b,
		SpillerDir: t.TempDir(),
		Capacity:   capacity,
		WALBytes:   walBytes,
	})
	require.NoError(t, err)

	// Receiver-side handler that drops every message after counting it.
	var received atomic.Int64
	cb.OnMessage(func(_ ipc.OutboxMessage) {
		received.Add(1)
	})

	ca.Start()
	cb.Start()

	// Baseline measurement BEFORE the burst.
	runtime.GC()
	runtime.GC() // run twice to ensure stable mark/sweep
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)

	// Burst: enqueue many tiny ping frames as fast as the producer can manage.
	// Conn.Send won't block (the spiller absorbs overflow into the bounded WAL),
	// but the receiver still has to drain everything for the test to be valid.
	for i := 1; i <= messages; i++ {
		ok := ca.Send(ipc.OutboxMessage{
			Header: ipc.Header{
				MsgType: ipc.MsgPing,
				SeqNo:   uint64(i),
			},
		})
		require.True(t, ok, "Send returned false at i=%d", i)
	}

	// Wait until the receiver has drained at least the bulk of the burst. We
	// don't insist on every single message because the WAL replay path may
	// drop a few when the WAL byte cap is reached — that itself is part of
	// the bounded-memory contract. Insist on at least 50% throughput.
	require.Eventually(t, func() bool {
		return received.Load() >= int64(messages/2)
	}, 15*time.Second, 50*time.Millisecond,
		"receiver only drained %d/%d messages", received.Load(), messages)

	// Force a GC and remeasure. We compare HeapAlloc deltas; this avoids
	// false positives from RSS-style metrics that include freed-but-unreturned
	// memory.
	runtime.GC()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	var heapGrowth uint64
	if after.HeapAlloc > baseline.HeapAlloc {
		heapGrowth = after.HeapAlloc - baseline.HeapAlloc
	}
	require.Less(t, heapGrowth, maxHeapGrowth,
		"heap grew by %d bytes (baseline=%d after=%d) — memory not bounded",
		heapGrowth, baseline.HeapAlloc, after.HeapAlloc)

	// Clean shutdown.
	closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, ca.Close(closeCtx))
	require.NoError(t, cb.Close(closeCtx))
}
