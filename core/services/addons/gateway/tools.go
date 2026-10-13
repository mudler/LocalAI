// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"strings"
)

// ToolSource reports the tools an installed add-on declares in its manifest.
// DeclaredTools answers for the current installed generation only: a request
// for any other generation, an add-on that is not installed, or a manifest
// that cannot be read is an error, and every error is a denial.
type ToolSource interface {
	DeclaredTools(ctx context.Context, addonID string, generation uint64) ([]string, error)
}

// ToolKey is the argument key that names the add-on tool a request invokes.
const ToolKey = "tool"

// AuthorizeTool is the server-side rule that a request only invokes a tool
// its add-on declared in capabilities.tools.
//
// The tool name travels in the top-level "tool" key of the request arguments.
// The rule is exact and fails closed:
//
//   - no "tool" key: the request names no tool and the rule has nothing to
//     check (a plain resource read);
//   - a key that equals "tool" only after case folding or trimming white
//     space, a duplicate, or a value that is not a string: denied, because
//     the add-on's decoder may read it as the tool key;
//   - an exact "tool" key: the value must be byte-for-byte one declared tool.
//     Case, Unicode and white-space variants are other names;
//   - no source, or a source that cannot answer: denied.
func AuthorizeTool(ctx context.Context, src ToolSource, addonID string, generation uint64, arguments json.RawMessage) error {
	var args map[string]json.RawMessage
	if len(arguments) == 0 {
		return nil
	}
	if json.Unmarshal(arguments, &args) != nil {
		return ErrForbidden
	}
	var raw json.RawMessage
	named := false
	for key, value := range args {
		if key == ToolKey {
			raw, named = value, true
			continue
		}
		// A decoder that folds case or trims would read this key as the tool.
		if strings.EqualFold(strings.TrimSpace(key), ToolKey) {
			return ErrForbidden
		}
	}
	if !named {
		return nil
	}
	var tool string
	if json.Unmarshal(raw, &tool) != nil || tool == "" || src == nil {
		return ErrForbidden
	}
	declared, err := src.DeclaredTools(ctx, addonID, generation)
	if err != nil {
		return ErrForbidden
	}
	for _, d := range declared {
		if d == tool {
			return nil
		}
	}
	return ErrForbidden
}
