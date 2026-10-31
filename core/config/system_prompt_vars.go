package config

import (
	"strings"
	"time"
)

// A system_prompt is static YAML, so without help a model never learns the
// current date and answers from its training cutoff ("this year" turns into a
// year long gone). The placeholders below are expanded per request. They use
// the names Open WebUI uses, so a prompt moves between the two unchanged.
const systemPromptVarPrefix = "{{CURRENT_"

// ExpandSystemPromptVariables replaces {{CURRENT_DATE}}, {{CURRENT_TIME}},
// {{CURRENT_DATETIME}}, {{CURRENT_WEEKDAY}} and {{CURRENT_TIMEZONE}} with
// values taken from now. Text without placeholders is returned unchanged.
func ExpandSystemPromptVariables(prompt string, now time.Time) string {
	if !strings.Contains(prompt, systemPromptVarPrefix) {
		return prompt
	}
	return strings.NewReplacer(
		"{{CURRENT_DATETIME}}", now.Format("2006-01-02 15:04:05"),
		"{{CURRENT_DATE}}", now.Format("2006-01-02"),
		"{{CURRENT_TIME}}", now.Format("15:04:05"),
		"{{CURRENT_WEEKDAY}}", now.Weekday().String(),
		"{{CURRENT_TIMEZONE}}", now.Location().String(),
	).Replace(prompt)
}

// RenderedSystemPrompt is the configured system prompt with its date and time
// placeholders expanded for the given moment.
func (c ModelConfig) RenderedSystemPrompt(now time.Time) string {
	return ExpandSystemPromptVariables(c.SystemPrompt, now)
}
