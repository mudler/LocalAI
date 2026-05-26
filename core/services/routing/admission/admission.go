// Package admission is routing-module subsystem 5: per-model
// concurrency control + audit. The middleware acquires a slot
// before the handler runs; on full, the request gets 503 with
// Retry-After so clients back off rather than pile on. The audit
// row goes into the shared event store alongside PII and proxy
// rows so admins see a single timeline of routing pressure.
//
// Concurrency model: one buffered channel per model name (kept in
// a sync.Map). Acquire is a non-blocking send; full = reject. No
// queueing in the MVP — adding queue depth + timeout is a small
// follow-up if/when telemetry shows admins want it.
package admission

import (
	"sync"
	"time"
)

// Limiter holds the per-model semaphores. Safe for concurrent use.
//
// Each model's slot count is fixed at first Acquire — a config
// edit that reduces MaxConcurrent only takes effect on the NEXT
// process start (or after the limiter is rebuilt). The alternative
// (dynamic resize on every call) would require swapping the channel
// out from under in-flight Acquires; the simplicity tradeoff favors
// "restart to apply" since admins editing limits do so rarely.
type Limiter struct {
	mu    sync.Mutex
	slots map[string]chan struct{}
}

// New returns an empty Limiter.
func New() *Limiter {
	return &Limiter{slots: make(map[string]chan struct{})}
}

// Acquire takes a slot for the named model. maxConcurrent <= 0
// means unlimited — Acquire returns immediately with a no-op
// release. When all slots are busy, returns ok=false. Callers
// MUST call the returned release when done (typically via defer);
// missing a release leaks one slot for the lifetime of the
// process.
func (l *Limiter) Acquire(modelName string, maxConcurrent int) (release func(), ok bool) {
	if maxConcurrent <= 0 {
		return func() {}, true
	}
	ch := l.slot(modelName, maxConcurrent)
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, true
	default:
		return nil, false
	}
}

// InFlight reports the number of currently-held slots for the
// named model. Used by the admin status surface — read-only and
// approximate (ch length is racy with concurrent Acquire/release
// but that's fine for a dashboard).
func (l *Limiter) InFlight(modelName string) int {
	l.mu.Lock()
	ch, ok := l.slots[modelName]
	l.mu.Unlock()
	if !ok {
		return 0
	}
	return len(ch)
}

// Capacity reports the limiter's configured slot count for the
// named model, or 0 if the model has never had Acquire called
// against it. Same dashboard-only purpose as InFlight.
func (l *Limiter) Capacity(modelName string) int {
	l.mu.Lock()
	ch, ok := l.slots[modelName]
	l.mu.Unlock()
	if !ok {
		return 0
	}
	return cap(ch)
}

// slot returns the per-model channel, creating it on first use.
func (l *Limiter) slot(modelName string, capacity int) chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ch, ok := l.slots[modelName]; ok {
		return ch
	}
	ch := make(chan struct{}, capacity)
	l.slots[modelName] = ch
	return ch
}

// RetryAfter returns the Retry-After header value for a rejected
// request. The Limiter doesn't track rolling latency — this is a
// pure config-driven hint, identity-mapped to the LimitsConfig
// field with a 1s fallback. Centralised here so the middleware
// doesn't reimplement the default rule.
func RetryAfter(configured int) time.Duration {
	if configured > 0 {
		return time.Duration(configured) * time.Second
	}
	return time.Second
}
