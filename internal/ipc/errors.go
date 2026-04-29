// SPDX-License-Identifier: Apache-2.0

package ipc

import "errors"

var (
	// ErrUnknownMsgType is returned (or simulated) when a peer receives a MsgType
	// it does not recognise. In production an older agent simply ignores unknown
	// types; this sentinel lets test code assert the "no-capability" path without
	// changing the wire format.
	ErrUnknownMsgType = errors.New("ipc: unknown message type")

	// ErrAgentDisconnectedEOF is returned when the agent connection closes with a
	// clean EOF (expected end-of-stream from the peer).
	ErrAgentDisconnectedEOF = errors.New("ipc: agent disconnected (EOF)")

	// ErrAgentDisconnectedUnexpected is returned when the agent connection closes
	// for an unexpected reason (e.g. mid-message drop, pipe reset).
	ErrAgentDisconnectedUnexpected = errors.New("ipc: agent disconnected (unexpected)")
)
