// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/internal/ipc"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// ptyDataSender is a test double for chunkSender that captures MsgTypePTYData frames.
type ptyDataSender struct {
	mu   sync.Mutex
	msgs []rawMsg
}

type rawMsg struct {
	msgType ipc.MsgType
	payload []byte
}

func (s *ptyDataSender) SendControl(t ipc.MsgType, payload any, _ bool) error {
	if encoded, ok := payload.([]byte); ok {
		s.mu.Lock()
		s.msgs = append(s.msgs, rawMsg{msgType: t, payload: encoded})
		s.mu.Unlock()
	}
	return nil
}

func (s *ptyDataSender) waitForPTYData(timeout time.Duration) (ipc.PTYData, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		for _, m := range s.msgs {
			if m.msgType == ipc.MsgTypePTYData {
				d, err := ipc.DecodePTYData(m.payload)
				s.mu.Unlock()
				if err == nil {
					return d, true
				}
				return ipc.PTYData{}, false
			}
		}
		s.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	return ipc.PTYData{}, false
}

func TestSpawnPTY_BashEcho(t *testing.T) {
	defer goleak.VerifyNone(t)

	spec := RunSpec{
		Command: "/bin/bash",
		Args:    []string{"-c", "echo hello && exit 0"},
		PTY:     &cwtypes.PTYConfig{InitialCols: 80, InitialRows: 24},
	}
	p, err := spawnPTY(context.Background(), spec)
	require.NoError(t, err)
	defer func() { _ = p.Close() }()

	var sink bytes.Buffer
	var mu sync.Mutex
	p.OnData(func(b []byte) {
		mu.Lock()
		sink.Write(b) //nolint:errcheck // bytes.Buffer.Write never fails
		mu.Unlock()
	})
	p.startReadPump()

	waitForPTYExit(t, p, 3*time.Second)

	mu.Lock()
	defer mu.Unlock()
	require.Contains(t, sink.String(), "hello")
}

// waitForPTYExit blocks until p.Done() is closed or the deadline is exceeded.
func waitForPTYExit(t *testing.T, p *ptyProc, timeout time.Duration) {
	t.Helper()
	select {
	case <-p.Done():
	case <-time.After(timeout):
		t.Fatal("ptyProc did not exit within deadline")
	}
}

func TestRunner_SendsMsgPTYData(t *testing.T) {
	defer goleak.VerifyNone(t)

	sender := &ptyDataSender{}
	r := NewRunner()
	r.SetSender(sender)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := r.Run(ctx, RunSpec{
		Command: "/bin/echo",
		Args:    []string{"pty-output-token"},
		PTY:     &cwtypes.PTYConfig{InitialCols: 80, InitialRows: 24},
	})
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)

	data, ok := sender.waitForPTYData(2 * time.Second)
	require.True(t, ok, "expected at least one MsgTypePTYData frame")
	require.Contains(t, string(data.Bytes), "pty-output-token")
}
