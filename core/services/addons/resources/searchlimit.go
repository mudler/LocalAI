// SPDX-License-Identifier: MIT
package resources

import (
	"sync"
	"time"
)

// maxSearchers bounds the people the limiter remembers. A person whose
// bucket is full again is forgotten first, which loses nothing.
const maxSearchers = 4096

// searchLimiter is a token bucket per person. It is per-instance state of
// the process that holds the picker, and a restart refills every bucket. The
// picker of the sharing routes of the kinds lives on the add-on owner, the
// only process that answers it, so that limit holds for the whole cluster.
// The picker of the folders of a home lives on every frontend
// (studioFolderDirectory), so that limit holds per frontend.
type searchLimiter struct {
	mu      sync.Mutex
	burst   float64
	every   time.Duration
	now     func() time.Time
	buckets map[string]*searchBucket
}

type searchBucket struct {
	tokens float64
	at     time.Time
}

func newSearchLimiter(burst int, every time.Duration, now func() time.Time) *searchLimiter {
	if now == nil {
		now = time.Now
	}
	return &searchLimiter{burst: float64(burst), every: every, now: now, buckets: map[string]*searchBucket{}}
}

// allow takes one search from the person's bucket.
func (l *searchLimiter) allow(user string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[user]
	if b == nil {
		if len(l.buckets) >= maxSearchers {
			l.prune(now)
		}
		if len(l.buckets) >= maxSearchers {
			// Everybody remembered is still searching: refuse rather than
			// forget a bucket that is not full, which would reset it.
			return false
		}
		b = &searchBucket{tokens: l.burst, at: now}
		l.buckets[user] = b
	}
	l.refill(b, now)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *searchLimiter) refill(b *searchBucket, now time.Time) {
	if elapsed := now.Sub(b.at); elapsed > 0 {
		b.tokens = min(l.burst, b.tokens+float64(elapsed)/float64(l.every))
		b.at = now
	}
}

// prune forgets the buckets that are full again.
func (l *searchLimiter) prune(now time.Time) {
	for user, b := range l.buckets {
		l.refill(b, now)
		if b.tokens >= l.burst {
			delete(l.buckets, user)
		}
	}
}
