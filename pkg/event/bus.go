// SPDX-License-Identifier: Apache-2.0

package event

// Bus is the publish/subscribe API for cliwrap events.
//
// Publish is non-blocking. Subscribe returns a Subscription whose Events
// channel the caller must either drain or Close. Slow subscribers are
// disconnected after repeated drops by the underlying implementation.
type Bus interface {
	Publish(e Event)
	Subscribe(filter Filter) Subscription
}

// Subscription represents one active subscription. Close releases the
// underlying channel and goroutine references.
type Subscription interface {
	Events() <-chan Event
	Close() error
}

// Filter is applied by the bus to decide which events a subscriber
// receives. Empty slices mean "any" — a zero-value Filter matches all
// events.
type Filter struct {
	Types      []Type
	ProcessIDs []string
}

// Matches reports whether e satisfies the filter.
func (f Filter) Matches(e Event) bool {
	if len(f.Types) > 0 {
		found := false
		for _, t := range f.Types {
			if t == e.EventType() {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(f.ProcessIDs) > 0 {
		found := false
		for _, id := range f.ProcessIDs {
			if id == e.ProcessID() {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
