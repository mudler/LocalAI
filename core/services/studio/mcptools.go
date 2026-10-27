package studio

import (
	"context"
	"encoding/json"

	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// reservedToolNames are the names the service owns. A tool from another source
// cannot take one, so the model always gets the real thing.
var reservedToolNames = map[string]bool{"spawn_agent": true}

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
	fns, err := exec.DiscoverTools(ctx)
	if err != nil {
		s.log.Warn("studio: MCP tools unavailable", "chat", a.ChatID, "model", rec.Model, "error", err)
		return nil, missing
	}
	tools := make([]session.Tool, 0, len(fns))
	for _, fn := range fns {
		name := fn.Name
		// The add-on namespace belongs to the broker (see mcpendpoint.IsAddonToolName):
		// a shared MCP session never offers a tool under it, whatever the
		// executor does.
		if mcpendpoint.IsAddonToolName(name) {
			s.log.Warn("studio: an MCP tool in the reserved add-on namespace was dropped", "tool", name)
			continue
		}
		if reservedToolNames[name] {
			s.log.Warn("studio: an MCP tool takes the name of a built-in and was dropped", "tool", name)
			continue
		}
		schema := json.RawMessage(`{"type":"object"}`)
		if fn.Parameters != nil {
			if raw, err := json.Marshal(fn.Parameters); err == nil {
				schema = raw
			}
		}
		tools = append(tools, session.Tool{
			Name: name, Description: fn.Description, Schema: schema,
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				return exec.ExecuteTool(ctx, name, string(args))
			},
		})
	}
	return tools, missing
}
