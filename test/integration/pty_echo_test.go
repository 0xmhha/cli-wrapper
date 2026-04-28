// SPDX-License-Identifier: Apache-2.0

//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// TestPTY_EchoLoop spawns /bin/cat in PTY mode and sends 100 lines, asserting
// that each line is echoed back by the PTY within a 2-second window.
func TestPTY_EchoLoop(t *testing.T) {
	defer goleak.VerifyNone(t)

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

	spec, err := cliwrap.NewSpec("pty-echo-loop", "/bin/cat").
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

	ch, unsub := h.SubscribePTYData()
	defer unsub()

	for i := 0; i < 100; i++ {
		line := fmt.Sprintf("line-%d\n", i)
		require.NoError(t, h.WriteInput(ctx, []byte(line)))

		gotMatch := false
		deadline := time.NewTimer(2 * time.Second)
	drain:
		for {
			select {
			case d := <-ch:
				if len(d.Bytes) > 0 && bytes.Contains(d.Bytes, []byte(line[:len(line)-1])) {
					gotMatch = true
					break drain
				}
				// PTY echoes may arrive in chunks; keep draining.
			case <-deadline.C:
				break drain
			}
		}
		deadline.Stop()
		require.Truef(t, gotMatch, "missed echo for line %d", i)
	}
}
