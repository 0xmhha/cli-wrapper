// SPDX-License-Identifier: Apache-2.0

//go:build darwin

package resource

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessCollectorDarwin_SelfPID(t *testing.T) {
	pc := DefaultProcessCollector()
	s, err := pc.Collect(os.Getpid())
	require.NoError(t, err)
	require.Greater(t, s.RSS, uint64(0))
}

func TestSystemCollectorDarwin_SysctlMemory(t *testing.T) {
	sc := DefaultSystemCollector()
	s, err := sc.Collect()
	require.NoError(t, err)
	require.Greater(t, s.TotalMemory, uint64(0))
}
