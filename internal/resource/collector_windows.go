// SPDX-License-Identifier: Apache-2.0

//go:build windows

package resource

import (
	"errors"
	"runtime"
	"time"
)

// ErrNotImplemented is returned by every collector method on Windows.
// Process- and system-resource sampling on Windows requires a different
// path (PerformanceCounters / GetProcessMemoryInfo / GetSystemInfo) and
// is part of the larger Windows runtime port (CW-P2-5) — not yet wired.
var ErrNotImplemented = errors.New("resource: not implemented on " + runtime.GOOS)

type windowsProcessCollector struct{}

func (windowsProcessCollector) Collect(pid int) (Sample, error) {
	return Sample{PID: pid, SampledAt: time.Now()}, ErrNotImplemented
}

type windowsSystemCollector struct{}

func (windowsSystemCollector) Collect() (SystemSample, error) {
	return SystemSample{NumCPU: runtime.NumCPU(), SampledAt: time.Now()}, ErrNotImplemented
}

var (
	defaultProcessCollector ProcessCollector = windowsProcessCollector{}
	defaultSystemCollector  SystemCollector  = windowsSystemCollector{}
)
