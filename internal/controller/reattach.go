// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

// ReattachOptions carries the inputs needed to construct a Controller
// around an already-handshaked persistent session conn (CW-G4 Task 14).
//
// The caller (Manager.Reattach) has already:
// 1. Verified the session metadata and pid alive.
// 2. Dialed the per-session UNIX socket.
// 3. Read the Hello frame and verified StartedAt against meta.StartedAt.
// 4. Read the PTYRingDump frame.
//
// NewControllerFromReattach takes ownership of Conn from this point;
// further reads belong to the IPC layer.
type ReattachOptions struct {
	Spec          cwtypes.Spec
	Conn          net.Conn // post-handshake; controller wraps in ipc.Conn
	InitialBuffer []byte   // ring buffer dump from agent
	RuntimeDir    string
	PersistentDir string
	OnLogChunk    func(stream uint8, data []byte)
}

// NewControllerFromReattach constructs a Controller around a connection
// to an already-running persistent agent. Returns the Controller in
// StateRunning with the initial PTY buffer queued for the next
// SubscribePTYData subscriber.
//
// CW-G4 Task 14.
func NewControllerFromReattach(opts ReattachOptions) (*Controller, error) {
	if opts.Conn == nil {
		return nil, errors.New("controller: Conn required")
	}
	if opts.RuntimeDir == "" {
		return nil, errors.New("controller: RuntimeDir is required")
	}

	c := &Controller{
		opts: ControllerOptions{
			Spec:          opts.Spec,
			RuntimeDir:    opts.RuntimeDir,
			PersistentDir: opts.PersistentDir,
			OnLogChunk:    opts.OnLogChunk,
		},
	}

	// Reattach skips the spawn + capability negotiation — the agent has
	// already advertised features and is past handshake. We assume the
	// agent advertises "persistence" (otherwise it wouldn't have a sock).
	// "pty" is also implied if Spec.PTY is non-nil (the original Spawn
	// would have refused otherwise).
	c.features = []string{"pty", "persistence"}
	c.capReplyCh = make(chan []string, 1)
	close(c.capReplyCh)

	conn, err := ipc.NewConn(ipc.ConnConfig{
		RWC:        opts.Conn,
		SpillerDir: filepath.Join(opts.RuntimeDir, "outbox-reattach-"+opts.Spec.ID),
		Capacity:   1024,
		WALBytes:   256 * 1024 * 1024,
	})
	if err != nil {
		return nil, fmt.Errorf("controller reattach conn: %w", err)
	}
	c.conn = conn

	if len(opts.InitialBuffer) > 0 {
		c.SetInitialPTYBuffer(opts.InitialBuffer)
	}

	conn.OnMessage(c.handleMessage)
	conn.SetOnDisconnect(func(_ error) {
		c.crashSource.Store(int32(CrashSourceConnectionLost))
		c.state.Store(int32(cwtypes.StateCrashed))
	})
	conn.Start()

	c.state.Store(int32(cwtypes.StateRunning))
	return c, nil
}

// ReadHandshakeHello reads exactly one MsgHello frame from conn and decodes
// the HelloPayload. Used by Manager.Reattach (CW-G4) to verify StartedAt
// before constructing a Controller.
func ReadHandshakeHello(conn net.Conn) (ipc.HelloPayload, error) {
	hdr, body, err := readOneFrame(conn)
	if err != nil {
		return ipc.HelloPayload{}, err
	}
	if hdr.MsgType != ipc.MsgHello {
		return ipc.HelloPayload{}, fmt.Errorf("controller reattach: expected MsgHello, got %v", hdr.MsgType)
	}
	var p ipc.HelloPayload
	if err := ipc.DecodePayload(body, &p); err != nil {
		return p, fmt.Errorf("controller reattach: decode hello: %w", err)
	}
	return p, nil
}

// ReadHandshakePTYRingDump reads exactly one MsgTypePTYRingDump frame and
// decodes the PTYRingDumpPayload. Used by Manager.Reattach (CW-G4) after
// ReadHandshakeHello.
func ReadHandshakePTYRingDump(conn net.Conn) (ipc.PTYRingDumpPayload, error) {
	hdr, body, err := readOneFrame(conn)
	if err != nil {
		return ipc.PTYRingDumpPayload{}, err
	}
	if hdr.MsgType != ipc.MsgTypePTYRingDump {
		return ipc.PTYRingDumpPayload{}, fmt.Errorf("controller reattach: expected MsgTypePTYRingDump, got %v", hdr.MsgType)
	}
	var p ipc.PTYRingDumpPayload
	if err := ipc.DecodePayload(body, &p); err != nil {
		return p, fmt.Errorf("controller reattach: decode ring dump: %w", err)
	}
	return p, nil
}

// readOneFrame reads exactly one IPC frame (header + body) from conn.
func readOneFrame(conn net.Conn) (ipc.Header, []byte, error) {
	headerBuf := make([]byte, ipc.HeaderSize)
	if _, err := io.ReadFull(conn, headerBuf); err != nil {
		return ipc.Header{}, nil, fmt.Errorf("read header: %w", err)
	}
	var hdr ipc.Header
	if err := hdr.Decode(headerBuf); err != nil {
		return hdr, nil, fmt.Errorf("decode header: %w", err)
	}
	body := make([]byte, hdr.Length)
	if _, err := io.ReadFull(conn, body); err != nil {
		return hdr, nil, fmt.Errorf("read body: %w", err)
	}
	return hdr, body, nil
}
