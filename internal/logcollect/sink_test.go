// SPDX-License-Identifier: Apache-2.0

package logcollect

import (
	"testing"
	"time"
)

func TestSink_SnapshotPreservesAllChunks(t *testing.T) {
	s := NewRingBufferSink(1024)
	now := time.Now()
	_ = s.Write(LogEntry{ProcessID: "p", Stream: 0, Timestamp: now, Data: []byte("hello\n")})
	_ = s.Write(LogEntry{ProcessID: "p", Stream: 0, Timestamp: now.Add(time.Millisecond), Data: []byte("world\n")})
	got := s.Snapshot("p", 0)
	want := "hello\nworld\n"
	if string(got) != want {
		t.Fatalf("Snapshot=%q want %q", got, want)
	}
}

func TestSink_ChunkBoundaryEviction(t *testing.T) {
	// Capacity 6, three 4-byte writes: oldest must be evicted on second
	// arrival (4+4=8 > 6); third also evicts. Newest chunk always retained.
	s := NewRingBufferSink(6)
	ts := time.Unix(0, 0)
	for i, line := range []string{"AAAA", "BBBB", "CCCC"} {
		_ = s.Write(LogEntry{ProcessID: "p", Stream: 0, Timestamp: ts.Add(time.Duration(i) * time.Second), Data: []byte(line)})
	}
	got := s.Snapshot("p", 0)
	// After three 4-byte writes into cap=6, only the newest chunk fits.
	if string(got) != "CCCC" {
		t.Fatalf("Snapshot=%q want %q", got, "CCCC")
	}
}

func TestSink_AlwaysKeepsLatestEvenIfOverCapacity(t *testing.T) {
	// Single 100-byte write into cap=10 — the chunk alone exceeds capacity.
	// We choose to keep it (last-write-always-visible) rather than discard.
	s := NewRingBufferSink(10)
	big := make([]byte, 100)
	for i := range big {
		big[i] = 'X'
	}
	_ = s.Write(LogEntry{ProcessID: "p", Stream: 0, Timestamp: time.Now(), Data: big})
	got := s.Snapshot("p", 0)
	if len(got) != 100 {
		t.Fatalf("Snapshot len=%d want 100 (last chunk always retained)", len(got))
	}
}

func TestSink_SnapshotFiltered_Since(t *testing.T) {
	s := NewRingBufferSink(1024)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, msg := range []string{"old1\n", "old2\n", "new1\n", "new2\n"} {
		_ = s.Write(LogEntry{ProcessID: "p", Stream: 0, Timestamp: t0.Add(time.Duration(i) * time.Second), Data: []byte(msg)})
	}
	cutoff := t0.Add(2 * time.Second) // includes new1 (at t0+2s) and new2 (at t0+3s)
	got := s.SnapshotFiltered("p", 0, cutoff, 0)
	want := "new1\nnew2\n"
	if string(got) != want {
		t.Fatalf("SnapshotFiltered(since=%v)=%q want %q", cutoff, got, want)
	}

	// Filter past the newest chunk — nil result.
	if got := s.SnapshotFiltered("p", 0, t0.Add(time.Hour), 0); got != nil {
		t.Fatalf("expected nil for since past newest, got %q", got)
	}

	// Zero-time since disables filter — equivalent to plain Snapshot.
	if got := s.SnapshotFiltered("p", 0, time.Time{}, 0); string(got) != "old1\nold2\nnew1\nnew2\n" {
		t.Fatalf("zero-time since must include all chunks, got %q", got)
	}
}

func TestSink_SnapshotFiltered_Lines(t *testing.T) {
	s := NewRingBufferSink(1024)
	now := time.Now()
	_ = s.Write(LogEntry{ProcessID: "p", Stream: 0, Timestamp: now, Data: []byte("a\nb\nc\nd\ne\n")})
	got := s.SnapshotFiltered("p", 0, time.Time{}, 3)
	if string(got) != "c\nd\ne\n" {
		t.Fatalf("Lines=3 got %q want %q", got, "c\nd\ne\n")
	}

	// Lines exceeding total returns all.
	if got := s.SnapshotFiltered("p", 0, time.Time{}, 99); string(got) != "a\nb\nc\nd\ne\n" {
		t.Fatalf("Lines=99 must return all, got %q", got)
	}

	// Lines = 0 disables limit.
	if got := s.SnapshotFiltered("p", 0, time.Time{}, 0); string(got) != "a\nb\nc\nd\ne\n" {
		t.Fatalf("Lines=0 must return all, got %q", got)
	}
}

func TestSink_SnapshotFiltered_LinesNoTrailingNewline(t *testing.T) {
	// A trailing partial line counts toward the line count.
	s := NewRingBufferSink(1024)
	now := time.Now()
	_ = s.Write(LogEntry{ProcessID: "p", Stream: 0, Timestamp: now, Data: []byte("a\nb\nc")})
	got := s.SnapshotFiltered("p", 0, time.Time{}, 2)
	if string(got) != "b\nc" {
		t.Fatalf("Lines=2 (no trailing nl) got %q want %q", got, "b\nc")
	}
	got = s.SnapshotFiltered("p", 0, time.Time{}, 1)
	if string(got) != "c" {
		t.Fatalf("Lines=1 (no trailing nl) got %q want %q", got, "c")
	}
}

func TestSink_SnapshotFiltered_SinceAndLines(t *testing.T) {
	s := NewRingBufferSink(1024)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Two chunks: first has 3 lines, second has 3 lines (older / newer).
	_ = s.Write(LogEntry{ProcessID: "p", Stream: 0, Timestamp: t0, Data: []byte("o1\no2\no3\n")})
	_ = s.Write(LogEntry{ProcessID: "p", Stream: 0, Timestamp: t0.Add(time.Second), Data: []byte("n1\nn2\nn3\n")})
	// since cutoff at t0+1s drops chunk 1 entirely (3 lines), then lines=2
	// retains the last 2 of the 3 in chunk 2.
	got := s.SnapshotFiltered("p", 0, t0.Add(time.Second), 2)
	if string(got) != "n2\nn3\n" {
		t.Fatalf("combined filter got %q want %q", got, "n2\nn3\n")
	}
}

func TestSink_SnapshotFiltered_UnknownProcessReturnsNil(t *testing.T) {
	s := NewRingBufferSink(1024)
	if got := s.SnapshotFiltered("nope", 0, time.Time{}, 0); got != nil {
		t.Fatalf("unknown process must return nil, got %q", got)
	}
}

func TestLastNLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"empty", "", 3, ""},
		{"n=0 disables", "a\nb\nc\n", 0, "a\nb\nc\n"},
		{"3 of 3 with trailing nl", "a\nb\nc\n", 3, "a\nb\nc\n"},
		{"2 of 3 with trailing nl", "a\nb\nc\n", 2, "b\nc\n"},
		{"1 of 3 with trailing nl", "a\nb\nc\n", 1, "c\n"},
		{"4 of 3 returns all", "a\nb\nc\n", 4, "a\nb\nc\n"},
		{"no trailing nl, n=1", "a\nb\nc", 1, "c"},
		{"no trailing nl, n=2", "a\nb\nc", 2, "b\nc"},
		{"no trailing nl, n=3", "a\nb\nc", 3, "a\nb\nc"},
		{"single line no nl", "abc", 1, "abc"},
		{"single line with nl", "abc\n", 1, "abc\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := lastNLines([]byte(c.in), c.n)
			if string(got) != c.want {
				t.Fatalf("lastNLines(%q, %d) = %q want %q", c.in, c.n, got, c.want)
			}
		})
	}
}
