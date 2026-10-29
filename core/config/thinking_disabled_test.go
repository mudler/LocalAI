package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

// ReasoningDisabled decides whether the streaming reasoning extractor may
// prefill a synthetic thinking-start token. It has to account for both the
// operator's configuration and the per-request enable_thinking lever.
var _ = Describe("ModelConfig.ReasoningDisabled", func() {
	It("is false when nothing disables reasoning", func() {
		Expect((&config.ModelConfig{}).ReasoningDisabled()).To(BeFalse())
	})

	It("is true when the operator disabled reasoning", func() {
		disabled := true
		cfg := &config.ModelConfig{}
		cfg.ReasoningConfig.DisableReasoning = &disabled
		Expect(cfg.ReasoningDisabled()).To(BeTrue())
	})

	It("is false when the operator explicitly enabled reasoning", func() {
		enabled := false
		cfg := &config.ModelConfig{}
		cfg.ReasoningConfig.DisableReasoning = &enabled
		Expect(cfg.ReasoningDisabled()).To(BeFalse())
	})

	It("honors a client enable_thinking=false", func() {
		cfg := &config.ModelConfig{RequestMetadata: map[string]string{"enable_thinking": "false"}}
		Expect(cfg.ReasoningDisabled()).To(BeTrue())
	})

	It("stays enabled for enable_thinking=true", func() {
		cfg := &config.ModelConfig{RequestMetadata: map[string]string{"enable_thinking": "true"}}
		Expect(cfg.ReasoningDisabled()).To(BeFalse())
	})

	It("stays enabled for an unparsable metadata value", func() {
		cfg := &config.ModelConfig{RequestMetadata: map[string]string{"enable_thinking": "sometimes"}}
		Expect(cfg.ReasoningDisabled()).To(BeFalse())
	})

	It("tolerates a nil receiver", func() {
		var cfg *config.ModelConfig
		Expect(cfg.ReasoningDisabled()).To(BeFalse())
	})
})
