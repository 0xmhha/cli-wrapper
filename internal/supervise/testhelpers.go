package supervise

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	testAgentOnce sync.Once
	testAgentPath string
)

// BuildAgentForTest compiles the cliwrap-agent binary once per test process
// and returns its path.
func BuildAgentForTest(t *testing.T) string {
	t.Helper()
	testAgentOnce.Do(func() {
		tmp, err := os.MkdirTemp("", "cliwrap-agent-*")
		require.NoError(t, err)
		testAgentPath = filepath.Join(tmp, "cliwrap-agent")
		cmd := exec.Command("go", "build", "-o", testAgentPath, "./cmd/cliwrap-agent")
		cmd.Dir = findModuleRoot(t)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	})
	return testAgentPath
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatal("go.mod not found")
		}
		wd = parent
	}
}
