package nodes

import (
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// probeCache memoizes recent successful gRPC HealthCheck results for
// (nodeID, addr) tuples so SmartRouter.probeHealth doesn't pay a round-trip
// on every inference request.
//
// Why this exists: with per-request routing (see pkg/model/loader.go), every
// inference call goes through SmartRouter.Route, which probes the backend
// before returning a client. Many gRPC backends (notably llama.cpp's server)
// serialize HealthCheck against active Predict on a shared goroutine, so a
// burst of new requests can stall behind a single long-running stream —
// exactly the "queue stalling" symptom observed in distributed clusters.
//
// The background HealthMonitor (perModelHealthCheck) is still the cluster-wide
// source of truth that reaps actually-dead backends within ~45s; this cache
// only saves the per-request hot path from re-asking when nothing has changed.
//
// TTL matches healthCheckTTL in pkg/model/model.go so the single-process
// IsRecentlyHealthy path and this distributed-mode path share the same
// staleness budget.
type probeCache struct {
	ttl    time.Duration
	mu     sync.Mutex
	seen   map[string]time.Time // key → last successful probe
	flight singleflight.Group   // coalesces concurrent probes for the same key
}

// newProbeCache returns a probeCache with the given TTL. Zero TTL disables
// caching: every call to DoOrCached invokes the probe.
func newProbeCache(ttl time.Duration) *probeCache {
	return &probeCache{
		ttl:  ttl,
		seen: make(map[string]time.Time),
	}
}

// IsFresh reports whether key was successfully probed within TTL.
func (c *probeCache) IsFresh(key string) bool {
	if c.ttl <= 0 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	last, ok := c.seen[key]
	return ok && time.Since(last) < c.ttl
}

// markFresh records key as successfully probed at the current time.
func (c *probeCache) markFresh(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen[key] = time.Now()
}

// Invalidate drops any cached freshness for key. Used after a probe failure
// (or any other signal that the backend may not be alive) so the next call
// will re-probe instead of trusting stale state.
func (c *probeCache) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.seen, key)
}

// DoOrCached returns true if key is fresh; otherwise it runs probe (coalescing
// concurrent callers via singleflight) and caches a successful result. Failed
// probes invalidate the cache, so a transient miss doesn't pin every
// subsequent request to a re-probe.
func (c *probeCache) DoOrCached(key string, probe func() bool) bool {
	alive, _ := c.DoOrCachedResult(key, func() (bool, error) { return probe(), nil })
	return alive
}

// DoOrCachedResult is DoOrCached with a second result: the reason the probe
// never reached the backend, or nil when it did.
//
// The second result travels through the SINGLEFLIGHT, which is the whole reason
// it is not simply a variable the caller closes over. A closed-over variable is
// only written by the goroutine that actually runs the probe; every other
// caller coalesced into that flight adopts the leader's boolean and sees its own
// unset variable, so the leader would correctly decline to reap while its
// joiners reaped on the very same observation. Carrying it in singleflight's
// error slot hands every joiner the leader's reason as well as its answer.
//
// A probe that could not reach the backend is NOT cached either way. Caching it
// as fresh would hide a genuinely dead backend behind a network blip, and
// caching it as a failure is what Invalidate already does.
func (c *probeCache) DoOrCachedResult(key string, probe func() (bool, error)) (bool, error) {
	if c.IsFresh(key) {
		return true, nil
	}
	v, unreached, _ := c.flight.Do(key, func() (any, error) {
		// Double-check after potentially waiting: another caller in this
		// flight may have just populated the cache.
		if c.IsFresh(key) {
			return true, nil
		}
		ok, unreached := probe()
		if ok {
			c.markFresh(key)
		} else {
			c.Invalidate(key)
		}
		return ok, unreached
	})
	return v.(bool), unreached
}
