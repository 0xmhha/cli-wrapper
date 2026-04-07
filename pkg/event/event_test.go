package event

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEventInterface_Implementations(t *testing.T) {
	now := time.Now()

	events := []Event{
		NewProcessStarted("p1", now, 100, 200),
		NewProcessStopped("p1", now, 0),
		NewProcessCrashed("p1", now, CrashContext{Reason: "oom"}, true, 2*time.Second),
		NewAgentFatal("p1", now, "panic in reader"),
		NewLogOverflow("p1", now, "rotator full"),
		NewBackpressureStall("p1", now, "host", 1024, 2048, 3),
	}
	for _, e := range events {
		require.Equal(t, "p1", e.ProcessID())
		require.NotEmpty(t, string(e.EventType()))
		require.False(t, e.Timestamp().IsZero())
	}
}
