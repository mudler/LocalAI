package nodes

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("probeCache", func() {
	It("invokes the probe on a cold cache and caches success", func() {
		c := newProbeCache(time.Minute)
		var calls int32
		probe := func() bool {
			atomic.AddInt32(&calls, 1)
			return true
		}

		Expect(c.DoOrCached("k", probe)).To(BeTrue())
		Expect(c.DoOrCached("k", probe)).To(BeTrue())
		Expect(c.DoOrCached("k", probe)).To(BeTrue())

		// Cached: probe ran once.
		Expect(atomic.LoadInt32(&calls)).To(Equal(int32(1)))
	})

	It("re-probes after the TTL expires", func() {
		// 1 ms TTL means the second call is virtually guaranteed to see an
		// expired entry without flaking on scheduler jitter.
		c := newProbeCache(time.Millisecond)
		var calls int32
		probe := func() bool {
			atomic.AddInt32(&calls, 1)
			return true
		}

		Expect(c.DoOrCached("k", probe)).To(BeTrue())
		time.Sleep(5 * time.Millisecond)
		Expect(c.DoOrCached("k", probe)).To(BeTrue())

		Expect(atomic.LoadInt32(&calls)).To(Equal(int32(2)))
	})

	It("does not cache failed probes — next call re-probes", func() {
		c := newProbeCache(time.Minute)
		var calls int32
		var result atomic.Bool
		probe := func() bool {
			atomic.AddInt32(&calls, 1)
			return result.Load()
		}

		// First probe fails — must NOT be cached.
		result.Store(false)
		Expect(c.DoOrCached("k", probe)).To(BeFalse())
		Expect(c.IsFresh("k")).To(BeFalse())

		// Recover: second probe succeeds and is cached.
		result.Store(true)
		Expect(c.DoOrCached("k", probe)).To(BeTrue())
		Expect(c.IsFresh("k")).To(BeTrue())

		// Third call short-circuits on the fresh entry.
		Expect(c.DoOrCached("k", probe)).To(BeTrue())
		Expect(atomic.LoadInt32(&calls)).To(Equal(int32(2)))
	})

	It("coalesces concurrent probes via singleflight", func() {
		// Models the "6 chat completions arrive simultaneously for a
		// not-yet-cached backend" scenario. Without singleflight every caller
		// would dial the backend, defeating the purpose of the cache.
		c := newProbeCache(time.Minute)
		var calls int32
		start := make(chan struct{})
		probe := func() bool {
			atomic.AddInt32(&calls, 1)
			// Stall briefly so the test reliably has all goroutines parked
			// inside flight.Do at the same time.
			time.Sleep(50 * time.Millisecond)
			return true
		}

		const N = 8
		var wg sync.WaitGroup
		results := make([]bool, N)
		for i := 0; i < N; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				results[i] = c.DoOrCached("k", probe)
			}(i)
		}

		close(start)
		wg.Wait()

		Expect(atomic.LoadInt32(&calls)).To(Equal(int32(1)),
			"singleflight must collapse %d concurrent probes into one", N)
		for i, got := range results {
			Expect(got).To(BeTrue(), "goroutine %d saw a different result", i)
		}
	})

	It("hands every coalesced joiner the leader's REASON, not just its answer", func() {
		// The hole this shape exists to close, and the one a closed-over
		// variable reintroduces. The reason is written only in the goroutine
		// that runs the probe; every caller coalesced into that flight would
		// read its own unset variable and see nil. In production that means the
		// leader correctly declines to reap a replica on an unreachable worker
		// while all seven joiners reap it, on the leader's own observation.
		c := newProbeCache(time.Minute)
		unreached := errors.New("no route to the worker")

		// The probe blocks until every goroutine is inside flight.Do, so the
		// joiners are genuinely coalesced rather than serialised. Released by a
		// channel, so nothing here waits on a clock.
		entered := make(chan struct{})
		release := make(chan struct{})
		var calls int32
		probe := func() (bool, error) {
			atomic.AddInt32(&calls, 1)
			close(entered)
			<-release
			return false, unreached
		}

		const N = 8
		start := make(chan struct{})
		var wg sync.WaitGroup
		reasons := make([]error, N)
		alive := make([]bool, N)
		for i := 0; i < N; i++ {
			wg.Add(1)
			go func(i int) {
				defer GinkgoRecover()
				defer wg.Done()
				<-start
				alive[i], reasons[i] = c.DoOrCachedResult("k", probe)
			}(i)
		}
		close(start)

		// Only unblock the leader once at least one goroutine is inside the
		// probe; the rest are then either waiting on the flight or about to be.
		<-entered
		close(release)
		wg.Wait()

		Expect(atomic.LoadInt32(&calls)).To(Equal(int32(1)),
			"singleflight must collapse %d concurrent probes into one", N)
		for i := range reasons {
			Expect(alive[i]).To(BeFalse(), "goroutine %d saw a different answer", i)
			Expect(reasons[i]).To(MatchError(unreached),
				"goroutine %d joined the flight and got the answer without the reason, which is how a joiner reaps what the leader would not", i)
		}
	})

	It("treats different keys independently", func() {
		c := newProbeCache(time.Minute)
		var aCalls, bCalls int32
		Expect(c.DoOrCached("a", func() bool { atomic.AddInt32(&aCalls, 1); return true })).To(BeTrue())
		Expect(c.DoOrCached("b", func() bool { atomic.AddInt32(&bCalls, 1); return true })).To(BeTrue())
		Expect(c.DoOrCached("a", func() bool { atomic.AddInt32(&aCalls, 1); return true })).To(BeTrue())

		Expect(atomic.LoadInt32(&aCalls)).To(Equal(int32(1)))
		Expect(atomic.LoadInt32(&bCalls)).To(Equal(int32(1)))
	})

	It("disables caching when TTL is zero", func() {
		c := newProbeCache(0)
		var calls int32
		probe := func() bool {
			atomic.AddInt32(&calls, 1)
			return true
		}

		Expect(c.DoOrCached("k", probe)).To(BeTrue())
		Expect(c.DoOrCached("k", probe)).To(BeTrue())
		Expect(c.DoOrCached("k", probe)).To(BeTrue())

		Expect(atomic.LoadInt32(&calls)).To(Equal(int32(3)))
	})

	It("Invalidate forces the next call to re-probe", func() {
		c := newProbeCache(time.Hour)
		var calls int32
		probe := func() bool {
			atomic.AddInt32(&calls, 1)
			return true
		}
		Expect(c.DoOrCached("k", probe)).To(BeTrue())
		c.Invalidate("k")
		Expect(c.DoOrCached("k", probe)).To(BeTrue())
		Expect(atomic.LoadInt32(&calls)).To(Equal(int32(2)))
	})
})
