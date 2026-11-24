package studio

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

var _ = Describe("eventJSON", func() {
	It("carries the model, the node and the prompt time of a usage event", func() {
		raw, err := json.Marshal(eventJSON(session.Event{Kind: session.KindUsage, Usage: &session.UsageEvent{
			Used: 1200, Limit: 8192, Completion: 40, TokensPerSecond: 32.5,
			Model: "qwen3-8b", Node: "worker-2", PromptMillis: 180,
		}}))
		Expect(err).ToNot(HaveOccurred())
		var got struct {
			Usage map[string]any `json:"usage"`
		}
		Expect(json.Unmarshal(raw, &got)).To(Succeed())
		Expect(got.Usage).To(HaveKeyWithValue("model", "qwen3-8b"))
		Expect(got.Usage).To(HaveKeyWithValue("node", "worker-2"))
		Expect(got.Usage).To(HaveKeyWithValue("prompt_ms", BeNumerically("==", 180)))
		Expect(got.Usage).To(HaveKeyWithValue("tokens_per_second", BeNumerically("==", 32.5)))
	})

	It("leaves the new fields out when they are not known", func() {
		raw, err := json.Marshal(eventJSON(session.Event{Kind: session.KindUsage, Usage: &session.UsageEvent{Used: 10, Completion: 2}}))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).ToNot(ContainSubstring(`"node"`))
		Expect(string(raw)).ToNot(ContainSubstring(`"prompt_ms"`))
		Expect(string(raw)).ToNot(ContainSubstring(`"model"`))
	})
})
