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

type fakeManager struct{}

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
