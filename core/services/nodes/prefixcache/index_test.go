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
		d := idx.Decide("m", []uint64{1, 2, 3}, []prefixcache.ReplicaKey{rk("A", 0), rk("B", 0)}, t0)
		Expect(d.HasHot).To(BeFalse())
		// cold order present (all weights zero -> deterministic by node id)
		Expect(d.ColdOrder).To(ConsistOf(rk("A", 0), rk("B", 0)))
	})

	It("returns the observed replica as hot match with the right ratio", func() {
		idx := prefixcache.NewIndex(cfg)
		idx.Observe("m", []uint64{1, 2, 3, 4}, rk("A", 0), t0)
		d := idx.Decide("m", []uint64{1, 2, 3, 4, 5}, []prefixcache.ReplicaKey{rk("A", 0), rk("B", 0)}, t0)
		Expect(d.HasHot).To(BeTrue())
		Expect(d.Hot).To(Equal(rk("A", 0)))
		Expect(d.MatchRatio).To(BeNumerically("~", 4.0/5.0, 0.001))
	})

	It("orders cold candidates by ascending cacheWeight", func() {
		idx := prefixcache.NewIndex(cfg)
		idx.Observe("m", []uint64{1}, rk("A", 0), t0)
		idx.Observe("m", []uint64{2}, rk("A", 0), t0) // A weight 2
		idx.Observe("m", []uint64{3}, rk("B", 0), t0) // B weight 1
		d := idx.Decide("m", []uint64{9}, []prefixcache.ReplicaKey{rk("A", 0), rk("B", 0)}, t0)
		Expect(d.HasHot).To(BeFalse())
		Expect(d.ColdOrder).To(Equal([]prefixcache.ReplicaKey{rk("B", 0), rk("A", 0)})) // B lower weight first
	})

	It("drops the hot match when the matched replica is not in the candidate set", func() {
		idx := prefixcache.NewIndex(cfg)
		idx.Observe("m", []uint64{1, 2, 3, 4}, rk("A", 0), t0)
		// A holds the longest match, but A is not a candidate (offline /
		// unloaded). The matched replica must be ignored so cold placement runs
		// and no false forced-disturb fires upstream.
		d := idx.Decide("m", []uint64{1, 2, 3, 4, 5}, []prefixcache.ReplicaKey{rk("B", 0), rk("C", 0)}, t0)
		Expect(d.HasHot).To(BeFalse())
		Expect(d.MatchRatio).To(Equal(0.0))
		Expect(d.ColdOrder).To(ConsistOf(rk("B", 0), rk("C", 0)))
	})

	It("returns a hot match for a query that only shares a prefix with an observed chain", func() {
		// The real-world case: a replica served chain [1,2,3,4]; a new request
		// shares the leading block [1,2,3] but diverges at the tail ([1,2,3,9]).
		// With prefix matching (value recorded at every node) Decide must still
		// route to the warm replica, matching at the depth of the shared prefix.
		idx := prefixcache.NewIndex(cfg)
		idx.Observe("m", []uint64{1, 2, 3, 4}, rk("A", 0), t0)
		d := idx.Decide("m", []uint64{1, 2, 3, 9}, []prefixcache.ReplicaKey{rk("A", 0), rk("B", 0)}, t0)
		Expect(d.HasHot).To(BeTrue())
		Expect(d.Hot).To(Equal(rk("A", 0)))
		Expect(d.MatchRatio).To(BeNumerically("~", 3.0/4.0, 0.001)) // shared [1,2,3] of len-4 query
	})

	It("keeps the hot match when the matched replica is a candidate", func() {
		idx := prefixcache.NewIndex(cfg)
		idx.Observe("m", []uint64{1, 2, 3, 4}, rk("A", 0), t0)
		d := idx.Decide("m", []uint64{1, 2, 3, 4, 5}, []prefixcache.ReplicaKey{rk("A", 0), rk("B", 0)}, t0)
		Expect(d.HasHot).To(BeTrue())
		Expect(d.Hot).To(Equal(rk("A", 0)))
		Expect(d.MatchRatio).To(BeNumerically("~", 4.0/5.0, 0.001))
	})

	It("tracks affinity per replica, not per node", func() {
		// Two replicas on the SAME node, each serving a different chain that share
		// a leading block. The hot match for a query extending chain1 must be the
		// EXACT replica that served chain1, not the other replica on the same node.
		idx := prefixcache.NewIndex(cfg)
		idx.Observe("m", []uint64{1, 2, 3, 4}, rk("A", 0), t0) // replica 0 owns [1,2,3,4]
		idx.Observe("m", []uint64{1, 2, 5, 6}, rk("A", 1), t0) // replica 1 owns [1,2,5,6]
		cands := []prefixcache.ReplicaKey{rk("A", 0), rk("A", 1)}
		d := idx.Decide("m", []uint64{1, 2, 3, 4, 7}, cands, t0)
		Expect(d.HasHot).To(BeTrue())
		Expect(d.Hot).To(Equal(rk("A", 0))) // distinct replicas on one node have distinct affinity
		d2 := idx.Decide("m", []uint64{1, 2, 5, 6, 7}, cands, t0)
		Expect(d2.HasHot).To(BeTrue())
		Expect(d2.Hot).To(Equal(rk("A", 1)))
	})

	It("Invalidate drops one replica while InvalidateNode drops all replicas of a node", func() {
		idx := prefixcache.NewIndex(cfg)
		idx.Observe("m", []uint64{1, 2, 3, 4}, rk("A", 0), t0)
		idx.Observe("m", []uint64{5, 6, 7, 8}, rk("A", 1), t0)
		cands := []prefixcache.ReplicaKey{rk("A", 0), rk("A", 1)}

		// Invalidate replica 0 only: replica 1 survives.
		idx.Invalidate("m", rk("A", 0))
		Expect(idx.Decide("m", []uint64{1, 2, 3, 4}, cands, t0).HasHot).To(BeFalse())
		d1 := idx.Decide("m", []uint64{5, 6, 7, 8}, cands, t0)
		Expect(d1.HasHot).To(BeTrue())
		Expect(d1.Hot).To(Equal(rk("A", 1)))

		// Re-observe both, then InvalidateNode drops BOTH replicas.
		idx.Observe("m", []uint64{1, 2, 3, 4}, rk("A", 0), t0)
		idx.InvalidateNode("m", "A")
		Expect(idx.Decide("m", []uint64{1, 2, 3, 4}, cands, t0).HasHot).To(BeFalse())
		Expect(idx.Decide("m", []uint64{5, 6, 7, 8}, cands, t0).HasHot).To(BeFalse())
	})

	It("forgets a replica on Invalidate", func() {
		idx := prefixcache.NewIndex(cfg)
		idx.Observe("m", []uint64{1, 2}, rk("A", 0), t0)
		idx.Invalidate("m", rk("A", 0))
		d := idx.Decide("m", []uint64{1, 2}, []prefixcache.ReplicaKey{rk("A", 0)}, t0)
		Expect(d.HasHot).To(BeFalse())
	})

	It("does not intern an empty tree when invalidating a model that has none", func() {
		idx := prefixcache.NewIndex(cfg)
		Expect(idx.TreeCountForTest()).To(Equal(0))
		// Round-robin model that never used the prefix cache: invalidating a
		// replica removal must be a no-op and must not retain a tree.
		idx.Invalidate("never-cached", rk("A", 0))
		idx.Invalidate("never-cached", rk("B", 0))
		idx.InvalidateNode("other", "C")
		Expect(idx.TreeCountForTest()).To(Equal(0))
		// And a Decide afterwards still works without a hot match.
		d := idx.Decide("never-cached", []uint64{1}, []prefixcache.ReplicaKey{rk("A", 0)}, t0)
		Expect(d.HasHot).To(BeFalse())
	})

	It("is safe for concurrent Decide/Observe/Invalidate (run with -race)", func() {
		idx := prefixcache.NewIndex(cfg)
		models := []string{"m1", "m2"}
		nodes := []string{"A", "B", "C"}
		cands := []prefixcache.ReplicaKey{rk("A", 0), rk("B", 0), rk("C", 0)}
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
					switch i % 4 {
					case 0:
						idx.Observe(model, chain, prefixcache.ReplicaKey{NodeID: node, Replica: i % 2}, now)
					case 1:
						idx.Decide(model, chain, cands, now)
					case 2:
						idx.Invalidate(model, prefixcache.ReplicaKey{NodeID: node, Replica: i % 2})
					case 3:
						idx.InvalidateNode(model, node)
					}
					now = now.Add(time.Millisecond)
				}
			}(g)
		}
		wg.Wait()
	})
})
