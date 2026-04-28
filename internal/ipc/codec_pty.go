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

func EncodePTYData(d PTYData) ([]byte, error)   { return msgpack.Marshal(d) }
func DecodePTYData(b []byte) (PTYData, error)   { var d PTYData; return d, msgpack.Unmarshal(b, &d) }
func EncodePTYWrite(d PTYWrite) ([]byte, error)  { return msgpack.Marshal(d) }
func DecodePTYWrite(b []byte) (PTYWrite, error)  { var d PTYWrite; return d, msgpack.Unmarshal(b, &d) }
func EncodePTYResize(d PTYResize) ([]byte, error) { return msgpack.Marshal(d) }
func DecodePTYResize(b []byte) (PTYResize, error) {
	var d PTYResize
	return d, msgpack.Unmarshal(b, &d)
}
func EncodePTYSignal(d PTYSignal) ([]byte, error) { return msgpack.Marshal(d) }
func DecodePTYSignal(b []byte) (PTYSignal, error) {
	var d PTYSignal
	return d, msgpack.Unmarshal(b, &d)
}
func EncodeCapabilityReply(r CapabilityReply) ([]byte, error) { return msgpack.Marshal(r) }
func DecodeCapabilityReply(b []byte) (CapabilityReply, error) {
	var r CapabilityReply
	return r, msgpack.Unmarshal(b, &r)
}
