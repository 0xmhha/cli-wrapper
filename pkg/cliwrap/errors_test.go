// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublicErrors_Distinct(t *testing.T) {
	require.NotEqual(t, ErrPTYUnsupportedByAgent, ErrPTYNotConfigured)
	require.NotEmpty(t, ErrPTYUnsupportedByAgent.Error())
	require.NotEmpty(t, ErrPTYNotConfigured.Error())
}

func TestPersistentErrors_AreDistinctAndNonEmpty(t *testing.T) {
	errs := []error{
		ErrSessionNotFound,
		ErrSessionAlreadyExists,
		ErrAgentDead,
		ErrAlreadyAttached,
		ErrIncompatibleAgent,
		ErrPersistenceUnsupportedByAgent,
	}
	seen := map[string]bool{}
	for _, e := range errs {
		require.NotNil(t, e, "nil error in set")
		msg := e.Error()
		require.NotEmpty(t, msg, "empty Error() string")
		require.False(t, seen[msg], "duplicate error message: %q", msg)
		seen[msg] = true
	}
}
