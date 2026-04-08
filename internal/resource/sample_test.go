package resource

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSample_Zero(t *testing.T) {
	s := Sample{}
	require.Equal(t, 0, s.PID)
	require.Equal(t, uint64(0), s.RSS)
}

func TestSystemSample_AvailableRatio(t *testing.T) {
	s := SystemSample{TotalMemory: 1000, AvailableMemory: 250}
	require.InDelta(t, 0.25, s.AvailableRatio(), 0.0001)
}

func TestSystemSample_AvailableRatio_ZeroTotal(t *testing.T) {
	require.Equal(t, 0.0, SystemSample{}.AvailableRatio())
}

func TestSample_Validate_Timestamp(t *testing.T) {
	s := Sample{SampledAt: time.Now()}
	require.False(t, s.SampledAt.IsZero())
}
