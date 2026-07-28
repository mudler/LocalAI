package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

// ResolveChatTemplateKwargs layers the model config map (base) under the coerced
// backend metadata (server reasoning levers + client request overrides).
var _ = Describe("ModelConfig.ResolveChatTemplateKwargs", func() {
	It("returns nil when nothing is set", func() {
		c := &config.ModelConfig{}
		Expect(c.ResolveChatTemplateKwargs(nil)).To(BeNil())
	})

	It("returns the config map when no metadata is present", func() {
		c := &config.ModelConfig{ChatTemplateKwargs: map[string]any{"preserve_thinking": true}}
		Expect(c.ResolveChatTemplateKwargs(nil)).To(HaveKeyWithValue("preserve_thinking", true))
	})

	It("lets metadata override the config map", func() {
		c := &config.ModelConfig{ChatTemplateKwargs: map[string]any{"enable_thinking": true}}
		got := c.ResolveChatTemplateKwargs(map[string]string{"enable_thinking": "false"})
		Expect(got).To(HaveKeyWithValue("enable_thinking", false))
	})

	It("coerces true/false to bool and leaves other strings as-is", func() {
		c := &config.ModelConfig{}
		got := c.ResolveChatTemplateKwargs(map[string]string{
			"enable_thinking":  "true",
			"reasoning_effort": "high",
		})
		Expect(got).To(HaveKeyWithValue("enable_thinking", true))
		Expect(got).To(HaveKeyWithValue("reasoning_effort", "high"))
	})

	It("skips the reserved chat_template_kwargs metadata key but keeps siblings", func() {
		c := &config.ModelConfig{}
		got := c.ResolveChatTemplateKwargs(map[string]string{
			"chat_template_kwargs": "{\"x\":1}",
			"preserve_thinking":    "true",
		})
		Expect(got).ToNot(HaveKey("chat_template_kwargs"))
		Expect(got).To(HaveKeyWithValue("preserve_thinking", true))
	})
})
