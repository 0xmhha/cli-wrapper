// SPDX-License-Identifier: Apache-2.0

package bubblewrap

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

func TestNew_LinuxRequiresBwrap(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only assertion")
	}
	if _, err := exec.LookPath("bwrap"); err == nil {
		t.Skip("bwrap is on PATH; this test asserts the missing-binary path")
	}
	_, err := New(Options{})
	if !errors.Is(err, ErrBwrapNotFound) {
		t.Fatalf("expected ErrBwrapNotFound, got %v", err)
	}
}

func TestNew_WithExplicitBwrapPath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only construction path")
	}
	// Synthetic path bypasses LookPath; the constructor should accept it.
	p, err := New(Options{BwrapPath: "/nonexistent/bwrap"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Name() != "bubblewrap" {
		t.Fatalf("Name=%q want %q", p.Name(), "bubblewrap")
	}
}

func TestParseConfig(t *testing.T) {
	cases := []struct {
		name    string
		raw     map[string]any
		want    config
		wantErr bool
	}{
		{
			name: "all-defaults",
			raw:  nil,
			want: config{},
		},
		{
			name: "network-true",
			raw:  map[string]any{"network": true},
			want: config{network: true},
		},
		{
			name: "chdir",
			raw:  map[string]any{"chdir": "/work"},
			want: config{chdir: "/work"},
		},
		{
			name: "bind_rw and bind_ro as []string",
			raw: map[string]any{
				"bind_rw": []string{"/data"},
				"bind_ro": []string{"/etc/ssl"},
			},
			want: config{bindRW: []string{"/data"}, bindRO: []string{"/etc/ssl"}},
		},
		{
			name: "bind_rw as []any (yaml-like)",
			raw:  map[string]any{"bind_rw": []any{"/a", "/b"}},
			want: config{bindRW: []string{"/a", "/b"}},
		},
		{
			name:    "network wrong type",
			raw:     map[string]any{"network": "yes"},
			wantErr: true,
		},
		{
			name:    "chdir wrong type",
			raw:     map[string]any{"chdir": 42},
			wantErr: true,
		},
		{
			name:    "bind_rw element wrong type",
			raw:     map[string]any{"bind_rw": []any{"/a", 7}},
			wantErr: true,
		},
		{
			name:    "bind_ro wrong outer type",
			raw:     map[string]any{"bind_ro": "/a:/b"},
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseConfig(c.raw)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil; got=%+v", got)
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

func TestBuildBwrapArgs_Defaults(t *testing.T) {
	got := buildBwrapArgs(config{})
	want := []string{
		"--unshare-all",
		"--ro-bind", "/", "/",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--die-with-parent",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default argv mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestBuildBwrapArgs_NetworkShared(t *testing.T) {
	got := buildBwrapArgs(config{network: true})
	// Expect --share-net immediately after --unshare-all.
	if got[0] != "--unshare-all" || got[1] != "--share-net" {
		t.Fatalf("expected --unshare-all then --share-net, got %v", got[:2])
	}
}

func TestBuildBwrapArgs_BindOrdering(t *testing.T) {
	// RW binds must appear AFTER RO binds in argv so a writeable bind
	// nested under a read-only parent wins (bwrap applies in order).
	got := buildBwrapArgs(config{
		bindRO: []string{"/etc"},
		bindRW: []string{"/etc/dynamic"},
	})
	roIdx, rwIdx := -1, -1
	for i := 0; i+1 < len(got); i++ {
		switch got[i] {
		case "--ro-bind":
			if got[i+1] == "/etc" {
				roIdx = i
			}
		case "--bind":
			if got[i+1] == "/etc/dynamic" {
				rwIdx = i
			}
		}
	}
	if roIdx < 0 || rwIdx < 0 {
		t.Fatalf("missing expected binds in %v", got)
	}
	if roIdx >= rwIdx {
		t.Fatalf("rw bind (idx %d) must follow ro bind (idx %d) in argv: %v", rwIdx, roIdx, got)
	}
}

func TestBuildBwrapArgs_Chdir(t *testing.T) {
	got := buildBwrapArgs(config{chdir: "/srv"})
	found := false
	for i := 0; i+1 < len(got); i++ {
		if got[i] == "--chdir" && got[i+1] == "/srv" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("--chdir /srv not found in %v", got)
	}
}

func TestPrepareAndExec_AssemblesArgv(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Provider construction is Linux-only")
	}
	p, err := New(Options{BwrapPath: "/usr/bin/bwrap"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	inst, err := p.Prepare(context.Background(), cwtypes.SandboxSpec{
		Provider: "bubblewrap",
		Config:   map[string]any{"network": true},
	}, nil)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer func() { _ = inst.Teardown(context.Background()) }()

	cmd, err := inst.Exec("/bin/echo", []string{"hi"}, nil)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if cmd.Path != "/usr/bin/bwrap" {
		t.Fatalf("cmd.Path=%q want /usr/bin/bwrap", cmd.Path)
	}
	// bwrap argv should end with: -- /bin/echo hi
	args := cmd.Args[1:] // skip argv[0] which is bwrap path
	if n := len(args); n < 3 || args[n-3] != "--" || args[n-2] != "/bin/echo" || args[n-1] != "hi" {
		t.Fatalf("expected ...-- /bin/echo hi suffix, got %v", args)
	}
	// --share-net must be present (network: true).
	if !slices.Contains(args, "--share-net") {
		t.Fatal("--share-net missing despite config.network=true")
	}
}

func TestExec_RejectsEmptyCmd(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only construction")
	}
	p, err := New(Options{BwrapPath: "/usr/bin/bwrap"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	inst, err := p.Prepare(context.Background(), cwtypes.SandboxSpec{Provider: "bubblewrap"}, nil)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if _, err := inst.Exec("", nil, nil); err == nil {
		t.Fatal("expected error from Exec(\"\", ...)")
	}
}
