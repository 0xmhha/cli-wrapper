package platform

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFindExecutable_LocatesSystemBinary(t *testing.T) {
	path, err := Default().FindExecutable("sh")
	require.NoError(t, err)
	require.NotEmpty(t, path)
}

func TestFindExecutable_ReturnsErrorForMissing(t *testing.T) {
	_, err := Default().FindExecutable("this-binary-does-not-exist-xyzzy")
	require.Error(t, err)
}

func TestSendSignalAndKill(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	require.NoError(t, cmd.Start())
	defer cmd.Wait()

	time.Sleep(50 * time.Millisecond)
	require.NoError(t, Default().SendSignal(cmd.Process.Pid, syscall.SIGTERM))

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = Default().Kill(cmd.Process.Pid)
		t.Fatal("process did not exit after SIGTERM")
	}
	_ = os.Getpid() // keep os import used
}
