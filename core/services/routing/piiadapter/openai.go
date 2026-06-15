// Package piiadapter holds the per-API-shape adapters that translate
// between the routing/pii middleware and concrete request types from
// core/schema. Lives outside core/services/routing/pii so the schema
// package never imports pii (and pii never imports schema), keeping
// the dependency direction clean.
package piiadapter

import (
	"strings"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/pii"
)

// OpenAI returns a pii.Adapter for *schema.OpenAIRequest. It scans
// every chat message's text content (string-form or text blocks of
// the structured `[]any` content), and writes redacted text back.
//
// Multimodal content (image_url, audio_url, video_url) is left alone
// — PII in image bytes is the encoder NER tier's problem, not the
// regex tier's. We do walk text fields embedded inside content
// blocks because those are the most common shape Claude Code and
// similar clients produce.
//
// System / developer / tool messages are scanned as well: an API key
// pasted into a system prompt is just as leak-prone as one in a user
// message.
func OpenAI() pii.Adapter {
	return pii.Adapter{
		Scan: func(parsed any) []pii.ScannedText {
			req, ok := parsed.(*schema.OpenAIRequest)
			if !ok || req == nil {
				return nil
			}
			var out []pii.ScannedText
			for i := range req.Messages {
				msg := &req.Messages[i]
				switch ct := msg.Content.(type) {
				case string:
					if ct != "" {
						// Index encodes (message index, -1) to mean
						// "the whole Content string". Negative
						// inner indices are a valid sentinel because
						// real array indices are ≥ 0.
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
			req, ok := parsed.(*schema.OpenAIRequest)
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
					// Whole-string content. Write BOTH the serializable
					// Content and the StringContent staging buffer: the
					// rendered-template path (evaluator.TemplateMessages,
					// taken whenever use_tokenizer_template is off — e.g.
					// cloud-proxy translate and Go-templated chat models)
					// reads StringContent, not Content. Masking only Content
					// would leave the original in StringContent and leak it
					// to the backend/upstream.
					msg.Content = u.Text
					msg.StringContent = u.Text
					continue
				}
				blocks, ok := msg.Content.([]any)
				if !ok || blockIdx >= len(blocks) {
					continue
				}
				blockMap, ok := blocks[blockIdx].(map[string]any)
				if !ok {
					continue
				}
				// Keep the StringContent projection in sync. For multimodal
				// messages StringContent is the text blocks flattened with
				// media markers injected (see middleware/request.go), so we
				// can't just overwrite it — replace this block's original text
				// run in place, preserving the markers around it.
				if orig, ok := blockMap["text"].(string); ok && orig != "" && msg.StringContent != "" {
					msg.StringContent = strings.Replace(msg.StringContent, orig, u.Text, 1)
				}
				blockMap["text"] = u.Text
			}
		},
	}
}

// encodeIdx packs (msg, block) into one int. block=-1 means
// "the whole Content string"; bit 24 is the sentinel flag and
// bits 0..23 hold the block index, leaving the rest for msg.
const idxWholeStringFlag = 1 << 24
const idxBlockMask = (1 << 24) - 1

func encodeIdx(msg, block int) int {
	if block < 0 {
		return (msg << 25) | idxWholeStringFlag
	}
	return (msg << 25) | (block & idxBlockMask)
}

func decodeIdx(packed int) (msg, block int) {
	msg = packed >> 25
	if packed&idxWholeStringFlag != 0 {
		return msg, -1
	}
	return msg, packed & idxBlockMask
}
