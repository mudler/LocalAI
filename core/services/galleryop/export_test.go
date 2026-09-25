package galleryop

import (
	"github.com/mudler/LocalAI/pkg/downloader"
)

// Test-only seams onto unexported behaviour. Compiled into the test binary
// only, so nothing here reaches the shipped surface.

// ApplyEndForTest drives the NATS end path without standing up a broker.
// applyEnd is unexported and only ever reached from a subscription callback,
// which an external test package cannot trigger.
func (m *OpCache) ApplyEndForTest(jobID string) {
	m.applyEnd(OpCacheEvent{JobID: jobID})
}

// StoreRateLimiterForTest injects a rate limiter for an operation so external
// endpoint tests can exercise the success path without booting the worker.
func (g *GalleryService) StoreRateLimiterForTest(id string, rl *downloader.DynamicRateLimiter) {
	g.Lock()
	defer g.Unlock()
	if g.rateLimiters == nil {
		g.rateLimiters = make(map[string]*downloader.DynamicRateLimiter)
	}
	g.rateLimiters[id] = rl
}
