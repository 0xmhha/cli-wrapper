// SPDX-License-Identifier: Apache-2.0

package mgmt

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/internal/ipc"
)

func TestProto_MessageTypesAreUnique(t *testing.T) {
	seen := map[ipc.MsgType]struct{}{}
	types := []ipc.MsgType{
		MsgListRequest, MsgListResponse,
		MsgStatusRequest, MsgStatusResponse,
		MsgStartRequest, MsgStopRequest,
		MsgLogsRequest, MsgLogsStream,
		MsgEventsSubscribe, MsgEventsStream,
	}
	for _, mt := range types {
		_, dup := seen[mt]
		require.False(t, dup, "duplicate type: %d", mt)
		seen[mt] = struct{}{}
	}
}

func TestProto_PayloadRoundTrip(t *testing.T) {
	in := ListResponsePayload{
		Entries: []ListEntry{
			{ID: "p1", Name: "proc 1", State: "running", ChildPID: 1000},
		},
	}
	data, err := ipc.EncodePayload(in)
	require.NoError(t, err)
	var out ListResponsePayload
	require.NoError(t, ipc.DecodePayload(data, &out))
	require.Equal(t, in, out)
}
