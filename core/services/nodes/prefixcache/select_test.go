package prefixcache_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
)

func rk(node string, replica int) prefixcache.ReplicaKey {
	return prefixcache.ReplicaKey{NodeID: node, Replica: replica}
}

var _ = Describe("Select (filter-then-score)", func() {
	cfg := prefixcache.DefaultConfig() // abs=2, rel=1.5, minMatch=0.3

	cand := func(node string, replica, inflight int) prefixcache.Candidate {
		return prefixcache.Candidate{Key: rk(node, replica), InFlight: inflight}
	}

	It("returns the hot-match replica when it is load-eligible and match >= min", func() {
		cands := []prefixcache.Candidate{cand("A", 0, 1), cand("B", 0, 0)}
		got, ok := prefixcache.Select(cands, prefixcache.PrefixDecision{
			Hot: rk("A", 0), HasHot: true, MatchRatio: 0.5,
		}, cfg)
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(rk("A", 0))) // A in-flight 1 <= min(0)+2 and <= 0*1.5+1
	})

	It("rejects the hot match when it violates the absolute load guard", func() {
		cands := []prefixcache.Candidate{cand("A", 0, 5), cand("B", 0, 0)}
		got, ok := prefixcache.Select(cands, prefixcache.PrefixDecision{
			Hot: rk("A", 0), HasHot: true, MatchRatio: 0.9,
			ColdOrder: []prefixcache.ReplicaKey{rk("B", 0), rk("A", 0)},
		}, cfg)
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(rk("B", 0))) // A 5 > min(0)+2, drop to cold placement
	})

	It("ignores a match below min_prefix_match", func() {
		cands := []prefixcache.Candidate{cand("A", 0, 0), cand("B", 0, 0)}
		got, ok := prefixcache.Select(cands, prefixcache.PrefixDecision{
			Hot: rk("A", 0), HasHot: true, MatchRatio: 0.2, // < 0.3
			ColdOrder: []prefixcache.ReplicaKey{rk("B", 0), rk("A", 0)},
		}, cfg)
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(rk("B", 0))) // cold placement: lowest cacheWeight eligible
	})

	It("cold-places to lowest-cacheWeight replica within the eligible subset", func() {
		cands := []prefixcache.Candidate{cand("A", 0, 0), cand("B", 0, 0), cand("C", 0, 9)}
		got, ok := prefixcache.Select(cands, prefixcache.PrefixDecision{
			ColdOrder: []prefixcache.ReplicaKey{rk("C", 0), rk("B", 0), rk("A", 0)},
		}, cfg)
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(rk("B", 0))) // C filtered out by load; B is next in cold order
	})

	It("returns false when no candidates", func() {
		_, ok := prefixcache.Select(nil, prefixcache.PrefixDecision{}, cfg)
		Expect(ok).To(BeFalse())
	})

	It("falls back to the least-in-flight eligible replica when ColdOrder is empty", func() {
		// Deterministic eligible fallback: ColdOrder does not cover the eligible
		// set, so Select picks the least-in-flight eligible replica, tiebreaking by
		// NodeID then Replica.
		cands := []prefixcache.Candidate{cand("B", 1, 0), cand("B", 0, 0), cand("A", 0, 0)}
		got, ok := prefixcache.Select(cands, prefixcache.PrefixDecision{}, cfg)
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(rk("A", 0))) // all in-flight 0; A < B; within B, replica 0 < 1
	})

	It("returns false when no candidate is eligible", func() {
		// Impossible in practice (min is always eligible) but guards the contract:
		// an empty eligible set yields no selection. Here every candidate is the
		// min, so one is always eligible; instead test the documented zero value.
		cands := []prefixcache.Candidate{cand("A", 0, 0)}
		got, ok := prefixcache.Select(cands, prefixcache.PrefixDecision{}, cfg)
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(rk("A", 0)))
	})
})

var _ = Describe("Select replica granularity", func() {
	cfg := prefixcache.DefaultConfig()

	It("distinguishes two replicas of the same node as separate candidates", func() {
		// Two replicas on NodeA: replica 0 is hot but saturated, replica 1 is cool.
		// The round-robin floor must drop to replica 1, NOT collapse them per node.
		cands := []prefixcache.Candidate{
			{Key: rk("A", 0), InFlight: 50},
			{Key: rk("A", 1), InFlight: 0},
		}
		got, ok := prefixcache.Select(cands, prefixcache.PrefixDecision{
			Hot: rk("A", 0), HasHot: true, MatchRatio: 1.0,
			ColdOrder: []prefixcache.ReplicaKey{rk("A", 1), rk("A", 0)},
		}, cfg)
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(rk("A", 1)))
	})

	It("pins back to the exact hot replica when it is within slack", func() {
		cands := []prefixcache.Candidate{
			{Key: rk("A", 0), InFlight: 1},
			{Key: rk("A", 1), InFlight: 0},
		}
		got, ok := prefixcache.Select(cands, prefixcache.PrefixDecision{
			Hot: rk("A", 0), HasHot: true, MatchRatio: 1.0,
			ColdOrder: []prefixcache.ReplicaKey{rk("A", 1), rk("A", 0)},
		}, cfg)
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(rk("A", 0))) // within slack -> reuse exact replica
	})
})

var _ = Describe("Select round-robin floor invariant", func() {
	It("never pins to a saturated hot replica (round-robin floor)", func() {
		cfg := prefixcache.DefaultConfig()
		cands := []prefixcache.Candidate{{Key: rk("hot", 0), InFlight: 50}, {Key: rk("cool", 0), InFlight: 0}}
		got, ok := prefixcache.Select(cands, prefixcache.PrefixDecision{
			Hot: rk("hot", 0), HasHot: true, MatchRatio: 1.0,
			ColdOrder: []prefixcache.ReplicaKey{rk("cool", 0), rk("hot", 0)},
		}, cfg)
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(rk("cool", 0)))
	})

	It("improves reuse when balanced", func() {
		cfg := prefixcache.DefaultConfig()
		cands := []prefixcache.Candidate{{Key: rk("hot", 0), InFlight: 1}, {Key: rk("cool", 0), InFlight: 0}}
		got, ok := prefixcache.Select(cands, prefixcache.PrefixDecision{
			Hot: rk("hot", 0), HasHot: true, MatchRatio: 1.0,
			ColdOrder: []prefixcache.ReplicaKey{rk("cool", 0), rk("hot", 0)},
		}, cfg)
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(rk("hot", 0))) // within slack -> reuse
	})
})
