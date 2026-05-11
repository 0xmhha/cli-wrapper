// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

func TestCrashInfo_EarliestWins(t *testing.T) {
	var c CrashInfo
	c.Record(CrashSourceExplicit, CrashInfo{
		ChildExit: 42,
		Reason:    "fixture-crasher",
	})
	c.Record(CrashSourceOSWait, CrashInfo{AgentExit: 137, AgentSig: 9})

	require.Equal(t, CrashSourceExplicit, c.Source) // first source wins
	require.Equal(t, 42, c.ChildExit)
	require.Equal(t, "fixture-crasher", c.Reason)
	// Layer 4 (OS wait) information is augmented, not overwritten.
	require.Equal(t, 137, c.AgentExit)
	require.Equal(t, 9, c.AgentSig)
}

func TestCrashInfo_DetectedAtSetOnFirstRecord(t *testing.T) {
	var c CrashInfo
	c.Record(CrashSourceHeartbeat, CrashInfo{})
	first := c.DetectedAt
	time.Sleep(5 * time.Millisecond)
	c.Record(CrashSourceOSWait, CrashInfo{})
	require.Equal(t, first, c.DetectedAt)
}

// TestController_TransientChildErrorDoesNotCrash pins the contract that
// transient ChildError messages (agent-side RPC failures while the child
// is still alive) do NOT flip the controller into StateCrashed. Hosts
// listening via OnStateChange would otherwise see a spurious
// Running → Crashed → Running sawtooth on every malformed pty_resize,
// dropped pty_write, etc.
func TestController_TransientChildErrorDoesNotCrash(t *testing.T) {
	c := &Controller{}
	c.setState(cwtypes.StateRunning)

	payload, err := ipc.EncodePayload(ipc.ChildErrorPayload{
		Phase:     "pty_resize",
		Message:   "no active PTY",
		Transient: true,
	})
	require.NoError(t, err)

	c.handleMessage(ipc.OutboxMessage{
		Header:  ipc.Header{MsgType: ipc.MsgChildError},
		Payload: payload,
	})

	require.Equal(t, cwtypes.StateRunning, c.State(),
		"transient ChildError must leave state unchanged")
}

// TestController_FatalChildErrorTransitionsToCrashed guards the inverse:
// payloads with Transient=false (or with the field omitted, e.g. from a
// pre-0.4.3 agent) MUST still flip the controller into StateCrashed. The
// zero-value semantics carry the backward-compat guarantee.
func TestController_FatalChildErrorTransitionsToCrashed(t *testing.T) {
	c := &Controller{}
	c.setState(cwtypes.StateRunning)

	// Encode without setting Transient — zero value means "fatal" so this
	// also exercises the wire-compat path where an old agent omits the
	// field entirely.
	payload, err := ipc.EncodePayload(ipc.ChildErrorPayload{
		Phase:   "exec",
		Message: "process spawn failed: no such file",
	})
	require.NoError(t, err)

	c.handleMessage(ipc.OutboxMessage{
		Header:  ipc.Header{MsgType: ipc.MsgChildError},
		Payload: payload,
	})

	require.Equal(t, cwtypes.StateCrashed, c.State(),
		"non-transient ChildError must transition to Crashed")
}
