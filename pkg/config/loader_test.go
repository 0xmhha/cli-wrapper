// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoader_HappyPath(t *testing.T) {
	yamlBody := `
version: "1"
runtime:
  dir: /tmp/cliwrap
  debug: info
groups:
  - name: web
    processes:
      - id: redis
        name: Redis
        command: /usr/bin/redis-server
        args: ["--port", "6379"]
        restart: on_failure
        max_restarts: 3
        restart_backoff: 2s
        stop_timeout: 5s
        resources:
          max_rss: "256MiB"
          on_exceed: kill
`
	path := writeTempFile(t, "config.yaml", yamlBody)
	cfg, err := LoadFile(path)
	require.NoError(t, err)
	require.Equal(t, "1", cfg.Version)
	require.Equal(t, "/tmp/cliwrap", cfg.Runtime.Dir)
	require.Len(t, cfg.Groups, 1)
	require.Len(t, cfg.Groups[0].Processes, 1)

	p := cfg.Groups[0].Processes[0]
	require.Equal(t, "redis", p.ID)
	require.Equal(t, "/usr/bin/redis-server", p.Command)
	require.Equal(t, cliwrap.RestartOnFailure, p.Restart)
	require.Equal(t, uint64(256*1024*1024), p.Resources.MaxRSS)
	require.Equal(t, cliwrap.ExceedKill, p.Resources.OnExceed)
}

func TestLoader_DuplicateIDsRejected(t *testing.T) {
	yamlBody := `
version: "1"
groups:
  - name: a
    processes:
      - id: dup
        command: /bin/echo
      - id: dup
        command: /bin/echo
`
	path := writeTempFile(t, "config.yaml", yamlBody)
	_, err := LoadFile(path)
	require.Error(t, err)
}

func TestLoader_EnvExpansion(t *testing.T) {
	t.Setenv("BIN", "/bin/echo")
	yamlBody := `
version: "1"
groups:
  - name: a
    processes:
      - id: e
        command: "${BIN}"
`
	path := writeTempFile(t, "config.yaml", yamlBody)
	cfg, err := LoadFile(path)
	require.NoError(t, err)
	require.Equal(t, "/bin/echo", cfg.Groups[0].Processes[0].Command)
}
