package event

import "time"

// Type identifies an event kind.
type Type string

// Defined event types.
const (
	TypeProcessRegistered Type = "process.registered"
	TypeProcessStarting   Type = "process.starting"
	TypeProcessStarted    Type = "process.started"
	TypeProcessStopping   Type = "process.stopping"
	TypeProcessStopped    Type = "process.stopped"
	TypeProcessCrashed    Type = "process.crashed"
	TypeProcessRestarting Type = "process.restarting"
	TypeProcessFailed     Type = "process.failed"

	TypeAgentSpawned      Type = "agent.spawned"
	TypeAgentFatal        Type = "agent.fatal"
	TypeAgentDisconnected Type = "agent.disconnected"

	TypeLogChunk    Type = "log.chunk"
	TypeLogFileRef  Type = "log.file_ref"
	TypeLogOverflow Type = "log.overflow"

	TypeBackpressureStall Type = "reliability.backpressure_stall"

	TypeShutdownRequested  Type = "system.shutdown_requested"
	TypeShutdownIncomplete Type = "system.shutdown_incomplete"
)

// Event is implemented by every concrete event type in this package.
// External packages can only consume events; they cannot implement Event.
type Event interface {
	EventType() Type
	ProcessID() string
	Timestamp() time.Time
	sealed()
}

type baseEvent struct {
	processID string
	ts        time.Time
}

func (e baseEvent) ProcessID() string    { return e.processID }
func (e baseEvent) Timestamp() time.Time { return e.ts }
func (baseEvent) sealed()                {}

// --- Process lifecycle events ---

type ProcessStartingEvent struct{ baseEvent }

func (ProcessStartingEvent) EventType() Type { return TypeProcessStarting }

func NewProcessStarting(id string, at time.Time) ProcessStartingEvent {
	return ProcessStartingEvent{baseEvent: baseEvent{id, at}}
}

type ProcessStartedEvent struct {
	baseEvent
	AgentPID int
	ChildPID int
}

func (ProcessStartedEvent) EventType() Type { return TypeProcessStarted }

func NewProcessStarted(id string, at time.Time, agentPID, childPID int) ProcessStartedEvent {
	return ProcessStartedEvent{baseEvent: baseEvent{id, at}, AgentPID: agentPID, ChildPID: childPID}
}

type ProcessStoppedEvent struct {
	baseEvent
	ExitCode int
}

func (ProcessStoppedEvent) EventType() Type { return TypeProcessStopped }

func NewProcessStopped(id string, at time.Time, exitCode int) ProcessStoppedEvent {
	return ProcessStoppedEvent{baseEvent: baseEvent{id, at}, ExitCode: exitCode}
}

// CrashContext summarizes the unified crash information reported to
// subscribers. It mirrors controller.CrashInfo without creating an
// import cycle.
type CrashContext struct {
	Source    string
	AgentExit int
	AgentSig  int
	ChildExit int
	ChildSig  int
	Reason    string
}

type ProcessCrashedEvent struct {
	baseEvent
	CrashContext  CrashContext
	WillRestart   bool
	NextAttemptIn time.Duration
}

func (ProcessCrashedEvent) EventType() Type { return TypeProcessCrashed }

func NewProcessCrashed(id string, at time.Time, c CrashContext, willRestart bool, backoff time.Duration) ProcessCrashedEvent {
	return ProcessCrashedEvent{
		baseEvent:     baseEvent{id, at},
		CrashContext:  c,
		WillRestart:   willRestart,
		NextAttemptIn: backoff,
	}
}

// --- Agent events ---

type AgentFatalEvent struct {
	baseEvent
	Reason string
}

func (AgentFatalEvent) EventType() Type { return TypeAgentFatal }

func NewAgentFatal(id string, at time.Time, reason string) AgentFatalEvent {
	return AgentFatalEvent{baseEvent: baseEvent{id, at}, Reason: reason}
}

// --- Log events ---

type LogChunkEvent struct {
	baseEvent
	Stream uint8
	Size   int
}

func (LogChunkEvent) EventType() Type { return TypeLogChunk }

func NewLogChunk(id string, at time.Time, stream uint8, size int) LogChunkEvent {
	return LogChunkEvent{baseEvent: baseEvent{id, at}, Stream: stream, Size: size}
}

type LogOverflowEvent struct {
	baseEvent
	Reason string
}

func (LogOverflowEvent) EventType() Type { return TypeLogOverflow }

func NewLogOverflow(id string, at time.Time, reason string) LogOverflowEvent {
	return LogOverflowEvent{baseEvent: baseEvent{id, at}, Reason: reason}
}

// --- Reliability events ---

type BackpressureStallEvent struct {
	baseEvent
	Endpoint    string
	WALSize     uint64
	WALSizeMax  uint64
	DroppedMsgs int
}

func (BackpressureStallEvent) EventType() Type { return TypeBackpressureStall }

func NewBackpressureStall(id string, at time.Time, endpoint string, size, sizeMax uint64, dropped int) BackpressureStallEvent {
	return BackpressureStallEvent{
		baseEvent:   baseEvent{id, at},
		Endpoint:    endpoint,
		WALSize:     size,
		WALSizeMax:  sizeMax,
		DroppedMsgs: dropped,
	}
}
