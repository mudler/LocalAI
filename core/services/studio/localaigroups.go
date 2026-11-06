package studio

import (
	"context"
	"slices"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The two built-in groups of LocalAI tools.
//
//   - LocalAI client (builtins.GroupLocalAIClient): inference for any user.
//     Each tool is offered when the user holds its feature permission and an
//     installed model has the use case, so the model sees only what it can call.
//   - LocalAI admin (builtins.GroupLocalAIAdmin): the tools that manage this
//     instance. For administrators, opt-in per chat or per saved agent, and
//     never for a sub-agent or a run that nobody watches.
//
// Each group is one entry of the registry of built-in tools, so the chat's
// switches, the agent's ceilings, the runtime settings and the Tools menu treat
// it like any other group. The entry is External: the registry decides whether
// a chat has the group (Registry.BuildOne) and this file turns the tools of the
// MCP server into chat tools, which the registry cannot do because it builds
// one tool per entry.

// ClientAssistant serves the client group: the executor of the client tools and
// the choice of a model for a use case.
type ClientAssistant interface {
	// ClientExecutor runs the client tools.
	ClientExecutor() mcpendpoint.ToolExecutor
	// FirstModel is the localaitools.ModelChooser: the first installed model
	// with the use case, or "".
	FirstModel(ctx context.Context, use config.ModelConfigUsecase) (string, error)
}

// clientSkipped are the client tools a chat does not take from the client
// group. The media group serves describe_image and transcribe_audio, with the
// helper models the administrator chose and the user's files.
var clientSkipped = map[string]bool{"describe_image": true, "transcribe_audio": true}

// clientToolNames are the names of the client tools that the group offers, for
// the gate of the unattended runs.
var clientToolNames = func() map[string]bool {
	out := map[string]bool{}
	for _, t := range localaitools.ClientTools {
		if !clientSkipped[t.Tool] {
			out[t.Tool] = true
		}
	}
	return out
}()

// registerLocalAIGroups adds the two groups to the registry. Each has its own
// runtime setting and is listed in Settings > Tools.
func (s *Service) registerLocalAIGroups() error {
	s.builtins.DescribeGroup(builtins.GroupLocalAIClient, builtins.GroupInfo{
		Title:       "LocalAI client",
		Description: "The model can make images, speech, sound and video, transcribe, embed, rerank, detect objects and compare voices and faces, with the models installed here. Each tool needs your permission for that feature.",
		WhenOff:     "The model cannot run these models itself.",
	})
	s.builtins.DescribeGroup(builtins.GroupLocalAIAdmin, builtins.GroupInfo{
		Title:       "LocalAI admin",
		Description: "The model can manage this LocalAI server: install and remove models and backends, change settings, read the state. Administrators only; the model asks you before it changes anything.",
		WhenOff:     "The model cannot manage this server.",
	})
	if s.d.ClientTools != nil {
		if err := s.builtins.Register(builtins.Tool{
			ID: builtins.GroupLocalAIClient, Group: builtins.GroupLocalAIClient, External: true,
			Label:       "LocalAI client",
			Description: "Let the chats run inference with the models installed here (images, speech, sound, video, embeddings, detection and more), within each user's own permissions.",
			Setting:     builtins.SettingLocalAIClient,
			Enabled:     func() bool { return s.d.LocalAIClientOn == nil || s.d.LocalAIClientOn() },
			Build: func(ctx context.Context, c builtins.Chat) (session.Tool, bool, string) {
				names, reason := s.clientToolNames(ctx, c.Actor.Principal)
				return session.Tool{}, len(names) > 0, reason
			},
		}); err != nil {
			return err
		}
	}
	if s.d.Assistant == nil {
		return nil
	}
	return s.builtins.Register(builtins.Tool{
		ID: builtins.GroupLocalAIAdmin, Group: builtins.GroupLocalAIAdmin, OptIn: true, External: true,
		Label:       "LocalAI admin",
		Description: "Let the chats of administrators manage this server: models, backends, settings and the state. Each chat, or saved agent, must switch it on.",
		Setting:     builtins.SettingLocalAIAdmin,
		Enabled:     func() bool { return s.d.LocalAIAdminOn == nil || s.d.LocalAIAdminOn() },
		Build: func(ctx context.Context, c builtins.Chat) (session.Tool, bool, string) {
			switch {
			case c.SubAgent:
				// A sub-agent works on a task of its parent and nobody
				// watches it: it never manages the server.
				return session.Tool{}, false, "a sub-agent never has the admin tools"
			case c.Origin != "":
				// A trigger or a job runs with no person to confirm, and a
				// connector turn comes from a sender who is not the owner.
				return session.Tool{}, false, "the admin tools are for chats you watch, not for runs that start by themselves or messaging chats"
			}
			if err := s.assistantAllowed(ctx, c.Actor.Principal); err != nil {
				return session.Tool{}, false, err.Error()
			}
			return session.Tool{}, true, ""
		},
	})
}

// clientToolNames returns the client tools this user can call now, and the
// reason when there are none. A tool needs the feature permission of its route
// and an installed model that has the use case.
func (s *Service) clientToolNames(ctx context.Context, user string) ([]string, string) {
	ct := s.d.ClientTools
	if ct == nil {
		return nil, "the LocalAI client tools are not available on this server"
	}
	exec := ct.ClientExecutor()
	if exec == nil || !exec.HasTools() {
		return nil, "the LocalAI client tools are not available on this server"
	}
	fns, err := exec.DiscoverTools(ctx)
	if err != nil {
		return nil, "the LocalAI client tools could not be listed"
	}
	served := map[string]bool{}
	for _, f := range fns {
		served[f.Name] = true
	}
	var u *auth.User
	if s.d.Users != nil {
		if u, err = s.d.Users(ctx, user); err != nil || u == nil {
			return nil, "the account is not available"
		}
	}
	var out []string
	missingFeature, missingModel := false, false
	hasModel := map[config.ModelConfigUsecase]bool{}
	for _, spec := range localaitools.ClientTools {
		if clientSkipped[spec.Tool] || !served[spec.Tool] {
			continue
		}
		if u != nil && s.d.Allowed != nil && !s.d.Allowed(u, spec.Feature) {
			missingFeature = true
			continue
		}
		ok, seen := hasModel[spec.Flag]
		if !seen {
			m, merr := ct.FirstModel(ctx, spec.Flag)
			ok = merr == nil && m != ""
			hasModel[spec.Flag] = ok
		}
		if !ok {
			missingModel = true
			continue
		}
		out = append(out, spec.Tool)
	}
	switch {
	case len(out) > 0:
		return out, ""
	case missingModel:
		return nil, "no installed model can serve these tools"
	case missingFeature:
		return nil, "the account has none of the features these tools need"
	}
	return nil, "no client tool is available"
}

// ownerOf returns the user a chat acts as, or nil without accounts.
func (s *Service) ownerOf(ctx context.Context, principal string) *auth.User {
	if s.d.Users == nil {
		return nil
	}
	u, _ := s.d.Users(ctx, principal)
	return u
}

// localaiTurn returns the tools of the two LocalAI groups for one turn of a
// chat, the system prompt of the admin tools and a note for the model when an
// assistant chat cannot have its tools. Each group goes through the registry
// (the setting, the chat's switches, the agent's ceiling) before its tools are
// turned into chat tools. have are the tools the chat holds already: a name
// taken by one of them is not offered again.
//
// The tools act as the chat's owner. The user travels in the call to the MCP
// server (mcpendpoint.AsUser), so the client tools reach the API as that user.
func (s *Service) localaiTurn(ctx context.Context, a workspace.Actor, rec session.ChatRecord, have []session.Tool) (tools []session.Tool, prompt, note string) {
	taken := map[string]bool{}
	for _, t := range have {
		taken[t.Name] = true
	}
	add := func(ts []session.Tool) {
		for _, t := range ts {
			if !taken[t.Name] {
				taken[t.Name] = true
				tools = append(tools, t)
			}
		}
	}
	chat := s.builtinChat(ctx, a, rec, rec.Kind == session.KindSubAgent)
	if b, ok := s.builtins.BuildOne(ctx, builtins.GroupLocalAIClient, chat); ok && b.OK {
		names, _ := s.clientToolNames(ctx, rec.Principal)
		if len(names) > 0 {
			exec := mcpendpoint.AsUser(mcpendpoint.Only(s.d.ClientTools.ClientExecutor(), func(n string) bool { return slices.Contains(names, n) }), s.ownerOf(ctx, rec.Principal))
			if ts, err := s.toolsOf(ctx, rec.ID, exec); err != nil {
				s.log.Warn("studio: the LocalAI client tools are unavailable", "chat", rec.ID, "error", err)
			} else {
				add(ts)
			}
		}
	}
	admin, ok := s.builtins.BuildOne(ctx, builtins.GroupLocalAIAdmin, chat)
	switch {
	case ok && admin.OK:
		exec := s.d.Assistant.Executor()
		if exec != nil {
			exec = mcpendpoint.AsUser(exec, s.ownerOf(ctx, rec.Principal))
		}
		if exec == nil || !exec.HasTools() {
			return tools, "", assistantUnavailable(rec)
		}
		ts, err := s.toolsOf(ctx, rec.ID, exec)
		if err != nil {
			s.log.Warn("studio: assistant tools unavailable", "chat", rec.ID, "error", err)
			return tools, "", assistantUnavailable(rec)
		}
		add(ts)
		// The prompt carries the safety rules (confirm before changing
		// anything), so the tools never come without it.
		prompt = s.d.Assistant.SystemPrompt()
	case rec.Assistant && rec.Kind != session.KindSubAgent:
		// An assistant chat that cannot have its tools this turn (the setting
		// was switched off, the user lost the role): say so once.
		note = assistantUnavailable(rec)
	}
	return tools, prompt, note
}

func assistantUnavailable(rec session.ChatRecord) string {
	if rec.Assistant {
		return "unavailable this turn: the LocalAI assistant"
	}
	return "unavailable this turn: the LocalAI admin tools"
}

// localaiNames lists the tools of the two groups a user would get, for the
// Tools menu: the client tools the user can call and, when the user is an
// administrator, the admin tools.
func (s *Service) localaiNames(ctx context.Context, group, user string) []string {
	switch group {
	case builtins.GroupLocalAIClient:
		names, _ := s.clientToolNames(ctx, user)
		return names
	case builtins.GroupLocalAIAdmin:
		if s.d.Assistant == nil || s.assistantAllowed(ctx, user) != nil {
			return nil
		}
		exec := s.d.Assistant.Executor()
		if exec == nil {
			return nil
		}
		fns, err := exec.DiscoverTools(ctx)
		if err != nil {
			return nil
		}
		out := make([]string, 0, len(fns))
		for _, f := range fns {
			out = append(out, f.Name)
		}
		slices.Sort(out)
		return out
	}
	return nil
}
