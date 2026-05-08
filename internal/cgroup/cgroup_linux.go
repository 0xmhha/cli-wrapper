// SPDX-License-Identifier: Apache-2.0

//go:build linux

package cgroup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Group is a handle to a single cgroup directory. Methods write to the
// kernel-exposed control files under that directory.
type Group struct {
	// Path is the absolute filesystem path to the cgroup directory,
	// e.g. /sys/fs/cgroup/cliwrap/proc-123.
	Path string
}

// New creates a new cgroup directory under parent (e.g.
// /sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/cliwrap.slice)
// with the given subgroup name. The parent directory must already exist
// and be writeable by the caller.
//
// Returns ErrCgroupV2Unavailable if the unified hierarchy is not mounted
// or the parent path is missing; ErrPermission on filesystem permission
// errors; or a wrapped error for other failures.
func New(parent, name string) (*Group, error) {
	if _, err := os.Stat(DefaultMountPoint); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrCgroupV2Unavailable
		}
		return nil, fmt.Errorf("cgroup: stat mount: %w", err)
	}
	if _, err := os.Stat(parent); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("cgroup: parent %q: %w", parent, ErrCgroupV2Unavailable)
		}
		return nil, fmt.Errorf("cgroup: stat parent: %w", err)
	}
	path := filepath.Join(parent, name)
	if err := os.Mkdir(path, 0o755); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("cgroup: mkdir %q: %w", path, ErrPermission)
		}
		if errors.Is(err, os.ErrExist) {
			// Reuse an existing cgroup directory; this is fine — caller
			// can re-add PIDs and reconfigure controllers idempotently.
			return &Group{Path: path}, nil
		}
		return nil, fmt.Errorf("cgroup: mkdir %q: %w", path, err)
	}
	return &Group{Path: path}, nil
}

// AddPID places pid into this cgroup by writing it to cgroup.procs.
// Subsequent threads of pid follow it automatically (cgroup v2 process
// migration semantics).
func (g *Group) AddPID(pid int) error {
	return g.write("cgroup.procs", strconv.Itoa(pid))
}

// SetCPUMax writes the cpu.max controller file. Quota=0 is interpreted as
// "max" (no limit). Period defaults to 100_000 µs when zero.
func (g *Group) SetCPUMax(m CPUMax) error {
	period := m.Period
	if period <= 0 {
		period = 100_000
	}
	var line string
	if m.Quota <= 0 {
		line = fmt.Sprintf("max %d", period)
	} else {
		line = fmt.Sprintf("%d %d", m.Quota, period)
	}
	return g.write("cpu.max", line)
}

// SetMemoryMax writes the memory.max controller file. A zero value
// writes "max" (no limit).
func (g *Group) SetMemoryMax(m MemoryMax) error {
	if m == 0 {
		return g.write("memory.max", "max")
	}
	return g.write("memory.max", strconv.FormatUint(uint64(m), 10))
}

// SetIOWeight writes io.weight (1..10000; default 100). Values outside
// the kernel-accepted range surface as a wrapped write error.
func (g *Group) SetIOWeight(w IOWeight) error {
	return g.write("io.weight", strconv.Itoa(int(w)))
}

// Remove deletes the cgroup directory. The kernel rejects rmdir on a
// non-empty cgroup (i.e. while it still contains processes); callers
// must wait for the contained processes to exit before calling Remove.
func (g *Group) Remove() error {
	if err := os.Remove(g.Path); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("cgroup: rmdir %q: %w", g.Path, ErrPermission)
		}
		return fmt.Errorf("cgroup: rmdir %q: %w", g.Path, err)
	}
	return nil
}

// write writes content to <Path>/file. Internal helper.
func (g *Group) write(file, content string) error {
	full := filepath.Join(g.Path, file)
	if err := os.WriteFile(full, []byte(content), 0); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("cgroup: write %q: %w", full, ErrPermission)
		}
		return fmt.Errorf("cgroup: write %q: %w", full, err)
	}
	return nil
}
