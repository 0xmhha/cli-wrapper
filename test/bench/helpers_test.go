// SPDX-License-Identifier: Apache-2.0

//go:build bench

package bench

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var (
	benchAgentOnce sync.Once
	benchAgentPath string
	benchAgentErr  error
)

// buildAgentForBench compiles the cliwrap-agent binary once per benchmark
// process and returns its path.
func buildAgentForBench(tb testing.TB) string {
	tb.Helper()
	benchAgentOnce.Do(func() {
		tmp, err := os.MkdirTemp("", "cliwrap-agent-bench-*")
		if err != nil {
			benchAgentErr = err
			return
		}
		benchAgentPath = filepath.Join(tmp, "cliwrap-agent")
		cmd := exec.Command("go", "build", "-o", benchAgentPath, "./cmd/cliwrap-agent")
		cmd.Dir = findBenchModuleRoot(tb)
		out, err := cmd.CombinedOutput()
		if err != nil {
			benchAgentErr = &agentBuildError{msg: string(out), cause: err}
		}
	})
	if benchAgentErr != nil {
		tb.Fatalf("buildAgentForBench: %v", benchAgentErr)
	}
	return benchAgentPath
}

func findBenchModuleRoot(tb testing.TB) string {
	tb.Helper()
	wd, err := os.Getwd()
	if err != nil {
		tb.Fatalf("findBenchModuleRoot: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			tb.Fatal("findBenchModuleRoot: go.mod not found")
		}
		wd = parent
	}
}

type agentBuildError struct {
	msg   string
	cause error
}

func (e *agentBuildError) Error() string {
	return "agent build failed: " + e.cause.Error() + "\n" + e.msg
}
