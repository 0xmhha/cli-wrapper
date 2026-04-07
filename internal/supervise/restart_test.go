package supervise

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExponentialBackoff(t *testing.T) {
	b := ExponentialBackoff{
		Initial:    100 * time.Millisecond,
		Max:        2 * time.Second,
		Multiplier: 2.0,
		Jitter:     0, // deterministic
	}
	require.Equal(t, 100*time.Millisecond, b.Next(0))
	require.Equal(t, 200*time.Millisecond, b.Next(1))
	require.Equal(t, 400*time.Millisecond, b.Next(2))
	require.Equal(t, 800*time.Millisecond, b.Next(3))
	require.Equal(t, 1600*time.Millisecond, b.Next(4))
	require.Equal(t, 2*time.Second, b.Next(5))  // capped
	require.Equal(t, 2*time.Second, b.Next(10)) // still capped
}

func TestExponentialBackoff_WithJitterProducesVariance(t *testing.T) {
	b := ExponentialBackoff{
		Initial:    100 * time.Millisecond,
		Max:        10 * time.Second,
		Multiplier: 2.0,
		Jitter:     0.5,
	}
	// Pull several values and assert they are not all identical.
	d := b.Next(3)
	seenDifferent := false
	for i := 0; i < 5; i++ {
		if b.Next(3) != d {
			seenDifferent = true
			break
		}
	}
	require.True(t, seenDifferent, "jitter should introduce variance")
}
