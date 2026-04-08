package event

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A minimal in-package fake, used only to test the types & contract.
type fakeBus struct{ published []Event }

func (f *fakeBus) Publish(e Event) { f.published = append(f.published, e) }
func (f *fakeBus) Subscribe(filter Filter) Subscription {
	ch := make(chan Event, 1)
	return &fakeSub{ch: ch}
}

type fakeSub struct{ ch chan Event }

func (f *fakeSub) Events() <-chan Event { return f.ch }
func (f *fakeSub) Close() error         { close(f.ch); return nil }

func TestBusInterface_AcceptsFakeImplementation(t *testing.T) {
	var b Bus = &fakeBus{}
	b.Publish(NewProcessStopped("p1", time.Now(), 0))
	sub := b.Subscribe(Filter{})
	require.NoError(t, sub.Close())
}

func TestFilter_MatchesAllWhenEmpty(t *testing.T) {
	f := Filter{}
	require.True(t, f.Matches(NewProcessStopped("p1", time.Now(), 0)))
}

func TestFilter_MatchesByType(t *testing.T) {
	f := Filter{Types: []Type{TypeProcessStopped}}
	require.True(t, f.Matches(NewProcessStopped("p1", time.Now(), 0)))
	require.False(t, f.Matches(NewProcessStarted("p1", time.Now(), 1, 2)))
}

func TestFilter_MatchesByProcessID(t *testing.T) {
	f := Filter{ProcessIDs: []string{"p1"}}
	require.True(t, f.Matches(NewProcessStopped("p1", time.Now(), 0)))
	require.False(t, f.Matches(NewProcessStopped("p2", time.Now(), 0)))
}
