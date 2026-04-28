// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	require.Equal(t, 3, cfg.IPCFD)
	require.Greater(t, cfg.OutboxCapacity, 0)
}
