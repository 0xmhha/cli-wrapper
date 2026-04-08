// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type sample struct {
	Name  string `msgpack:"n"`
	Value int64  `msgpack:"v"`
}

func TestCodec_RoundTrip(t *testing.T) {
	in := sample{Name: "answer", Value: 42}
	data, err := EncodePayload(in)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	var out sample
	require.NoError(t, DecodePayload(data, &out))
	require.Equal(t, in, out)
}

func TestCodec_DecodeIntoNilReturnsError(t *testing.T) {
	data, err := EncodePayload(sample{})
	require.NoError(t, err)
	require.Error(t, DecodePayload(data, nil))
}

func TestCodec_DecodeGarbageReturnsError(t *testing.T) {
	var out sample
	err := DecodePayload([]byte{0xFF, 0xFF, 0xFF}, &out)
	require.Error(t, err)
}
