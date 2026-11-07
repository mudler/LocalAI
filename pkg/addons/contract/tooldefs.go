// SPDX-License-Identifier: MIT
package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
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
	// ToolKindAction runs under the act action and changes things at once, as
	// the calling user, with no browser confirmation: the add-on does what a
	// person asked a chat to do, in that person's own space, and the change is
	// undone from the history of the space. It names no resource. The host
	// offers it to the chat consumer only; every other consumer neither lists
	// nor runs it. An add-on that declares one must say so in its
	// capabilities (actions).
	ToolKindAction = "action"
)

// ApprovalAlways is the value of ToolDefinition.Approval that makes the host
// keep a proposal for a person to confirm, whatever the write mode of the
// binding says.
const ApprovalAlways = "always"

// The two answers of the user setting that relaxes ApprovalAlways for a tool
// that declares ApprovalOverridable (see ApprovalSettingKey).
const (
	// ApprovalAsk is the default: a person confirms every call.
	ApprovalAsk = "ask"
	// ApprovalSkip is the owner's choice to run the tool without a
	// confirmation, where the host allows it (see ToolDefinition).
	ApprovalSkip = "skip"
)

// ApprovalSettingKey is the key of the user setting that holds the person's
// choice for an ApprovalOverridable tool: "approval-" and the tool name.
func ApprovalSettingKey(tool string) string { return "approval-" + tool }

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
	// Approval, when it is ApprovalAlways, is a ceiling the person cannot lift:
	// the host stores a proposal for a write tool of this kind and applies it
	// only when a person confirms it at a host surface. A chat or an agent
	// never applies it, even when the person allowed writes without asking.
	// Only a write tool may set it.
	Approval string `json:"approval,omitempty"`
	// ApprovalOverridable lets the person relax Approval to "ask never" for
	// this tool, and nothing else. The choice is a user setting that the
	// manifest declares under ApprovalSettingKey(Name), with the values
	// ApprovalAsk and ApprovalSkip and the default ApprovalAsk, so the answer
	// is the person's own, written from their own browser session only. The
	// host honors ApprovalSkip only for a call that the person makes in their
	// own chat, or that a sender paired with the person's bot makes in a
	// messaging chat. Every other caller, and every call without a chat,
	// still waits for a confirmation. Only a tool with Approval "always" may
	// set it.
	ApprovalOverridable bool `json:"approvalOverridable,omitempty"`
	// UIOnly marks a tool that only the add-on's own page may call, such as a
	// settings change that asks the host whether the caller is an
	// administrator. It is never offered to a model, a chat or an agent. The
	// page has no resource to name, so it must be a read tool without one.
	UIOnly bool `json:"uiOnly,omitempty"`
	// Enumerates marks a read tool that lists what the credential reaches, such
	// as the repositories of a token. It names no resource, so the host cannot
	// hold it to the person's list: it runs only for a binding whose list is
	// the wildcard * (see ConnectorKind.WildcardResources), and is refused for
	// any other.
	Enumerates bool `json:"enumerates,omitempty"`
	// Shadows names a built-in chat tool that this tool replaces. When the
	// add-on is installed, running and on for a chat, the model sees one tool
	// under that name, served by the add-on, and the built-in one is hidden.
	// Only a name in ReplaceableTools may be shadowed. A tool without it keeps
	// its add-on name (addon__<id>__<tool>).
	Shadows string `json:"shadows,omitempty"`
	// Execution declares that this tool runs commands and what the host may
	// say about the provider. Only a tool that shadows bash may declare it.
	Execution *ExecutionDescriptor `json:"execution,omitempty"`
	// Title is the name of the tool in plain words ("Open an issue"), for a
	// person who reads that a call waits for them in a chat. Only a write,
	// schema or create tool may set it. A tool without one is named by its
	// tool name.
	Title string `json:"title,omitempty"`
	// SummaryFields name keys of the sample that the add-on's preview returns
	// (the facts it shows the person who confirms). The host may repeat the
	// scalar values of those keys, cut short, in the chat where a call waits
	// for a confirmation. Only the add-on knows which facts are safe to show
	// there; a key that is not listed is never shown in a chat. At most
	// MaxSummaryFields keys.
	SummaryFields []string `json:"summaryFields,omitempty"`
}

// ReplaceableTool says what a tool that shadows a built-in chat tool must look
// like, so that the model calls the replacement the way it calls the built-in
// and the user keeps the consent split of the built-in name.
type ReplaceableTool struct {
	// Kind is the kind the shadowing tool must have: an action tool for bash
	// (chats only, capabilities.actions), a read tool for the web tools.
	Kind string
	// Required are the properties the built-in requires, with their JSON
	// type. The shadowing tool declares each of them with that type and
	// requires no other property; extra optional properties are allowed.
	Required map[string]string
}

// ReplaceableTools are the built-in chat tools an add-on tool may shadow.
// The file tools are not on the list: they are the one file toolset of a
// chat, with Undo cards and the shared-folder rules, and an add-on must not
// take them over.
var ReplaceableTools = map[string]ReplaceableTool{
	"bash":       {Kind: ToolKindAction, Required: map[string]string{"command": "string"}},
	"web_search": {Kind: ToolKindRead, Required: map[string]string{"query": "string"}},
	"web_fetch":  {Kind: ToolKindRead, Required: map[string]string{"url": "string"}},
}

// Replaceable reports whether an add-on tool may shadow the built-in tool
// name.
func Replaceable(name string) bool {
	_, ok := ReplaceableTools[name]
	return ok
}

// ValidateShadow checks a tool that shadows a built-in chat tool against
// ReplaceableTools: the name is replaceable, the kind is the built-in's, the
// schema declares every property the built-in requires with its type, it
// requires no other property, and it names no resource. It is nil for a tool
// that shadows nothing.
func ValidateShadow(d ToolDefinition) error {
	if d.Shadows == "" {
		return nil
	}
	r, ok := ReplaceableTools[d.Shadows]
	if !ok {
		return fmt.Errorf("tool %s: %q is not a built-in tool an add-on may shadow", d.Name, d.Shadows)
	}
	if d.UIOnly {
		return fmt.Errorf("tool %s: a page-only tool cannot shadow a chat tool", d.Name)
	}
	if d.Kind != r.Kind {
		return fmt.Errorf("tool %s: a tool that shadows %s must be a %s tool", d.Name, d.Shadows, r.Kind)
	}
	var root struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(d.InputSchema, &root); err != nil {
		return fmt.Errorf("tool %s: invalid arguments schema", d.Name)
	}
	if _, has := root.Properties[ResourceArgument]; has {
		return fmt.Errorf("tool %s: a tool that shadows %s names no %s", d.Name, d.Shadows, ResourceArgument)
	}
	for name, typ := range r.Required {
		p, ok := root.Properties[name]
		if !ok || p.Type != typ {
			return fmt.Errorf("tool %s: a tool that shadows %s declares %s as a %s", d.Name, d.Shadows, name, typ)
		}
	}
	for _, name := range root.Required {
		if _, ok := r.Required[name]; !ok {
			return fmt.Errorf("tool %s: a tool that shadows %s requires %s, which the built-in does not", d.Name, d.Shadows, name)
		}
	}
	return nil
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
	return validateToolDefinitionsFor(defs, tools, false)
}

// validateToolDefinitionsFor is validateToolDefinitions for an add-on that does
// or does not declare a credential slot.
//
// credentialed says that the add-on declares a credential slot. A write tool
// of such an add-on names its resource with the slot argument and not with
// resourceId, so its schema is checked with ValidateCredentialedToolSchema.
func validateToolDefinitionsFor(defs []ToolDefinition, tools []string, credentialed bool) error {
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
	shadowed := map[string]bool{}
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
		case ToolKindRead, ToolKindWrite, ToolKindSchema, ToolKindCreate, ToolKindAction:
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
		if d.Approval != "" && (d.Approval != ApprovalAlways || d.Kind != ToolKindWrite) {
			return fmt.Errorf("tool %s: approval is %q for a write tool only", d.Name, ApprovalAlways)
		}
		if err := d.validateDisplay(); err != nil {
			return err
		}
		if d.ApprovalOverridable && d.Approval != ApprovalAlways {
			return fmt.Errorf("tool %s: approvalOverridable needs approval %q", d.Name, ApprovalAlways)
		}
		check := ValidateToolSchema
		if credentialed {
			check = ValidateCredentialedToolSchema
		}
		if err := check(d.InputSchema, d.Kind); err != nil {
			return fmt.Errorf("tool %s: %w", d.Name, err)
		}
		if d.UIOnly && !IsResourceless(d) {
			return fmt.Errorf("tool %s: a page-only tool must be a read tool without a resource", d.Name)
		}
		if err := d.Execution.validate(d); err != nil {
			return err
		}
		if d.Shadows != "" {
			if err := ValidateShadow(d); err != nil {
				return err
			}
			if shadowed[d.Shadows] {
				return fmt.Errorf("tool %s: two tools shadow %s", d.Name, d.Shadows)
			}
			shadowed[d.Shadows] = true
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
	return validateToolSchema(raw, kind, false)
}

// ValidateCredentialedToolSchema is ValidateToolSchema for a tool of an add-on
// that declares a credential slot. A write tool there needs no resourceId: it
// names its resource with the argument of the slot, which the host checks
// against the list of the person's binding (see validateCredentials).
func ValidateCredentialedToolSchema(raw json.RawMessage, kind string) error {
	return validateToolSchema(raw, kind, true)
}

// ValidateDefinitionSchema checks the schema of an installed tool definition
// again at the point of use. A write tool is also accepted in the form of a
// credentialed add-on, since the caller may not know the manifest. The host
// refused a write tool without a resource argument when the add-on was
// installed, and the broker refuses a write call that has no resource.
func ValidateDefinitionSchema(d ToolDefinition) error {
	err := ValidateToolSchema(d.InputSchema, d.Kind)
	if err != nil && d.Kind == ToolKindWrite {
		return ValidateCredentialedToolSchema(d.InputSchema, d.Kind)
	}
	return err
}

func validateToolSchema(raw json.RawMessage, kind string, credentialed bool) error {
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
	case credentialed && kind == ToolKindWrite:
		// The slot argument is the resource; validateCredentials holds the
		// tool to it.
	case kind != ToolKindCreate && kind != ToolKindRead && kind != ToolKindAction && !has:
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

// ShadowedTools lists, in definition order, the built-in chat tools that the
// tool definitions replace.
func ShadowedTools(defs []ToolDefinition) []string {
	var out []string
	for _, d := range defs {
		if d.Shadows != "" && ValidateShadow(d) == nil {
			out = append(out, d.Shadows)
		}
	}
	return out
}

// Bounds of the display fields of a tool definition.
const (
	MaxToolTitleLen = 60
	// MaxSummaryFields bounds SummaryFields.
	MaxSummaryFields = 6
	// MaxFactRunes bounds one fact value that the host repeats in a chat.
	MaxFactRunes = 120
)

var summaryFieldRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`)

func (d ToolDefinition) validateDisplay() error {
	if d.Title == "" && len(d.SummaryFields) == 0 {
		return nil
	}
	if d.Kind != ToolKindWrite && d.Kind != ToolKindSchema && d.Kind != ToolKindCreate {
		return fmt.Errorf("tool %s: title and summaryFields are for a write, schema or create tool", d.Name)
	}
	if d.Title != "" && !plainText(d.Title, MaxToolTitleLen) {
		return fmt.Errorf("tool %s: invalid title", d.Name)
	}
	if len(d.SummaryFields) > MaxSummaryFields {
		return fmt.Errorf("tool %s: at most %d summaryFields", d.Name, MaxSummaryFields)
	}
	seen := map[string]bool{}
	for _, f := range d.SummaryFields {
		if !summaryFieldRE.MatchString(f) || seen[f] {
			return fmt.Errorf("tool %s: invalid or repeated summaryFields entry", d.Name)
		}
		seen[f] = true
	}
	return nil
}

// Fact is one line of what a call would do, for a person who reads it in a
// chat: the name of the key of the preview and its value as short text.
type Fact struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// DisplayFacts picks, from the sample of a preview, the facts that the tool
// definition lists in SummaryFields, in that order. Only a string, a number, a
// boolean or a list of those is shown; an object, a null or an empty value is
// skipped, so no raw JSON reaches a chat. Each value is one line of at most
// MaxFactRunes runes, with the characters that the host never repeats
// (controls, angle brackets) replaced by spaces.
func DisplayFacts(d ToolDefinition, sample json.RawMessage) []Fact {
	if len(d.SummaryFields) == 0 || len(sample) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(sample, &m) != nil {
		return nil
	}
	var out []Fact
	for _, k := range d.SummaryFields {
		if v := factValue(m[k]); v != "" {
			out = append(out, Fact{Key: k, Value: v})
		}
	}
	return out
}

func factValue(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var one any
	if json.Unmarshal(raw, &one) != nil {
		return ""
	}
	var parts []string
	scalar := func(v any) (string, bool) {
		switch x := v.(type) {
		case string:
			return x, true
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64), true
		case bool:
			return strconv.FormatBool(x), true
		}
		return "", false
	}
	if list, ok := one.([]any); ok {
		for _, e := range list {
			s, ok := scalar(e)
			if !ok {
				return ""
			}
			parts = append(parts, s)
		}
	} else if s, ok := scalar(one); ok {
		parts = []string{s}
	}
	text := strings.Join(parts, ", ")
	text = strings.Map(func(r rune) rune {
		if r == '<' || r == '>' || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) > MaxFactRunes {
		text = string([]rune(text)[:MaxFactRunes]) + "…"
	}
	return text
}
