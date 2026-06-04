package openai

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/reasoning"
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

// spokenReasoningConfig clears DisableReasoning so realtime spoken output always
// strips reasoning, even though disable_thinking sets DisableReasoning=true on the
// LLM config (which the backend reads as enable_thinking=false).
var _ = Describe("spokenReasoningConfig", func() {
	It("clears DisableReasoning so the extractor still strips leaked reasoning", func() {
		disable := true
		out := spokenReasoningConfig(reasoning.Config{DisableReasoning: &disable})
		Expect(out.DisableReasoning).To(BeNil())
	})

	It("preserves the other reasoning settings", func() {
		disable := true
		out := spokenReasoningConfig(reasoning.Config{
			DisableReasoning:    &disable,
			ThinkingStartTokens: []string{"<reason>"},
			TagPairs:            []reasoning.TagPair{{Start: "<reason>", End: "</reason>"}},
		})
		Expect(out.ThinkingStartTokens).To(Equal([]string{"<reason>"}))
		Expect(out.TagPairs).To(HaveLen(1))
		Expect(out.TagPairs[0].Start).To(Equal("<reason>"))
	})
})
