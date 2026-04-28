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
