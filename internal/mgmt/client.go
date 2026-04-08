// SPDX-License-Identifier: Apache-2.0

package mgmt

import (
	"fmt"
	"net"

	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

// Client is a synchronous client for the management socket. It is not
// safe for concurrent use - callers should open one per goroutine.
type Client struct {
	conn net.Conn
	r    *ipc.FrameReader
	w    *ipc.FrameWriter
	seq  uint64
}

// Dial opens a connection to the manager at socketPath.
func Dial(socketPath string) (*Client, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("mgmt: dial: %w", err)
	}
	return &Client{
		conn: conn,
		r:    ipc.NewFrameReader(conn, ipc.MaxPayloadSize),
		w:    ipc.NewFrameWriter(conn),
	}, nil
}

// Close releases the connection.
func (c *Client) Close() error { return c.conn.Close() }

// Call sends a request and returns the response header and body.
func (c *Client) Call(t ipc.MsgType, payload any) (ipc.Header, []byte, error) {
	c.seq++
	var data []byte
	if payload != nil {
		b, err := ipc.EncodePayload(payload)
		if err != nil {
			return ipc.Header{}, nil, err
		}
		data = b
	}
	_, err := c.w.WriteFrame(ipc.Header{
		MsgType: t,
		SeqNo:   c.seq,
		Length:  uint32(len(data)),
	}, data)
	if err != nil {
		return ipc.Header{}, nil, err
	}
	return c.r.ReadFrame()
}
