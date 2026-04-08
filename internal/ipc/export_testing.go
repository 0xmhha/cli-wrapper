// SPDX-License-Identifier: Apache-2.0

package ipc

// WALReplayForTest exposes the Spiller's WAL replay to external test packages
// (e.g. test/chaos). It lives in a non-test file because Go's _test.go files
// are only visible to the same package's tests, but chaos tests live in a
// separate module subdirectory and import ipc as a regular dependency.
//
// This method is safe for concurrent use with Ack/ReplayInto and exists solely
// to let chaos tests inspect unacked WAL contents after forced disconnects.
func (s *Spiller) WALReplayForTest() ([]OutboxMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wal.Replay()
}
