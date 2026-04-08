package eventbus

import (
	"sync"

	"github.com/0xmhha/cli-wrapper/pkg/event"
)

// SlowPolicy controls how the bus reacts to slow subscribers.
type SlowPolicy struct {
	// DropThreshold is the number of consecutive dropped events after
	// which the subscription is forcibly closed. Zero means "never close".
	DropThreshold int
}

// Bus is the default in-memory implementation of event.Bus.
// It is safe for concurrent use.
type Bus struct {
	mu     sync.RWMutex
	subs   map[*subscription]struct{}
	closed bool
	policy SlowPolicy
	chCap  int
}

// New returns a Bus with the default slow-subscriber policy.
// chCap is the per-subscription channel capacity.
func New(chCap int) *Bus {
	return NewWithPolicy(chCap, SlowPolicy{DropThreshold: 10})
}

// NewWithPolicy returns a Bus with the given policy.
func NewWithPolicy(chCap int, p SlowPolicy) *Bus {
	if chCap < 1 {
		chCap = 1
	}
	return &Bus{
		subs:   make(map[*subscription]struct{}),
		policy: p,
		chCap:  chCap,
	}
}

// Publish delivers e to every matching subscription. Non-blocking.
func (b *Bus) Publish(e event.Event) {
	b.mu.RLock()
	subs := make([]*subscription, 0, len(b.subs))
	for s := range b.subs {
		subs = append(subs, s)
	}
	closed := b.closed
	b.mu.RUnlock()
	if closed {
		return
	}

	for _, s := range subs {
		if !s.filter.Matches(e) {
			continue
		}
		select {
		case s.ch <- e:
			s.resetDrops()
		default:
			s.recordDrop()
			if b.policy.DropThreshold > 0 && s.drops() >= b.policy.DropThreshold {
				b.disconnect(s)
			}
		}
	}
}

// Subscribe creates a new subscription.
func (b *Bus) Subscribe(filter event.Filter) event.Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &subscription{
		ch:     make(chan event.Event, b.chCap),
		filter: filter,
		bus:    b,
	}
	if b.closed {
		close(s.ch)
		return s
	}
	b.subs[s] = struct{}{}
	return s
}

// Close disconnects every subscription. Idempotent.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for s := range b.subs {
		s.closeChannel()
		delete(b.subs, s)
	}
}

func (b *Bus) disconnect(s *subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[s]; !ok {
		return
	}
	delete(b.subs, s)
	s.closeChannel()
}

type subscription struct {
	mu       sync.Mutex
	ch       chan event.Event
	filter   event.Filter
	bus      *Bus
	dropCnt  int
	chClosed bool
}

func (s *subscription) Events() <-chan event.Event { return s.ch }

func (s *subscription) Close() error {
	s.bus.disconnect(s)
	return nil
}

func (s *subscription) recordDrop() {
	s.mu.Lock()
	s.dropCnt++
	s.mu.Unlock()
}

func (s *subscription) resetDrops() {
	s.mu.Lock()
	s.dropCnt = 0
	s.mu.Unlock()
}

func (s *subscription) drops() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropCnt
}

func (s *subscription) closeChannel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chClosed {
		return
	}
	close(s.ch)
	s.chClosed = true
}
