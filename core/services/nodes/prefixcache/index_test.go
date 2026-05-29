package prefixcache_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
)

var t0 = time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

var _ = Describe("Index provider", func() {
	cfg := prefixcache.DefaultConfig()

	It("returns no hot match before anything is observed", func() {
		idx := prefixcache.NewIndex(cfg)
		d := idx.Decide("m", []uint64{1, 2, 3}, []string{"A", "B"}, t0)
		Expect(d.HotNodeID).To(Equal(""))
		// cold order present (all weights zero -> deterministic by node id)
		Expect(d.ColdOrder).To(ConsistOf("A", "B"))
	})

	It("returns the observed node as hot match with the right ratio", func() {
		idx := prefixcache.NewIndex(cfg)
		idx.Observe("m", []uint64{1, 2, 3, 4}, "A", t0)
		d := idx.Decide("m", []uint64{1, 2, 3, 4, 5}, []string{"A", "B"}, t0)
		Expect(d.HotNodeID).To(Equal("A"))
		Expect(d.MatchRatio).To(BeNumerically("~", 4.0/5.0, 0.001))
	})

	It("orders cold candidates by ascending cacheWeight", func() {
		idx := prefixcache.NewIndex(cfg)
		idx.Observe("m", []uint64{1}, "A", t0)
		idx.Observe("m", []uint64{2}, "A", t0) // A weight 2
		idx.Observe("m", []uint64{3}, "B", t0) // B weight 1
		d := idx.Decide("m", []uint64{9}, []string{"A", "B"}, t0)
		Expect(d.HotNodeID).To(Equal(""))
		Expect(d.ColdOrder).To(Equal([]string{"B", "A"})) // B lower weight first
	})

	It("forgets a node on Invalidate", func() {
		idx := prefixcache.NewIndex(cfg)
		idx.Observe("m", []uint64{1, 2}, "A", t0)
		idx.Invalidate("m", "A")
		d := idx.Decide("m", []uint64{1, 2}, []string{"A"}, t0)
		Expect(d.HotNodeID).To(Equal(""))
	})
})
