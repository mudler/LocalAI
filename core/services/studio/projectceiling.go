package studio

import (
	"context"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// capAgent is the agent as a project allows it to work. For skills, add-ons
// and collections a non-empty project list is a ceiling: the agent keeps only
// the names on both lists. The MCP list of an agent holds server names and
// "connector:<id>" entries; the names are capped by the project's MCP servers
// and the connector entries by its connectors, each only when that project
// list is non-empty. An empty project list is no ceiling.
//
// Connections, resources and grants are not capped: the project manifest has
// no source for them. Neither are the skills a host layer adds to a chat (the
// memory provider, the tool groups) or the agent's own skills folder: they are
// not entries of the agent's list. The stored agent is never changed; the lists are new
// slices.
func capAgent(ag agents.Agent, d projects.Defaults) agents.Agent {
	ag.Skills = capList(ag.Skills, d.Skills)
	ag.Addons = capList(ag.Addons, d.Addons)
	ag.Collections = capList(ag.Collections, d.Collections)
	if len(d.MCPServers) == 0 && len(d.Connectors) == 0 {
		return ag
	}
	var allowed []string
	for _, id := range d.Connectors {
		allowed = append(allowed, mcpconns.Prefix+id)
	}
	out := make([]string, 0, len(ag.MCPServers))
	for _, m := range ag.MCPServers {
		ceiling := d.MCPServers
		if strings.HasPrefix(m, mcpconns.Prefix) {
			ceiling = allowed
		}
		if len(ceiling) == 0 || slices.Contains(ceiling, m) {
			out = append(out, m)
		}
	}
	if len(ag.MCPServers) > 0 {
		ag.MCPServers = out
	}
	return ag
}

// capList keeps the entries of list that are in ceiling; an empty ceiling
// keeps the list as it is.
func capList(list, ceiling []string) []string {
	if len(ceiling) == 0 || len(list) == 0 {
		return list
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		if slices.Contains(ceiling, v) {
			out = append(out, v)
		}
	}
	return out
}

// runtimeAgent is the agent a run reads its lists from, for the actor of the
// run. A plain agent is returned as it is. A project agent is capped by the
// manifest of its project as it is now (read on every call, so an edit narrows
// at once), and gets the attached tools of the project when the actor owns it;
// when the project cannot be resolved the error is returned and nothing runs
// with the uncapped lists.
func (s *Service) runtimeAgent(ctx context.Context, actor string, ag agents.Agent) (agents.Agent, error) {
	return s.runtimeAgentVia(ctx, actor, ag, false)
}

// runtimeAgentVia is runtimeAgent for a run that may start from a messaging
// connection: with viaConnector set the project attaches no connector, add-on
// or MCP server to the agent (see agentUnderProject).
func (s *Service) runtimeAgentVia(ctx context.Context, actor string, ag agents.Agent, viaConnector bool) (agents.Agent, error) {
	if ag.ProjectID == "" {
		return ag, nil
	}
	r, err := s.projectOfAgent(ctx, ag)
	if err != nil {
		return agents.Agent{}, err
	}
	return agentUnderProject(ag, r.Manifest, r.Owner, actor, viaConnector), nil
}

// cappedForChat is the agent as the chat rec may use it, ag being the stored
// agent of the chat. A project agent is capped by its own project
// (runtimeAgent). A sub-agent that runs a saved agent of no project inside the
// project of its parent (projectAgentOf: it works in the project's fence) is
// capped by the project of the parent, so a plain saved agent cannot hold more
// than the project allows. A chat with no project pays nothing. A project that
// cannot be resolved is an error: nothing runs with the uncapped lists.
func (s *Service) cappedForChat(ctx context.Context, rec session.ChatRecord, ag agents.Agent) (agents.Agent, error) {
	viaConnector := s.viaConnector(ctx, rec)
	ag, err := s.runtimeAgentVia(ctx, rec.Principal, ag, viaConnector)
	if err != nil || ag.ProjectID != "" || rec.Kind != session.KindSubAgent {
		return ag, err
	}
	pag, ok, err := s.projectAgentOf(ctx, rec)
	switch {
	case err != nil:
		// The answer is unknown: nothing runs with the uncapped lists.
		return agents.Agent{}, err
	case !ok:
		return ag, nil
	}
	r, err := s.projectOfAgent(ctx, pag)
	if err != nil {
		return agents.Agent{}, err
	}
	return agentUnderProject(ag, r.Manifest, r.Owner, rec.Principal, viaConnector), nil
}

// CappedAgentForChat is cappedForChat for callers outside this package that
// read the lists of the agent of a chat to decide what the chat may use.
func (s *Service) CappedAgentForChat(ctx context.Context, rec session.ChatRecord, ag agents.Agent) (agents.Agent, error) {
	return s.cappedForChat(ctx, rec, ag)
}
