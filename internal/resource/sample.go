// SPDX-License-Identifier: Apache-2.0

// Package resource implements per-process and system-wide resource sampling
// and enforcement.
package resource

import "time"

// Sample is one point-in-time reading for a single process.
type Sample struct {
	PID        int
	RSS        uint64  // bytes
	VMS        uint64  // bytes
	CPUPercent float64 // 0-100 normalized per core
	NumThreads int
	NumFDs     int
	SampledAt  time.Time
}

// ProcessCollector returns a Sample for the given PID.
type ProcessCollector interface {
	Collect(pid int) (Sample, error)
}

// SystemSample is one host-wide reading.
type SystemSample struct {
	TotalMemory     uint64
	AvailableMemory uint64
	LoadAvg1        float64
	LoadAvg5        float64
	NumCPU          int
	SampledAt       time.Time
}

// AvailableRatio returns AvailableMemory / TotalMemory, or 0 if TotalMemory is 0.
func (s SystemSample) AvailableRatio() float64 {
	if s.TotalMemory == 0 {
		return 0
	}
	return float64(s.AvailableMemory) / float64(s.TotalMemory)
}

// SystemCollector returns a system-wide sample.
type SystemCollector interface {
	Collect() (SystemSample, error)
}

// DefaultProcessCollector returns the platform's default process collector.
func DefaultProcessCollector() ProcessCollector { return defaultProcessCollector }

// DefaultSystemCollector returns the platform's default system collector.
func DefaultSystemCollector() SystemCollector { return defaultSystemCollector }
