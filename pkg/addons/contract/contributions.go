// SPDX-License-Identifier: MIT
package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
)

// Host slot identifiers. The list is closed: adding a slot is a host change
// with its own review, never an addon declaration.
const (
	SlotChatComposerActions = "chat.composer.actions"
	SlotChatMessageActions  = "chat.message.actions"
	SlotAppNav              = "app.nav"
	SlotSettingsSections    = "settings.sections"
)

// HostSlots are the slot identifiers a manifest may contribute to.
var HostSlots = []string{SlotChatComposerActions, SlotChatMessageActions, SlotAppNav, SlotSettingsSections}

// Chat action placements. Each one is a host slot of its own.
const (
	PlacementComposer = "composer"
	PlacementMessage  = "message"
)

// Renderer kinds an addon may declare for one of its own tools.
const (
	RendererApprovalCard = "approval-card"
	RendererToolResult   = "tool-result"
)

// Bounds of the declarations of one addon.
const (
	MaxChatActions = 16
	MaxSlots       = 16
	MaxRenderers   = 16
	MaxLabelChars  = 60
)

// IconNames are the semantic icon names of the host Icon allowlist. The host
// SDK (core/http/react-ui/src/addons/sdk.js) carries the same list; a test
// keeps both equal.
var IconNames = []string{
	"puzzle", "file", "file-text", "folder", "table", "database", "search", "plus", "minus", "close", "check",
	"check-circle", "alert-circle", "warning", "info", "edit", "trash", "download", "upload", "refresh",
	"settings", "list", "grid", "chart-line", "clock", "calendar", "tag", "star", "link", "external-link",
	"copy", "eye", "lock", "user", "users", "message", "sparkles", "chevron-down", "chevron-up", "chevron-right",
}

// componentName is the export name of a component of the addon module: a
// PascalCase identifier, so it can never be a prototype key or a host name.
var componentName = regexp.MustCompile(`^[A-Z][A-Za-z0-9]{0,63}$`)

// ValidComponentName reports whether s may name an exported component.
func ValidComponentName(s string) bool { return componentName.MatchString(s) }

// ChatTarget is what a chat action triggers: exactly one of Tool (a tool the
// addon declares) and Component (a component the addon module exports).
type ChatTarget struct {
	Tool      string `json:"tool,omitempty"`
	Component string `json:"component,omitempty"`
}

// ChatAction is one chat action. A bare identifier (the form of the first
// manifest version) declares only the ID and is never drawn: it has no
// placement. A full declaration has a label, an icon, a placement and a target.
type ChatAction struct {
	ID        string
	Label     *Localized
	Icon      string
	Placement string
	Target    *ChatTarget
}

type chatActionWire struct {
	ID        string      `json:"id"`
	Label     *Localized  `json:"label,omitempty"`
	Icon      string      `json:"icon,omitempty"`
	Placement string      `json:"placement,omitempty"`
	Target    *ChatTarget `json:"target,omitempty"`
}

// Declared reports whether the action is a full declaration.
func (a ChatAction) Declared() bool {
	return a.Label != nil || a.Icon != "" || a.Placement != "" || a.Target != nil
}

func (a ChatAction) MarshalJSON() ([]byte, error) {
	if !a.Declared() {
		return json.Marshal(a.ID)
	}
	return json.Marshal(chatActionWire{ID: a.ID, Label: a.Label, Icon: a.Icon, Placement: a.Placement, Target: a.Target})
}

func (a *ChatAction) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var id string
		if e := json.Unmarshal(b, &id); e != nil {
			return e
		}
		*a = ChatAction{ID: id}
		return nil
	}
	var w chatActionWire
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(&w); e != nil {
		return e
	}
	*a = ChatAction{ID: w.ID, Label: w.Label, Icon: w.Icon, Placement: w.Placement, Target: w.Target}
	return nil
}

// SlotContribution places one exported component in a host slot.
type SlotContribution struct {
	ID        string `json:"id"`
	Slot      string `json:"slot"`
	Component string `json:"component"`
}

// Renderer draws the approval card or the result of one of the addon's own
// tools. The host owns the frame around it.
type Renderer struct {
	Kind      string `json:"kind"`
	Tool      string `json:"tool"`
	Component string `json:"component"`
}

// ChatActionIDs returns the identifiers of every declared chat action.
func (u UI) ChatActionIDs() []string {
	out := make([]string, 0, len(u.ChatActions))
	for _, a := range u.ChatActions {
		out = append(out, a.ID)
	}
	return out
}

func validIconName(name string) bool { return slices.Contains(IconNames, name) }

// validateContributions checks the chat actions, slots and renderers of one
// UI declaration against the addon's own tools.
func validateContributions(u *UI, tools []string) error {
	if len(u.ChatActions) > MaxChatActions || len(u.Slots) > MaxSlots || len(u.Renderers) > MaxRenderers {
		return fmt.Errorf("too many UI contributions")
	}
	own := map[string]bool{}
	for _, t := range tools {
		own[t] = true
	}
	ids := map[string]bool{}
	for _, a := range u.ChatActions {
		if !namespace.MatchString(a.ID) || ids[a.ID] {
			return fmt.Errorf("invalid or duplicate chat action")
		}
		ids[a.ID] = true
		if !a.Declared() {
			continue
		}
		if a.Label == nil || !a.Label.valid(MaxLabelChars) {
			return fmt.Errorf("invalid chat action label")
		}
		if a.Placement != PlacementComposer && a.Placement != PlacementMessage {
			return fmt.Errorf("unknown chat action placement")
		}
		if !validIconName(a.Icon) && (a.Icon == "" || a.Icon != u.Icon) {
			return fmt.Errorf("chat action icon must be a host icon name or the packaged icon")
		}
		t := a.Target
		if t == nil || (t.Tool == "") == (t.Component == "") {
			return fmt.Errorf("chat action needs exactly one target")
		}
		if t.Tool != "" && !own[t.Tool] {
			return fmt.Errorf("chat action targets a tool the addon does not declare")
		}
		if t.Component != "" && !componentName.MatchString(t.Component) {
			return fmt.Errorf("invalid chat action component name")
		}
	}
	for _, s := range u.Slots {
		if !namespace.MatchString(s.ID) || ids[s.ID] {
			return fmt.Errorf("invalid or duplicate slot contribution")
		}
		ids[s.ID] = true
		if !slices.Contains(HostSlots, s.Slot) {
			return fmt.Errorf("unknown host slot")
		}
		if !componentName.MatchString(s.Component) {
			return fmt.Errorf("invalid slot component name")
		}
	}
	renderers := map[string]bool{}
	for _, r := range u.Renderers {
		if r.Kind != RendererApprovalCard && r.Kind != RendererToolResult {
			return fmt.Errorf("unknown renderer kind")
		}
		if !own[r.Tool] {
			return fmt.Errorf("renderer for a tool the addon does not declare")
		}
		if !componentName.MatchString(r.Component) {
			return fmt.Errorf("invalid renderer component name")
		}
		key := r.Kind + "\x00" + r.Tool
		if renderers[key] {
			return fmt.Errorf("duplicate renderer")
		}
		renderers[key] = true
	}
	return nil
}
