// SPDX-License-Identifier: Apache-2.0

//go:build linux

package cgroup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hasDelegatedCgroup reports whether the test process is running under a
// cgroup v2 directory it can write to. Most Linux developer hosts running
// under systemd's user-session do NOT have this without explicit setup,
// so the integration tests below skip when the precondition fails.
func hasDelegatedCgroup(t *testing.T) string {
	t.Helper()
	// Find our own cgroup path.
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		t.Skip("/proc/self/cgroup unavailable")
	}
	// cgroup v2 line looks like "0::/user.slice/user-1000.slice/...".
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "0::") {
		t.Skip("not running under cgroup v2 unified hierarchy")
	}
	relPath := strings.TrimPrefix(line, "0::")
	abs := filepath.Join("/sys/fs/cgroup", relPath)
	// Probe write permission by attempting a no-op rename of cgroup.procs.
	// Simpler: check directory writability via Stat + permission bits.
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("cgroup %s not stat-able: %v", abs, err)
	}
	probe := filepath.Join(abs, ".cliwrap-probe")
	if err := os.Mkdir(probe, 0o755); err != nil {
		t.Skipf("cgroup %s not writeable (no Delegate=yes? need root?): %v", abs, err)
	}
	_ = os.Remove(probe)
	return abs
}

func TestNew_CreatesAndRemovesGroup(t *testing.T) {
	parent := hasDelegatedCgroup(t)
	g, err := New(parent, "cw-test-1")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := os.Stat(g.Path); err != nil {
		t.Fatalf("expected cgroup dir at %s: %v", g.Path, err)
	}
	if err := g.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(g.Path); !os.IsNotExist(err) {
		t.Fatalf("cgroup dir should have been removed; err=%v", err)
	}
}

func TestNew_ParentMissingReturnsCgroupV2Unavailable(t *testing.T) {
	// Use a clearly non-existent parent path.
	_, err := New("/sys/fs/cgroup/__cliwrap_definitely_missing__", "foo")
	if err == nil {
		t.Fatal("expected error for missing parent")
	}
	if !errors.Is(err, ErrCgroupV2Unavailable) {
		t.Fatalf("expected ErrCgroupV2Unavailable, got %v", err)
	}
}

func TestSetCPUMax_FormatsLine(t *testing.T) {
	parent := hasDelegatedCgroup(t)
	g, err := New(parent, "cw-test-2")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = g.Remove() }()
	if err := g.SetCPUMax(CPUMax{Quota: 50000, Period: 100000}); err != nil {
		t.Fatalf("SetCPUMax: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(g.Path, "cpu.max"))
	if err != nil {
		t.Fatalf("read cpu.max: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(got)), "50000 100000") {
		t.Fatalf("cpu.max contents = %q want prefix %q", got, "50000 100000")
	}
}

func TestSetCPUMax_ZeroQuotaWritesMax(t *testing.T) {
	parent := hasDelegatedCgroup(t)
	g, err := New(parent, "cw-test-3")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = g.Remove() }()
	if err := g.SetCPUMax(CPUMax{Quota: 0, Period: 200000}); err != nil {
		t.Fatalf("SetCPUMax: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(g.Path, "cpu.max"))
	if err != nil {
		t.Fatalf("read cpu.max: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(got)), "max 200000") {
		t.Fatalf("cpu.max = %q want prefix 'max 200000'", got)
	}
}

func TestSetMemoryMax_ZeroWritesMax(t *testing.T) {
	parent := hasDelegatedCgroup(t)
	g, err := New(parent, "cw-test-4")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = g.Remove() }()
	if err := g.SetMemoryMax(0); err != nil {
		t.Fatalf("SetMemoryMax: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(g.Path, "memory.max"))
	if err != nil {
		t.Fatalf("read memory.max: %v", err)
	}
	if strings.TrimSpace(string(got)) != "max" {
		t.Fatalf("memory.max = %q want 'max'", got)
	}
}
