// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDisconnectErrors_Distinct(t *testing.T) {
	require.NotEqual(t, ErrAgentDisconnectedEOF, ErrAgentDisconnectedUnexpected)
	require.NotEmpty(t, ErrAgentDisconnectedEOF.Error())
	require.NotEmpty(t, ErrAgentDisconnectedUnexpected.Error())
}
