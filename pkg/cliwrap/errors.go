// SPDX-License-Identifier: Apache-2.0

package cliwrap

import (
	"errors"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

// Sentinel errors.
var (
	ErrProcessNotFound         = errors.New("cliwrap: process not found")
	ErrProcessAlreadyExists    = errors.New("cliwrap: process already exists")
	ErrProcessNotRunning       = errors.New("cliwrap: process not running")
	ErrProcessAlreadyRunning   = errors.New("cliwrap: process already running")
	ErrManagerShuttingDown     = errors.New("cliwrap: manager is shutting down")
	ErrManagerClosed           = errors.New("cliwrap: manager is closed")
	ErrSandboxProviderNotFound = errors.New("cliwrap: sandbox provider not registered")
	ErrAgentSpawnFailed        = errors.New("cliwrap: failed to spawn agent")
	ErrHandshakeTimeout        = errors.New("cliwrap: handshake timeout")
	ErrStopTimeout             = errors.New("cliwrap: stop timeout")

	// ErrInvalidSpec is re-exported from cwtypes so that
	// SpecValidationError.Is(ErrInvalidSpec) works across both packages.
	ErrInvalidSpec = cwtypes.ErrInvalidSpec

	// PTY-related errors.
	// ErrPTYUnsupportedByAgent is re-exported from cwtypes so that
	// errors.Is checks work across both packages.
	ErrPTYUnsupportedByAgent = cwtypes.ErrPTYUnsupportedByAgent
	ErrPTYNotConfigured      = errors.New("cliwrap: PTY operations require Spec.PTY to be set")

	// CW-G4 persistent-session error sentinels.

	// ErrSessionNotFound is returned by Reattach when the session ID has
	// no metadata directory under WithPersistentDir.
	ErrSessionNotFound = errors.New("cliwrap: persistent session not found")

	// ErrSessionAlreadyExists is returned by Spawn (Persistent=true) when
	// a session with the same ID already exists in WithPersistentDir.
	// The user must terminate or rm the existing session first; cli-wrapper
	// does not auto-clean stale dirs.
	ErrSessionAlreadyExists = errors.New("cliwrap: persistent session already exists")

	// ErrAgentDead is returned by Reattach when the pid file points to a
	// dead PID, OR when the agent's MsgHello.startedAt does not match the
	// recorded meta.json startedAt (PID rollover defense).
	ErrAgentDead = errors.New("cliwrap: persistent agent is no longer alive")

	// ErrAlreadyAttached is returned by Reattach when another host is
	// currently connected to the session.
	ErrAlreadyAttached = errors.New("cliwrap: persistent session already attached by another host")

	// ErrIncompatibleAgent is returned by Reattach when the agent's
	// capability version does not match the host's expectations.
	ErrIncompatibleAgent = errors.New("cliwrap: incompatible agent version")

	// ErrPersistenceUnsupportedByAgent is returned at Start time when the
	// caller passes Persistent=true but the agent did not advertise the
	// "persistence" capability.
	ErrPersistenceUnsupportedByAgent = errors.New("cliwrap: agent does not support persistent sessions")
)
