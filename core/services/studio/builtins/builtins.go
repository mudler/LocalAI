// Package builtins is the one registry of the built-in chat tools: the ones
// an administrator can switch off (bash on this machine, web_search,
// web_fetch, list_agents) and the ones a chat can switch off with a group
// (the file tools, the image and audio helpers, the collection tools,
// spawn_agent). A tool registers its runtime settings, the per-chat switch
// group it belongs to, whether an add-on may replace it, and how it is built
// for one chat. The chats add the built-in tools no add-on serves (see
// shadowing in core/services/studio), the chat's Tools menu lists the groups,
// and Settings > Tools lists every listed tool for administrators, so a new
// built-in tool appears in all of them by registering one entry.
package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
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
	// GroupTriggers holds propose_trigger, list_triggers, update_trigger and
	// delete_trigger: a person's chat can set up tasks that run by itself. It
	// is on until the chat switches it off.
	GroupTriggers = "triggers"
	// GroupShell holds bash, whoever serves it: the Sandbox add-on or the
	// built-in bash on this machine.
	GroupShell = "shell"
	// GroupFiles holds the file tools: list_files, read_file, write_file,
	// edit_file, move_file, delete_file, search_files and read_document.
	GroupFiles = "files"
	// GroupMedia holds the helper-model tools describe_image and
	// transcribe_audio.
	GroupMedia = "media"
	// GroupCollections holds list_collections and search_collection.
	GroupCollections = "collections"
	// GroupChanges holds propose_change and revert_change: a chat can ask to
	// change the person's memory or an agent profile, and the person confirms.
	GroupChanges = "changes"
	// GroupSubAgents holds spawn_agent.
	GroupSubAgents = "subagents"
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
	// OriginID names the trigger, the connector binding or the job that
	// started the chat.
	OriginID string
	// Agent is true when the chat runs a saved agent, whose grants the owner
	// chose: an unattended run without one gets no tool that acts at once.
	Agent bool
	// AgentID and Model are the saved agent and the model of the chat, for a
	// tool that records them.
	AgentID, Model string
	// SubAgent is true for a sub-agent's conversation.
	SubAgent bool
	// Admin is true when the user is an administrator (always without
	// accounts).
	Admin bool
	// GroupsOff are the switch groups the chat switched off.
	GroupsOff []string
	// GroupsOn are the opt-in switch groups the chat switched on.
	GroupsOn []string
	// Grants are the builtin grants of the chat's saved agent after the
	// intersection with what the user may use. Empty for a chat without an
	// agent: see HoldsGrant.
	Grants []string
	// GoalActive is true while the chat works toward an active goal.
	GoalActive bool
	// Allow answers whether the sender of this turn may use a capability
	// (contract.ConnectorCapabilityNames), with the reason when not. It is set
	// for a connector chat and nil for any other, which has no sender to
	// check. Audit records the use of a capability; it is nil when Allow is.
	Allow func(capability string) (bool, string)
	Audit func(capability string, allowed bool, reason string)
}

// Require checks a capability at the moment a tool uses it and records the
// decision. A chat without a sender check passes. The error is worded for the
// model, which tells the person.
func (c Chat) Require(capability string) error {
	if c.Allow == nil {
		return nil
	}
	ok, why := c.Allow(capability)
	if c.Audit != nil {
		c.Audit(capability, ok, why)
	}
	if !ok {
		return fmt.Errorf("not permitted: %s. Tell the person you cannot do this for them here", why)
	}
	return nil
}

// Holds says, without recording, whether the sender of the turn holds any one
// of the capabilities. A chat without a sender check holds all.
func (c Chat) Holds(capabilities ...string) (bool, string) {
	if c.Allow == nil || len(capabilities) == 0 {
		return true, ""
	}
	why := ""
	for _, name := range capabilities {
		ok, w := c.Allow(name)
		if ok {
			return true, ""
		}
		why = w
	}
	return false, why
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
	// Unlisted says that the tool has no runtime setting: the operator's
	// start-up switch governs it, and Settings > Tools does not list it. It
	// needs no Setting.
	Unlisted bool
	// Options are the other runtime settings that configure the tool.
	Options []string
	// ReadOnly says that the tool only reads: it needs no confirmation.
	ReadOnly bool
	// Replaceable says that an add-on tool may shadow this one by name. It
	// must be one of contract.ReplaceableTools.
	Replaceable bool
	// Covers says that the chat's switch of the group also hides an add-on
	// tool that replaces this one, so a chat with the group off has no tool
	// of this name at all (bash in GroupShell). Without it the switch only
	// governs the tool LocalAI serves itself. It needs a Group and
	// Replaceable.
	Covers bool
	// External says that another part of the chats builds the tool for a
	// turn, together with tools of its own kind (the file tools, the media
	// helpers, the collection tools, spawn_agent). Build only says whether
	// the chat can have it, Registry.Build leaves it out, and the builder
	// asks Hidden whether the chat switched it off. It needs a Group.
	External bool
	// Capabilities are the capabilities that gate the tool in a connector
	// chat: it is offered only when the sender of the turn holds at least one,
	// and a call is refused at once when none is held. A tool whose right
	// depends on its arguments lists the capabilities of all its uses and
	// checks the one it needs with Chat.Require. Empty: not gated. A chat
	// that is not a connector chat is never gated.
	Capabilities []string
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
	mu     sync.RWMutex
	tools  []Tool
	groups map[string]GroupInfo
}

// GroupInfo is what a person reads about a switch group: a title and a
// description of what the model can do when the switch is on. Both are
// English; the interface may translate them by group ID. A group without a
// declaration falls back to the label and description of its first tool.
type GroupInfo struct {
	Title, Description string
	// WhenOff says what changes for the model when the switch is off. Empty
	// when "the model has none of these tools" says it all.
	WhenOff string
}

// DescribeGroup declares the title and description of a switch group. The
// code that registers the tools of a group declares it next to them.
func (r *Registry) DescribeGroup(group string, info GroupInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.groups == nil {
		r.groups = map[string]GroupInfo{}
	}
	r.groups[group] = info
}

// GroupInfo returns the declared information of a group, or the label and
// description of its first registered tool.
func (r *Registry) GroupInfo(group string) GroupInfo {
	r.mu.RLock()
	info, ok := r.groups[group]
	r.mu.RUnlock()
	if ok {
		return info
	}
	for _, t := range r.Tools() {
		if t.Group == group {
			return GroupInfo{Title: t.Label, Description: t.Description}
		}
	}
	return GroupInfo{}
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{} }

// Register adds a tool. It refuses a tool without an ID, a Setting (unless it is Unlisted) or a
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
	case t.Setting == "" && !t.Unlisted:
		return fmt.Errorf("builtins: %s has no Setting", t.ID)
	case t.OptIn && t.Group == "":
		return fmt.Errorf("builtins: %s is opt-in but has no Group", t.ID)
	case t.External && t.Group == "":
		return fmt.Errorf("builtins: %s is external but has no Group", t.ID)
	case slices.ContainsFunc(t.Capabilities, func(n string) bool { return !contract.KnownCapability(n) }):
		return fmt.Errorf("builtins: %s names a capability that is not in the closed list", t.ID)
	case t.Covers && (t.Group == "" || !t.Replaceable):
		return fmt.Errorf("builtins: %s covers its replacement but has no Group or is not replaceable", t.ID)
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

// GroupOf returns the switch group of the tool of that name, or "".
func (r *Registry) GroupOf(id string) string {
	t, _ := r.Lookup(id)
	return t.Group
}

// Hidden returns the names of the tools that the chat's switches take away
// from it although the registry does not build them: the External tools and
// the Covers tools of every group the chat switched off, or did not switch
// on when the group is opt-in. Whoever builds those tools drops these names.
func (r *Registry) Hidden(c Chat) map[string]bool {
	off, on := map[string]bool{}, map[string]bool{}
	for _, g := range c.GroupsOff {
		off[g] = true
	}
	for _, g := range c.GroupsOn {
		on[g] = true
	}
	out := map[string]bool{}
	for _, t := range r.Tools() {
		if (t.External || t.Covers) && (off[t.Group] || (t.OptIn && !on[t.Group])) {
			out[t.ID] = true
		}
	}
	return out
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
	// ReasonNotPermitted: the sender of a connector turn lacks the capability
	// that the tool needs. The text after it says which.
	ReasonNotPermitted = "not permitted"
)

// gateCall makes a gated tool check its capabilities again when the model calls
// it: the grant of a turn can shrink after the tools were offered, when a
// less trusted sender joins the conversation.
func gateCall(tool session.Tool, t Tool, c Chat) session.Tool {
	if len(t.Capabilities) == 0 || c.Allow == nil || tool.Run == nil {
		return tool
	}
	run := tool.Run
	tool.Run = func(ctx context.Context, raw json.RawMessage) (string, error) {
		if held, why := c.Holds(t.Capabilities...); !held {
			if c.Audit != nil {
				c.Audit(strings.Join(t.Capabilities, "|"), false, why)
			}
			return "", fmt.Errorf("not permitted: %s. Tell the person you cannot do this for them here", why)
		}
		return run(ctx, raw)
	}
	return tool
}

// Built is a built-in tool built for a chat, or why not.
type Built struct {
	ID     string
	Tool   session.Tool
	OK     bool
	Reason string // why not, when OK is false: ReasonOff, ReasonChatOff or the tool's own
}

// Build builds every registered tool for one chat, but the External ones.
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
		if t.External {
			continue
		}
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
		if len(t.Capabilities) > 0 {
			if held, why := c.Holds(t.Capabilities...); !held {
				out = append(out, Built{ID: t.ID, Reason: ReasonNotPermitted + ": " + why})
				continue
			}
		}
		tool, ok, reason := t.Build(ctx, c)
		if ok {
			tool.Name, tool.Provider = t.ID, BuiltinProvider
			tool = gateCall(tool, t, c)
		}
		out = append(out, Built{ID: t.ID, Tool: tool, OK: ok, Reason: reason})
	}
	return out
}
