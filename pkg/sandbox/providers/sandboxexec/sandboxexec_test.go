// SPDX-License-Identifier: Apache-2.0

package sandboxexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"slices"
	"testing"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

func TestNew_RejectsNonDarwin(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("only meaningful on non-macOS hosts")
	}
	_, err := New(Options{})
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("expected ErrUnsupportedPlatform, got %v", err)
	}
}

func TestNew_DarwinAcceptsExplicitPath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only construction path")
	}
	p, err := New(Options{SandboxExecPath: "/usr/bin/sandbox-exec"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Name() != "sandbox-exec" {
		t.Fatalf("Name=%q want sandbox-exec", p.Name())
	}
}

func TestNew_DarwinMissingBinary(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only assertion")
	}
	if _, err := exec.LookPath("sandbox-exec"); err == nil {
		t.Skip("sandbox-exec is on PATH; this test asserts the missing-binary path")
	}
	_, err := New(Options{})
	if !errors.Is(err, ErrSandboxExecNotFound) {
		t.Fatalf("expected ErrSandboxExecNotFound, got %v", err)
	}
}

func TestParseConfig(t *testing.T) {
	cases := []struct {
		name    string
		raw     map[string]any
		want    config
		wantErr bool
	}{
		{name: "profile inline", raw: map[string]any{"profile": "(version 1)"},
			want: config{profileText: "(version 1)"}},
		{name: "profile_name", raw: map[string]any{"profile_name": "no-network"},
			want: config{profileName: "no-network"}},
		{name: "profile_file", raw: map[string]any{"profile_file": "/tmp/x.sb"},
			want: config{profileFile: "/tmp/x.sb"}},
		{name: "all unset", raw: map[string]any{}, wantErr: true},
		{name: "two set is rejected", raw: map[string]any{
			"profile": "(version 1)", "profile_name": "no-network",
		}, wantErr: true},
		{name: "all three set is rejected", raw: map[string]any{
			"profile": "x", "profile_name": "y", "profile_file": "z",
		}, wantErr: true},
		{name: "unknown key", raw: map[string]any{"made_up": "yes"}, wantErr: true},
		{name: "wrong type", raw: map[string]any{"profile_name": 7}, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseConfig(c.raw)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v want %+v", got, c.want)
			}
		})
	}
}

func TestPrepare_BuiltinProfileName(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only")
	}
	p, err := New(Options{SandboxExecPath: "/usr/bin/sandbox-exec"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	inst, err := p.Prepare(context.Background(), cwtypes.SandboxSpec{
		Provider: "sandbox-exec",
		Config:   map[string]any{"profile_name": "no-network"},
	}, nil)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer func() { _ = inst.Teardown(context.Background()) }()

	cmd, err := inst.Exec("/bin/echo", []string{"hi"}, nil)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	args := cmd.Args[1:] // drop argv[0]
	want := []string{"-n", "no-network", "/bin/echo", "hi"}
	if !slices.Equal(args, want) {
		t.Fatalf("argv = %v want %v", args, want)
	}
}

func TestPrepare_InlineProfileMaterializes(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only")
	}
	tmp := t.TempDir()
	p, err := New(Options{
		SandboxExecPath: "/usr/bin/sandbox-exec",
		TmpDir:          tmp,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const text = "(version 1)\n(deny default)"
	inst, err := p.Prepare(context.Background(), cwtypes.SandboxSpec{
		Provider: "sandbox-exec",
		Config:   map[string]any{"profile": text},
	}, nil)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	cmd, err := inst.Exec("/bin/echo", []string{"hi"}, nil)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	args := cmd.Args[1:]
	if args[0] != "-f" {
		t.Fatalf("first arg should be -f, got %q", args[0])
	}
	profilePath := args[1]

	// Profile file must exist with the configured text before Teardown.
	got, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("reading profile file %q: %v", profilePath, err)
	}
	if string(got) != text {
		t.Fatalf("profile contents = %q want %q", got, text)
	}

	// Teardown removes the materialized file.
	if err := inst.Teardown(context.Background()); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if _, err := os.Stat(profilePath); !os.IsNotExist(err) {
		t.Fatalf("profile file should have been removed; err=%v", err)
	}
}

func TestPrepare_ProfileFilePathIsForwarded(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only")
	}
	p, err := New(Options{SandboxExecPath: "/usr/bin/sandbox-exec"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	inst, err := p.Prepare(context.Background(), cwtypes.SandboxSpec{
		Provider: "sandbox-exec",
		Config:   map[string]any{"profile_file": "/etc/cliwrap.sb"},
	}, nil)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer func() { _ = inst.Teardown(context.Background()) }()

	cmd, err := inst.Exec("/bin/echo", nil, nil)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	args := cmd.Args[1:]
	if !slices.Equal(args[:2], []string{"-f", "/etc/cliwrap.sb"}) {
		t.Fatalf("expected -f /etc/cliwrap.sb prefix, got %v", args)
	}
}

func TestExec_RejectsEmptyCmd(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only")
	}
	p, err := New(Options{SandboxExecPath: "/usr/bin/sandbox-exec"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	inst, err := p.Prepare(context.Background(), cwtypes.SandboxSpec{
		Config: map[string]any{"profile_name": "no-network"},
	}, nil)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if _, err := inst.Exec("", nil, nil); err == nil {
		t.Fatal("expected error from Exec(\"\", ...)")
	}
}
