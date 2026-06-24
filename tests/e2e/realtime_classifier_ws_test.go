package e2e_test

import (
	"time"

	"github.com/gorilla/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Classifier mode (LocalAI extension): the pipeline scores each user turn
// against a fixed option list via the Score primitive and emits the winning
// option's canned reply / tool call instead of generating. The mock backend
// scores by ROUTE_HINT=<id> markers in the prompt (see mock-backend Score),
// so these specs steer the outcome deterministically through user text.
var _ = Describe("Realtime classifier mode", Label("Realtime"), func() {
	const model = "realtime-pipeline-classifier"

	openSession := func() *websocket.Conn {
		c := connectWS(model)
		created := readServerEvent(c, 30*time.Second)
		Expect(created["type"]).To(Equal("session.created"))
		sendClientEvent(c, disableVADEvent())
		drainUntil(c, "session.updated", 10*time.Second)
		return c
	}

	sendUserText := func(c *websocket.Conn, text string) {
		sendClientEvent(c, map[string]any{
			"type": "conversation.item.create",
			"item": map[string]any{
				"type": "message",
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": text},
				},
			},
		})
		drainUntil(c, "conversation.item.added", 10*time.Second)
	}

	textResponseCreate := map[string]any{
		"type": "response.create",
		"response": map[string]any{
			"output_modalities": []string{"text"},
		},
	}

	It("echoes the classifier config in session.created", func() {
		c := connectWS(model)
		defer func() { _ = c.Close() }()

		created := readServerEvent(c, 30*time.Second)
		Expect(created["type"]).To(Equal("session.created"))
		session, ok := created["session"].(map[string]any)
		Expect(ok).To(BeTrue())
		classifier, ok := session["localai_classifier"].(map[string]any)
		Expect(ok).To(BeTrue(), "session.created should carry localai_classifier")
		options, ok := classifier["options"].([]any)
		Expect(ok).To(BeTrue())
		Expect(options).To(HaveLen(2))
	})

	It("picks the hinted option and emits its canned reply and tool call", func() {
		c := openSession()
		defer func() { _ = c.Close() }()

		sendUserText(c, "ROUTE_HINT=up please fly higher")
		sendClientEvent(c, textResponseCreate)

		result := drainUntil(c, "localai.classifier.result", 30*time.Second)
		Expect(result["chosen_id"]).To(Equal("up"))
		Expect(result["fallback"]).To(BeNil())
		scores, ok := result["scores"].([]any)
		Expect(ok).To(BeTrue())
		Expect(scores).To(HaveLen(2))
		top, ok := scores[0].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(top["id"]).To(Equal("up"))
		Expect(top["score"]).To(BeNumerically(">", 0.9))

		textDone := drainUntil(c, "response.output_text.done", 30*time.Second)
		Expect(textDone["text"]).To(Equal("Going up."))

		fcDone := drainUntil(c, "response.function_call_arguments.done", 30*time.Second)
		Expect(fcDone["arguments"]).To(MatchJSON(`{"direction":"up"}`))

		done := drainUntil(c, "response.done", 30*time.Second)
		Expect(done).ToNot(BeNil())
	})

	It("applies the fallback reply when no option clears the threshold", func() {
		c := openSession()
		defer func() { _ = c.Close() }()

		sendUserText(c, "mumble mumble nothing matches")
		sendClientEvent(c, textResponseCreate)

		result := drainUntil(c, "localai.classifier.result", 30*time.Second)
		Expect(result["chosen_id"]).To(BeNil())
		Expect(result["fallback"]).To(Equal("reply"))

		textDone := drainUntil(c, "response.output_text.done", 30*time.Second)
		Expect(textDone["text"]).To(Equal("Say again?"))

		done := drainUntil(c, "response.done", 30*time.Second)
		Expect(done).ToNot(BeNil())
	})

	It("honors a client-pushed option list via session.update", func() {
		// The drone demo app replaces the whole option list at runtime;
		// mirror its exact sequence: session.update with
		// localai_classifier, then a turn that hints the NEW option.
		c := openSession()
		defer func() { _ = c.Close() }()

		sendClientEvent(c, map[string]any{
			"type": "session.update",
			"session": map[string]any{
				"type": "realtime",
				"localai_classifier": map[string]any{
					"enabled":   true,
					"threshold": 0.6,
					"fallback":  map[string]any{"mode": "reply", "reply": "Say again?"},
					"options": []map[string]any{
						{
							"id":          "spin",
							"description": "the user asks the drone to spin around",
							"reply":       "Spinning.",
							"tool":        map[string]any{"name": "move_drone", "arguments": map[string]any{"direction": "spin"}},
						},
					},
				},
			},
		})
		updated := drainUntil(c, "session.updated", 10*time.Second)
		session, ok := updated["session"].(map[string]any)
		Expect(ok).To(BeTrue())
		classifier, ok := session["localai_classifier"].(map[string]any)
		Expect(ok).To(BeTrue(), "session.updated should echo the replaced classifier")
		options, ok := classifier["options"].([]any)
		Expect(ok).To(BeTrue())
		Expect(options).To(HaveLen(1))

		sendUserText(c, "ROUTE_HINT=spin do a barrel roll")
		sendClientEvent(c, textResponseCreate)

		result := drainUntil(c, "localai.classifier.result", 30*time.Second)
		Expect(result["chosen_id"]).To(Equal("spin"))

		fcDone := drainUntil(c, "response.function_call_arguments.done", 30*time.Second)
		Expect(fcDone["name"]).To(Equal("move_drone"))
		Expect(fcDone["arguments"]).To(MatchJSON(`{"direction":"spin"}`))
		drainUntil(c, "response.done", 30*time.Second)
	})

	It("silently ignores turns that do not address the assistant by name", func() {
		c := openSession()
		defer func() { _ = c.Close() }()

		sendClientEvent(c, map[string]any{
			"type": "session.update",
			"session": map[string]any{
				"type": "realtime",
				"localai_classifier": map[string]any{
					"enabled":   true,
					"threshold": 0.6,
					"address":   map[string]any{"names": []string{"drone"}, "mode": "ignore"},
					"options": []map[string]any{
						{
							"id":          "up",
							"description": "the user asks the drone to fly higher",
							"reply":       "Going up.",
						},
					},
				},
			},
		})
		drainUntil(c, "session.updated", 10*time.Second)

		// No name mentioned: dropped before scoring, response completes
		// with no output.
		sendUserText(c, "ROUTE_HINT=up please fly higher")
		sendClientEvent(c, textResponseCreate)
		result := drainUntil(c, "localai.classifier.result", 30*time.Second)
		Expect(result["fallback"]).To(Equal("not_addressed"))
		Expect(result["chosen_id"]).To(BeNil())
		scores, ok := result["scores"].([]any)
		Expect(ok).To(BeTrue())
		Expect(scores).To(BeEmpty())
		done := drainUntil(c, "response.done", 30*time.Second)
		resp, ok := done["response"].(map[string]any)
		Expect(ok).To(BeTrue())
		// Empty output marshals as JSON null.
		Expect(resp["output"]).To(SatisfyAny(BeNil(), BeEmpty()))

		// Addressed: scored and acted on as usual.
		sendUserText(c, "drone ROUTE_HINT=up please fly higher")
		sendClientEvent(c, textResponseCreate)
		result = drainUntil(c, "localai.classifier.result", 30*time.Second)
		Expect(result["chosen_id"]).To(Equal("up"))
		textDone := drainUntil(c, "response.output_text.done", 30*time.Second)
		Expect(textDone["text"]).To(Equal("Going up."))
		drainUntil(c, "response.done", 30*time.Second)
	})

	It("runs normal generation when the response disables the classifier", func() {
		c := openSession()
		defer func() { _ = c.Close() }()

		sendUserText(c, "ROUTE_HINT=up please fly higher")
		sendClientEvent(c, map[string]any{
			"type": "response.create",
			"response": map[string]any{
				"output_modalities":  []string{"text"},
				"localai_classifier": map[string]any{"enabled": false},
			},
		})

		// The classifier must not run: the next terminal must arrive with
		// generated (mock-llm) output and no classifier result event.
		deadline := time.Now().Add(60 * time.Second)
		sawClassifier := false
		var text string
		for time.Now().Before(deadline) {
			evt := readServerEvent(c, time.Until(deadline))
			switch evt["type"] {
			case "localai.classifier.result":
				sawClassifier = true
			case "response.output_text.done":
				text, _ = evt["text"].(string)
			case "response.done":
				Expect(sawClassifier).To(BeFalse(), "classifier must not run when disabled per response")
				Expect(text).ToNot(BeEmpty())
				Expect(text).ToNot(Equal("Going up."))
				return
			}
		}
		Fail("timed out waiting for response.done")
	})
})
