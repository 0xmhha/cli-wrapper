// SPDX-License-Identifier: Apache-2.0

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
//
// The ordering is: rename(tmp, path) FIRST, then close(old fd), then reopen.
// This ensures the file at w.path is always valid even if the process crashes
// mid-Retire.
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
