package studio

import (
	"context"
	"encoding/json"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Shadowing, by tool name. Some chat tools are built in and can be replaced by
// an add-on: the built-in bash runs commands on this machine, and the Sandbox
// add-on's bash runs them isolated. An add-on tool that declares
// shadows: "<name>" in its manifest takes the built-in's name when the add-on
// is installed, running and on for the chat: the model sees one tool, served by
// the add-on, and the built-in one is hidden. Only the names in
// contract.ReplaceableTools can be shadowed (not the file tools); the host
// checks that at install and the adapter and this code check it again. An
// add-on tool without shadows keeps its add-on name (addon__<id>__<tool>).
//
// The tool keeps its add-on name for the broker: the call is routed to the
// add-on by that name, so the broker, its permission checks and its audit see
// the same call as before.

// BuiltinProvider is the Provider of a built-in tool that an add-on may shadow.
const BuiltinProvider = "builtin"

// ProviderCardKind is the card of a call of a shadowed tool that an add-on
// served: its ID is the add-on's ID. The interface shows who served the call
// on the tool line, also after a reload. A call of the built-in tool carries a
// card of its own (HostExecCardKind for bash).
const ProviderCardKind = "tool_provider"

// shadowTool turns an add-on tool that shadows a built-in name into the chat
// tool of that name. ok is false for a tool that shadows nothing (or a name
// that may not be shadowed): it keeps its add-on name.
func (s *Service) shadowTool(a workspace.Actor, d AddonTool, schema json.RawMessage) (session.Tool, bool) {
	if d.Shadows == "" || !contract.Replaceable(d.Shadows) {
		return session.Tool{}, false
	}
	route := d.Name
	addon := d.AddonID
	return session.Tool{
		Name: d.Shadows, Description: d.Description, Schema: schema, Provider: addon,
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			out, err := s.d.AddonTools.Call(ctx, a.Principal, a.ChatID, route, args)
			if err == nil {
				session.SetToolCard(ctx, session.ToolCard{Kind: ProviderCardKind, ID: addon})
			}
			return out, err
		},
	}, true
}

// withBuiltins adds the built-in tools that no add-on shadows. A built-in whose
// name an add-on tool already has (Provider set) is left out: the add-on wins.
// Any other tool with the name of an added built-in (an MCP tool, say) is
// dropped, so the model never sees two tools of one name.
func withBuiltins(tools []session.Tool, builtins ...session.Tool) []session.Tool {
	shadowed := map[string]bool{}
	for _, t := range tools {
		if t.Provider != "" {
			shadowed[t.Name] = true
		}
	}
	added := map[string]bool{}
	var keep []session.Tool
	for _, b := range builtins {
		if !shadowed[b.Name] {
			added[b.Name] = true
			keep = append(keep, b)
		}
	}
	if len(added) == 0 {
		return tools
	}
	out := make([]session.Tool, 0, len(tools)+len(keep))
	for _, t := range tools {
		if !added[t.Name] {
			out = append(out, t)
		}
	}
	return append(out, keep...)
}

// resolveShadows keeps one add-on tool per shadowed name: the add-on that pins
// names, or with no pin (or a pin to an add-on the chat does not have) the
// first by add-on ID. The add-ons that replace one tool are mutually
// exclusive; the host keeps no install time to prefer the oldest.
func resolveShadows(tools []session.Tool, pins map[string]string) []session.Tool {
	active := map[string]string{}
	for _, t := range tools {
		if t.Provider == "" || t.Provider == BuiltinProvider {
			continue
		}
		cur, seen := active[t.Name]
		switch {
		case pins[t.Name] == t.Provider:
			active[t.Name] = t.Provider
		case !seen:
			active[t.Name] = t.Provider
		case pins[t.Name] != cur && t.Provider < cur:
			active[t.Name] = t.Provider
		}
	}
	if len(active) == 0 {
		return tools
	}
	out := tools[:0:0]
	for _, t := range tools {
		if t.Provider != "" && t.Provider != BuiltinProvider && active[t.Name] != t.Provider {
			continue
		}
		out = append(out, t)
	}
	return out
}

// providerOf returns who serves the tool of that name in the list: the add-on
// ID, BuiltinProvider, or "" when the list has no such tool or it is not a
// replaceable tool.
func providerOf(tools []session.Tool, name string) string {
	for _, t := range tools {
		if t.Name == name {
			return t.Provider
		}
	}
	return ""
}
