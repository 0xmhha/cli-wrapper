// SPDX-License-Identifier: Apache-2.0

package supervise

import (
	"math"
	"math/rand"
	"time"
)

// BackoffStrategy computes the delay before the Nth restart attempt.
type BackoffStrategy interface {
	Next(attempt int) time.Duration
}

// ExponentialBackoff is an exponential backoff with optional jitter.
type ExponentialBackoff struct {
	Initial    time.Duration
	Max        time.Duration
	Multiplier float64
	Jitter     float64 // 0..1, relative jitter range

	rng *rand.Rand
}

// Next returns the delay before the (attempt)th retry. Attempt 0 returns Initial.
func (b *ExponentialBackoff) Next(attempt int) time.Duration {
	if b.Initial <= 0 {
		b.Initial = time.Second
	}
	if b.Max <= 0 {
		b.Max = time.Minute
	}
	if b.Multiplier <= 1 {
		b.Multiplier = 2
	}

	d := float64(b.Initial) * math.Pow(b.Multiplier, float64(attempt))
	if d > float64(b.Max) {
		d = float64(b.Max)
	}

	if b.Jitter > 0 {
		if b.rng == nil {
			b.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
		}
		delta := d * b.Jitter
		d += (b.rng.Float64()*2 - 1) * delta
		if d < 0 {
			d = 0
		}
	}
	return time.Duration(d)
}
