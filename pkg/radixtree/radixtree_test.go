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
