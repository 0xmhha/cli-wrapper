// Package noop provides a pass-through sandbox provider.
package noop

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"

	"github.com/cli-wrapper/cli-wrapper/internal/cwtypes"
	"github.com/cli-wrapper/cli-wrapper/pkg/sandbox"
)

// Provider is the no-op provider. It performs no isolation and exists only
// to satisfy the interface when no sandbox is requested.
type Provider struct {
	counter atomic.Int64
}

// New returns a Provider.
func New() *Provider { return &Provider{} }

// Name returns "noop".
func (p *Provider) Name() string { return "noop" }

// Prepare returns an Instance that runs commands directly.
func (p *Provider) Prepare(ctx context.Context, spec cwtypes.SandboxSpec, scripts []sandbox.Script) (sandbox.Instance, error) {
	id := "noop-" + strconv.FormatInt(p.counter.Add(1), 10)
	return &instance{id: id}, nil
}

type instance struct {
	id string
}

func (i *instance) ID() string { return i.id }

func (i *instance) Exec(cmd string, args []string, env map[string]string) (*exec.Cmd, error) {
	c := exec.Command(cmd, args...)
	c.Env = append(os.Environ(), envSlice(env)...)
	return c, nil
}

func (i *instance) Teardown(ctx context.Context) error { return nil }

func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
