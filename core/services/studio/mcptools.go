package studio

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// reservedToolNames are the names the service owns. A tool from another source
// cannot take one, so the model always gets the real thing.
var reservedToolNames = map[string]bool{"spawn_agent": true, proposeTriggerName: true, listTriggersName: true}

// hostTools returns the tools a conversation has besides the ones the service
// adds itself: those from Deps.HostTools, the add-on tools of its user, the
// collection tools and the MCP tools of its model. A chat with a saved agent
// gets only what the agent grants. The second result lists the grants that
// could not be used, as phrases for a note to the model.
func (s *Service) hostTools(ctx context.Context, a workspace.Actor) ([]session.Tool, error) {
	tools, _, err := s.scopedHostTools(ctx, a)
	return tools, err
}

func (s *Service) scopedHostTools(ctx context.Context, a workspace.Actor) ([]session.Tool, []string, error) {
	rec, err := s.d.Store.GetChat(ctx, a.Principal, a.ChatID)
	if err != nil {
		return nil, nil, err
	}
	sc := s.scopeOf(ctx, rec)
	var tools []session.Tool
	if s.d.HostTools != nil {
		ts, err := s.d.HostTools(ctx, a)
		if err != nil {
			return nil, nil, err
		}
		tools = append(tools, ts...)
	}
	if sc != nil && sc.deleted {
		return tools, []string{"the agent of this chat was deleted"}, nil
	}
	var missing []string
	t, m := s.addonTools(ctx, a, rec, sc)
	tools, missing = append(tools, t...), append(missing, m...)
	t, m = s.collectionTools(ctx, a, sc)
	tools, missing = append(tools, t...), append(missing, m...)
	t, m = s.mcpTools(ctx, a, rec, sc, tools)
	return append(tools, t...), append(missing, m...), nil
}

// mcpTools gives the chat its MCP tools: those of the model's own servers and
// those of the user's connectors, less what the chat switched off. A chat with
// a saved agent gets only the servers and connectors the agent names; a name
// with the prefix "connector:" is a connector, any other is a server of the
// model. A server that cannot be reached gives no tools and a warning; it never
// fails the chat. have lists the tools the chat has already, so that a name
// is never taken twice.
func (s *Service) mcpTools(ctx context.Context, a workspace.Actor, rec session.ChatRecord, sc *scope, have []session.Tool) ([]session.Tool, []string) {
	off := set(rec.McpOff)
	var servers, refs []string
	if sc != nil {
		for _, n := range sc.mcp {
			if off[n] {
				continue
			}
			if id, ok := strings.CutPrefix(n, mcpconns.Prefix); ok {
				refs = append(refs, id)
			} else {
				servers = append(servers, n)
			}
		}
	}
	tools, missing := s.modelMCPTools(ctx, a, rec, sc, servers, off)
	ct, cm := s.connectorTools(ctx, a, rec, sc, refs, off, append(append([]session.Tool(nil), have...), tools...))
	return append(tools, ct...), append(missing, cm...)
}

// modelMCPTools turns the MCP tools of the conversation's model into chat
// tools. The executor is built each time: it reuses cached MCP sessions.
func (s *Service) modelMCPTools(ctx context.Context, a workspace.Actor, rec session.ChatRecord, sc *scope, servers []string, off map[string]bool) ([]session.Tool, []string) {
	if s.d.MCP == nil {
		return nil, nil
	}
	var only []string
	limited := sc != nil
	if sc != nil {
		if len(servers) == 0 {
			return nil, nil
		}
		only = servers
	} else if s.d.MCPServers != nil && len(off) > 0 {
		// A chat with no agent uses the servers the model picks by itself. To
		// leave some out, ask for the others by name.
		var auto, kept []string
		for _, m := range s.d.MCPServers(ctx, a.Principal, rec.Model) {
			if m.Auto {
				auto = append(auto, m.Name)
				if !off[m.Name] {
					kept = append(kept, m.Name)
				}
			}
		}
		if len(kept) < len(auto) {
			if len(kept) == 0 {
				return nil, nil
			}
			only, limited = kept, true
		}
	}
	exec, absent := s.d.MCP(ctx, a.Principal, rec.Model, only, limited)
	missing := prefixed("MCP server", absent)
	if exec == nil || !exec.HasTools() {
		return nil, missing
	}
	tools, err := s.toolsOfApps(ctx, a.ChatID, exec, true)
	if err != nil {
		s.log.Warn("studio: MCP tools unavailable", "chat", a.ChatID, "model", rec.Model, "error", err)
		return nil, missing
	}
	return tools, missing
}
