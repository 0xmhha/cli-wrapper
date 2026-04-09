// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
	"github.com/0xmhha/cli-wrapper/pkg/event"
)

// TestIntegration_EventsStreamDeliversLifecycle verifies that lifecycle
// events published by Manager.watchLoop are observable via the event
// bus subscription — which is the same mechanism the mgmt server uses
// to serve the `cliwrap events` command. This exercises the
// Manager→Bus→Subscription path end-to-end with a real agent + child.
//
// The mgmt socket path (subscribe → stream → decode) is covered by
// the unit test TestServer_EventsSubscribeStreamsFrames in
// internal/mgmt/server_test.go, so this test intentionally stays at
// the in-process subscription level to keep it deterministic (no
// network, no extra goroutine pair).
func TestIntegration_EventsStreamDeliversLifecycle(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)
	root := findRoot(t)
	noisy := filepath.Join(root, "test/fixtures/bin/fixture-noisy")

	mgr, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Deferred Shutdown with a fresh ctx so a test failure does not
	// leak Manager goroutines (follows the pattern from logs_test.go).
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = mgr.Shutdown(shutdownCtx)
	}()

	// Subscribe BEFORE Register/Start so we catch the early
	// ProcessStarting event. Calling Events() triggers lazy bus
	// construction if this is the first reference.
	sub := mgr.Events().Subscribe(event.Filter{})
	defer func() { _ = sub.Close() }()

	// Drain events in a goroutine so the bus does not drop them
	// under the slow-subscriber policy. Record the event type
	// history under a mutex for the main goroutine to assert
	// against.
	var mu sync.Mutex
	var seen []event.Type
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for e := range sub.Events() {
			mu.Lock()
			seen = append(seen, e.EventType())
			mu.Unlock()
		}
	}()

	spec, err := cliwrap.NewSpec("noisy", noisy).
		WithStopTimeout(2 * time.Second).
		Build()
	require.NoError(t, err)

	h, err := mgr.Register(spec)
	require.NoError(t, err)
	require.NoError(t, h.Start(ctx))

	// Wait for at least ProcessStarting and ProcessStarted to land
	// on the subscription. The Manager.watchLoop polls at 5 ms and
	// synthesizes any missed transitions, so these should appear
	// well within 10 s even on slow runners.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		sawStarting := false
		sawStarted := false
		for _, tp := range seen {
			if tp == event.TypeProcessStarting {
				sawStarting = true
			}
			if tp == event.TypeProcessStarted {
				sawStarted = true
			}
		}
		return sawStarting && sawStarted
	}, 10*time.Second, 50*time.Millisecond,
		"expected ProcessStarting + ProcessStarted events within 10 s")
}
