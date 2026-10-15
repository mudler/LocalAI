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

// slot addresses one scannable text in a request: a field, an element of an
// array value (elem -1 means the whole value) and a part of a structured
// content object (part -1 means the element itself is the text). Slots are
// plain ints, so no width of int can overflow or collide with another slot.
type slot struct {
	field, elem, part int
}

// textVisitor receives one scannable text and the slot it came from.
type textVisitor func(s slot, text string)

// visitContentParts visits the text parts of a structured content object
// ({"content":[{"type":"text","text":"..."},...]}). Media parts are not text
// and are skipped.
func visitContentParts(field, elem int, obj map[string]any, visit textVisitor) {
	parts, _ := obj["content"].([]any)
	for k, p := range parts {
		m, ok := p.(map[string]any)
		if !ok || m["type"] != "text" {
			continue
		}
		if s, ok := m["text"].(string); ok && s != "" {
			visit(slot{field, elem, k}, s)
		}
	}
}

// visitAnyText visits scannable strings from a string-or-[]any field. Non-string
// array elements (token-id arrays, numbers) are skipped; only human text is
// redacted. When structured is set, the text parts of content objects are
// visited too, with the same results as a plain string.
func visitAnyText(field int, v any, structured bool, visit textVisitor) {
	switch t := v.(type) {
	case string:
		if t != "" {
			visit(slot{field, -1, -1}, t)
		}
	case map[string]any:
		if structured {
			visitContentParts(field, -1, t, visit)
		}
	case []any:
		for k, e := range t {
			switch el := e.(type) {
			case string:
				if el != "" {
					visit(slot{field, k, -1}, el)
				}
			case map[string]any:
				if structured {
					visitContentParts(field, k, el, visit)
				}
			}
		}
	}
}

// scanSlots turns a walk into ScannedText values. Index is the position in walk
// order, so Apply can find the same slot again by walking the same request.
func scanSlots(walk func(textVisitor)) []pii.ScannedText {
	var out []pii.ScannedText
	walk(func(_ slot, text string) {
		out = append(out, pii.ScannedText{Index: len(out), Text: text})
	})
	return out
}

// slotsOf lists the slots of a walk in the order scanSlots numbers them.
func slotsOf(walk func(textVisitor)) []slot {
	var out []slot
	walk(func(s slot, _ string) { out = append(out, s) })
	return out
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

// completionWalk visits Prompt, Input and Instruction in a fixed order.
func completionWalk(req *schema.OpenAIRequest) func(textVisitor) {
	return func(visit textVisitor) {
		visitAnyText(fldPrompt, req.Prompt, false, visit)
		visitAnyText(fldInput, req.Input, true, visit)
		if req.Instruction != "" {
			visit(slot{fldInstruction, -1, -1}, req.Instruction)
		}
	}
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
			return scanSlots(completionWalk(req))
		},
		Apply: func(parsed any, updates []pii.ScannedText) {
			req, ok := parsed.(*schema.OpenAIRequest)
			if !ok || req == nil {
				return
			}
			// Resolve every index before the first write, against the request as
			// Scan saw it. An index outside the scanned slots names no text and is
			// dropped; it can never be written into another field.
			slots := slotsOf(completionWalk(req))
			for _, u := range updates {
				if u.Index < 0 || u.Index >= len(slots) {
					continue
				}
				s := slots[u.Index]
				switch s.field {
				case fldPrompt:
					req.Prompt = applyAnyText(req.Prompt, s.elem, s.part, u.Text)
				case fldInput:
					req.Input = applyAnyText(req.Input, s.elem, s.part, u.Text)
				case fldInstruction:
					req.Instruction = u.Text
				}
			}
		},
	}
}
