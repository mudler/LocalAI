package backend

import (
	"github.com/mudler/LocalAI/core/config"

	"github.com/gpustack/gguf-parser-go/util/ptr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("thinking probe gating", func() {
	It("probes tokenizer-template models when any reasoning default is still unset", func() {
		cfg := &config.ModelConfig{
			TemplateConfig: config.TemplateConfig{UseTokenizerTemplate: true},
		}
		Expect(needsThinkingProbe(cfg)).To(BeTrue())

		cfg.ReasoningConfig.DisableReasoning = ptr.To(true)
		Expect(needsThinkingProbe(cfg)).To(BeTrue())

		cfg.ReasoningConfig.DisableReasoningTagPrefill = ptr.To(true)
		Expect(needsThinkingProbe(cfg)).To(BeFalse())
	})

	It("does not probe when tokenizer templates are disabled", func() {
		cfg := &config.ModelConfig{}
		Expect(needsThinkingProbe(cfg)).To(BeFalse())
	})
})
