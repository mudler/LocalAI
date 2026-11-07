package studio

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// scope is what a saved agent allows a chat. It only narrows: each list is
// intersected with what the user may use at the time of the turn. A nil scope is
// a plain chat, which gets everything its user may use.
type scope struct {
	agentName    string
	agentID      string // the saved agent of the chat; its skills layer is granted
	instructions string
	deleted      bool // the chat names an agent that no longer exists
	mcp          []string
	addons       map[string]bool
	collections  map[string]bool
	skills       map[string]bool
	memory       string          // the agent's choice of notes; empty is "as before"
	builtins     map[string]bool // the builtin grants the agent holds now
	builtinNames []string
	skillNames   []string
	addonNames   []string
	collNames    []string
	// hostSkills are the skills the host grants for this chat on top of the
	// agent's skills: those of the active memory provider while the chat reads
	// the notes (memorySkillsOff sets them). hostSkillAddon is the add-on that
	// ships them when the provider is an add-on.
	hostSkills     map[string]bool
	hostSkillAddon string
	// groupSkills are the built-in skills of the LocalAI tool groups that the
	// chat has now (groupSkillsOff sets them): the skills that teach the tools.
	groupSkills map[string]bool
}

func set(l []string) map[string]bool {
	m := make(map[string]bool, len(l))
	for _, v := range l {
		m[v] = true
	}
	return m
}

// scopeOf returns the scope of a chat, or nil for a plain chat.
func (s *Service) scopeOf(ctx context.Context, rec session.ChatRecord) *scope {
	if rec.AgentID == "" {
		return nil
	}
	if s.d.Agents == nil {
		return &scope{deleted: true}
	}
	ag, err := s.chatAgent(ctx, rec.Principal, rec.AgentID)
	if errors.Is(err, agents.ErrNotFound) {
		return &scope{deleted: true}
	}
	if err != nil {
		s.log.Warn("studio: agent unavailable", "chat", rec.ID, "agent", rec.AgentID, "error", err)
		return &scope{deleted: true}
	}
	held := s.usableGrants(ctx, rec.Principal, ag.Builtins)
	return &scope{
		agentID: rec.AgentID, agentName: ag.Name,
		builtins: set(held), builtinNames: held,
		instructions: strings.TrimSpace(ag.Instructions), mcp: ag.MCPServers, memory: ag.Memory,
		addons: set(ag.Addons), collections: set(ag.Collections), skills: set(ag.Skills),
		addonNames: ag.Addons, collNames: ag.Collections, skillNames: ag.Skills,
	}
}

// unavailable renders the grants that could not be used into one line for the
// model. Empty when nothing is missing.
func unavailable(parts ...[]string) string {
	var all []string
	for _, p := range parts {
		all = append(all, p...)
	}
	if len(all) == 0 {
		return ""
	}
	sort.Strings(all)
	return "unavailable this turn: " + strings.Join(all, ", ")
}

func prefixed(prefix string, names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = prefix + " " + n
	}
	return out
}
