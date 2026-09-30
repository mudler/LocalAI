package agentpool_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("standalone agent service contract", func() {
	var (
		llm *fakeLLM
		dir string
	)

	BeforeEach(func() {
		llm = newFakeLLM("hello from the fake model")
		dir = GinkgoT().TempDir()
	})
	AfterEach(func() { llm.Close() })

	It("boots against a fake LLM and lists no agents", func() {
		svc := startStandalone(dir, llm.URL())
		DeferCleanup(svc.Stop)

		Expect(svc.ListAgentsForUser("")).To(BeEmpty())
	})
})
