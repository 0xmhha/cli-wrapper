// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func findRoot(t *testing.T) string {
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

// fixturesMu guards fixturesCache. Each fixture is built at most once per
// test process; the resulting path is reused across tests.
var (
	fixturesMu    sync.Mutex
	fixturesCache = map[string]string{}
)

// BuildFixtureForTest compiles a fixture binary by short name (e.g.
// "noisy", "crasher", "echo", "hanger") on demand and returns its
// absolute path. The build is cached per name across the test process.
//
// Without this helper, integration tests had to rely on `make fixtures`
// having been run beforehand. When the fixture binary was missing, the
// agent's exec of the non-existent path failed silently — the controller
// never saw MsgChildStarted, and tests timed out at "process should reach
// Running state" with no hint that the underlying cause was a missing
// build artifact. Building on demand removes that footgun: `go test
// ./test/integration/...` self-supplies its own fixtures.
func BuildFixtureForTest(t *testing.T, name string) string {
	t.Helper()
	fixturesMu.Lock()
	defer fixturesMu.Unlock()

	if path, ok := fixturesCache[name]; ok {
		return path
	}

	root := findRoot(t)
	tmp, err := os.MkdirTemp("", "cliwrap-fixture-"+name+"-*")
	require.NoError(t, err)
	out := filepath.Join(tmp, "fixture-"+name)

	cmd := exec.Command("go", "build", "-o", out, "./test/fixtures/"+name)
	cmd.Dir = root
	co, err := cmd.CombinedOutput()
	require.NoError(t, err, "build fixture %q failed: %s", name, string(co))

	fixturesCache[name] = out
	return out
}
