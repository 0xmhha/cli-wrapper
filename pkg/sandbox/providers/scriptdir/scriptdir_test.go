// SPDX-License-Identifier: Apache-2.0

package scriptdir

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
	"github.com/0xmhha/cli-wrapper/pkg/sandbox"
)

func TestScriptdir_EndToEnd(t *testing.T) {
	p := New(Options{BaseDir: t.TempDir()})
	require.Equal(t, "scriptdir", p.Name())

	script := sandbox.NewEntrypointScript("/bin/echo", []string{"hello", "sandbox"}, map[string]string{"X": "1"})

	inst, err := p.Prepare(context.Background(), cwtypes.SandboxSpec{}, []sandbox.Script{script})
	require.NoError(t, err)
	defer inst.Teardown(context.Background())

	// The script file must exist inside the instance dir.
	dir := inst.(*instance).dir
	require.FileExists(t, filepath.Join(dir, "cliwrap/entrypoint.sh"))

	cmd, err := inst.Exec("/bin/echo", []string{"hello", "sandbox"}, map[string]string{"X": "1"})
	require.NoError(t, err)

	var out bytes.Buffer
	cmd.Stdout = &out
	require.NoError(t, cmd.Run())
	require.Contains(t, out.String(), "hello sandbox")
}
