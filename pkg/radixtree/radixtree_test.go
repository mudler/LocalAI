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
