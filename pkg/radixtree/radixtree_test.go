package radixtree_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/radixtree"
)

var t0 = time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

var _ = Describe("Tree construction", func() {
	It("returns an empty tree that matches nothing", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Minute})
		_, depth, ok := tr.LongestMatch([]uint64{1, 2, 3}, t0)
		Expect(ok).To(BeFalse())
		Expect(depth).To(Equal(0))
	})
})

var _ = Describe("Insert and LongestMatch", func() {
	It("returns the deepest matching prefix value", func() {
		// Non-overlapping chains keep the longest-prefix intent clean: every
		// node on the value's own chain records that value, and no other Insert
		// overwrites a shared prefix node. A query that runs off the end of a
		// chain stops matching at the deepest stored element it reached.
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour})
		tr.Insert([]uint64{1, 2, 3, 4}, "nodeB", t0)
		tr.Insert([]uint64{7, 8}, "nodeA", t0)

		v, depth, ok := tr.LongestMatch([]uint64{1, 2, 3, 4, 5}, t0)
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("nodeB"))
		Expect(depth).To(Equal(4))

		v, depth, ok = tr.LongestMatch([]uint64{7, 8, 9}, t0)
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("nodeA"))
		Expect(depth).To(Equal(2))
	})

	It("lets the last writer own a shared prefix node", func() {
		// When two chains share a leading block, value-at-every-node means the
		// later Insert overwrites the shared prefix node. Inserting nodeA on
		// [1,2] then nodeB on [1,2,3,4] makes nodeB own [1] and [1,2], so a
		// query that diverges within the shared block resolves to nodeB. This
		// is the intended recency heuristic: the most recent chain through that
		// block is the one most likely still warm.
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour})
		tr.Insert([]uint64{1, 2}, "nodeA", t0)
		tr.Insert([]uint64{1, 2, 3, 4}, "nodeB", t0)

		v, depth, ok := tr.LongestMatch([]uint64{1, 2, 3, 4, 5}, t0)
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("nodeB"))
		Expect(depth).To(Equal(4))

		// The shared prefix [1,2] is now owned by nodeB (last writer wins).
		v, depth, ok = tr.LongestMatch([]uint64{1, 2, 9}, t0)
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("nodeB"))
		Expect(depth).To(Equal(2))
	})

	It("returns ok=false when no prefix is stored", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour})
		tr.Insert([]uint64{7, 8}, "nodeA", t0)
		_, _, ok := tr.LongestMatch([]uint64{1, 2}, t0)
		Expect(ok).To(BeFalse())
	})

	It("matches a shared prefix when the query tail diverges", func() {
		// SGLang/vLLM-style prefix matching: a single Insert of a full chain
		// must let any query that shares a leading block match at the depth of
		// the deepest shared element, even though the tails differ. This is the
		// core use case (shared system prompt / multi-turn extension / volatile
		// tail), not exact-repeat.
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour})
		tr.Insert([]uint64{1, 2, 3, 4, 5}, "nodeA", t0)
		v, depth, ok := tr.LongestMatch([]uint64{1, 2, 3, 9, 9}, t0)
		Expect(ok).To(BeTrue())
		Expect(depth).To(Equal(3)) // shared prefix [1,2,3]
		Expect(v).To(Equal("nodeA"))
	})
})

var _ = Describe("TTL expiry", func() {
	It("does not match an entry past its TTL", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Minute})
		tr.Insert([]uint64{1, 2}, "nodeA", t0)
		_, _, ok := tr.LongestMatch([]uint64{1, 2}, t0.Add(2*time.Minute))
		Expect(ok).To(BeFalse())
	})

	It("refreshes lastSeen on re-insert so a live path survives", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Minute})
		tr.Insert([]uint64{1, 2}, "nodeA", t0)
		tr.Insert([]uint64{1, 2}, "nodeA", t0.Add(50*time.Second))
		_, _, ok := tr.LongestMatch([]uint64{1, 2}, t0.Add(90*time.Second))
		Expect(ok).To(BeTrue())
	})

	It("Evict reclaims expired nodes", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Minute})
		// Value-at-every-node: Insert of a 2-element chain records nodeA at both
		// {1} and {1,2}, so Len is 2 (one valued node per distinct prefix).
		tr.Insert([]uint64{1, 2}, "nodeA", t0)
		Expect(tr.Len()).To(Equal(2))
		tr.Evict(t0.Add(2 * time.Minute))
		Expect(tr.Len()).To(Equal(0))
	})
})

var _ = Describe("Weight", func() {
	It("counts live entries for a value with no decay", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour}) // HalfLife=0
		tr.Insert([]uint64{1}, "A", t0)
		tr.Insert([]uint64{1, 2}, "A", t0)
		tr.Insert([]uint64{9}, "B", t0)
		Expect(tr.Weight("A", t0)).To(BeNumerically("==", 2))
		Expect(tr.Weight("B", t0)).To(BeNumerically("==", 1))
		Expect(tr.Weight("C", t0)).To(BeNumerically("==", 0))
	})

	It("decays older entries by half-life", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour, HalfLife: time.Minute})
		tr.Insert([]uint64{1}, "A", t0)
		// one half-life later, the entry weighs 0.5
		Expect(tr.Weight("A", t0.Add(time.Minute))).To(BeNumerically("~", 0.5, 0.001))
	})

	It("ignores expired entries", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Minute})
		tr.Insert([]uint64{1}, "A", t0)
		Expect(tr.Weight("A", t0.Add(2*time.Minute))).To(BeNumerically("==", 0))
	})
})

var _ = Describe("WeightsFor", func() {
	It("matches per-value Weight with no decay", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour}) // HalfLife=0
		tr.Insert([]uint64{1}, "A", t0)
		tr.Insert([]uint64{1, 2}, "A", t0)
		tr.Insert([]uint64{9}, "B", t0)

		got := tr.WeightsFor([]string{"A", "B", "C"}, t0)
		Expect(got).To(HaveLen(3))
		Expect(got["A"]).To(BeNumerically("==", 2))
		Expect(got["B"]).To(BeNumerically("==", 1))
		Expect(got["C"]).To(BeNumerically("==", 0))
	})

	It("matches per-value Weight under decay", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour, HalfLife: time.Minute})
		tr.Insert([]uint64{1}, "A", t0)
		tr.Insert([]uint64{1, 2}, "A", t0.Add(30*time.Second))
		tr.Insert([]uint64{9}, "B", t0)

		now := t0.Add(time.Minute)
		got := tr.WeightsFor([]string{"A", "B", "C"}, now)
		Expect(got["A"]).To(BeNumerically("~", tr.Weight("A", now), 1e-12))
		Expect(got["B"]).To(BeNumerically("~", tr.Weight("B", now), 1e-12))
		Expect(got["C"]).To(BeNumerically("==", 0))
	})

	It("respects TTL expiry and matches Weight at a non-zero age under decay", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Minute, HalfLife: 30 * time.Second})
		tr.Insert([]uint64{1}, "A", t0)                     // will be expired at now
		tr.Insert([]uint64{2}, "A", t0.Add(90*time.Second)) // live, aged 30s at now
		tr.Insert([]uint64{9}, "B", t0)                     // expired at now

		now := t0.Add(2 * time.Minute)
		got := tr.WeightsFor([]string{"A", "B"}, now)
		Expect(got["A"]).To(BeNumerically("~", tr.Weight("A", now), 1e-12))
		Expect(got["A"]).To(BeNumerically("~", 0.5, 0.001)) // single live entry aged one half-life
		Expect(got["B"]).To(BeNumerically("==", 0))
	})

	It("returns an empty map for an empty values slice", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour})
		tr.Insert([]uint64{1}, "A", t0)
		Expect(tr.WeightsFor(nil, t0)).To(BeEmpty())
		Expect(tr.WeightsFor([]string{}, t0)).To(BeEmpty())
	})

	It("maps a value not present in the tree to 0", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour})
		tr.Insert([]uint64{1}, "A", t0)
		got := tr.WeightsFor([]string{"Z"}, t0)
		Expect(got).To(HaveLen(1))
		Expect(got["Z"]).To(BeNumerically("==", 0))
	})
})

var _ = Describe("Remove", func() {
	It("drops every entry anchored to a value and prunes", func() {
		// Non-overlapping chains so Remove("A") and the survival of B are both
		// meaningful: with value-at-every-node, overlapping chains would let the
		// later writer own the shared prefix nodes, so A could own nothing and
		// the test would be vacuous.
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour})
		tr.Insert([]uint64{1, 2}, "A", t0)
		tr.Insert([]uint64{7, 8, 9}, "B", t0)
		tr.Remove("A")
		_, _, ok := tr.LongestMatch([]uint64{1, 2}, t0)
		Expect(ok).To(BeFalse()) // A gone; its branch is fully reclaimed
		v, _, ok := tr.LongestMatch([]uint64{7, 8, 9}, t0)
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("B")) // B survives
		Expect(tr.Weight("A", t0)).To(BeNumerically("==", 0))
	})
})

var _ = Describe("RemoveFunc", func() {
	It("drops every entry matching the predicate, prunes, and keeps the rest", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour})
		tr.Insert([]uint64{1, 2}, "drop-a", t0)
		tr.Insert([]uint64{3, 4}, "drop-b", t0)
		tr.Insert([]uint64{7, 8, 9}, "keep", t0)
		// Drop everything whose value starts with "drop".
		tr.RemoveFunc(func(v string) bool { return len(v) >= 4 && v[:4] == "drop" })
		_, _, ok := tr.LongestMatch([]uint64{1, 2}, t0)
		Expect(ok).To(BeFalse())
		_, _, ok = tr.LongestMatch([]uint64{3, 4}, t0)
		Expect(ok).To(BeFalse())
		v, _, ok := tr.LongestMatch([]uint64{7, 8, 9}, t0)
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("keep"))
		Expect(tr.Len()).To(Equal(3)) // only the 3-node "keep" chain remains
	})

	It("makes Remove a special case of RemoveFunc", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour})
		tr.Insert([]uint64{1, 2}, "A", t0)
		tr.Insert([]uint64{7, 8, 9}, "B", t0)
		tr.RemoveFunc(func(v string) bool { return v == "A" })
		_, _, ok := tr.LongestMatch([]uint64{1, 2}, t0)
		Expect(ok).To(BeFalse())
		v, _, ok := tr.LongestMatch([]uint64{7, 8, 9}, t0)
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("B"))
	})
})

var _ = Describe("TTL boundary", func() {
	It("treats age exactly equal to TTL as still live, and one tick past as expired", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Minute})
		tr.Insert([]uint64{1, 2}, "A", t0)

		// age == TTL: strict greater-than means this is still live.
		_, _, ok := tr.LongestMatch([]uint64{1, 2}, t0.Add(time.Minute))
		Expect(ok).To(BeTrue())

		// one nanosecond past TTL: expired.
		_, _, ok = tr.LongestMatch([]uint64{1, 2}, t0.Add(time.Minute+time.Nanosecond))
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("MaxEntries eviction", func() {
	It("drops the least-recently-seen entry when the cap is exceeded", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour, MaxEntries: 2})
		tr.Insert([]uint64{1}, "A", t0)
		tr.Insert([]uint64{2}, "B", t0.Add(time.Second))
		tr.Insert([]uint64{3}, "C", t0.Add(2*time.Second))

		Expect(tr.Len()).To(Equal(2))

		// A was the least-recently-seen, so it is the one dropped.
		_, _, ok := tr.LongestMatch([]uint64{1}, t0.Add(2*time.Second))
		Expect(ok).To(BeFalse())

		// B and C survive.
		_, _, ok = tr.LongestMatch([]uint64{2}, t0.Add(2*time.Second))
		Expect(ok).To(BeTrue())
		_, _, ok = tr.LongestMatch([]uint64{3}, t0.Add(2*time.Second))
		Expect(ok).To(BeTrue())
	})

	It("prunes value-less ancestors left behind by an eviction", func() {
		// Value-at-every-node: Inserting the deep chain B = [1,2,3] records B at
		// {1}, {1,2}, and {1,2,3} (three valued nodes). With the cap at 2, the
		// least-recently-seen valued nodes are evicted one per subsequent Insert.
		// The two fresh single-element keys (C, D) are newer, so eviction keeps
		// peeling B's nodes off until B's entire branch is reclaimed - none of
		// its internal nodes may linger and inflate Len past the cap.
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour, MaxEntries: 2})
		tr.Insert([]uint64{1, 2, 3}, "B", t0)
		tr.Insert([]uint64{5}, "C", t0.Add(time.Second))
		tr.Insert([]uint64{6}, "D", t0.Add(2*time.Second))

		Expect(tr.Len()).To(Equal(2))
		// B (oldest) evicted; its deep branch reclaimed.
		_, _, ok := tr.LongestMatch([]uint64{1, 2, 3}, t0.Add(2*time.Second))
		Expect(ok).To(BeFalse())
		_, _, ok = tr.LongestMatch([]uint64{1, 2}, t0.Add(2*time.Second))
		Expect(ok).To(BeFalse())
		Expect(tr.Weight("B", t0.Add(2*time.Second))).To(BeNumerically("==", 0))
	})

	It("reclaims structure so the tree never grows past the cap under churn", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour, MaxEntries: 2})
		tr.Insert([]uint64{1}, "A", t0)
		tr.Insert([]uint64{2}, "B", t0.Add(time.Second))
		Expect(tr.Len()).To(Equal(2))

		for i := range 10 {
			tr.Insert([]uint64{uint64(100 + i)}, "X", t0.Add(time.Duration(i+2)*time.Second))
			Expect(tr.Len()).To(Equal(2))
		}
	})
})

var _ = Describe("Empty key", func() {
	It("LongestMatch on an empty key returns ok=false", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour})
		tr.Insert([]uint64{1, 2}, "A", t0)
		_, depth, ok := tr.LongestMatch([]uint64{}, t0)
		Expect(ok).To(BeFalse())
		Expect(depth).To(Equal(0))
	})

	It("Insert with an empty key is a no-op that creates no root value", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour})
		Expect(func() { tr.Insert([]uint64{}, "A", t0) }).NotTo(Panic())
		Expect(tr.Len()).To(Equal(0))
		_, _, ok := tr.LongestMatch([]uint64{}, t0)
		Expect(ok).To(BeFalse())
		Expect(tr.Weight("A", t0)).To(BeNumerically("==", 0))
	})
})

var _ = Describe("Concurrent access", func() {
	It("is race-free under parallel insert/match/weight", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour})
		done := make(chan struct{})
		for g := range 8 {
			go func(g int) {
				defer GinkgoRecover()
				for i := range 1000 {
					tr.Insert([]uint64{uint64(g), uint64(i % 10)}, "n", t0)
					tr.LongestMatch([]uint64{uint64(g), 1}, t0)
					tr.Weight("n", t0)
				}
				done <- struct{}{}
			}(g)
		}
		for range 8 {
			<-done
		}
	})
})
