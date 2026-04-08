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
)
