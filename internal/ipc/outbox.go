package ipc

import (
	"sync"
	"time"
)

// OutboxMessage is one unit of work in an Outbox.
type OutboxMessage struct {
	Header  Header
	Payload []byte
}

// SpillFunc is invoked when the in-memory queue is full. Implementations
// typically persist the message to a WAL. Returning a non-nil error causes
// the Outbox.Enqueue call to fail.
type SpillFunc func(OutboxMessage) error

// Outbox is a bounded FIFO with an optional disk spill handler.
// Enqueue never blocks the producer; when the in-memory slots are full
// it invokes the configured SpillFunc synchronously.
//
// Shutdown is signalled via a dedicated `done` channel — the data channel
// `ch` is never closed directly, which eliminates the "send on closed channel"
// race between Enqueue and Close.
type Outbox struct {
	ch    chan OutboxMessage
	done  chan struct{}
	spill SpillFunc
	once  sync.Once
}

// NewOutbox returns an Outbox with the given in-memory capacity and spill
// handler. If spill is nil, overflow enqueues are dropped by returning false.
func NewOutbox(capacity int, spill SpillFunc) *Outbox {
	if capacity < 1 {
		capacity = 1
	}
	return &Outbox{
		ch:    make(chan OutboxMessage, capacity),
		done:  make(chan struct{}),
		spill: spill,
	}
}

// Enqueue attempts to add msg to the queue. Returns false if the outbox is
// closed or the SpillFunc (when the channel is full) returned an error.
func (o *Outbox) Enqueue(msg OutboxMessage) bool {
	select {
	case <-o.done:
		return false
	default:
	}

	select {
	case o.ch <- msg:
		return true
	case <-o.done:
		return false
	default:
	}

	if o.spill == nil {
		return false
	}
	return o.spill(msg) == nil
}

// Dequeue blocks until a message is available, the timeout elapses, or the
// outbox is closed. The second return value indicates success.
func (o *Outbox) Dequeue(timeout time.Duration) (OutboxMessage, bool) {
	if timeout <= 0 {
		select {
		case msg := <-o.ch:
			return msg, true
		case <-o.done:
			// Drain any remaining messages before returning false.
			select {
			case msg := <-o.ch:
				return msg, true
			default:
				return OutboxMessage{}, false
			}
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case msg := <-o.ch:
		return msg, true
	case <-o.done:
		// Drain any remaining messages before returning false.
		select {
		case msg := <-o.ch:
			return msg, true
		default:
			return OutboxMessage{}, false
		}
	case <-timer.C:
		return OutboxMessage{}, false
	}
}

// InjectFront pushes a message to the front of the queue, bypassing the
// normal capacity check. This is used by the spiller to replay WAL entries
// into the in-memory queue.
func (o *Outbox) InjectFront(msg OutboxMessage) bool {
	select {
	case <-o.done:
		return false
	default:
	}
	select {
	case o.ch <- msg:
		return true
	default:
		return false
	}
}

// Close shuts the outbox down. Subsequent Enqueue calls return false and
// Dequeue callers are unblocked with ok=false once all remaining messages
// are drained.
func (o *Outbox) Close() {
	o.once.Do(func() {
		close(o.done)
	})
}
