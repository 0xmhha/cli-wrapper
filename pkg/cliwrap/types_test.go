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
