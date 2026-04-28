// SPDX-License-Identifier: Apache-2.0

package agent

import "github.com/0xmhha/cli-wrapper/internal/ipc"

// FeaturePTY is the capability token advertised when the agent supports
// PTY-mode child processes.
const FeaturePTY = "pty"

// BuildCapabilityReply constructs the CapabilityReply the agent sends in
// response to a MsgTypeCapabilityQuery frame.
func BuildCapabilityReply() ipc.CapabilityReply {
	return ipc.CapabilityReply{Features: []string{FeaturePTY}}
}
