// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSpecBuilder_BuildsValidSpec(t *testing.T) {
	s, err := NewSpec("echo", "echo", "hi", "world").
		WithEnv("X", "1").
		WithWorkDir("/tmp").
		WithRestart(RestartOnFailure).
		WithMaxRestarts(5).
		WithRestartBackoff(2 * time.Second).
		WithStopTimeout(10 * time.Second).
		WithResourceLimits(ResourceLimits{MaxRSS: 1 << 20, OnExceed: ExceedKill}).
		Build()

	require.NoError(t, err)
	require.Equal(t, "echo", s.ID)
	require.Equal(t, []string{"hi", "world"}, s.Args)
	require.Equal(t, map[string]string{"X": "1"}, s.Env)
	require.Equal(t, RestartOnFailure, s.Restart)
	require.Equal(t, 5, s.MaxRestarts)
}

func TestSpecBuilder_FailsOnInvalidID(t *testing.T) {
	_, err := NewSpec("", "echo").Build()
	require.Error(t, err)
}
