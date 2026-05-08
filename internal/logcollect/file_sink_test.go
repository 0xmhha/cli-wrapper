// SPDX-License-Identifier: Apache-2.0

package logcollect

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileSink_PersistsPerStream(t *testing.T) {
	dir := t.TempDir()
	s := NewFileSink(FileSinkOptions{Dir: dir})
	defer func() { _ = s.Close() }()

	now := time.Now()
	if err := s.Write(LogEntry{ProcessID: "p1", Stream: 0, Timestamp: now, Data: []byte("hello\n")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(LogEntry{ProcessID: "p1", Stream: 1, Timestamp: now, Data: []byte("oops\n")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(LogEntry{ProcessID: "p2", Stream: 0, Timestamp: now, Data: []byte("other\n")}); err != nil {
		t.Fatal(err)
	}

	stdout, err := os.ReadFile(filepath.Join(dir, "p1.0.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(stdout) != "hello\n" {
		t.Errorf("p1 stdout = %q", stdout)
	}

	stderr, err := os.ReadFile(filepath.Join(dir, "p1.1.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(stderr) != "oops\n" {
		t.Errorf("p1 stderr = %q", stderr)
	}

	other, err := os.ReadFile(filepath.Join(dir, "p2.0.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(other) != "other\n" {
		t.Errorf("p2 stdout = %q", other)
	}
}

func TestFileSink_RotatesByMaxSize(t *testing.T) {
	dir := t.TempDir()
	s := NewFileSink(FileSinkOptions{Dir: dir, MaxSize: 8, MaxFiles: 3})
	defer func() { _ = s.Close() }()

	for i := 0; i < 4; i++ {
		err := s.Write(LogEntry{ProcessID: "p", Stream: 0, Data: []byte("AAAAAAAA")}) // 8 bytes
		if err != nil {
			t.Fatal(err)
		}
	}
	// After 4 × 8-byte writes into a 8-byte rotator there should be at
	// least one rotated sibling file (p.0.1.log).
	matches, err := filepath.Glob(filepath.Join(dir, "p.0.*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected at least one rotated p.0.*.log; dir contents:")
	}
}

func TestFileSink_EmptyDataIsNoop(t *testing.T) {
	dir := t.TempDir()
	s := NewFileSink(FileSinkOptions{Dir: dir})
	defer func() { _ = s.Close() }()

	if err := s.Write(LogEntry{ProcessID: "p", Stream: 0, Data: nil}); err != nil {
		t.Fatal(err)
	}
	// No file should have been created (no rotator opened).
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("empty Write created %d files; want 0", len(entries))
	}
}

func TestFileSink_CloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := NewFileSink(FileSinkOptions{Dir: dir})
	if err := s.Write(LogEntry{ProcessID: "p", Stream: 0, Data: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
