package piiadapter

import (
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/pii"
)

// OllamaChat returns a pii.Adapter for *schema.OllamaChatRequest (POST
// /api/chat). It scans each message's text content (Ollama messages carry a
// plain string, no multimodal block form) and writes redacted text back.
func OllamaChat() pii.Adapter {
	return pii.Adapter{
		Scan: func(parsed any) []pii.ScannedText {
			req, ok := parsed.(*schema.OllamaChatRequest)
			if !ok || req == nil {
				return nil
			}
			var out []pii.ScannedText
			for i := range req.Messages {
				if req.Messages[i].Content != "" {
					out = append(out, pii.ScannedText{Index: i, Text: req.Messages[i].Content})
				}
			}
			return out
		},
		Apply: func(parsed any, updates []pii.ScannedText) {
			req, ok := parsed.(*schema.OllamaChatRequest)
			if !ok || req == nil {
				return
			}
			for _, u := range updates {
				if u.Index >= 0 && u.Index < len(req.Messages) {
					req.Messages[u.Index].Content = u.Text
				}
			}
		},
	}
}

// Field selectors for OllamaGenerate (Prompt + System).
const (
	ollamaGenPrompt = iota
	ollamaGenSystem
)

// OllamaGenerate returns a pii.Adapter for *schema.OllamaGenerateRequest (POST
// /api/generate). It scans the Prompt and System strings.
func OllamaGenerate() pii.Adapter {
	return pii.Adapter{
		Scan: func(parsed any) []pii.ScannedText {
			req, ok := parsed.(*schema.OllamaGenerateRequest)
			if !ok || req == nil {
				return nil
			}
			var out []pii.ScannedText
			if req.Prompt != "" {
				out = append(out, pii.ScannedText{Index: ollamaGenPrompt, Text: req.Prompt})
			}
			if req.System != "" {
				out = append(out, pii.ScannedText{Index: ollamaGenSystem, Text: req.System})
			}
			return out
		},
		Apply: func(parsed any, updates []pii.ScannedText) {
			req, ok := parsed.(*schema.OllamaGenerateRequest)
			if !ok || req == nil {
				return
			}
			for _, u := range updates {
				switch u.Index {
				case ollamaGenPrompt:
					req.Prompt = u.Text
				case ollamaGenSystem:
					req.System = u.Text
				}
			}
		},
	}
}

// Field selectors for OllamaEmbed (Input + its Prompt alias). They share the
// slot addressing used by the OpenAI adapter.
const (
	ollamaEmbInput = iota
	ollamaEmbPrompt
)

// ollamaEmbWalk visits Input and its Prompt alias in a fixed order.
func ollamaEmbWalk(req *schema.OllamaEmbedRequest) func(textVisitor) {
	return func(visit textVisitor) {
		visitAnyText(ollamaEmbInput, req.Input, false, visit)
		visitAnyText(ollamaEmbPrompt, req.Prompt, false, visit)
	}
}

// OllamaEmbed returns a pii.Adapter for *schema.OllamaEmbedRequest (POST
// /api/embed, /api/embeddings). Input and its Prompt alias may be a string or
// a []any of strings; non-string elements are skipped.
func OllamaEmbed() pii.Adapter {
	return pii.Adapter{
		Scan: func(parsed any) []pii.ScannedText {
			req, ok := parsed.(*schema.OllamaEmbedRequest)
			if !ok || req == nil {
				return nil
			}
			return scanSlots(ollamaEmbWalk(req))
		},
		Apply: func(parsed any, updates []pii.ScannedText) {
			req, ok := parsed.(*schema.OllamaEmbedRequest)
			if !ok || req == nil {
				return
			}
			slots := slotsOf(ollamaEmbWalk(req))
			for _, u := range updates {
				if u.Index < 0 || u.Index >= len(slots) {
					continue
				}
				s := slots[u.Index]
				switch s.field {
				case ollamaEmbInput:
					req.Input = applyAnyText(req.Input, s.elem, s.part, u.Text)
				case ollamaEmbPrompt:
					req.Prompt = applyAnyText(req.Prompt, s.elem, s.part, u.Text)
				}
			}
		},
	}
}
