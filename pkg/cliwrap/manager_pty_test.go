// SPDX-License-Identifier: Apache-2.0

//go:build integration

package cliwrap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
)

func TestManager_SpawnPTYAndEcho(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)
	m, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	spec, err := NewSpec("pty-echo", "/bin/cat").
		WithPTY(&PTYConfig{InitialCols: 80, InitialRows: 24}).
		WithStopTimeout(2 * time.Second).
		Build()
	require.NoError(t, err)

	h, err := m.Register(spec)
	require.NoError(t, err)

	require.NoError(t, h.Start(ctx))

	defer func() {
		_ = h.Stop(context.Background())
		_ = h.Close(context.Background())
		_ = m.Shutdown(context.Background())
	}()

	// Wait for the process to reach running state.
	require.Eventually(t, func() bool {
		return h.Status().State == StateRunning
	}, 5*time.Second, 20*time.Millisecond, "process did not reach running state")

	ch, unsub := h.SubscribePTYData()
	defer unsub()

	require.NoError(t, h.WriteInput(ctx, []byte("hello\n")))

	select {
	case d := <-ch:
		require.Contains(t, string(d.Bytes), "hello")
	case <-time.After(5 * time.Second):
		t.Fatal("no PTY data received within timeout")
	}
}

func TestManager_WriteInputErrorsForPipeMode(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)
	m, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	defer func() { _ = m.Shutdown(context.Background()) }()

	spec, err := NewSpec("pipe-sleep", "/bin/sleep", "10").
		WithStopTimeout(2 * time.Second).
		Build()
	require.NoError(t, err)

	h, err := m.Register(spec)
	require.NoError(t, err)

	// WriteInput must return ErrPTYNotConfigured before even starting.
	err = h.WriteInput(context.Background(), []byte("x"))
	require.ErrorIs(t, err, ErrPTYNotConfigured)
}
