// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAckTracker_PendingAndRetire(t *testing.T) {
	at := NewAckTracker()

	at.MarkPending(1)
	at.MarkPending(2)
	at.MarkPending(3)

	require.ElementsMatch(t, []uint64{1, 2, 3}, at.Pending())

	at.Ack(2)
	require.ElementsMatch(t, []uint64{1, 3}, at.Pending())
}

func TestAckTracker_AckUnknownIsNoop(t *testing.T) {
	at := NewAckTracker()
	at.MarkPending(5)
	at.Ack(99)
	require.ElementsMatch(t, []uint64{5}, at.Pending())
}

func TestAckTracker_MaxPendingSeq(t *testing.T) {
	at := NewAckTracker()
	at.MarkPending(3)
	at.MarkPending(7)
	at.MarkPending(5)
	require.Equal(t, uint64(7), at.MaxPendingSeq())

	at.Ack(7)
	require.Equal(t, uint64(5), at.MaxPendingSeq())
}
