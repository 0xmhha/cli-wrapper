// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

// TestSetState_FiresCallbackOnTransition verifies that registered
// OnStateChange callbacks see every state transition exactly once.
// Same-value setState calls are filtered out so hosts that only care
// about distinct transitions don't see noise.
func TestSetState_FiresCallbackOnTransition(t *testing.T) {
	c := &Controller{}
	c.state.Store(int32(cwtypes.StatePending))

	var mu sync.Mutex
	var seen []cwtypes.State
	c.SetOnStateChange(func(s cwtypes.State) {
		mu.Lock()
		seen = append(seen, s)
		mu.Unlock()
	})

	c.setState(cwtypes.StateStarting)
	c.setState(cwtypes.StateRunning)
	c.setState(cwtypes.StateRunning) // duplicate — must be filtered
	c.setState(cwtypes.StateStopping)
	c.setState(cwtypes.StateStopped)

	mu.Lock()
	got := append([]cwtypes.State(nil), seen...)
	mu.Unlock()

	require.Equal(t, []cwtypes.State{
		cwtypes.StateStarting,
		cwtypes.StateRunning,
		cwtypes.StateStopping,
		cwtypes.StateStopped,
	}, got, "callback must fire on each distinct transition; duplicates filtered")
}

// TestSetState_NilCallbackIsNoop verifies that setState is safe to call
// before any callback is registered (the construction path uses this:
// NewController calls setState(StatePending) and the field is nil).
func TestSetState_NilCallbackIsNoop(t *testing.T) {
	c := &Controller{}
	c.setState(cwtypes.StatePending)
	c.setState(cwtypes.StateRunning)
	require.Equal(t, cwtypes.StateRunning, c.State())
}

// TestSetOnStateChange_ReplacesPriorCallback verifies that registering
// a new callback atomically replaces the previous one — old callback
// stops firing immediately, new one starts. Pass nil to disable.
func TestSetOnStateChange_ReplacesPriorCallback(t *testing.T) {
	c := &Controller{}
	c.state.Store(int32(cwtypes.StatePending))

	var oldCount, newCount int
	c.SetOnStateChange(func(_ cwtypes.State) { oldCount++ })
	c.setState(cwtypes.StateStarting) // oldCount = 1

	c.SetOnStateChange(func(_ cwtypes.State) { newCount++ })
	c.setState(cwtypes.StateRunning) // newCount = 1

	c.SetOnStateChange(nil)
	c.setState(cwtypes.StateStopped) // neither

	require.Equal(t, 1, oldCount)
	require.Equal(t, 1, newCount)
}
