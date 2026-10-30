// Package builtins is the registry of the built-in chat tools that an add-on
// may replace: bash today, web_search and web_fetch when the web tools
// register. A tool registers how it is switched off (a runtime setting) and
// how it is built for one chat. The chats add the built-in tools no add-on
// serves (see shadowing in core/services/studio), and Settings > Tools lists
// them for administrators.
package builtins

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
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
}

// Tool is one built-in chat tool that an add-on may replace.
type Tool struct {
	// ID is the tool's name in a chat, one of contract.ReplaceableTools.
	ID string
	// Label and Description are English; the interface translates by ID.
	Label, Description string
	// Setting is the runtime setting that switches the tool off.
	Setting string
	// Enabled reports whether administrators left the tool on. Nil: on.
	Enabled func() bool
	// Build returns the tool for one chat, or ok false and a short reason when
	// this chat cannot have it now. The tool's Provider is set by the
	// registry.
	Build func(ctx context.Context, c Chat) (tool session.Tool, ok bool, reason string)
}

// On reports whether administrators left the tool on.
func (t Tool) On() bool { return t.Enabled == nil || t.Enabled() }

// Registry holds the built-in tools. It is safe for concurrent use.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{tools: map[string]Tool{}} }

// Register adds a tool. It refuses an ID an add-on may not replace, a tool
// without Build, and an ID that is registered already.
func (r *Registry) Register(t Tool) error {
	if !contract.Replaceable(t.ID) {
		return fmt.Errorf("builtins: %q is not a replaceable tool", t.ID)
	}
	if t.Build == nil {
		return fmt.Errorf("builtins: %s has no Build", t.ID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[t.ID]; ok {
		return fmt.Errorf("builtins: %s is registered already", t.ID)
	}
	r.tools[t.ID] = t
	return nil
}

// Tools returns the registered tools in ID order.
func (r *Registry) Tools() []Tool {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Lookup returns the tool of an ID.
func (r *Registry) Lookup(id string) (Tool, bool) {
	if r == nil {
		return Tool{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[id]
	return t, ok
}

// BuiltinProvider is the Provider of a built-in tool in a chat.
const BuiltinProvider = "builtin"

// Built is a built-in tool built for a chat, or why not.
type Built struct {
	ID     string
	Tool   session.Tool
	OK     bool
	Reason string // why not, when OK is false; "off" when switched off
}

// Build builds every registered tool for one chat.
func (r *Registry) Build(ctx context.Context, c Chat) []Built {
	var out []Built
	for _, t := range r.Tools() {
		if !t.On() {
			out = append(out, Built{ID: t.ID, Reason: "off"})
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
