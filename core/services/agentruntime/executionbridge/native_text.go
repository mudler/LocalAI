// SPDX-License-Identifier: MIT
package executionbridge

import (
	"reflect"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	chat "github.com/mudler/nib/chat"
)

// pinnedFields makes schema additions fail closed, including private fields.
// This adapter is intentionally tied to the readonly go-sdk v1.5.0 pin.
func pinnedFields(t reflect.Type, names ...string) bool {
	if t.NumField() != len(names) {
		return false
	}
	for i, name := range names {
		if t.Field(i).Name != name {
			return false
		}
	}
	return true
}

// normalizeNativeText accounts for EVERY pinned field:
// CallToolResult: Meta absent, Content inspected, StructuredContent absent,
// IsError false, private err zero (checked reflectively without extracting it).
// TextContent: Text concatenated verbatim, Meta and Annotations both absent.
// Cogito contentToString concatenates text items with NO separator. We require
// exact equality with the parallel Text rather than trusting or replacing it.
// This narrow transport normalization confers no projection/release authority.
func normalizeNativeText(result chat.ToolExecutionResult) (string, error) {
	if result.Data == nil {
		if validateFreshText(result.Text) != nil {
			return "", ErrHold
		}
		return strings.Clone(result.Text), nil
	}
	r, ok := result.Data.(*sdkmcp.CallToolResult)
	if !ok || r == nil || !pinnedFields(reflect.TypeFor[sdkmcp.CallToolResult](), "Meta", "Content", "StructuredContent", "IsError", "err") || !pinnedFields(reflect.TypeFor[sdkmcp.TextContent](), "Text", "Meta", "Annotations") {
		return "", ErrHold
	}
	if r.Meta != nil || r.StructuredContent != nil || r.IsError || !reflect.ValueOf(r).Elem().FieldByName("err").IsZero() {
		return "", ErrHold
	}
	var text strings.Builder
	for _, item := range r.Content {
		t, ok := item.(*sdkmcp.TextContent)
		if !ok || t == nil || t.Meta != nil || t.Annotations != nil || validateFreshText(t.Text) != nil || len(t.Text) > maxTextBytes-text.Len() {
			return "", ErrHold
		}
		text.WriteString(t.Text)
	}
	normalized := text.String()
	if normalized != result.Text || validateFreshText(normalized) != nil {
		return "", ErrHold
	}
	return normalized, nil
}
