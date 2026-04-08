package supervise

import (
	"context"
	"errors"
	"time"
)

// RestartLoopPolicy controls whether Run retries on failure.
type RestartLoopPolicy int

const (
	RestartLoopNever RestartLoopPolicy = iota
	RestartLoopOnFailure
	RestartLoopAlways
)

// ErrMaxRestartsExhausted is returned when Run gives up.
var ErrMaxRestartsExhausted = errors.New("supervise: max restarts exhausted")

// Starter is any object that can attempt to start a process once.
type Starter interface {
	Start(ctx context.Context) error
}

// RestartLoopOptions configures a RestartLoop.
type RestartLoopOptions struct {
	Starter     Starter
	Policy      RestartLoopPolicy
	MaxRestarts int // 0 = unlimited
	Backoff     BackoffStrategy
}

// RestartLoop runs Starter.Start in a loop, honoring the configured policy.
type RestartLoop struct {
	opts RestartLoopOptions
}

// NewRestartLoop returns a loop.
func NewRestartLoop(opts RestartLoopOptions) *RestartLoop {
	if opts.Backoff == nil {
		opts.Backoff = &ExponentialBackoff{}
	}
	return &RestartLoop{opts: opts}
}

// Run executes Starter.Start until it succeeds (for OnFailure/Always) or
// MaxRestarts is exhausted. It returns nil on successful start, or the
// last observed error otherwise.
func (r *RestartLoop) Run(ctx context.Context) error {
	attempts := 0
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return lastErr
			}
			return err
		}

		err := r.opts.Starter.Start(ctx)
		if err == nil {
			return nil
		}
		lastErr = err

		if r.opts.Policy == RestartLoopNever {
			return err
		}

		attempts++
		if r.opts.MaxRestarts > 0 && attempts > r.opts.MaxRestarts {
			return ErrMaxRestartsExhausted
		}
		delay := r.opts.Backoff.Next(attempts - 1)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
