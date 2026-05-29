package prefixcache

import (
	"sync"
	"time"
)

// Pressure is a concurrency-safe rolling per-model counter of forced-disturb
// events. A forced-disturb is recorded by the router when a usable hot prefix
// match existed but the load guard forced the request off the warm node (see
// SmartRouter.buildPreference). The reconciler reads Count to decide whether
// the cache-warm replica is saturated enough to warrant a scale-up.
//
// Entries older than the window are dropped lazily on Count, so the slice never
// grows unbounded for an active model and an idle model's history decays to
// zero on the next read.
type Pressure struct {
	mu     sync.Mutex
	window time.Duration
	events map[string][]time.Time
}

// NewPressure creates a Pressure counter that remembers events for the given
// rolling window.
func NewPressure(window time.Duration) *Pressure {
	return &Pressure{
		window: window,
		events: make(map[string][]time.Time),
	}
}

// Record appends a forced-disturb timestamp for the model.
func (p *Pressure) Record(model string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events[model] = append(p.events[model], now)
}

// Count returns the number of records for the model within [now-window, now],
// dropping any entries older than the window so the backing slice stays bounded.
func (p *Pressure) Count(model string, now time.Time) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	ts := p.events[model]
	if len(ts) == 0 {
		return 0
	}
	cutoff := now.Add(-p.window)
	kept := ts[:0]
	for _, t := range ts {
		// Keep entries within [now-window, now]; the cutoff boundary itself is
		// inclusive so an event exactly window-old still counts.
		if !t.Before(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(p.events, model)
		return 0
	}
	p.events[model] = kept
	return len(kept)
}
