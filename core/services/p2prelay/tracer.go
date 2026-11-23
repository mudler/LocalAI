package p2prelay

import (
	"sync"
	"sync/atomic"
	"time"

	pbv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/pb"
	"golang.org/x/time/rate"
)

// tracer counts circuits, reservations and bytes for Status. The relay
// cannot see what a circuit carries (the peers run their own encryption), so
// counts are all it keeps.
type tracer struct {
	circuits, circuitsTotal, denied, bytes, reservations atomic.Int64
}

func (t *tracer) RelayStatus(bool) {}

func (t *tracer) ConnectionOpened() {
	t.circuits.Add(1)
	t.circuitsTotal.Add(1)
}

func (t *tracer) ConnectionClosed(time.Duration) { t.circuits.Add(-1) }

func (t *tracer) ConnectionRequestHandled(s pbv2.Status) {
	if s != pbv2.Status_OK {
		t.denied.Add(1)
	}
}

func (t *tracer) ReservationAllowed(renewal bool) {
	if !renewal {
		t.reservations.Add(1)
	}
}

func (t *tracer) ReservationClosed(n int) { t.reservations.Add(int64(-n)) }

func (t *tracer) ReservationRequestHandled(pbv2.Status) {}

func (t *tracer) BytesTransferred(n int) { t.bytes.Add(int64(n)) }

// attempts is a token bucket per key (an IP address or a peer ID), for the
// claim and pass tries.
type attempts struct {
	mu    sync.Mutex
	every time.Duration
	burst int
	m     map[string]*attempt
}

type attempt struct {
	lim  *rate.Limiter
	last time.Time
}

// newAttempts allows n tries per window for each key.
func newAttempts(n int, window time.Duration) *attempts {
	return &attempts{every: window / time.Duration(n), burst: n, m: map[string]*attempt{}}
}

func (a *attempts) allow(key string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	e := a.m[key]
	if e == nil {
		e = &attempt{lim: rate.NewLimiter(rate.Every(a.every), a.burst)}
		a.m[key] = e
	}
	e.last = now
	return e.lim.AllowN(now, 1)
}

// prune forgets keys that have been quiet long enough to refill.
func (a *attempts) prune(now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	idle := a.every * time.Duration(a.burst)
	for k, e := range a.m {
		if now.Sub(e.last) > idle {
			delete(a.m, k)
		}
	}
}
