package studio

import (
	"context"

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
	t, m = s.mcpTools(ctx, a, rec, sc)
	return append(tools, t...), append(missing, m...), nil
}

// mcpTools turns the MCP tools of the conversation's model into chat tools. A
// server that cannot be reached gives no tools and a warning; it never fails
// the chat. The executor is built each time: it reuses cached MCP sessions.
func (s *Service) mcpTools(ctx context.Context, a workspace.Actor, rec session.ChatRecord, sc *scope) ([]session.Tool, []string) {
	if s.d.MCP == nil || (sc != nil && len(sc.mcp) == 0) {
		return nil, nil
	}
	var only []string
	if sc != nil {
		only = sc.mcp
	}
	exec, absent := s.d.MCP(ctx, a.Principal, rec.Model, only, sc != nil)
	missing := prefixed("MCP server", absent)
	if exec == nil || !exec.HasTools() {
		return nil, missing
	}
	tools, err := s.toolsOf(ctx, a.ChatID, exec)
	if err != nil {
		s.log.Warn("studio: MCP tools unavailable", "chat", a.ChatID, "model", rec.Model, "error", err)
		return nil, missing
	}
	return tools, missing
}
