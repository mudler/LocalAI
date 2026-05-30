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
// Entries older than the window are dropped on both Record and Count, so the
// slice never grows unbounded - even for a model that takes records but is
// never Counted (e.g. one with zero loaded replicas the reconciler skips). An
// idle model's history also decays to zero on the next read.
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

// pruneLocked drops entries older than cutoff, compacting in place. The cutoff
// boundary itself is inclusive so an event exactly window-old still counts.
// Callers must hold p.mu.
func pruneLocked(ts []time.Time, cutoff time.Time) []time.Time {
	kept := ts[:0]
	for _, t := range ts {
		if !t.Before(cutoff) {
			kept = append(kept, t)
		}
	}
	return kept
}

// Record appends a forced-disturb timestamp for the model and prunes entries
// older than the window, so the per-model slice stays bounded regardless of how
// often Count runs.
func (p *Pressure) Record(model string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cutoff := now.Add(-p.window)
	kept := append(pruneLocked(p.events[model], cutoff), now)
	p.events[model] = kept
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
	kept := pruneLocked(ts, now.Add(-p.window))
	if len(kept) == 0 {
		delete(p.events, model)
		return 0
	}
	p.events[model] = kept
	return len(kept)
}

// Reset clears all recorded events for model. Call after acting on the signal
// (a pressure-triggered scale-up) so a single burst does not trigger repeated
// scale-ups across consecutive ticks.
func (p *Pressure) Reset(model string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.events, model)
}
