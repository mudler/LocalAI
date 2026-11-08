package coordinator_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/core/services/studio/coordinator/coordinatortest"
)

var _ = Describe("the JSON coordinator store", func() {
	Describe("in memory", func() {
		coordinatortest.Run(func() coordinator.Store {
			s, err := coordinator.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	Describe("on disk", func() {
		coordinatortest.Run(func() coordinator.Store {
			s, err := coordinator.NewJSON(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	It("survives a reopen", func() {
		dir := GinkgoT().TempDir()
		first, err := coordinator.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		c := coordinatortest.Sample("a1", "u1")
		Expect(first.Put(GinkgoT().Context(), c)).To(Succeed())
		second, err := coordinator.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		got, err := second.Get(GinkgoT().Context(), "a1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(c))
	})
})
