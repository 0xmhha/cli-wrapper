// SPDX-License-Identifier: Apache-2.0

package mgmt

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

// Replace the existing fakeManager with this expanded version.
type fakeManager struct {
	logs map[string][]byte // key = id + "/" + stream
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
