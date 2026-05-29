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
