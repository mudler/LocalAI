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

	// runOnce chats once and waits for the completed status. ChatForUser sends
	// that status as soon as Ask returns, before the job finalizers have written
	// the finished observable, so callers that read observables use
	// settledObservable instead.
	runOnce := func() {
		events, stop := collectSSE(svc, "alice", "observed")
		defer stop()
		_, err := svc.ChatForUser("alice", "observed", "go")
		Expect(err).ToNot(HaveOccurred())
		Eventually(func() []sseEvent { return statusEvents(events(), "completed") }, "30s", "100ms").
			ShouldNot(BeEmpty(), "no completed status event")
	}

	// settledObservable runs once and returns the first observable after the
	// job finalizers are done. Two finalizers each call observer.Update, and
	// Update re-appends an observable whose id is gone, so clearing while one
	// is still pending would bring the observable back. Each Update also sends
	// an observable_update event, so the run is settled once the observable
	// carries a completion and no further observable_update arrives.
	settledObservable := func() map[string]any {
		events, stop := collectSSE(svc, "alice", "observed")
		defer stop()
		_, err := svc.ChatForUser("alice", "observed", "go")
		Expect(err).ToNot(HaveOccurred())

		var first map[string]any
		Eventually(func(g Gomega) {
			obs, err := svc.GetAgentObservablesForUser("alice", "observed")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(obs).ToNot(BeEmpty())
			first = nil
			g.Expect(json.Unmarshal(obs[0], &first)).To(Succeed())
			g.Expect(first).To(HaveKey("completion"))
		}, "30s", "100ms").Should(Succeed())

		updates := func() int {
			n := 0
			for _, e := range events() {
				if e.Name == "observable_update" {
					n++
				}
			}
			return n
		}
		var last int
		Eventually(func() bool {
			n := updates()
			stable := n == last
			last = n
			return stable
		}, "10s", "300ms").Should(BeTrue())
		Consistently(updates, "300ms", "50ms").Should(Equal(last))
		return first
	}

	It("returns an empty observable list before any run", func() {
		obs, err := svc.GetAgentObservablesForUser("alice", "observed")
		Expect(err).ToNot(HaveOccurred())
		Expect(obs).To(BeEmpty())
	})

	// Discovery: a plain-content reply (no tool call) is enough for LocalAGI
	// to record a "job" observable, so the action fallback was not needed.
	// parent_id is not pinned: it is omitempty and a root job has none.
	It("records observables after a run with the fields the agent status UI reads", func() {
		first := settledObservable()
		Expect(first).To(HaveKey("id"))
		Expect(first).To(HaveKey("creation"))
		Expect(first).To(HaveKey("completion"))
	})

	It("clears observables", func() {
		settledObservable()

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
