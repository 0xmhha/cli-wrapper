// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSpec_Validate_OK(t *testing.T) {
	s := Spec{
		ID:          "echo",
		Command:     "echo",
		Args:        []string{"hi"},
		Restart:     RestartNever,
		StopTimeout: 1 * time.Second,
	}
	require.NoError(t, s.Validate())
}

func TestSpec_Validate_MissingID(t *testing.T) {
	s := Spec{Command: "x"}
	err := s.Validate()
	require.Error(t, err)
	var ve *SpecValidationError
	require.ErrorAs(t, err, &ve)
	require.Equal(t, "ID", ve.Field)
}

func TestSpec_Validate_BadID(t *testing.T) {
	s := Spec{ID: "bad id!", Command: "x"}
	err := s.Validate()
	require.Error(t, err)
}

func TestSpec_Validate_MissingCommand(t *testing.T) {
	s := Spec{ID: "a"}
	err := s.Validate()
	require.Error(t, err)
}

func TestSpec_PTYDefaultsZero(t *testing.T) {
	s := Spec{Command: "bash"}
	require.Nil(t, s.PTY)
}

func TestPTYConfig_DefaultsApplied(t *testing.T) {
	c := PTYConfig{} // zero
	require.NoError(t, ApplyPTYDefaults(&c))
	require.Equal(t, uint16(80), c.InitialCols)
	require.Equal(t, uint16(24), c.InitialRows)
	require.False(t, c.Echo)
}

func TestPTYConfig_RespectsExplicitValues(t *testing.T) {
	c := PTYConfig{InitialCols: 132, InitialRows: 50, Echo: true}
	require.NoError(t, ApplyPTYDefaults(&c))
	require.Equal(t, uint16(132), c.InitialCols)
	require.Equal(t, uint16(50), c.InitialRows)
	require.True(t, c.Echo)
}

func TestState_String(t *testing.T) {
	cases := []struct {
		s State
		w string
	}{
		{StatePending, "pending"},
		{StateStarting, "starting"},
		{StateRunning, "running"},
		{StateStopping, "stopping"},
		{StateStopped, "stopped"},
		{StateCrashed, "crashed"},
		{StateRestarting, "restarting"},
		{StateFailed, "failed"},
	}
	for _, c := range cases {
		require.Equal(t, c.w, c.s.String())
	}
}
