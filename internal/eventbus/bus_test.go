// SPDX-License-Identifier: Apache-2.0

package eventbus

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/pkg/event"
)

func TestBus_PublishAndReceive(t *testing.T) {
	defer goleak.VerifyNone(t)

	b := New(64)
	defer b.Close()

	sub := b.Subscribe(event.Filter{})
	defer sub.Close()

	b.Publish(event.NewProcessStopped("p1", time.Now(), 0))

	select {
	case ev := <-sub.Events():
		require.Equal(t, event.TypeProcessStopped, ev.EventType())
	case <-time.After(time.Second):
		t.Fatal("event not received")
	}
}

func TestBus_FilterByType(t *testing.T) {
	defer goleak.VerifyNone(t)

	b := New(64)
	defer b.Close()

	sub := b.Subscribe(event.Filter{Types: []event.Type{event.TypeProcessCrashed}})
	defer sub.Close()

	b.Publish(event.NewProcessStopped("p1", time.Now(), 0)) // should be filtered
	b.Publish(event.NewProcessCrashed("p1", time.Now(), event.CrashContext{Reason: "x"}, false, 0))

	select {
	case ev := <-sub.Events():
		require.Equal(t, event.TypeProcessCrashed, ev.EventType())
	case <-time.After(time.Second):
		t.Fatal("event not received")
	}

	select {
	case ev := <-sub.Events():
		t.Fatalf("unexpected second event: %v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestBus_SlowSubscriberIsDropped(t *testing.T) {
	defer goleak.VerifyNone(t)

	b := NewWithPolicy(4, SlowPolicy{DropThreshold: 3})
	defer b.Close()

	sub := b.Subscribe(event.Filter{})
	// Do not drain.

	// Publish many events to exceed both channel capacity and drop threshold.
	for i := 0; i < 20; i++ {
		b.Publish(event.NewProcessStopped("p1", time.Now(), 0))
	}

	// Subscription should eventually close itself.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-sub.Events():
			if !ok {
				return // channel closed, success
			}
		case <-deadline:
			t.Fatal("slow subscriber was not disconnected")
		}
	}
}
