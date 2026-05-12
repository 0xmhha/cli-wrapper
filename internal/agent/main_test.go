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

func TestDefaultConfig_OutboxCapacityEnvOverride(t *testing.T) {
	cases := []struct {
		name string
		envv string
		want int
	}{
		{"unset stays at default 1024", "", 1024},
		{"valid positive integer overrides", "4096", 4096},
		{"empty string treated as unset", "", 1024},
		{"zero rejected, keeps default", "0", 1024},
		{"negative rejected, keeps default", "-1", 1024},
		{"non-numeric rejected, keeps default", "lots", 1024},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLIWRAP_AGENT_OUTBOX_CAPACITY", tc.envv)
			cfg := DefaultConfig()
			require.Equal(t, tc.want, cfg.OutboxCapacity)
		})
	}
}
