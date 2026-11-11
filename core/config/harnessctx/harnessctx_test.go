package harnessctx_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config/harnessctx"
)

var _ = Describe("For", func() {
	It("gives a regular user the lower minimum", func() {
		minimum, recommended := harnessctx.For(false)
		Expect(minimum).To(Equal(16384))
		Expect(recommended).To(Equal(32768))
	})

	It("gives an administrator the larger minimum", func() {
		minimum, recommended := harnessctx.For(true)
		Expect(minimum).To(Equal(24576))
		Expect(recommended).To(Equal(32768))
	})

	It("keeps the minimums below the recommended window", func() {
		Expect(harnessctx.MinContext).To(BeNumerically("<", harnessctx.RecommendedContext))
		Expect(harnessctx.MinContextAdmin).To(BeNumerically("<=", harnessctx.RecommendedContext))
		Expect(harnessctx.MinContext).To(BeNumerically("<=", harnessctx.MinContextAdmin))
	})
})
