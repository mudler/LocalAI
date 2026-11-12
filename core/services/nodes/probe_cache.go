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
	return c.DoOrCachedVerdict(key, func() probeVerdict {
		if probe() {
			return probeAlive
		}
		return probeDead
	}) == probeAlive
}

// probeVerdict is what a probe of a backend found. Only probeDead is a statement
// that the backend is gone.
type probeVerdict int

const (
	// probeAlive: the backend answered.
	probeAlive probeVerdict = iota
	// probeDead: the backend did not answer, and the probe reached its host.
	probeDead
	// probeUnknown: the probe could not reach the host, because the transport
	// failed. Nothing was learned about the backend.
	probeUnknown
)

// DoOrCachedVerdict is DoOrCached for a probe that can say that it learned
// nothing. Only probeAlive is cached, and every other verdict invalidates the
// cache. Callers that coalesce on the same key all get the verdict of the one
// probe that ran.
func (c *probeCache) DoOrCachedVerdict(key string, probe func() probeVerdict) probeVerdict {
	if c.IsFresh(key) {
		return probeAlive
	}
	v, _, _ := c.flight.Do(key, func() (any, error) {
		// Double-check after potentially waiting: another caller in this
		// flight may have just populated the cache.
		if c.IsFresh(key) {
			return probeAlive, nil
		}
		verdict := probe()
		if verdict == probeAlive {
			c.markFresh(key)
		} else {
			c.Invalidate(key)
		}
		return verdict, nil
	})
	return v.(probeVerdict)
}
