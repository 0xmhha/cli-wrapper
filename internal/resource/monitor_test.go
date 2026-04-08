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
	time.Sleep(80 * time.Millisecond)
	m.Stop()

	require.GreaterOrEqual(t, samples.Load(), int64(2))
}
