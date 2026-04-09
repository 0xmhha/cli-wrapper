// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManager_LogsSnapshotRoundTrip(t *testing.T) {
	m := &Manager{}

	// Nothing written yet: snapshot for unknown id/stream is nil.
	require.Nil(t, m.LogsSnapshot("redis", 0))

	// Simulate inbound log chunks as if the controller callback fired.
	m.emitLogChunk("redis", 0, []byte("line1\n"))
	m.emitLogChunk("redis", 0, []byte("line2\n"))
	m.emitLogChunk("redis", 1, []byte("err1\n"))

	stdout := m.LogsSnapshot("redis", 0)
	require.Equal(t, "line1\nline2\n", string(stdout))

	stderr := m.LogsSnapshot("redis", 1)
	require.Equal(t, "err1\n", string(stderr))

	// Unrelated id remains empty.
	require.Nil(t, m.LogsSnapshot("other", 0))
}

func TestManager_LogsSnapshotIsolatesProcesses(t *testing.T) {
	m := &Manager{}

	m.emitLogChunk("a", 0, []byte("A-out"))
	m.emitLogChunk("b", 0, []byte("B-out"))

	require.Equal(t, []byte("A-out"), m.LogsSnapshot("a", 0))
	require.Equal(t, []byte("B-out"), m.LogsSnapshot("b", 0))
}

func TestManager_NewControllerInjectsOnLogChunk(t *testing.T) {
	// Use the public NewManager constructor so the internal Spawner is
	// set up. Direct &Manager{...} literals leave spawner nil, which
	// causes controller.NewController to reject the options with
	// "controller: Spawner is required". The agent path need not be
	// real — it's only exec'd at Start() time, and this test never
	// calls Start().
	m, err := NewManager(
		WithAgentPath("/does/not/matter"),
		WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)

	spec, err := NewSpec("p1", "/bin/true").Build()
	require.NoError(t, err)

	ctrl, err := m.newController(spec)
	require.NoError(t, err)
	require.NotNil(t, ctrl)

	// Simulate the controller forwarding a log chunk. The closure
	// should route to m.emitLogChunk with processID="p1".
	// (Controller.Options is a test-only accessor defined on
	// internal/controller.Controller — see controller.go.)
	opts := ctrl.Options()
	require.NotNil(t, opts.OnLogChunk, "newController must set OnLogChunk")
	opts.OnLogChunk(0, []byte("from p1"))

	require.Equal(t, []byte("from p1"), m.LogsSnapshot("p1", 0))
}

func TestManager_WatchLogsDeliversLiveChunks(t *testing.T) {
	m := &Manager{}

	ch, unregister := m.WatchLogs("p1")
	defer unregister()

	// Emit one matching chunk and one non-matching (different id).
	m.emitLogChunk("p1", 0, []byte("first"))
	m.emitLogChunk("other", 0, []byte("ignored"))
	m.emitLogChunk("p1", 1, []byte("second-err"))

	// Drain three events: both p1 chunks; the "other" chunk must
	// not arrive on this watcher.
	got := make([]LogChunk, 0, 2)
	timeout := time.After(500 * time.Millisecond)
	for len(got) < 2 {
		select {
		case c, ok := <-ch:
			require.True(t, ok)
			got = append(got, c)
		case <-timeout:
			t.Fatalf("timed out waiting for chunks, got %d", len(got))
		}
	}

	require.Equal(t, "p1", got[0].ProcessID)
	require.Equal(t, uint8(0), got[0].Stream)
	require.Equal(t, []byte("first"), got[0].Data)

	require.Equal(t, "p1", got[1].ProcessID)
	require.Equal(t, uint8(1), got[1].Stream)
	require.Equal(t, []byte("second-err"), got[1].Data)
}

func TestManager_WatchLogsMatchAllWhenProcessIDEmpty(t *testing.T) {
	m := &Manager{}

	ch, unregister := m.WatchLogs("")
	defer unregister()

	m.emitLogChunk("a", 0, []byte("A"))
	m.emitLogChunk("b", 0, []byte("B"))

	var got []string
	timeout := time.After(500 * time.Millisecond)
	for len(got) < 2 {
		select {
		case c := <-ch:
			got = append(got, c.ProcessID)
		case <-timeout:
			t.Fatalf("timed out, got %v", got)
		}
	}

	require.ElementsMatch(t, []string{"a", "b"}, got)
}

func TestManager_WatchLogsUnregisterClosesChannel(t *testing.T) {
	m := &Manager{}

	ch, unregister := m.WatchLogs("p1")
	unregister()

	// After unregister the channel should be closed so a receive
	// returns (zero, false) rather than blocking forever.
	select {
	case _, ok := <-ch:
		require.False(t, ok, "channel should be closed after unregister")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("receive should return immediately on a closed channel")
	}
}
