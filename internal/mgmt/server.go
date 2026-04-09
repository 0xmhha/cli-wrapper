// SPDX-License-Identifier: Apache-2.0

package mgmt

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/internal/ipc"
	"github.com/0xmhha/cli-wrapper/pkg/event"
)

// ManagerAPI is the subset of Manager functionality the server exposes.
// Using an interface keeps tests decoupled from the concrete Manager.
type ManagerAPI interface {
	List() []ListEntry
	StatusOf(id string) (ListEntry, error)
	Stop(ctx context.Context, id string) error
	LogsSnapshot(id string, stream uint8) []byte
	// WatchLogs returns a channel that delivers every log chunk
	// emitted for processID after the subscription is registered,
	// plus an unregister function the caller MUST invoke exactly
	// once. The channel closes when the manager shuts down.
	WatchLogs(processID string) (<-chan cwtypes.LogChunk, func())
	Events() event.Bus
}

// ServerOptions configures the management server.
type ServerOptions struct {
	SocketPath string
	Manager    ManagerAPI
	SpillerDir string
}

// Server listens on a Unix domain socket and handles management requests.
type Server struct {
	opts     ServerOptions
	listener net.Listener
	wg       sync.WaitGroup
	closed   bool
	mu       sync.Mutex
}

// NewServer returns a Server; call Start to begin accepting connections.
func NewServer(opts ServerOptions) (*Server, error) {
	if opts.SocketPath == "" {
		return nil, errors.New("mgmt: SocketPath required")
	}
	if opts.Manager == nil {
		return nil, errors.New("mgmt: Manager required")
	}
	if err := os.MkdirAll(filepath.Dir(opts.SocketPath), 0o700); err != nil {
		return nil, fmt.Errorf("mgmt: mkdir: %w", err)
	}
	_ = os.Remove(opts.SocketPath)
	ln, err := net.Listen("unix", opts.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("mgmt: listen: %w", err)
	}
	if err := os.Chmod(opts.SocketPath, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("mgmt: chmod socket: %w", err)
	}
	return &Server{opts: opts, listener: ln}, nil
}

// Start begins accepting connections in a background goroutine.
func (s *Server) Start() error {
	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() { _ = conn.Close() }()

	reader := ipc.NewFrameReader(conn, ipc.MaxPayloadSize)
	writer := ipc.NewFrameWriter(conn)

	for {
		h, body, err := reader.ReadFrame()
		if err != nil {
			return
		}

		// MsgEventsSubscribe upgrades the connection into a streaming
		// channel: after receiving the subscribe request, the server
		// pushes MsgEventsStream frames as events arrive until either
		// the client disconnects or the manager's bus closes. The
		// connection is dedicated to the stream and does NOT return to
		// the request-response loop.
		if h.MsgType == MsgEventsSubscribe {
			s.handleEventsStream(body, writer)
			return
		}

		// MsgLogsRequest is handled inline (both snapshot and follow
		// modes) to avoid decoding the payload twice — once here to
		// inspect the Follow flag, and again in handleRequest for
		// the snapshot case. Follow mode is a streaming upgrade and
		// hard-returns from handleConn; snapshot mode writes a
		// single EOF=true frame and loops back to the reader.
		if h.MsgType == MsgLogsRequest {
			var req LogsRequestPayload
			if derr := ipc.DecodePayload(body, &req); derr == nil {
				if req.Follow {
					s.handleLogsFollow(writer, req)
					return
				}
				snap := s.opts.Manager.LogsSnapshot(req.ID, req.Stream)
				if werr := writeLogsStreamFrame(writer, LogsStreamPayload{
					ID:     req.ID,
					Stream: req.Stream,
					Data:   snap,
					EOF:    true,
				}); werr != nil {
					return
				}
				continue
			}
			// decode failed — fall through to handleRequest, which
			// will emit a default error response frame.
		}

		resp, respType, rerr := s.handleRequest(h, body)
		if rerr != nil {
			return
		}
		data, err := ipc.EncodePayload(resp)
		if err != nil {
			return
		}
		_, err = writer.WriteFrame(ipc.Header{
			MsgType: respType,
			SeqNo:   h.SeqNo,
			Length:  uint32(len(data)),
		}, data)
		if err != nil {
			return
		}
	}
}

// handleEventsStream subscribes to the manager's event bus and pushes
// each event to the client as a MsgEventsStream frame until either the
// bus closes (range over channel exits) or the client disconnects
// (WriteFrame returns an error).
func (s *Server) handleEventsStream(body []byte, writer *ipc.FrameWriter) {
	var req EventsSubscribePayload
	_ = ipc.DecodePayload(body, &req)

	bus := s.opts.Manager.Events()
	sub := bus.Subscribe(event.Filter{ProcessIDs: req.ProcessIDs})
	defer func() { _ = sub.Close() }()

	for e := range sub.Events() {
		payload := EventsStreamPayload{
			ProcessID: e.ProcessID(),
			Type:      string(e.EventType()),
			Timestamp: e.Timestamp().UnixNano(),
			Summary:   summarizeEvent(e),
		}
		data, err := ipc.EncodePayload(payload)
		if err != nil {
			continue
		}
		_, err = writer.WriteFrame(ipc.Header{
			MsgType: MsgEventsStream,
			Length:  uint32(len(data)),
		}, data)
		if err != nil {
			// Client disconnected; stop streaming. The deferred
			// Close will remove the subscription from the bus.
			return
		}
	}
}

// summarizeEvent extracts a short human-readable description of an
// event for streaming over the mgmt protocol. The current schema carries
// only (ProcessID, Type, Timestamp, Summary), so this helper collapses
// the per-type context into a single string.
func summarizeEvent(e event.Event) string {
	return fmt.Sprintf("%s %s", e.EventType(), e.ProcessID())
}

// handleLogsFollow writes the current ring-buffer snapshot to the
// client as a single MsgLogsStream frame with EOF=false, then
// subscribes to live log chunks via Manager.WatchLogs and pushes each
// new chunk as an additional MsgLogsStream frame with EOF=false until
// either the client disconnects (WriteFrame error) or the manager
// shuts down (watcher channel closes).
//
// Unlike the snapshot-only handler in handleRequest, this method
// keeps the connection open and the caller MUST return from
// handleConn immediately after this function returns — otherwise the
// request-response loop would double-handle the connection.
func (s *Server) handleLogsFollow(writer *ipc.FrameWriter, req LogsRequestPayload) {
	// Defensive validation: the CLI client rejects Stream>1 before
	// sending, but a raw wire-level client could still send junk.
	// Without this guard, WatchLogs would subscribe normally, the
	// stream-filter `chunk.Stream != req.Stream` on line below
	// would drop every chunk forever, and the handler would hold
	// the subscription + connection open indefinitely — a resource
	// leak. Close the stream early with an error payload instead.
	if req.Stream > 1 {
		errPayload := LogsStreamPayload{
			ID:     req.ID,
			Stream: req.Stream,
			Data:   fmt.Appendf(nil, "cliwrap: invalid stream id %d (want 0 or 1)\n", req.Stream),
			EOF:    true,
		}
		_ = writeLogsStreamFrame(writer, errPayload)
		return
	}

	// Step 1: send the current ring-buffer snapshot.
	snap := s.opts.Manager.LogsSnapshot(req.ID, req.Stream)
	snapPayload := LogsStreamPayload{
		ID:     req.ID,
		Stream: req.Stream,
		Data:   snap,
		EOF:    false, // more frames to follow
	}
	if err := writeLogsStreamFrame(writer, snapPayload); err != nil {
		return
	}

	// Step 2: register a live watcher and forward every chunk
	// matching the requested stream. An unregister function must be
	// invoked exactly once — via defer.
	ch, unregister := s.opts.Manager.WatchLogs(req.ID)
	defer unregister()

	for chunk := range ch {
		// The watcher has no stream filter, so skip chunks that do
		// not match the request's stream id. (Request stream 0/1
		// maps 1:1 to the child's stdout/stderr channels.)
		if chunk.Stream != req.Stream {
			continue
		}
		payload := LogsStreamPayload{
			ID:     req.ID,
			Stream: chunk.Stream,
			Data:   chunk.Data,
			EOF:    false,
		}
		if err := writeLogsStreamFrame(writer, payload); err != nil {
			return
		}
	}
}

// writeLogsStreamFrame encodes and sends one MsgLogsStream frame.
// Helper used by both the snapshot and follow handlers to avoid
// duplicating the encode/write boilerplate.
func writeLogsStreamFrame(writer *ipc.FrameWriter, payload LogsStreamPayload) error {
	data, err := ipc.EncodePayload(payload)
	if err != nil {
		return err
	}
	_, err = writer.WriteFrame(ipc.Header{
		MsgType: MsgLogsStream,
		Length:  uint32(len(data)),
	}, data)
	return err
}

func (s *Server) handleRequest(h ipc.Header, body []byte) (any, ipc.MsgType, error) {
	ctx := context.Background()
	switch h.MsgType {
	case MsgListRequest:
		return ListResponsePayload{Entries: s.opts.Manager.List()}, MsgListResponse, nil
	case MsgStatusRequest:
		var req StatusRequestPayload
		_ = ipc.DecodePayload(body, &req)
		entry, err := s.opts.Manager.StatusOf(req.ID)
		resp := StatusResponsePayload{Entry: entry}
		if err != nil {
			resp.Err = err.Error()
		}
		return resp, MsgStatusResponse, nil
	case MsgStopRequest:
		var req StopRequestPayload
		_ = ipc.DecodePayload(body, &req)
		err := s.opts.Manager.Stop(ctx, req.ID)
		resp := StatusResponsePayload{}
		if err != nil {
			resp.Err = err.Error()
		}
		return resp, MsgStatusResponse, nil
	// MsgLogsRequest is handled inline in handleConn (both snapshot
	// and follow modes) to avoid decoding the payload twice. If
	// handleRequest ever sees a MsgLogsRequest it means the inline
	// decode failed — fall through to the default error response.
	default:
		return StatusResponsePayload{Err: fmt.Sprintf("unknown request type 0x%02x", h.MsgType)}, MsgStatusResponse, nil
	}
}

// Close stops the listener and waits for goroutines to exit.
func (s *Server) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	_ = s.listener.Close()
	_ = os.Remove(s.opts.SocketPath)

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
