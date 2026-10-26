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
// adds itself: those from Deps.HostTools and the MCP tools of its model.
func (s *Service) hostTools(ctx context.Context, a workspace.Actor) ([]session.Tool, error) {
	var tools []session.Tool
	if s.d.HostTools != nil {
		ts, err := s.d.HostTools(ctx, a)
		if err != nil {
			return nil, err
		}
		tools = append(tools, ts...)
	}
	return append(tools, s.mcpTools(ctx, a)...), nil
}

// mcpTools turns the MCP tools of the conversation's model into chat tools. A
// server that cannot be reached gives no tools and a warning; it never fails
// the chat. The executor is built each time: it reuses cached MCP sessions.
func (s *Service) mcpTools(ctx context.Context, a workspace.Actor) []session.Tool {
	if s.d.MCP == nil {
		return nil
	}
	rec, err := s.d.Store.GetChat(ctx, a.Principal, a.ChatID)
	if err != nil {
		return nil
	}
	exec := s.d.MCP(ctx, a.Principal, rec.Model)
	if exec == nil || !exec.HasTools() {
		return nil
	}
	fns, err := exec.DiscoverTools(ctx)
	if err != nil {
		s.log.Warn("studio: MCP tools unavailable", "chat", a.ChatID, "model", rec.Model, "error", err)
		return nil
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
	return tools
}
