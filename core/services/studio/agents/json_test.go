package agents_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/agents/agentstest"
)

var _ = Describe("the JSON agent store", func() {
	Describe("in memory", func() {
		agentstest.Run(func() agents.Store {
			s, err := agents.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	Describe("on disk", func() {
		agentstest.Run(func() agents.Store {
			s, err := agents.NewJSON(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	It("survives a reopen", func() {
		dir := GinkgoT().TempDir()
		first, err := agents.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		agentstest.Seed(first)
		second, err := agents.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		agentstest.CheckSeed(second)
	})
})
