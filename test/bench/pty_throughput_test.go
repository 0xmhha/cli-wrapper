// SPDX-License-Identifier: Apache-2.0

//go:build bench

package bench

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// BenchmarkPTY_Throughput spawns /usr/bin/yes in PTY mode, drains its output
// for 5 seconds, and reports the sustained throughput in MB/s.
func BenchmarkPTY_Throughput(b *testing.B) {
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

	spec, err := cliwrap.NewSpec("pty-yes", "/usr/bin/yes").
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

	// Drain PTY output for 5 seconds, counting total bytes received.
	const measureDuration = 5 * time.Second
	b.ResetTimer()

	var totalBytes int64
	deadline := time.NewTimer(measureDuration)
	defer deadline.Stop()

drain:
	for {
		select {
		case d, ok := <-ch:
			if !ok {
				break drain
			}
			totalBytes += int64(len(d.Bytes))
		case <-deadline.C:
			break drain
		}
	}

	b.StopTimer()

	mbps := float64(totalBytes) / measureDuration.Seconds() / (1024 * 1024)
	b.ReportMetric(mbps, "MB/s")
}
