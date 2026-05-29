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
})
