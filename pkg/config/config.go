// SPDX-License-Identifier: Apache-2.0

package config

import (
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

// Config is the canonical in-memory representation of a loaded YAML file.
type Config struct {
	Version          string
	Runtime          RuntimeConfig
	SystemBudget     SystemBudgetConfig
	SandboxProviders []string
	Groups           []ProcessGroup
}

// RuntimeConfig mirrors the "runtime" YAML block.
type RuntimeConfig struct {
	Dir       string
	Debug     string // off | info | verbose | trace
	AgentPath string
	// LogRingBufferBytes overrides the per-(process, stream) log ring
	// buffer capacity. Zero means use cliwrap.DefaultLogRingBufferBytes
	// (1 MiB). YAML key: runtime.log_ring_buffer_bytes.
	LogRingBufferBytes int
}

// SystemBudgetConfig mirrors the "system_budget" YAML block.
type SystemBudgetConfig struct {
	MaxMemoryPercent float64
	MinFreeMemory    uint64
	MaxLoadAvg       float64
}

// ProcessGroup is a named collection of process specs.
type ProcessGroup struct {
	Name      string
	Processes []cliwrap.Spec
}
