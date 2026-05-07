// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/0xmhha/cli-wrapper/internal/cwtypes"
)

// defaultRingBufferSize is the in-memory PTY scrollback used when
// Spec.RingBufferSize is unset (zero). 256 KiB covers ~1000 lines of
// typical mixed CLI output (raw bytes + ANSI sequences).
const defaultRingBufferSize = 256 * 1024

// persistentState holds the per-session resources owned by a persistent
// agent: meta files, UNIX listener, ring buffer, and the attach lock.
//
// Spec: docs/superpowers/specs/2026-05-07-CW-G4-persistent-reattach-design.md
type persistentState struct {
	sessionDir string
	listener   *net.UnixListener
	ring       *ringBuffer

	mu       sync.Mutex
	attached bool
}

type persistentInitOpts struct {
	SessionDir string
	AgentID    string
	Spec       cwtypes.Spec
}

// initPersistent runs the bootstrap sequence for an agent invoked with
// --persistent: ignores SIGHUP, allocates the ring buffer, writes
// meta.json + pid, opens UNIX listener at <sessionDir>/sock.
//
// Returns the persistentState the caller must Close() on shutdown.
func initPersistent(opts persistentInitOpts) (*persistentState, error) {
	if opts.SessionDir == "" {
		return nil, errors.New("persistent: SessionDir required")
	}
	if err := os.MkdirAll(opts.SessionDir, 0o700); err != nil {
		return nil, fmt.Errorf("persistent: mkdir SessionDir: %w", err)
	}

	// Defense-in-depth: Setsid (set by spawner) already detaches us from a
	// controlling terminal, but explicitly ignore SIGHUP in case the process
	// group somehow delivers it.
	signal.Ignore(syscall.SIGHUP)

	// Allocate ring buffer first so failure leaves no partial filesystem state.
	size := opts.Spec.RingBufferSize
	if size == 0 {
		size = defaultRingBufferSize
	}
	if size <= 0 {
		return nil, fmt.Errorf("persistent: invalid RingBufferSize %d", size)
	}
	ring := newRingBuffer(size)

	// Write meta.json before opening the listener. A partial dir is detectable
	// as "no sock yet" by ListPersistent.
	meta := PersistentMeta{
		Version:   "1.0",
		ID:        opts.AgentID,
		Spec:      opts.Spec,
		AgentPID:  os.Getpid(),
		StartedAt: time.Now().UTC(),
	}
	if err := WritePersistentMeta(opts.SessionDir, meta); err != nil {
		return nil, fmt.Errorf("persistent: write meta: %w", err)
	}
	if err := WritePidFile(opts.SessionDir, os.Getpid()); err != nil {
		return nil, fmt.Errorf("persistent: write pid: %w", err)
	}

	sockPath := filepath.Join(opts.SessionDir, "sock")
	// Remove a stale sock file (from a prior crashed agent that didn't
	// clean up). Spawn-time ID conflict policy already rejected existing
	// sessions, so any sock file present here is leftover state.
	_ = os.Remove(sockPath)

	addr := &net.UnixAddr{Name: sockPath, Net: "unix"}
	listener, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("persistent: listen %s: %w", sockPath, err)
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("persistent: chmod sock: %w", err)
	}

	return &persistentState{
		sessionDir: opts.SessionDir,
		listener:   listener,
		ring:       ring,
	}, nil
}

// Close closes the listener and removes sock + pid files. meta.json + agent.log
// are intentionally preserved for post-mortem (the user can run
// `cliwrap list --persistent` and inspect them).
func (p *persistentState) Close() {
	if p == nil {
		return
	}
	if p.listener != nil {
		_ = p.listener.Close()
	}
	_ = os.Remove(filepath.Join(p.sessionDir, "sock"))
	_ = os.Remove(filepath.Join(p.sessionDir, "pid"))
	_ = os.Remove(filepath.Join(p.sessionDir, ".attached"))
}

// ringBufferCapForTest exposes the ring buffer capacity for unit tests.
// Production code does not call this.
func (p *persistentState) ringBufferCapForTest() int {
	if p == nil || p.ring == nil {
		return 0
	}
	return len(p.ring.data)
}
