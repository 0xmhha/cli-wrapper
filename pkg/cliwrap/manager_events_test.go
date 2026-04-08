package cliwrap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/cli-wrapper/cli-wrapper/internal/supervise"
	"github.com/cli-wrapper/cli-wrapper/pkg/event"
)

func TestManager_EmitsLifecycleEvents(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)
	mgr, err := NewManager(
		WithAgentPath(agentBin),
		WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sub := mgr.Events().Subscribe(event.Filter{})
	defer sub.Close()

	spec, err := NewSpec("ev", "/bin/sh", "-c", "exit 0").Build()
	require.NoError(t, err)
	h, err := mgr.Register(spec)
	require.NoError(t, err)
	require.NoError(t, h.Start(ctx))

	seen := map[event.Type]bool{}
	deadline := time.After(3 * time.Second)
	for !seen[event.TypeProcessStarted] || !seen[event.TypeProcessStopped] {
		select {
		case ev := <-sub.Events():
			seen[ev.EventType()] = true
		case <-deadline:
			t.Fatalf("did not observe lifecycle events; got %v", seen)
		}
	}
	require.True(t, seen[event.TypeProcessStarted])
	require.True(t, seen[event.TypeProcessStopped])

	require.NoError(t, mgr.Shutdown(ctx))
}
