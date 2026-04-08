package supervise

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeStarter struct {
	starts    atomic.Int32
	failUntil int32
	returnErr error
}

func (f *fakeStarter) Start(ctx context.Context) error {
	n := f.starts.Add(1)
	if n <= f.failUntil {
		return f.returnErr
	}
	return nil
}

func TestRestartLoop_AlwaysPolicy(t *testing.T) {
	s := &fakeStarter{failUntil: 3, returnErr: errors.New("boom")}
	rl := NewRestartLoop(RestartLoopOptions{
		Starter:     s,
		Policy:      RestartLoopAlways,
		MaxRestarts: 5,
		Backoff:     &ExponentialBackoff{Initial: 5 * time.Millisecond, Max: 10 * time.Millisecond, Multiplier: 2},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := rl.Run(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 4, s.starts.Load(), "first 3 fail, 4th succeeds")
}

func TestRestartLoop_NeverPolicyReturnsFirstError(t *testing.T) {
	boom := errors.New("boom")
	s := &fakeStarter{failUntil: 10, returnErr: boom}
	rl := NewRestartLoop(RestartLoopOptions{
		Starter: s,
		Policy:  RestartLoopNever,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := rl.Run(ctx)
	require.ErrorIs(t, err, boom)
	require.EqualValues(t, 1, s.starts.Load())
}

func TestRestartLoop_MaxRestartsExhausted(t *testing.T) {
	s := &fakeStarter{failUntil: 100, returnErr: errors.New("boom")}
	rl := NewRestartLoop(RestartLoopOptions{
		Starter:     s,
		Policy:      RestartLoopAlways,
		MaxRestarts: 2,
		Backoff:     &ExponentialBackoff{Initial: time.Millisecond, Max: 5 * time.Millisecond, Multiplier: 2},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := rl.Run(ctx)
	require.Error(t, err)
	require.EqualValues(t, 3, s.starts.Load(), "initial + 2 retries")
}
