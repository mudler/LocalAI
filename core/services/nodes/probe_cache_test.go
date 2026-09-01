package nodes

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/sync/singleflight"
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
		//
		// The FIRST version of this spec raced eight goroutines at the cache
		// and hoped they coalesced. Nothing made them: a goroutine that arrived
		// after the leader's flight finished started its own, re-entered the
		// probe and double-closed a channel, so the spec panicked about one run
		// in three. Its comment claimed the probe blocked until every goroutine
		// was inside flight.Do, which was the design intended rather than the
		// one written, and that gap was exactly the panic.
		//
		// This version does not hope. singleflight.DoChan registers its channel
		// on the in-flight call under the group's own mutex and returns WITHOUT
		// running its function (x/sync@v0.22.0 singleflight.go:127-132), so
		// calling it while the leader is provably parked inside the probe joins
		// that exact flight, with no window and no scheduler dependency. The
		// group is reachable because this spec lives in the package.
		c := newProbeCache(time.Minute)
		unreached := errors.New("no route to the worker")

		// Buffered, and sent on rather than closed: a probe that somehow ran
		// twice must fail an assertion, not panic and take the suite with it.
		entered := make(chan struct{}, 4)
		release := make(chan struct{})
		var calls int32
		probe := func() (bool, error) {
			atomic.AddInt32(&calls, 1)
			entered <- struct{}{}
			<-release
			return false, unreached
		}

		type leaderResult struct {
			alive     bool
			unreached error
		}
		leader := make(chan leaderResult, 1)
		go func() {
			defer GinkgoRecover()
			alive, reason := c.DoOrCachedResult("k", probe)
			leader <- leaderResult{alive: alive, unreached: reason}
		}()

		// The leader is now inside the probe, so the group holds an entry for
		// "k" and will hold it until the probe returns.
		Eventually(entered, "10s").Should(Receive())

		// Deterministically coalesced. This function must never run; if the
		// join failed it would, and the assertion below on the probe count
		// would catch it too.
		joined := c.flight.DoChan("k", func() (any, error) {
			Fail("DoChan started its own flight, so nothing was coalesced")
			return false, nil
		})

		close(release)

		var got leaderResult
		Eventually(leader, "10s").Should(Receive(&got))
		Expect(got.alive).To(BeFalse())
		Expect(got.unreached).To(MatchError(unreached), "the caller that RAN the probe must get the reason")

		var shared singleflight.Result
		Eventually(joined, "10s").Should(Receive(&shared))
		Expect(shared.Shared).To(BeTrue(), "this caller did not actually join the leader's flight")
		Expect(shared.Val).To(Equal(false))
		Expect(shared.Err).To(MatchError(unreached),
			"a joiner got the answer without the reason, which is how a joiner reaps what the leader would not")

		Expect(atomic.LoadInt32(&calls)).To(Equal(int32(1)),
			"the probe must have run exactly once")
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
