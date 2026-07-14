package piiadapter

import (
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/pii"
)

// Anthropic returns a pii.Adapter for *schema.AnthropicRequest. The
// scan walks every message's text content (string-form or text blocks
// inside the structured `[]any` content), and the apply writes redacted
// text back in place.
//
// The shape mirrors OpenAI() — Anthropic's multimodal blocks
// (`{"type":"image","source":{...}}`, `{"type":"tool_use", ...}`) are
// left untouched; text-block scanning covers the chat-completion path.
//
// System prompts in the Anthropic API live on the request's top-level
// System field, not in Messages — they're skipped here for now (chat
// messages are the high-traffic surface). System-prompt scanning is a
// follow-up if a deployment proves it needs it.
func Anthropic() pii.Adapter {
	return pii.Adapter{
		Scan: func(parsed any) []pii.ScannedText {
			req, ok := parsed.(*schema.AnthropicRequest)
			if !ok || req == nil {
				return nil
			}
			var out []pii.ScannedText
			for i := range req.Messages {
				msg := &req.Messages[i]
				switch ct := msg.Content.(type) {
				case string:
					if ct != "" {
						out = append(out, pii.ScannedText{
							Index: encodeIdx(i, -1),
							Text:  ct,
						})
					}
				case []any:
					for j, block := range ct {
						if blockMap, ok := block.(map[string]any); ok {
							if blockMap["type"] == "text" {
								if text, ok := blockMap["text"].(string); ok && text != "" {
									out = append(out, pii.ScannedText{
										Index: encodeIdx(i, j),
										Text:  text,
									})
								}
							}
						}
					}
				}
			}
			return out
		},
		Apply: func(parsed any, updates []pii.ScannedText) {
			req, ok := parsed.(*schema.AnthropicRequest)
			if !ok || req == nil {
				return
			}
			for _, u := range updates {
				msgIdx, blockIdx := decodeIdx(u.Index)
				if msgIdx < 0 || msgIdx >= len(req.Messages) {
					continue
				}
				msg := &req.Messages[msgIdx]
				if blockIdx < 0 {
					msg.Content = u.Text
					continue
				}
				blocks, ok := msg.Content.([]any)
				if !ok || blockIdx >= len(blocks) {
					continue
				}
				if blockMap, ok := blocks[blockIdx].(map[string]any); ok {
					blockMap["text"] = u.Text
				}
			}
		},
	}
}
