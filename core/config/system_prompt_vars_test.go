package config_test

import (
	"time"

	. "github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("system prompt variables", func() {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	now := time.Date(2026, 10, 31, 19, 5, 7, 0, berlin)

	It("expands every date and time placeholder", func() {
		out := ExpandSystemPromptVariables(
			"Today is {{CURRENT_WEEKDAY}}, {{CURRENT_DATE}} {{CURRENT_TIME}} ({{CURRENT_TIMEZONE}}); now {{CURRENT_DATETIME}}.", now)
		Expect(out).To(Equal("Today is Saturday, 2026-10-31 19:05:07 (Europe/Berlin); now 2026-10-31 19:05:07."))
	})

	It("leaves prompts without placeholders and unknown placeholders untouched", func() {
		Expect(ExpandSystemPromptVariables("You are helpful. {{.Input}}", now)).To(Equal("You are helpful. {{.Input}}"))
		Expect(ExpandSystemPromptVariables("{{CURRENT_MOOD}}", now)).To(Equal("{{CURRENT_MOOD}}"))
	})

	It("renders the profile prompt, which replaces the target's prompt", func() {
		cfg := ModelConfig{}
		cfg.SystemPrompt = "Heute ist {{CURRENT_DATE}}."
		Expect(cfg.RenderedSystemPrompt(now)).To(Equal("Heute ist 2026-10-31."))
		Expect(cfg.SystemPrompt).To(Equal("Heute ist {{CURRENT_DATE}}."), "the stored config must stay a template")
	})
})
