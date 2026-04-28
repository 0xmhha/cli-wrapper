// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/internal/ipc"
	"github.com/0xmhha/cli-wrapper/internal/supervise"
)

func TestController_StartAgentAndStop(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)

	spawner := supervise.NewSpawner(supervise.SpawnerOptions{AgentPath: agentBin})

	spec := cwtypes.Spec{
		ID:          "echo-test",
		Command:     "/bin/sh",
		Args:        []string{"-c", "exit 0"},
		StopTimeout: 2 * time.Second,
	}

	ctrl, err := NewController(ControllerOptions{
		Spec:       spec,
		Spawner:    spawner,
		RuntimeDir: t.TempDir(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, ctrl.Start(ctx))

	// Wait for natural exit.
	require.Eventually(t, func() bool {
		return ctrl.State() == cwtypes.StateStopped || ctrl.State() == cwtypes.StateCrashed
	}, 3*time.Second, 20*time.Millisecond)

	require.NoError(t, ctrl.Close(ctx))
}

func TestController_HandleLogChunkInvokesCallback(t *testing.T) {
	var (
		gotStream uint8
		gotData   []byte
		called    int
	)

	c := &Controller{
		opts: ControllerOptions{
			OnLogChunk: func(stream uint8, data []byte) {
				gotStream = stream
				gotData = append(gotData, data...)
				called++
			},
		},
	}

	payload, err := ipc.EncodePayload(ipc.LogChunkPayload{
		Stream: 1,
		SeqNo:  42,
		Data:   []byte("hello stderr"),
	})
	require.NoError(t, err)

	c.handleMessage(ipc.OutboxMessage{
		Header:  ipc.Header{MsgType: ipc.MsgLogChunk},
		Payload: payload,
	})

	require.Equal(t, 1, called)
	require.Equal(t, uint8(1), gotStream)
	require.Equal(t, []byte("hello stderr"), gotData)
}

func TestController_NegotiatesPTY(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)
	spawner := supervise.NewSpawner(supervise.SpawnerOptions{AgentPath: agentBin})

	spec := cwtypes.Spec{
		ID:          "cap-pty-test",
		Command:     "/bin/sh",
		Args:        []string{"-c", "exit 0"},
		StopTimeout: 2 * time.Second,
	}

	ctrl, err := NewController(ControllerOptions{
		Spec:       spec,
		Spawner:    spawner,
		RuntimeDir: t.TempDir(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, ctrl.Start(ctx))
	require.True(t, ctrl.AgentSupportsPTY(), "expected agent to advertise PTY feature")

	require.NoError(t, ctrl.Close(ctx))
}

func TestController_RefusesPTYWhenAgentLacksFeature(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)
	// CLIWRAP_AGENT_NO_CAPABILITY=1 makes the agent skip its CapabilityReply,
	// simulating an older agent that does not understand capability negotiation.
	spawner := supervise.NewSpawner(supervise.SpawnerOptions{
		AgentPath: agentBin,
		ExtraEnv:  []string{"CLIWRAP_AGENT_NO_CAPABILITY=1"},
	})

	spec := cwtypes.Spec{
		ID:          "cap-nopt-test",
		Command:     "/bin/sh",
		Args:        []string{"-c", "exit 0"},
		StopTimeout: 2 * time.Second,
		PTY:         &cwtypes.PTYConfig{InitialCols: 80, InitialRows: 24},
	}

	ctrl, err := NewController(ControllerOptions{
		Spec:       spec,
		Spawner:    spawner,
		RuntimeDir: t.TempDir(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = ctrl.Start(ctx)
	require.Error(t, err)
	require.True(t, errors.Is(err, cwtypes.ErrPTYUnsupportedByAgent),
		"expected ErrPTYUnsupportedByAgent, got: %v", err)

	require.NoError(t, ctrl.Close(ctx))
}

func TestController_HandleLogChunkWithoutCallbackIsNoop(t *testing.T) {
	// No callback set — should not panic.
	c := &Controller{opts: ControllerOptions{}}

	payload, _ := ipc.EncodePayload(ipc.LogChunkPayload{
		Stream: 0,
		Data:   []byte("ignored"),
	})

	require.NotPanics(t, func() {
		c.handleMessage(ipc.OutboxMessage{
			Header:  ipc.Header{MsgType: ipc.MsgLogChunk},
			Payload: payload,
		})
	})
}
