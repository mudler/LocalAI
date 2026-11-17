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
	// Blocked reports whether a redeem attempt from this peer and source must
	// be refused now. source is the network address that the caller saw
	// (for a relayed connection, the relay's view of the dialer); it may be
	// empty, which turns the per-source limit off.
	Blocked(p peer.ID, source string) bool
	// Fail records a wrong code from this peer and source.
	Fail(p peer.ID, source string)
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

type memLimiter struct {
	mu      sync.Mutex
	now     func() time.Time
	peers   map[peer.ID][]time.Time
	sources map[string][]time.Time
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

func (l *memLimiter) Blocked(p peer.ID, source string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.peers[p] = prune(l.peers[p], now, PeerWindow)
	if len(l.peers[p]) == 0 {
		delete(l.peers, p)
	}
	if len(l.peers[p]) >= MaxFailuresPerPeer {
		return true
	}
	if source == "" {
		return false
	}
	l.sources[source] = prune(l.sources[source], now, SourceWindow)
	if len(l.sources[source]) == 0 {
		delete(l.sources, source)
	}
	return len(l.sources[source]) >= MaxFailuresPerSource
}

func (l *memLimiter) Fail(p peer.ID, source string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.peers[p] = append(prune(l.peers[p], now, PeerWindow), now)
	if source != "" {
		l.sources[source] = append(prune(l.sources[source], now, SourceWindow), now)
	}
}
