// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"fmt"
	"slices"
)

const (
	MaxHomeCommands            = 8
	MaxExecutionTools          = 8
	MaxRequestedHostOperations = 32
)

// HomeCommand contributes a declared page, never a tool or an external URL.
// Empty Page resolves to UI.MainPage. Alias requests a name, not ownership.
type HomeCommand struct {
	ID    string     `json:"id"`
	Alias string     `json:"alias,omitempty"`
	Page  string     `json:"page,omitempty"`
	Label *Localized `json:"label,omitempty"`
	Icon  string     `json:"icon,omitempty"`
}

// UnmarshalJSON keeps omission distinct from an explicit empty or null
// override. Only omission selects the installed display/destination defaults.
func (c *HomeCommand) UnmarshalJSON(b []byte) error {
	type wire HomeCommand
	var v wire
	fields, err := exactObjectFields(b, DefaultLimits().ManifestBytes, "id", "alias", "page", "label", "icon")
	if err != nil {
		return err
	}
	if err := strictJSON(b, &v, DefaultLimits().ManifestBytes); err != nil {
		return err
	}
	for key, value := range map[string]string{"alias": v.Alias, "page": v.Page, "icon": v.Icon} {
		if _, present := fields[key]; present && value == "" {
			return fmt.Errorf("home command %s must be nonempty when present", key)
		}
	}
	if _, present := fields["label"]; present && v.Label == nil {
		return fmt.Errorf("home command label must not be null")
	}
	*c = HomeCommand(v)
	return nil
}

// ExecutionProvider binds observations to these action tools only. It grants
// neither shadowing authority nor permission to execute commands.
type ExecutionProvider struct {
	Tools      []string `json:"tools"`
	StatusTool string   `json:"statusTool"`
}

// HostOperations declares requests, never host grants. Omission preserves
// legacy/unspecified requests; an explicitly empty declaration is invalid.
type HostOperations struct {
	Required []string `json:"required,omitempty"`
	Optional []string `json:"optional,omitempty"`
}

// IsExecutionAction reports declaration facts only, never execution authority.
func IsExecutionAction(c Capabilities, d ToolDefinition) bool {
	return slices.Contains(c.Tools, d.Name) && d.Kind == ToolKindAction && !d.UIOnly && d.Shadows == "bash"
}

// IsExecutionStatusTool reports the status declaration shape, not read authority.
// Schema acceptance is checked separately to preserve manifest diagnostics.
func IsExecutionStatusTool(c Capabilities, d ToolDefinition) bool {
	return slices.Contains(c.Tools, d.Name) && !slices.Contains(c.AdminTools, d.Name) && d.Kind == ToolKindRead && d.UIOnly && IsResourceless(d)
}

// ExecutionStatusAcceptsEmptyArguments applies the manifest schema rules even
// when a capability source supplies raw definitions without parsing a manifest.
func ExecutionStatusAcceptsEmptyArguments(d ToolDefinition) bool {
	if ValidateToolSchema(d.InputSchema, ToolKindRead) != nil {
		return false
	}
	var schema struct {
		Required []string        `json:"required"`
		Enum     json.RawMessage `json:"enum"`
	}
	// Allowed constraints otherwise apply to absent properties or non-object types.
	return json.Unmarshal(d.InputSchema, &schema) == nil && len(schema.Required) == 0 && len(schema.Enum) == 0
}

func validateExtensions(c Capabilities) error {
	if u := c.UI; u != nil {
		if err := ValidateModelActions(u); err != nil {
			return err
		}
		if len(u.HomeCommands) > MaxHomeCommands {
			return fmt.Errorf("too many home commands")
		}
		seen := map[string]bool{}
		for _, command := range u.HomeCommands {
			if !ValidName(command.ID) || seen[command.ID] {
				return fmt.Errorf("invalid or duplicate home command")
			}
			seen[command.ID] = true
			if command.Alias != "" && !ValidName(command.Alias) {
				return fmt.Errorf("invalid home command alias")
			}
			page := command.Page
			if page == "" {
				page = u.MainPage
			}
			if page == "" || !slices.Contains(u.Pages, page) {
				return fmt.Errorf("home command requires a declared page or mainPage")
			}
			if command.Label != nil && !command.Label.valid(MaxLabelChars) {
				return fmt.Errorf("invalid home command label")
			}
			if command.Icon != "" && !validIconName(command.Icon) && command.Icon != u.Icon {
				return fmt.Errorf("home command icon must be a host icon name or the packaged icon")
			}
		}
	}
	if p := c.ExecutionProvider; p != nil {
		if len(p.Tools) == 0 || len(p.Tools) > MaxExecutionTools || !uniqueIDs(p.Tools) || !ValidName(p.StatusTool) || slices.Contains(p.Tools, p.StatusTool) {
			return fmt.Errorf("invalid execution provider bindings")
		}
		defs := map[string]ToolDefinition{}
		for _, d := range c.ToolDefinitions {
			defs[d.Name] = d
		}
		for _, name := range p.Tools {
			d, ok := defs[name]
			if !ok || !IsExecutionAction(c, d) {
				return fmt.Errorf("execution provider must bind declared action tools shadowing bash")
			}
		}
		d, ok := defs[p.StatusTool]
		if !ok || !IsExecutionStatusTool(c, d) {
			return fmt.Errorf("execution status must be a separate page-only resourceless read tool")
		}
		if !ExecutionStatusAcceptsEmptyArguments(d) {
			return fmt.Errorf("execution status must accept empty arguments")
		}
	}
	if ops := c.HostOperations; ops != nil {
		n := len(ops.Required) + len(ops.Optional)
		if n == 0 || n > MaxRequestedHostOperations {
			return fmt.Errorf("host operations require 1 to %d requests", MaxRequestedHostOperations)
		}
		seen := map[string]bool{}
		for _, list := range [][]string{ops.Required, ops.Optional} {
			for _, name := range list {
				if !ValidHostOp(name) || seen[name] {
					return fmt.Errorf("invalid or duplicate host operation request")
				}
				seen[name] = true
			}
		}
	}
	return nil
}
