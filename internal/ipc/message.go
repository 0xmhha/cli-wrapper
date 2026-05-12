// SPDX-License-Identifier: Apache-2.0

package ipc

// --- Control plane (host -> agent) ---

// HelloPayload is the handshake initiator.
type HelloPayload struct {
	ProtocolVersion uint8  `msgpack:"v"`
	AgentID         string `msgpack:"id"`

	// StartedAt is the agent's startup time in nanoseconds since epoch UTC.
	// Used by Manager.Reattach (CW-G4) to defend against PID rollover —
	// the host compares this value to the recorded meta.json startedAt
	// and returns ErrAgentDead on mismatch.
	//
	// Zero (omitted) is acceptable for backward compatibility with
	// non-persistent agents that never call into Reattach.
	StartedAt int64 `msgpack:"sa,omitempty"`
}

// StartChildPayload instructs the agent to fork/exec the target CLI.
type StartChildPayload struct {
	Command     string            `msgpack:"cmd"`
	Args        []string          `msgpack:"args"`
	Env         map[string]string `msgpack:"env"`
	WorkDir     string            `msgpack:"wd"`
	StdinMode   uint8             `msgpack:"stdin"` // 0=none, 1=pipe, 2=inherit
	StopTimeout int64             `msgpack:"stop_to_ms"`
	PTY         *StartChildPTY    `msgpack:"pty,omitempty"`
}

// StartChildPTY carries PTY configuration within StartChildPayload.
type StartChildPTY struct {
	InitialCols uint16 `msgpack:"cols"`
	InitialRows uint16 `msgpack:"rows"`
	Echo        bool   `msgpack:"echo"`
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
//
// Transient distinguishes "operational RPC failure while the child remains
// alive" (e.g., pty_write on a closed fd, malformed StartChild decode) from
// "the child is dead or never started" (exec failure). The controller uses
// the flag to gate StateCrashed: only non-transient errors actually flip
// state, so a host that observed StateRunning won't see a phantom
// StateRunning → StateCrashed → StateRunning sawtooth caused by a single
// resize-on-closed-fd error.
//
// Zero value (false) intentionally means "fatal" so that pre-0.4.3 agents
// — which omit this field — keep the historical behavior of flipping the
// controller to StateCrashed on every MsgChildError, preserving wire
// compatibility for hosts upgraded to a new cli-wrapper version while
// still running an older agent binary.
type ChildErrorPayload struct {
	Phase     string `msgpack:"phase"`
	Message   string `msgpack:"msg"`
	Transient bool   `msgpack:"transient,omitempty"`
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

// PTYRingDumpPayload carries a one-shot snapshot of the agent's persistent
// PTY ring buffer, sent immediately after MsgHello when a new host attaches
// to a persistent session. The host's controller queues this dump and
// delivers it as the first chunk(s) to the next SubscribePTYData subscriber.
type PTYRingDumpPayload struct {
	Bytes []byte `msgpack:"b"`
}
