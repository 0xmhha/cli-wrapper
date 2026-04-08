// SPDX-License-Identifier: Apache-2.0

package supervise

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWatcher_EmitsExitCode(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 3")
	require.NoError(t, cmd.Start())

	w := NewWatcher(cmd.Process)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := w.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, res.ExitCode)
	require.Equal(t, cmd.Process.Pid, res.PID)
}
