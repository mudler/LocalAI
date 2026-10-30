package studio

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
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
const BuiltinProvider = builtins.BuiltinProvider

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

// OrderProviders sorts the add-ons that replace one name into the order of
// preference with no pin: the oldest install first. Ties go by ID; an add-on
// with no install time (a record from before the host kept it) sorts after
// every add-on that has one, by ID. installed maps an add-on ID to its install
// time (RFC 3339). It is the one rule for tools and skills.
func OrderProviders(ids []string, installed map[string]string) []string {
	out := slices.Clone(ids)
	slices.SortStableFunc(out, func(a, b string) int {
		ta, oka := installTime(installed[a])
		tb, okb := installTime(installed[b])
		switch {
		case oka && okb && !ta.Equal(tb):
			return ta.Compare(tb)
		case oka != okb:
			if oka {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	return out
}

// installTime parses an install time; a missing or unreadable one has none.
func installTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}

// ChooseProvider returns the add-on that serves a name: the pinned one when it
// is among ids, else the first of OrderProviders. It is "" when ids is empty.
func ChooseProvider(ids []string, installed map[string]string, pin string) string {
	if slices.Contains(ids, pin) {
		return pin
	}
	if ordered := OrderProviders(ids, installed); len(ordered) > 0 {
		return ordered[0]
	}
	return ""
}

// resolveShadows keeps one add-on tool per shadowed name: the add-on that pins
// names, or with no pin (or a pin to an add-on the chat does not have) the
// default one (ChooseProvider: the oldest install). The add-ons that replace
// one tool are mutually exclusive.
func resolveShadows(tools []session.Tool, pins, installed map[string]string) []session.Tool {
	byName := map[string][]string{}
	for _, t := range tools {
		if t.Provider == "" || t.Provider == BuiltinProvider || slices.Contains(byName[t.Name], t.Provider) {
			continue
		}
		byName[t.Name] = append(byName[t.Name], t.Provider)
	}
	if len(byName) == 0 {
		return tools
	}
	active := make(map[string]string, len(byName))
	for name, ids := range byName {
		active[name] = ChooseProvider(ids, installed, pins[name])
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
