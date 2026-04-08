// SPDX-License-Identifier: Apache-2.0

package logcollect

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RotatorOptions configures a FileRotator.
type RotatorOptions struct {
	Dir      string
	BaseName string
	MaxSize  int64 // rotate when current file exceeds this many bytes
	MaxFiles int   // keep at most this many files (including current)
}

// FileRotator writes to an active log file and rotates it when the size cap
// is exceeded. Rotated files are named "<base>.<n>.log" for n = 1..MaxFiles-1.
type FileRotator struct {
	opts RotatorOptions
	mu   sync.Mutex
	f    *os.File
	size int64
}

// NewFileRotator opens (creating if necessary) the active log file.
func NewFileRotator(opts RotatorOptions) (*FileRotator, error) {
	if opts.MaxSize <= 0 {
		opts.MaxSize = 64 * 1024 * 1024
	}
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = 7
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("rotator: mkdir: %w", err)
	}
	f, err := openActive(opts)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &FileRotator{opts: opts, f: f, size: info.Size()}, nil
}

// Write appends data, rotating if needed.
func (r *FileRotator) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.size+int64(len(p)) > r.opts.MaxSize {
		if err := r.rotateLocked(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	if err != nil {
		return n, err
	}
	r.size += int64(n)
	return n, nil
}

// Close flushes and closes the active file.
func (r *FileRotator) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

func (r *FileRotator) rotateLocked() error {
	if err := r.f.Close(); err != nil {
		return fmt.Errorf("rotator: close active: %w", err)
	}
	// Shift: base.(N-1).log -> base.N.log ... base.1.log -> base.2.log
	for i := r.opts.MaxFiles - 1; i >= 1; i-- {
		from := rotatedPath(r.opts, i)
		to := rotatedPath(r.opts, i+1)
		if _, err := os.Stat(from); err == nil {
			if i+1 >= r.opts.MaxFiles {
				_ = os.Remove(from)
				continue
			}
			if err := os.Rename(from, to); err != nil {
				return fmt.Errorf("rotator: rename: %w", err)
			}
		}
	}
	if err := os.Rename(activePath(r.opts), rotatedPath(r.opts, 1)); err != nil {
		return fmt.Errorf("rotator: rename active: %w", err)
	}
	f, err := openActive(r.opts)
	if err != nil {
		return err
	}
	r.f = f
	r.size = 0
	return nil
}

func activePath(opts RotatorOptions) string {
	return filepath.Join(opts.Dir, opts.BaseName+".log")
}

func rotatedPath(opts RotatorOptions, n int) string {
	return filepath.Join(opts.Dir, fmt.Sprintf("%s.%d.log", opts.BaseName, n))
}

func openActive(opts RotatorOptions) (*os.File, error) {
	return os.OpenFile(activePath(opts), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}
