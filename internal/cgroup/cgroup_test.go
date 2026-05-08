// SPDX-License-Identifier: Apache-2.0

package cgroup

import (
	"errors"
	"runtime"
	"testing"
)

func TestNew_NonLinuxReturnsUnsupported(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("only meaningful on non-Linux hosts")
	}
	if _, err := New("/sys/fs/cgroup", "test"); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("New on non-Linux must return ErrUnsupportedPlatform, got %v", err)
	}
}

func TestStub_AllMethodsReturnUnsupported(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("only meaningful on non-Linux hosts")
	}
	g := &Group{}
	if !errors.Is(g.AddPID(123), ErrUnsupportedPlatform) {
		t.Error("AddPID must return ErrUnsupportedPlatform on non-Linux")
	}
	if !errors.Is(g.SetCPUMax(CPUMax{Quota: 50000, Period: 100000}), ErrUnsupportedPlatform) {
		t.Error("SetCPUMax must return ErrUnsupportedPlatform on non-Linux")
	}
	if !errors.Is(g.SetMemoryMax(0), ErrUnsupportedPlatform) {
		t.Error("SetMemoryMax must return ErrUnsupportedPlatform on non-Linux")
	}
	if !errors.Is(g.SetIOWeight(100), ErrUnsupportedPlatform) {
		t.Error("SetIOWeight must return ErrUnsupportedPlatform on non-Linux")
	}
	if !errors.Is(g.Remove(), ErrUnsupportedPlatform) {
		t.Error("Remove must return ErrUnsupportedPlatform on non-Linux")
	}
}
