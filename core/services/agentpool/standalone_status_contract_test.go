package agentpool_test

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/services/agentpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("standalone status and observables contract", func() {
	var (
		llm *fakeLLM
		svc *agentpool.AgentPoolService
	)

	BeforeEach(func() {
		llm = newFakeLLM("done")
		// Cleanups run in reverse, so the pool stops before the fake LLM goes away.
		DeferCleanup(llm.Close)
		svc = startStandalone(GinkgoT().TempDir(), llm.URL())
		DeferCleanup(svc.Stop)
		Expect(svc.CreateAgentForUser("alice", newAgentConfig("observed"))).To(Succeed())
	})

	runOnce := func() {
		events, stop := collectSSE(svc, "alice", "observed")
		defer stop()
		_, err := svc.ChatForUser("alice", "observed", "go")
		Expect(err).ToNot(HaveOccurred())
		Eventually(func() []sseEvent { return statusEvents(events(), "completed") }, "30s", "100ms").
			ShouldNot(BeEmpty(), "no completed status event")
	}

	observableCount := func() int {
		obs, err := svc.GetAgentObservablesForUser("alice", "observed")
		Expect(err).ToNot(HaveOccurred())
		return len(obs)
	}

	It("returns an empty observable list before any run", func() {
		obs, err := svc.GetAgentObservablesForUser("alice", "observed")
		Expect(err).ToNot(HaveOccurred())
		Expect(obs).To(BeEmpty())
	})

	// Discovery: a plain-content reply (no tool call) is enough for LocalAGI
	// to record a "job" observable, so the action fallback was not needed.
	It("records observables after a run, each a JSON object with an id", func() {
		runOnce()
		Eventually(observableCount, "10s", "100ms").Should(BeNumerically(">", 0))

		obs, err := svc.GetAgentObservablesForUser("alice", "observed")
		Expect(err).ToNot(HaveOccurred())
		var first map[string]any
		Expect(json.Unmarshal(obs[0], &first)).To(Succeed())
		Expect(first).To(HaveKey("id"))
	})

	It("clears observables", func() {
		runOnce()
		Eventually(observableCount, "10s", "100ms").Should(BeNumerically(">", 0))

		Expect(svc.ClearAgentObservablesForUser("alice", "observed")).To(Succeed())
		obs, err := svc.GetAgentObservablesForUser("alice", "observed")
		Expect(err).ToNot(HaveOccurred())
		Expect(obs).To(BeEmpty())
	})

	It("reports observables of a missing agent as ErrAgentNotFound", func() {
		_, err := svc.GetAgentObservablesForUser("alice", "ghost")
		Expect(err).To(MatchError(agentpool.ErrAgentNotFound))
		Expect(svc.ClearAgentObservablesForUser("alice", "ghost")).To(MatchError(agentpool.ErrAgentNotFound))
	})

	// LocalAGI only creates a status entry when an action result is recorded,
	// so an agent whose runs never called an action looks the same as a
	// missing one. Pinned as current behavior, not as a desirable contract.
	It("returns a nil status for an agent with no action results, as for a missing one", func() {
		Expect(svc.GetAgentStatusForUser("alice", "observed")).To(BeNil())
		runOnce()
		Consistently(func() any { return svc.GetAgentStatusForUser("alice", "observed") }, "500ms", "100ms").Should(BeNil())
		Expect(svc.GetAgentStatusForUser("alice", "ghost")).To(BeNil())
	})
})
