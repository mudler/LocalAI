package e2e_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openai/openai-go/v3"
)

// Router e2e: drives /v1/chat/completions through the RouteModel middleware
// against a configured score classifier (mock-classifier from the suite
// fixtures) and two candidates. The mock-backend's Score handler ranks
// candidates by looking for a `ROUTE_HINT=<label>` marker in the prompt and
// boosting the candidate whose label matches; without a hint, all candidates
// score equally and the router falls back. The ECHO_SERVED_MODEL trigger
// makes the chosen candidate echo its loaded model file path so the test can
// verify routing decisively rather than infer it from content shape.
var _ = Describe("Router E2E", Label("Router"), func() {
	chat := func(message string) (*openai.ChatCompletion, error) {
		return client.Chat.Completions.New(
			context.TODO(),
			openai.ChatCompletionNewParams{
				Model: "smart-router",
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage(message),
				},
			},
		)
	}

	It("routes a casual probe to the casual-chat candidate", func() {
		resp, err := chat("ROUTE_HINT=casual-chat ECHO_SERVED_MODEL")
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Choices).To(HaveLen(1))
		Expect(resp.Choices[0].Message.Content).To(ContainSubstring("SERVED_MODEL=mock-cand-casual.bin"),
			"casual hint should have routed to mock-cand-casual; got %q", resp.Choices[0].Message.Content)
	})

	It("routes a code probe to the code-generation candidate", func() {
		resp, err := chat("ROUTE_HINT=code-generation ECHO_SERVED_MODEL")
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Choices).To(HaveLen(1))
		Expect(resp.Choices[0].Message.Content).To(ContainSubstring("SERVED_MODEL=mock-cand-code.bin"),
			"code hint should have routed to mock-cand-code; got %q", resp.Choices[0].Message.Content)
	})

	It("falls back when no policy label matches the probe", func() {
		// No ROUTE_HINT marker — the mock Score handler gives every candidate
		// the same base log-prob, softmax goes uniform, no label clears
		// activation_threshold=0.40, so the router falls back to
		// mock-cand-casual.
		resp, err := chat("ECHO_SERVED_MODEL hello world")
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Choices).To(HaveLen(1))
		Expect(resp.Choices[0].Message.Content).To(ContainSubstring("SERVED_MODEL=mock-cand-casual.bin"),
			"unhinted probe should have fallen back; got %q", resp.Choices[0].Message.Content)
	})

	It("routes correctly over a long conversation (exercises fitMessages)", func() {
		// Build a conversation long enough that the score classifier's
		// probeTokenBudget kicks in and fitMessages has to trim. mock-backend's
		// TokenizeString returns ~1 token per 4 prompt characters, and the
		// classifier ContextSize is 4096, so >40k chars guarantees the trim
		// path. The ROUTE_HINT marker is placed ONLY in the newest message —
		// if fitMessages dropped it during trim, no candidate would win and we
		// would route to the fallback (mock-cand-casual) instead of the code
		// candidate.
		filler := strings.Repeat("background context, lorem ipsum dolor sit amet. ", 200) // ~10k chars × 5 turns
		msgs := make([]openai.ChatCompletionMessageParamUnion, 0, 6)
		for range 5 {
			msgs = append(msgs, openai.UserMessage(filler))
		}
		msgs = append(msgs, openai.UserMessage("ROUTE_HINT=code-generation ECHO_SERVED_MODEL"))

		resp, err := client.Chat.Completions.New(
			context.TODO(),
			openai.ChatCompletionNewParams{Model: "smart-router", Messages: msgs},
		)
		Expect(err).ToNot(HaveOccurred(), "router must survive a long conversation without erroring")
		Expect(resp.Choices).To(HaveLen(1))
		// The newest turn carries the routing intent ("code"); fitMessages must
		// keep it intact even after dropping older fillers, so the code
		// candidate still wins.
		Expect(resp.Choices[0].Message.Content).To(ContainSubstring("SERVED_MODEL=mock-cand-code.bin"),
			"long-conversation routing must still resolve to the code candidate; got %q",
			resp.Choices[0].Message.Content)
	})
})
