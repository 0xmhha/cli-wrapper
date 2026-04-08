// SPDX-License-Identifier: Apache-2.0

// Package sandbox defines the plugin interface for isolating managed CLI processes.
package sandbox

import (
	"context"
	"os/exec"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

// Provider prepares isolated environments in which child CLI processes can run.
// Implementations MUST be safe for concurrent use.
type Provider interface {
	// Name identifies the provider; must be unique at the Manager level.
	Name() string

	// Prepare creates a new isolated environment and returns an Instance.
	// The Instance is owned by the caller, which must call Teardown after use.
	Prepare(ctx context.Context, spec cwtypes.SandboxSpec, scripts []Script) (Instance, error)
}

// Instance is a prepared sandbox that can execute exactly one CLI invocation.
type Instance interface {
	// ID identifies this instance (provider-defined).
	ID() string

	// Exec returns an exec.Cmd that, when run OUTSIDE the sandbox by the agent,
	// launches cmd/args inside the isolated environment.
	Exec(cmd string, args []string, env map[string]string) (*exec.Cmd, error)

	// Teardown releases resources associated with this instance.
	Teardown(ctx context.Context) error
}
