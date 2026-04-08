package sandbox

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

type fakeInstance struct {
	id  string
	cmd *exec.Cmd
}

func (f *fakeInstance) ID() string { return f.id }
func (f *fakeInstance) Exec(cmd string, args []string, env map[string]string) (*exec.Cmd, error) {
	return exec.Command(cmd, args...), nil
}
func (f *fakeInstance) Teardown(ctx context.Context) error { return nil }

type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }
func (fakeProvider) Prepare(ctx context.Context, spec cwtypes.SandboxSpec, scripts []Script) (Instance, error) {
	return &fakeInstance{id: "fake-1"}, nil
}

func TestProviderInterface_AcceptsFake(t *testing.T) {
	var p Provider = fakeProvider{}
	inst, err := p.Prepare(context.Background(), cwtypes.SandboxSpec{}, nil)
	require.NoError(t, err)
	require.Equal(t, "fake-1", inst.ID())

	cmd, err := inst.Exec("/bin/echo", []string{"hi"}, nil)
	require.NoError(t, err)
	require.NotNil(t, cmd)
	require.NoError(t, inst.Teardown(context.Background()))
}
