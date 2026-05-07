// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"bytes"
	"testing"
	"time"
)

// These tests exercise the initial-buffer-drain logic on a zero-valued
// Controller without requiring a real agent process. The buffer
// machinery is independent of Start/Spawner; it operates purely on
// internal struct state.
//
// CW-G4 §"Reattach end-to-end flow" + §"Reattach behavior".

func TestController_InitialBufferDeliveredToFirstSubscriber(t *testing.T) {
	c := &Controller{}
	c.SetInitialPTYBuffer([]byte("redraw_payload"))

	ch, unsub := c.SubscribePTYData()
	defer unsub()

	select {
	case data := <-ch:
		if !bytes.Equal(data.Bytes, []byte("redraw_payload")) {
			t.Fatalf("first chunk = %q; want %q", data.Bytes, "redraw_payload")
		}
		if data.Seq != 0 {
			t.Fatalf("initial buffer Seq = %d; want 0", data.Seq)
		}
	case <-time.After(time.Second):
		t.Fatalf("initial buffer not delivered")
	}
}

func TestController_InitialBufferOnlyDeliveredOnce(t *testing.T) {
	c := &Controller{}
	c.SetInitialPTYBuffer([]byte("once"))

	ch1, unsub1 := c.SubscribePTYData()
	defer unsub1()
	first := <-ch1
	if !bytes.Equal(first.Bytes, []byte("once")) {
		t.Fatalf("first sub got %q; want %q", first.Bytes, "once")
	}

	ch2, unsub2 := c.SubscribePTYData()
	defer unsub2()
	select {
	case data := <-ch2:
		if bytes.Equal(data.Bytes, []byte("once")) {
			t.Fatalf("second subscriber should NOT see initial buffer; got %q", data.Bytes)
		}
	case <-time.After(100 * time.Millisecond):
		// Expected: no live data, no initial buffer for second subscriber.
	}
}

func TestController_InitialBufferEmptyIsNoOp(t *testing.T) {
	c := &Controller{}
	c.SetInitialPTYBuffer([]byte{}) // empty

	ch, unsub := c.SubscribePTYData()
	defer unsub()

	select {
	case data := <-ch:
		t.Fatalf("empty initial buffer should not be delivered; got %q", data.Bytes)
	case <-time.After(100 * time.Millisecond):
		// Expected
	}
}

func TestController_InitialBufferReplaceBeforeDrain(t *testing.T) {
	// SetInitialPTYBuffer called twice before Subscribe — last write wins.
	c := &Controller{}
	c.SetInitialPTYBuffer([]byte("first"))
	c.SetInitialPTYBuffer([]byte("second"))

	ch, unsub := c.SubscribePTYData()
	defer unsub()

	select {
	case data := <-ch:
		if !bytes.Equal(data.Bytes, []byte("second")) {
			t.Fatalf("got %q; want %q (last write wins)", data.Bytes, "second")
		}
	case <-time.After(time.Second):
		t.Fatalf("initial buffer not delivered")
	}
}
