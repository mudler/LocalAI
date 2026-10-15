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

// encPart packs (field, element, part) into one ScannedText.Index. element=-1
// means the field is a whole value; element>=0 indexes into a []any value.
// part=-1 means the element itself is the text; part>=0 indexes into the
// "content" array of a structured content object. Offsets by one let -1 map to
// zero. Layout: field in bits 48+, element+1 in bits 24-47, part+1 in bits 0-23.
func encPart(field, elem, part int) int {
	return (field << 48) | ((elem + 1) << 24) | (part + 1)
}
func decPart(p int) (field, elem, part int) {
	return p >> 48, ((p >> 24) & 0xFFFFFF) - 1, (p & 0xFFFFFF) - 1
}
func encField(field, elem int) int { return encPart(field, elem, -1) }

// scanContentParts appends the text parts of a structured content object
// ({"content":[{"type":"text","text":"..."},...]}). Media parts are not text
// and are skipped.
func scanContentParts(field, elem int, obj map[string]any, out *[]pii.ScannedText) {
	parts, _ := obj["content"].([]any)
	for k, p := range parts {
		m, ok := p.(map[string]any)
		if !ok || m["type"] != "text" {
			continue
		}
		if s, ok := m["text"].(string); ok && s != "" {
			*out = append(*out, pii.ScannedText{Index: encPart(field, elem, k), Text: s})
		}
	}
}

// scanAnyText appends scannable strings from a string-or-[]any field. Non-string
// array elements (token-id arrays, numbers) are skipped; only human text is
// redacted. When structured is set, the text parts of content objects are
// scanned too, with the same results as a plain string.
func scanAnyText(field int, v any, structured bool, out *[]pii.ScannedText) {
	switch t := v.(type) {
	case string:
		if t != "" {
			*out = append(*out, pii.ScannedText{Index: encField(field, -1), Text: t})
		}
	case map[string]any:
		if structured {
			scanContentParts(field, -1, t, out)
		}
	case []any:
		for k, e := range t {
			switch el := e.(type) {
			case string:
				if el != "" {
					*out = append(*out, pii.ScannedText{Index: encField(field, k), Text: el})
				}
			case map[string]any:
				if structured {
					scanContentParts(field, k, el, out)
				}
			}
		}
	}
}

// applyContentPart writes redacted text into one text part of a content object.
func applyContentPart(obj map[string]any, part int, text string) {
	parts, _ := obj["content"].([]any)
	if part < 0 || part >= len(parts) {
		return
	}
	if m, ok := parts[part].(map[string]any); ok && m["type"] == "text" {
		m["text"] = text
	}
}

// applyAnyText writes redacted text back to a string-or-[]any field, returning
// the (possibly replaced) value to assign back to the struct field.
func applyAnyText(v any, elem, part int, text string) any {
	if part >= 0 {
		switch t := v.(type) {
		case map[string]any:
			applyContentPart(t, part, text)
		case []any:
			if elem >= 0 && elem < len(t) {
				if m, ok := t[elem].(map[string]any); ok {
					applyContentPart(m, part, text)
				}
			}
		}
		return v
	}
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
// Input and Instruction: the string form, the string elements of an array
// form and the text parts of embedding content objects. It writes redacted text back. Chat uses the separate OpenAI()
// adapter (Messages); these endpoints leave Messages empty and vice versa.
func OpenAICompletion() pii.Adapter {
	return pii.Adapter{
		Scan: func(parsed any) []pii.ScannedText {
			req, ok := parsed.(*schema.OpenAIRequest)
			if !ok || req == nil {
				return nil
			}
			var out []pii.ScannedText
			scanAnyText(fldPrompt, req.Prompt, false, &out)
			scanAnyText(fldInput, req.Input, true, &out)
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
				field, elem, part := decPart(u.Index)
				switch field {
				case fldPrompt:
					req.Prompt = applyAnyText(req.Prompt, elem, part, u.Text)
				case fldInput:
					req.Input = applyAnyText(req.Input, elem, part, u.Text)
				case fldInstruction:
					req.Instruction = u.Text
				}
			}
		},
	}
}
