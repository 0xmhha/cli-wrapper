// SPDX-License-Identifier: Apache-2.0

// Package firejail is a Linux-only sandbox provider that wraps the
// firejail binary. firejail is an SUID-root namespace sandbox that
// applies seccomp filters, capability drops, and a private filesystem
// layout via predefined or custom profiles.
//
// Configuration is taken from cwtypes.SandboxSpec.Config:
//
//	profile     string   — named firejail profile (e.g. "default", "claude").
//	                       Empty = no --profile flag (firejail's auto-detect).
//	netns       string   — interface for --netns (network namespace);
//	                       empty disables the flag. Mutually exclusive
//	                       with `nonet`.
//	nonet       bool     — pass --nonet (no network at all).
//	private     bool     — pass --private (private home temporary dir).
//	noroot      bool     — pass --noroot (deny gaining privileges).
//	whitelist   []string — extra --whitelist paths.
//	blacklist   []string — extra --blacklist paths.
//	caps_drop   bool     — pass --caps.drop=all.
//	seccomp     bool     — pass --seccomp.
//
// Linux-only: New returns ErrUnsupportedPlatform on other GOOS values.
// `firejail` must be present on PATH at New() time; otherwise
// ErrFirejailNotFound is returned.
package firejail

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

var (
	ErrUnsupportedPlatform = errors.New("firejail: unsupported platform (Linux only)")
	ErrFirejailNotFound    = errors.New("firejail: firejail binary not found in PATH")
)

// Options configures a firejail.Provider.
type Options struct {
	// FirejailPath overrides the auto-detected firejail path. Empty =
	// look up via exec.LookPath at New() time.
	FirejailPath string
}

// Provider implements sandbox.Provider via firejail.
type Provider struct {
	firejail string
	counter  atomic.Int64
}

// New constructs a Provider. Returns ErrUnsupportedPlatform on non-Linux,
// ErrFirejailNotFound if firejail cannot be located on PATH.
func New(opts Options) (*Provider, error) {
	if runtime.GOOS != "linux" {
		return nil, ErrUnsupportedPlatform
	}
	fj := opts.FirejailPath
	if fj == "" {
		p, err := exec.LookPath("firejail")
		if err != nil {
			return nil, errors.Join(ErrFirejailNotFound, err)
		}
		fj = p
	}
	return &Provider{firejail: fj}, nil
}

// Name returns "firejail".
func (p *Provider) Name() string { return "firejail" }

// config holds the parsed values from SandboxSpec.Config.
type config struct {
	profile   string
	netns     string
	nonet     bool
	private   bool
	noroot    bool
	whitelist []string
	blacklist []string
	capsDrop  bool
	seccomp   bool
}

func parseConfig(raw map[string]any) (config, error) {
	var c config
	for k, v := range raw {
		switch k {
		case "profile":
			s, ok := v.(string)
			if !ok {
				return c, typeErr(k, v, "string")
			}
			c.profile = s
		case "netns":
			s, ok := v.(string)
			if !ok {
				return c, typeErr(k, v, "string")
			}
			c.netns = s
		case "nonet":
			b, ok := v.(bool)
			if !ok {
				return c, typeErr(k, v, "bool")
			}
			c.nonet = b
		case "private":
			b, ok := v.(bool)
			if !ok {
				return c, typeErr(k, v, "bool")
			}
			c.private = b
		case "noroot":
			b, ok := v.(bool)
			if !ok {
				return c, typeErr(k, v, "bool")
			}
			c.noroot = b
		case "caps_drop":
			b, ok := v.(bool)
			if !ok {
				return c, typeErr(k, v, "bool")
			}
			c.capsDrop = b
		case "seccomp":
			b, ok := v.(bool)
			if !ok {
				return c, typeErr(k, v, "bool")
			}
			c.seccomp = b
		case "whitelist":
			s, err := stringSlice(v, k)
			if err != nil {
				return c, err
			}
			c.whitelist = s
		case "blacklist":
			s, err := stringSlice(v, k)
			if err != nil {
				return c, err
			}
			c.blacklist = s
		default:
			return c, fmt.Errorf("firejail: unknown config key %q", k)
		}
	}
	if c.nonet && c.netns != "" {
		return c, errors.New("firejail: nonet and netns are mutually exclusive")
	}
	return c, nil
}

func typeErr(key string, v any, want string) error {
	return fmt.Errorf("firejail: config.%s must be %s, got %T", key, want, v)
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
				return nil, fmt.Errorf("firejail: config.%s[%d] must be string, got %T", name, i, e)
			}
			out = append(out, str)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("firejail: config.%s must be []string, got %T", name, v)
	}
}

// buildFirejailArgs assembles the firejail argv (excluding the leading
// firejail binary path). Exposed for unit-testing without invoking firejail.
func buildFirejailArgs(c config) []string {
	args := []string{
		// --quiet keeps firejail's own banner out of the child's stdout.
		"--quiet",
	}
	if c.profile != "" {
		args = append(args, "--profile="+c.profile)
	}
	if c.private {
		args = append(args, "--private")
	}
	if c.noroot {
		args = append(args, "--noroot")
	}
	if c.capsDrop {
		args = append(args, "--caps.drop=all")
	}
	if c.seccomp {
		args = append(args, "--seccomp")
	}
	if c.nonet {
		args = append(args, "--net=none")
	}
	if c.netns != "" {
		args = append(args, "--netns="+c.netns)
	}
	for _, p := range c.whitelist {
		args = append(args, "--whitelist="+p)
	}
	for _, p := range c.blacklist {
		args = append(args, "--blacklist="+p)
	}
	return args
}

// Prepare returns an Instance that will exec firejail on first
// Instance.Exec. scripts is presently ignored — firejail composes via
// argv + profile files, not by writing entrypoints into a directory.
func (p *Provider) Prepare(ctx context.Context, spec cwtypes.SandboxSpec, scripts []sandbox.Script) (sandbox.Instance, error) {
	cfg, err := parseConfig(spec.Config)
	if err != nil {
		return nil, err
	}
	id := "firejail-" + strconv.FormatInt(p.counter.Add(1), 10)
	return &instance{
		id:       id,
		firejail: p.firejail,
		fjArg:    buildFirejailArgs(cfg),
	}, nil
}

type instance struct {
	id       string
	firejail string
	fjArg    []string
}

func (i *instance) ID() string { return i.id }

// Exec returns an exec.Cmd that, when run, invokes firejail with the
// pre-computed sandbox argv followed by the caller's cmd/args.
func (i *instance) Exec(cmd string, args []string, env map[string]string) (*exec.Cmd, error) {
	if cmd == "" {
		return nil, errors.New("firejail: empty cmd")
	}
	full := make([]string, 0, len(i.fjArg)+1+len(args))
	full = append(full, i.fjArg...)
	full = append(full, cmd)
	full = append(full, args...)
	c := exec.Command(i.firejail, full...)
	c.Env = append(os.Environ(), envSlice(env)...)
	return c, nil
}

// Teardown is a no-op: firejail tears down its own namespaces when the
// child exits.
func (i *instance) Teardown(ctx context.Context) error { return nil }

func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
