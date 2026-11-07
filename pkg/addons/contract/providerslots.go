// SPDX-License-Identifier: MIT
package contract

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"sync"
)

// Provider slots. A provider slot is a job the host does by itself and lets an
// add-on do in its place: the host owns the slot, its gates and its built-in
// provider; an add-on declares that it provides the slot
// (capabilities.provides) and the person picks one provider. The list of slots
// is data (provider-slots.json) and closed: only a host release adds a slot,
// and an add-on cannot invent one. The word "slot" also names the host slots
// of the UI (contributions.go) and the slots of skills (slots.go); this
// facility says provider slot everywhere.

// Bounds of a provider declaration.
const (
	// MaxProvides is the most slots one add-on provides.
	MaxProvides = 4
	// MaxProviderRoleTools is the most tools one role of a declaration names,
	// before the limit of the slot's own role.
	MaxProviderRoleTools = 8
)

// The values of ProviderSelection.Default and of a stored selection that are
// not add-on IDs.
const (
	// ProviderBuiltin selects the provider the host ships for the slot.
	ProviderBuiltin = "builtin"
	// ProviderOff selects no provider at all.
	ProviderOff = "off"
)

// The value types of a provider property.
const (
	PropertyBool = "bool"
	PropertyEnum = "enum"
)

// ProviderSlot is the host's description of one provider slot.
type ProviderSlot struct {
	ID string `json:"id"`
	// Version is the current version of the slot's contract. MinVersion is the
	// oldest one the host still accepts in a manifest.
	Version    int    `json:"version"`
	MinVersion int    `json:"minVersion"`
	Title      string `json:"title"`
	// Description is the English text shown in the list of providers.
	Description string              `json:"description"`
	Selection   ProviderSelection   `json:"selection"`
	Builtin     ProviderBuiltinInfo `json:"builtin"`
	// Aliases are add-on IDs that mean the built-in provider. An add-on whose
	// job the host took over keeps its ID here, so a copy that is still
	// installed, and a choice that names it, keep working without serving a
	// second time. The host lists no alias as a provider of its own.
	Aliases []string `json:"aliases,omitempty"`
	// Roles name what a provider can supply. A role maps to tools the add-on
	// declares in capabilities.tools.
	Roles map[string]ProviderRole `json:"roles"`
	// Contributions say which of the add-on's own declarations the slot
	// activates while the add-on is the selected provider.
	Contributions ProviderContributions `json:"contributions"`
	// Properties are the typed facts an add-on may declare about itself.
	Properties map[string]ProviderProperty `json:"properties,omitempty"`
	// Policy are ids of host rules applied to every provider of the slot. The
	// add-on cannot change or opt out of them.
	Policy []string `json:"policy,omitempty"`
}

// ProviderSelection says how the person chooses a provider.
type ProviderSelection struct {
	// Scope is "user": one choice per person.
	Scope string `json:"scope"`
	// Default is ProviderBuiltin.
	Default    string `json:"default"`
	OffAllowed bool   `json:"offAllowed"`
	// OffMeans is the sentence the UI shows for Off.
	OffMeans string `json:"offMeans,omitempty"`
}

// ProviderBuiltinInfo describes the provider the host ships.
type ProviderBuiltinInfo struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// ProviderRole is what a provider can supply for a slot.
type ProviderRole struct {
	// Kinds are the tool kinds the role accepts.
	Kinds    []string `json:"kinds"`
	Required bool     `json:"required,omitempty"`
	// Max is the most tools the role takes. Zero means one.
	Max int `json:"max,omitempty"`
}

// ProviderContributions are the declarations a slot activates by selection.
type ProviderContributions struct {
	// ContextFiles are the scopes of context files the slot allows.
	ContextFiles     []string `json:"contextFiles,omitempty"`
	SkillDefinitions bool     `json:"skillDefinitions,omitempty"`
}

// ProviderProperty is the type of one property a provider declares.
type ProviderProperty struct {
	Type   string   `json:"type"`
	Values []string `json:"values,omitempty"`
}

//go:embed provider-slots.json
var providerSlotsJSON []byte

var providerSlots = sync.OnceValue(func() []ProviderSlot {
	var out []ProviderSlot
	d := json.NewDecoder(bytes.NewReader(providerSlotsJSON))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		panic("contract: provider-slots.json: " + err.Error())
	}
	if err := validateProviderSlots(out); err != nil {
		panic("contract: provider-slots.json: " + err.Error())
	}
	return out
})

// ProviderSlots are the provider slots of this host, in file order.
func ProviderSlots() []ProviderSlot {
	src := providerSlots()
	out := make([]ProviderSlot, len(src))
	for i, s := range src {
		out[i] = s.clone()
	}
	return out
}

// ProviderSlotByID returns the slot called id.
func ProviderSlotByID(id string) (ProviderSlot, bool) {
	for _, s := range providerSlots() {
		if s.ID == id {
			return s.clone(), true
		}
	}
	return ProviderSlot{}, false
}

func (s ProviderSlot) clone() ProviderSlot {
	roles := make(map[string]ProviderRole, len(s.Roles))
	for k, r := range s.Roles {
		r.Kinds = slices.Clone(r.Kinds)
		roles[k] = r
	}
	s.Roles = roles
	props := make(map[string]ProviderProperty, len(s.Properties))
	for k, p := range s.Properties {
		p.Values = slices.Clone(p.Values)
		props[k] = p
	}
	s.Properties = props
	s.Contributions.ContextFiles = slices.Clone(s.Contributions.ContextFiles)
	s.Aliases = slices.Clone(s.Aliases)
	s.Policy = slices.Clone(s.Policy)
	return s
}

// IsAlias reports whether id is an add-on that the built-in provider of the
// slot stands in for.
func (s ProviderSlot) IsAlias(id string) bool { return slices.Contains(s.Aliases, id) }

// Canonical turns an alias into the built-in provider and leaves every other
// ID as it is.
func (s ProviderSlot) Canonical(id string) string {
	if s.IsAlias(id) {
		return ProviderBuiltin
	}
	return id
}

// RoleNames returns the role names of the slot, sorted.
func (s ProviderSlot) RoleNames() []string {
	out := make([]string, 0, len(s.Roles))
	for k := range s.Roles {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// validateProviderSlots checks the data file at start, so a malformed
// descriptor stops the host instead of weakening a check.
func validateProviderSlots(slots []ProviderSlot) error {
	seen := map[string]bool{}
	for _, s := range slots {
		if !ValidName(s.ID) || seen[s.ID] {
			return fmt.Errorf("invalid or duplicate slot id %q", s.ID)
		}
		seen[s.ID] = true
		if s.Version < 1 || s.MinVersion < 1 || s.MinVersion > s.Version {
			return fmt.Errorf("slot %s: invalid version range", s.ID)
		}
		if !plainText(s.Title, MaxTitleChars) || !plainText(s.Description, MaxDescriptionChars) {
			return fmt.Errorf("slot %s: invalid title or description", s.ID)
		}
		if s.Selection.Scope != "user" || s.Selection.Default != ProviderBuiltin && !(s.Selection.Default == ProviderOff && s.Selection.OffAllowed) {
			return fmt.Errorf("slot %s: invalid selection", s.ID)
		}
		if s.Selection.OffMeans != "" && !plainText(s.Selection.OffMeans, MaxDescriptionChars) {
			return fmt.Errorf("slot %s: invalid off text", s.ID)
		}
		if !ValidName(s.Builtin.ID) || s.Builtin.ID != ProviderBuiltin || !plainText(s.Builtin.Title, MaxTitleChars) || !plainText(s.Builtin.Description, MaxDescriptionChars) {
			return fmt.Errorf("slot %s: invalid built-in provider", s.ID)
		}
		for _, a := range s.Aliases {
			if !ValidName(a) || a == ProviderBuiltin || a == ProviderOff {
				return fmt.Errorf("slot %s: invalid alias %q", s.ID, a)
			}
		}
		for name, r := range s.Roles {
			if !ValidName(name) || len(r.Kinds) == 0 || r.Max < 0 || r.Max > MaxProviderRoleTools {
				return fmt.Errorf("slot %s: invalid role %q", s.ID, name)
			}
			for _, k := range r.Kinds {
				// A provider never brings a tool that changes things at once.
				if k != ToolKindRead && k != ToolKindWrite {
					return fmt.Errorf("slot %s: role %s: kind %q is not allowed", s.ID, name, k)
				}
			}
		}
		for _, sc := range s.Contributions.ContextFiles {
			if placeholderOf(ChangeScope(sc)) == "" && ChangeScope(sc) != ScopeUser {
				return fmt.Errorf("slot %s: unknown context scope %q", s.ID, sc)
			}
		}
		for name, p := range s.Properties {
			if !ValidName(name) || p.Type != PropertyBool && p.Type != PropertyEnum || p.Type == PropertyEnum && len(p.Values) == 0 {
				return fmt.Errorf("slot %s: invalid property %q", s.ID, name)
			}
		}
	}
	return nil
}

// ProviderDeclaration is the manifest's statement that the add-on provides a
// slot (capabilities.provides).
type ProviderDeclaration struct {
	// Slot names a slot of the host. The list is closed.
	Slot string `json:"slot"`
	// SlotVersion is the version of the slot's contract the add-on was
	// written for.
	SlotVersion int `json:"slotVersion"`
	// Roles map a role of the slot to tools of capabilities.tools.
	Roles map[string]ProviderTools `json:"roles,omitempty"`
	// Properties are typed facts the slot asks for: a boolean or one of the
	// listed values.
	Properties map[string]any `json:"properties,omitempty"`
}

// ProviderTools is the tool or the tools that serve one role. The manifest
// writes one name as a string and several as a list.
type ProviderTools []string

func (p *ProviderTools) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var one string
		if err := json.Unmarshal(b, &one); err != nil {
			return err
		}
		*p = ProviderTools{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("a role names a tool or a list of tools")
	}
	*p = many
	return nil
}

// ProvidedSlots returns the IDs of the slots the capabilities declare.
func (c Capabilities) ProvidedSlots() []string {
	out := make([]string, 0, len(c.Provides))
	for _, d := range c.Provides {
		out = append(out, d.Slot)
	}
	return out
}

// ProvidesSlot reports whether the capabilities declare the slot.
func (c Capabilities) ProvidesSlot(slot string) bool {
	return slices.Contains(c.ProvidedSlots(), slot)
}

// validateProvides checks every declaration against the host's descriptor of
// its slot. Nothing here names a slot: the rules come from the data.
func validateProvides(c Capabilities) error {
	if len(c.Provides) == 0 {
		return nil
	}
	if len(c.Provides) > MaxProvides {
		return fmt.Errorf("an add-on provides at most %d slots", MaxProvides)
	}
	defs := map[string]ToolDefinition{}
	for _, d := range c.ToolDefinitions {
		defs[d.Name] = d
	}
	scopes := map[string]bool{}
	seen := map[string]bool{}
	for _, d := range c.Provides {
		slot, ok := ProviderSlotByID(d.Slot)
		if !ok {
			return fmt.Errorf("unknown provider slot %q", d.Slot)
		}
		if seen[d.Slot] {
			return fmt.Errorf("provider slot %q is declared twice", d.Slot)
		}
		seen[d.Slot] = true
		if d.SlotVersion < slot.MinVersion || d.SlotVersion > slot.Version {
			return fmt.Errorf("provider slot %s: slotVersion must be %d to %d", d.Slot, slot.MinVersion, slot.Version)
		}
		used := map[string]string{}
		for name, tools := range d.Roles {
			role, ok := slot.Roles[name]
			if !ok {
				return fmt.Errorf("provider slot %s: unknown role %q", d.Slot, name)
			}
			limit := max(role.Max, 1)
			if len(tools) == 0 || len(tools) > limit {
				return fmt.Errorf("provider slot %s: role %s takes 1 to %d tools", d.Slot, name, limit)
			}
			for _, t := range tools {
				def, ok := defs[t]
				switch {
				case !ok || !slices.Contains(c.Tools, t):
					return fmt.Errorf("provider slot %s: role %s names %q, which is not a declared tool with a definition", d.Slot, name, t)
				case !slices.Contains(role.Kinds, def.Kind):
					return fmt.Errorf("provider slot %s: role %s needs a tool of kind %v, %q is %s", d.Slot, name, role.Kinds, t, def.Kind)
				case def.UIOnly || def.Shadows != "":
					return fmt.Errorf("provider slot %s: role %s: tool %q must be a plain model tool", d.Slot, name, t)
				case used[t] != "":
					return fmt.Errorf("provider slot %s: tool %q serves both %s and %s", d.Slot, t, used[t], name)
				}
				used[t] = name
			}
		}
		for name, role := range slot.Roles {
			if role.Required && len(d.Roles[name]) == 0 {
				return fmt.Errorf("provider slot %s: role %s is required", d.Slot, name)
			}
		}
		for key, v := range d.Properties {
			p, ok := slot.Properties[key]
			if !ok {
				return fmt.Errorf("provider slot %s: unknown property %q", d.Slot, key)
			}
			switch val := v.(type) {
			case bool:
				if p.Type != PropertyBool {
					return fmt.Errorf("provider slot %s: property %s must be one of %v", d.Slot, key, p.Values)
				}
			case string:
				if p.Type != PropertyEnum || !slices.Contains(p.Values, val) {
					return fmt.Errorf("provider slot %s: property %s has a value that is not allowed", d.Slot, key)
				}
			default:
				return fmt.Errorf("provider slot %s: property %s has an invalid value", d.Slot, key)
			}
		}
		// A provider that sends notes to a service says so, and a service is
		// reached over the network: the manifest must declare the network, so
		// the person and the host see the access before anything is sent.
		if v, _ := d.Properties["remote"].(bool); v && c.Network == nil {
			return fmt.Errorf("provider slot %s: remote true needs a network declaration (capabilities.network)", d.Slot)
		}
		// A provider that says its changes are undone by the add-on keeps the
		// change sets that the host's undo calls read.
		if v, _ := d.Properties["undo"].(string); v == "provider" && !c.Undo {
			return fmt.Errorf("provider slot %s: undo \"provider\" needs capabilities.undo", d.Slot)
		}
		for _, sc := range slot.Contributions.ContextFiles {
			scopes[sc] = true
		}
	}
	for _, f := range c.ContextFiles {
		if !scopes[string(f.Scope)] {
			return fmt.Errorf("context file scope %q is not allowed by the slots the add-on provides", f.Scope)
		}
	}
	return nil
}
