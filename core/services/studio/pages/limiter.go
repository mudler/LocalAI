package pages

import (
	"sync"
	"time"
)

// maxBuckets bounds the limiter's memory; full buckets are dropped first.
const maxBuckets = 10000

// limiter is a token bucket per key. It is per frontend: with several frontends
// a page gets one budget on each, which is fine for a guard against a page that
// loops, and needs no shared state.
type limiter struct {
	mu      sync.Mutex
	burst   float64
	every   time.Duration // one token comes back after every
	now     func() time.Time
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

func newLimiter(burst int, every time.Duration, now func() time.Time) *limiter {
	return &limiter{burst: float64(burst), every: every, now: now, buckets: map[string]*bucket{}}
}

func (l *limiter) refill(b *bucket, now time.Time) {
	if gained := float64(now.Sub(b.at)) / float64(l.every); gained > 0 {
		b.tokens = min(l.burst, b.tokens+gained)
		b.at = now
	}
}

// allow takes one token of key and says whether there was one.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= maxBuckets {
			l.prune(now)
		}
		b = &bucket{tokens: l.burst, at: now}
		l.buckets[key] = b
	}
	l.refill(b, now)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// prune drops the buckets that are full again: they hold no state.
func (l *limiter) prune(now time.Time) {
	for k, b := range l.buckets {
		l.refill(b, now)
		if b.tokens >= l.burst {
			delete(l.buckets, k)
		}
	}
}
