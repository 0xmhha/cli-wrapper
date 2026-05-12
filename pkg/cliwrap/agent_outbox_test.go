// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWithAgentOutboxCapacity_PropagatesViaSpawnerEnv pins that the
// Manager option threads its value into the Spawner's ExtraEnv slice
// at NewManager time. The agent process's DefaultConfig reads
// CLIWRAP_AGENT_OUTBOX_CAPACITY from its environment; this test asserts
// the value lands in the env list that Spawner.Spawn will copy onto
// the child's exec environment.
func TestWithAgentOutboxCapacity_PropagatesViaSpawnerEnv(t *testing.T) {
	cases := []struct {
		name       string
		applyOpt   bool
		value      int
		wantEnvSet bool
		wantEnvVal string
	}{
		{"unset → no env var", false, 0, false, ""},
		{"zero → no env var (legacy default preserved)", true, 0, false, ""},
		{"negative → no env var", true, -1, false, ""},
		{"4096 → env set with stringified value", true, 4096, true, "CLIWRAP_AGENT_OUTBOX_CAPACITY=4096"},
		{"16384 → env set", true, 16384, true, "CLIWRAP_AGENT_OUTBOX_CAPACITY=16384"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := []ManagerOption{
				WithAgentPath("/bin/true"), // any non-empty path; NewManager only checks emptiness
				WithRuntimeDir(t.TempDir()),
			}
			if tc.applyOpt {
				opts = append(opts, WithAgentOutboxCapacity(tc.value))
			}
			mgr, err := NewManager(opts...)
			require.NoError(t, err)

			extraEnv := mgr.spawner.Options().ExtraEnv
			if tc.wantEnvSet {
				require.True(t,
					slices.Contains(extraEnv, tc.wantEnvVal),
					"ExtraEnv %v missing %q", extraEnv, tc.wantEnvVal)
			} else {
				for _, e := range extraEnv {
					require.False(t,
						len(e) >= 30 && e[:30] == "CLIWRAP_AGENT_OUTBOX_CAPACITY=",
						"ExtraEnv unexpectedly contains CLIWRAP_AGENT_OUTBOX_CAPACITY: %q", e)
				}
			}
		})
	}
}
