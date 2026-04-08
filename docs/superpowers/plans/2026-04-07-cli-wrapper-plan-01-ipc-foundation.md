# cli-wrapper Plan 01 — IPC Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the core IPC layer (framing, codec, messages, persistent outbox with WAL, UDS connection wrapper) that every other subsystem of cli-wrapper will depend on.

**Architecture:** A length-prefixed framed protocol carried over a Unix Domain Socket, with MessagePack-encoded payloads. A persistent outbox guarantees zero message loss: the in-memory bounded channel spills to a disk WAL on overflow, and a sender goroutine drains both. Every frame carries a monotonic `SeqNo` and may require receiver ACK for retirement.

**Tech Stack:**
- Go 1.22+ (module-based project)
- `github.com/vmihailenco/msgpack/v5` — MessagePack codec
- `github.com/stretchr/testify/require` — test assertions
- `go.uber.org/goleak` — goroutine leak detector (CI gate)
- Standard library for UDS (`net`), atomic (`sync/atomic`), and file I/O

**Context for the implementer:**
- The project root is `/Users/wm-it-22-00661/Work/github/study/ai/cli-wrapper` (currently empty except for `.git`).
- The canonical spec lives at `docs/superpowers/specs/2026-04-07-cli-wrapper-design.md` — refer to it when any detail here seems underspecified, especially Sections 2.3–2.8.
- Strict TDD: write the failing test first, then the minimal implementation.
- `gofmt` and `go vet` must pass before every commit.
- Commit messages follow conventional commits (`feat:`, `test:`, `chore:`, `docs:`, `fix:`).

---

## File Structure

All files for this plan are inside `internal/ipc/` unless noted. This plan does not touch any other package.

| File | Responsibility |
|------|----------------|
| `go.mod` | Module declaration + dependencies |
| `Makefile` | Convenience targets (`test`, `lint`, `build`) |
| `.gitignore` | Ignore build artifacts and editor files |
| `internal/ipc/doc.go` | Package doc comment |
| `internal/ipc/frame.go` | Frame header struct, constants, encode/decode of the 17-byte header |
| `internal/ipc/frame_test.go` | Header round-trip and decode error tests |
| `internal/ipc/writer.go` | `FrameWriter` — buffered writer that emits whole frames to an `io.Writer` |
| `internal/ipc/writer_test.go` | Writer tests with `bytes.Buffer` fake |
| `internal/ipc/reader.go` | `FrameReader` — reads and validates frames from an `io.Reader` |
| `internal/ipc/reader_test.go` | Reader tests (happy path, corrupt magic, truncated payload, oversized frame) |
| `internal/ipc/reader_fuzz_test.go` | Fuzz target for the decoder |
| `internal/ipc/message.go` | `MsgType` enum, `Flags` bitmask, all payload structs |
| `internal/ipc/message_test.go` | MessagePack round-trip tests for every payload struct |
| `internal/ipc/codec.go` | Thin wrappers around `msgpack.Marshal` / `msgpack.Unmarshal` |
| `internal/ipc/codec_test.go` | Codec happy path + error case tests |
| `internal/ipc/seq.go` | Monotonic sequence-number generator + per-peer dedup tracker |
| `internal/ipc/seq_test.go` | Sequence generator and dedup tests |
| `internal/ipc/outbox.go` | `Outbox` — bounded in-memory queue with spill callback |
| `internal/ipc/outbox_test.go` | Outbox tests (enqueue, dequeue, overflow → spill) |
| `internal/ipc/wal.go` | `WAL` — append-only log file with replay iterator |
| `internal/ipc/wal_test.go` | WAL append, iterate, and crash-recovery tests |
| `internal/ipc/spiller.go` | `Spiller` — couples `Outbox` + `WAL` (delayed delivery) |
| `internal/ipc/spiller_test.go` | Spiller overflow/drain/replay tests |
| `internal/ipc/acktracker.go` | `AckTracker` — tracks unacked messages and retires them on ACK |
| `internal/ipc/acktracker_test.go` | Ack tracker tests |
| `internal/ipc/conn.go` | `Conn` — full bidirectional peer (outbox + reader + writer goroutines) |
| `internal/ipc/conn_test.go` | End-to-end tests over a `net.Pipe()` and a `socketpair` |
| `.github/workflows/ci.yml` | GitHub Actions: `go test`, `go vet`, `goleak` matrix |

---

## Task 1: Project Bootstrap

**Files:**
- Create: `go.mod`
- Create: `Makefile`
- Create: `.gitignore`
- Create: `internal/ipc/doc.go`

- [ ] **Step 1: Initialize the Go module**

Run:

```bash
cd /Users/wm-it-22-00661/Work/github/study/ai/cli-wrapper
go mod init github.com/0xmhha/cli-wrapper
```

Expected output: `go: creating new go.mod: module github.com/0xmhha/cli-wrapper`

- [ ] **Step 2: Set the Go version in `go.mod`**

Overwrite `go.mod` with exactly:

```
module github.com/0xmhha/cli-wrapper

go 1.22
```

- [ ] **Step 3: Add required dependencies**

Run:

```bash
go get github.com/vmihailenco/msgpack/v5@v5.4.1
go get github.com/stretchr/testify@v1.9.0
go get go.uber.org/goleak@v1.3.0
go mod tidy
```

Expected: `go.sum` is created, `go.mod` has `require` blocks for the three modules.

- [ ] **Step 4: Create `.gitignore`**

Create `.gitignore`:

```
# Binaries
/bin/
/dist/
cliwrap
cliwrap-agent

# Test artifacts
*.test
*.out
coverage.txt
testdata/fuzz/

# Editor
.idea/
.vscode/
*.swp
*~

# OS
.DS_Store
```

- [ ] **Step 5: Create `Makefile`**

Create `Makefile`:

```make
GO := go
PKG := ./...

.PHONY: test
test:
	$(GO) test -race -count=1 $(PKG)

.PHONY: test-short
test-short:
	$(GO) test -short -count=1 $(PKG)

.PHONY: vet
vet:
	$(GO) vet $(PKG)

.PHONY: fmt
fmt:
	$(GO) fmt $(PKG)

.PHONY: lint
lint: fmt vet

.PHONY: cover
cover:
	$(GO) test -race -coverprofile=coverage.txt -covermode=atomic $(PKG)
	$(GO) tool cover -func=coverage.txt | tail -n 1
```

- [ ] **Step 6: Create the package doc file**

Create `internal/ipc/doc.go`:

```go
// Package ipc implements the cli-wrapper inter-process communication layer.
//
// It provides a length-prefixed framed protocol over a Unix Domain Socket.
// Each frame carries a 17-byte header, a MessagePack-encoded payload, and
// a monotonic sequence number. The package also implements a persistent
// outbox with a write-ahead log (WAL) so that message delivery survives
// transient connection loss and producer bursts.
//
// This package is consumed by both the host-side controller and the
// cliwrap-agent subprocess.
package ipc
```

- [ ] **Step 7: Verify the tree builds**

Run:

```bash
go build ./...
```

Expected: exit code 0, no output.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum Makefile .gitignore internal/ipc/doc.go
git commit -m "chore: bootstrap Go module and ipc package skeleton"
```

---

## Task 2: Frame Header Constants and Types

**Files:**
- Create: `internal/ipc/frame.go`
- Create: `internal/ipc/frame_test.go`

- [ ] **Step 1: Write the failing test for frame constants**

Create `internal/ipc/frame_test.go`:

```go
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
```

- [ ] **Step 2: Run the test and confirm it fails to compile**

Run:

```bash
go test ./internal/ipc/ -run TestFrame -count=1
```

Expected: build failure — `undefined: MagicByte0` (and friends).

- [ ] **Step 3: Implement the constants and flag type**

Create `internal/ipc/frame.go`:

```go
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run:

```bash
go test ./internal/ipc/ -run TestFrame -count=1 -v
```

Expected: `PASS: TestFrameConstants`, `PASS: TestFlagsBitmask`.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc/frame.go internal/ipc/frame_test.go
git commit -m "feat(ipc): add frame header constants and flags bitmask"
```

---

## Task 3: Frame Header Encode/Decode

**Files:**
- Modify: `internal/ipc/frame.go`
- Modify: `internal/ipc/frame_test.go`

- [ ] **Step 1: Write the failing test for header round-trip**

Append to `internal/ipc/frame_test.go`:

```go
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
			Length:  1024,
		}},
		{"max length", Header{
			MsgType: MsgLogFileRef,
			Flags:   FlagFileRef | FlagAckRequired,
			SeqNo:   1,
			Length:  MaxPayloadSize,
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
```

- [ ] **Step 2: Run the tests and confirm they fail to compile**

Run:

```bash
go test ./internal/ipc/ -run TestHeader -count=1
```

Expected: compile error — `undefined: Header`, `undefined: MsgLogChunk`, etc.

- [ ] **Step 3: Implement `Header`, `MsgType`, and the error sentinels**

Replace `internal/ipc/frame.go` with the following complete file:

```go
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
	MsgHello        MsgType = 0x01
	MsgStartChild   MsgType = 0x02
	MsgStopChild    MsgType = 0x03
	MsgSignalChild  MsgType = 0x04
	MsgWriteStdin   MsgType = 0x05
	MsgSetLogLevel  MsgType = 0x06
	MsgPing         MsgType = 0x07
	MsgShutdown     MsgType = 0x08
	MsgAckControl   MsgType = 0x0A // host ACKs a data-plane message
	MsgResume       MsgType = 0x0B
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/ipc/ -run TestHeader -count=1 -v
```

Expected: all four subtests under `TestHeaderRoundTrip` and all four under `TestHeaderDecodeErrors` pass.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc/frame.go internal/ipc/frame_test.go
git commit -m "feat(ipc): implement frame header encode/decode with validation"
```

---

## Task 4: Frame Writer

**Files:**
- Create: `internal/ipc/writer.go`
- Create: `internal/ipc/writer_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/ipc/writer_test.go`:

```go
package ipc

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFrameWriter_WriteFrame(t *testing.T) {
	var buf bytes.Buffer
	w := NewFrameWriter(&buf)

	payload := []byte("hello world")
	n, err := w.WriteFrame(Header{
		MsgType: MsgPing,
		Flags:   0,
		SeqNo:   42,
		Length:  uint32(len(payload)),
	}, payload)
	require.NoError(t, err)
	require.Equal(t, HeaderSize+len(payload), n)

	raw := buf.Bytes()
	require.Len(t, raw, HeaderSize+len(payload))

	var h Header
	require.NoError(t, h.Decode(raw[:HeaderSize]))
	require.Equal(t, MsgPing, h.MsgType)
	require.Equal(t, uint64(42), h.SeqNo)
	require.Equal(t, uint32(len(payload)), h.Length)
	require.Equal(t, payload, raw[HeaderSize:])
}

func TestFrameWriter_RejectsOversizedPayload(t *testing.T) {
	var buf bytes.Buffer
	w := NewFrameWriter(&buf)

	hugeLen := MaxPayloadSize + 1
	_, err := w.WriteFrame(Header{MsgType: MsgPing, Length: hugeLen}, make([]byte, hugeLen))
	require.ErrorIs(t, err, ErrFrameTooLarge)
	require.Equal(t, 0, buf.Len(), "nothing should have been written")
}

func TestFrameWriter_LengthMustMatchPayload(t *testing.T) {
	var buf bytes.Buffer
	w := NewFrameWriter(&buf)

	_, err := w.WriteFrame(Header{MsgType: MsgPing, Length: 10}, []byte("short"))
	require.ErrorIs(t, err, ErrLengthMismatch)
}
```

- [ ] **Step 2: Run the test to verify it fails to compile**

Run:

```bash
go test ./internal/ipc/ -run TestFrameWriter -count=1
```

Expected: `undefined: NewFrameWriter`, `undefined: ErrLengthMismatch`.

- [ ] **Step 3: Implement `FrameWriter`**

Create `internal/ipc/writer.go`:

```go
package ipc

import (
	"errors"
	"io"
)

// ErrLengthMismatch indicates that the header's Length field does not match
// the size of the payload slice passed to WriteFrame.
var ErrLengthMismatch = errors.New("ipc: header length does not match payload size")

// FrameWriter serializes frames to an io.Writer.
// It is NOT safe for concurrent use; callers must serialize WriteFrame calls.
type FrameWriter struct {
	w       io.Writer
	scratch [HeaderSize]byte
}

// NewFrameWriter returns a FrameWriter that writes to w.
func NewFrameWriter(w io.Writer) *FrameWriter {
	return &FrameWriter{w: w}
}

// WriteFrame serializes h followed by payload to the underlying writer.
// It returns the total number of bytes written (header + payload) on success.
func (fw *FrameWriter) WriteFrame(h Header, payload []byte) (int, error) {
	if h.Length > MaxPayloadSize {
		return 0, ErrFrameTooLarge
	}
	if int(h.Length) != len(payload) {
		return 0, ErrLengthMismatch
	}
	h.Encode(fw.scratch[:])
	n, err := fw.w.Write(fw.scratch[:])
	if err != nil {
		return n, err
	}
	if len(payload) == 0 {
		return n, nil
	}
	m, err := fw.w.Write(payload)
	return n + m, err
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run:

```bash
go test ./internal/ipc/ -run TestFrameWriter -count=1 -v
```

Expected: three test cases all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc/writer.go internal/ipc/writer_test.go
git commit -m "feat(ipc): add FrameWriter with header/payload validation"
```

---

## Task 5: Frame Reader

**Files:**
- Create: `internal/ipc/reader.go`
- Create: `internal/ipc/reader_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/ipc/reader_test.go`:

```go
package ipc

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func encodeFrame(t *testing.T, h Header, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := NewFrameWriter(&buf)
	_, err := w.WriteFrame(h, payload)
	require.NoError(t, err)
	return buf.Bytes()
}

func TestFrameReader_ReadFrame(t *testing.T) {
	payload := []byte("hello world")
	data := encodeFrame(t, Header{
		MsgType: MsgPing,
		SeqNo:   7,
		Length:  uint32(len(payload)),
	}, payload)

	r := NewFrameReader(bytes.NewReader(data), MaxPayloadSize)
	h, body, err := r.ReadFrame()
	require.NoError(t, err)
	require.Equal(t, MsgPing, h.MsgType)
	require.Equal(t, uint64(7), h.SeqNo)
	require.Equal(t, payload, body)

	// EOF on subsequent read
	_, _, err = r.ReadFrame()
	require.ErrorIs(t, err, io.EOF)
}

func TestFrameReader_CorruptMagic(t *testing.T) {
	data := encodeFrame(t, Header{MsgType: MsgPing, Length: 0}, nil)
	data[0] = 0x00

	r := NewFrameReader(bytes.NewReader(data), MaxPayloadSize)
	_, _, err := r.ReadFrame()
	require.ErrorIs(t, err, ErrInvalidMagic)
}

func TestFrameReader_TruncatedHeader(t *testing.T) {
	data := encodeFrame(t, Header{MsgType: MsgPing, Length: 0}, nil)
	r := NewFrameReader(bytes.NewReader(data[:5]), MaxPayloadSize)
	_, _, err := r.ReadFrame()
	require.True(t, errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF))
}

func TestFrameReader_TruncatedPayload(t *testing.T) {
	payload := []byte("hello world")
	data := encodeFrame(t, Header{MsgType: MsgPing, Length: uint32(len(payload))}, payload)

	r := NewFrameReader(bytes.NewReader(data[:HeaderSize+3]), MaxPayloadSize)
	_, _, err := r.ReadFrame()
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestFrameReader_EnforcesMaxPayloadSize(t *testing.T) {
	// Craft a header with a length larger than the reader's cap.
	h := Header{MsgType: MsgPing, Length: 1024}
	var hdr [HeaderSize]byte
	h.Encode(hdr[:])

	r := NewFrameReader(bytes.NewReader(hdr[:]), 512)
	_, _, err := r.ReadFrame()
	require.ErrorIs(t, err, ErrFrameTooLarge)
}
```

- [ ] **Step 2: Run the tests and confirm they fail to compile**

Run:

```bash
go test ./internal/ipc/ -run TestFrameReader -count=1
```

Expected: `undefined: NewFrameReader`.

- [ ] **Step 3: Implement `FrameReader`**

Create `internal/ipc/reader.go`:

```go
package ipc

import (
	"io"
)

// FrameReader reads framed messages from an io.Reader.
// It is NOT safe for concurrent use; callers must serialize ReadFrame calls.
type FrameReader struct {
	r       io.Reader
	cap     uint32
	scratch [HeaderSize]byte
}

// NewFrameReader returns a FrameReader that reads from r and rejects frames
// with Length > maxPayload. Callers should pass MaxPayloadSize unless they
// want an even tighter bound.
func NewFrameReader(r io.Reader, maxPayload uint32) *FrameReader {
	if maxPayload == 0 || maxPayload > MaxPayloadSize {
		maxPayload = MaxPayloadSize
	}
	return &FrameReader{r: r, cap: maxPayload}
}

// ReadFrame reads exactly one frame from the underlying reader.
// It returns the decoded header, the payload bytes (may be empty), and an
// error. On io.EOF with no bytes consumed the error is io.EOF. On partial
// consumption the error is io.ErrUnexpectedEOF.
func (fr *FrameReader) ReadFrame() (Header, []byte, error) {
	var h Header
	if _, err := io.ReadFull(fr.r, fr.scratch[:]); err != nil {
		if err == io.ErrUnexpectedEOF {
			return h, nil, io.ErrUnexpectedEOF
		}
		return h, nil, err
	}
	if err := h.Decode(fr.scratch[:]); err != nil {
		return h, nil, err
	}
	if h.Length > fr.cap {
		return h, nil, ErrFrameTooLarge
	}
	if h.Length == 0 {
		return h, nil, nil
	}
	body := make([]byte, h.Length)
	if _, err := io.ReadFull(fr.r, body); err != nil {
		if err == io.EOF {
			return h, nil, io.ErrUnexpectedEOF
		}
		return h, nil, err
	}
	return h, body, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/ipc/ -run TestFrameReader -count=1 -v
```

Expected: five test cases all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc/reader.go internal/ipc/reader_test.go
git commit -m "feat(ipc): add FrameReader with size and truncation validation"
```

---

## Task 6: Fuzz Target for the Frame Reader

**Files:**
- Create: `internal/ipc/reader_fuzz_test.go`

- [ ] **Step 1: Write the fuzz target**

Create `internal/ipc/reader_fuzz_test.go`:

```go
package ipc

import (
	"bytes"
	"testing"
)

func FuzzFrameReader(f *testing.F) {
	// Seed corpus: one valid frame with small payload.
	valid := encodeFrameForFuzz(Header{MsgType: MsgPing, Length: 4}, []byte("data"))
	f.Add(valid)
	// Seed corpus: just a header with no body.
	empty := encodeFrameForFuzz(Header{MsgType: MsgShutdown, Length: 0}, nil)
	f.Add(empty)

	f.Fuzz(func(t *testing.T, data []byte) {
		r := NewFrameReader(bytes.NewReader(data), MaxPayloadSize)
		for i := 0; i < 8; i++ {
			_, _, err := r.ReadFrame()
			if err != nil {
				return
			}
		}
	})
}

func encodeFrameForFuzz(h Header, payload []byte) []byte {
	var buf bytes.Buffer
	w := NewFrameWriter(&buf)
	_, _ = w.WriteFrame(h, payload)
	return buf.Bytes()
}
```

- [ ] **Step 2: Run the fuzz target for a short duration**

Run:

```bash
go test ./internal/ipc/ -run=^$ -fuzz=FuzzFrameReader -fuzztime=10s
```

Expected: `fuzz: elapsed: 10s, execs: <N>`, no crashes. The process should exit cleanly after the timeout.

- [ ] **Step 3: Commit**

```bash
git add internal/ipc/reader_fuzz_test.go
git commit -m "test(ipc): add fuzz target for FrameReader"
```

---

## Task 7: MessagePack Codec Helpers

**Files:**
- Create: `internal/ipc/codec.go`
- Create: `internal/ipc/codec_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/ipc/codec_test.go`:

```go
package ipc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type sample struct {
	Name  string `msgpack:"n"`
	Value int64  `msgpack:"v"`
}

func TestCodec_RoundTrip(t *testing.T) {
	in := sample{Name: "answer", Value: 42}
	data, err := EncodePayload(in)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	var out sample
	require.NoError(t, DecodePayload(data, &out))
	require.Equal(t, in, out)
}

func TestCodec_DecodeIntoNilReturnsError(t *testing.T) {
	data, err := EncodePayload(sample{})
	require.NoError(t, err)
	require.Error(t, DecodePayload(data, nil))
}

func TestCodec_DecodeGarbageReturnsError(t *testing.T) {
	var out sample
	err := DecodePayload([]byte{0xFF, 0xFF, 0xFF}, &out)
	require.Error(t, err)
}
```

- [ ] **Step 2: Run the test and confirm it fails to compile**

Run:

```bash
go test ./internal/ipc/ -run TestCodec -count=1
```

Expected: `undefined: EncodePayload`, `undefined: DecodePayload`.

- [ ] **Step 3: Implement the codec helpers**

Create `internal/ipc/codec.go`:

```go
package ipc

import (
	"errors"

	"github.com/vmihailenco/msgpack/v5"
)

// ErrNilDecodeTarget is returned by DecodePayload when v is nil.
var ErrNilDecodeTarget = errors.New("ipc: decode target is nil")

// EncodePayload serializes v to MessagePack bytes.
func EncodePayload(v any) ([]byte, error) {
	return msgpack.Marshal(v)
}

// DecodePayload deserializes MessagePack bytes into v. v must be a non-nil
// pointer to the destination struct.
func DecodePayload(data []byte, v any) error {
	if v == nil {
		return ErrNilDecodeTarget
	}
	return msgpack.Unmarshal(data, v)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run:

```bash
go test ./internal/ipc/ -run TestCodec -count=1 -v
```

Expected: three test cases pass.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc/codec.go internal/ipc/codec_test.go
git commit -m "feat(ipc): add MessagePack codec wrappers"
```

---

## Task 8: Payload Structs for All Message Types

**Files:**
- Create: `internal/ipc/message.go`
- Create: `internal/ipc/message_test.go`

- [ ] **Step 1: Write the failing round-trip test**

Create `internal/ipc/message_test.go`:

```go
package ipc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPayloads_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		val  any
		ptr  any
	}{
		{"HelloPayload", HelloPayload{ProtocolVersion: 1, AgentID: "agent-1"}, &HelloPayload{}},
		{"HelloAckPayload", HelloAckPayload{ProtocolVersion: 1, Capabilities: []string{"logs"}}, &HelloAckPayload{}},
		{"StartChildPayload", StartChildPayload{
			Command:     "/bin/echo",
			Args:        []string{"hi"},
			Env:         map[string]string{"X": "1"},
			WorkDir:     "/tmp",
			StdinMode:   1,
			StopTimeout: 5000,
		}, &StartChildPayload{}},
		{"StopChildPayload", StopChildPayload{TimeoutMs: 3000}, &StopChildPayload{}},
		{"SignalChildPayload", SignalChildPayload{Signal: 15}, &SignalChildPayload{}},
		{"WriteStdinPayload", WriteStdinPayload{Data: []byte("input")}, &WriteStdinPayload{}},
		{"SetLogLevelPayload", SetLogLevelPayload{Level: 2}, &SetLogLevelPayload{}},
		{"ChildStartedPayload", ChildStartedPayload{PID: 1234, StartedAt: 1_700_000_000_000_000_000}, &ChildStartedPayload{}},
		{"ChildExitedPayload", ChildExitedPayload{
			PID: 1234, ExitCode: 0, Signal: 0, ExitedAt: 1_700_000_000, Reason: "ok",
		}, &ChildExitedPayload{}},
		{"LogChunkPayload", LogChunkPayload{Stream: 0, SeqNo: 7, Data: []byte("bytes")}, &LogChunkPayload{}},
		{"LogFileRefPayload", LogFileRefPayload{
			Stream: 1, Path: "/tmp/log.0", Size: 1024, Checksum: "abc",
		}, &LogFileRefPayload{}},
		{"ChildErrorPayload", ChildErrorPayload{Phase: "exec", Message: "not found"}, &ChildErrorPayload{}},
		{"ResourceSamplePayload", ResourceSamplePayload{
			PID: 1234, RSS: 1024 * 1024, CPUPercent: 12.5, Threads: 4, FDs: 16,
		}, &ResourceSamplePayload{}},
		{"AgentFatalPayload", AgentFatalPayload{Reason: "panic", Stack: "..."}, &AgentFatalPayload{}},
		{"AckPayload", AckPayload{AckedSeq: 99}, &AckPayload{}},
		{"ResumePayload", ResumePayload{LastAckedSeq: 500}, &ResumePayload{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := EncodePayload(tc.val)
			require.NoError(t, err)
			require.NoError(t, DecodePayload(data, tc.ptr))
		})
	}
}
```

- [ ] **Step 2: Run the test and confirm it fails**

Run:

```bash
go test ./internal/ipc/ -run TestPayloads -count=1
```

Expected: compile failure — all payload types are undefined.

- [ ] **Step 3: Implement the payload types**

Create `internal/ipc/message.go`:

```go
package ipc

// --- Control plane (host -> agent) ---

// HelloPayload is the handshake initiator.
type HelloPayload struct {
	ProtocolVersion uint8  `msgpack:"v"`
	AgentID         string `msgpack:"id"`
}

// StartChildPayload instructs the agent to fork/exec the target CLI.
type StartChildPayload struct {
	Command     string            `msgpack:"cmd"`
	Args        []string          `msgpack:"args"`
	Env         map[string]string `msgpack:"env"`
	WorkDir     string            `msgpack:"wd"`
	StdinMode   uint8             `msgpack:"stdin"` // 0=none, 1=pipe, 2=inherit
	StopTimeout int64             `msgpack:"stop_to_ms"`
}

// StopChildPayload requests graceful termination of the child.
type StopChildPayload struct {
	TimeoutMs int64 `msgpack:"to_ms"`
}

// SignalChildPayload asks the agent to deliver a specific signal.
type SignalChildPayload struct {
	Signal int32 `msgpack:"sig"`
}

// WriteStdinPayload forwards bytes to the child's stdin.
type WriteStdinPayload struct {
	Data []byte `msgpack:"d"`
}

// SetLogLevelPayload toggles the agent's log verbosity.
type SetLogLevelPayload struct {
	Level uint8 `msgpack:"lvl"` // matches DebugMode
}

// --- Data plane (agent -> host) ---

// HelloAckPayload is the agent's handshake response.
type HelloAckPayload struct {
	ProtocolVersion uint8    `msgpack:"v"`
	Capabilities    []string `msgpack:"caps"`
}

// ChildStartedPayload signals that exec succeeded.
type ChildStartedPayload struct {
	PID       int32 `msgpack:"pid"`
	StartedAt int64 `msgpack:"at"`
}

// ChildExitedPayload signals that the child process has terminated.
type ChildExitedPayload struct {
	PID      int32  `msgpack:"pid"`
	ExitCode int32  `msgpack:"code"`
	Signal   int32  `msgpack:"sig"`
	ExitedAt int64  `msgpack:"at"`
	Reason   string `msgpack:"reason"`
}

// LogChunkPayload carries a stdout/stderr fragment inline.
// Stream: 0=stdout, 1=stderr, 2=diag (debug mode only)
type LogChunkPayload struct {
	Stream uint8  `msgpack:"s"`
	SeqNo  uint64 `msgpack:"n"`
	Data   []byte `msgpack:"d"`
}

// LogFileRefPayload references an out-of-band log file on disk.
type LogFileRefPayload struct {
	Stream   uint8  `msgpack:"s"`
	Path     string `msgpack:"p"`
	Size     uint64 `msgpack:"sz"`
	Checksum string `msgpack:"ck"`
}

// ChildErrorPayload reports an exec/runtime error from inside the agent.
type ChildErrorPayload struct {
	Phase   string `msgpack:"phase"`
	Message string `msgpack:"msg"`
}

// ResourceSamplePayload carries a periodic resource sample.
type ResourceSamplePayload struct {
	PID        int32   `msgpack:"pid"`
	RSS        uint64  `msgpack:"rss"`
	CPUPercent float64 `msgpack:"cpu"`
	Threads    int32   `msgpack:"thr"`
	FDs        int32   `msgpack:"fds"`
}

// AgentFatalPayload announces imminent agent termination.
type AgentFatalPayload struct {
	Reason string `msgpack:"reason"`
	Stack  string `msgpack:"stack"`
}

// --- Shared ---

// AckPayload acknowledges a specific sequence number.
type AckPayload struct {
	AckedSeq uint64 `msgpack:"ack"`
}

// ResumePayload exchanges last-seen sequence numbers on reconnect.
type ResumePayload struct {
	LastAckedSeq uint64 `msgpack:"last"`
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run:

```bash
go test ./internal/ipc/ -run TestPayloads -count=1 -v
```

Expected: 16 subtests all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc/message.go internal/ipc/message_test.go
git commit -m "feat(ipc): add payload structs for all message types"
```

---

## Task 9: Sequence Number Generator

**Files:**
- Create: `internal/ipc/seq.go`
- Create: `internal/ipc/seq_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/ipc/seq_test.go`:

```go
package ipc

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSeqGenerator_Monotonic(t *testing.T) {
	g := NewSeqGenerator(1)
	require.Equal(t, uint64(1), g.Next())
	require.Equal(t, uint64(2), g.Next())
	require.Equal(t, uint64(3), g.Next())
}

func TestSeqGenerator_Concurrent(t *testing.T) {
	g := NewSeqGenerator(1)
	const N = 1000
	const G = 8

	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := make(map[uint64]struct{}, N*G)

	for i := 0; i < G; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < N; j++ {
				v := g.Next()
				mu.Lock()
				seen[v] = struct{}{}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	require.Len(t, seen, N*G, "all sequence numbers must be unique")
}

func TestDedupTracker(t *testing.T) {
	d := NewDedupTracker()

	require.False(t, d.Seen(1), "first seq is new")
	require.True(t, d.Seen(1), "repeat seq must be duplicate")
	require.False(t, d.Seen(2), "advancing seq is new")
	require.False(t, d.Seen(3), "advancing seq is new")
	require.True(t, d.Seen(2), "seq below watermark must be duplicate")
	require.True(t, d.Seen(3), "seq at watermark must be duplicate")
	require.False(t, d.Seen(5), "skip ahead is new")
	require.True(t, d.Seen(4), "seq below new watermark must be duplicate")
}

func TestDedupTracker_Concurrent(t *testing.T) {
	d := NewDedupTracker()
	const N = 1000
	const G = 8

	var wg sync.WaitGroup
	for i := 0; i < G; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for j := 1; j <= N; j++ {
				d.Seen(uint64(base*N + j))
			}
		}(i)
	}
	wg.Wait()

	// After all goroutines, the watermark should have advanced.
	// All previously seen seqs must be duplicates.
	require.True(t, d.Seen(1), "seq 1 must be duplicate after concurrent writes")
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run:

```bash
go test ./internal/ipc/ -run "TestSeqGenerator|TestDedupTracker" -count=1
```

Expected: `undefined: NewSeqGenerator`, `undefined: NewDedupTracker`.

- [ ] **Step 3: Implement `SeqGenerator` and `DedupTracker`**

Create `internal/ipc/seq.go`:

```go
package ipc

import (
	"sync"
	"sync/atomic"
)

// SeqGenerator produces monotonically increasing sequence numbers.
// It is safe for concurrent use.
type SeqGenerator struct {
	n atomic.Uint64
}

// NewSeqGenerator returns a generator whose first Next() returns start.
// If start is 0, it is clamped to 1 to avoid uint64 underflow.
func NewSeqGenerator(start uint64) *SeqGenerator {
	if start == 0 {
		start = 1
	}
	g := &SeqGenerator{}
	g.n.Store(start - 1)
	return g
}

// Next returns the next sequence number.
func (g *SeqGenerator) Next() uint64 {
	return g.n.Add(1)
}

// DedupTracker remembers the highest observed sequence number (watermark).
// A frame is considered duplicate iff seq <= lastSeen. This approach uses
// O(1) memory regardless of connection lifetime, which is safe for
// long-running connections (spec §2.7).
//
// It is safe for concurrent use.
type DedupTracker struct {
	mu       sync.Mutex
	lastSeen uint64
}

// NewDedupTracker returns an empty tracker.
func NewDedupTracker() *DedupTracker {
	return &DedupTracker{}
}

// Seen reports whether seq has already been observed. If seq is new
// (greater than the current watermark), it advances the watermark and
// returns false.
func (d *DedupTracker) Seen(seq uint64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if seq <= d.lastSeen {
		return true
	}
	d.lastSeen = seq
	return false
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/ipc/ -run "TestSeqGenerator|TestDedupTracker" -count=1 -v -race
```

Expected: three tests pass, including the concurrent one under `-race`.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc/seq.go internal/ipc/seq_test.go
git commit -m "feat(ipc): add sequence number generator and dedup tracker"
```

---

## Task 10: In-Memory Outbox

**Files:**
- Create: `internal/ipc/outbox.go`
- Create: `internal/ipc/outbox_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/ipc/outbox_test.go`:

```go
package ipc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOutbox_EnqueueAndDequeue(t *testing.T) {
	ob := NewOutbox(4, nil)
	defer ob.Close()

	msg := OutboxMessage{
		Header:  Header{MsgType: MsgPing, SeqNo: 1, Length: 4},
		Payload: []byte("ping"),
	}
	require.True(t, ob.Enqueue(msg))

	got, ok := ob.Dequeue(100 * time.Millisecond)
	require.True(t, ok)
	require.Equal(t, msg, got)
}

func TestOutbox_BoundedCapacityTriggersSpill(t *testing.T) {
	var spilled []OutboxMessage
	spill := func(m OutboxMessage) error {
		spilled = append(spilled, m)
		return nil
	}

	ob := NewOutbox(2, spill)
	defer ob.Close()

	for i := 1; i <= 4; i++ {
		ok := ob.Enqueue(OutboxMessage{
			Header:  Header{MsgType: MsgPing, SeqNo: uint64(i)},
			Payload: nil,
		})
		require.True(t, ok, "enqueue #%d must succeed even when in-memory queue is full", i)
	}

	require.Len(t, spilled, 2, "overflow messages must be spilled")
	require.Equal(t, uint64(3), spilled[0].Header.SeqNo)
	require.Equal(t, uint64(4), spilled[1].Header.SeqNo)
}

func TestOutbox_EnqueueAfterClose(t *testing.T) {
	ob := NewOutbox(2, nil)
	ob.Close()

	require.False(t, ob.Enqueue(OutboxMessage{Header: Header{SeqNo: 1}}))
}

func TestOutbox_DequeueReturnsFalseOnClose(t *testing.T) {
	ob := NewOutbox(2, nil)
	go func() {
		time.Sleep(20 * time.Millisecond)
		ob.Close()
	}()
	_, ok := ob.Dequeue(500 * time.Millisecond)
	require.False(t, ok)
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run:

```bash
go test ./internal/ipc/ -run TestOutbox -count=1
```

Expected: `undefined: NewOutbox`, `undefined: OutboxMessage`.

- [ ] **Step 3: Implement the `Outbox`**

Create `internal/ipc/outbox.go`:

```go
package ipc

import (
	"sync"
	"time"
)

// OutboxMessage is one unit of work in an Outbox.
type OutboxMessage struct {
	Header  Header
	Payload []byte
}

// SpillFunc is invoked when the in-memory queue is full. Implementations
// typically persist the message to a WAL. Returning a non-nil error causes
// the Outbox.Enqueue call to fail.
type SpillFunc func(OutboxMessage) error

// Outbox is a bounded FIFO with an optional disk spill handler.
// Enqueue never blocks the producer; when the in-memory slots are full
// it invokes the configured SpillFunc synchronously.
//
// Shutdown is signalled via a dedicated `done` channel — the data channel
// `ch` is never closed directly, which eliminates the "send on closed channel"
// race between Enqueue and Close.
type Outbox struct {
	ch    chan OutboxMessage
	done  chan struct{}
	spill SpillFunc
	once  sync.Once
}

// NewOutbox returns an Outbox with the given in-memory capacity and spill
// handler. If spill is nil, overflow enqueues are dropped by returning false.
func NewOutbox(capacity int, spill SpillFunc) *Outbox {
	if capacity < 1 {
		capacity = 1
	}
	return &Outbox{
		ch:    make(chan OutboxMessage, capacity),
		done:  make(chan struct{}),
		spill: spill,
	}
}

// Enqueue attempts to add msg to the queue. Returns false if the outbox is
// closed or the SpillFunc (when the channel is full) returned an error.
func (o *Outbox) Enqueue(msg OutboxMessage) bool {
	select {
	case <-o.done:
		return false
	default:
	}

	select {
	case o.ch <- msg:
		return true
	case <-o.done:
		return false
	default:
	}

	if o.spill == nil {
		return false
	}
	return o.spill(msg) == nil
}

// Dequeue blocks until a message is available, the timeout elapses, or the
// outbox is closed. The second return value indicates success.
func (o *Outbox) Dequeue(timeout time.Duration) (OutboxMessage, bool) {
	if timeout <= 0 {
		select {
		case msg := <-o.ch:
			return msg, true
		case <-o.done:
			// Drain any remaining messages before returning false.
			select {
			case msg := <-o.ch:
				return msg, true
			default:
				return OutboxMessage{}, false
			}
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case msg := <-o.ch:
		return msg, true
	case <-o.done:
		// Drain any remaining messages before returning false.
		select {
		case msg := <-o.ch:
			return msg, true
		default:
			return OutboxMessage{}, false
		}
	case <-timer.C:
		return OutboxMessage{}, false
	}
}

// InjectFront pushes a message to the front of the queue, bypassing the
// normal capacity check. This is used by the spiller to replay WAL entries
// into the in-memory queue.
func (o *Outbox) InjectFront(msg OutboxMessage) bool {
	select {
	case <-o.done:
		return false
	default:
	}
	select {
	case o.ch <- msg:
		return true
	default:
		return false
	}
}

// Close shuts the outbox down. Subsequent Enqueue calls return false and
// Dequeue callers are unblocked with ok=false once all remaining messages
// are drained.
func (o *Outbox) Close() {
	o.once.Do(func() {
		close(o.done)
	})
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/ipc/ -run TestOutbox -count=1 -v -race
```

Expected: four tests all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc/outbox.go internal/ipc/outbox_test.go
git commit -m "feat(ipc): add bounded Outbox with overflow spill hook"
```

---

## Task 11: Write-Ahead Log (WAL)

**Files:**
- Create: `internal/ipc/wal.go`
- Create: `internal/ipc/wal_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/ipc/wal_test.go`:

```go
package ipc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWAL_AppendAndIterate(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenWAL(dir, 1<<20)
	require.NoError(t, err)
	defer wal.Close()

	msgs := []OutboxMessage{
		{Header: Header{MsgType: MsgPing, SeqNo: 1, Length: 0}, Payload: nil},
		{Header: Header{MsgType: MsgLogChunk, SeqNo: 2, Length: 5}, Payload: []byte("hello")},
		{Header: Header{MsgType: MsgLogChunk, SeqNo: 3, Length: 3}, Payload: []byte("abc")},
	}
	for _, m := range msgs {
		require.NoError(t, wal.Append(m))
	}

	got, err := wal.Replay()
	require.NoError(t, err)
	require.Equal(t, msgs, got)
}

func TestWAL_ReplayAfterReopen(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenWAL(dir, 1<<20)
	require.NoError(t, err)

	msg := OutboxMessage{
		Header:  Header{MsgType: MsgPing, SeqNo: 99, Length: 2},
		Payload: []byte("hi"),
	}
	require.NoError(t, wal.Append(msg))
	require.NoError(t, wal.Close())

	wal2, err := OpenWAL(dir, 1<<20)
	require.NoError(t, err)
	defer wal2.Close()

	got, err := wal2.Replay()
	require.NoError(t, err)
	require.Equal(t, []OutboxMessage{msg}, got)
}

func TestWAL_RetireRemovesEntries(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenWAL(dir, 1<<20)
	require.NoError(t, err)
	defer wal.Close()

	for i := 1; i <= 5; i++ {
		require.NoError(t, wal.Append(OutboxMessage{
			Header: Header{MsgType: MsgPing, SeqNo: uint64(i)},
		}))
	}

	require.NoError(t, wal.Retire(3))

	got, err := wal.Replay()
	require.NoError(t, err)
	seqs := make([]uint64, len(got))
	for i, m := range got {
		seqs[i] = m.Header.SeqNo
	}
	require.Equal(t, []uint64{4, 5}, seqs)
}

func TestWAL_EnforcesSizeCap(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenWAL(dir, 64) // tiny cap
	require.NoError(t, err)
	defer wal.Close()

	require.NoError(t, wal.Append(OutboxMessage{
		Header:  Header{MsgType: MsgPing, SeqNo: 1, Length: 4},
		Payload: []byte("data"),
	}))

	err = wal.Append(OutboxMessage{
		Header:  Header{MsgType: MsgLogChunk, SeqNo: 2, Length: 1024},
		Payload: make([]byte, 1024),
	})
	require.ErrorIs(t, err, ErrWALFull)
}

func TestWAL_FileLayoutIsStable(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenWAL(dir, 1<<20)
	require.NoError(t, err)
	defer wal.Close()

	require.NoError(t, wal.Append(OutboxMessage{
		Header:  Header{MsgType: MsgPing, SeqNo: 1, Length: 0},
		Payload: nil,
	}))

	// Exactly one file should exist in the WAL directory.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Contains(t, entries[0].Name(), "wal")

	info, err := os.Stat(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)
	require.Greater(t, info.Size(), int64(0))
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run:

```bash
go test ./internal/ipc/ -run TestWAL -count=1
```

Expected: `undefined: OpenWAL`, `undefined: ErrWALFull`.

- [ ] **Step 3: Implement the WAL**

Create `internal/ipc/wal.go`:

```go
package ipc

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// ErrWALFull indicates the WAL has reached its configured size cap.
var ErrWALFull = errors.New("ipc: WAL size cap reached")

// walFileName is the fixed filename for v1. Future versions may rotate files.
const walFileName = "outbox.wal"

// WAL is an append-only log of OutboxMessage records. Records are framed
// on disk using the same 17-byte header format as the wire protocol so
// that replay is bit-for-bit compatible with the in-flight format.
//
// The file layout is:
//
//	[ header(17) | payload(N) ] [ header(17) | payload(N) ] ...
//
// Retire(seq) rewrites the file, keeping only records with Header.SeqNo > seq.
type WAL struct {
	mu       sync.Mutex
	dir      string
	path     string
	f        *os.File
	bw       *bufio.Writer
	sizeCap  int64
	curBytes int64
}

// OpenWAL opens or creates the WAL in dir with the given byte cap.
func OpenWAL(dir string, sizeCap int64) (*WAL, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("wal: mkdir: %w", err)
	}
	path := filepath.Join(dir, walFileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("wal: open: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("wal: stat: %w", err)
	}
	return &WAL{
		dir:      dir,
		path:     path,
		f:        f,
		bw:       bufio.NewWriter(f),
		sizeCap:  sizeCap,
		curBytes: info.Size(),
	}, nil
}

// Append writes msg to the WAL. It returns ErrWALFull if the byte cap would
// be exceeded.
func (w *WAL) Append(msg OutboxMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	recSize := int64(HeaderSize) + int64(len(msg.Payload))
	if w.curBytes+recSize > w.sizeCap {
		return ErrWALFull
	}

	var hdr [HeaderSize]byte
	msg.Header.Encode(hdr[:])
	if _, err := w.bw.Write(hdr[:]); err != nil {
		return fmt.Errorf("wal: write header: %w", err)
	}
	if len(msg.Payload) > 0 {
		if _, err := w.bw.Write(msg.Payload); err != nil {
			return fmt.Errorf("wal: write payload: %w", err)
		}
	}
	if err := w.bw.Flush(); err != nil {
		return fmt.Errorf("wal: flush: %w", err)
	}
	if err := w.f.Sync(); err != nil {
		return fmt.Errorf("wal: fsync: %w", err)
	}
	w.curBytes += recSize
	return nil
}

// Replay returns every record currently in the WAL, in append order.
func (w *WAL) Replay() ([]OutboxMessage, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.readAllLocked()
}

func (w *WAL) readAllLocked() ([]OutboxMessage, error) {
	if _, err := w.f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("wal: seek: %w", err)
	}
	reader := bufio.NewReader(w.f)

	var out []OutboxMessage
	for {
		var buf [HeaderSize]byte
		_, err := io.ReadFull(reader, buf[:])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("wal: read header: %w", err)
		}
		var h Header
		if err := h.Decode(buf[:]); err != nil {
			return nil, fmt.Errorf("wal: decode header: %w", err)
		}
		var payload []byte
		if h.Length > 0 {
			payload = make([]byte, h.Length)
			if _, err := io.ReadFull(reader, payload); err != nil {
				return nil, fmt.Errorf("wal: read payload: %w", err)
			}
		}
		out = append(out, OutboxMessage{Header: h, Payload: payload})
	}

	// Seek back to EOF so Append continues to append.
	if _, err := w.f.Seek(0, io.SeekEnd); err != nil {
		return nil, fmt.Errorf("wal: seek end: %w", err)
	}
	// Re-wrap the writer after the Seek.
	w.bw = bufio.NewWriter(w.f)
	return out, nil
}

// Retire discards every record with Header.SeqNo <= ackedSeq.
// It does so by rewriting the WAL file in place.
func (w *WAL) Retire(ackedSeq uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	all, err := w.readAllLocked()
	if err != nil {
		return err
	}

	kept := all[:0]
	for _, m := range all {
		if m.Header.SeqNo > ackedSeq {
			kept = append(kept, m)
		}
	}

	tmpPath := w.path + ".tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("wal: open tmp: %w", err)
	}
	bw := bufio.NewWriter(tmp)

	var total int64
	var hdr [HeaderSize]byte
	for _, m := range kept {
		m.Header.Encode(hdr[:])
		if _, err := bw.Write(hdr[:]); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("wal: retire write hdr: %w", err)
		}
		if len(m.Payload) > 0 {
			if _, err := bw.Write(m.Payload); err != nil {
				_ = tmp.Close()
				return fmt.Errorf("wal: retire write payload: %w", err)
			}
		}
		total += int64(HeaderSize) + int64(len(m.Payload))
	}
	if err := bw.Flush(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("wal: retire flush: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("wal: retire fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("wal: retire close tmp: %w", err)
	}

	// Swap files: rename first, then close old fd, then reopen.
	// This ordering ensures the file at w.path is always valid even if
	// the process crashes mid-Retire.
	if err := os.Rename(tmpPath, w.path); err != nil {
		return fmt.Errorf("wal: retire rename: %w", err)
	}
	if err := w.f.Close(); err != nil {
		return fmt.Errorf("wal: retire close old: %w", err)
	}
	f, err := os.OpenFile(w.path, os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("wal: retire reopen: %w", err)
	}
	w.f = f
	w.bw = bufio.NewWriter(f)
	w.curBytes = total
	return nil
}

// Close flushes and closes the WAL file.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	if err := w.bw.Flush(); err != nil {
		return err
	}
	err := w.f.Close()
	w.f = nil
	return err
}

```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/ipc/ -run TestWAL -count=1 -v
```

Expected: all five WAL tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc/wal.go internal/ipc/wal_test.go
git commit -m "feat(ipc): add append-only WAL with replay and retire"
```

---

## Task 12: Spiller — Outbox + WAL Integration

**Files:**
- Create: `internal/ipc/spiller.go`
- Create: `internal/ipc/spiller_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/ipc/spiller_test.go`:

```go
package ipc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSpiller_OverflowAndDrain(t *testing.T) {
	dir := t.TempDir()
	sp, err := NewSpiller(dir, 2, 1<<20)
	require.NoError(t, err)
	defer sp.Close()

	// Enqueue 5 messages; in-memory capacity is 2, so 3 must spill.
	for i := 1; i <= 5; i++ {
		require.True(t, sp.Outbox().Enqueue(OutboxMessage{
			Header: Header{MsgType: MsgPing, SeqNo: uint64(i)},
		}))
	}

	// Drain the in-memory queue.
	drained := make([]uint64, 0, 5)
	for i := 0; i < 2; i++ {
		msg, ok := sp.Outbox().Dequeue(200 * time.Millisecond)
		require.True(t, ok)
		drained = append(drained, msg.Header.SeqNo)
	}
	require.Equal(t, []uint64{1, 2}, drained)

	// Trigger a replay from the WAL.
	require.NoError(t, sp.ReplayInto(sp.Outbox()))

	// The remaining 3 messages must now be available in order.
	for want := uint64(3); want <= 5; want++ {
		msg, ok := sp.Outbox().Dequeue(200 * time.Millisecond)
		require.True(t, ok)
		require.Equal(t, want, msg.Header.SeqNo)
	}
}

func TestSpiller_AckRetiresWALEntries(t *testing.T) {
	dir := t.TempDir()
	// Capacity=4 so that: seq 1..4 go to channel, seq 5..6 spill to WAL.
	// After draining the channel, ReplayInto has room to inject WAL records.
	sp, err := NewSpiller(dir, 4, 1<<20)
	require.NoError(t, err)
	defer sp.Close()

	for i := 1; i <= 6; i++ {
		sp.Outbox().Enqueue(OutboxMessage{Header: Header{MsgType: MsgPing, SeqNo: uint64(i)}})
	}
	// Seqs 1..4 in the channel, seqs 5,6 in the WAL.

	// Ack up to seq 5 — retires seq 5 from the WAL, leaving only seq 6.
	require.NoError(t, sp.Ack(5))

	// Drain the in-memory channel (seqs 1..4).
	for i := 0; i < 4; i++ {
		_, ok := sp.Outbox().Dequeue(50 * time.Millisecond)
		require.True(t, ok)
	}

	// Replay; only seq 6 should come back from disk.
	require.NoError(t, sp.ReplayInto(sp.Outbox()))

	// Drain everything.
	var seqs []uint64
	for {
		msg, ok := sp.Outbox().Dequeue(50 * time.Millisecond)
		if !ok {
			break
		}
		seqs = append(seqs, msg.Header.SeqNo)
	}
	require.Contains(t, seqs, uint64(6))
	require.NotContains(t, seqs, uint64(5), "seq 5 must have been retired")
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run:

```bash
go test ./internal/ipc/ -run TestSpiller -count=1
```

Expected: `undefined: NewSpiller`.

- [ ] **Step 3: Implement the `Spiller`**

Create `internal/ipc/spiller.go`:

```go
package ipc

import (
	"fmt"
	"sync"
)

// Spiller couples an in-memory Outbox with a disk-backed WAL so that
// producers never block and messages are never lost.
type Spiller struct {
	mu  sync.Mutex
	ob  *Outbox
	wal *WAL
}

// NewSpiller returns a Spiller whose Outbox has the given capacity and
// whose WAL lives under dir with a byte cap of walCap.
func NewSpiller(dir string, capacity int, walCap int64) (*Spiller, error) {
	wal, err := OpenWAL(dir, walCap)
	if err != nil {
		return nil, err
	}
	sp := &Spiller{wal: wal}
	sp.ob = NewOutbox(capacity, sp.spill)
	return sp, nil
}

// Outbox exposes the underlying in-memory queue for producers and consumers.
func (s *Spiller) Outbox() *Outbox { return s.ob }

// Ack retires every WAL record with SeqNo <= ackedSeq.
func (s *Spiller) Ack(ackedSeq uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wal.Retire(ackedSeq)
}

// ReplayInto reads WAL records and feeds them back into target in order.
// Any record that cannot fit into the in-memory queue is left in the WAL
// for a later replay.
func (s *Spiller) ReplayInto(target *Outbox) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	msgs, err := s.wal.Replay()
	if err != nil {
		return fmt.Errorf("spiller: replay: %w", err)
	}
	for _, m := range msgs {
		if !target.InjectFront(m) {
			// Queue full; abort here, the caller must drain and try again.
			break
		}
	}
	return nil
}

// Close releases both the outbox and the WAL.
func (s *Spiller) Close() error {
	s.ob.Close()
	return s.wal.Close()
}

// spill is the SpillFunc passed to the Outbox; it serializes through the
// same mutex that protects Ack/ReplayInto so that WAL state is consistent.
func (s *Spiller) spill(msg OutboxMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wal.Append(msg)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/ipc/ -run TestSpiller -count=1 -v -race
```

Expected: two tests pass under the race detector.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc/spiller.go internal/ipc/spiller_test.go
git commit -m "feat(ipc): add Spiller coupling Outbox and WAL for delayed delivery"
```

---

## Task 13: AckTracker — Pending ACK Bookkeeping

**Files:**
- Create: `internal/ipc/acktracker.go`
- Create: `internal/ipc/acktracker_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/ipc/acktracker_test.go`:

```go
package ipc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAckTracker_PendingAndRetire(t *testing.T) {
	at := NewAckTracker()

	at.MarkPending(1)
	at.MarkPending(2)
	at.MarkPending(3)

	require.ElementsMatch(t, []uint64{1, 2, 3}, at.Pending())

	at.Ack(2)
	require.ElementsMatch(t, []uint64{1, 3}, at.Pending())
}

func TestAckTracker_AckUnknownIsNoop(t *testing.T) {
	at := NewAckTracker()
	at.MarkPending(5)
	at.Ack(99)
	require.ElementsMatch(t, []uint64{5}, at.Pending())
}

func TestAckTracker_MaxPendingSeq(t *testing.T) {
	at := NewAckTracker()
	at.MarkPending(3)
	at.MarkPending(7)
	at.MarkPending(5)
	require.Equal(t, uint64(7), at.MaxPendingSeq())

	at.Ack(7)
	require.Equal(t, uint64(5), at.MaxPendingSeq())
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run:

```bash
go test ./internal/ipc/ -run TestAckTracker -count=1
```

Expected: `undefined: NewAckTracker`.

- [ ] **Step 3: Implement the tracker**

Create `internal/ipc/acktracker.go`:

```go
package ipc

import (
	"sort"
	"sync"
)

// AckTracker maintains the set of sequence numbers for which the sender
// is still awaiting receiver acknowledgment. It is safe for concurrent use.
type AckTracker struct {
	mu      sync.Mutex
	pending map[uint64]struct{}
}

// NewAckTracker returns an empty tracker.
func NewAckTracker() *AckTracker {
	return &AckTracker{pending: make(map[uint64]struct{})}
}

// MarkPending records that seq has been sent and is awaiting ACK.
func (a *AckTracker) MarkPending(seq uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending[seq] = struct{}{}
}

// Ack removes seq from the pending set if present.
func (a *AckTracker) Ack(seq uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.pending, seq)
}

// Pending returns a sorted snapshot of every seq still awaiting ACK.
func (a *AckTracker) Pending() []uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]uint64, 0, len(a.pending))
	for s := range a.pending {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// MaxPendingSeq returns the highest seq awaiting ACK, or 0 if none.
func (a *AckTracker) MaxPendingSeq() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	var max uint64
	for s := range a.pending {
		if s > max {
			max = s
		}
	}
	return max
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/ipc/ -run TestAckTracker -count=1 -v -race
```

Expected: three tests pass under race detection.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc/acktracker.go internal/ipc/acktracker_test.go
git commit -m "feat(ipc): add AckTracker for pending-seq bookkeeping"
```

---

## Task 14: Conn — Bidirectional Peer Wrapper

**Files:**
- Create: `internal/ipc/conn.go`
- Create: `internal/ipc/conn_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/ipc/conn_test.go`:

```go
package ipc

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestConn_EndToEnd_OverPipe(t *testing.T) {
	defer goleak.VerifyNone(t)

	a, b := net.Pipe()

	dirA := t.TempDir()
	dirB := t.TempDir()

	connA, err := NewConn(ConnConfig{
		RWC:        a,
		SpillerDir: dirA,
		Capacity:   8,
		WALBytes:   1 << 20,
	})
	require.NoError(t, err)

	connB, err := NewConn(ConnConfig{
		RWC:        b,
		SpillerDir: dirB,
		Capacity:   8,
		WALBytes:   1 << 20,
	})
	require.NoError(t, err)

	received := make(chan OutboxMessage, 1)
	connB.OnMessage(func(msg OutboxMessage) {
		received <- msg
	})

	connA.Start()
	connB.Start()

	ping := OutboxMessage{
		Header:  Header{MsgType: MsgPing, SeqNo: 1, Length: 4},
		Payload: []byte("ping"),
	}
	require.True(t, connA.Send(ping))

	select {
	case got := <-received:
		require.Equal(t, ping.Header.MsgType, got.Header.MsgType)
		require.Equal(t, ping.Header.SeqNo, got.Header.SeqNo)
		require.Equal(t, ping.Payload, got.Payload)
	case <-time.After(2 * time.Second):
		t.Fatal("message not received in time")
	}

	// Clean shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, connA.Close(ctx))
	require.NoError(t, connB.Close(ctx))
}

func TestConn_CloseIsIdempotentAndLeakFree(t *testing.T) {
	defer goleak.VerifyNone(t)

	a, b := net.Pipe()
	dirA := t.TempDir()
	dirB := t.TempDir()

	ca, err := NewConn(ConnConfig{RWC: a, SpillerDir: dirA, Capacity: 4, WALBytes: 1 << 20})
	require.NoError(t, err)
	cb, err := NewConn(ConnConfig{RWC: b, SpillerDir: dirB, Capacity: 4, WALBytes: 1 << 20})
	require.NoError(t, err)

	ca.Start()
	cb.Start()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, ca.Close(ctx))
	require.NoError(t, ca.Close(ctx)) // second close is no-op
	require.NoError(t, cb.Close(ctx))
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run:

```bash
go test ./internal/ipc/ -run TestConn -count=1
```

Expected: `undefined: NewConn`, `undefined: ConnConfig`.

- [ ] **Step 3: Implement the `Conn`**

Create `internal/ipc/conn.go`:

```go
package ipc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// MessageHandler is invoked for every frame that Conn receives.
// It MUST NOT block; handlers should forward work to a buffered channel.
type MessageHandler func(OutboxMessage)

// ConnConfig parameterises NewConn.
type ConnConfig struct {
	// RWC is the underlying bidirectional stream (net.Conn, net.Pipe, etc.).
	RWC io.ReadWriteCloser

	// SpillerDir holds the WAL for the outgoing direction.
	SpillerDir string

	// Capacity is the in-memory outbox capacity.
	Capacity int

	// WALBytes is the WAL size cap.
	WALBytes int64

	// MaxRecvPayload caps inbound frame size. If zero, MaxPayloadSize is used.
	MaxRecvPayload uint32
}

// Conn is a full-duplex, framed, ACK-aware peer. It owns:
//   - a Spiller (outbox + WAL) for outgoing frames
//   - a writer goroutine that drains the outbox to the wire
//   - a reader goroutine that pushes inbound frames to the handler
type Conn struct {
	rwc         io.ReadWriteCloser
	sp          *Spiller
	fw          *FrameWriter
	fr          *FrameReader
	handler     atomic.Pointer[MessageHandler]
	seqs        *SeqGenerator
	dedup       *DedupTracker
	closed      atomic.Bool
	wg          sync.WaitGroup
	cancelCtx   context.Context
	cancelFunc  context.CancelFunc
	startOnce   sync.Once
	closeOnce   sync.Once
	closeResult error
}

// NewConn constructs a Conn. Call Start() to launch the reader/writer
// goroutines.
func NewConn(cfg ConnConfig) (*Conn, error) {
	if cfg.RWC == nil {
		return nil, errors.New("ipc: ConnConfig.RWC is required")
	}
	if cfg.Capacity <= 0 {
		cfg.Capacity = 1024
	}
	if cfg.WALBytes <= 0 {
		cfg.WALBytes = 256 * 1024 * 1024
	}
	if cfg.MaxRecvPayload == 0 {
		cfg.MaxRecvPayload = MaxPayloadSize
	}
	sp, err := NewSpiller(cfg.SpillerDir, cfg.Capacity, cfg.WALBytes)
	if err != nil {
		return nil, fmt.Errorf("ipc: spiller: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{
		rwc:        cfg.RWC,
		sp:         sp,
		fw:         NewFrameWriter(cfg.RWC),
		fr:         NewFrameReader(cfg.RWC, cfg.MaxRecvPayload),
		seqs:       NewSeqGenerator(1),
		dedup:      NewDedupTracker(),
		cancelCtx:  ctx,
		cancelFunc: cancel,
	}
	return c, nil
}

// OnMessage sets the handler invoked for each incoming frame. Subsequent
// calls replace the previous handler atomically.
func (c *Conn) OnMessage(h MessageHandler) {
	c.handler.Store(&h)
}

// Seqs exposes the outbound sequence-number generator so higher layers can
// attach seqs to frames they build.
func (c *Conn) Seqs() *SeqGenerator { return c.seqs }

// Start launches the reader and writer goroutines. It is safe to call more
// than once; subsequent calls are no-ops.
func (c *Conn) Start() {
	c.startOnce.Do(func() {
		c.wg.Add(2)
		go c.readLoop()
		go c.writeLoop()
	})
}

// Send enqueues msg for delivery. Returns false if the conn is closed.
func (c *Conn) Send(msg OutboxMessage) bool {
	if c.closed.Load() {
		return false
	}
	return c.sp.Outbox().Enqueue(msg)
}

// Close gracefully stops reader/writer goroutines and releases resources.
// It is idempotent. The ctx is used only to bound the spiller close; the
// goroutine join (wg.Wait) is always unconditional so that goleak never
// observes lingering goroutines.
func (c *Conn) Close(ctx context.Context) error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.cancelFunc()
		_ = c.rwc.Close()
		c.sp.Outbox().Close()

		// Always wait unconditionally for reader/writer goroutines to exit.
		// The writer selects on cancelCtx.Done() so it exits immediately
		// after cancel, and the reader returns on rwc close.
		c.wg.Wait()

		// Use ctx only to bound the spiller (WAL) close.
		spillDone := make(chan error, 1)
		go func() {
			spillDone <- c.sp.Close()
		}()
		select {
		case err := <-spillDone:
			if err != nil {
				c.closeResult = err
			}
		case <-ctx.Done():
			c.closeResult = ctx.Err()
		}
	})
	return c.closeResult
}

func (c *Conn) readLoop() {
	defer c.wg.Done()
	for {
		h, body, err := c.fr.ReadFrame()
		if err != nil {
			// EOF or closed network pipe is the normal shutdown signal.
			return
		}
		// Per spec §2.7, receivers must be idempotent on SeqNo regardless
		// of the FlagIsReplay bit. Dedup every inbound frame so higher
		// layers see each seq at most once.
		if c.dedup.Seen(h.SeqNo) {
			continue
		}
		if hp := c.handler.Load(); hp != nil && *hp != nil {
			(*hp)(OutboxMessage{Header: h, Payload: body})
		}
	}
}

func (c *Conn) writeLoop() {
	defer c.wg.Done()
	for {
		// Wait for either a message or a cancel signal. By selecting on
		// cancelCtx.Done() directly (instead of polling closed + Dequeue),
		// the writer exits immediately when Close cancels the context,
		// preventing goleak flakes.
		select {
		case <-c.cancelCtx.Done():
			return
		default:
		}
		msg, ok := c.sp.Outbox().Dequeue(100 * time.Millisecond)
		if !ok {
			// Check cancel again after a failed dequeue.
			select {
			case <-c.cancelCtx.Done():
				return
			default:
			}
			if c.closed.Load() {
				return
			}
			continue
		}
		if _, err := c.fw.WriteFrame(msg.Header, msg.Payload); err != nil {
			// On write failure, re-queue via the WAL for eventual replay.
			_ = c.sp.spill(msg)
			return
		}
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass under `-race` and goleak**

Run:

```bash
go test ./internal/ipc/ -run TestConn -count=1 -v -race
```

Expected: both tests pass, `goleak.VerifyNone` reports no leaked goroutines.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc/conn.go internal/ipc/conn_test.go
git commit -m "feat(ipc): add Conn end-to-end peer with reader/writer goroutines"
```

---

## Task 15: Full IPC Package Test Pass

**Files:**
- None — verification-only task.

- [ ] **Step 1: Run the entire IPC test suite**

Run:

```bash
go test ./internal/ipc/ -count=1 -race -v
```

Expected: every test from Tasks 2–14 passes. The total test count should be at least 30 assertions.

- [ ] **Step 2: Run `go vet` and `gofmt` checks**

Run:

```bash
go vet ./...
gofmt -l internal/
```

Expected: `go vet` produces no output; `gofmt -l` produces no output (no unformatted files).

- [ ] **Step 3: Measure coverage**

Run:

```bash
go test -coverprofile=coverage.txt -covermode=atomic ./internal/ipc/
go tool cover -func=coverage.txt | tail -n 1
```

Expected: total coverage for `internal/ipc` is ≥ 80%. If it is below, add focused tests to whichever file shows the lowest coverage.

- [ ] **Step 4: Run a short benchmark sanity check**

Append to `internal/ipc/conn_test.go`:

```go
func BenchmarkConn_Throughput(b *testing.B) {
	sa, sb := net.Pipe()
	dirA := b.TempDir()
	dirB := b.TempDir()

	ca, _ := NewConn(ConnConfig{RWC: sa, SpillerDir: dirA, Capacity: 1024, WALBytes: 1 << 20})
	cb, _ := NewConn(ConnConfig{RWC: sb, SpillerDir: dirB, Capacity: 1024, WALBytes: 1 << 20})

	done := make(chan struct{}, b.N)
	cb.OnMessage(func(OutboxMessage) { done <- struct{}{} })

	ca.Start()
	cb.Start()

	payload := make([]byte, 256)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ca.Send(OutboxMessage{
			Header:  Header{MsgType: MsgLogChunk, SeqNo: uint64(i + 1), Length: uint32(len(payload))},
			Payload: payload,
		})
		<-done
	}
	b.StopTimer()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = ca.Close(ctx)
	_ = cb.Close(ctx)
}
```

Run:

```bash
go test ./internal/ipc/ -run=^$ -bench=BenchmarkConn_Throughput -benchtime=2s
```

Expected: a benchmark line similar to `BenchmarkConn_Throughput-8   XXXXXX   XXX ns/op`. The exact numbers are not a requirement of this plan; the goal is to confirm the benchmark runs cleanly.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc/conn_test.go
git commit -m "test(ipc): add throughput benchmark for Conn"
```

---

## Task 16: CI Workflow

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Create the workflow**

Create `.github/workflows/ci.yml`:

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  test:
    runs-on: ${{ matrix.os }}
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-latest]
        go: ['1.22.x']
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: ${{ matrix.go }}
          cache: true

      - name: Verify formatting
        run: |
          test -z "$(gofmt -l .)"

      - name: Vet
        run: go vet ./...

      - name: Test with race detector
        run: go test -race -count=1 ./...

      - name: Coverage
        run: |
          go test -coverprofile=coverage.txt -covermode=atomic ./internal/ipc/...
          go tool cover -func=coverage.txt | tail -n 1
```

- [ ] **Step 2: Validate the workflow file locally (syntax check)**

Run:

```bash
python3 -c "import yaml, sys; yaml.safe_load(open('.github/workflows/ci.yml'))" && echo "yaml ok"
```

Expected: `yaml ok`.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add GitHub Actions workflow for test and vet"
```

---

## Task 17: Documentation Update

**Files:**
- Create: `README.md`

- [ ] **Step 1: Create the project README**

Create `README.md`:

```markdown
# cli-wrapper

A production-grade Go library and companion CLI for supervising background CLI processes on macOS and Linux.

## Status

Early development. Plan 01 (IPC Foundation) is in progress. See `docs/superpowers/plans/` for the roadmap.

## Development

```bash
# Run tests with race detector
make test

# Run linters
make lint

# Generate coverage
make cover
```

## Layout

- `pkg/` — public API (semver-stable, not yet implemented)
- `internal/ipc/` — frame protocol, outbox, WAL, connection wrapper (Plan 01)
- `docs/superpowers/specs/` — design specs
- `docs/superpowers/plans/` — implementation plans
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: add initial README pointing at plans directory"
```

---

## Verification Checklist

When every task above is complete, run these commands from the project root:

```bash
make lint
make test
```

Both must pass. Additionally verify manually:

- [ ] `go test ./internal/ipc/... -race` completes with no goroutine leaks (goleak).
- [ ] `go test ./internal/ipc/... -fuzz=FuzzFrameReader -fuzztime=30s` completes without crashes.
- [ ] `internal/ipc` coverage is ≥ 80%.
- [ ] Every commit is atomic: it contains either one feature (production code + tests) or one chore (CI, docs).
- [ ] No placeholder comments (`TODO`, `FIXME`, `XXX`) remain in production code.

---

## What This Plan Does NOT Cover

This plan intentionally stops at the IPC library boundary. The following come in later plans:

- **Plan 02 — Process Supervision Core:** agent binary, controller, manager, event system, log collection, crash detection, restart policy.
- **Plan 03 — Resource & Sandbox:** resource monitoring, sandbox plugin interface, noop and scriptdir providers.
- **Plan 04 — Config & Management CLI:** YAML loader, cliwrap CLI binary, manager control socket.
- **Plan 05 — Hardening & Reliability:** integration tests, chaos tests, fuzz targets for higher layers, benchmarks, CI matrix expansion.

Each of those plans will be written after Plan 01 is merged.
