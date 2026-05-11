// SPDX-License-Identifier: Apache-2.0

package agent

import "github.com/0xmhha/cli-wrapper/internal/ipc"

// FeaturePTY is the capability token advertised when the agent supports
// PTY-mode child processes.
const FeaturePTY = "pty"

// FeaturePersistence is the capability token advertised when the agent
// supports CW-G4 persistent sessions (--persistent flag, UNIX listener,
// reattach handshake).
const FeaturePersistence = "persistence"

// FeatureFrameAck is the capability token advertised when the agent
// emits MsgAckData in response to any inbound frame whose header has
// FlagAckRequired set. Hosts that detect this bit may use the ack-
// await Conn helpers (SendAndAwaitAck) to turn fire-and-forget control
// messages into synchronous request/response. Without the bit, hosts
// must fall back to the historical fire-and-forget behaviour.
const FeatureFrameAck = "frame_ack"

// BuildCapabilityReply constructs the CapabilityReply the agent sends in
// response to a MsgTypeCapabilityQuery frame.
func BuildCapabilityReply() ipc.CapabilityReply {
	return ipc.CapabilityReply{Features: []string{FeaturePTY, FeaturePersistence, FeatureFrameAck}}
}
