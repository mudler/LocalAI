// SPDX-License-Identifier: MIT

// Package toolargs validates the arguments of an add-on tool call against the
// tool definition of the verified manifest. A model's call and a person's own
// UI action are validated by the same code, so one tool schema is one rule.
// The package decides nothing about authority: it only checks shape.
package toolargs

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var (
	// ErrForbiddenArgument means the arguments carry a field that stands for
	// identity, authority or approval.
	ErrForbiddenArgument = errors.New("addon tool arguments carry a reserved field")
	// ErrInvalid means the arguments are not what the tool schema declares.
	ErrInvalid = errors.New("addon tool arguments are invalid")
)

// MaxArgumentBytes bounds the arguments of one call as the add-on receives
// them, with the tool key the host adds. It equals the payload
// limit of the add-on contract, so a tool that takes an inline dataset of up to
// 900 KiB can be called by a model.
const MaxArgumentBytes = 1 << 20

var resourceID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

var conversationPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// ValidConversationID reports whether id has the shape of a conversation
// identifier: the namespace of one user's selections.
func ValidConversationID(id string) bool { return conversationPattern.MatchString(id) }

// ValidResourceID reports whether id has the shape of a resource identifier.
func ValidResourceID(id string) bool { return resourceID.MatchString(id) }

// Bind validates the model's arguments against the manifest schema
// and returns the resource ID and the argument object the owner receives. The
// resource is taken out of the object; the tool name is put in by the host.
func Bind(tool string, parameters map[string]any, raw json.RawMessage, omitResource bool) (string, json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > MaxArgumentBytes {
		return "", nil, ErrInvalid
	}
	if gateway.CheckToolArguments(raw) != nil {
		return "", nil, ErrInvalid
	}
	var in map[string]json.RawMessage
	if json.Unmarshal(raw, &in) != nil {
		return "", nil, ErrInvalid
	}
	// Identity, role, approval and operation fields are refused first and by
	// name, whatever the schema declares.
	for k := range in {
		if contract.ForbiddenToolProperty(k) {
			return "", nil, ErrForbiddenArgument
		}
	}
	props, _ := parameters["properties"].(map[string]any)
	for k, v := range in {
		spec, ok := props[k].(map[string]any)
		if !ok {
			return "", nil, ErrInvalid
		}
		if !matches(spec, v) {
			return "", nil, ErrInvalid
		}
	}
	if req, ok := parameters["required"].([]any); ok {
		for _, r := range req {
			if s, _ := r.(string); s != "" {
				// A chat-bound call may leave the resource out: the owner
				// then resolves the conversation's selection.
				if s == contract.ResourceArgument && omitResource {
					continue
				}
				if _, present := in[s]; !present {
					return "", nil, ErrInvalid
				}
			}
		}
	}
	resource := ""
	if v, ok := in[contract.ResourceArgument]; ok {
		if json.Unmarshal(v, &resource) != nil || !resourceID.MatchString(resource) {
			return "", nil, ErrInvalid
		}
		delete(in, contract.ResourceArgument)
	}
	toolValue, _ := json.Marshal(tool)
	in["tool"] = toolValue
	body, err := json.Marshal(in)
	if err != nil {
		return "", nil, ErrInvalid
	}
	// The limit is on what the add-on receives, and the host adds the tool key.
	// Bounding the bound body keeps it within the payload limit of the
	// contract, so a call that Bind accepts is never refused later for size.
	if len(body) > MaxArgumentBytes {
		return "", nil, ErrInvalid
	}
	return resource, body, nil
}

// matches checks the top-level type, enum and size keywords of one property.
// Nested structure is the add-on's to validate: it owns the typed operation.
func matches(spec map[string]any, v json.RawMessage) bool {
	typ, _ := spec["type"].(string)
	var x any
	d := json.NewDecoder(strings.NewReader(string(v)))
	d.UseNumber()
	if d.Decode(&x) != nil {
		return false
	}
	switch typ {
	case "":
	case "string":
		s, ok := x.(string)
		if !ok {
			return false
		}
		if n, ok := number(spec["maxLength"]); ok && int64(len([]rune(s))) > n {
			return false
		}
		if en, ok := spec["enum"].([]any); ok && !slices.Contains(en, any(s)) {
			return false
		}
	case "integer", "number":
		n, ok := x.(json.Number)
		if !ok {
			return false
		}
		f, err := n.Float64()
		if err != nil || (typ == "integer" && strings.ContainsAny(n.String(), ".eE")) {
			return false
		}
		if lo, ok := number(spec["minimum"]); ok && f < float64(lo) {
			return false
		}
		if hi, ok := number(spec["maximum"]); ok && f > float64(hi) {
			return false
		}
	case "boolean":
		if _, ok := x.(bool); !ok {
			return false
		}
	case "array":
		list, ok := x.([]any)
		if !ok {
			return false
		}
		if n, ok := number(spec["maxItems"]); ok && int64(len(list)) > n {
			return false
		}
		if n, ok := number(spec["minItems"]); ok && int64(len(list)) < n {
			return false
		}
	case "object":
		if _, ok := x.(map[string]any); !ok {
			return false
		}
	default:
		return false
	}
	return true
}

func number(v any) (int64, bool) {
	f, ok := v.(float64)
	if !ok {
		return 0, false
	}
	return int64(f), true
}

// BindDefinition validates the arguments of a person-initiated call of a
// tool against its manifest definition, the same way a model's call is, and
// returns the resource ID taken out of the arguments and the argument object
// with the tool name set by the host.
func BindDefinition(d contract.ToolDefinition, raw json.RawMessage) (string, json.RawMessage, error) {
	if contract.ValidateToolSchema(d.InputSchema, d.Kind) != nil {
		return "", nil, ErrInvalid
	}
	var params map[string]any
	if json.Unmarshal(d.InputSchema, &params) != nil {
		return "", nil, ErrInvalid
	}
	return Bind(d.Name, params, raw, false)
}
