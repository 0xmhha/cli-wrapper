// SPDX-License-Identifier: Apache-2.0

// Package mgmt defines the control-socket protocol between the cliwrap
// management CLI and a running Manager.
package mgmt

import "github.com/0xmhha/cli-wrapper/internal/ipc"

// Management-specific message types. They reuse the ipc framing layer but
// use distinct numeric IDs in the 0xA0..0xAF range.
const (
	MsgListRequest     ipc.MsgType = 0xA0
	MsgListResponse    ipc.MsgType = 0xA1
	MsgStatusRequest   ipc.MsgType = 0xA2
	MsgStatusResponse  ipc.MsgType = 0xA3
	MsgLogsRequest     ipc.MsgType = 0xA4
	MsgLogsStream      ipc.MsgType = 0xA5
	MsgEventsSubscribe ipc.MsgType = 0xA6
	MsgEventsStream    ipc.MsgType = 0xA7
	MsgStartRequest    ipc.MsgType = 0xA8
	MsgStopRequest     ipc.MsgType = 0xA9
)

// ListEntry is one row in a list response.
type ListEntry struct {
	ID       string `msgpack:"id"`
	Name     string `msgpack:"name"`
	State    string `msgpack:"state"`
	ChildPID int    `msgpack:"pid"`
	RSS      uint64 `msgpack:"rss"`
}

// ListResponsePayload is returned for MsgListResponse.
type ListResponsePayload struct {
	Entries []ListEntry `msgpack:"entries"`
}

// StatusRequestPayload asks for a specific process's status.
type StatusRequestPayload struct {
	ID string `msgpack:"id"`
}

// StatusResponsePayload summarizes one process.
type StatusResponsePayload struct {
	Entry ListEntry `msgpack:"entry"`
	Err   string    `msgpack:"err"`
}

// StartRequestPayload requests that a registered process be started.
type StartRequestPayload struct {
	ID string `msgpack:"id"`
}

// StopRequestPayload requests that a running process be stopped.
type StopRequestPayload struct {
	ID string `msgpack:"id"`
}

// LogsRequestPayload asks for the ring-buffer snapshot of a process.
type LogsRequestPayload struct {
	ID     string `msgpack:"id"`
	Stream uint8  `msgpack:"s"`
}

// LogsStreamPayload carries a single chunk of streamed log bytes.
type LogsStreamPayload struct {
	ID     string `msgpack:"id"`
	Stream uint8  `msgpack:"s"`
	Data   []byte `msgpack:"d"`
	EOF    bool   `msgpack:"eof"`
}

// EventsSubscribePayload is sent by a client to subscribe to a stream of
// lifecycle events. An empty ProcessIDs slice subscribes to every process.
type EventsSubscribePayload struct {
	ProcessIDs []string `msgpack:"pids"`
}

// EventsStreamPayload carries a serialized event summary.
type EventsStreamPayload struct {
	ProcessID string `msgpack:"pid"`
	Type      string `msgpack:"t"`
	Timestamp int64  `msgpack:"ts"`
	Summary   string `msgpack:"sum"`
}
