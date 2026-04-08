package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/cli-wrapper/cli-wrapper/pkg/cliwrap"
)

// LoadFile reads and parses a YAML config at path.
func LoadFile(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return Load(b)
}

// Load parses raw YAML bytes.
func Load(data []byte) (*Config, error) {
	expanded, err := ExpandEnv(string(data))
	if err != nil {
		return nil, err
	}

	var raw rawConfig
	if err := yaml.Unmarshal([]byte(expanded), &raw); err != nil {
		return nil, fmt.Errorf("config: yaml: %w", err)
	}

	cfg, err := raw.toCanonical()
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate checks for duplicate IDs and required fields across all groups.
func (c *Config) Validate() error {
	seen := map[string]struct{}{}
	for gi, g := range c.Groups {
		for pi, p := range g.Processes {
			if err := p.Validate(); err != nil {
				return fmt.Errorf("config: groups[%d].processes[%d]: %w", gi, pi, err)
			}
			if _, dup := seen[p.ID]; dup {
				return fmt.Errorf("config: duplicate process id %q", p.ID)
			}
			seen[p.ID] = struct{}{}
		}
	}
	return nil
}

type rawConfig struct {
	Version          string            `yaml:"version"`
	Runtime          rawRuntime        `yaml:"runtime"`
	SystemBudget     rawSystemBudget   `yaml:"system_budget"`
	SandboxProviders []rawSandboxProv  `yaml:"sandbox_providers"`
	Groups           []rawProcessGroup `yaml:"groups"`
}

type rawRuntime struct {
	Dir       string `yaml:"dir"`
	Debug     string `yaml:"debug"`
	AgentPath string `yaml:"agent_path"`
}

type rawSystemBudget struct {
	MaxMemoryPercent float64 `yaml:"max_memory_percent"`
	MinFreeMemory    string  `yaml:"min_free_memory"`
	MaxLoadAvg       float64 `yaml:"max_load_avg"`
}

type rawSandboxProv struct {
	Name string `yaml:"name"`
}

type rawProcessGroup struct {
	Name      string       `yaml:"name"`
	Processes []rawProcess `yaml:"processes"`
}

type rawProcess struct {
	ID             string            `yaml:"id"`
	Name           string            `yaml:"name"`
	Command        string            `yaml:"command"`
	Args           []string          `yaml:"args"`
	Env            map[string]string `yaml:"env"`
	WorkDir        string            `yaml:"workdir"`
	Restart        string            `yaml:"restart"`
	MaxRestarts    int               `yaml:"max_restarts"`
	RestartBackoff string            `yaml:"restart_backoff"`
	StopTimeout    string            `yaml:"stop_timeout"`
	Stdin          string            `yaml:"stdin"`
	Resources      rawResources      `yaml:"resources"`
}

type rawResources struct {
	MaxRSS        string  `yaml:"max_rss"`
	MaxCPUPercent float64 `yaml:"max_cpu_percent"`
	OnExceed      string  `yaml:"on_exceed"`
}

func (r rawConfig) toCanonical() (*Config, error) {
	cfg := &Config{
		Version: r.Version,
		Runtime: RuntimeConfig{
			Dir:       r.Runtime.Dir,
			Debug:     r.Runtime.Debug,
			AgentPath: r.Runtime.AgentPath,
		},
	}

	if r.SystemBudget.MinFreeMemory != "" {
		v, err := ParseByteSize(r.SystemBudget.MinFreeMemory)
		if err != nil {
			return nil, err
		}
		cfg.SystemBudget.MinFreeMemory = v
	}
	cfg.SystemBudget.MaxMemoryPercent = r.SystemBudget.MaxMemoryPercent
	cfg.SystemBudget.MaxLoadAvg = r.SystemBudget.MaxLoadAvg

	for _, p := range r.SandboxProviders {
		cfg.SandboxProviders = append(cfg.SandboxProviders, p.Name)
	}

	for _, g := range r.Groups {
		group := ProcessGroup{Name: g.Name}
		for _, raw := range g.Processes {
			spec, err := rawProcessToSpec(raw)
			if err != nil {
				return nil, err
			}
			group.Processes = append(group.Processes, spec)
		}
		cfg.Groups = append(cfg.Groups, group)
	}
	return cfg, nil
}

func rawProcessToSpec(r rawProcess) (cliwrap.Spec, error) {
	spec := cliwrap.Spec{
		ID:          r.ID,
		Name:        r.Name,
		Command:     r.Command,
		Args:        r.Args,
		Env:         r.Env,
		WorkDir:     r.WorkDir,
		MaxRestarts: r.MaxRestarts,
	}

	if r.Restart != "" {
		p, err := parseRestart(r.Restart)
		if err != nil {
			return spec, err
		}
		spec.Restart = p
	}
	if r.RestartBackoff != "" {
		d, err := time.ParseDuration(r.RestartBackoff)
		if err != nil {
			return spec, fmt.Errorf("config: restart_backoff: %w", err)
		}
		spec.RestartBackoff = d
	}
	if r.StopTimeout != "" {
		d, err := time.ParseDuration(r.StopTimeout)
		if err != nil {
			return spec, fmt.Errorf("config: stop_timeout: %w", err)
		}
		spec.StopTimeout = d
	}
	if r.Stdin != "" {
		m, err := parseStdin(r.Stdin)
		if err != nil {
			return spec, err
		}
		spec.Stdin = m
	}
	if r.Resources.MaxRSS != "" {
		v, err := ParseByteSize(r.Resources.MaxRSS)
		if err != nil {
			return spec, err
		}
		spec.Resources.MaxRSS = v
	}
	spec.Resources.MaxCPUPercent = r.Resources.MaxCPUPercent
	if r.Resources.OnExceed != "" {
		a, err := parseExceed(r.Resources.OnExceed)
		if err != nil {
			return spec, err
		}
		spec.Resources.OnExceed = a
	}
	return spec, nil
}

func parseRestart(s string) (cliwrap.RestartPolicy, error) {
	switch s {
	case "never":
		return cliwrap.RestartNever, nil
	case "on_failure":
		return cliwrap.RestartOnFailure, nil
	case "always":
		return cliwrap.RestartAlways, nil
	}
	return 0, fmt.Errorf("config: unknown restart policy %q", s)
}

func parseStdin(s string) (cliwrap.StdinMode, error) {
	switch s {
	case "none":
		return cliwrap.StdinNone, nil
	case "pipe":
		return cliwrap.StdinPipe, nil
	case "inherit":
		return cliwrap.StdinInherit, nil
	}
	return 0, fmt.Errorf("config: unknown stdin mode %q", s)
}

func parseExceed(s string) (cliwrap.ExceedAction, error) {
	switch s {
	case "warn":
		return cliwrap.ExceedWarn, nil
	case "throttle":
		return cliwrap.ExceedThrottle, nil
	case "kill":
		return cliwrap.ExceedKill, nil
	}
	return 0, fmt.Errorf("config: unknown on_exceed %q", s)
}
