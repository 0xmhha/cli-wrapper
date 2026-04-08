// SPDX-License-Identifier: Apache-2.0

package ipc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPayloads_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		val  any
		ptr  any
	}{
		{"HelloPayload", HelloPayload{ProtocolVersion: 1, AgentID: "agent-1"}, &HelloPayload{}},
		{"HelloAckPayload", HelloAckPayload{ProtocolVersion: 1, Capabilities: []string{"logs"}}, &HelloAckPayload{}},
		{"StartChildPayload", StartChildPayload{
			Command:     "/bin/echo",
			Args:        []string{"hi"},
			Env:         map[string]string{"X": "1"},
			WorkDir:     "/tmp",
			StdinMode:   1,
			StopTimeout: 5000,
		}, &StartChildPayload{}},
		{"StopChildPayload", StopChildPayload{TimeoutMs: 3000}, &StopChildPayload{}},
		{"SignalChildPayload", SignalChildPayload{Signal: 15}, &SignalChildPayload{}},
		{"WriteStdinPayload", WriteStdinPayload{Data: []byte("input")}, &WriteStdinPayload{}},
		{"SetLogLevelPayload", SetLogLevelPayload{Level: 2}, &SetLogLevelPayload{}},
		{"ChildStartedPayload", ChildStartedPayload{PID: 1234, StartedAt: 1_700_000_000_000_000_000}, &ChildStartedPayload{}},
		{"ChildExitedPayload", ChildExitedPayload{
			PID: 1234, ExitCode: 0, Signal: 0, ExitedAt: 1_700_000_000, Reason: "ok",
		}, &ChildExitedPayload{}},
		{"LogChunkPayload", LogChunkPayload{Stream: 0, SeqNo: 7, Data: []byte("bytes")}, &LogChunkPayload{}},
		{"LogFileRefPayload", LogFileRefPayload{
			Stream: 1, Path: "/tmp/log.0", Size: 1024, Checksum: "abc",
		}, &LogFileRefPayload{}},
		{"ChildErrorPayload", ChildErrorPayload{Phase: "exec", Message: "not found"}, &ChildErrorPayload{}},
		{"ResourceSamplePayload", ResourceSamplePayload{
			PID: 1234, RSS: 1024 * 1024, CPUPercent: 12.5, Threads: 4, FDs: 16,
		}, &ResourceSamplePayload{}},
		{"AgentFatalPayload", AgentFatalPayload{Reason: "panic", Stack: "..."}, &AgentFatalPayload{}},
		{"AckPayload", AckPayload{AckedSeq: 99}, &AckPayload{}},
		{"ResumePayload", ResumePayload{LastAckedSeq: 500}, &ResumePayload{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := EncodePayload(tc.val)
			require.NoError(t, err)
			require.NoError(t, DecodePayload(data, tc.ptr))
		})
	}
}
