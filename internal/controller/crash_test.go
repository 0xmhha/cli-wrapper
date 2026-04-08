// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCrashInfo_EarliestWins(t *testing.T) {
	var c CrashInfo
	c.Record(CrashSourceExplicit, CrashInfo{
		ChildExit: 42,
		Reason:    "fixture-crasher",
	})
	c.Record(CrashSourceOSWait, CrashInfo{AgentExit: 137, AgentSig: 9})

	require.Equal(t, CrashSourceExplicit, c.Source) // first source wins
	require.Equal(t, 42, c.ChildExit)
	require.Equal(t, "fixture-crasher", c.Reason)
	// Layer 4 (OS wait) information is augmented, not overwritten.
	require.Equal(t, 137, c.AgentExit)
	require.Equal(t, 9, c.AgentSig)
}

func TestCrashInfo_DetectedAtSetOnFirstRecord(t *testing.T) {
	var c CrashInfo
	c.Record(CrashSourceHeartbeat, CrashInfo{})
	first := c.DetectedAt
	time.Sleep(5 * time.Millisecond)
	c.Record(CrashSourceOSWait, CrashInfo{})
	require.Equal(t, first, c.DetectedAt)
}
