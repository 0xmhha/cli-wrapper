// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"
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

func TestDispatcher_StartChildInstallsLogChunkSinks(t *testing.T) {
	// We can't easily spin up a real Conn in a unit test, but we can
	// observe the effect by calling startChild on a dispatcher whose
	// runner is a spy. After startChild runs (and before the fake child
	// exits), runner.StdoutSink and runner.StderrSink must be non-nil
	// chunkWriter instances.
	d := &Dispatcher{runner: NewRunner()}

	// Before startChild: defaults (io.Discard) are set by NewRunner.
	require.NotNil(t, d.runner.StdoutSink)
	require.NotNil(t, d.runner.StderrSink)

	// Call the helper that installs the sinks (extracted for testability).
	d.installLogSinks(&recordingSender{})

	_, okOut := d.runner.StdoutSink.(*chunkWriter)
	require.True(t, okOut, "StdoutSink should be a *chunkWriter after install")

	_, okErr := d.runner.StderrSink.(*chunkWriter)
	require.True(t, okErr, "StderrSink should be a *chunkWriter after install")
}

func TestDispatcher_DrainWaitsForActiveRunners(t *testing.T) {
	defer goleak.VerifyNone(t)

	d := NewDispatcher(nil) // nil conn ok for this test; no IPC traffic

	var releases []func()
	for i := 0; i < 3; i++ {
		release := d.markRunnerActive()
		releases = append(releases, release)
	}

	drainDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		drainDone <- d.Drain(ctx)
	}()

	select {
	case err := <-drainDone:
		t.Fatalf("Drain returned before runners released: err=%v", err)
	case <-time.After(50 * time.Millisecond):
	}

	for _, r := range releases {
		r()
	}

	select {
	case err := <-drainDone:
		if err != nil {
			t.Fatalf("Drain returned err: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("Drain did not return after runners released")
	}
}

func TestDispatcher_DrainRespectsContextDeadline(t *testing.T) {
	defer goleak.VerifyNone(t)

	d := NewDispatcher(nil)
	release := d.markRunnerActive()
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := d.Drain(ctx)
	if err == nil {
		t.Fatalf("Drain returned nil; expected deadline-exceeded error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain err = %v; expected DeadlineExceeded", err)
	}
}

func TestDispatcher_MarkRunnerActiveReleaseIsIdempotent(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := NewDispatcher(nil)
	release := d.markRunnerActive()
	release()
	release() // must not panic or double-decrement
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := d.Drain(ctx); err != nil {
		t.Fatalf("Drain after idempotent release: %v", err)
	}
}
