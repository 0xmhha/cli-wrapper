// Package cliwrap re-exports the canonical types from internal/cwtypes
// via Go type aliases, so the public API surface is unchanged.
package cliwrap

import (
	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

// Type aliases — these are transparent to callers. Users continue to
// write cliwrap.State, cliwrap.Spec, etc.
type (
	State               = cwtypes.State
	RestartPolicy       = cwtypes.RestartPolicy
	ExceedAction        = cwtypes.ExceedAction
	StdinMode           = cwtypes.StdinMode
	ResourceLimits      = cwtypes.ResourceLimits
	LogOptions          = cwtypes.LogOptions
	SandboxSpec         = cwtypes.SandboxSpec
	Spec                = cwtypes.Spec
	Status              = cwtypes.Status
	SpecValidationError = cwtypes.SpecValidationError
)

// Re-exported constants.
const (
	RestartNever     = cwtypes.RestartNever
	RestartOnFailure = cwtypes.RestartOnFailure
	RestartAlways    = cwtypes.RestartAlways

	ExceedWarn     = cwtypes.ExceedWarn
	ExceedThrottle = cwtypes.ExceedThrottle
	ExceedKill     = cwtypes.ExceedKill

	StdinNone    = cwtypes.StdinNone
	StdinPipe    = cwtypes.StdinPipe
	StdinInherit = cwtypes.StdinInherit

	StatePending    = cwtypes.StatePending
	StateStarting   = cwtypes.StateStarting
	StateRunning    = cwtypes.StateRunning
	StateStopping   = cwtypes.StateStopping
	StateStopped    = cwtypes.StateStopped
	StateCrashed    = cwtypes.StateCrashed
	StateRestarting = cwtypes.StateRestarting
	StateFailed     = cwtypes.StateFailed
)
