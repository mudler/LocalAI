package studio

import (
	"context"
	"slices"

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
// The code that does this is behind MemoryProvider so that a later change can
// let an add-on serve the same job. The built-in provider is the reference.
//
// Until the add-on "memory" is retired its manifest still declares the same
// index files and skills. The built-in wins: its declarations are ignored while
// the host serves memory (see addonContextSources and addonSkillsFor), so
// nothing loads twice. The add-on stays installed and harmless.

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
}

func memoryOff(code, by string) MemoryState { return MemoryState{Code: code, By: by} }

// memoryState resolves the switch of a chat from what the host knows. legacy
// is the Memory add-on, for the person who never chose.
func (s *Service) memoryState(ctx context.Context, rec session.ChatRecord, legacy legacyMemory) MemoryState {
	switch {
	case !s.d.Files.Available():
		return memoryOff(MemoryOffFiles, "server")
	case slices.Contains(rec.AddonsOff, MemoryAddonID):
		// The chat switched the Memory add-on off: the same switch, in the
		// words it had before memory was built in.
		return memoryOff(MemoryOffChat, "chat")
	case !s.memoryUserOn(ctx, rec.Principal, legacy):
		return memoryOff(MemoryOffUser, "user")
	}
	return MemoryState{On: true}
}

// memoryUserOn is the person's own switch. Without a stored answer memory is
// on, but for a person who disabled the Memory add-on to stop it: the disabled
// add-on keeps meaning "off" until they choose. A store that cannot answer
// reads as off: a failure never reads notes the person may have switched off.
func (s *Service) memoryUserOn(ctx context.Context, user string, legacy legacyMemory) bool {
	if s.d.UserSetting != nil {
		v, ok, err := s.d.UserSetting(ctx, principal(user), HostSettingsID, MemoryEnabledKey)
		switch {
		case err != nil:
			s.log.Warn("studio: the memory switch could not be read, memory stays off for this turn", "error", err)
			return false
		case ok && v == MemoryOn:
			return true
		case ok && v == MemoryOff:
			return false
		}
	}
	return !legacy.Installed || legacy.Running
}

// The storage of the host settings of a person. They sit in the same store as
// the settings of the add-ons, under an ID that no add-on can have (an add-on
// ID starts with a letter).
const (
	// HostSettingsID is the owner of the host's own per-person settings.
	HostSettingsID = "_host"
	// MemoryEnabledKey holds MemoryOn or MemoryOff.
	MemoryEnabledKey = "memory"
	// MemorySaveKey holds the save level: ask, auto or never.
	MemorySaveKey = "memory-save-mode"
)

// The values of MemoryEnabledKey.
const (
	MemoryOn  = "on"
	MemoryOff = "off"
)

// memoryReads says whether the chat reads the notes. A saved agent reads them
// only when it names the Memory add-on, as before; the choice per agent comes
// later. A chat without an agent reads them.
func (s *Service) memoryReads(ctx context.Context, rec session.ChatRecord, sc *scope, legacy legacyMemory) bool {
	if !s.memoryState(ctx, rec, legacy).On {
		return false
	}
	return sc == nil || sc.addons[MemoryAddonID]
}

// memorySkillsOff adds the memory skills to the skills a chat has switched off
// when the chat does not read the notes: a skill that says "read your notes"
// has no use then, and the model must not look for them.
func (s *Service) memorySkillsOff(ctx context.Context, rec session.ChatRecord, sc *scope, off []string) []string {
	legacy := legacyMemoryOf(s.addonContextSets(ctx, rec.Principal))
	if s.memoryReads(ctx, rec, sc, legacy) {
		return off
	}
	return append(slices.Clone(off), s.memory.Skills()...)
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
	return s.memoryState(ctx, rec, legacy).On && !slices.Contains(c.GroupsOff, builtins.GroupMemory)
}
