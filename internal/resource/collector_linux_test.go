// SPDX-License-Identifier: Apache-2.0

//go:build linux

package resource

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessCollectorLinux_SelfPID(t *testing.T) {
	pc := DefaultProcessCollector()
	s, err := pc.Collect(os.Getpid())
	require.NoError(t, err)
	require.Greater(t, s.RSS, uint64(0))
	require.Greater(t, s.NumThreads, 0)
}

func TestSystemCollectorLinux_ReadsMeminfo(t *testing.T) {
	sc := DefaultSystemCollector()
	s, err := sc.Collect()
	require.NoError(t, err)
	require.Greater(t, s.TotalMemory, uint64(0))
	require.GreaterOrEqual(t, s.NumCPU, 1)
}
