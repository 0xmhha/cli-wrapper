package resource

import (
	"sync"
	"time"
)

// PIDSource is any object that can report the current set of (id, PID) pairs
// that should be sampled.
type PIDSource interface {
	ChildPIDs() map[string]int
}

// MonitorOptions configures a Monitor.
type MonitorOptions struct {
	Interval         time.Duration
	Source           PIDSource
	ProcessCollector ProcessCollector
	SystemCollector  SystemCollector
	OnSample         func(id string, s Sample)
	OnSystem         func(s SystemSample)
}

// Monitor periodically polls every child process in its Source.
type Monitor struct {
	opts MonitorOptions
	stop chan struct{}
	wg   sync.WaitGroup
	once sync.Once
}

// NewMonitor returns a Monitor.
func NewMonitor(opts MonitorOptions) *Monitor {
	if opts.Interval <= 0 {
		opts.Interval = 5 * time.Second
	}
	return &Monitor{opts: opts, stop: make(chan struct{})}
}

// Start launches the ticker goroutine. Calling Start more than once is a no-op.
func (m *Monitor) Start() {
	m.once.Do(func() {
		m.wg.Add(1)
		go m.loop()
	})
}

// Stop halts the ticker and waits for the goroutine to exit.
func (m *Monitor) Stop() {
	close(m.stop)
	m.wg.Wait()
}

func (m *Monitor) loop() {
	defer m.wg.Done()
	t := time.NewTicker(m.opts.Interval)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			m.tick()
		}
	}
}

func (m *Monitor) tick() {
	if m.opts.Source == nil {
		return
	}
	for id, pid := range m.opts.Source.ChildPIDs() {
		if pid <= 0 || m.opts.ProcessCollector == nil {
			continue
		}
		s, err := m.opts.ProcessCollector.Collect(pid)
		if err != nil {
			continue
		}
		if m.opts.OnSample != nil {
			m.opts.OnSample(id, s)
		}
	}
	if m.opts.SystemCollector != nil {
		if s, err := m.opts.SystemCollector.Collect(); err == nil && m.opts.OnSystem != nil {
			m.opts.OnSystem(s)
		}
	}
}
