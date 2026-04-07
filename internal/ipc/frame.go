package ipc

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
