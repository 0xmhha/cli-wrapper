package resource

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cli-wrapper/cli-wrapper/pkg/cliwrap"
)

func TestEvaluator_EmitsActionAfterSustainedBreach(t *testing.T) {
	var actions []Action
	ev := NewEvaluator(EvaluatorOptions{
		OnAction: func(a Action) { actions = append(actions, a) },
	})

	ev.RegisterLimits("p1", cliwrap.ResourceLimits{MaxRSS: 100, OnExceed: cliwrap.ExceedKill})

	// Two breaches are below the threshold of 3.
	ev.Evaluate("p1", Sample{PID: 1, RSS: 500})
	ev.Evaluate("p1", Sample{PID: 1, RSS: 500})
	require.Empty(t, actions)

	// Third breach should trigger.
	ev.Evaluate("p1", Sample{PID: 1, RSS: 500})
	require.Len(t, actions, 1)
	require.Equal(t, "p1", actions[0].ProcessID)
	require.Equal(t, cliwrap.ExceedKill, actions[0].ExceedAction)
	require.Equal(t, "rss", actions[0].Kind)
}

func TestEvaluator_ResetsOnGoodSample(t *testing.T) {
	var actions []Action
	ev := NewEvaluator(EvaluatorOptions{
		OnAction: func(a Action) { actions = append(actions, a) },
	})
	ev.RegisterLimits("p1", cliwrap.ResourceLimits{MaxRSS: 100, OnExceed: cliwrap.ExceedKill})

	ev.Evaluate("p1", Sample{RSS: 500})
	ev.Evaluate("p1", Sample{RSS: 500})
	ev.Evaluate("p1", Sample{RSS: 50}) // resets
	ev.Evaluate("p1", Sample{RSS: 500})
	require.Empty(t, actions)
}

func TestEvaluator_CPUNeedsMoreBreaches(t *testing.T) {
	var actions []Action
	ev := NewEvaluator(EvaluatorOptions{
		OnAction: func(a Action) { actions = append(actions, a) },
	})
	ev.RegisterLimits("p1", cliwrap.ResourceLimits{MaxCPUPercent: 50, OnExceed: cliwrap.ExceedWarn})

	// 9 breaches should still be below the CPU threshold of 10.
	for i := 0; i < 9; i++ {
		ev.Evaluate("p1", Sample{CPUPercent: 99})
	}
	require.Empty(t, actions)

	ev.Evaluate("p1", Sample{CPUPercent: 99}) // 10th
	require.Len(t, actions, 1)
	require.Equal(t, "cpu", actions[0].Kind)
}
