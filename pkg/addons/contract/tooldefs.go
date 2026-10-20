// SPDX-License-Identifier: MIT
package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Tool kinds. The kind says which host authority an add-on tool runs under; it
// is the add-on's declaration, and the host still checks the caller's role
// for that authority on every call.
const (
	// ToolKindRead runs under the read action and returns data.
	ToolKindRead = "read"
	// ToolKindWrite proposes a data change under the write action.
	ToolKindWrite = "write"
	// ToolKindSchema proposes a schema change under the schema action.
	ToolKindSchema = "schema"
	// ToolKindCreate proposes the creation of a resource. It names no
	// resource: the host reserves one for the proposer.
	ToolKindCreate = "create"
)

// ResourceArgument is the tool argument that names the resource a tool works
// on. The host takes it out of the arguments and binds it to the request, so
// the add-on never reads it from the arguments.
const ResourceArgument = "resourceId"

// Bounds of the tool definitions of one manifest.
const (
	MaxToolDefinitions     = 32
	MaxToolDescriptionLen  = 400
	MaxToolSchemaBytes     = 8 << 10
	MaxToolDefinitionBytes = 32 << 10
	maxToolSchemaDepth     = 6
	maxToolProperties      = 32
	maxToolEnum            = 32
)

// ToolDefinition describes one tool the add-on offers to a model. It is
// served by the host from the verified manifest: the model sees this schema
// and nothing the add-on process says at run time.
type ToolDefinition struct {
	// Name is one of capabilities.tools.
	Name string `json:"name"`
	// Description is plain text for the model.
	Description string `json:"description"`
	// Kind is read, write, schema or create.
	Kind string `json:"kind"`
	// InputSchema is the JSON schema of the arguments object. It is an
	// object schema with explicit properties, additionalProperties false at
	// the top level and the keywords the host allows only.
	InputSchema json.RawMessage `json:"inputSchema" swaggertype:"object"`
}

var toolPropertyName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,63}$`)

// forbiddenToolProperties are argument names that stand for identity,
// authority or approval. A schema never declares them, and the host refuses
// them in a call, whatever the schema says.
var forbiddenToolProperties = map[string]bool{
	"tool": true, "actor": true, "user": true, "userid": true, "credential": true, "credentialid": true,
	"role": true, "mode": true, "scope": true, "authority": true, "approved": true, "approve": true,
	"approval": true, "confirm": true, "confirmed": true, "confirmation": true, "digest": true,
	"proposal": true, "proposalid": true, "operation": true, "operationid": true, "generation": true,
	"addon": true, "addonid": true, "action": true, "owner": true, "grant": true, "share": true,
	"admin": true, "session": true, "token": true, "apikey": true, "force": true, "yes": true,
}

// ForbiddenToolProperty reports whether name is an argument name that no
// tool may declare or receive. The comparison ignores case and the
// separators - and _.
func ForbiddenToolProperty(name string) bool {
	k := strings.ToLower(strings.TrimSpace(name))
	k = strings.NewReplacer("-", "", "_", "", ".", "").Replace(k)
	return forbiddenToolProperties[k]
}

var schemaKeywords = map[string]bool{
	"type": true, "properties": true, "required": true, "items": true, "enum": true, "description": true,
	"minimum": true, "maximum": true, "minLength": true, "maxLength": true, "minItems": true, "maxItems": true,
	"additionalProperties": true, "default": true,
}

var schemaTypes = map[string]bool{"object": true, "string": true, "integer": true, "number": true, "boolean": true, "array": true}

// validateToolDefinitions checks the definitions against the declared tools.
func validateToolDefinitions(defs []ToolDefinition, tools []string) error {
	if len(defs) == 0 {
		return nil
	}
	if len(defs) > MaxToolDefinitions {
		return fmt.Errorf("too many tool definitions")
	}
	declared := map[string]bool{}
	for _, t := range tools {
		declared[t] = true
	}
	seen := map[string]bool{}
	total := 0
	for _, d := range defs {
		if !declared[d.Name] || seen[d.Name] {
			return fmt.Errorf("tool definition names an undeclared or repeated tool")
		}
		seen[d.Name] = true
		if !plainText(d.Description, MaxToolDescriptionLen) {
			return fmt.Errorf("invalid tool description")
		}
		switch d.Kind {
		case ToolKindRead, ToolKindWrite, ToolKindSchema, ToolKindCreate:
		default:
			return fmt.Errorf("invalid tool kind")
		}
		if len(d.InputSchema) == 0 || len(d.InputSchema) > MaxToolSchemaBytes {
			return fmt.Errorf("tool schema is missing or too large")
		}
		total += len(d.InputSchema) + len(d.Description)
		if total > MaxToolDefinitionBytes {
			return fmt.Errorf("tool definitions are too large")
		}
		if err := ValidateToolSchema(d.InputSchema, d.Kind); err != nil {
			return fmt.Errorf("tool %s: %w", d.Name, err)
		}
	}
	return nil
}

// ValidateToolSchema checks the arguments schema of one tool: a bounded
// object schema built only from the allowed keywords, with no identity or
// approval property. A tool that works on a resource names it with the
// resourceId argument; a create tool does not, and a read tool may leave it
// out (see IsResourceless).
func ValidateToolSchema(raw json.RawMessage, kind string) error {
	if err := strictJSON(raw, new(map[string]json.RawMessage), MaxToolSchemaBytes); err != nil {
		return err
	}
	var root map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&root); err != nil {
		return err
	}
	if root["type"] != "object" {
		return fmt.Errorf("the arguments schema must be an object")
	}
	if v, ok := root["additionalProperties"]; !ok || v != false {
		return fmt.Errorf("the arguments schema must set additionalProperties to false")
	}
	if err := walkSchema(root, 0); err != nil {
		return err
	}
	props, _ := root["properties"].(map[string]any)
	_, has := props[ResourceArgument]
	// A create tool names no resource: the host reserves one. A read tool may
	// name none either, when it answers about the caller and not about a
	// resource; the host then scopes the result to the calling user. Every
	// other tool works on a resource and names it.
	switch {
	case kind == ToolKindCreate && has:
		return fmt.Errorf("%s is absent for a create tool", ResourceArgument)
	case kind != ToolKindCreate && kind != ToolKindRead && !has:
		return fmt.Errorf("%s is required for a tool of a resource", ResourceArgument)
	}
	req, _ := root["required"].([]any)
	required := map[string]bool{}
	for _, r := range req {
		s, ok := r.(string)
		if !ok {
			return fmt.Errorf("invalid required list")
		}
		if _, ok := props[s]; !ok {
			return fmt.Errorf("required names an undeclared property")
		}
		required[s] = true
	}
	if has && !required[ResourceArgument] {
		return fmt.Errorf("%s must be required", ResourceArgument)
	}
	return nil
}

func walkSchema(node map[string]any, depth int) error {
	if depth > maxToolSchemaDepth {
		return fmt.Errorf("schema nesting is too deep")
	}
	for k, v := range node {
		if !schemaKeywords[k] {
			return fmt.Errorf("schema keyword %q is not allowed", k)
		}
		switch k {
		case "type":
			s, ok := v.(string)
			if !ok || !schemaTypes[s] {
				return fmt.Errorf("invalid schema type")
			}
		case "description":
			s, ok := v.(string)
			if !ok || !plainText(s, MaxToolDescriptionLen) {
				return fmt.Errorf("invalid schema description")
			}
		case "enum":
			list, ok := v.([]any)
			if !ok || len(list) == 0 || len(list) > maxToolEnum {
				return fmt.Errorf("invalid enum")
			}
			for _, e := range list {
				if _, ok := e.(string); !ok {
					return fmt.Errorf("enum values must be strings")
				}
			}
		case "additionalProperties":
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("additionalProperties must be a boolean")
			}
		case "required":
			if _, ok := v.([]any); !ok {
				return fmt.Errorf("invalid required list")
			}
		case "properties":
			props, ok := v.(map[string]any)
			if !ok || len(props) > maxToolProperties {
				return fmt.Errorf("invalid properties")
			}
			for name, p := range props {
				if !toolPropertyName.MatchString(name) {
					return fmt.Errorf("invalid property name")
				}
				// Identity and approval names are refused at every depth: a
				// nested object is still read as arguments by the add-on.
				if ForbiddenToolProperty(name) {
					return fmt.Errorf("property %q is reserved", name)
				}
				child, ok := p.(map[string]any)
				if !ok {
					return fmt.Errorf("invalid property schema")
				}
				if err := walkSchema(child, depth+1); err != nil {
					return err
				}
			}
		case "items":
			child, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid items schema")
			}
			if err := walkSchema(child, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// IsResourceless reports a tool definition that names no resource: a read tool
// whose schema has no resourceId property. The host runs such a read without a
// resource and the add-on answers for the calling user only.
func IsResourceless(d ToolDefinition) bool {
	if d.Kind != ToolKindRead {
		return false
	}
	var root struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(d.InputSchema, &root) != nil {
		return false
	}
	_, has := root.Properties[ResourceArgument]
	return !has
}
