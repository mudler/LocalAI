package studio

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mudler/LocalAI/core/http/auth"
	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/mcpapps"
)

// Errors of the assistant chats.
var (
	// ErrAssistantOff means the LocalAI assistant is switched off, or this
	// server has none.
	ErrAssistantOff = errors.New("studio: the LocalAI assistant is not available on this server")
	// ErrAssistantForbidden means only an administrator may start an assistant chat.
	ErrAssistantForbidden = errors.New("studio: the LocalAI assistant needs an administrator")
	// ErrAssistantAgent means an assistant chat cannot also run with a saved agent.
	ErrAssistantAgent = errors.New("studio: an assistant chat cannot name a saved agent")
)

// Assistant is the LocalAI assistant as the chats see it: the tools that manage
// this instance (install models and backends, change settings) and the system
// prompt that teaches the model to use them safely.
type Assistant interface {
	// Enabled says whether the assistant may be used now. It is read on every
	// turn, so switching the setting off takes effect at once.
	Enabled() bool
	// Executor runs the assistant tools.
	Executor() mcpendpoint.ToolExecutor
	// SystemPrompt is the prompt of the assistant.
	SystemPrompt() string
}

// assistantAllowed says whether user may use the assistant now: it is on and
// the user is an administrator. Without a user lookup, authentication is off
// and everyone is the administrator.
func (s *Service) assistantAllowed(ctx context.Context, user string) error {
	if s.d.Assistant == nil || !s.d.Assistant.Enabled() {
		return ErrAssistantOff
	}
	if s.d.Users == nil {
		return nil
	}
	u, err := s.d.Users(ctx, user)
	if err != nil || u == nil || u.Role != auth.RoleAdmin {
		return ErrAssistantForbidden
	}
	return nil
}

// assistantTurn returns the tools and the prompt an assistant chat has this
// turn, and a note for the model when it cannot have them. Another chat gets
// nothing. The check is made every turn, not only when the chat starts: the
// setting can be switched off and the user can lose the role.
func (s *Service) assistantTurn(ctx context.Context, rec session.ChatRecord) (tools []session.Tool, prompt, note string) {
	if !rec.Assistant || rec.Kind == session.KindSubAgent {
		return nil, "", ""
	}
	if err := s.assistantAllowed(ctx, rec.Principal); err != nil {
		return nil, "", "unavailable this turn: the LocalAI assistant"
	}
	exec := s.d.Assistant.Executor()
	if exec == nil || !exec.HasTools() {
		return nil, "", "unavailable this turn: the LocalAI assistant"
	}
	tools, err := s.toolsOf(ctx, rec.ID, exec)
	if err != nil {
		s.log.Warn("studio: assistant tools unavailable", "chat", rec.ID, "error", err)
		return nil, "", "unavailable this turn: the LocalAI assistant"
	}
	return tools, s.d.Assistant.SystemPrompt(), ""
}

// toolsOf turns the tools of an executor into chat tools. A tool in the add-on
// namespace or with the name of a built-in is dropped.
func (s *Service) toolsOf(ctx context.Context, chat string, exec mcpendpoint.ToolExecutor) ([]session.Tool, error) {
	return s.toolsOfApps(ctx, chat, exec, false)
}

// toolsOfApps is toolsOf. With apps set and an executor that serves MCP Apps,
// a tool for the app only is left out, and a tool with a view attaches its app
// to the result.
func (s *Service) toolsOfApps(ctx context.Context, chat string, exec mcpendpoint.ToolExecutor, apps bool) ([]session.Tool, error) {
	fns, err := exec.DiscoverTools(ctx)
	if err != nil {
		return nil, err
	}
	var ae mcpendpoint.AppExecutor
	byName := map[string]mcpendpoint.AppTool{}
	if apps {
		if x, ok := exec.(mcpendpoint.AppExecutor); ok {
			ae = x
			for _, t := range x.AppTools() {
				byName[t.Name] = t
			}
		}
	}
	tools := make([]session.Tool, 0, len(fns))
	for _, fn := range fns {
		name := fn.Name
		app, hasApp := byName[name]
		if hasApp && !app.UI.ForModel() {
			continue // a tool for the app only is never offered to the model
		}
		if mcpendpoint.IsAddonToolName(name) {
			s.log.Warn("studio: a tool in the reserved add-on namespace was dropped", "chat", chat, "tool", name)
			continue
		}
		if reservedToolNames[name] {
			s.log.Warn("studio: a tool takes the name of a built-in and was dropped", "chat", chat, "tool", name)
			continue
		}
		schema := json.RawMessage(`{"type":"object"}`)
		if fn.Parameters != nil {
			if raw, err := json.Marshal(fn.Parameters); err == nil {
				schema = raw
			}
		}
		run := func(ctx context.Context, args json.RawMessage) (string, error) {
			return exec.ExecuteTool(ctx, name, string(args))
		}
		if hasApp && app.UI.ResourceURI != "" {
			run = withApp(app.Server, name, app.UI, func(ctx context.Context, args json.RawMessage) (mcpapps.CallResult, error) {
				return ae.ExecuteAppTool(ctx, app.Server, name, args)
			})
		}
		tools = append(tools, session.Tool{Name: name, Description: fn.Description, Schema: schema, Run: run})
	}
	return tools, nil
}
