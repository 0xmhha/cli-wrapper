// SPDX-License-Identifier: Apache-2.0

//go:build integration

package integration

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// TestPTY_DataPersistedToLogPipeline spawns /bin/echo in PTY mode, waits for
// the child to exit, then verifies that the echoed token appears in the log
// ring-buffer via Manager.LogsSnapshot.
//
// Spec §3.3: PTY bytes are teed to r.StdoutSink (the chunkWriter installed by
// dispatcher.go for stream 0) in addition to being sent as MsgTypePTYData
// frames. This means PTY output flows through the same MsgLogChunk pipeline
// used by pipe-mode stdout, and therefore lands in the Collector under
// (processID, stream=0).
func TestPTY_DataPersistedToLogPipeline(t *testing.T) {
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

	const token = "persisted-token"

	spec, err := cliwrap.NewSpec("pty-log-persist", "/bin/echo", token).
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

	// Wait for the child to exit naturally. /bin/echo emits its output and
	// exits immediately, so StateStopped should arrive quickly. We use a
	// generous 15-second budget to cover agent-spawn + handshake latency on
	// slow CI runners.
	require.Eventually(t, func() bool {
		state := h.Status().State
		return state == cliwrap.StateStopped || state == cliwrap.StateCrashed
	}, 15*time.Second, 50*time.Millisecond,
		"echo process should exit (StateStopped or StateCrashed) within 15 s")

	// PTY bytes are teed to StdoutSink (stream 0) as they are read from the
	// PTY master fd. After the child has exited the full output has been
	// forwarded through the pipeline. Poll LogsSnapshot briefly to allow any
	// in-flight IPC frames to drain.
	require.Eventually(t, func() bool {
		snapshot := mgr.LogsSnapshot("pty-log-persist", 0)
		return bytes.Contains(snapshot, []byte(token))
	}, 10*time.Second, 100*time.Millisecond,
		"token %q must appear in log ring-buffer (stream 0) after PTY child exits", token)
}
