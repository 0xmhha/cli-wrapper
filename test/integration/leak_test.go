// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xmhha/cli-wrapper/internal/supervise"
	"github.com/0xmhha/cli-wrapper/pkg/cliwrap"
)

func TestIntegration_NoLeaksAcrossCycles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping leak stress in -short mode")
	}
	defer goleak.VerifyNone(t)

	agentBin := supervise.BuildAgentForTest(t)

	mgr, err := cliwrap.NewManager(
		cliwrap.WithAgentPath(agentBin),
		cliwrap.WithRuntimeDir(t.TempDir()),
	)
	require.NoError(t, err)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, mgr.Shutdown(ctx))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("leak-test-%d", i)
		spec, err := cliwrap.NewSpec(id, "/bin/sh", "-c", "exit 0").
			WithStopTimeout(time.Second).
			Build()
		require.NoError(t, err)

		h, err := mgr.Register(spec)
		require.NoError(t, err)
		require.NoError(t, h.Start(ctx))
		require.Eventually(t, func() bool {
			return h.Status().State == cliwrap.StateStopped
		}, 3*time.Second, 20*time.Millisecond)
		require.NoError(t, h.Close(ctx))
	}
}
