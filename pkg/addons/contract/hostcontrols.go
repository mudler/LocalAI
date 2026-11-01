// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"slices"
	"strings"
)

// A host control is a component the host ships and draws itself, bound to
// host settings, that an add-on asks for by ID in ui.controls. The add-on
// supplies no code, value or label for it. The registry is closed: a new
// control is a host change with its own review, like a new slot. The React UI
// keeps the same IDs in src/addons/hostControls.js, and a test keeps both
// lists equal.
const (
	// ControlNetworkPolicy is the network of the commands an add-on runs: the
	// administrator's settings on the add-on's Settings tab, the Network row
	// of the chat Tools menu and the administrator link of a network card.
	ControlNetworkPolicy = "network-policy"
	// ControlToolProviders is the Settings > Tools rows of the built-in tools
	// an add-on replaces, on the add-on's Settings tab.
	ControlToolProviders = "tool-providers"
)

// AnyShadow in HostControl.Shadows means a tool that shadows any built-in.
const AnyShadow = "*"

// HostControl is one entry of the registry: what an add-on must hold for the
// host to draw it. The manifest declares a control; the host decides, from
// its own setting and the verified manifest, whether it is drawn.
type HostControl struct {
	ID string
	// Grant is a host operation the add-on must be granted (the host setting
	// of addon-id=op-prefix pairs), or "" for none.
	Grant string
	// Shadows is a built-in tool the add-on must replace with a valid tool,
	// AnyShadow for any, or "" for none.
	Shadows string
}

// HostControls is the closed registry of host controls.
var HostControls = []HostControl{
	{ID: ControlNetworkPolicy, Grant: "egress.check", Shadows: "bash"},
	{ID: ControlToolProviders, Shadows: AnyShadow},
}

// MaxControls bounds ui.controls.
const MaxControls = 8

// HostControlIDs returns the IDs of the registry, in order.
func HostControlIDs() []string {
	out := make([]string, 0, len(HostControls))
	for _, c := range HostControls {
		out = append(out, c.ID)
	}
	return out
}

func hostControl(id string) (HostControl, bool) {
	for _, c := range HostControls {
		if c.ID == id {
			return c, true
		}
	}
	return HostControl{}, false
}

// validateControls checks the shape of ui.controls: known IDs, no repeat,
// within the bound. Entitlement is not checked here: it depends on a host
// setting, so it is decided each time the host lists the add-on.
func validateControls(ids []string) error {
	if len(ids) > MaxControls {
		return fmt.Errorf("too many host controls")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if _, ok := hostControl(id); !ok || seen[id] {
			return fmt.Errorf("unknown or repeated host control")
		}
		seen[id] = true
	}
	return nil
}

// grantCovers reports whether one of the granted operation prefixes covers
// op, the way the host operation broker matches them.
func grantCovers(grants []string, op string) bool {
	for _, p := range grants {
		if p != "" && strings.HasPrefix(op, p) {
			return true
		}
	}
	return false
}

// EntitledControls returns the controls of the manifest that the host draws
// for it: those declared in ui.controls whose grant is among grants (the
// operation prefixes the host setting gives this add-on) and whose built-in
// tool the add-on replaces with a valid tool. The order of the manifest is
// kept. A control the add-on is not entitled to is dropped, never an error:
// an administrator can remove a grant at any time.
func EntitledControls(m Manifest, grants []string) []string {
	u := m.Capabilities.UI
	if u == nil || len(u.Controls) == 0 {
		return nil
	}
	shadows := ShadowedTools(m.Capabilities.ToolDefinitions)
	var out []string
	for _, id := range u.Controls {
		c, ok := hostControl(id)
		if !ok || slices.Contains(out, id) {
			continue
		}
		if c.Grant != "" && !grantCovers(grants, c.Grant) {
			continue
		}
		switch c.Shadows {
		case "":
		case AnyShadow:
			if len(shadows) == 0 {
				continue
			}
		default:
			if !slices.Contains(shadows, c.Shadows) {
				continue
			}
		}
		out = append(out, id)
	}
	return out
}
