//go:build darwin

package resource

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	defaultProcessCollector ProcessCollector = darwinProcessCollector{}
	defaultSystemCollector  SystemCollector  = darwinSystemCollector{}
)

type darwinProcessCollector struct{}

// Collect shells out to ps for v1. A follow-up plan may replace this with
// libproc via cgo for lower overhead.
func (darwinProcessCollector) Collect(pid int) (Sample, error) {
	// Note: macOS ps(1) does not support nlwp. Thread count requires
	// proc_pidinfo (cgo); deferred to v2.
	cmd := exec.Command("ps", "-o", "rss=,vsz=,%cpu=", "-p", strconv.Itoa(pid))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return Sample{}, fmt.Errorf("resource: ps: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(stdout.String()))
	if len(fields) < 3 {
		return Sample{}, fmt.Errorf("resource: ps output too short: %q", stdout.String())
	}
	rssKB, _ := strconv.ParseUint(fields[0], 10, 64)
	vszKB, _ := strconv.ParseUint(fields[1], 10, 64)
	cpu, _ := strconv.ParseFloat(fields[2], 64)
	return Sample{
		PID:        pid,
		RSS:        rssKB * 1024,
		VMS:        vszKB * 1024,
		CPUPercent: cpu,
		NumThreads: 0, // Thread count requires proc_pidinfo (cgo); deferred to v2
		SampledAt:  time.Now(),
	}, nil
}

type darwinSystemCollector struct{}

func (darwinSystemCollector) Collect() (SystemSample, error) {
	s := SystemSample{NumCPU: runtime.NumCPU(), SampledAt: time.Now()}

	// Total memory via sysctl
	if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
		if v, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64); err == nil {
			s.TotalMemory = v
		}
	}

	// Available memory via vm_stat (free + inactive + speculative).
	// Using vm.page_free_count alone underestimates available memory on macOS
	// because it ignores inactive and speculative pages that the OS can reclaim.
	if out, err := exec.Command("vm_stat").Output(); err == nil {
		s.AvailableMemory = parseVMStat(string(out))
	}

	// Load average
	if out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output(); err == nil {
		// format: "{ 1.23 0.45 0.67 }"
		fields := strings.Fields(strings.Trim(strings.TrimSpace(string(out)), "{}"))
		if len(fields) >= 2 {
			if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
				s.LoadAvg1 = v
			}
			if v, err := strconv.ParseFloat(fields[1], 64); err == nil {
				s.LoadAvg5 = v
			}
		}
	}

	return s, nil
}

// parseVMStat parses vm_stat output and sums free + inactive + speculative pages.
func parseVMStat(output string) uint64 {
	pageSize := uint64(4096)
	var free, inactive, speculative uint64
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "Pages free:") {
			free = extractVMStatValue(line)
		} else if strings.Contains(line, "Pages inactive:") {
			inactive = extractVMStatValue(line)
		} else if strings.Contains(line, "Pages speculative:") {
			speculative = extractVMStatValue(line)
		}
	}
	return (free + inactive + speculative) * pageSize
}

// extractVMStatValue extracts the numeric value from a vm_stat line.
func extractVMStatValue(line string) uint64 {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return 0
	}
	s := strings.TrimRight(parts[len(parts)-1], ".")
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}
