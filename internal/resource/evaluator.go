package resource

import (
	"sync"

	"github.com/cli-wrapper/cli-wrapper/pkg/cliwrap"
)

// Action describes an enforcement decision.
type Action struct {
	ProcessID    string
	Kind         string // "rss" | "cpu"
	Current      float64
	Limit        float64
	ExceedAction cliwrap.ExceedAction
}

// EvaluatorOptions parameterises NewEvaluator.
type EvaluatorOptions struct {
	// OnAction is invoked (synchronously) when a sustained breach is detected.
	OnAction func(Action)

	// RSSBreachThreshold sets the consecutive breach count for memory.
	// Default 3.
	RSSBreachThreshold int

	// CPUBreachThreshold sets the consecutive breach count for CPU.
	// Default 10 (CPU samples are noisier).
	CPUBreachThreshold int
}

// Evaluator tracks per-process limit breaches and fires actions.
type Evaluator struct {
	mu       sync.Mutex
	opts     EvaluatorOptions
	limits   map[string]cliwrap.ResourceLimits
	rssCount map[string]int
	cpuCount map[string]int
}

// NewEvaluator returns an Evaluator.
func NewEvaluator(opts EvaluatorOptions) *Evaluator {
	if opts.RSSBreachThreshold <= 0 {
		opts.RSSBreachThreshold = 3
	}
	if opts.CPUBreachThreshold <= 0 {
		opts.CPUBreachThreshold = 10
	}
	return &Evaluator{
		opts:     opts,
		limits:   make(map[string]cliwrap.ResourceLimits),
		rssCount: make(map[string]int),
		cpuCount: make(map[string]int),
	}
}

// RegisterLimits associates limits with a process ID.
func (e *Evaluator) RegisterLimits(id string, l cliwrap.ResourceLimits) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.limits[id] = l
}

// Unregister removes a process ID.
func (e *Evaluator) Unregister(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.limits, id)
	delete(e.rssCount, id)
	delete(e.cpuCount, id)
}

// Evaluate checks sample against the limits of id and may fire actions.
func (e *Evaluator) Evaluate(id string, sample Sample) {
	e.mu.Lock()
	limits, ok := e.limits[id]
	if !ok {
		e.mu.Unlock()
		return
	}

	var fired []Action

	if limits.MaxRSS > 0 {
		if sample.RSS > limits.MaxRSS {
			e.rssCount[id]++
			if e.rssCount[id] >= e.opts.RSSBreachThreshold {
				fired = append(fired, Action{
					ProcessID:    id,
					Kind:         "rss",
					Current:      float64(sample.RSS),
					Limit:        float64(limits.MaxRSS),
					ExceedAction: limits.OnExceed,
				})
				e.rssCount[id] = 0
			}
		} else {
			e.rssCount[id] = 0
		}
	}

	if limits.MaxCPUPercent > 0 {
		if sample.CPUPercent > limits.MaxCPUPercent {
			e.cpuCount[id]++
			if e.cpuCount[id] >= e.opts.CPUBreachThreshold {
				fired = append(fired, Action{
					ProcessID:    id,
					Kind:         "cpu",
					Current:      sample.CPUPercent,
					Limit:        limits.MaxCPUPercent,
					ExceedAction: limits.OnExceed,
				})
				e.cpuCount[id] = 0
			}
		} else {
			e.cpuCount[id] = 0
		}
	}

	e.mu.Unlock()

	if e.opts.OnAction != nil {
		for _, a := range fired {
			e.opts.OnAction(a)
		}
	}
}
