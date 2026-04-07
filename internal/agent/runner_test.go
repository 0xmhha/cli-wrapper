package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestRunner_RunAndExit(t *testing.T) {
	defer goleak.VerifyNone(t)

	r := NewRunner()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := r.Run(ctx, RunSpec{
		Command: "/bin/sh",
		Args:    []string{"-c", "exit 0"},
	})
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
	require.Equal(t, 0, result.Signal)
	require.NotZero(t, result.PID)
}

func TestRunner_NonZeroExit(t *testing.T) {
	defer goleak.VerifyNone(t)

	r := NewRunner()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := r.Run(ctx, RunSpec{
		Command: "/bin/sh",
		Args:    []string{"-c", "exit 42"},
	})
	require.NoError(t, err)
	require.Equal(t, 42, result.ExitCode)
}

func TestRunner_CancelTerminatesProcess(t *testing.T) {
	defer goleak.VerifyNone(t)

	r := NewRunner()
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		_, err := r.Run(ctx, RunSpec{
			Command: "/bin/sh",
			Args:    []string{"-c", "sleep 10"},
		})
		errCh <- err
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err, "Run should return cleanly after ctx cancel")
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}
