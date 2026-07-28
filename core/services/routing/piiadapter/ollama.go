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

// Field selectors for OllamaEmbed (Input + its Prompt alias). Reuses the
// shared encField/decField packing.
const (
	ollamaEmbInput = iota
	ollamaEmbPrompt
)

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
			var out []pii.ScannedText
			scanAnyText(ollamaEmbInput, req.Input, &out)
			scanAnyText(ollamaEmbPrompt, req.Prompt, &out)
			return out
		},
		Apply: func(parsed any, updates []pii.ScannedText) {
			req, ok := parsed.(*schema.OllamaEmbedRequest)
			if !ok || req == nil {
				return
			}
			for _, u := range updates {
				field, elem := decField(u.Index)
				switch field {
				case ollamaEmbInput:
					req.Input = applyAnyText(req.Input, elem, u.Text)
				case ollamaEmbPrompt:
					req.Prompt = applyAnyText(req.Prompt, elem, u.Text)
				}
			}
		},
	}
}
