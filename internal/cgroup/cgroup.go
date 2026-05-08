// SPDX-License-Identifier: Apache-2.0

// Package cgroup provides a thin helper for placing a child process under
// a cgroup v2 group with CPU quota and I/O weight limits. Linux-only.
//
// This is a building block for resource-throttled child supervision; the
// runner (internal/agent) does NOT consume it automatically yet. Callers
// pass an existing PID to AddPID after starting the child, then configure
// the relevant controller files. On child exit, call Remove to delete
// the cgroup directory.
//
// Requirements:
//   - cgroup v2 unified hierarchy (Linux 4.5+; ubiquitous on systemd hosts).
//   - Write access to the parent cgroup directory. This typically means
//     either running under a delegated subtree (e.g. via a systemd
//     `Delegate=yes` user slice) or as root.
//
// Errors:
//   - ErrUnsupportedPlatform: returned by every constructor on non-Linux.
//   - ErrCgroupV2Unavailable: cgroup v2 not mounted at /sys/fs/cgroup,
//     or the parent path does not exist.
//   - ErrPermission: filesystem permission denied (typically: not running
//     under a delegated subtree).
package cgroup

import "errors"

// DefaultMountPoint is the conventional cgroup v2 unified-hierarchy mount.
const DefaultMountPoint = "/sys/fs/cgroup"

// Sentinel errors.
var (
	ErrUnsupportedPlatform = errors.New("cgroup: unsupported platform (Linux only)")
	ErrCgroupV2Unavailable = errors.New("cgroup: cgroup v2 unified hierarchy not available")
	ErrPermission          = errors.New("cgroup: permission denied (run under a delegated subtree or as root)")
)

// CPUMax describes a CPU quota for a cgroup. The kernel format is
// "<quota> <period>" where quota is microseconds-of-CPU per period
// microseconds-of-wall-clock. Setting Quota=0 produces "max <period>"
// (no limit).
type CPUMax struct {
	Quota  int64 // microseconds; 0 = no limit
	Period int64 // microseconds; defaults to 100_000 (100ms) on the kernel side
}

// IOWeight is a relative weight 1..10000 (default 100). Lower = less I/O
// share. cgroup v2 only honors this when the io controller is enabled.
type IOWeight int

// MemoryMax is the memory hard limit in bytes. Zero = no limit.
type MemoryMax uint64
