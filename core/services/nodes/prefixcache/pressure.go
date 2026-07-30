package prefixcache

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/xlog"
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
	seen   map[string]time.Time
	pub    publisher
	origin string
	seq    atomic.Uint64
}

// NewPressure creates a Pressure counter that remembers events for the given
// rolling window.
func NewPressure(window time.Duration) *Pressure {
	return &Pressure{
		window: window,
		events: make(map[string][]time.Time),
		seen:   make(map[string]time.Time),
	}
}

// NewSyncedPressure creates a Pressure counter that broadcasts locally
// originated events so every frontend sees the same cluster-wide signal.
func NewSyncedPressure(window time.Duration, pub publisher) *Pressure {
	p := NewPressure(window)
	p.pub = pub
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		p.origin = fmt.Sprintf("%d", time.Now().UnixNano())
	} else {
		p.origin = hex.EncodeToString(id[:])
	}
	return p
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
	id := fmt.Sprintf("%s:%d", p.origin, p.seq.Add(1))
	p.record(model, id, now)
	recordPressureMetric(model)
	if p.pub != nil {
		ev := messaging.PrefixCachePressureEvent{ID: id, Model: model}
		if err := p.pub.Publish(messaging.SubjectPrefixCachePressure, ev); err != nil {
			xlog.Debug("prefixcache: pressure publish failed", "error", err)
		}
	}
}

func (p *Pressure) record(model, id string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cutoff := now.Add(-p.window)
	for eventID, recordedAt := range p.seen {
		if recordedAt.Before(cutoff) {
			delete(p.seen, eventID)
		}
	}
	if id != "" {
		if _, exists := p.seen[id]; exists {
			return
		}
		p.seen[id] = now
	}
	kept := append(pruneLocked(p.events[model], cutoff), now)
	p.events[model] = kept
}

// ApplyPressure records a pressure event received from NATS without
// re-broadcasting it. Duplicate deliveries, including the publisher's own echo,
// are ignored by event ID.
func (p *Pressure) ApplyPressure(ev messaging.PrefixCachePressureEvent, now time.Time) {
	if ev.ID == "" || ev.Model == "" {
		return
	}
	if ev.Reset {
		p.reset(ev.Model, ev.ID, now)
		return
	}
	p.record(ev.Model, ev.ID, now)
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
	id := fmt.Sprintf("%s:%d", p.origin, p.seq.Add(1))
	p.reset(model, id, time.Now())
	if p.pub != nil {
		ev := messaging.PrefixCachePressureEvent{ID: id, Model: model, Reset: true}
		if err := p.pub.Publish(messaging.SubjectPrefixCachePressure, ev); err != nil {
			xlog.Debug("prefixcache: pressure reset publish failed", "error", err)
		}
	}
}

func (p *Pressure) reset(model, id string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.seen[id]; exists {
		return
	}
	p.seen[id] = now
	delete(p.events, model)
}
