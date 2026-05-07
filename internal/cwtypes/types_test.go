// SPDX-License-Identifier: Apache-2.0

package cwtypes

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

func TestSpec_Validate_PersistentDefaults(t *testing.T) {
	s := Spec{ID: "test", Command: "/bin/cat"}
	require.NoError(t, s.Validate(), "default Spec should validate")
	require.False(t, s.Persistent, "Persistent should default false")
	require.Equal(t, 0, s.RingBufferSize, "RingBufferSize should default zero (resolved at agent startup)")
}

func TestSpec_Validate_RingBufferSizeRejectsNegative(t *testing.T) {
	s := Spec{ID: "test", Command: "/bin/cat", Persistent: true, RingBufferSize: -1}
	err := s.Validate()
	require.Error(t, err, "expected error for negative RingBufferSize")
	require.Contains(t, err.Error(), "RingBufferSize")
}

func TestSpec_Validate_RingBufferSizeRejectsOversize(t *testing.T) {
	s := Spec{ID: "test", Command: "/bin/cat", Persistent: true, RingBufferSize: 65 * 1024 * 1024}
	err := s.Validate()
	require.Error(t, err, "expected error for RingBufferSize > 64 MiB cap")
	require.Contains(t, err.Error(), "RingBufferSize")
}

func TestSpec_Validate_RingBufferSizeIgnoredWhenNotPersistent(t *testing.T) {
	// Pathological RingBufferSize is OK as long as Persistent=false.
	s := Spec{ID: "test", Command: "/bin/cat", Persistent: false, RingBufferSize: -1}
	require.NoError(t, s.Validate(), "RingBufferSize should be ignored when Persistent=false")
}
