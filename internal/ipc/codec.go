package ipc

import (
	"errors"

	"github.com/vmihailenco/msgpack/v5"
)

// ErrNilDecodeTarget is returned by DecodePayload when v is nil.
var ErrNilDecodeTarget = errors.New("ipc: decode target is nil")

// EncodePayload serializes v to MessagePack bytes.
func EncodePayload(v any) ([]byte, error) {
	return msgpack.Marshal(v)
}

// DecodePayload deserializes MessagePack bytes into v. v must be a non-nil
// pointer to the destination struct.
func DecodePayload(data []byte, v any) error {
	if v == nil {
		return ErrNilDecodeTarget
	}
	return msgpack.Unmarshal(data, v)
}
