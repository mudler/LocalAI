package prefixcache_test

import (
	"time"

	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Pressure counter", func() {
	t0 := time.Unix(1700000000, 0)

	It("counts events within the window and forgets older ones", func() {
		p := prefixcache.NewPressure(time.Minute)
		p.Record("m", t0)
		p.Record("m", t0.Add(30*time.Second))
		Expect(p.Count("m", t0.Add(40*time.Second))).To(Equal(2))
		Expect(p.Count("m", t0.Add(90*time.Second))).To(Equal(1)) // first expired
	})

	It("tracks pressure per model independently", func() {
		p := prefixcache.NewPressure(time.Minute)
		p.Record("a", t0)
		p.Record("a", t0.Add(10*time.Second))
		p.Record("b", t0.Add(20*time.Second))
		Expect(p.Count("a", t0.Add(30*time.Second))).To(Equal(2))
		Expect(p.Count("b", t0.Add(30*time.Second))).To(Equal(1))
		Expect(p.Count("c", t0.Add(30*time.Second))).To(Equal(0))
	})

	It("returns zero for a model that was never recorded", func() {
		p := prefixcache.NewPressure(time.Minute)
		Expect(p.Count("never", t0)).To(Equal(0))
	})

	It("includes the boundary timestamp at exactly now-window", func() {
		p := prefixcache.NewPressure(time.Minute)
		p.Record("m", t0)
		// now-window == t0 exactly, so the entry is still within [now-window, now].
		Expect(p.Count("m", t0.Add(time.Minute))).To(Equal(1))
		// one nanosecond past the window drops it.
		Expect(p.Count("m", t0.Add(time.Minute+1))).To(Equal(0))
	})

	It("bounds the backing slice in Record without any Count calls", func() {
		p := prefixcache.NewPressure(time.Minute)
		// Record many timestamps, advancing now well past the window between
		// each, and never call Count. Each Record must prune the entries that
		// have fallen out of [now-window, now] so the slice cannot accumulate.
		var last time.Time
		for i := range 1000 {
			last = t0.Add(time.Duration(i) * 10 * time.Second)
			p.Record("m", last)
		}
		// With a 1m window and 10s spacing, at most ~7 records (the boundary is
		// inclusive) can be within [last-window, last]. The slice must stay that
		// bounded, never growing toward 1000.
		Expect(p.LenForTest("m")).To(BeNumerically("<=", 7))
		// And the in-window count must reflect only those bounded entries.
		Expect(p.Count("m", last)).To(Equal(p.LenForTest("m")))
	})

	It("clears all recorded events on Reset", func() {
		p := prefixcache.NewPressure(time.Minute)
		p.Record("m", t0)
		p.Record("m", t0.Add(10*time.Second))
		p.Record("m", t0.Add(20*time.Second))
		Expect(p.Count("m", t0.Add(30*time.Second))).To(BeNumerically(">", 0))

		p.Reset("m")

		// After Reset the model has no in-window events even though the
		// timestamps would otherwise still be within [now-window, now].
		Expect(p.Count("m", t0.Add(30*time.Second))).To(Equal(0))
		Expect(p.LenForTest("m")).To(Equal(0))
	})

	It("Reset only clears the named model", func() {
		p := prefixcache.NewPressure(time.Minute)
		p.Record("a", t0)
		p.Record("b", t0)
		p.Reset("a")
		Expect(p.Count("a", t0.Add(time.Second))).To(Equal(0))
		Expect(p.Count("b", t0.Add(time.Second))).To(Equal(1))
	})

	It("does not accumulate repeated out-of-window Records", func() {
		p := prefixcache.NewPressure(time.Minute)
		// Each record is more than a window apart, so every Record prunes the
		// previous one. The slice should never hold more than a single entry.
		for i := range 100 {
			p.Record("m", t0.Add(time.Duration(i)*2*time.Minute))
		}
		Expect(p.LenForTest("m")).To(Equal(1))
		Expect(p.Count("m", t0.Add(198*time.Minute))).To(Equal(1))
	})
})
