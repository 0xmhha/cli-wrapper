package noop

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

func TestNoop_Lifecycle(t *testing.T) {
	p := New()
	require.Equal(t, "noop", p.Name())

	inst, err := p.Prepare(context.Background(), cwtypes.SandboxSpec{}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, inst.ID())

	cmd, err := inst.Exec("/bin/echo", []string{"hello"}, map[string]string{"X": "1"})
	require.NoError(t, err)
	require.Equal(t, "/bin/echo", cmd.Path)
	require.Contains(t, cmd.Env, "X=1")

	require.NoError(t, inst.Teardown(context.Background()))
}
