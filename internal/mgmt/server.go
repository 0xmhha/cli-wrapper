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

	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

// ManagerAPI is the subset of Manager functionality the server exposes.
// Using an interface keeps tests decoupled from the concrete Manager.
type ManagerAPI interface {
	List() []ListEntry
	StatusOf(id string) (ListEntry, error)
	Stop(ctx context.Context, id string) error
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
