package ipc

import (
	"encoding/binary"
	"errors"
)

// Protocol constants.
const (
	// MagicByte0 and MagicByte1 mark the start of every frame.
	// A mismatch triggers immediate connection termination.
	MagicByte0 byte = 0xC1
	MagicByte1 byte = 0xD9

	// ProtocolVersion is the major version of the wire protocol.
	// Incompatible changes must bump this value.
	ProtocolVersion byte = 0x01

	// HeaderSize is the fixed size of every frame header.
	// Layout: Magic(2) + Version(1) + MsgType(1) + Flags(1) + SeqNo(8) + Length(4)
	HeaderSize = 17

	// MaxPayloadSize is the largest payload a single frame may carry.
	// Anything larger must be delivered via FILE_REF.
	MaxPayloadSize uint32 = 16 * 1024 * 1024
)

// Flags is the bitmask carried in every frame header.
type Flags uint8

// Flag bits.
const (
	FlagAckRequired Flags = 0x01
	FlagIsReplay    Flags = 0x02
	FlagFileRef     Flags = 0x04
)

// Has reports whether all bits in mask are set.
func (f Flags) Has(mask Flags) bool {
	return f&mask == mask
}

// MsgType identifies the payload shape of a frame.
type MsgType uint8

// Control-plane message types (host -> agent).
const (
	MsgHello       MsgType = 0x01
	MsgStartChild  MsgType = 0x02
	MsgStopChild   MsgType = 0x03
	MsgSignalChild MsgType = 0x04
	MsgWriteStdin  MsgType = 0x05
	MsgSetLogLevel MsgType = 0x06
	MsgPing        MsgType = 0x07
	MsgShutdown    MsgType = 0x08
	MsgAckControl  MsgType = 0x0A // host ACKs a data-plane message
	MsgResume      MsgType = 0x0B
)

// Data-plane message types (agent -> host).
const (
	MsgHelloAck       MsgType = 0x81
	MsgChildStarted   MsgType = 0x82
	MsgChildExited    MsgType = 0x83
	MsgLogChunk       MsgType = 0x84
	MsgLogFileRef     MsgType = 0x85
	MsgChildError     MsgType = 0x86
	MsgPong           MsgType = 0x87
	MsgResourceSample MsgType = 0x88
	MsgAgentFatal     MsgType = 0x89
	MsgAckData        MsgType = 0x8A // agent ACKs a control-plane message
)

// Header is the fixed-size frame prefix that precedes every payload.
type Header struct {
	MsgType MsgType
	Flags   Flags
	SeqNo   uint64
	Length  uint32
}

// Sentinel decode errors.
var (
	ErrShortHeader        = errors.New("ipc: frame header is too short")
	ErrInvalidMagic       = errors.New("ipc: frame magic bytes mismatch")
	ErrUnsupportedVersion = errors.New("ipc: unsupported protocol version")
	ErrFrameTooLarge      = errors.New("ipc: frame length exceeds maximum")
)

// Encode serializes the header into dst. dst must be at least HeaderSize bytes.
func (h Header) Encode(dst []byte) {
	_ = dst[HeaderSize-1] // bounds check hint
	dst[0] = MagicByte0
	dst[1] = MagicByte1
	dst[2] = ProtocolVersion
	dst[3] = byte(h.MsgType)
	dst[4] = byte(h.Flags)
	binary.BigEndian.PutUint64(dst[5:13], h.SeqNo)
	binary.BigEndian.PutUint32(dst[13:17], h.Length)
}

// Decode parses the header from src. src must be at least HeaderSize bytes.
func (h *Header) Decode(src []byte) error {
	if len(src) < HeaderSize {
		return ErrShortHeader
	}
	if src[0] != MagicByte0 || src[1] != MagicByte1 {
		return ErrInvalidMagic
	}
	if src[2] != ProtocolVersion {
		return ErrUnsupportedVersion
	}
	h.MsgType = MsgType(src[3])
	h.Flags = Flags(src[4])
	h.SeqNo = binary.BigEndian.Uint64(src[5:13])
	h.Length = binary.BigEndian.Uint32(src[13:17])
	if h.Length > MaxPayloadSize {
		return ErrFrameTooLarge
	}
	return nil
}
