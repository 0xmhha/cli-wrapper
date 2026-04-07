package ipc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFrameConstants(t *testing.T) {
	require.Equal(t, byte(0xC1), MagicByte0)
	require.Equal(t, byte(0xD9), MagicByte1)
	require.Equal(t, byte(0x01), ProtocolVersion)
	require.Equal(t, 17, HeaderSize)
	require.Equal(t, uint32(16*1024*1024), MaxPayloadSize)
}

func TestFlagsBitmask(t *testing.T) {
	require.Equal(t, Flags(0x01), FlagAckRequired)
	require.Equal(t, Flags(0x02), FlagIsReplay)
	require.Equal(t, Flags(0x04), FlagFileRef)

	f := FlagAckRequired | FlagIsReplay
	require.True(t, f.Has(FlagAckRequired))
	require.True(t, f.Has(FlagIsReplay))
	require.False(t, f.Has(FlagFileRef))
}
