package openai

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

// applyPipelineThinking lets a realtime pipeline force the LLM's thinking off
// (enable_thinking=false metadata) without editing the LLM model config.
var _ = Describe("applyPipelineThinking", func() {
	It("disables reasoning on the LLM config when the pipeline disables thinking", func() {
		disable := true
		llm := &config.ModelConfig{}
		applyPipelineThinking(llm, config.Pipeline{DisableThinking: &disable})
		Expect(llm.ReasoningConfig.DisableReasoning).ToNot(BeNil())
		Expect(*llm.ReasoningConfig.DisableReasoning).To(BeTrue())
	})

	It("leaves the LLM config untouched when the pipeline does not set disable_thinking", func() {
		llm := &config.ModelConfig{}
		applyPipelineThinking(llm, config.Pipeline{})
		Expect(llm.ReasoningConfig.DisableReasoning).To(BeNil())
	})
})
