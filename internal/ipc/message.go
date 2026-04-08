// SPDX-License-Identifier: Apache-2.0

package ipc

// --- Control plane (host -> agent) ---

// HelloPayload is the handshake initiator.
type HelloPayload struct {
	ProtocolVersion uint8  `msgpack:"v"`
	AgentID         string `msgpack:"id"`
}

// StartChildPayload instructs the agent to fork/exec the target CLI.
type StartChildPayload struct {
	Command     string            `msgpack:"cmd"`
	Args        []string          `msgpack:"args"`
	Env         map[string]string `msgpack:"env"`
	WorkDir     string            `msgpack:"wd"`
	StdinMode   uint8             `msgpack:"stdin"` // 0=none, 1=pipe, 2=inherit
	StopTimeout int64             `msgpack:"stop_to_ms"`
}

// StopChildPayload requests graceful termination of the child.
type StopChildPayload struct {
	TimeoutMs int64 `msgpack:"to_ms"`
}

// SignalChildPayload asks the agent to deliver a specific signal.
type SignalChildPayload struct {
	Signal int32 `msgpack:"sig"`
}

// WriteStdinPayload forwards bytes to the child's stdin.
type WriteStdinPayload struct {
	Data []byte `msgpack:"d"`
}

// SetLogLevelPayload toggles the agent's log verbosity.
type SetLogLevelPayload struct {
	Level uint8 `msgpack:"lvl"` // matches DebugMode
}

// --- Data plane (agent -> host) ---

// HelloAckPayload is the agent's handshake response.
type HelloAckPayload struct {
	ProtocolVersion uint8    `msgpack:"v"`
	Capabilities    []string `msgpack:"caps"`
}

// ChildStartedPayload signals that exec succeeded.
type ChildStartedPayload struct {
	PID       int32 `msgpack:"pid"`
	StartedAt int64 `msgpack:"at"`
}

// ChildExitedPayload signals that the child process has terminated.
type ChildExitedPayload struct {
	PID      int32  `msgpack:"pid"`
	ExitCode int32  `msgpack:"code"`
	Signal   int32  `msgpack:"sig"`
	ExitedAt int64  `msgpack:"at"`
	Reason   string `msgpack:"reason"`
}

// LogChunkPayload carries a stdout/stderr fragment inline.
// Stream: 0=stdout, 1=stderr, 2=diag (debug mode only)
type LogChunkPayload struct {
	Stream uint8  `msgpack:"s"`
	SeqNo  uint64 `msgpack:"n"`
	Data   []byte `msgpack:"d"`
}

// LogFileRefPayload references an out-of-band log file on disk.
type LogFileRefPayload struct {
	Stream   uint8  `msgpack:"s"`
	Path     string `msgpack:"p"`
	Size     uint64 `msgpack:"sz"`
	Checksum string `msgpack:"ck"`
}

// ChildErrorPayload reports an exec/runtime error from inside the agent.
type ChildErrorPayload struct {
	Phase   string `msgpack:"phase"`
	Message string `msgpack:"msg"`
}

// ResourceSamplePayload carries a periodic resource sample.
type ResourceSamplePayload struct {
	PID        int32   `msgpack:"pid"`
	RSS        uint64  `msgpack:"rss"`
	CPUPercent float64 `msgpack:"cpu"`
	Threads    int32   `msgpack:"thr"`
	FDs        int32   `msgpack:"fds"`
}

// AgentFatalPayload announces imminent agent termination.
type AgentFatalPayload struct {
	Reason string `msgpack:"reason"`
	Stack  string `msgpack:"stack"`
}

// --- Shared ---

// AckPayload acknowledges a specific sequence number.
type AckPayload struct {
	AckedSeq uint64 `msgpack:"ack"`
}

// ResumePayload exchanges last-seen sequence numbers on reconnect.
type ResumePayload struct {
	LastAckedSeq uint64 `msgpack:"last"`
}
