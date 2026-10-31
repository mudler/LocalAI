package openai

import (
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("withConfiguredSystemPrompt", func() {
	now := time.Date(2026, 10, 31, 19, 5, 0, 0, time.UTC)
	user := []schema.Message{{Role: "user", Content: "Welcher Tag ist heute?", StringContent: "Welcher Tag ist heute?"}}
	cfg := func(prompt string) *config.ModelConfig {
		c := &config.ModelConfig{}
		c.TemplateConfig.UseTokenizerTemplate = true
		c.SystemPrompt = prompt
		return c
	}

	It("sends today's date to the model instead of the placeholder", func() {
		out := withConfiguredSystemPrompt(user, cfg("Heute ist {{CURRENT_WEEKDAY}}, {{CURRENT_DATE}}."), now)
		Expect(out).To(HaveLen(2))
		Expect(out[0].Role).To(Equal("system"))
		Expect(out[0].StringContent).To(Equal("Heute ist Saturday, 2026-10-31."))
		Expect(out[0].Content).To(Equal("Heute ist Saturday, 2026-10-31."))
	})

	It("keeps a request's own system turn and adds nothing", func() {
		own := append([]schema.Message{{Role: "system", Content: "Eigene Anweisung", StringContent: "Eigene Anweisung"}}, user...)
		Expect(withConfiguredSystemPrompt(own, cfg("Heute ist {{CURRENT_DATE}}."), now)).To(Equal(own))
	})

	It("leaves Go-template models to the template path", func() {
		c := cfg("Heute ist {{CURRENT_DATE}}.")
		c.TemplateConfig.UseTokenizerTemplate = false
		Expect(withConfiguredSystemPrompt(user, c, now)).To(Equal(user))
	})
})
