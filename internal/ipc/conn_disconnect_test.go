// SPDX-License-Identifier: Apache-2.0

//go:build !race_only_skip

package ipc

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// pipeConn wraps one end of a net.Pipe() as an io.ReadWriteCloser.
// net.Pipe() is used because its Close() unblocks concurrent Reads, which is
// required for Close() → readLoop exit to work without a raw-fd deadlock.
type pipeConn = net.Conn

// disconnectPair returns two connected net.Pipe() ends.
func disconnectPair() (pipeConn, pipeConn) {
	a, b := net.Pipe()
	return a, b
}

func newDisconnectConn(t *testing.T, rwc io.ReadWriteCloser) *Conn {
	t.Helper()
	conn, err := NewConn(ConnConfig{
		RWC:        rwc,
		SpillerDir: t.TempDir(),
		Capacity:   8,
		WALBytes:   1 << 20,
	})
	require.NoError(t, err)
	return conn
}

func TestConn_OnDisconnect_FiresOnSocketEOF(t *testing.T) {
	a, b := disconnectPair()
	defer func() { _ = b.Close() }()

	conn := newDisconnectConn(t, a)
	var fired int32
	var gotErr error
	var mu sync.Mutex
	conn.SetOnDisconnect(func(err error) {
		atomic.StoreInt32(&fired, 1)
		mu.Lock()
		gotErr = err
		mu.Unlock()
	})
	conn.Start()

	// Trigger EOF on the other side.
	require.NoError(t, b.Close())

	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&fired) == 1
	}, time.Second, 10*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	require.ErrorIs(t, gotErr, ErrAgentDisconnectedEOF)

	// Clean up conn so goleak sees no leaked goroutines.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = conn.Close(ctx)
}

func TestConn_OnDisconnect_DoesNotFireOnNormalClose(t *testing.T) {
	a, b := disconnectPair()
	defer func() { _ = b.Close() }()

	conn := newDisconnectConn(t, a)
	var fired int32
	conn.SetOnDisconnect(func(error) { atomic.StoreInt32(&fired, 1) })
	conn.Start()

	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, conn.Close(ctx))
	time.Sleep(200 * time.Millisecond)

	require.Equal(t, int32(0), atomic.LoadInt32(&fired),
		"OnDisconnect must NOT fire when shutdown is initiated locally")
}

func TestConn_OnDisconnect_FiresAtMostOnce(t *testing.T) {
	a, b := disconnectPair()
	defer func() { _ = b.Close() }()

	conn := newDisconnectConn(t, a)
	var calls int32
	conn.SetOnDisconnect(func(error) { atomic.AddInt32(&calls, 1) })
	conn.Start()

	require.NoError(t, b.Close()) // EOF #1
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = conn.Close(ctx) // would-be second fire path
	time.Sleep(100 * time.Millisecond)

	require.Equal(t, int32(1), atomic.LoadInt32(&calls))
}
