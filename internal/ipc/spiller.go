// SPDX-License-Identifier: Apache-2.0

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
