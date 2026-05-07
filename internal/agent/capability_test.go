// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgent_ReplyContainsPTYFeature(t *testing.T) {
	reply := BuildCapabilityReply()
	require.Contains(t, reply.Features, "pty")
}

func TestAgent_ReplyContainsPersistenceFeature(t *testing.T) {
	reply := BuildCapabilityReply()
	require.Contains(t, reply.Features, "persistence")
}
