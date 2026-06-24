package admission

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Limiter", func() {
	It("returns immediate no-op when unlimited", func() {
		l := New()
		for i := 0; i < 5; i++ {
			release, ok := l.Acquire("anything", 0)
			Expect(ok).To(BeTrue(), "max=0 should never reject")
			release()
		}
		Expect(l.InFlight("anything")).To(Equal(0))
		Expect(l.Capacity("anything")).To(Equal(0))
	})

	It("rejects when full", func() {
		// Two concurrent requests at MaxConcurrent=1: second is
		// rejected and the limiter reports the in-flight count.
		l := New()
		r1, ok := l.Acquire("m", 1)
		Expect(ok).To(BeTrue(), "first Acquire should succeed")
		defer r1()

		_, ok = l.Acquire("m", 1)
		Expect(ok).To(BeFalse(), "second Acquire should reject — slot is held")
		Expect(l.InFlight("m")).To(Equal(1))
		Expect(l.Capacity("m")).To(Equal(1))
	})

	It("allows the next Acquire after Release", func() {
		l := New()
		r1, _ := l.Acquire("m", 1)
		r1()
		_, ok := l.Acquire("m", 1)
		Expect(ok).To(BeTrue(), "Acquire after release should succeed")
	})

	It("isolates slots per-model", func() {
		// Slots are per-model; saturating one does not affect another.
		l := New()
		r1, _ := l.Acquire("m1", 1)
		defer r1()
		_, ok := l.Acquire("m2", 1)
		Expect(ok).To(BeTrue(), "m2 should have its own slot")
	})

	It("honours the cap under concurrent Acquires", func() {
		// Hammer Acquire from multiple goroutines; the count of
		// successful acquires must not exceed the cap.
		l := New()
		const cap = 4
		const goroutines = 50
		var wg sync.WaitGroup
		successes := make(chan func(), goroutines)
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if release, ok := l.Acquire("m", cap); ok {
					successes <- release
				}
			}()
		}
		wg.Wait()
		close(successes)

		count := 0
		for r := range successes {
			count++
			r()
		}
		Expect(count).To(Equal(cap))
	})

	// First-Acquire fixes the channel capacity. A subsequent Acquire
	// at a different maxConcurrent does NOT resize — admins editing
	// limits expect a process restart. Pin that behaviour so the
	// surprise isn't accidentally introduced.
	It("fixes the cap at first Acquire", func() {
		l := New()
		r1, _ := l.Acquire("m", 2)
		defer r1()
		// Try to acquire with cap=10 — should still be bounded by 2.
		r2, _ := l.Acquire("m", 10)
		defer r2()
		_, ok := l.Acquire("m", 10)
		Expect(ok).To(BeFalse(), "third Acquire should reject — initial cap of 2 still applies")
	})
})

var _ = Describe("RetryAfter", func() {
	It("defaults to one second", func() {
		Expect(RetryAfter(0)).To(Equal(time.Second))
		Expect(RetryAfter(5)).To(Equal(5 * time.Second))
	})
})
