package nodes

import (
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
