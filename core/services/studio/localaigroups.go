package studio

import (
	"context"
	"errors"
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
//     It has no special case for the origin of a chat: a messaging chat gets
//     it like a Studio chat, for the owner's permissions, and an agent switches
//     it off with builtins_off. Unattended runs go through act.spend.
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
			case c.Origin == session.OriginConnector:
				// The person who writes in a messaging chat may not be the
				// owner, and these tools change the server: no switch of the
				// chat or the agent changes that.
				return session.Tool{}, false, "the admin tools are never offered in a messaging chat: the person writing may not be the owner, and these tools change the server"
			case c.Origin != "":
				// A trigger or a job runs with no person to confirm.
				return session.Tool{}, false, "the admin tools are for chats you watch, not for runs that start by themselves"
			}
			if err := s.assistantAllowed(ctx, c.Actor.Principal); err != nil {
				return session.Tool{}, false, err.Error()
			}
			return session.Tool{}, true, ""
		},
	})
}

// ClientSkill is the built-in skill that teaches the model the client tools.
const ClientSkill = "localai"

// adminSkillNames are the built-in skills of the admin recipes
// (localaitools.Recipes): the step-by-step text that used to be in the prompt
// of every admin chat. Only a chat that has the admin tools lists them.
var adminSkillNames = func() []string {
	var out []string
	for _, r := range localaitools.Recipes() {
		out = append(out, r.Name)
	}
	return out
}()

// groupSkillNames are the built-in skills that belong to a LocalAI tool group:
// the chat lists and loads them only while it has the group. They cost
// nothing when the group is off, and a user who cannot have the group never
// sees their names.
func groupSkillNames(group string) []string {
	switch group {
	case builtins.GroupLocalAIClient:
		return []string{ClientSkill}
	case builtins.GroupLocalAIAdmin:
		return adminSkillNames
	}
	return nil
}

// skillsOffOf returns the skills a chat has switched off, plus those the chat
// does not read: the memory skills (memorySkillsOff) and the skills of a tool
// group the chat does not have (groupSkillsOff).
func (s *Service) skillsOffOf(ctx context.Context, rec session.ChatRecord, sc *scope) []string {
	return s.groupSkillsOff(ctx, rec, sc, s.memorySkillsOff(ctx, rec, sc, rec.SkillsOff))
}

// groupSkillsOff adds the skills of each LocalAI tool group the chat does not
// have to off. It asks the registry, so the answer is the one the tools get:
// the setting, the chat's switches, the agent's ceiling, the role and the
// origin. For a chat that has a group, it records on the scope of a saved
// agent that the host grants those skills (a built-in skill is otherwise off
// for an agent unless it was granted). The scope is built for this call, so a
// caller must call this before it resolves the skills.
func (s *Service) groupSkillsOff(ctx context.Context, rec session.ChatRecord, sc *scope, off []string) []string {
	chat := s.builtinChat(ctx, userChatActor(rec.Principal, rec.ID), rec, rec.Kind == session.KindSubAgent)
	out := slices.Clone(off)
	for _, g := range []string{builtins.GroupLocalAIClient, builtins.GroupLocalAIAdmin} {
		names := groupSkillNames(g)
		if len(names) == 0 {
			continue
		}
		if b, ok := s.builtins.BuildOne(ctx, g, chat); ok && b.OK {
			if sc != nil {
				if sc.groupSkills == nil {
					sc.groupSkills = map[string]bool{}
				}
				for _, n := range names {
					sc.groupSkills[n] = true
				}
			}
			continue
		}
		out = append(out, names...)
	}
	return out
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
		// A tool with several routes has several rows (same feature and
		// model use case); it is offered once.
		if clientSkipped[spec.Tool] || !served[spec.Tool] || slices.Contains(out, spec.Tool) {
			continue
		}
		if u != nil && s.d.Allowed != nil && !s.d.Allowed(u, spec.Feature) {
			missingFeature = true
			continue
		}
		if spec.Flag == config.FLAG_ANY {
			// A tool that only manages data needs no model.
			out = append(out, spec.Tool)
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

// errOwnerLost means authentication is on and the account a chat acts as could
// not be found or is not active. Nothing may run in its name, and nothing may
// fall back to another identity.
var errOwnerLost = errors.New("the account this chat acts as is not available")

// ownerOf returns the user a chat acts as. Without accounts (authentication
// off) it returns nil and no error. With accounts, a failed lookup or a missing
// user is an error: the caller must not run anything on the user's behalf.
func (s *Service) ownerOf(ctx context.Context, principal string) (*auth.User, error) {
	if s.d.Users == nil {
		return nil, nil
	}
	u, err := s.d.Users(ctx, principal)
	if err != nil || u == nil {
		return nil, errOwnerLost
	}
	return u, nil
}

// localaiTurn returns the tools of the two LocalAI groups for one turn of a
// chat, the system prompt of the admin tools and a note for the model when an
// assistant chat cannot have its tools. Each group goes through the registry
// (the setting, the chat's switches, the agent's ceiling) before its tools are
// turned into chat tools. have are the tools the chat holds already: a name
// taken by one of them is not offered again.
//
// The client tools read and write files only through the chat's fence: x is the
// access of the chat, so an agent's folder bounds the references they accept and
// the place where their results land.
//
// The tools act as the chat's owner. The user travels in the call to the MCP
// server (mcpendpoint.AsUser), so the client tools reach the API as that user.
//
// When the tool search applies (see shouldDefer), the tools of the two groups
// come back in deferred instead of tools: the caller gates them and hides them
// behind the bridge tools (toolSearchTools). deferredGroups names the groups
// that have a tool there.
func (s *Service) localaiTurn(ctx context.Context, a workspace.Actor, x session.Access, rec session.ChatRecord, have []session.Tool) (tools, deferred []session.Tool, deferredGroups []string, prompt, note string) {
	taken := map[string]bool{}
	for _, t := range have {
		taken[t.Name] = true
	}
	var groupTools []session.Tool
	var groups []string
	add := func(group string, ts []session.Tool) {
		for _, t := range ts {
			if !taken[t.Name] {
				taken[t.Name] = true
				groupTools = append(groupTools, t)
				if !slices.Contains(groups, group) {
					groups = append(groups, group)
				}
			}
		}
	}
	// finish decides, from the tools of both groups and the chat's window,
	// whether they go in front of the model or behind the tool search.
	finish := func() {
		if shouldDefer(s.toolSearchMode(), groupTools, s.windowOf(rec)) {
			deferred, deferredGroups = groupTools, groups
			return
		}
		tools = groupTools
	}
	chat := s.builtinChat(ctx, a, rec, rec.Kind == session.KindSubAgent)
	// The tools act as the owner. With authentication on, an owner that
	// cannot be found means neither group is offered this turn: a tool
	// without an acting user must never run as another identity.
	owner, ownerErr := s.ownerOf(ctx, rec.Principal)
	if ownerErr != nil {
		s.log.Warn("studio: the LocalAI tools are not offered: the chat owner could not be found", "chat", rec.ID)
		if rec.Assistant && rec.Kind != session.KindSubAgent {
			note = assistantUnavailable(rec)
		}
		return nil, nil, nil, "", note
	}
	if b, ok := s.builtins.BuildOne(ctx, builtins.GroupLocalAIClient, chat); ok && b.OK {
		names, _ := s.clientToolNames(ctx, rec.Principal)
		if len(names) > 0 {
			// The client tools read and write files only through the chat's
			// fence (clientFilesOf), for direct calls and the tool search.
			exec := mcpendpoint.WithFiles(mcpendpoint.AsUser(mcpendpoint.Only(s.d.ClientTools.ClientExecutor(), func(n string) bool { return slices.Contains(names, n) }), owner), s.clientFilesOf(x))
			if ts, err := s.toolsOf(ctx, rec.ID, exec); err != nil {
				s.log.Warn("studio: the LocalAI client tools are unavailable", "chat", rec.ID, "error", err)
			} else {
				add(builtins.GroupLocalAIClient, guardMessagingRegistry(s.guardVoiceProfile(ts, chat, exec), chat))
			}
		}
	}
	admin, ok := s.builtins.BuildOne(ctx, builtins.GroupLocalAIAdmin, chat)
	switch {
	case ok && admin.OK:
		exec := s.d.Assistant.Executor()
		if exec != nil {
			exec = mcpendpoint.AsUser(exec, owner)
		}
		if exec == nil || !exec.HasTools() {
			finish()
			return tools, deferred, deferredGroups, "", assistantUnavailable(rec)
		}
		ts, err := s.toolsOf(ctx, rec.ID, exec)
		if err != nil {
			s.log.Warn("studio: assistant tools unavailable", "chat", rec.ID, "error", err)
			finish()
			return tools, deferred, deferredGroups, "", assistantUnavailable(rec)
		}
		add(builtins.GroupLocalAIAdmin, ts)
		// The prompt carries the safety rules (confirm before changing
		// anything), so the tools never come without it.
		prompt = s.adminPrompt()
	case rec.Assistant && rec.Kind != session.KindSubAgent:
		// An assistant chat that cannot have its tools this turn (the setting
		// was switched off, the user lost the role): say so once.
		note = assistantUnavailable(rec)
	}
	finish()
	return tools, deferred, deferredGroups, prompt, note
}

// CompactAssistant is an Assistant that also has the short prompt for chats
// with skills: the role, the safety rules and the names of the recipes, which
// the model loads as skills (localai-admin-*). An Assistant without it gets
// the full prompt.
type CompactAssistant interface {
	CompactSystemPrompt() string
}

// adminPrompt is the prompt of a chat that has the admin tools. The chats that
// can load skills get the compact prompt, because every turn pays for it; a
// chat without the skills layer gets the full prompt, with every recipe inline.
func (s *Service) adminPrompt() string {
	if c, ok := s.d.Assistant.(CompactAssistant); ok && s.d.Files.Available() {
		if p := c.CompactSystemPrompt(); p != "" {
			return p
		}
	}
	return s.d.Assistant.SystemPrompt()
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
