package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewEntrypointScript(t *testing.T) {
	s := NewEntrypointScript("/bin/echo", []string{"hello", "world"}, map[string]string{"X": "1"})
	body := string(s.Contents)

	require.True(t, strings.HasPrefix(body, "#!/bin/sh"))
	require.Contains(t, body, "export X=1")
	require.Contains(t, body, "exec /bin/echo hello world")
	require.Equal(t, "/cliwrap/entrypoint.sh", s.InnerPath)
	require.Equal(t, os.FileMode(0o755), s.Mode)
}

func TestShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"simple", "simple"},
		{"has space", "'has space'"},
		{"has'quote", `'has'\''quote'`},
		{"", "''"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, shellQuote(c.in))
	}
}

func TestWriteInto(t *testing.T) {
	dir := t.TempDir()
	s := Script{InnerPath: "/cliwrap/run.sh", Mode: 0o755, Contents: []byte("echo ok\n")}

	require.NoError(t, s.WriteInto(dir))
	b, err := os.ReadFile(filepath.Join(dir, "cliwrap/run.sh"))
	require.NoError(t, err)
	require.Equal(t, "echo ok\n", string(b))
}
