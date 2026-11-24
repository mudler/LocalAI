package p2ppair

import (
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// Limiter rate limits wrong codes. The caller supplies the implementation (and
// the source) so that a cluster can share the counts; NewLimiter is a
// per-process default.
type Limiter interface {
	// Attempt checks and records one redeem attempt from this peer and source
	// in one step, so that attempts at the same time cannot all pass a check
	// before any of them is counted. It returns false when the attempt must be
	// refused now; nothing is recorded then. Otherwise the attempt counts as a
	// wrong code until the caller calls refund, which it does when the code
	// was right or the failure was not the code's fault. refund is never nil
	// and may be called once.
	//
	// source is the network address that the caller saw (for a relayed
	// connection, the relay's view of the dialer); it may be empty, which
	// turns the per-source limit off.
	Attempt(p peer.ID, source string) (ok bool, refund func())
}

// Limits of the default limiter.
const (
	// MaxFailuresPerPeer is the number of wrong codes one peer may send per
	// PeerWindow.
	MaxFailuresPerPeer = 5
	// PeerWindow is the window of the per-peer limit.
	PeerWindow = time.Hour
	// MaxFailuresPerSource is the number of wrong codes one source may send
	// per SourceWindow.
	MaxFailuresPerSource = 10
	// SourceWindow is the window of the per-source limit.
	SourceWindow = time.Minute
)

// maxTracked bounds the number of peers and of sources that the default
// limiter remembers, so that wrong codes from many made-up peers cannot grow
// its memory without end.
const maxTracked = 10000

type memLimiter struct {
	mu        sync.Mutex
	now       func() time.Time
	peers     map[peer.ID][]time.Time
	sources   map[string][]time.Time
	lastSweep time.Time
}

// NewLimiter returns an in-memory Limiter: 5 wrong codes per peer per hour
// and 10 per source per minute. now may be nil.
func NewLimiter(now func() time.Time) Limiter {
	if now == nil {
		now = time.Now
	}
	return &memLimiter{now: now, peers: map[peer.ID][]time.Time{}, sources: map[string][]time.Time{}}
}

// prune drops the stamps older than the window and returns the rest.
func prune(stamps []time.Time, now time.Time, window time.Duration) []time.Time {
	i := 0
	for i < len(stamps) && now.Sub(stamps[i]) >= window {
		i++
	}
	return stamps[i:]
}

// sweep forgets the peers and sources whose stamps all expired. It runs at
// most once per SourceWindow, and at once when a map reaches maxTracked, in
// which case it also drops arbitrary entries until the map has room.
func (l *memLimiter) sweep(now time.Time) {
	full := len(l.peers) >= maxTracked || len(l.sources) >= maxTracked
	if !full && now.Sub(l.lastSweep) < SourceWindow {
		return
	}
	l.lastSweep = now
	for p, st := range l.peers {
		if len(prune(st, now, PeerWindow)) == 0 {
			delete(l.peers, p)
		}
	}
	for src, st := range l.sources {
		if len(prune(st, now, SourceWindow)) == 0 {
			delete(l.sources, src)
		}
	}
	for p := range l.peers {
		if len(l.peers) < maxTracked {
			break
		}
		delete(l.peers, p)
	}
	for src := range l.sources {
		if len(l.sources) < maxTracked {
			break
		}
		delete(l.sources, src)
	}
}

// unrecord removes one stamp equal to t.
func unrecord(stamps []time.Time, t time.Time) []time.Time {
	for i := len(stamps) - 1; i >= 0; i-- {
		if stamps[i].Equal(t) {
			return append(stamps[:i:i], stamps[i+1:]...)
		}
	}
	return stamps
}

func (l *memLimiter) Attempt(p peer.ID, source string) (bool, func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	l.peers[p] = prune(l.peers[p], now, PeerWindow)
	if len(l.peers[p]) >= MaxFailuresPerPeer {
		return false, func() {}
	}
	if source != "" {
		l.sources[source] = prune(l.sources[source], now, SourceWindow)
		if len(l.sources[source]) >= MaxFailuresPerSource {
			return false, func() {}
		}
	}
	l.peers[p] = append(l.peers[p], now)
	if source != "" {
		l.sources[source] = append(l.sources[source], now)
	}
	var once sync.Once
	return true, func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if st, ok := l.peers[p]; ok {
				l.peers[p] = unrecord(st, now)
			}
			if st, ok := l.sources[source]; ok && source != "" {
				l.sources[source] = unrecord(st, now)
			}
		})
	}
}
