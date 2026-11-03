// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"fmt"
)

// MinLegacyArgsAPI is the first manifest API version that knows legacyArgs. A
// manifest that declares one must accept it.
const MinLegacyArgsAPI uint64 = 2

// Bounds of the legacy argument list of one tool.
const (
	MaxLegacyArgs      = 8
	MaxLegacyArgChars  = 64
	MaxLegacyHintChars = 200
)

// LegacyArgument names an argument that an older version of a tool took and
// that the tool refuses now. When a call fails the schema check and carries
// it, the host answers with the hint instead of a bare refusal, so a model
// that learned the old schema can correct itself. The argument is never
// forwarded.
type LegacyArgument struct {
	// Name is the old argument name. The tool's schema must not declare it.
	Name string `json:"name"`
	// Hint is one plain sentence on what to send instead, such as "use the
	// markdown string".
	Hint string `json:"hint"`
}

// LegacyMessage returns the message for a call whose arguments carry a legacy
// argument of the tool d, or "". Arguments that would be refused for another
// reason (a forbidden name, bad JSON) get no message.
func LegacyMessage(d ToolDefinition, raw json.RawMessage) string {
	if len(d.LegacyArgs) == 0 || len(raw) == 0 {
		return ""
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return ""
	}
	for key := range fields {
		if ForbiddenToolProperty(key) {
			return ""
		}
	}
	for _, l := range d.LegacyArgs {
		if _, ok := fields[l.Name]; ok {
			return fmt.Sprintf("%s is no longer supported; %s", l.Name, l.Hint)
		}
	}
	return ""
}

func validateLegacyArgs(d ToolDefinition) error {
	if len(d.LegacyArgs) == 0 {
		return nil
	}
	if len(d.LegacyArgs) > MaxLegacyArgs {
		return fmt.Errorf("tool %s: too many legacy arguments", d.Name)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal(d.InputSchema, &schema)
	seen := map[string]bool{}
	for _, l := range d.LegacyArgs {
		if !plainText(l.Name, MaxLegacyArgChars) || !plainText(l.Hint, MaxLegacyHintChars) || ForbiddenToolProperty(l.Name) || seen[l.Name] {
			return fmt.Errorf("tool %s: invalid legacy argument", d.Name)
		}
		if _, ok := schema.Properties[l.Name]; ok {
			return fmt.Errorf("tool %s: legacy argument %s is in the schema", d.Name, l.Name)
		}
		seen[l.Name] = true
	}
	return nil
}
