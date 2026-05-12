// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"github.com/vmihailenco/msgpack/v5"
)

// PTYData carries terminal output from the agent to the host.
type PTYData struct {
	Seq   uint64 `msgpack:"seq"`
	Bytes []byte `msgpack:"bytes"`
}

// PTYWrite carries terminal input from the host to the agent.
type PTYWrite struct {
	Seq   uint64 `msgpack:"seq"`
	Bytes []byte `msgpack:"bytes"`
}

// PTYResize requests a terminal dimension change.
type PTYResize struct {
	Cols uint16 `msgpack:"cols"`
	Rows uint16 `msgpack:"rows"`
}

// PTYSignal requests delivery of a signal to the PTY process group.
type PTYSignal struct {
	Signum int32 `msgpack:"signum"`
}

// CapabilityQuery is sent by the host to request capability negotiation.
// It carries no fields; the MsgType alone identifies it.
type CapabilityQuery struct{}

// CapabilityReply lists the features the agent supports.
type CapabilityReply struct {
	Features []string `msgpack:"features"`
}

func EncodePTYData(d PTYData) ([]byte, error)     { return msgpack.Marshal(d) }
func EncodePTYWrite(d PTYWrite) ([]byte, error)   { return msgpack.Marshal(d) }
func EncodePTYResize(d PTYResize) ([]byte, error) { return msgpack.Marshal(d) }
func EncodePTYSignal(d PTYSignal) ([]byte, error) { return msgpack.Marshal(d) }
func EncodeCapabilityReply(r CapabilityReply) ([]byte, error) {
	return msgpack.Marshal(r)
}

func DecodePTYData(b []byte) (PTYData, error) {
	var d PTYData
	err := msgpack.Unmarshal(b, &d)
	return d, err
}
func DecodePTYWrite(b []byte) (PTYWrite, error) {
	var d PTYWrite
	err := msgpack.Unmarshal(b, &d)
	return d, err
}
func DecodePTYResize(b []byte) (PTYResize, error) {
	var d PTYResize
	err := msgpack.Unmarshal(b, &d)
	return d, err
}
func DecodePTYSignal(b []byte) (PTYSignal, error) {
	var d PTYSignal
	err := msgpack.Unmarshal(b, &d)
	return d, err
}
func DecodeCapabilityReply(b []byte) (CapabilityReply, error) {
	var r CapabilityReply
	err := msgpack.Unmarshal(b, &r)
	return r, err
}
