// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManager_LogsSnapshotRoundTrip(t *testing.T) {
	m := &Manager{}

	// Nothing written yet: snapshot for unknown id/stream is nil.
	require.Nil(t, m.LogsSnapshot("redis", 0))

	// Simulate inbound log chunks as if the controller callback fired.
	m.emitLogChunk("redis", 0, []byte("line1\n"))
	m.emitLogChunk("redis", 0, []byte("line2\n"))
	m.emitLogChunk("redis", 1, []byte("err1\n"))

	stdout := m.LogsSnapshot("redis", 0)
	require.Equal(t, "line1\nline2\n", string(stdout))

	stderr := m.LogsSnapshot("redis", 1)
	require.Equal(t, "err1\n", string(stderr))

	// Unrelated id remains empty.
	require.Nil(t, m.LogsSnapshot("other", 0))
}

func TestManager_LogsSnapshotIsolatesProcesses(t *testing.T) {
	m := &Manager{}

	m.emitLogChunk("a", 0, []byte("A-out"))
	m.emitLogChunk("b", 0, []byte("B-out"))

	require.Equal(t, []byte("A-out"), m.LogsSnapshot("a", 0))
	require.Equal(t, []byte("B-out"), m.LogsSnapshot("b", 0))
}
