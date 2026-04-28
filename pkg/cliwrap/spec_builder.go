// SPDX-License-Identifier: Apache-2.0

package cliwrap

import "time"

// SpecBuilder is a fluent constructor for Spec. Errors surface only from Build.
type SpecBuilder struct {
	spec Spec
}

// NewSpec starts a new SpecBuilder for the given ID, command, and arguments.
func NewSpec(id, cmd string, args ...string) *SpecBuilder {
	return &SpecBuilder{spec: Spec{
		ID:             id,
		Command:        cmd,
		Args:           append([]string(nil), args...),
		Env:            map[string]string{},
		StopTimeout:    10 * time.Second,
		RestartBackoff: time.Second,
	}}
}

// WithEnv sets a single environment variable.
func (b *SpecBuilder) WithEnv(k, v string) *SpecBuilder {
	if b.spec.Env == nil {
		b.spec.Env = map[string]string{}
	}
	b.spec.Env[k] = v
	return b
}

// WithWorkDir sets the working directory for the child process.
func (b *SpecBuilder) WithWorkDir(path string) *SpecBuilder {
	b.spec.WorkDir = path
	return b
}

// WithRestart sets the restart policy.
func (b *SpecBuilder) WithRestart(p RestartPolicy) *SpecBuilder {
	b.spec.Restart = p
	return b
}

// WithMaxRestarts limits how many restarts will be attempted.
func (b *SpecBuilder) WithMaxRestarts(n int) *SpecBuilder {
	b.spec.MaxRestarts = n
	return b
}

// WithRestartBackoff sets the initial backoff delay between restarts.
func (b *SpecBuilder) WithRestartBackoff(d time.Duration) *SpecBuilder {
	b.spec.RestartBackoff = d
	return b
}

// WithStopTimeout sets the SIGTERM → SIGKILL grace period.
func (b *SpecBuilder) WithStopTimeout(d time.Duration) *SpecBuilder {
	b.spec.StopTimeout = d
	return b
}

// WithStdin selects how the child's stdin is wired.
func (b *SpecBuilder) WithStdin(m StdinMode) *SpecBuilder {
	b.spec.Stdin = m
	return b
}

// WithResourceLimits attaches resource caps.
func (b *SpecBuilder) WithResourceLimits(l ResourceLimits) *SpecBuilder {
	b.spec.Resources = l
	return b
}

// WithLogOptions attaches log storage options.
func (b *SpecBuilder) WithLogOptions(o LogOptions) *SpecBuilder {
	b.spec.LogOptions = o
	return b
}

// WithSandbox attaches a sandbox spec.
func (b *SpecBuilder) WithSandbox(s *SandboxSpec) *SpecBuilder {
	b.spec.Sandbox = s
	return b
}

// WithPTY attaches a PTY configuration. If cfg is nil the field is cleared.
// Call ApplyPTYDefaults on the returned Spec (or let Controller.Start do it)
// to fill in zero-valued cols/rows.
func (b *SpecBuilder) WithPTY(cfg *PTYConfig) *SpecBuilder {
	b.spec.PTY = cfg
	return b
}

// Build validates and returns the Spec.
func (b *SpecBuilder) Build() (Spec, error) {
	if err := b.spec.Validate(); err != nil {
		return Spec{}, err
	}
	return b.spec, nil
}
