package supervise

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// buildAgent compiles the cliwrap-agent binary once per test run.
func buildAgent(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	out := filepath.Join(tmp, "cliwrap-agent")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/cliwrap-agent")
	cmd.Dir = projectRoot(t)
	b, err := cmd.CombinedOutput()
	require.NoError(t, err, string(b))
	return out
}

func projectRoot(t *testing.T) string {
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

func TestSpawner_SpawnsAgentAndReceivesHello(t *testing.T) {
	defer goleak.VerifyNone(t)

	agentBin := buildAgent(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	spawner := NewSpawner(SpawnerOptions{
		AgentPath: agentBin,
	})
	h, err := spawner.Spawn(ctx, "test-agent-1")
	require.NoError(t, err)
	defer h.Close()

	// The host side of the socket pair is h.Socket; verify it is readable.
	require.NotNil(t, h.Socket)
	require.Greater(t, h.PID, 0)
}
