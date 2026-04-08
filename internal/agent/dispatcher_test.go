// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

func TestDispatcher_HandlesStartChildAndExit(t *testing.T) {
	defer goleak.VerifyNone(t)

	a, b := net.Pipe()
	dirA := t.TempDir()
	dirB := t.TempDir()

	agentConn, err := ipc.NewConn(ipc.ConnConfig{RWC: a, SpillerDir: dirA, Capacity: 64, WALBytes: 1 << 20})
	require.NoError(t, err)
	hostConn, err := ipc.NewConn(ipc.ConnConfig{RWC: b, SpillerDir: dirB, Capacity: 64, WALBytes: 1 << 20})
	require.NoError(t, err)

	d := NewDispatcher(agentConn)
	agentConn.OnMessage(d.Handle)
	agentConn.Start()

	received := make(chan ipc.OutboxMessage, 4)
	hostConn.OnMessage(func(m ipc.OutboxMessage) { received <- m })
	hostConn.Start()

	// Host sends START_CHILD for /bin/sh -c 'exit 7'.
	sc := ipc.StartChildPayload{
		Command: "/bin/sh",
		Args:    []string{"-c", "exit 7"},
	}
	data, err := ipc.EncodePayload(sc)
	require.NoError(t, err)
	hostConn.Send(ipc.OutboxMessage{
		Header:  ipc.Header{MsgType: ipc.MsgStartChild, SeqNo: 1, Length: uint32(len(data))},
		Payload: data,
	})

	// Expect CHILD_STARTED then CHILD_EXITED.
	var startedSeen, exitedSeen bool
	var exitedPayload ipc.ChildExitedPayload
	timeout := time.After(3 * time.Second)
	for !exitedSeen {
		select {
		case m := <-received:
			switch m.Header.MsgType {
			case ipc.MsgChildStarted:
				startedSeen = true
			case ipc.MsgChildExited:
				exitedSeen = true
				require.NoError(t, ipc.DecodePayload(m.Payload, &exitedPayload))
			}
		case <-timeout:
			t.Fatalf("timed out waiting for child lifecycle events (started=%v exited=%v)", startedSeen, exitedSeen)
		}
	}
	require.True(t, startedSeen)
	require.Equal(t, int32(7), exitedPayload.ExitCode)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = agentConn.Close(ctx)
	_ = hostConn.Close(ctx)
}
