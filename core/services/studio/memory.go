package studio

import (
	"context"
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/contextfiles"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Memory is the short notes the assistant reads before it answers and may
// save. It is part of the host. The notes are plain Markdown files of the
// person's home:
//
//	user          Memory/index.md                           every chat
//	agent         Memory/agents/<agent>/index.md            chats of one saved agent
//	conversation  Memory/channels/<binding>-<hash>/index.md one messaging conversation
//
// The host puts the three indexes in the prompt (40 lines and 2 KiB each, 6 KiB
// together, wrapped as data the person saved), serves the topic files with
// context_read, ships the two skills that teach the model to read and to save,
// and applies the save level (ask, auto, never). Saving is the memory kind of
// propose_change; the grant memory.write and the rules of changemode.go decide
// who may.
//
// The code that does this is behind MemoryProvider. The built-in provider is
// the reference. Memory is the first provider slot (providerslots.go): an
// add-on that declares it provides the slot (capabilities.provides) can serve
// the job in its place, and the person picks one provider in the settings. The
// host keeps every gate for any provider.
//
// Until the add-on "memory" is retired its manifest still declares the same
// index files and skills. The built-in wins: its declarations are ignored while
// the host serves memory (see addonContextSources and addonSkillsFor), so
// nothing loads twice. The add-on stays installed and harmless.

// MemorySlot is the provider slot of the notes.
const MemorySlot = "memory"

// MemoryAddonID is the add-on that used to make notes reach the prompt.
const MemoryAddonID = contract.LegacyMemoryAddonID

// MemoryProvider is what serves the notes of a chat. The host asks it for the
// files the prompt carries and the skills that teach the model; it keeps every
// gate (the save level, the grants, the messaging rules, Off) to itself, so a
// provider cannot loosen one.
type MemoryProvider interface {
	// ID is "builtin" for the host's own, or the ID of an add-on.
	ID() string
	// Sources are the index files of the notes, widest scope first.
	Sources() []contextfiles.Source
	// Skills are the names of the skills that teach the model to read and to
	// save notes.
	Skills() []string
}

// BuiltinMemoryID is the ID of the host's own provider.
const BuiltinMemoryID = "builtin"

type builtinMemory struct{}

func (builtinMemory) ID() string { return BuiltinMemoryID }

// Sources are the same three index files, with the same ID, that the add-on
// declared, so the block of the prompt does not change.
func (builtinMemory) Sources() []contextfiles.Source {
	return []contextfiles.Source{
		{ID: MemoryAddonID, Scope: contextfiles.ScopeUser, Path: "Memory/index.md"},
		{ID: MemoryAddonID, Scope: contextfiles.ScopeAgent, Path: "Memory/agents/{agent}/index.md"},
		{ID: MemoryAddonID, Scope: contextfiles.ScopeChannel, Path: "Memory/channels/{channel}/index.md"},
	}
}

func (builtinMemory) Skills() []string { return []string{"memory", "memory-check-own-files"} }

// addonMemory is the provider an add-on gives when the person picked it: the
// index files and the skills that the add-on declares. The host reads the files
// as the person and applies the gates itself, as it does for the built-in
// provider; the add-on's role tools reach the model as its other tools do,
// only while it is the provider picked (providerGate).
type addonMemory struct {
	id      string
	sources []contextfiles.Source
	skills  []string
}

func (a addonMemory) ID() string                     { return a.id }
func (a addonMemory) Sources() []contextfiles.Source { return slices.Clone(a.sources) }
func (a addonMemory) Skills() []string               { return slices.Clone(a.skills) }

// memoryProviderFor is what serves the notes for a person: the built-in
// provider, or the adapter of the add-on they picked. When the slot is off the
// built-in provider is returned, and the callers that read notes check the
// state first.
func (s *Service) memoryProviderFor(ctx context.Context, user string, legacy legacyMemory) MemoryProvider {
	addon := s.memoryResolution(ctx, user, legacy).Addon()
	if addon == "" {
		return s.memory
	}
	out := addonMemory{id: addon}
	for _, set := range s.addonContextSets(ctx, user) {
		if set.AddonID == addon {
			out.sources = set.sources()
		}
	}
	if l, ok := s.d.AddonTools.(AddonSkillLister); ok {
		if sets, err := l.AddonSkills(ctx, user); err == nil {
			for _, set := range sets {
				if set.AddonID == addon {
					out.skills = set.Skills
				}
			}
		}
	}
	return out
}

// memoryResolution is the choice of provider for the notes, with the older
// switch of the person (the On or Off setting, or a disabled legacy add-on)
// for one who never chose a provider.
func (s *Service) memoryResolution(ctx context.Context, user string, legacy legacyMemory) SlotResolution {
	return s.resolveSlot(ctx, user, MemorySlot, func() (string, bool) { return s.memoryLegacyChoice(ctx, user, legacy) })
}

// memoryLegacyChoice reads the switch that the provider choice replaced. A
// store that cannot answer reads as off: a failure never reads notes the
// person may have switched off.
func (s *Service) memoryLegacyChoice(ctx context.Context, user string, legacy legacyMemory) (string, bool) {
	if s.d.UserSetting != nil {
		v, ok, err := s.d.UserSetting(ctx, principal(user), HostSettingsID, MemoryEnabledKey)
		switch {
		case err != nil:
			s.log.Warn("studio: the memory switch could not be read, memory stays off for this turn", "error", err)
			return contract.ProviderOff, true
		case ok && v == MemoryOn:
			return contract.ProviderBuiltin, true
		case ok && v == MemoryOff:
			return contract.ProviderOff, true
		}
	}
	if legacy.Installed && !legacy.Running {
		return contract.ProviderOff, true
	}
	return "", false
}

// registerMemorySlot wires the notes into the provider slots.
func (s *Service) registerMemorySlot() {
	s.slotWirings = map[string]slotWiring{MemorySlot: {
		legacy: func(ctx context.Context, user string) (string, bool) {
			return s.memoryLegacyChoice(ctx, user, legacyMemoryOf(s.addonContextSets(ctx, user)))
		},
		narrowed: func(ctx context.Context, rec session.ChatRecord) bool {
			return !s.memoryState(ctx, rec, legacyMemoryOf(s.addonContextSets(ctx, rec.Principal))).On
		},
		onSelect: func(ctx context.Context, user, id string) {
			// The On or Off switch of the notes follows the choice, so an older
			// reader of the switch sees the same answer.
			v := MemoryOn
			if id == contract.ProviderOff {
				v = MemoryOff
			}
			if s.d.SetUserSetting != nil {
				if err := s.d.SetUserSetting(ctx, principal(user), HostSettingsID, MemoryEnabledKey, v); err != nil {
					s.log.Warn("studio: the memory switch could not follow the provider", "error", err)
				}
			}
		},
	}}
}

// memorySkill reports whether sk is one of the built-in memory skills.
func (s *Service) memorySkill(sk string) bool { return slices.Contains(s.memory.Skills(), sk) }

// legacyMemory is what the add-on "memory" is on this server for a user.
type legacyMemory struct {
	// Installed is true when the user can see the add-on.
	Installed bool
	// Running is true when it is enabled and running.
	Running bool
}

// legacyMemoryOf finds the add-on among the sets that declare context files.
func legacyMemoryOf(sets []AddonContextSet) legacyMemory {
	for _, set := range sets {
		if set.AddonID == MemoryAddonID {
			return legacyMemory{Installed: true, Running: set.Running}
		}
	}
	return legacyMemory{}
}

// The reasons memory is off for a chat. The code is stable; a client words it.
const (
	// MemoryOffUser: the person switched memory off for every chat, or
	// disabled the Memory add-on and never chose.
	MemoryOffUser = "memory_off_user"
	// MemoryOffChat: this chat is private, or switched the Memory add-on off.
	MemoryOffChat = "memory_off_chat"
	// MemoryOffAgent: the saved agent of the chat turns memory off.
	MemoryOffAgent = "memory_off_agent"
	// MemoryOffFiles: the files are off on this server, and notes are files.
	MemoryOffFiles = "memory_off_files"
	// MemoryOffProvider: the add-on that serves memory for the person is
	// stopped, disabled or removed. Memory stays off until they pick again;
	// the notes are never moved to another provider on their own.
	MemoryOffProvider = "memory_provider_unavailable"
)

// MemoryState says whether memory works for a chat, and when it does not,
// which level of the chain turned it off. The narrowest level wins: the chat,
// then the agent, then the person, then the server.
type MemoryState struct {
	On bool
	// Code is one of the MemoryOff constants, empty when On.
	Code string
	// By is who holds the switch that is off: "chat", "agent", "user" or
	// "server".
	By string
	// Field names the field of the agent form that holds the switch, for the
	// level "agent": "memory" (the choice None) or "tools:memory" (the old
	// switch of the group).
	Field string
	// Provider and ProviderTitle name the add-on the person picked, for the
	// code MemoryOffProvider.
	Provider, ProviderTitle string
}

func memoryOff(code, by string) MemoryState { return MemoryState{Code: code, By: by} }

// memoryState resolves the switch of a chat from what the host knows. legacy
// is the Memory add-on, for the person who never chose.
func (s *Service) memoryState(ctx context.Context, rec session.ChatRecord, legacy legacyMemory) MemoryState {
	switch {
	case slices.Contains(rec.BuiltinsOff, builtins.GroupMemory):
		// A private chat.
		return memoryOff(MemoryOffChat, "chat")
	case slices.Contains(rec.AddonsOff, MemoryAddonID):
		// The chat switched the Memory add-on off: the same switch, in the
		// words it had before memory was built in.
		return memoryOff(MemoryOffChat, "chat")
	case s.agentMemory(ctx, rec.Principal, rec.AgentID) == agents.MemoryNone:
		// The agent's choice None: it reads and saves nothing.
		st := memoryOff(MemoryOffAgent, "agent")
		st.Field = "memory"
		return st
	case slices.Contains(s.agentOff(ctx, rec), builtins.GroupMemory):
		st := memoryOff(MemoryOffAgent, "agent")
		st.Field = "tools:" + builtins.GroupMemory
		return st
	case s.memoryResolution(ctx, rec.Principal, legacy).Off:
		res := s.memoryResolution(ctx, rec.Principal, legacy)
		if res.Code == ProviderUnavailable {
			st := memoryOff(MemoryOffProvider, "provider")
			st.Provider, st.ProviderTitle = res.AddonID, res.AddonTitle
			return st
		}
		return memoryOff(MemoryOffUser, "user")
	case !s.d.Files.Available():
		return memoryOff(MemoryOffFiles, "server")
	}
	return MemoryState{On: true}
}

// memoryUserOn is the person's own switch: the provider they picked is not
// Off. Without a stored answer memory is on, but for a person who disabled the
// Memory add-on to stop it: the disabled add-on keeps meaning "off" until they
// choose. A store that cannot answer reads as off: a failure never reads notes
// the person may have switched off. So does a picked add-on that cannot serve.
func (s *Service) memoryUserOn(ctx context.Context, user string, legacy legacyMemory) bool {
	return !s.memoryResolution(ctx, user, legacy).Off
}

// The storage of the host settings of a person. They sit in the same store as
// the settings of the add-ons, under an ID that no add-on can have (an add-on
// ID starts with a letter).
const (
	// HostSettingsID is the owner of the host's own per-person settings.
	HostSettingsID = "_host"
	// MemoryEnabledKey holds MemoryOn or MemoryOff.
	MemoryEnabledKey = "memory"
	// MemorySaveKey holds the save level: ask, auto, always or never.
	MemorySaveKey = "memory-save-mode"
	// MemorySaveMessagingKey holds the messaging row: ask, paired_private or
	// never.
	MemorySaveMessagingKey = "memory-save-messaging"
)

// The values of MemoryEnabledKey.
const (
	MemoryOn  = "on"
	MemoryOff = "off"
)

// memoryReads says whether the chat reads the notes. A saved agent with a
// choice of its own (agents.Memory) reads what the choice names; one saved
// before the choice ("as before") reads them only when it names the Memory
// add-on. A chat without an agent reads them.
func (s *Service) memoryReads(ctx context.Context, rec session.ChatRecord, sc *scope, legacy legacyMemory) bool {
	if !s.memoryState(ctx, rec, legacy).On {
		return false
	}
	return sc == nil || sc.memory != "" || sc.addons[MemoryAddonID]
}

// personalNotesOpen says whether a chat may read the owner's personal notes
// (the user and agent scopes of the notes). The agent profile is not one of
// them: it holds the standing rules of the agent, like its instructions, and
// every sender gets it. A chat
// outside a messaging app always may. A messaging chat may only when the
// sender is paired with the bot; an allowlisted sender who is not paired, a
// sender let in through a channel, a turn with no known sender and a grant
// that expired all read none. The notes of the conversation itself (the
// channel scope) are not personal notes and are never held back here. This is
// the read side of the rule that memoryChatScopes enforces on writes.
func (s *Service) personalNotesOpen(rec session.ChatRecord) bool {
	if rec.Origin != session.OriginConnector {
		return true
	}
	_, paired := s.messagingFacts(rec.ID)
	return paired
}

// withoutPersonalNotes drops the user and agent index files of the notes from
// a list of sources, for the ids that serve the notes. The channel files and
// the sources of other add-ons stay.
func withoutPersonalNotes(sources []contextfiles.Source, ids ...string) []contextfiles.Source {
	return slices.DeleteFunc(slices.Clone(sources), func(src contextfiles.Source) bool {
		return src.Scope != contextfiles.ScopeChannel && slices.Contains(ids, src.ID)
	})
}

// agentMemory is the choice of notes of a saved agent: one of the agents.Memory
// values, or empty for a chat without an agent, an agent that cannot be read
// and an agent saved before the choice ("as before").
func (s *Service) agentMemory(ctx context.Context, user, agentID string) string {
	if agentID == "" || s.d.Agents == nil {
		return ""
	}
	ag, err := s.chatAgent(ctx, user, agentID)
	if err != nil {
		return ""
	}
	return ag.Memory
}

// agentMemorySave is the save rule of a saved agent (agents.MemorySaveAlways,
// agents.MemorySaveNever), or empty for the person's setting and for a chat
// without an agent. An agent that cannot be read fails closed: the rule is
// always, because the stored rule may have been a stricter one.
func (s *Service) agentMemorySave(ctx context.Context, user, agentID string) string {
	if agentID == "" || s.d.Agents == nil {
		return ""
	}
	ag, err := s.chatAgent(ctx, user, agentID)
	if err != nil {
		return agents.MemorySaveAlways
	}
	return ag.MemorySave
}

// memorySourcesFor narrows the index files of the notes to what a choice of
// the agent reads. The notes of a messaging conversation stay with every
// choice but None: they belong to the chat, and a conversation outside a
// messaging app has none. Empty (as before) and Mine and its own read all.
func memorySourcesFor(all []contextfiles.Source, choice string) []contextfiles.Source {
	var keep func(contextfiles.Scope) bool
	switch choice {
	case agents.MemoryNone:
		return nil
	case agents.MemoryOwn:
		keep = func(sc contextfiles.Scope) bool { return sc != contextfiles.ScopeUser }
	case agents.MemoryMine:
		keep = func(sc contextfiles.Scope) bool { return sc != contextfiles.ScopeAgent }
	default:
		return all
	}
	var out []contextfiles.Source
	for _, src := range all {
		if keep(src.Scope) {
			out = append(out, src)
		}
	}
	return out
}

// memoryWriteScopes are the scopes a chat may save a note to, enforced by the
// host: Mine and its own and Its own only save to the agent, Mine saves to the
// person, None saves nowhere. It is nil when the model chooses, which is a
// chat without an agent and an agent saved before the choice ("as before").
func (s *Service) memoryWriteScopes(ctx context.Context, c builtins.Chat) []string {
	switch s.agentMemory(ctx, c.Actor.Principal, c.AgentID) {
	case agents.MemoryOwn, agents.MemoryMineAndOwn:
		return []string{string(contract.ScopeAgent)}
	case agents.MemoryMine:
		return []string{string(contract.ScopeUser)}
	case agents.MemoryNone:
		return []string{}
	}
	return nil
}

// memoryChatScopes are the scopes a chat may save a note to. A chat outside a
// messaging app has what memoryWriteScopes says. A messaging chat saves to its
// own conversation first, and may also save to the agent or the person only
// when the sender is paired with the bot and the agent's choice allows that
// scope (with no choice, "as before", the model may name either). An agent
// with the choice None saves nowhere. It is never nil for a messaging chat.
func (s *Service) memoryChatScopes(ctx context.Context, c builtins.Chat) []string {
	fixed := s.memoryWriteScopes(ctx, c)
	if c.Origin != session.OriginConnector {
		return fixed
	}
	if fixed != nil && len(fixed) == 0 {
		return fixed
	}
	out := []string{string(contract.ScopeChannel)}
	if _, paired := s.messagingFacts(c.Actor.ChatID); !paired {
		return out
	}
	if fixed == nil {
		return append(out, string(contract.ScopeUser), string(contract.ScopeAgent))
	}
	return append(out, fixed...)
}

// memorySkillsOff adds the memory skills to the skills a chat has switched off
// when the chat does not read the notes: a skill that says "read your notes"
// has no use then, and the model must not look for them. The skills of the
// built-in provider are off whenever another provider serves.
//
// It also records, on the scope of a saved agent, the skills that the host
// grants for this chat: the skills of the active memory provider, whenever the
// chat reads the notes and may read the personal ones. A sender who is not
// paired in a messaging chat gets no personal notes, so it gets no skills that
// teach reading them. The scope is built for this call, so it is set here and
// read by grantsSkill; a caller must call this before it resolves the skills.
func (s *Service) memorySkillsOff(ctx context.Context, rec session.ChatRecord, sc *scope, off []string) []string {
	legacy := legacyMemoryOf(s.addonContextSets(ctx, rec.Principal))
	prov := s.memoryProviderFor(ctx, rec.Principal, legacy)
	builtin := prov.ID() == BuiltinMemoryID
	reads := s.memoryReads(ctx, rec, sc, legacy) && s.personalNotesOpen(rec)
	if reads && sc != nil {
		sc.hostSkills = set(prov.Skills())
		if !builtin {
			sc.hostSkillAddon = prov.ID()
		}
	}
	if builtin && reads {
		return off
	}
	out := slices.Clone(off)
	if !builtin || !reads {
		out = append(out, s.memory.Skills()...)
	}
	if !builtin && !reads {
		out = append(out, prov.Skills()...)
	}
	return out
}

// memoryProviderSkills returns the skills that the active memory provider of
// the user ships, for the skill lists: the names, and the add-on that ships
// them ("" for the built-in provider).
func (s *Service) memoryProviderSkills(ctx context.Context, user string) ([]string, string) {
	legacy := legacyMemoryOf(s.addonContextSets(ctx, user))
	prov := s.memoryProviderFor(ctx, user, legacy)
	if prov.ID() == BuiltinMemoryID {
		return prov.Skills(), ""
	}
	return prov.Skills(), prov.ID()
}

// memoryWritable says whether a chat may propose a memory note: memory is on
// for it. A chat that does not read the notes (a saved agent that does not name
// the Memory add-on) still proposes, as before: the grant decides that.
func (s *Service) memoryWritable(ctx context.Context, c builtins.Chat) bool {
	rec := session.ChatRecord{Principal: c.Actor.Principal, ID: c.Actor.ChatID}
	if got, err := s.d.Store.GetChat(ctx, c.Actor.Principal, c.Actor.ChatID); err == nil {
		rec = got
	}
	legacy := legacyMemoryOf(s.addonContextSets(ctx, c.Actor.Principal))
	// The memory kind of propose_change is the built-in provider's way to save.
	// An add-on that serves the notes saves with its own write tools.
	if s.memoryProviderFor(ctx, c.Actor.Principal, legacy).ID() != BuiltinMemoryID {
		return false
	}
	return s.memoryState(ctx, rec, legacy).On && !slices.Contains(c.GroupsOff, builtins.GroupMemory)
}

// memoryOffLine is the one line the model reads while memory is off for a
// chat: it hears that nothing is read or saved and what to say when asked.
func memoryOffLine(st MemoryState) string {
	where := "in the user's settings"
	switch st.By {
	case "chat":
		where = "for this chat, which is private"
	case "agent":
		where = "for this agent"
	case "provider":
		where = "because the add-on that serves it is not available"
	}
	return "Memory is off " + where + ", so notes are neither read nor saved. If asked to remember something, say that memory is off and that the user can turn it on again; do not try to save it any other way."
}

// MemoryReason is a memory state worded for a person, with where to fix it.
type MemoryReason struct {
	On bool `json:"on"`
	// Code is a stable name of the level that is off: memory_off_chat,
	// memory_off_agent, memory_off_user or memory_off_files. Empty when on.
	Code string `json:"code,omitempty"`
	// By is who holds that switch: chat, agent, user or server.
	By string `json:"blocked_by,omitempty"`
	// Reason is an English sentence for logs and tests.
	Reason string `json:"reason,omitempty"`
	// Fix says where to turn it on again.
	Fix *Fix `json:"fix,omitempty"`
	// Provider and ProviderTitle name the add-on whose absence turned memory
	// off, with the code memory_provider_unavailable.
	Provider      string `json:"provider,omitempty"`
	ProviderTitle string `json:"provider_title,omitempty"`
}

// The fixes of a memory switch that is off.
const (
	// FixOpenMemorySettings points at the Memory tab of the account settings.
	FixOpenMemorySettings = "open_memory_settings"
	// FixOpenToolsMenu points at the Tools menu of the chat.
	FixOpenToolsMenu = "open_tools_menu"
)

// memoryReason words a state.
func memoryReason(st MemoryState, rec session.ChatRecord) MemoryReason {
	out := MemoryReason{On: st.On, Code: st.Code, By: st.By}
	switch st.Code {
	case MemoryOffChat:
		out.Reason = "Memory is off in this chat: it is a private chat."
		out.Fix = &Fix{Kind: FixOpenToolsMenu, Target: "memory"}
	case MemoryOffAgent:
		out.Reason = "Memory is off for the agent of this chat."
		out.Fix = &Fix{Kind: FixOpenAgent, Target: rec.AgentID, Field: st.Field}
	case MemoryOffUser:
		out.Reason = "Memory is off in your settings."
		out.Fix = &Fix{Kind: FixOpenMemorySettings, Target: "memory"}
	case MemoryOffProvider:
		out.Reason = "The add-on that serves memory is not available, so memory is off until you pick a provider."
		out.Provider, out.ProviderTitle = st.Provider, st.ProviderTitle
		out.Fix = &Fix{Kind: FixOpenMemorySettings, Target: "memory"}
	case MemoryOffFiles:
		out.Reason = "Memory needs files, and files are off on this server."
		out.Fix = &Fix{Kind: FixOpenServerSetting, Target: "files"}
	}
	return out
}

// ChatMemory says whether memory works in a chat and, when it does not, which
// level turned it off.
func (s *Service) ChatMemory(ctx context.Context, rec session.ChatRecord) MemoryReason {
	legacy := legacyMemoryOf(s.addonContextSets(ctx, rec.Principal))
	return memoryReason(s.memoryState(ctx, rec, legacy), rec)
}

// registerMemoryGroup puts memory in the Tools menu as one switch group. The
// group has no tool of its own to build: the host reads the switch when it
// builds the prompt, so a private chat has no notes in it and no context_read.
// Build only says whether the switch can work now, and why not.
func (s *Service) registerMemoryGroup() error {
	if !s.d.Files.Available() {
		return nil // notes are files: without them there is no switch to show
	}
	return s.builtins.Register(builtins.Tool{
		ID: contextfiles.ToolName, Group: builtins.GroupMemory, Label: "Memory", External: true, Unlisted: true,
		Description: "Read the notes of the user's memory.",
		Build: func(ctx context.Context, c builtins.Chat) (session.Tool, bool, string) {
			switch {
			case !s.d.Files.Available():
				return session.Tool{}, false, "memory needs files, and files are off on this server"
			case c.SubAgent:
				return session.Tool{}, false, "a sub-agent follows the memory of its parent chat"
			case !s.memoryUserOn(ctx, c.Actor.Principal, legacyMemoryOf(s.addonContextSets(ctx, c.Actor.Principal))):
				return session.Tool{}, false, "memory is off in your settings"
			}
			return session.Tool{}, true, ""
		},
	})
}
