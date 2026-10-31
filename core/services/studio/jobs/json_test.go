package jobs_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/jobs"
	"github.com/mudler/LocalAI/core/services/studio/jobs/jobstest"
)

var _ = Describe("the JSON jobs store", func() {
	Describe("in memory", func() {
		jobstest.Run(func() jobs.Store {
			s, err := jobs.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	Describe("on disk", func() {
		jobstest.Run(func() jobs.Store {
			s, err := jobs.NewJSON(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	It("survives a reopen", func() {
		dir := GinkgoT().TempDir()
		first, err := jobs.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(first.PutTask(context.Background(), jobs.Task{ID: "t", Owner: "u", Name: "N", Model: "m", Prompt: "p", Enabled: true})).To(Succeed())
		second, err := jobs.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		_, err = second.GetTask(context.Background(), "u", "t")
		Expect(err).NotTo(HaveOccurred(), "the task is gone after a reopen")
	})
})
