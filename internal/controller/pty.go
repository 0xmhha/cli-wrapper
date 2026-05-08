// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

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
	// Hold the mutex for the full fan-out. Each send is non-blocking (select
	// with default) so lock hold time is O(n) constant-time operations.
	// Holding during sends is required to prevent a concurrent remove from
	// closing a channel while fanOut is sending to it (data race).
	ps.mu.Lock()
	defer ps.mu.Unlock()

	for _, s := range ps.list {
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
	c.conn.SendWithNewSeq(msgType, 0, payload)
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
	dbg := os.Getenv("CLIWRAP_DEBUG") == "1"

	// CW-G4: if a ring buffer dump was received via MsgPTYRingDump (reattach),
	// drain it to THIS subscriber as the first chunk before live data.
	// CompareAndSwap ensures exactly-once delivery across concurrent
	// subscribers — only the first wins.
	if c.initialBufferDrained.CompareAndSwap(false, true) {
		c.mu.Lock()
		buf := c.initialPTYBuffer
		c.initialPTYBuffer = nil
		c.mu.Unlock()
		if dbg {
			fmt.Fprintf(os.Stderr, "CTRL: SubscribePTYData drain initial buffer len=%d\n", len(buf))
		}
		if len(buf) > 0 {
			// Send asynchronously to avoid blocking SubscribePTYData
			// caller. The 5-second timeout drops the dump if the
			// subscriber doesn't drain promptly.
			go func() {
				select {
				case s.ch <- PTYData{Seq: 0, Bytes: buf}:
					if dbg {
						fmt.Fprintf(os.Stderr, "CTRL: initial buffer delivered len=%d\n", len(buf))
					}
				case <-time.After(5 * time.Second):
					if dbg {
						fmt.Fprintf(os.Stderr, "CTRL: initial buffer drop (timeout)\n")
					}
				}
			}()
		}
	} else if dbg {
		fmt.Fprintf(os.Stderr, "CTRL: SubscribePTYData no buffer drain (already drained)\n")
	}

	return s.ch, func() { c.ptySubs.remove(s) }
}
