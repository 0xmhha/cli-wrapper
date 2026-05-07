// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestRingBuffer_EmptySnapshot(t *testing.T) {
	rb := newRingBuffer(16)
	got := rb.Snapshot()
	if len(got) != 0 {
		t.Fatalf("empty buffer Snapshot len=%d; want 0", len(got))
	}
}

func TestRingBuffer_PartialFill(t *testing.T) {
	rb := newRingBuffer(16)
	rb.Write([]byte("hello"))
	got := rb.Snapshot()
	if string(got) != "hello" {
		t.Fatalf("Snapshot=%q; want %q", string(got), "hello")
	}
}

func TestRingBuffer_ExactFill(t *testing.T) {
	rb := newRingBuffer(5)
	rb.Write([]byte("hello"))
	got := rb.Snapshot()
	if string(got) != "hello" {
		t.Fatalf("Snapshot=%q; want %q", string(got), "hello")
	}
}

func TestRingBuffer_OverflowDropsOldest(t *testing.T) {
	rb := newRingBuffer(5)
	rb.Write([]byte("hello")) // fills exactly
	rb.Write([]byte("world")) // overwrites all of "hello"
	got := rb.Snapshot()
	if string(got) != "world" {
		t.Fatalf("Snapshot=%q; want %q", string(got), "world")
	}
}

func TestRingBuffer_Wraparound(t *testing.T) {
	rb := newRingBuffer(5)
	rb.Write([]byte("hello")) // wrote 5; head=0, full=true
	rb.Write([]byte("xy"))    // wrote 2; chronological order: "lloxy"
	got := rb.Snapshot()
	if string(got) != "lloxy" {
		t.Fatalf("Snapshot=%q; want %q", string(got), "lloxy")
	}
}

func TestRingBuffer_WriteLargerThanBufferKeepsTail(t *testing.T) {
	rb := newRingBuffer(5)
	rb.Write([]byte("0123456789")) // 10 bytes; only last 5 retained
	got := rb.Snapshot()
	if string(got) != "56789" {
		t.Fatalf("Snapshot=%q; want %q", string(got), "56789")
	}
}

func TestRingBuffer_ChronologicalAfterMultipleWraps(t *testing.T) {
	rb := newRingBuffer(4)
	rb.Write([]byte("abcd"))
	rb.Write([]byte("ef")) // head=2, content (chrono): "cdef"
	rb.Write([]byte("g"))  // head=3, content (chrono): "defg"
	got := rb.Snapshot()
	if string(got) != "defg" {
		t.Fatalf("Snapshot=%q; want %q", string(got), "defg")
	}
}

func TestRingBuffer_NewRingBufferPanicsOnNonPositive(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic for size=0")
		}
	}()
	_ = newRingBuffer(0)
}

func TestRingBuffer_ConcurrentWriteAndSnapshot(t *testing.T) {
	rb := newRingBuffer(64)
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); rb.Write([]byte(strings.Repeat("a", 8))) }()
		go func() { defer wg.Done(); _ = rb.Snapshot() }()
	}
	wg.Wait()
	final := rb.Snapshot()
	for _, b := range final {
		if b != 'a' && b != 0 {
			t.Fatalf("unexpected byte %v in final snapshot", b)
		}
	}
	if !bytes.Contains(final, []byte("a")) {
		t.Fatalf("expected at least one 'a' in final snapshot")
	}
}
