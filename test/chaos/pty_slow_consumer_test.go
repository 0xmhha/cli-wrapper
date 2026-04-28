// SPDX-License-Identifier: Apache-2.0

//go:build chaos

package chaos

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// TestPTY_SlowSubscriberDoesNotBlockAgent spawns /usr/bin/yes (unbounded
// stdout firehose) via PTY, subscribes to PTY data but consumes it at a
// deliberately slow rate (one chunk per 100 ms), and asserts that the agent
// remains responsive for the duration. The slow consumer must not cause the
// agent goroutines to stall or the status call to hang.
func TestPTY_SlowSubscriberDoesNotBlockAgent(t *testing.T) {
	agentBin := supervise.BuildAgentForTest(t)
	mgr, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = mgr.Shutdown(shutdownCtx)
	}()

	spec, err := cliwrap.NewSpec("pty-slow-consumer", "/usr/bin/yes").
		WithPTY(&cliwrap.PTYConfig{InitialCols: 80, InitialRows: 24}).
		WithStopTimeout(2 * time.Second).
		Build()
	require.NoError(t, err)

	h, err := mgr.Register(spec)
	require.NoError(t, err)

	require.NoError(t, h.Start(ctx))
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = h.Stop(stopCtx)
		_ = h.Close(stopCtx)
	}()

	require.Eventually(t, func() bool {
		return h.Status().State == cliwrap.StateRunning
	}, 5*time.Second, 20*time.Millisecond, "process did not reach running state")

	// Subscribe to PTY data and consume one chunk every 100 ms — deliberately
	// slower than /usr/bin/yes can produce. This saturates the subscriber
	// channel; a blocked fan-out path would stall the agent's IPC goroutine.
	ch, unsub := h.SubscribePTYData()
	defer unsub()

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

loop:
	for {
		select {
		case <-deadline.C:
			break loop
		case <-ticker.C:
			// Drain at most one chunk per tick to simulate a slow consumer.
			select {
			case <-ch:
			default:
			}
		}
	}

	// After 2 s of slow consumption the agent must still be responsive.
	// Status() must return successfully (not hang) and the process must still
	// be Running — /usr/bin/yes does not exit on its own.
	st := h.Status()
	require.Equal(t, cliwrap.StateRunning, st.State,
		"agent must still be running after 2 s of slow PTY consumption")
}
