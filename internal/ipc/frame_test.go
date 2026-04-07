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

func TestHeaderRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		h    Header
	}{
		{"zero", Header{}},
		{"typical", Header{
			MsgType: MsgLogChunk,
			Flags:   FlagAckRequired,
			SeqNo:   0xDEADBEEFCAFEBABE,
			Length:   1024,
		}},
		{"max length", Header{
			MsgType: MsgLogFileRef,
			Flags:   FlagFileRef | FlagAckRequired,
			SeqNo:   1,
			Length:   MaxPayloadSize,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf [HeaderSize]byte
			tc.h.Encode(buf[:])

			var got Header
			require.NoError(t, got.Decode(buf[:]))
			require.Equal(t, tc.h, got)
		})
	}
}

func TestHeaderDecodeErrors(t *testing.T) {
	good := Header{MsgType: MsgHello, SeqNo: 1, Length: 8}
	var buf [HeaderSize]byte
	good.Encode(buf[:])

	t.Run("short buffer", func(t *testing.T) {
		var h Header
		require.ErrorIs(t, h.Decode(buf[:HeaderSize-1]), ErrShortHeader)
	})

	t.Run("bad magic", func(t *testing.T) {
		bad := buf
		bad[0] = 0x00
		var h Header
		require.ErrorIs(t, h.Decode(bad[:]), ErrInvalidMagic)
	})

	t.Run("bad version", func(t *testing.T) {
		bad := buf
		bad[2] = 0xFF
		var h Header
		require.ErrorIs(t, h.Decode(bad[:]), ErrUnsupportedVersion)
	})

	t.Run("oversized length", func(t *testing.T) {
		bad := buf
		// bytes 13..17 are the Length field (big-endian)
		bad[13] = 0xFF
		bad[14] = 0xFF
		bad[15] = 0xFF
		bad[16] = 0xFF
		var h Header
		require.ErrorIs(t, h.Decode(bad[:]), ErrFrameTooLarge)
	})
}
