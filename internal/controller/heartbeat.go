// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// PingSender is any object that can emit a PING message.
type PingSender interface {
	SendPing() error
}

// HeartbeatOptions configures a Heartbeat.
type HeartbeatOptions struct {
	Sender        PingSender
	Interval      time.Duration
	Timeout       time.Duration
	MissThreshold int
	OnMiss        func(consecutiveMisses int)
}

// Heartbeat sends periodic PINGs and fires OnMiss after the configured number
// of consecutive missing PONGs.
type Heartbeat struct {
	opts     HeartbeatOptions
	lastPong atomic.Int64
	wg       sync.WaitGroup
	running  atomic.Bool
}

// NewHeartbeat returns a Heartbeat.
func NewHeartbeat(opts HeartbeatOptions) *Heartbeat {
	if opts.Interval <= 0 {
		opts.Interval = 5 * time.Second
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Second
	}
	if opts.MissThreshold <= 0 {
		opts.MissThreshold = 3
	}
	hb := &Heartbeat{opts: opts}
	hb.lastPong.Store(time.Now().UnixNano())
	return hb
}

// Start launches the heartbeat goroutine.
func (h *Heartbeat) Start(ctx context.Context) {
	if h.running.Swap(true) {
		return
	}
	h.wg.Add(1)
	go h.loop(ctx)
}

// RecordPong resets the miss counter to zero.
func (h *Heartbeat) RecordPong() {
	h.lastPong.Store(time.Now().UnixNano())
}

// Wait blocks until the loop exits.
func (h *Heartbeat) Wait() { h.wg.Wait() }

func (h *Heartbeat) loop(ctx context.Context) {
	defer h.wg.Done()
	ticker := time.NewTicker(h.opts.Interval)
	defer ticker.Stop()

	misses := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = h.opts.Sender.SendPing()

			// Sleep for the timeout then check if a pong has arrived since
			// the last tick.
			deadline := time.NewTimer(h.opts.Timeout)
			select {
			case <-ctx.Done():
				deadline.Stop()
				return
			case <-deadline.C:
			}

			age := time.Since(time.Unix(0, h.lastPong.Load()))
			if age > h.opts.Interval+h.opts.Timeout {
				misses++
				if misses >= h.opts.MissThreshold && h.opts.OnMiss != nil {
					h.opts.OnMiss(misses)
					misses = 0 // avoid spamming
				}
			} else {
				misses = 0
			}
		}
	}
}
