package piiadapter

import (
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/pii"
)

// Field selectors for the prompt-style OpenAI requests (/v1/completions,
// /v1/embeddings, /v1/edits), which carry user text in Prompt / Input /
// Instruction rather than Messages.
const (
	fldPrompt = iota
	fldInput
	fldInstruction
)

// encField packs (field, element) into one ScannedText.Index. element=-1
// means the field is a whole string; element>=0 indexes into a []any value.
// Stored as element+1 so -1 maps to 0, with the field in the high bits.
func encField(field, elem int) int     { return (field << 24) | (elem + 1) }
func decField(p int) (field, elem int) { return p >> 24, (p & 0xFFFFFF) - 1 }

// scanAnyText appends scannable strings from a string-or-[]any field. Non-string
// array elements (token-id arrays, numbers) are skipped — only human text is
// redacted.
func scanAnyText(field int, v any, out *[]pii.ScannedText) {
	switch t := v.(type) {
	case string:
		if t != "" {
			*out = append(*out, pii.ScannedText{Index: encField(field, -1), Text: t})
		}
	case []any:
		for k, e := range t {
			if s, ok := e.(string); ok && s != "" {
				*out = append(*out, pii.ScannedText{Index: encField(field, k), Text: s})
			}
		}
	}
}

// applyAnyText writes redacted text back to a string-or-[]any field, returning
// the (possibly replaced) value to assign back to the struct field.
func applyAnyText(v any, elem int, text string) any {
	if elem < 0 {
		return text
	}
	if arr, ok := v.([]any); ok && elem < len(arr) {
		arr[elem] = text
	}
	return v
}

// OpenAICompletion returns a pii.Adapter for the prompt-style OpenAI requests
// (completions, embeddings, edits) on *schema.OpenAIRequest. It scans Prompt,
// Input and Instruction — the string form and the string elements of an array
// form — and writes redacted text back. Chat uses the separate OpenAI()
// adapter (Messages); these endpoints leave Messages empty and vice versa.
func OpenAICompletion() pii.Adapter {
	return pii.Adapter{
		Scan: func(parsed any) []pii.ScannedText {
			req, ok := parsed.(*schema.OpenAIRequest)
			if !ok || req == nil {
				return nil
			}
			var out []pii.ScannedText
			scanAnyText(fldPrompt, req.Prompt, &out)
			scanAnyText(fldInput, req.Input, &out)
			if req.Instruction != "" {
				out = append(out, pii.ScannedText{Index: encField(fldInstruction, -1), Text: req.Instruction})
			}
			return out
		},
		Apply: func(parsed any, updates []pii.ScannedText) {
			req, ok := parsed.(*schema.OpenAIRequest)
			if !ok || req == nil {
				return
			}
			for _, u := range updates {
				field, elem := decField(u.Index)
				switch field {
				case fldPrompt:
					req.Prompt = applyAnyText(req.Prompt, elem, u.Text)
				case fldInput:
					req.Input = applyAnyText(req.Input, elem, u.Text)
				case fldInstruction:
					req.Instruction = u.Text
				}
			}
		},
	}
}
