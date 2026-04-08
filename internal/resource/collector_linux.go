// SPDX-License-Identifier: Apache-2.0

//go:build linux

package resource

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	defaultProcessCollector ProcessCollector = linuxProcessCollector{}
	defaultSystemCollector  SystemCollector  = linuxSystemCollector{}
)

type linuxProcessCollector struct{}

func (linuxProcessCollector) Collect(pid int) (Sample, error) {
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	statusPath := fmt.Sprintf("/proc/%d/status", pid)

	statBytes, err := os.ReadFile(statPath)
	if err != nil {
		return Sample{}, fmt.Errorf("resource: read %s: %w", statPath, err)
	}
	// /proc/<pid>/stat: pid (comm) state ... the comm field may contain spaces,
	// so we parse relative to the LAST ')'.
	raw := string(statBytes)
	rParen := strings.LastIndex(raw, ")")
	if rParen < 0 {
		return Sample{}, fmt.Errorf("resource: malformed stat line for pid %d", pid)
	}
	fields := strings.Fields(raw[rParen+1:])
	if len(fields) < 22 {
		return Sample{}, fmt.Errorf("resource: stat line too short for pid %d", pid)
	}
	rssPages, _ := strconv.ParseUint(fields[21], 10, 64)
	numThreads, _ := strconv.Atoi(fields[17])

	s := Sample{
		PID:        pid,
		RSS:        rssPages * uint64(os.Getpagesize()),
		NumThreads: numThreads,
		SampledAt:  time.Now(),
	}

	// VmSize and open FDs from status and /proc/<pid>/fd.
	if status, err := os.Open(statusPath); err == nil {
		scanner := bufio.NewScanner(status)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "VmSize:") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					if kb, err := strconv.ParseUint(parts[1], 10, 64); err == nil {
						s.VMS = kb * 1024
					}
				}
				break
			}
		}
		_ = status.Close()
	}

	if entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid)); err == nil {
		s.NumFDs = len(entries)
	}

	return s, nil
}

type linuxSystemCollector struct{}

func (linuxSystemCollector) Collect() (SystemSample, error) {
	s := SystemSample{NumCPU: runtime.NumCPU(), SampledAt: time.Now()}

	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return s, fmt.Errorf("resource: open meminfo: %w", err)
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		val, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			continue
		}
		switch parts[0] {
		case "MemTotal:":
			s.TotalMemory = val * 1024
		case "MemAvailable:":
			s.AvailableMemory = val * 1024
		}
	}

	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		parts := strings.Fields(string(b))
		if len(parts) >= 2 {
			if v, err := strconv.ParseFloat(parts[0], 64); err == nil {
				s.LoadAvg1 = v
			}
			if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
				s.LoadAvg5 = v
			}
		}
	}

	return s, nil
}
