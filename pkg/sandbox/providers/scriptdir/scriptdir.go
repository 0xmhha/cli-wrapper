// Package scriptdir is a reference sandbox provider that writes scripts into
// a temp directory and runs them via /bin/sh. It does NOT provide isolation
// and exists only to demonstrate the script-injection pattern.
package scriptdir

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/pkg/sandbox"
)

// Options configures a scriptdir.Provider.
type Options struct {
	BaseDir string
}

// Provider is the scriptdir reference provider.
type Provider struct {
	opts Options
}

// New returns a scriptdir provider.
func New(opts Options) *Provider {
	if opts.BaseDir == "" {
		opts.BaseDir = os.TempDir()
	}
	return &Provider{opts: opts}
}

// Name returns "scriptdir".
func (p *Provider) Name() string { return "scriptdir" }

// Prepare creates a temp directory, writes every script into it, and returns
// an Instance whose Exec runs the entrypoint via /bin/sh.
func (p *Provider) Prepare(ctx context.Context, spec cwtypes.SandboxSpec, scripts []sandbox.Script) (sandbox.Instance, error) {
	if err := os.MkdirAll(p.opts.BaseDir, 0o700); err != nil {
		return nil, fmt.Errorf("scriptdir: mkdir base: %w", err)
	}
	dir, err := os.MkdirTemp(p.opts.BaseDir, "scriptdir-*")
	if err != nil {
		return nil, fmt.Errorf("scriptdir: mkdtemp: %w", err)
	}

	var entrypoint string
	for _, s := range scripts {
		if err := s.WriteInto(dir); err != nil {
			_ = os.RemoveAll(dir)
			return nil, err
		}
		if s.InnerPath == "/cliwrap/entrypoint.sh" {
			entrypoint = filepath.Join(dir, "cliwrap/entrypoint.sh")
		}
	}
	if entrypoint == "" {
		_ = os.RemoveAll(dir)
		return nil, errors.New("scriptdir: no entrypoint script provided")
	}

	return &instance{dir: dir, entrypoint: entrypoint}, nil
}

type instance struct {
	dir        string
	entrypoint string
}

func (i *instance) ID() string { return filepath.Base(i.dir) }

func (i *instance) Exec(cmd string, args []string, env map[string]string) (*exec.Cmd, error) {
	// The entrypoint script encodes cmd/args/env; callers ignore its inputs.
	c := exec.Command("/bin/sh", i.entrypoint)
	c.Env = append(os.Environ(), envSlice(env)...)
	return c, nil
}

func (i *instance) Teardown(ctx context.Context) error {
	return os.RemoveAll(i.dir)
}

func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
