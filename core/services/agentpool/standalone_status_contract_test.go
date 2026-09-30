package agentpool_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAGI/core/state"
	"github.com/mudler/LocalAGI/core/types"

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
		awaitRunning(svc, "alice", "observed")
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

	// settleRun chats once with the named agent and returns its observables
	// and SSE events after the job finalizers are done. Two finalizers each
	// call observer.Update, and Update re-appends an observable whose id is
	// gone, so clearing while one is still pending would bring the observable
	// back. Each Update also sends an observable_update event, so the run is
	// settled once the root observable carries a completion, the completed
	// status went out, and no further observable_update arrives.
	settleRun := func(name string) ([]map[string]any, []sseEvent) {
		events, stop := collectSSE(svc, "alice", name)
		defer stop()
		_, err := svc.ChatForUser("alice", name, "go")
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() []sseEvent { return statusEvents(events(), "completed") }, "30s", "100ms").
			ShouldNot(BeEmpty(), "no completed status event")
		Eventually(func(g Gomega) {
			raw, err := svc.GetAgentObservablesForUser("alice", name)
			g.Expect(err).ToNot(HaveOccurred())
			rootDone := false
			for _, r := range raw {
				var o map[string]any
				g.Expect(json.Unmarshal(r, &o)).To(Succeed())
				if _, child := o["parent_id"]; !child {
					_, rootDone = o["completion"]
				}
			}
			g.Expect(rootDone).To(BeTrue())
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

		raw, err := svc.GetAgentObservablesForUser("alice", name)
		Expect(err).ToNot(HaveOccurred())
		obs := make([]map[string]any, len(raw))
		for i, r := range raw {
			Expect(json.Unmarshal(r, &obs[i])).To(Succeed())
		}
		return obs, events()
	}

	// settledObservable returns the first observable of a settled plain run.
	settledObservable := func() map[string]any {
		obs, _ := settleRun("observed")
		Expect(obs).ToNot(BeEmpty())
		return obs[0]
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

	Context("after a run that calls a tool", func() {
		BeforeEach(func() {
			cfg := newAgentConfig("tooled")
			// counter is pure and in-memory, so the action result is
			// deterministic without any external service.
			cfg.Actions = []state.ActionsConfig{{Name: "counter", Config: "{}"}}
			Expect(svc.CreateAgentForUser("alice", cfg)).To(Succeed())
			awaitRunning(svc, "alice", "tooled")
			llm.SetToolCall("counter", `{"name":"contract","adjustment":1}`)
		})

		It("records a status entry the status endpoint renders with action, params and result", func() {
			settleRun("tooled")

			st := svc.GetAgentStatusForUser("alice", "tooled")
			Expect(st).ToNot(BeNil())
			// Select by action name rather than position: the order of
			// status entries is a LocalAGI detail, not part of the contract.
			var h types.ActionState
			Expect(st.Results()).To(ContainElement(Satisfy(func(s types.ActionState) bool {
				return s.ActionCurrentState.Action != nil &&
					s.ActionCurrentState.Action.Definition().Name.String() == "counter"
			}), &h))
			Expect(h.ActionCurrentState.Params).To(HaveKeyWithValue("name", "contract"))
			Expect(h.Result).To(ContainSubstring("Created counter 'contract'"))

			// Same format string as GetAgentStatusEndpoint: this text is what
			// the agent status page shows, so the params must render as JSON
			// through ActionParams.String rather than as a Go map.
			rendered := fmt.Sprintf("Reasoning: %s\nAction taken: %s\nParameters: %+v\nResult: %s",
				h.Reasoning, h.ActionCurrentState.Action.Definition().Name.String(), h.ActionCurrentState.Params, h.Result)
			Expect(rendered).To(ContainSubstring("Action taken: counter\n"))
			Expect(rendered).To(ContainSubstring(`"name":"contract"`))
			Expect(rendered).To(ContainSubstring("Result: Created counter 'contract'"))
		})

		// The observables tree nests the action under the job that ran it.
		// Only the link is pinned: ids, ordering and conversation contents are
		// LocalAGI internals.
		It("records a child action observable linked to the root job by parent_id", func() {
			obs, _ := settleRun("tooled")
			Expect(len(obs)).To(BeNumerically(">", 1))

			var roots, children []map[string]any
			for _, o := range obs {
				if _, ok := o["parent_id"]; ok {
					children = append(children, o)
				} else {
					roots = append(roots, o)
				}
			}
			Expect(roots).To(HaveLen(1))
			Expect(children).ToNot(BeEmpty())
			for _, c := range children {
				Expect(c["parent_id"]).To(BeAssignableToTypeOf(float64(0)))
				Expect(c["parent_id"]).To(Equal(roots[0]["id"]))
			}
			Expect(children).To(ContainElement(And(
				HaveKeyWithValue("name", "action"),
				HaveKeyWithValue("completion", HaveKeyWithValue("action_result", ContainSubstring("Created counter"))),
			)))
		})

		It("still delivers the agent's final reply over SSE", func() {
			_, events := settleRun("tooled")
			var replies []sseEvent
			for _, e := range events {
				if e.Name == "json_message" && e.Data["sender"] == "agent" {
					replies = append(replies, e)
				}
			}
			Expect(replies).ToNot(BeEmpty())
			Expect(replies[0].Data["content"]).To(ContainSubstring("done"))
		})
	})
})
