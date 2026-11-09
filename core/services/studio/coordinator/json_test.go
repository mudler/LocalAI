package coordinator_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"time"

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

	It("keeps a helper report across a reopen", func() {
		ctx := GinkgoT().Context()
		dir := GinkgoT().TempDir()
		first, err := coordinator.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		at := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
		Expect(first.AddReport(ctx, coordinator.Report{ChildID: "c1", ParentChat: "p", CoordinatorID: "a1", Principal: "u1", Created: at})).To(Succeed())
		Expect(first.AddReport(ctx, coordinator.Report{ChildID: "c2", ParentChat: "p", CoordinatorID: "a1", Principal: "u1", Created: at})).To(Succeed())
		Expect(first.FinishReport(ctx, "c2", at)).To(Succeed())
		second, err := coordinator.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		due, err := second.DueReports(ctx, at.Add(time.Hour), 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(due).To(HaveLen(1))
		Expect(due[0].ChildID).To(Equal("c1"))
	})
})
