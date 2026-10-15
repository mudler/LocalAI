// SPDX-License-Identifier: MIT
package piiadapter

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	"github.com/mudler/xlog"
)

// Top-level positions in an Open Responses request. Real input-item
// indices are >= 0, so the negative values cannot collide with them.
const (
	orInstructions = -2
	orInputString  = -1
)

// encOR packs (item, part) into one ScannedText.Index. item is an index
// into the input item array or one of the orInstructions / orInputString
// sentinels; part=-1 means "the item's whole text field", part>=0 indexes
// into a content-part array. Both are stored offset so the sentinels map
// to non-negative bit fields.
func encOR(item, part int) int     { return ((item + 2) << 24) | (part + 1) }
func decOR(p int) (item, part int) { return (p >> 24) - 2, (p & 0xFFFFFF) - 1 }

// orItemTextField names the field of an input item that carries the text
// the Responses handler forwards to the model, or "" for items whose text
// is not part of the prompt (item_reference: the referenced item was
// scanned when it was first submitted). Scan and Apply both resolve the
// field through here so they can never disagree on where a span lives.
func orItemTextField(item map[string]any) string {
	itemType, _ := item["type"].(string)
	if itemType == "" {
		// Bare {role, content} objects are message items; the handler
		// (convertORInputToMessages) treats them the same way.
		if _, hasRole := item["role"].(string); hasRole {
			itemType = "message"
		}
	}
	switch itemType {
	case "message", "reasoning":
		return "content"
	case "function_call_output":
		return "output"
	case "function_call":
		return "arguments"
	}
	return ""
}

// isORTextPart reports whether a content part carries plain text. Image,
// file, audio and video parts are left alone, as in the other adapters.
func isORTextPart(part map[string]any) bool {
	t, _ := part["type"].(string)
	return t == "input_text" || t == "output_text"
}

// OpenResponses returns a pii.Adapter for *schema.OpenResponsesRequest
// (POST /v1/responses). It scans every place the handler takes prompt text
// from: Instructions, Input as a plain string, and in an Input item array
// the text of message and reasoning items (string content or input_text /
// output_text parts), function_call arguments and function_call_output
// output. Without it the same content that is blocked on
// /v1/chat/completions could be sent unfiltered through /v1/responses.
//
// A function_call_output whose output is neither a string nor a part array
// is forwarded by the handler as its JSON encoding, so that encoding is
// scanned as one span; a redaction replaces the value with the redacted
// JSON string, which the handler then forwards verbatim.
func OpenResponses() pii.Adapter {
	return pii.Adapter{
		Scan: func(parsed any) []pii.ScannedText {
			req, ok := parsed.(*schema.OpenResponsesRequest)
			if !ok || req == nil {
				return nil
			}
			var out []pii.ScannedText
			if req.Instructions != "" {
				out = append(out, pii.ScannedText{Index: encOR(orInstructions, -1), Text: req.Instructions})
			}
			switch in := req.Input.(type) {
			case string:
				if in != "" {
					out = append(out, pii.ScannedText{Index: encOR(orInputString, -1), Text: in})
				}
			case []any:
				for i, raw := range in {
					item, ok := raw.(map[string]any)
					if !ok {
						continue
					}
					field := orItemTextField(item)
					if field == "" {
						continue
					}
					switch v := item[field].(type) {
					case nil:
						// Missing field: nothing reaches the model.
					case string:
						if v != "" {
							out = append(out, pii.ScannedText{Index: encOR(i, -1), Text: v})
						}
					case []any:
						for j, p := range v {
							part, ok := p.(map[string]any)
							if !ok || !isORTextPart(part) {
								continue
							}
							if text, ok := part["text"].(string); ok && text != "" {
								out = append(out, pii.ScannedText{Index: encOR(i, j), Text: text})
							}
						}
					default:
						if field != "output" {
							continue
						}
						b, err := json.Marshal(v)
						if err != nil {
							xlog.Warn("pii: cannot encode function_call_output for scanning", "error", err)
							continue
						}
						out = append(out, pii.ScannedText{Index: encOR(i, -1), Text: string(b)})
					}
				}
			}
			return out
		},
		Apply: func(parsed any, updates []pii.ScannedText) {
			req, ok := parsed.(*schema.OpenResponsesRequest)
			if !ok || req == nil {
				return
			}
			for _, u := range updates {
				itemIdx, partIdx := decOR(u.Index)
				switch itemIdx {
				case orInstructions:
					req.Instructions = u.Text
					continue
				case orInputString:
					if _, ok := req.Input.(string); ok {
						req.Input = u.Text
					}
					continue
				}
				items, ok := req.Input.([]any)
				if !ok || itemIdx < 0 || itemIdx >= len(items) {
					continue
				}
				item, ok := items[itemIdx].(map[string]any)
				if !ok {
					continue
				}
				field := orItemTextField(item)
				if field == "" {
					continue
				}
				if partIdx < 0 {
					item[field] = u.Text
					continue
				}
				parts, ok := item[field].([]any)
				if !ok || partIdx >= len(parts) {
					continue
				}
				if part, ok := parts[partIdx].(map[string]any); ok {
					part["text"] = u.Text
				}
			}
		},
	}
}
