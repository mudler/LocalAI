package prefixcache_test

import (
	"sync"
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

	It("drops the hot match when the matched node is not in the candidate set", func() {
		idx := prefixcache.NewIndex(cfg)
		idx.Observe("m", []uint64{1, 2, 3, 4}, "A", t0)
		// A holds the longest match, but A is not a candidate (offline /
		// unloaded). The matched node must be ignored so cold placement runs
		// and no false forced-disturb fires upstream.
		d := idx.Decide("m", []uint64{1, 2, 3, 4, 5}, []string{"B", "C"}, t0)
		Expect(d.HotNodeID).To(Equal(""))
		Expect(d.MatchRatio).To(Equal(0.0))
		Expect(d.ColdOrder).To(ConsistOf("B", "C"))
	})

	It("keeps the hot match when the matched node is a candidate", func() {
		idx := prefixcache.NewIndex(cfg)
		idx.Observe("m", []uint64{1, 2, 3, 4}, "A", t0)
		d := idx.Decide("m", []uint64{1, 2, 3, 4, 5}, []string{"A", "B"}, t0)
		Expect(d.HotNodeID).To(Equal("A"))
		Expect(d.MatchRatio).To(BeNumerically("~", 4.0/5.0, 0.001))
	})

	It("forgets a node on Invalidate", func() {
		idx := prefixcache.NewIndex(cfg)
		idx.Observe("m", []uint64{1, 2}, "A", t0)
		idx.Invalidate("m", "A")
		d := idx.Decide("m", []uint64{1, 2}, []string{"A"}, t0)
		Expect(d.HotNodeID).To(Equal(""))
	})

	It("does not intern an empty tree when invalidating a model that has none", func() {
		idx := prefixcache.NewIndex(cfg)
		Expect(idx.TreeCountForTest()).To(Equal(0))
		// Round-robin model that never used the prefix cache: invalidating a
		// replica removal must be a no-op and must not retain a tree.
		idx.Invalidate("never-cached", "A")
		idx.Invalidate("never-cached", "B")
		idx.Invalidate("other", "C")
		Expect(idx.TreeCountForTest()).To(Equal(0))
		// And a Decide afterwards still works without a hot match.
		d := idx.Decide("never-cached", []uint64{1}, []string{"A"}, t0)
		Expect(d.HotNodeID).To(Equal(""))
	})

	It("is safe for concurrent Decide/Observe/Invalidate (run with -race)", func() {
		idx := prefixcache.NewIndex(cfg)
		models := []string{"m1", "m2"}
		nodes := []string{"A", "B", "C"}
		var wg sync.WaitGroup
		for g := range 8 {
			wg.Add(1)
			go func(g int) {
				defer GinkgoRecover()
				defer wg.Done()
				model := models[g%len(models)]
				node := nodes[g%len(nodes)]
				now := t0
				for i := range 200 {
					chain := []uint64{uint64(g), uint64(i % 7), uint64(i)}
					switch i % 3 {
					case 0:
						idx.Observe(model, chain, node, now)
					case 1:
						idx.Decide(model, chain, nodes, now)
					case 2:
						idx.Invalidate(model, node)
					}
					now = now.Add(time.Millisecond)
				}
			}(g)
		}
		wg.Wait()
	})
})
