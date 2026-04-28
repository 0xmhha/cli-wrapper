// SPDX-License-Identifier: Apache-2.0

package ipc

import "errors"

// ErrUnknownMsgType is returned (or simulated) when a peer receives a MsgType
// it does not recognise. In production an older agent simply ignores unknown
// types; this sentinel lets test code assert the "no-capability" path without
// changing the wire format.
var ErrUnknownMsgType = errors.New("ipc: unknown message type")
