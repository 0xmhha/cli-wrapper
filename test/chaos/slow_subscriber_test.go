package chaos

import (
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/cli-wrapper/cli-wrapper/internal/eventbus"
	"github.com/cli-wrapper/cli-wrapper/pkg/event"
)

func TestChaos_SlowSubscriberIsDisconnected(t *testing.T) {
	defer goleak.VerifyNone(t)

	b := eventbus.NewWithPolicy(4, eventbus.SlowPolicy{DropThreshold: 5})
	defer b.Close()

	sub := b.Subscribe(event.Filter{})
	// Deliberately never read from sub.Events() to simulate a slow consumer.

	for i := 0; i < 50; i++ {
		b.Publish(event.NewProcessStopped("p1", time.Now(), 0))
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-sub.Events():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("slow subscriber was never closed")
		}
	}
}
