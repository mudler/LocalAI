package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

// ApplyReasoningEffort resolves the effective reasoning effort (request value
// overrides the model config default), stores it on the config so it reaches the
// backend, and maps it onto the enable_thinking toggle.
var _ = Describe("ModelConfig.ApplyReasoningEffort", func() {
	It("uses the request value over the config default", func() {
		c := &config.ModelConfig{ReasoningEffort: "high"}
		c.ApplyReasoningEffort("none")
		Expect(c.ReasoningEffort).To(Equal("none"))
		Expect(c.ReasoningConfig.DisableReasoning).ToNot(BeNil())
		Expect(*c.ReasoningConfig.DisableReasoning).To(BeTrue())
	})

	It("falls back to the config default when the request omits it", func() {
		c := &config.ModelConfig{ReasoningEffort: "none"}
		c.ApplyReasoningEffort("")
		Expect(c.ReasoningEffort).To(Equal("none"))
		Expect(c.ReasoningConfig.DisableReasoning).ToNot(BeNil())
		Expect(*c.ReasoningConfig.DisableReasoning).To(BeTrue())
	})

	It("enables thinking for an explicit effort level", func() {
		c := &config.ModelConfig{}
		c.ApplyReasoningEffort("medium")
		Expect(c.ReasoningEffort).To(Equal("medium"))
		Expect(c.ReasoningConfig.DisableReasoning).ToNot(BeNil())
		Expect(*c.ReasoningConfig.DisableReasoning).To(BeFalse())
	})

	It("enables thinking for the levels above high", func() {
		for _, level := range []string{"xhigh", "max"} {
			c := &config.ModelConfig{}
			c.ApplyReasoningEffort(level)
			Expect(c.ReasoningEffort).To(Equal(level))
			Expect(c.ReasoningConfig.DisableReasoning).ToNot(BeNil())
			Expect(*c.ReasoningConfig.DisableReasoning).To(BeFalse())
		}
	})

	It("does not let a level override an operator's config-level disable", func() {
		for _, level := range []string{"high", "xhigh", "max"} {
			disabled := true
			c := &config.ModelConfig{}
			c.ReasoningConfig.DisableReasoning = &disabled
			c.ApplyReasoningEffort(level)
			Expect(*c.ReasoningConfig.DisableReasoning).To(BeTrue(), level)
		}
	})

	It("is a no-op on the toggle when no effort is set anywhere", func() {
		c := &config.ModelConfig{}
		c.ApplyReasoningEffort("")
		Expect(c.ReasoningEffort).To(Equal(""))
		Expect(c.ReasoningConfig.DisableReasoning).To(BeNil())
	})
})
