// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

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
