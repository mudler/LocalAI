package prefixcache_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
)

var _ = Describe("Select (filter-then-score)", func() {
	cfg := prefixcache.DefaultConfig() // abs=2, rel=1.5, minMatch=0.3

	cand := func(id string, inflight int) prefixcache.Candidate {
		return prefixcache.Candidate{NodeID: id, InFlight: inflight}
	}

	It("returns the hot-match node when it is load-eligible and match >= min", func() {
		cands := []prefixcache.Candidate{cand("A", 1), cand("B", 0)}
		got := prefixcache.Select(cands, prefixcache.PrefixDecision{
			HotNodeID: "A", MatchRatio: 0.5,
		}, cfg)
		Expect(got).To(Equal("A")) // A in-flight 1 <= min(0)+2 and <= 0*1.5? see note
	})

	It("rejects the hot match when it violates the absolute load guard", func() {
		cands := []prefixcache.Candidate{cand("A", 5), cand("B", 0)}
		got := prefixcache.Select(cands, prefixcache.PrefixDecision{
			HotNodeID: "A", MatchRatio: 0.9,
		}, cfg)
		Expect(got).To(Equal("B")) // A 5 > min(0)+2, drop to cold placement
	})

	It("ignores a match below min_prefix_match", func() {
		cands := []prefixcache.Candidate{cand("A", 0), cand("B", 0)}
		got := prefixcache.Select(cands, prefixcache.PrefixDecision{
			HotNodeID: "A", MatchRatio: 0.2, // < 0.3
			ColdOrder: []string{"B", "A"}, // B has least cacheWeight
		}, cfg)
		Expect(got).To(Equal("B")) // cold placement: lowest cacheWeight eligible
	})

	It("cold-places to lowest-cacheWeight node within the eligible subset", func() {
		cands := []prefixcache.Candidate{cand("A", 0), cand("B", 0), cand("C", 9)}
		got := prefixcache.Select(cands, prefixcache.PrefixDecision{
			ColdOrder: []string{"C", "B", "A"}, // C lowest weight but not eligible (9>0+2)
		}, cfg)
		Expect(got).To(Equal("B")) // C filtered out by load; B is next in cold order
	})

	It("returns empty when no candidates", func() {
		Expect(prefixcache.Select(nil, prefixcache.PrefixDecision{}, cfg)).To(Equal(""))
	})
})

var _ = Describe("Select round-robin floor invariant", func() {
	It("never pins to a saturated hot node (round-robin floor)", func() {
		cfg := prefixcache.DefaultConfig()
		cands := []prefixcache.Candidate{{NodeID: "hot", InFlight: 50}, {NodeID: "cool", InFlight: 0}}
		got := prefixcache.Select(cands, prefixcache.PrefixDecision{HotNodeID: "hot", MatchRatio: 1.0, ColdOrder: []string{"cool", "hot"}}, cfg)
		Expect(got).To(Equal("cool"))
	})

	It("improves reuse when balanced", func() {
		cfg := prefixcache.DefaultConfig()
		cands := []prefixcache.Candidate{{NodeID: "hot", InFlight: 1}, {NodeID: "cool", InFlight: 0}}
		got := prefixcache.Select(cands, prefixcache.PrefixDecision{HotNodeID: "hot", MatchRatio: 1.0, ColdOrder: []string{"cool", "hot"}}, cfg)
		Expect(got).To(Equal("hot")) // within slack -> reuse
	})
})
