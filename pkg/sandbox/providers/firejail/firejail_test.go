// SPDX-License-Identifier: Apache-2.0

package firejail

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"runtime"
	"slices"
	"testing"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

func TestNew_RejectsNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("only meaningful on non-Linux hosts")
	}
	_, err := New(Options{})
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("expected ErrUnsupportedPlatform, got %v", err)
	}
}

func TestNew_LinuxRequiresFirejail(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only assertion")
	}
	if _, err := exec.LookPath("firejail"); err == nil {
		t.Skip("firejail is on PATH; this test asserts the missing-binary path")
	}
	_, err := New(Options{})
	if !errors.Is(err, ErrFirejailNotFound) {
		t.Fatalf("expected ErrFirejailNotFound, got %v", err)
	}
}

func TestNew_WithExplicitPath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only construction path")
	}
	p, err := New(Options{FirejailPath: "/nonexistent/firejail"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Name() != "firejail" {
		t.Fatalf("Name=%q want %q", p.Name(), "firejail")
	}
}

func TestParseConfig(t *testing.T) {
	cases := []struct {
		name    string
		raw     map[string]any
		want    config
		wantErr bool
	}{
		{name: "all-defaults", raw: nil, want: config{}},
		{name: "profile", raw: map[string]any{"profile": "claude"}, want: config{profile: "claude"}},
		{name: "private+noroot+seccomp+caps_drop", raw: map[string]any{
			"private": true, "noroot": true, "seccomp": true, "caps_drop": true,
		}, want: config{private: true, noroot: true, seccomp: true, capsDrop: true}},
		{name: "nonet", raw: map[string]any{"nonet": true}, want: config{nonet: true}},
		{name: "netns", raw: map[string]any{"netns": "br0"}, want: config{netns: "br0"}},
		{name: "whitelist []string", raw: map[string]any{"whitelist": []string{"/data"}},
			want: config{whitelist: []string{"/data"}}},
		{name: "blacklist []any", raw: map[string]any{"blacklist": []any{"/etc/shadow"}},
			want: config{blacklist: []string{"/etc/shadow"}}},
		{name: "nonet+netns mutually exclusive", raw: map[string]any{
			"nonet": true, "netns": "br0",
		}, wantErr: true},
		{name: "unknown key", raw: map[string]any{"made_up": true}, wantErr: true},
		{name: "wrong type bool", raw: map[string]any{"private": "yes"}, wantErr: true},
		{name: "wrong type string", raw: map[string]any{"profile": 7}, wantErr: true},
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

func TestBuildFirejailArgs_Defaults(t *testing.T) {
	got := buildFirejailArgs(config{})
	if !slices.Equal(got, []string{"--quiet"}) {
		t.Fatalf("default argv = %v want [--quiet]", got)
	}
}

func TestBuildFirejailArgs_AllOptions(t *testing.T) {
	got := buildFirejailArgs(config{
		profile:   "claude",
		private:   true,
		noroot:    true,
		capsDrop:  true,
		seccomp:   true,
		netns:     "br0",
		whitelist: []string{"/data"},
		blacklist: []string{"/etc/shadow"},
	})
	for _, want := range []string{
		"--quiet",
		"--profile=claude",
		"--private",
		"--noroot",
		"--caps.drop=all",
		"--seccomp",
		"--netns=br0",
		"--whitelist=/data",
		"--blacklist=/etc/shadow",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in argv %v", want, got)
		}
	}
	if slices.Contains(got, "--net=none") {
		t.Error("--net=none must not appear when nonet=false")
	}
}

func TestBuildFirejailArgs_Nonet(t *testing.T) {
	got := buildFirejailArgs(config{nonet: true})
	if !slices.Contains(got, "--net=none") {
		t.Fatalf("expected --net=none in %v", got)
	}
}

func TestPrepareAndExec(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only construction")
	}
	p, err := New(Options{FirejailPath: "/usr/bin/firejail"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	inst, err := p.Prepare(context.Background(), cwtypes.SandboxSpec{
		Provider: "firejail",
		Config:   map[string]any{"private": true},
	}, nil)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer func() { _ = inst.Teardown(context.Background()) }()

	cmd, err := inst.Exec("/bin/echo", []string{"hi"}, nil)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if cmd.Path != "/usr/bin/firejail" {
		t.Fatalf("cmd.Path=%q want /usr/bin/firejail", cmd.Path)
	}
	args := cmd.Args[1:] // skip argv[0]
	// firejail argv ends with: <fj-args> /bin/echo hi
	n := len(args)
	if args[n-2] != "/bin/echo" || args[n-1] != "hi" {
		t.Fatalf("expected ... /bin/echo hi suffix, got %v", args)
	}
	if !slices.Contains(args, "--private") {
		t.Fatal("--private missing despite config.private=true")
	}
}

func TestExec_RejectsEmptyCmd(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only construction")
	}
	p, err := New(Options{FirejailPath: "/usr/bin/firejail"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	inst, err := p.Prepare(context.Background(), cwtypes.SandboxSpec{Provider: "firejail"}, nil)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if _, err := inst.Exec("", nil, nil); err == nil {
		t.Fatal("expected error from Exec(\"\", ...)")
	}
}
