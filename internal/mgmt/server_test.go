// SPDX-License-Identifier: Apache-2.0

package mgmt

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/internal/eventbus"
	"github.com/0xmhha/cli-wrapper/internal/ipc"
	"github.com/0xmhha/cli-wrapper/pkg/event"
)

// fakeManager is the test double used by all mgmt server tests. It
// satisfies the ManagerAPI interface with minimal stubs. Tests that
// exercise specific methods may set the corresponding fields to tune
// behavior.
type fakeManager struct {
	logs map[string][]byte // key = id + "/" + stream
	bus  *eventbus.Bus     // lazily created via Events()

	// Follow-mode watcher registry. Tests that want to drive the
	// follow-path push chunks to every registered channel.
	watchMu  sync.Mutex
	watchers []chan cwtypes.LogChunk
}

func (f *fakeManager) List() []ListEntry {
	return []ListEntry{{ID: "p1", State: "running", ChildPID: 42}}
}
func (f *fakeManager) StatusOf(id string) (ListEntry, error) {
	if id != "p1" {
		return ListEntry{}, errNotFound
	}
	return ListEntry{ID: "p1", State: "running", ChildPID: 42}, nil
}
func (f *fakeManager) Stop(ctx context.Context, id string) error { return nil }

func (f *fakeManager) LogsSnapshot(id string, stream uint8) []byte {
	if f.logs == nil {
		return nil
	}
	return f.logs[id+"/"+string(rune('0'+stream))]
}

// Events lazily constructs an in-memory event bus and returns it.
// Tests that want to publish events can call this and then Publish
// via the returned bus; tests that do not care about events can
// ignore the lazy init entirely.
func (f *fakeManager) Events() event.Bus {
	if f.bus == nil {
		f.bus = eventbus.New(16)
	}
	return f.bus
}

// WatchLogs registers a follow-mode channel on the fake. Tests call
// pushLogChunk to deliver chunks to every registered channel.
func (f *fakeManager) WatchLogs(processID string) (<-chan cwtypes.LogChunk, func()) {
	ch := make(chan cwtypes.LogChunk, 64)
	f.watchMu.Lock()
	f.watchers = append(f.watchers, ch)
	f.watchMu.Unlock()
	unregister := func() {
		f.watchMu.Lock()
		defer f.watchMu.Unlock()
		for i, c := range f.watchers {
			if c == ch {
				f.watchers = append(f.watchers[:i], f.watchers[i+1:]...)
				close(ch)
				return
			}
		}
	}
	return ch, unregister
}

// pushLogChunk delivers chunk to every registered watcher channel.
// Returns false if any send would block (channel full).
func (f *fakeManager) pushLogChunk(chunk cwtypes.LogChunk) bool {
	f.watchMu.Lock()
	defer f.watchMu.Unlock()
	for _, ch := range f.watchers {
		select {
		case ch <- chunk:
		default:
			return false
		}
	}
	return true
}

// closeAllWatchers closes every registered watcher channel — used in
// test teardown to mimic Manager.Shutdown so follow-mode handlers
// exit cleanly.
func (f *fakeManager) closeAllWatchers() {
	f.watchMu.Lock()
	defer f.watchMu.Unlock()
	for _, ch := range f.watchers {
		close(ch)
	}
	f.watchers = nil
}

var errNotFound = &notFoundError{}

type notFoundError struct{}

func (n *notFoundError) Error() string { return "not found" }

func TestServer_ListRoundTrip(t *testing.T) {
	defer goleak.VerifyNone(t)

	sockPath := filepath.Join(t.TempDir(), "mgr.sock")
	srv, err := NewServer(ServerOptions{
		SocketPath: sockPath,
		Manager:    &fakeManager{},
		SpillerDir: t.TempDir(),
	})
	require.NoError(t, err)
	require.NoError(t, srv.Start())
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Close(ctx)
	}()

	// Dial and send a LIST_REQUEST manually.
	conn, err := net.Dial("unix", sockPath)
	require.NoError(t, err)
	defer conn.Close()

	w := ipc.NewFrameWriter(conn)
	_, err = w.WriteFrame(ipc.Header{MsgType: MsgListRequest, SeqNo: 1, Length: 0}, nil)
	require.NoError(t, err)

	r := ipc.NewFrameReader(conn, ipc.MaxPayloadSize)
	h, body, err := r.ReadFrame()
	require.NoError(t, err)
	require.Equal(t, MsgListResponse, h.MsgType)

	var resp ListResponsePayload
	require.NoError(t, ipc.DecodePayload(body, &resp))
	require.Len(t, resp.Entries, 1)
	require.Equal(t, "p1", resp.Entries[0].ID)
}

func TestServer_LogsRequestRoundTrip(t *testing.T) {
	defer goleak.VerifyNone(t)

	fm := &fakeManager{
		logs: map[string][]byte{
			"p1/0": []byte("out1\nout2\n"),
			"p1/1": []byte("err1\n"),
		},
	}

	sockPath := filepath.Join(t.TempDir(), "mgr.sock")
	srv, err := NewServer(ServerOptions{
		SocketPath: sockPath,
		Manager:    fm,
		SpillerDir: t.TempDir(),
	})
	require.NoError(t, err)
	require.NoError(t, srv.Start())
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Close(ctx)
	}()

	cli, err := Dial(sockPath)
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()

	// Request stdout for p1.
	h, body, err := cli.Call(MsgLogsRequest, LogsRequestPayload{ID: "p1", Stream: 0})
	require.NoError(t, err)
	require.Equal(t, MsgLogsStream, h.MsgType)

	var resp LogsStreamPayload
	require.NoError(t, ipc.DecodePayload(body, &resp))
	require.Equal(t, "p1", resp.ID)
	require.Equal(t, uint8(0), resp.Stream)
	require.Equal(t, []byte("out1\nout2\n"), resp.Data)
	require.True(t, resp.EOF)
}

func TestServer_EventsSubscribeStreamsFrames(t *testing.T) {
	defer goleak.VerifyNone(t)

	fm := &fakeManager{}
	// Pre-construct the bus so the test publisher can push events
	// after the subscription is registered server-side. The deferred
	// bus.Close() is critical: it closes the subscription channel,
	// causing the server's `range sub.Events()` loop inside
	// handleEventsStream to exit, which in turn lets handleConn
	// return cleanly so goleak.VerifyNone does not catch a leaked
	// server goroutine on test exit.
	bus := fm.Events().(*eventbus.Bus)
	defer bus.Close()

	// macOS unix-socket paths have a ~104-byte SUN_LEN limit, and
	// t.TempDir() + the long test name can easily exceed it. Use a
	// shorter custom path.
	shortDir, err := os.MkdirTemp("", "evtsub-")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(shortDir) }()
	sockPath := filepath.Join(shortDir, "m.sock")
	srv, err := NewServer(ServerOptions{
		SocketPath: sockPath,
		Manager:    fm,
		SpillerDir: t.TempDir(),
	})
	require.NoError(t, err)
	require.NoError(t, srv.Start())
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Close(ctx)
	}()

	cli, err := Dial(sockPath)
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()

	// Send the subscribe frame without waiting for a response.
	require.NoError(t, cli.Stream(MsgEventsSubscribe, EventsSubscribePayload{}))

	// Publish events from a goroutine until the test ends. This
	// solves the "subscribe is asynchronous server-side" race: we
	// cannot observe the exact moment the server registers the
	// subscription, so we keep republishing until the client's
	// ReadFrame returns one event, then stop.
	stopPublishing := make(chan struct{})
	publishDone := make(chan struct{})
	go func() {
		defer close(publishDone)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopPublishing:
				return
			case <-ticker.C:
				bus.Publish(event.NewProcessStopped("p1", time.Now(), 0))
			}
		}
	}()

	// First event observation — signals that the subscription is
	// registered on the server side.
	h, body, err := cli.ReadFrame()
	require.NoError(t, err)
	require.Equal(t, MsgEventsStream, h.MsgType)
	var p EventsStreamPayload
	require.NoError(t, ipc.DecodePayload(body, &p))
	require.Equal(t, "p1", p.ProcessID)
	require.Equal(t, string(event.TypeProcessStopped), p.Type)

	// Stop publishing now that we know the stream works.
	close(stopPublishing)
	<-publishDone
}

func TestServer_LogsRequestFollowStreamsFrames(t *testing.T) {
	defer goleak.VerifyNone(t)

	fm := &fakeManager{
		logs: map[string][]byte{
			"p1/0": []byte("snapshot-out\n"),
		},
	}
	// Ensure teardown closes every watcher so the server handler
	// exits via the "channel closed" path and goleak stays clean.
	defer fm.closeAllWatchers()

	// Use a short socket path (macOS 104-byte limit).
	shortDir, err := os.MkdirTemp("", "logsfol-")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(shortDir) }()
	sockPath := filepath.Join(shortDir, "m.sock")

	srv, err := NewServer(ServerOptions{
		SocketPath: sockPath,
		Manager:    fm,
		SpillerDir: t.TempDir(),
	})
	require.NoError(t, err)
	require.NoError(t, srv.Start())
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Close(ctx)
	}()

	cli, err := Dial(sockPath)
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()

	// Send a follow-mode logs request for stream 0 (stdout).
	require.NoError(t, cli.Stream(MsgLogsRequest, LogsRequestPayload{
		ID:     "p1",
		Stream: 0,
		Follow: true,
	}))

	// First frame: snapshot with EOF=false.
	h, body, err := cli.ReadFrame()
	require.NoError(t, err)
	require.Equal(t, MsgLogsStream, h.MsgType)
	var snap LogsStreamPayload
	require.NoError(t, ipc.DecodePayload(body, &snap))
	require.Equal(t, "p1", snap.ID)
	require.Equal(t, uint8(0), snap.Stream)
	require.Equal(t, []byte("snapshot-out\n"), snap.Data)
	require.False(t, snap.EOF, "follow-mode snapshot frame must have EOF=false")

	// Publish a live chunk from the fake's watcher registry until
	// the client observes one. Like the events test, this handles
	// the subscribe-registration race: the server's WatchLogs
	// registration happens asynchronously, so we push events in a
	// ticker until the first frame arrives.
	stopPush := make(chan struct{})
	pushDone := make(chan struct{})
	go func() {
		defer close(pushDone)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopPush:
				return
			case <-ticker.C:
				fm.pushLogChunk(cwtypes.LogChunk{
					ProcessID: "p1",
					Stream:    0,
					Data:      []byte("live-chunk\n"),
				})
			}
		}
	}()

	// Read the next frame — it should be the first live chunk.
	h, body, err = cli.ReadFrame()
	require.NoError(t, err)
	require.Equal(t, MsgLogsStream, h.MsgType)
	var live LogsStreamPayload
	require.NoError(t, ipc.DecodePayload(body, &live))
	require.Equal(t, []byte("live-chunk\n"), live.Data)
	require.False(t, live.EOF)

	close(stopPush)
	<-pushDone
}
