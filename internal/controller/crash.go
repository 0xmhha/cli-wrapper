// SPDX-License-Identifier: Apache-2.0

package controller

import "time"

// CrashSource identifies which of the 4 crash-detection layers fired first.
type CrashSource int

const (
	CrashSourceNone CrashSource = iota
	CrashSourceExplicit
	CrashSourceConnectionLost
	CrashSourceHeartbeat
	CrashSourceOSWait
)

// String returns the lowercase name of the crash source.
func (s CrashSource) String() string {
	switch s {
	case CrashSourceExplicit:
		return "explicit"
	case CrashSourceConnectionLost:
		return "connection_lost"
	case CrashSourceHeartbeat:
		return "heartbeat"
	case CrashSourceOSWait:
		return "os_wait"
	default:
		return "none"
	}
}

// CrashInfo unifies information from all four crash-detection layers.
// The earliest-arriving source wins for the "primary" fields; Layer 4
// (OS wait) augments but does not overwrite.
type CrashInfo struct {
	Source     CrashSource
	AgentExit  int
	AgentSig   int
	ChildExit  int
	ChildSig   int
	Reason     string
	DetectedAt time.Time
}

// Record merges data from a detection source.
// The first call wins for Source/Reason/ChildExit/ChildSig; OS wait data is
// always merged in.
func (c *CrashInfo) Record(src CrashSource, data CrashInfo) {
	if c.Source == CrashSourceNone {
		c.Source = src
		c.DetectedAt = time.Now()
		c.Reason = data.Reason
		c.ChildExit = data.ChildExit
		c.ChildSig = data.ChildSig
	}
	// OS wait fields are always merged.
	if src == CrashSourceOSWait || c.AgentExit == 0 {
		if data.AgentExit != 0 || data.AgentSig != 0 {
			c.AgentExit = data.AgentExit
			c.AgentSig = data.AgentSig
		}
	}
}
