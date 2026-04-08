// SPDX-License-Identifier: Apache-2.0

// Package cwtypes contains the shared type definitions used by both
// pkg/cliwrap (the public API) and internal/controller. Placing them in
// a leaf package avoids an import cycle.
package cwtypes

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// RestartPolicy decides what happens after a child exits.
type RestartPolicy int

const (
	RestartNever RestartPolicy = iota
	RestartOnFailure
	RestartAlways
)

// ExceedAction is the action taken when a resource limit is breached.
type ExceedAction int

const (
	ExceedWarn ExceedAction = iota
	ExceedThrottle
	ExceedKill
)

// StdinMode controls how the agent connects the child's stdin.
type StdinMode int

const (
	StdinNone    StdinMode = iota
	StdinPipe              // host may call ProcessHandle.SendStdin
	StdinInherit           // inherit agent's stdin (rarely useful)
)

// State is the runtime state of a managed process.
type State int

const (
	StatePending State = iota
	StateStarting
	StateRunning
	StateStopping
	StateStopped
	StateCrashed
	StateRestarting
	StateFailed
)

// String returns the lowercase name of a State.
func (s State) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	case StateStopped:
		return "stopped"
	case StateCrashed:
		return "crashed"
	case StateRestarting:
		return "restarting"
	case StateFailed:
		return "failed"
	default:
		return fmt.Sprintf("state(%d)", int(s))
	}
}

// ResourceLimits express per-process resource caps.
type ResourceLimits struct {
	MaxRSS        uint64  // bytes
	MaxCPUPercent float64 // 0-100
	OnExceed      ExceedAction
}

// LogOptions controls per-process log storage.
type LogOptions struct {
	RingBufferBytes       int
	RotationDir           string
	RotationMaxSize       int64
	RotationMaxFiles      int
	PreserveWALOnShutdown bool
}

// FileRotationOptions is an alias kept for backward compatibility.
type FileRotationOptions = LogOptions

// SandboxSpec selects a sandbox provider and its configuration.
type SandboxSpec struct {
	Provider string
	Config   map[string]any
}

// Spec is the immutable description of a managed process.
type Spec struct {
	ID             string
	Name           string
	Command        string
	Args           []string
	Env            map[string]string
	WorkDir        string
	Restart        RestartPolicy
	MaxRestarts    int
	RestartBackoff time.Duration
	StopTimeout    time.Duration
	Stdin          StdinMode
	Sandbox        *SandboxSpec
	Resources      ResourceLimits
	LogOptions     LogOptions
}

// Status is a snapshot of the runtime state.
type Status struct {
	State         State
	AgentPID      int
	ChildPID      int
	StartedAt     time.Time
	LastExitAt    time.Time
	LastExitCode  int
	RestartCount  int
	LastError     string
	PendingReason string
	RSS           uint64
	CPUPercent    float64
}

var validID = regexp.MustCompile(`^[A-Za-z0-9_\-]+$`)

// Validate returns an error if s is missing required fields or has bad values.
func (s *Spec) Validate() error {
	if s.ID == "" {
		return &SpecValidationError{Field: "ID", Reason: "required"}
	}
	if !validID.MatchString(s.ID) {
		return &SpecValidationError{Field: "ID", Value: s.ID, Reason: "must match [A-Za-z0-9_-]+"}
	}
	if s.Command == "" {
		return &SpecValidationError{Field: "Command", Reason: "required"}
	}
	if s.StopTimeout < 0 {
		return &SpecValidationError{Field: "StopTimeout", Value: s.StopTimeout, Reason: "must be >= 0"}
	}
	if s.MaxRestarts < 0 {
		return &SpecValidationError{Field: "MaxRestarts", Value: s.MaxRestarts, Reason: "must be >= 0"}
	}
	return nil
}

// ErrInvalidSpec is the sentinel that SpecValidationError.Is maps to.
var ErrInvalidSpec = errors.New("cliwrap: invalid spec")

// SpecValidationError describes a field-level validation failure.
type SpecValidationError struct {
	Field  string
	Value  any
	Reason string
}

func (e *SpecValidationError) Error() string {
	if e.Value != nil {
		return fmt.Sprintf("cliwrap: invalid spec field %q (value=%v): %s", e.Field, e.Value, e.Reason)
	}
	return fmt.Sprintf("cliwrap: invalid spec field %q: %s", e.Field, e.Reason)
}

func (e *SpecValidationError) Is(target error) bool {
	return target == ErrInvalidSpec
}
