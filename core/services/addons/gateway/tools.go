// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ToolSource reports the tools an installed add-on declares in its manifest.
// DeclaredTools answers for the current installed generation only: a request
// for any other generation, an add-on that is not installed, or a manifest
// that cannot be read is an error. A source answers a real "not declared",
// "stale generation" or "not installed" with a denial (ErrForbidden or a
// wrapped denial). It answers ErrNotOwner, ErrRecoveryRequired, a context
// error or any unclassified failure as an error that is unavailable.
type ToolSource interface {
	DeclaredTools(ctx context.Context, addonID string, generation uint64) ([]string, error)
}

// CapabilitySource reports the declared capabilities of the installed
// generation, which carry the kind of every tool the model may call. It
// answers like ToolSource: a stale or unknown generation is a denial, an
// uncertain journal is unavailable.
type CapabilitySource interface {
	DeclaredCapabilities(ctx context.Context, addonID string, generation uint64) (contract.Capabilities, error)
}

// NamedTool returns the tool the arguments name. AuthorizeTool has already
// refused ambiguous arguments, so the answer here is exact: ok is false when
// the arguments name no tool or the name is not a string.
func NamedTool(arguments json.RawMessage) (tool string, ok bool) {
	var args map[string]json.RawMessage
	if json.Unmarshal(arguments, &args) != nil {
		return "", false
	}
	raw, named := args[ToolKey]
	if !named || json.Unmarshal(raw, &tool) != nil || tool == "" {
		return "", false
	}
	return tool, true
}

// ToolKey is the argument key that names the add-on tool a request invokes.
const ToolKey = "tool"

// AuthorizeTool is the server-side rule that a request only invokes a tool
// its add-on declared in capabilities.tools.
//
// The tool name travels in the top-level "tool" key of the request arguments.
// The rule is exact and fails closed, and it holds on its own, whatever
// validation the arguments passed before:
//
//   - empty arguments name no tool and the rule has nothing to check;
//   - arguments that are not exactly one JSON object (an array, a scalar,
//     null, trailing data, invalid JSON, nesting past 64 levels): denied;
//   - a repeated key at any depth, compared after unescaping, trimming white
//     space and Unicode case folding: denied, because a runtime that keeps the
//     first value would read another tool than the one checked;
//   - no "tool" key: the request names no tool (a plain resource read);
//   - a top-level key that equals "tool" only after case folding or trimming
//     white space, or a value that is not a string: denied;
//   - an exact "tool" key: the value must be byte-for-byte one declared tool.
//     Case, Unicode and white-space variants are other names;
//   - no source: denied. A tool that is not declared, or a generation that is
//     stale or not installed: denied (ErrForbidden, 403);
//   - a source that cannot answer (closed manager, recovery required, a
//     cancelled context, an unclassified failure): ErrUnavailable wrapping the
//     cause (503). It is never reported as a policy decision.
func AuthorizeTool(ctx context.Context, src ToolSource, addonID string, generation uint64, arguments json.RawMessage) error {
	if len(arguments) == 0 {
		return nil
	}
	if checkObject(arguments) != nil {
		return ErrForbidden
	}
	var args map[string]json.RawMessage
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
		return toolSourceError(err)
	}
	for _, d := range declared {
		if d == tool {
			return nil
		}
	}
	return ErrForbidden
}

// toolSourceError classifies a failure of the tool source. Only an explicit
// denial that carries no unavailable cause is a denial; everything else, a
// context error and an unclassified failure included, is unavailable.
func toolSourceError(err error) error {
	if errors.Is(err, ErrUnavailable) {
		return err
	}
	if StatusFor(err) == 403 {
		return ErrForbidden
	}
	return fmt.Errorf("%w: declared tools: %w", ErrUnavailable, err)
}

const maxToolArgumentDepth = 64

// CheckToolArguments applies the structural rule of AuthorizeTool to a
// consumer that builds the arguments itself: exactly one JSON object with no
// repeated key at any depth, no trailing data and bounded nesting.
func CheckToolArguments(arguments []byte) error { return checkObject(arguments) }

// checkObject accepts exactly one JSON object with no repeated key at any
// depth and nothing after it.
func checkObject(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' {
		return errors.New("arguments are not an object")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := walkValue(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing data")
	}
	return nil
}

func walkValue(d *json.Decoder, depth int) error {
	if depth > maxToolArgumentDepth {
		return errors.New("nesting too deep")
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for d.More() {
			kt, err := d.Token()
			if err != nil {
				return err
			}
			k, ok := kt.(string)
			if !ok {
				return errors.New("non-string key")
			}
			// The decoder has unescaped the key. Fold and trim so that
			// variants a lenient runtime decoder treats as one key collide.
			folded := strings.ToLower(strings.TrimSpace(k))
			if _, dup := seen[folded]; dup {
				return errors.New("repeated key")
			}
			seen[folded] = struct{}{}
			if err := walkValue(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := walkValue(d, depth+1); err != nil {
				return err
			}
		}
	}
	_, err = d.Token()
	return err
}
