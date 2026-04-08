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
