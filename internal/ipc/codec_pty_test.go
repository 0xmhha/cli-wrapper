// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPTY_DataRoundTrip(t *testing.T) {
	in := PTYData{Seq: 42, Bytes: []byte("hello\n")}
	raw, err := EncodePTYData(in)
	require.NoError(t, err)
	out, err := DecodePTYData(raw)
	require.NoError(t, err)
	require.Equal(t, in, out)
}

func TestPTY_WriteRoundTrip(t *testing.T) {
	in := PTYWrite{Seq: 7, Bytes: []byte{0x03}}
	raw, _ := EncodePTYWrite(in)
	out, err := DecodePTYWrite(raw)
	require.NoError(t, err)
	require.Equal(t, in, out)
}

func TestPTY_ResizeRoundTrip(t *testing.T) {
	in := PTYResize{Cols: 120, Rows: 40}
	raw, _ := EncodePTYResize(in)
	out, err := DecodePTYResize(raw)
	require.NoError(t, err)
	require.Equal(t, in, out)
}

func TestPTY_SignalRoundTrip(t *testing.T) {
	in := PTYSignal{Signum: 2}
	raw, _ := EncodePTYSignal(in)
	out, err := DecodePTYSignal(raw)
	require.NoError(t, err)
	require.Equal(t, in, out)
}

func TestCapability_RoundTrip(t *testing.T) {
	in := CapabilityReply{Features: []string{"pty", "logs"}}
	raw, _ := EncodeCapabilityReply(in)
	out, err := DecodeCapabilityReply(raw)
	require.NoError(t, err)
	require.Equal(t, in, out)
}
