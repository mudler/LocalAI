package openai

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

// applyPipelineReasoning lets a realtime pipeline set the reasoning effort for
// its LLM (forwarded to the backend as reasoning_effort) without editing the LLM
// model config. The pipeline value overrides the LLM's own reasoning_effort.
var _ = Describe("applyPipelineReasoning", func() {
	It("applies the pipeline reasoning_effort to the LLM config", func() {
		llm := &config.ModelConfig{}
		applyPipelineReasoning(llm, config.Pipeline{ReasoningEffort: "none"})
		Expect(llm.ReasoningEffort).To(Equal("none"))
		Expect(llm.ReasoningConfig.DisableReasoning).ToNot(BeNil())
		Expect(*llm.ReasoningConfig.DisableReasoning).To(BeTrue())
	})

	It("falls back to the LLM's own reasoning_effort when the pipeline is unset", func() {
		llm := &config.ModelConfig{ReasoningEffort: "high"}
		applyPipelineReasoning(llm, config.Pipeline{})
		Expect(llm.ReasoningEffort).To(Equal("high"))
		Expect(llm.ReasoningConfig.DisableReasoning).ToNot(BeNil())
		Expect(*llm.ReasoningConfig.DisableReasoning).To(BeFalse())
	})

	It("is nil-safe", func() {
		applyPipelineReasoning(nil, config.Pipeline{ReasoningEffort: "low"})
	})
})
