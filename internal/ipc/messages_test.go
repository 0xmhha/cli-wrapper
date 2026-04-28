// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewMessageTypeIDs_RespectDirectionConvention(t *testing.T) {
	// host→agent: low half (< 0x80)
	require.Equal(t, MsgType(0x20), MsgTypePTYWrite)
	require.Equal(t, MsgType(0x22), MsgTypePTYResize)
	require.Equal(t, MsgType(0x23), MsgTypePTYSignal)
	require.Equal(t, MsgType(0x30), MsgTypeCapabilityQuery)
	// agent→host: high half (>= 0x80)
	require.Equal(t, MsgType(0x90), MsgTypePTYData)
	require.Equal(t, MsgType(0xB0), MsgTypeCapabilityReply)

	// Ensure no collision with existing IDs
	used := map[MsgType]bool{}
	for _, id := range AllMessageTypes() {
		require.False(t, used[id], "duplicate message type %d", id)
		used[id] = true
	}
}
