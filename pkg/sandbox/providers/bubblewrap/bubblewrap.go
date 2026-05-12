// SPDX-License-Identifier: Apache-2.0

// Package bubblewrap is a Linux-only sandbox provider that wraps the
// bubblewrap binary (`bwrap`). It builds user/mount/pid/net namespaces
// per call so a child CLI runs against a read-only root, an isolated
// /tmp, fresh /dev and /proc, and no network unless explicitly allowed.
//
// Configuration is taken from cwtypes.SandboxSpec.Config:
//
//	network    bool       — true to share host network (default: isolated)
//	bind_rw    []string   — extra read-write bind mounts (host==sandbox path)
//	bind_ro    []string   — extra read-only bind mounts beyond the default ro /
//	chdir      string     — working directory inside the sandbox (default: "/")
//
// Linux only: New returns ErrUnsupportedPlatform on other GOOS values.
// `bwrap` must be present on PATH at New() time; otherwise New returns
// ErrBwrapNotFound. Once constructed, Prepare/Instance never re-probe.
package bubblewrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync/atomic"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/pkg/sandbox"
)

// Sentinel errors so callers can distinguish "not Linux" from "bwrap missing".
var (
	ErrUnsupportedPlatform = errors.New("bubblewrap: unsupported platform (Linux only)")
	ErrBwrapNotFound       = errors.New("bubblewrap: bwrap binary not found in PATH")
)

// Options configures a bubblewrap.Provider.
type Options struct {
	// BwrapPath overrides the auto-detected bwrap path. Empty = look up via
	// exec.LookPath at New() time.
	BwrapPath string
}

// Provider implements sandbox.Provider via bubblewrap.
type Provider struct {
	bwrap   string
	counter atomic.Int64
}

// New constructs a Provider. Returns ErrUnsupportedPlatform on non-Linux,
// ErrBwrapNotFound if bwrap cannot be located on PATH.
func New(opts Options) (*Provider, error) {
	if runtime.GOOS != "linux" {
		return nil, ErrUnsupportedPlatform
	}
	bwrap := opts.BwrapPath
	if bwrap == "" {
		p, err := exec.LookPath("bwrap")
		if err != nil {
			return nil, errors.Join(ErrBwrapNotFound, err)
		}
		bwrap = p
	}
	return &Provider{bwrap: bwrap}, nil
}

// Name returns "bubblewrap".
func (p *Provider) Name() string { return "bubblewrap" }

// config holds the parsed values from SandboxSpec.Config.
type config struct {
	network bool
	bindRW  []string
	bindRO  []string
	chdir   string
}

func parseConfig(raw map[string]any) (config, error) {
	var c config
	if v, ok := raw["network"]; ok {
		b, ok := v.(bool)
		if !ok {
			return c, fmt.Errorf("bubblewrap: config.network must be bool, got %T", v)
		}
		c.network = b
	}
	if v, ok := raw["chdir"]; ok {
		s, ok := v.(string)
		if !ok {
			return c, fmt.Errorf("bubblewrap: config.chdir must be string, got %T", v)
		}
		c.chdir = s
	}
	if v, ok := raw["bind_rw"]; ok {
		paths, err := stringSlice(v, "bind_rw")
		if err != nil {
			return c, err
		}
		c.bindRW = paths
	}
	if v, ok := raw["bind_ro"]; ok {
		paths, err := stringSlice(v, "bind_ro")
		if err != nil {
			return c, err
		}
		c.bindRO = paths
	}
	return c, nil
}

func stringSlice(v any, name string) ([]string, error) {
	switch s := v.(type) {
	case []string:
		return s, nil
	case []any:
		out := make([]string, 0, len(s))
		for i, e := range s {
			str, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("bubblewrap: config.%s[%d] must be string, got %T", name, i, e)
			}
			out = append(out, str)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("bubblewrap: config.%s must be []string, got %T", name, v)
	}
}

// buildBwrapArgs assembles the bwrap argv (excluding the leading bwrap binary
// path). Exposed for unit-testing without invoking bwrap.
func buildBwrapArgs(c config) []string {
	args := []string{
		// Always start from a clean namespace; --unshare-all unshares
		// uts/ipc/pid/net/cgroup/user, then --share-net selectively
		// re-joins host network when requested.
		"--unshare-all",
	}
	if c.network {
		args = append(args, "--share-net")
	}
	// Default rootfs: read-only bind of host /. This is the sane default
	// for "run my CLI against the host's filesystem but stop it from
	// modifying anything outside /tmp".
	args = append(args,
		"--ro-bind", "/", "/",
		// Fresh /proc and /dev for the new pid/uts namespaces.
		"--proc", "/proc", "--dev", "/dev",
		// Isolated /tmp so the CLI's scratch files don't pollute the host.
		"--tmpfs", "/tmp",
	)
	// Extra read-only binds (host path == sandbox path).
	for _, p := range c.bindRO {
		args = append(args, "--ro-bind", p, p)
	}
	// Extra read-write binds (host path == sandbox path). Order: after
	// --ro-bind so a writeable bind under a read-only parent wins (bwrap
	// applies binds in argv order).
	for _, p := range c.bindRW {
		args = append(args, "--bind", p, p)
	}
	if c.chdir != "" {
		args = append(args, "--chdir", c.chdir)
	}
	// --die-with-parent ensures bwrap (and the child) terminate when the
	// agent process exits, matching the cleanup contract that ipc/agent
	// already provides for non-sandboxed children.
	args = append(args, "--die-with-parent")
	return args
}

// Prepare returns an Instance that will exec bwrap on first Instance.Exec.
// scripts is presently ignored — bubblewrap composes via argv, not by
// writing entrypoints into a directory like scriptdir.
func (p *Provider) Prepare(ctx context.Context, spec cwtypes.SandboxSpec, scripts []sandbox.Script) (sandbox.Instance, error) {
	cfg, err := parseConfig(spec.Config)
	if err != nil {
		return nil, err
	}
	id := "bubblewrap-" + strconv.FormatInt(p.counter.Add(1), 10)
	return &instance{
		id:       id,
		bwrap:    p.bwrap,
		bwrapArg: buildBwrapArgs(cfg),
	}, nil
}

type instance struct {
	id       string
	bwrap    string
	bwrapArg []string
}

func (i *instance) ID() string { return i.id }

// Exec returns an exec.Cmd that, when run, invokes bwrap with the
// pre-computed sandbox argv followed by the caller's cmd/args.
func (i *instance) Exec(cmd string, args []string, env map[string]string) (*exec.Cmd, error) {
	if cmd == "" {
		return nil, errors.New("bubblewrap: empty cmd")
	}
	full := make([]string, 0, len(i.bwrapArg)+2+len(args))
	full = append(full, i.bwrapArg...)
	full = append(full, "--", cmd)
	full = append(full, args...)
	c := exec.Command(i.bwrap, full...)
	c.Env = append(os.Environ(), envSlice(env)...)
	return c, nil
}

// Teardown is a no-op: bubblewrap's namespaces are cleaned up when bwrap
// exits, which happens with the child process (--die-with-parent).
func (i *instance) Teardown(ctx context.Context) error { return nil }

func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
