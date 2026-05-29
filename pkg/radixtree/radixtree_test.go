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
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour})
		tr.Insert([]uint64{1, 2}, "nodeA", t0)
		tr.Insert([]uint64{1, 2, 3, 4}, "nodeB", t0)

		v, depth, ok := tr.LongestMatch([]uint64{1, 2, 3, 4, 5}, t0)
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("nodeB"))
		Expect(depth).To(Equal(4))

		v, depth, ok = tr.LongestMatch([]uint64{1, 2, 9}, t0)
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("nodeA"))
		Expect(depth).To(Equal(2))
	})

	It("returns ok=false when no prefix is stored", func() {
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour})
		tr.Insert([]uint64{7, 8}, "nodeA", t0)
		_, _, ok := tr.LongestMatch([]uint64{1, 2}, t0)
		Expect(ok).To(BeFalse())
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
		tr.Insert([]uint64{1, 2}, "nodeA", t0)
		Expect(tr.Len()).To(Equal(1))
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
		tr := radixtree.New[string](radixtree.Options{TTL: time.Hour})
		tr.Insert([]uint64{1, 2}, "A", t0)
		tr.Insert([]uint64{1, 2, 3}, "B", t0)
		tr.Remove("A")
		_, _, ok := tr.LongestMatch([]uint64{1, 2}, t0)
		Expect(ok).To(BeFalse()) // A gone; node {1,2} has no value
		v, _, ok := tr.LongestMatch([]uint64{1, 2, 3}, t0)
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal("B")) // B survives
		Expect(tr.Weight("A", t0)).To(BeNumerically("==", 0))
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
		// {1,2} carries A's value-less ancestor at depth 1; {1,2,3} carries B.
		// Evicting B (oldest, deepest) must reclaim {1,2,3} and its now-childless
		// ancestor {1,2} since neither holds a value afterwards. Then a fresh
		// unrelated key keeps Len at the cap, proving no stale internal nodes
		// inflate the count.
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
