package agentpool_test

import (
	"net/http"

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
		awaitRunning(svc, "alice", "chatty")
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
				// Current standalone shape: the reply id is the id ChatForUser
				// returned plus "-agent". The UI correlates on message_id
				// (AgentChat.jsx), which the distributed dispatcher sends; a
				// native engine may send either, so flip this deliberately.
				Expect(e.Data).To(HaveKeyWithValue("id", msgID+"-agent"))
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
		_, err := svc.ChatForUser("alice", "chatty", "ping")
		Expect(err).ToNot(HaveOccurred())

		Eventually(llm.Requests, "30s", "100ms").ShouldNot(BeEmpty())
		req := llm.Requests()[0]
		Expect(req.Model).To(Equal("fake-model"))
		// Observed shape: content is a plain string, not a parts array. A
		// switch to parts would change what an OpenAI-compatible backend sees.
		Expect(req.Messages).To(ContainElement(And(
			HaveKeyWithValue("role", "user"),
			HaveKeyWithValue("content", "ping"),
		)))
	})

	// The chat page clears its "processing" state only on an agent
	// json_message or a json_error, so a failed turn must end in json_error
	// followed by completed, never in silence. The fake fails every request;
	// cogito retries the decision 5 times with a linear 1s..5s backoff, so the
	// turn settles after about 15s, hence the 60s budget. The error text is
	// cogito's wrapped chain and is not pinned.
	It("reports a failing LLM as json_error then completed, with no agent reply", func() {
		llm.SetFailure(http.StatusInternalServerError)
		events, stop := collectSSE(svc, "alice", "chatty")
		defer stop()

		_, err := svc.ChatForUser("alice", "chatty", "ping")
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() []sseEvent { return statusEvents(events(), "completed") }, "60s", "100ms").
			ShouldNot(BeEmpty(), "no completed status event")

		errorAt, completedAt := -1, -1
		for i, e := range events() {
			switch {
			case e.Name == "json_error" && errorAt < 0:
				errorAt = i
				Expect(e.Data).To(HaveKeyWithValue("error", And(BeAssignableToTypeOf(""), Not(BeEmpty()))))
			case e.Name == "json_message_status" && e.Data["status"] == "completed" && completedAt < 0:
				completedAt = i
			case e.Name == "json_message" && e.Data["sender"] == "agent":
				Fail("a failed turn must not produce an agent json_message")
			}
		}
		Expect(errorAt).To(BeNumerically(">=", 0), "no json_error event")
		Expect(errorAt).To(BeNumerically("<", completedAt), "json_error must precede completed")
		Expect(llm.Requests()).ToNot(BeEmpty())
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
		awaitRunning(svc, "bob", "chatty")
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
