// SPDX-License-Identifier: Apache-2.0

// Package sandboxexec is a macOS-only sandbox provider that wraps the
// `sandbox-exec` binary (a deprecated-but-still-functional CLI shipped
// with macOS). It runs a child under a Scheme-syntax sandbox profile,
// which is materialized to a temp file at Prepare time.
//
// Apple has marked sandbox-exec as deprecated for years yet has not
// removed it; it remains the simplest profile-based isolation available
// on macOS short of writing a full XPC/launchd/LaunchAgent solution.
//
// Configuration is taken from cwtypes.SandboxSpec.Config:
//
//	profile         string — full Scheme profile text. Mutually exclusive
//	                         with profile_name and profile_file.
//	profile_name    string — apply a built-in named profile via
//	                         `sandbox-exec -n <name>` (e.g. "no-network",
//	                         "pure-computation"). Mutually exclusive with
//	                         profile and profile_file.
//	profile_file    string — path to a profile file on disk. Mutually
//	                         exclusive with profile and profile_name.
//
// Exactly one of {profile, profile_name, profile_file} must be set.
//
// macOS-only: New returns ErrUnsupportedPlatform on other GOOS values.
// `sandbox-exec` must be present on PATH at New() time; otherwise
// ErrSandboxExecNotFound is returned.
package sandboxexec

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
	ErrUnsupportedPlatform = errors.New("sandboxexec: unsupported platform (macOS only)")
	ErrSandboxExecNotFound = errors.New("sandboxexec: sandbox-exec binary not found in PATH")
)

// Options configures a sandboxexec.Provider.
type Options struct {
	// SandboxExecPath overrides the auto-detected sandbox-exec path.
	SandboxExecPath string
	// TmpDir is the parent directory under which inline profiles are
	// materialized. Empty = os.TempDir().
	TmpDir string
}

// Provider implements sandbox.Provider via sandbox-exec.
type Provider struct {
	binary  string
	tmpDir  string
	counter atomic.Int64
}

// New constructs a Provider. Returns ErrUnsupportedPlatform on non-macOS,
// ErrSandboxExecNotFound if sandbox-exec cannot be located on PATH.
func New(opts Options) (*Provider, error) {
	if runtime.GOOS != "darwin" {
		return nil, ErrUnsupportedPlatform
	}
	bin := opts.SandboxExecPath
	if bin == "" {
		p, err := exec.LookPath("sandbox-exec")
		if err != nil {
			return nil, errors.Join(ErrSandboxExecNotFound, err)
		}
		bin = p
	}
	td := opts.TmpDir
	if td == "" {
		td = os.TempDir()
	}
	return &Provider{binary: bin, tmpDir: td}, nil
}

// Name returns "sandbox-exec".
func (p *Provider) Name() string { return "sandbox-exec" }

// config holds parsed values from SandboxSpec.Config.
type config struct {
	profileText string
	profileName string
	profileFile string
}

func parseConfig(raw map[string]any) (config, error) {
	var c config
	for k, v := range raw {
		switch k {
		case "profile":
			s, ok := v.(string)
			if !ok {
				return c, fmt.Errorf("sandboxexec: config.%s must be string, got %T", k, v)
			}
			c.profileText = s
		case "profile_name":
			s, ok := v.(string)
			if !ok {
				return c, fmt.Errorf("sandboxexec: config.%s must be string, got %T", k, v)
			}
			c.profileName = s
		case "profile_file":
			s, ok := v.(string)
			if !ok {
				return c, fmt.Errorf("sandboxexec: config.%s must be string, got %T", k, v)
			}
			c.profileFile = s
		default:
			return c, fmt.Errorf("sandboxexec: unknown config key %q", k)
		}
	}
	set := 0
	if c.profileText != "" {
		set++
	}
	if c.profileName != "" {
		set++
	}
	if c.profileFile != "" {
		set++
	}
	if set != 1 {
		return c, errors.New("sandboxexec: exactly one of profile, profile_name, profile_file must be set")
	}
	return c, nil
}

// Prepare validates the config, materializes an inline profile to a temp
// file when needed, and returns an Instance that will exec sandbox-exec.
func (p *Provider) Prepare(ctx context.Context, spec cwtypes.SandboxSpec, scripts []sandbox.Script) (sandbox.Instance, error) {
	cfg, err := parseConfig(spec.Config)
	if err != nil {
		return nil, err
	}
	id := "sandbox-exec-" + strconv.FormatInt(p.counter.Add(1), 10)

	in := &instance{id: id, binary: p.binary}
	switch {
	case cfg.profileName != "":
		in.builtinName = cfg.profileName
	case cfg.profileFile != "":
		in.profilePath = cfg.profileFile
	case cfg.profileText != "":
		f, err := os.CreateTemp(p.tmpDir, "sandboxexec-*.sb")
		if err != nil {
			return nil, fmt.Errorf("sandboxexec: create temp profile: %w", err)
		}
		if _, werr := f.WriteString(cfg.profileText); werr != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
			return nil, fmt.Errorf("sandboxexec: write profile: %w", werr)
		}
		if cerr := f.Close(); cerr != nil {
			_ = os.Remove(f.Name())
			return nil, fmt.Errorf("sandboxexec: close profile: %w", cerr)
		}
		in.profilePath = f.Name()
		in.profileOwned = true
	}
	return in, nil
}

type instance struct {
	id           string
	binary       string
	builtinName  string // when set, use -n
	profilePath  string // when set, use -f
	profileOwned bool   // remove on Teardown
}

func (i *instance) ID() string { return i.id }

// buildSandboxArgs constructs the prefix argv (everything before the
// child cmd/args). Exposed for unit testing.
func (i *instance) buildSandboxArgs() []string {
	switch {
	case i.builtinName != "":
		return []string{"-n", i.builtinName}
	case i.profilePath != "":
		return []string{"-f", i.profilePath}
	}
	return nil
}

// Exec returns an exec.Cmd that, when run, invokes sandbox-exec with the
// configured profile, then the caller's cmd/args.
func (i *instance) Exec(cmd string, args []string, env map[string]string) (*exec.Cmd, error) {
	if cmd == "" {
		return nil, errors.New("sandboxexec: empty cmd")
	}
	prefix := i.buildSandboxArgs()
	full := make([]string, 0, len(prefix)+1+len(args))
	full = append(full, prefix...)
	full = append(full, cmd)
	full = append(full, args...)
	c := exec.Command(i.binary, full...)
	c.Env = append(os.Environ(), envSlice(env)...)
	return c, nil
}

// Teardown removes the temp profile file if Prepare materialized one.
func (i *instance) Teardown(ctx context.Context) error {
	if i.profileOwned && i.profilePath != "" {
		return os.Remove(i.profilePath)
	}
	return nil
}

func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
