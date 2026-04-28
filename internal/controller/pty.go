// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"sync"

	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

// PTYData carries one chunk of PTY output delivered to a subscriber.
type PTYData struct {
	Seq   uint64
	Bytes []byte
}

// ptySubscribers manages the fan-out of inbound MsgTypePTYData frames.
//
// Each call to SubscribePTYData allocates a buffered channel and registers it.
// The controller's handleMessage loop decodes each PTYData frame and calls
// fanOutPTYData, which delivers a copy to every registered subscriber.
// Unsubscribing removes the channel from the list and closes it, which
// causes any goroutine blocked on the channel to detect closure and exit.
type ptySubscribers struct {
	mu   sync.Mutex
	list []*ptySubscriber
}

type ptySubscriber struct {
	ch chan PTYData
}

func (ps *ptySubscribers) add() *ptySubscriber {
	s := &ptySubscriber{ch: make(chan PTYData, 256)}
	ps.mu.Lock()
	ps.list = append(ps.list, s)
	ps.mu.Unlock()
	return s
}

func (ps *ptySubscribers) remove(target *ptySubscriber) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for i, s := range ps.list {
		if s == target {
			ps.list = append(ps.list[:i], ps.list[i+1:]...)
			close(s.ch)
			return
		}
	}
}

func (ps *ptySubscribers) fanOut(d PTYData) {
	ps.mu.Lock()
	// Snapshot under lock to avoid holding it during channel sends.
	subs := make([]*ptySubscriber, len(ps.list))
	copy(subs, ps.list)
	ps.mu.Unlock()

	for _, s := range subs {
		// Non-blocking: a full channel drops the chunk for this subscriber.
		select {
		case s.ch <- d:
		default:
		}
	}
}

// SendPTY sends a raw PTY control frame to the agent. payload must already
// be encoded (e.g. via ipc.EncodePTYWrite). The context is present for
// interface symmetry and future flow-control; the send is currently
// non-blocking relative to ctx.
func (c *Controller) SendPTY(_ context.Context, msgType ipc.MsgType, payload []byte) error {
	h := ipc.Header{
		MsgType: msgType,
		SeqNo:   c.conn.Seqs().Next(),
		Length:  uint32(len(payload)),
	}
	c.conn.Send(ipc.OutboxMessage{Header: h, Payload: payload})
	return nil
}

// SubscribePTYData registers a new PTY data subscriber. The returned channel
// receives PTYData values for every inbound MsgTypePTYData frame. The caller
// MUST invoke the returned unsubscribe function exactly once when done,
// typically via defer. Failing to call it leaks the channel.
//
// If the controller is not configured for PTY mode the caller should guard
// with Spec.PTY != nil before calling this method. SubscribePTYData itself
// does not validate the spec; it simply registers a receiver.
func (c *Controller) SubscribePTYData() (<-chan PTYData, func()) {
	s := c.ptySubs.add()
	return s.ch, func() { c.ptySubs.remove(s) }
}
