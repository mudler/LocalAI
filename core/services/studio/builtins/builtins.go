// Package builtins is the one registry of the built-in chat tools that an
// administrator can switch off: bash (commands on this machine), web_search,
// web_fetch and list_agents (the user's saved agents as sub-agents). A tool registers its runtime settings, the per-chat switch
// group it belongs to, whether an add-on may replace it, and how it is built
// for one chat. The chats add the built-in tools no add-on serves (see
// shadowing in core/services/studio), the chat's Tools menu lists the groups,
// and Settings > Tools lists every tool for administrators, so a new built-in
// tool appears in all of them by registering one entry.
package builtins

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The per-chat switch groups. The values are what PUT
// /api/chats/{id}/builtins accepts.
const (
	GroupWeb = "web"
	// GroupSavedAgents lets a chat call the user's saved agents as
	// sub-agents. It is opt-in: off until the chat switches it on.
	GroupSavedAgents = "saved_agents"
)

// The runtime settings of the built-in tools.
const (
	SettingHostExec           = "chats_host_exec"
	SettingWebSearch          = "chats_web_search"
	SettingWebFetch           = "chats_web_fetch"
	SettingWebSearXNGURL      = "chats_web_searxng_url"
	SettingWebSearchProviders = "chats_web_search_providers"
	SettingWebAllowPrivate    = "chats_web_allow_private"
	SettingWebPorts           = "chats_web_ports"
	SettingSavedAgents        = "chats_saved_agents"
)

// Chat is what a built-in tool knows of the chat it is built for.
type Chat struct {
	// Actor is the user and the chat; the tool acts as them.
	Actor workspace.Actor
	// Origin is "" for a chat a person watches, or trigger, connector, job.
	Origin string
	// Agent is true when the chat runs a saved agent, whose grants the owner
	// chose: an unattended run without one gets no tool that acts at once.
	Agent bool
	// SubAgent is true for a sub-agent's conversation.
	SubAgent bool
	// Admin is true when the user is an administrator (always without
	// accounts).
	Admin bool
	// GroupsOff are the switch groups the chat switched off.
	GroupsOff []string
	// GroupsOn are the opt-in switch groups the chat switched on.
	GroupsOn []string
}

// Tool is one built-in chat tool.
type Tool struct {
	// ID is the tool's name in a chat, such as "web_search".
	ID string
	// Label and Description are English; the interface translates by ID.
	Label, Description string
	// Group is the per-chat switch the tool belongs to, such as GroupWeb, or
	// "" for a tool a chat cannot switch off by itself.
	Group string
	// OptIn says that the tool's group is off in a chat until the chat
	// switches it on (Chat.GroupsOn), instead of on until the chat switches
	// it off. It needs a Group.
	OptIn bool
	// Setting is the runtime setting that switches the tool off.
	Setting string
	// Options are the other runtime settings that configure the tool.
	Options []string
	// ReadOnly says that the tool only reads: it needs no confirmation.
	ReadOnly bool
	// Replaceable says that an add-on tool may shadow this one by name. It
	// must be one of contract.ReplaceableTools.
	Replaceable bool
	// Enabled reports whether administrators left the tool on. Nil: on.
	Enabled func() bool
	// Build returns the tool for one chat, or ok false and a short reason when
	// this chat cannot have it now. The tool's Name and Provider are set by
	// the registry.
	Build func(ctx context.Context, c Chat) (tool session.Tool, ok bool, reason string)
}

// On reports whether administrators left the tool on.
func (t Tool) On() bool { return t.Enabled == nil || t.Enabled() }

// Registry holds the built-in tools. It is safe for concurrent use.
type Registry struct {
	mu    sync.RWMutex
	tools []Tool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{} }

// Register adds a tool. It refuses a tool without an ID, a Setting or a
// Build, a Replaceable tool whose name an add-on may not replace, and an ID
// that is registered already.
func (r *Registry) Register(t Tool) error {
	switch {
	case t.ID == "":
		return fmt.Errorf("builtins: a tool has no ID")
	case t.Replaceable && !contract.Replaceable(t.ID):
		return fmt.Errorf("builtins: %q is not a replaceable tool", t.ID)
	case t.Build == nil:
		return fmt.Errorf("builtins: %s has no Build", t.ID)
	case t.Setting == "":
		return fmt.Errorf("builtins: %s has no Setting", t.ID)
	case t.OptIn && t.Group == "":
		return fmt.Errorf("builtins: %s is opt-in but has no Group", t.ID)
	}
	t.Options = append([]string(nil), t.Options...)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, have := range r.tools {
		if have.ID == t.ID {
			return fmt.Errorf("builtins: %s is registered already", t.ID)
		}
	}
	r.tools = append(r.tools, t)
	return nil
}

// Tools returns the registered tools in the order they were registered. The
// caller may change the copy it gets.
func (r *Registry) Tools() []Tool {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, len(r.tools))
	for i, t := range r.tools {
		t.Options = append([]string(nil), t.Options...)
		out[i] = t
	}
	return out
}

// Lookup returns the tool of an ID.
func (r *Registry) Lookup(id string) (Tool, bool) {
	for _, t := range r.Tools() {
		if t.ID == id {
			return t, true
		}
	}
	return Tool{}, false
}

// Groups returns the switch groups of the registered tools, in a stable
// order.
func (r *Registry) Groups() []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range r.Tools() {
		if t.Group != "" && !seen[t.Group] {
			seen[t.Group] = true
			out = append(out, t.Group)
		}
	}
	return out
}

// IsOptIn says whether g is the switch group of a registered opt-in tool.
func (r *Registry) IsOptIn(g string) bool {
	for _, t := range r.Tools() {
		if t.Group == g && t.OptIn {
			return true
		}
	}
	return false
}

// IsGroup says whether g is the switch group of a registered tool.
func (r *Registry) IsGroup(g string) bool {
	return slices.Contains(r.Groups(), g)
}

// BuiltinProvider is the Provider of a built-in tool in a chat.
const BuiltinProvider = "builtin"

// The reasons Build gives for a tool it left out.
const (
	// ReasonOff: an administrator switched the tool off.
	ReasonOff = "off"
	// ReasonChatOff: the chat switched the tool's group off.
	ReasonChatOff = "off for this chat"
	// ReasonNotOn: the tool's group is opt-in and the chat did not switch it
	// on.
	ReasonNotOn = "not switched on for this chat"
)

// Built is a built-in tool built for a chat, or why not.
type Built struct {
	ID     string
	Tool   session.Tool
	OK     bool
	Reason string // why not, when OK is false: ReasonOff, ReasonChatOff or the tool's own
}

// Build builds every registered tool for one chat.
func (r *Registry) Build(ctx context.Context, c Chat) []Built {
	off, on := map[string]bool{}, map[string]bool{}
	for _, g := range c.GroupsOff {
		off[g] = true
	}
	for _, g := range c.GroupsOn {
		on[g] = true
	}
	var out []Built
	for _, t := range r.Tools() {
		if !t.On() {
			out = append(out, Built{ID: t.ID, Reason: ReasonOff})
			continue
		}
		if t.Group != "" && off[t.Group] {
			out = append(out, Built{ID: t.ID, Reason: ReasonChatOff})
			continue
		}
		if t.OptIn && !on[t.Group] {
			out = append(out, Built{ID: t.ID, Reason: ReasonNotOn})
			continue
		}
		tool, ok, reason := t.Build(ctx, c)
		if ok {
			tool.Name, tool.Provider = t.ID, BuiltinProvider
		}
		out = append(out, Built{ID: t.ID, Tool: tool, OK: ok, Reason: reason})
	}
	return out
}
