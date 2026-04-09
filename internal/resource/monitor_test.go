// SPDX-License-Identifier: Apache-2.0

package resource

import (
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

type fakeSource struct{ pids []int }

func (f *fakeSource) ChildPIDs() map[string]int {
	out := make(map[string]int, len(f.pids))
	for i, p := range f.pids {
		out[idFor(i)] = p
	}
	return out
}

func idFor(i int) string { return "p" + strconv.Itoa(i) }

func TestMonitor_PollsAndStops(t *testing.T) {
	defer goleak.VerifyNone(t)

	var samples atomic.Int64
	src := &fakeSource{pids: []int{os.Getpid()}}

	m := NewMonitor(MonitorOptions{
		Interval:         20 * time.Millisecond,
		Source:           src,
		ProcessCollector: DefaultProcessCollector(),
		SystemCollector:  DefaultSystemCollector(),
		OnSample:         func(id string, s Sample) { samples.Add(1) },
	})

	m.Start()
	// Use Eventually instead of a fixed Sleep so the test is
	// deterministic under slow schedulers. A fixed 80ms sleep
	// with 20ms ticks is a race: on loaded CI runners the first
	// tick may arrive late enough that only 1 sample lands before
	// Stop is called. Eventually waits up to 2 s for the second
	// sample and exits immediately once it's observed.
	require.Eventually(t, func() bool {
		return samples.Load() >= 2
	}, 2*time.Second, 10*time.Millisecond,
		"monitor should produce at least 2 samples at 20ms interval")
	m.Stop()
}
