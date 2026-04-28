// SPDX-License-Identifier: Apache-2.0

//go:build bench

package bench

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// BenchmarkPTY_KeystrokeLatency spawns /bin/cat in PTY mode and measures the
// round-trip time from writing a marker line to receiving its echo back.
// p50/p95/p99 latencies are reported in microseconds.
func BenchmarkPTY_KeystrokeLatency(b *testing.B) {
	agentBin := buildAgentForBench(b)
	mgr, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(b.TempDir()),
	)
	require.NoError(b, err)

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = mgr.Shutdown(shutdownCtx)
	}()

	spec, err := cliwrap.NewSpec("pty-cat-latency", "/bin/cat").
		WithPTY(&cliwrap.PTYConfig{InitialCols: 80, InitialRows: 24}).
		WithStopTimeout(2 * time.Second).
		Build()
	require.NoError(b, err)

	h, err := mgr.Register(spec)
	require.NoError(b, err)

	startCtx, startCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer startCancel()

	require.NoError(b, h.Start(startCtx))
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.Stop(stopCtx)
		_ = h.Close(stopCtx)
	}()

	require.Eventually(b, func() bool {
		return h.Status().State == cliwrap.StateRunning
	}, 5*time.Second, 20*time.Millisecond, "process did not reach running state")

	ch, unsub := h.SubscribePTYData()
	defer unsub()

	samples := make([]int64, 0, b.N)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		marker := fmt.Sprintf("k%d", i)
		line := []byte(marker + "\n")

		writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		start := time.Now()
		require.NoError(b, h.WriteInput(writeCtx, line))
		writeCancel()

		// Drain until we see the marker echoed back.
		echoDeadline := time.NewTimer(5 * time.Second)
	echo:
		for {
			select {
			case d, ok := <-ch:
				if !ok {
					break echo
				}
				if bytes.Contains(d.Bytes, []byte(marker)) {
					samples = append(samples, time.Since(start).Microseconds())
					break echo
				}
			case <-echoDeadline.C:
				b.Logf("timeout waiting for echo of marker %q on iteration %d", marker, i)
				samples = append(samples, 5_000_000) // 5s sentinel
				break echo
			}
		}
		echoDeadline.Stop()
	}
	b.StopTimer()

	if len(samples) == 0 {
		b.Fatal("no latency samples collected")
	}

	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })

	percentile := func(p float64) float64 {
		idx := int(float64(len(samples)-1) * p)
		return float64(samples[idx])
	}

	p50 := percentile(0.50)
	p95 := percentile(0.95)
	p99 := percentile(0.99)

	b.ReportMetric(p50, "p50_us")
	b.ReportMetric(p95, "p95_us")
	b.ReportMetric(p99, "p99_us")
}
