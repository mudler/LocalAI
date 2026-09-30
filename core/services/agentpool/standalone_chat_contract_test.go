package agentpool_test

import (
	"github.com/mudler/LocalAI/core/services/agentpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("standalone chat contract", func() {
	var (
		llm *fakeLLM
		svc *agentpool.AgentPoolService
	)

	BeforeEach(func() {
		llm = newFakeLLM("pong")
		// Ginkgo runs cleanups LIFO: registering Close first stops the pool
		// before its LLM goes away.
		DeferCleanup(llm.Close)
		svc = startStandalone(GinkgoT().TempDir(), llm.URL())
		DeferCleanup(svc.Stop)
		Expect(svc.CreateAgentForUser("alice", newAgentConfig("chatty"))).To(Succeed())
	})

	It("streams the user message, processing, agent reply and completed status over SSE", func() {
		events, stop := collectSSE(svc, "alice", "chatty")
		defer stop()

		msgID, err := svc.ChatForUser("alice", "chatty", "ping")
		Expect(err).ToNot(HaveOccurred())
		Expect(msgID).ToNot(BeEmpty())

		Eventually(func() []sseEvent { return statusEvents(events(), "completed") }, "30s", "100ms").
			ShouldNot(BeEmpty(), "no completed status event")

		var user, agent, processing bool
		for _, e := range events() {
			switch {
			case e.Name == "json_message" && e.Data["sender"] == "user":
				Expect(e.Data["content"]).To(Equal("ping"))
				user = true
			case e.Name == "json_message" && e.Data["sender"] == "agent":
				Expect(e.Data["content"]).To(ContainSubstring("pong"))
				Expect(e.Data).To(HaveKey("id"))
				agent = true
			case e.Name == "json_message_status" && e.Data["status"] == "processing":
				processing = true
			}
		}
		Expect(user).To(BeTrue(), "user json_message")
		Expect(processing).To(BeTrue(), "processing status")
		Expect(agent).To(BeTrue(), "agent json_message")
	})

	It("sends the user's message to the LLM under the configured model", func() {
		_, stop := collectSSE(svc, "alice", "chatty")
		defer stop()
		_, err := svc.ChatForUser("alice", "chatty", "ping")
		Expect(err).ToNot(HaveOccurred())

		Eventually(llm.Requests, "30s", "100ms").ShouldNot(BeEmpty())
		req := llm.Requests()[0]
		Expect(req.Model).To(Equal("fake-model"))
		Expect(req.Messages).ToNot(BeEmpty())
	})

	It("reports chat with an unknown agent as ErrAgentNotFound", func() {
		_, err := svc.ChatForUser("alice", "ghost", "hi")
		Expect(err).To(MatchError(agentpool.ErrAgentNotFound))
	})

	It("reports chat with a deleted agent as ErrAgentNotFound", func() {
		Expect(svc.DeleteAgentForUser("alice", "chatty")).To(Succeed())
		_, err := svc.ChatForUser("alice", "chatty", "hi")
		Expect(err).To(MatchError(agentpool.ErrAgentNotFound))
	})

	It("does not deliver one user's chat events to another user's agent of the same name", func() {
		Expect(svc.CreateAgentForUser("bob", newAgentConfig("chatty"))).To(Succeed())
		aliceEvents, stopAlice := collectSSE(svc, "alice", "chatty")
		defer stopAlice()
		bobEvents, stopBob := collectSSE(svc, "bob", "chatty")
		defer stopBob()

		_, err := svc.ChatForUser("alice", "chatty", "ping")
		Expect(err).ToNot(HaveOccurred())
		Eventually(func() []sseEvent { return statusEvents(aliceEvents(), "completed") }, "30s", "100ms").ShouldNot(BeEmpty())

		// LocalAGI pushes a "hud" snapshot of each agent's own state every
		// second, so bob's stream is not silent; what must never reach it is
		// anything produced by alice's chat.
		Consistently(func() []string {
			var names []string
			for _, e := range bobEvents() {
				if e.Name != "hud" {
					names = append(names, e.Name)
				}
			}
			return names
		}, "1s", "100ms").Should(BeEmpty())
	})
})

func statusEvents(events []sseEvent, status string) []sseEvent {
	var out []sseEvent
	for _, e := range events {
		if e.Name == "json_message_status" && e.Data["status"] == status {
			out = append(out, e)
		}
	}
	return out
}
