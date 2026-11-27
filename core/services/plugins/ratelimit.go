package plugins

import (
	"errors"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limits of the inspects of one frontend. Each inspect runs a git fetch of up
// to the scratch cap and writes the package for the review, so a user who
// scripts inspects across the entries of a marketplace must not tie up the
// fetches or fill the store.
const (
	// InspectBurst inspects may run back to back for one user; then one
	// more every InspectEvery.
	InspectBurst = 6
	InspectEvery = 10 * time.Second
	// MaxConcurrentInspects is the most inspects one frontend runs at once,
	// for all users.
	MaxConcurrentInspects = 4
	// maxLimiters bounds the map of per-user limiters; full ones are dropped
	// first.
	maxLimiters = 4096
)

// ErrRateLimited refuses an inspect over the limits.
var ErrRateLimited = errors.New("plugins: too many plugin reviews at once; wait a moment and try again")

// inspectLimiter keeps the per-user limits of inspects. Mode: per instance,
// in memory; each frontend applies the limit to the requests it serves.
type inspectLimiter struct {
	mu   sync.Mutex
	per  map[string]*rate.Limiter
	busy chan struct{}
}

func newInspectLimiter() *inspectLimiter {
	return &inspectLimiter{per: map[string]*rate.Limiter{}, busy: make(chan struct{}, MaxConcurrentInspects)}
}

// take reserves one inspect for principal at now. The caller calls release
// when the inspect ends. An administrator is not limited per user, only by
// the number of inspects at once.
func (l *inspectLimiter) take(principal string, admin bool, now time.Time) (release func(), err error) {
	if !admin {
		l.mu.Lock()
		lim, ok := l.per[principal]
		if !ok {
			if len(l.per) >= maxLimiters {
				l.prune(now)
			}
			lim = rate.NewLimiter(rate.Every(InspectEvery), InspectBurst)
			l.per[principal] = lim
		}
		allowed := lim.AllowN(now, 1)
		l.mu.Unlock()
		if !allowed {
			return nil, ErrRateLimited
		}
	}
	select {
	case l.busy <- struct{}{}:
		return func() { <-l.busy }, nil
	default:
		return nil, ErrRateLimited
	}
}

// prune drops the limiters that are full again, which hold no state. With
// mu held.
func (l *inspectLimiter) prune(now time.Time) {
	for k, lim := range l.per {
		if lim.TokensAt(now) >= InspectBurst {
			delete(l.per, k)
		}
	}
}
