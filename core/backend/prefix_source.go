package backend

import (
	"strings"

	"github.com/mudler/LocalAI/core/schema"
)

// messagesPrefixSource builds a deterministic, prefix-stable serialization of a
// chat conversation for prefix-cache-aware routing. It is the fallback used when
// the frontend did not render a prompt string: models with
// config.TemplateConfig.UseTokenizerTemplate tokenize the structured messages
// backend-side, so the frontend's rendered prompt is empty and a chain built
// from it would always be empty - silently degrading prefix routing to
// round-robin for the bulk of modern chat models.
//
// Messages are emitted head-first in turn order (role line + content line per
// message), so two conversations sharing a leading system prompt and early turns
// share a leading byte prefix. That is exactly what ExtractChain hashes into a
// shared chain prefix, landing both requests on the same cache-warm replica.
func messagesPrefixSource(messages schema.Messages) string {
	var b strings.Builder
	for _, m := range messages {
		b.WriteString(m.Role)
		b.WriteByte('\n')
		content := m.StringContent
		if content == "" {
			if s, ok := m.Content.(string); ok {
				content = s
			}
		}
		b.WriteString(content)
		b.WriteByte('\n')
	}
	return b.String()
}
