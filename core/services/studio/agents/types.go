// Package agents keeps the saved agents of the chats: a name, instructions and
// the lists of things the agent may use. A definition grants nothing by
// itself; the chat that runs it gets the intersection of these lists and what
// its owner may use at that moment.
//
// Two stores exist: NewJSON (a file under a data directory, or memory when the
// directory is empty) and NewGORM (SQLite or PostgreSQL). agentstest.Run checks
// both.
package agents

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/connsets"
	"github.com/mudler/LocalAI/core/services/studio/resourcesets"
)

// Errors. An agent that does not exist, or belongs to another principal, is
// ErrNotFound for every method.
var (
	ErrNotFound  = errors.New("agents: not found")
	ErrNameTaken = errors.New("agents: the user already has an agent with this name")
	// ErrInvalid wraps the reason a definition is refused.
	ErrInvalid = errors.New("agents: invalid agent")
)

// Limits of a definition.
const (
	MaxNameRunes    = 80
	MaxDescription  = 400
	MaxInstructions = 32 << 10 // bytes
	MaxListEntries  = 100
	MaxEntryRunes   = 200
	MaxModelRunes   = 200
)

// Agent is a saved agent. The four lists are exact grants: an empty list
// grants nothing.
type Agent struct {
	ID, Principal string
	Name          string
	Description   string
	Instructions  string
	// Model is the model a chat with this agent talks to. Empty: the model the
	// chat is created with.
	Model       string
	MCPServers  []string // names of servers in the model's MCP configuration
	Addons      []string // add-on IDs
	Collections []string // collection names
	Skills      []string // skill names
	// BuiltinsOff are the groups of built-in tools (web, files, triggers...)
	// the agent's chats never have, and BuiltinsOn the opt-in groups (saved
	// agents) they have from the start. They are a ceiling: a chat of the
	// agent cannot switch on a group listed in BuiltinsOff. Empty: every
	// group a chat has by default, as agents saved before the field.
	BuiltinsOff []string
	BuiltinsOn  []string
	// Builtins are the built-in capabilities the owner granted the agent, by
	// the names of core/services/studio/builtins (memory.write, skills.write
	// and so on). Empty grants nothing: no grant is on by default, and a chat
	// gets the intersection with what its user may use at the turn.
	Builtins []string
	// Memory is the agent's choice of notes: MemoryOwn, MemoryMineAndOwn,
	// MemoryMine or MemoryNone. Empty is "as before", an agent saved before
	// the choice existed: it reads the notes when its add-ons name Memory and
	// the model picks the scope of a note. The host enforces any other value.
	Memory string
	// MemorySave is the agent's own rule for saving a note: MemorySaveAlways,
	// MemorySaveAuto or MemorySaveNever. Empty uses the person's setting.
	// Always and never can only be stricter than the person's setting; auto is
	// the one explicit choice that loosens it, for this agent only.
	MemorySave string
	// Connections names the connections (connector binding IDs) the agent's
	// chats may use for each add-on whose tools run with a credential, by
	// add-on ID. The owner chooses them; a chat of the agent cannot change
	// them. An add-on with no entry gets none from the agent: the person
	// chooses in the chat. The service checks that each binding is the
	// owner's and belongs to the add-on.
	Connections connsets.Sets
	// Resources names, for each add-on whose resources are scoped per agent
	// (the manifest opts in), the resources the agent may use and the mode of
	// each, read or write, by add-on ID. An add-on of that kind with no entry
	// gives the agent no resource. The field has no effect on an add-on that
	// does not opt in. The host enforces it on every tool call that takes a
	// resource, on top of what the owner of the chat may use.
	Resources resourcesets.Sets
	// Network is the network preset of the Sandbox commands of the agent's
	// unattended runs (triggers, connectors): off, registries or all, capped
	// by the administrator's ceiling. Empty is off. A run never asks a
	// person: a host outside the preset is blocked.
	Network string
	// Folders are the folders the agent works in, at most MaxFolders, in the
	// order the person gave them. Each is kept by workspace node so a rename
	// or a move of the folder is followed. No folder means the agent works on
	// the whole home. An entry with node 0 is a folder that is not chosen yet
	// (an import whose folder was not found): the agent must not start.
	Folders []AgentFolder
	// FolderID and FolderMode are the first entry of Folders, kept for one
	// release so a rollback to a server that knows one folder still works.
	//
	// Deprecated: use Folders. A store fills Folders from these two fields
	// when it reads an agent that has only them, and fills these two from the
	// first of Folders when it writes.
	FolderID   int64
	FolderMode string
	// ShareRef is the ID of the sharing record of the agent in the sharing
	// policy (kind "agent"), empty until the owner first shares it. Only
	// SetShareRef sets it: Put keeps the stored value whatever the caller
	// sends, so an edit of the definition never drops the grants.
	ShareRef string
	Created  time.Time
	Updated  time.Time
}

// The choices of Agent.Memory. Empty (not listed) is "as before".
const (
	// MemoryOwn: read and write the notes of this agent only.
	MemoryOwn = "own"
	// MemoryMineAndOwn: read the person's notes and the agent's own, write the
	// agent's own. The default of a new agent.
	MemoryMineAndOwn = "mine_and_own"
	// MemoryMine: read and write the person's notes.
	MemoryMine = "mine"
	// MemoryNone: read and write nothing.
	MemoryNone = "none"
)

// The values of Agent.MemorySave. Empty uses the person's setting.
const (
	// MemorySaveAlways: every note waits for the person, and the model cannot
	// undo or forget by itself.
	MemorySaveAlways = "always"
	// MemorySaveNever: the agent reads notes and never saves one.
	MemorySaveNever = "never"
	// MemorySaveAuto: the agent saves a note at once and tells the person in
	// the chat, with an Undo, even when the person's own level is Ask me
	// first. It is the one value that loosens, and only for this agent: the
	// person's Always confirm and Never still hold, and so do the limits of an
	// automatic save (see studio.effectiveSaveLevel).
	MemorySaveAuto = "auto"
	// MemorySaveUseMine is what a client writes to clear the override. It is
	// never stored: the stored value is empty.
	MemorySaveUseMine = "use_mine"
)

// ValidMemorySave reports whether v is a value of Agent.MemorySave, or empty.
func ValidMemorySave(v string) bool {
	return v == "" || v == MemorySaveAlways || v == MemorySaveAuto || v == MemorySaveNever
}

// ValidMemory reports whether v is a choice of Agent.Memory, or empty.
func ValidMemory(v string) bool {
	switch v {
	case "", MemoryOwn, MemoryMineAndOwn, MemoryMine, MemoryNone:
		return true
	}
	return false
}

// MaxFolders is the most folders one agent may have.
const MaxFolders = 8

// AgentFolder is one folder of an agent: a workspace node and the most the
// agent may do there. Where one folder is inside another, the outer mode caps
// the inner one.
type AgentFolder struct {
	Node int64
	Mode string // FolderRead or FolderWrite
}

// The access modes of an agent's folder.
const (
	FolderRead  = "read"
	FolderWrite = "write"
)

// HasFolder reports whether the agent is limited to folders, chosen or not.
func (a Agent) HasFolder() bool { return len(a.FolderList()) > 0 }

// FolderList returns the folders of the agent. An agent read from a store that
// kept one folder in FolderID and FolderMode has them as a one-entry list.
func (a Agent) FolderList() []AgentFolder {
	if len(a.Folders) > 0 {
		return a.Folders
	}
	if a.FolderMode != "" {
		return []AgentFolder{{Node: a.FolderID, Mode: a.FolderMode}}
	}
	return nil
}

// normalizeFolders makes Folders and the deprecated pair agree: Folders is the
// truth, the pair mirrors its first entry, and the slice is a copy.
func (a Agent) normalizeFolders() Agent {
	list := a.FolderList()
	a.Folders = append([]AgentFolder(nil), list...)
	if len(list) == 0 {
		a.Folders, a.FolderID, a.FolderMode = nil, 0, ""
		return a
	}
	a.FolderID, a.FolderMode = list[0].Node, list[0].Mode
	return a
}

// Store is the storage of saved agents.
type Store interface {
	// Put inserts the agent or replaces the one with the same ID and principal.
	// An ID that belongs to another principal is ErrNotFound. Created is kept
	// when the agent exists; Updated is set by the caller.
	Put(ctx context.Context, a Agent) error
	Get(ctx context.Context, principal, id string) (Agent, error)
	// List returns the agents of the principal sorted by name, case-insensitively.
	List(ctx context.Context, principal string) ([]Agent, error)
	Delete(ctx context.Context, principal, id string) error
	// ListAll returns the agents of every principal sorted by principal, then
	// name. It is for the administrator's view and for migrations.
	ListAll(ctx context.Context) ([]Agent, error)
	// SetShareRef records the sharing reference of an agent of the principal
	// when it has none and returns the one stored: the first wins, so two
	// concurrent first shares agree on one record. ErrNotFound when the agent
	// is not the principal's.
	SetShareRef(ctx context.Context, principal, id, ref string) (string, error)
	// GetShared returns an agent by ID whatever its principal, only when it
	// has a sharing reference; any other agent is ErrNotFound. The caller
	// must check the grant on the reference before it uses the agent.
	GetShared(ctx context.Context, id string) (Agent, error)
	// ListShared returns the agents whose sharing reference is in refs, in
	// any order. A reference with no agent is skipped.
	ListShared(ctx context.Context, refs []string) ([]Agent, error)
}

var addonIDShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// NameKey is the form of a name that must be unique per user.
func NameKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// Validate checks the limits of a definition. The error wraps ErrInvalid.
func Validate(a Agent) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(a.Name)); n == 0 || n > MaxNameRunes {
		return bad("the name must have 1 to %d characters", MaxNameRunes)
	}
	if utf8.RuneCountInString(a.Description) > MaxDescription {
		return bad("the description is longer than %d characters", MaxDescription)
	}
	if len(a.Instructions) > MaxInstructions {
		return bad("the instructions are longer than %d bytes", MaxInstructions)
	}
	if utf8.RuneCountInString(a.Model) > MaxModelRunes {
		return bad("the model name is longer than %d characters", MaxModelRunes)
	}
	if !ValidMemory(a.Memory) {
		return bad("memory must be own, mine_and_own, mine or none")
	}
	if !ValidMemorySave(a.MemorySave) {
		return bad("memory_save must be always, auto or never")
	}
	switch a.Network {
	case "", "off", "registries", "all":
	default:
		return bad("the network must be off, registries or all")
	}
	if len(a.Connections) > MaxListEntries {
		return bad("connections has more than %d entries", MaxListEntries)
	}
	for addon, ids := range a.Connections {
		if !addonIDShape.MatchString(addon) || len(ids) == 0 || len(ids) > MaxListEntries {
			return bad("a connection needs an add-on ID and one or more binding IDs")
		}
		for _, binding := range ids {
			if strings.TrimSpace(binding) == "" || utf8.RuneCountInString(binding) > MaxEntryRunes {
				return bad("a connection needs an add-on ID and a binding ID")
			}
		}
	}
	if err := resourcesets.Validate(a.Resources); err != nil {
		return bad("%s", err)
	}
	if a.FolderMode == "" && a.FolderID != 0 && len(a.Folders) == 0 {
		return bad("a folder needs a mode, read or write")
	}
	if err := ValidateFolders(a.FolderList()); err != nil {
		return err
	}
	for _, l := range []struct {
		name    string
		entries []string
		addon   bool
	}{{"MCP servers", a.MCPServers, false}, {"add-ons", a.Addons, true}, {"collections", a.Collections, false}, {"skills", a.Skills, false}, {"built-in tool groups off", a.BuiltinsOff, false}, {"built-in tool groups on", a.BuiltinsOn, false}, {"built-in grants", a.Builtins, false}} {
		if len(l.entries) > MaxListEntries {
			return bad("%s has more than %d entries", l.name, MaxListEntries)
		}
		for _, e := range l.entries {
			switch {
			case l.addon && !addonIDShape.MatchString(e):
				return bad("%q is not an add-on ID", e)
			case strings.TrimSpace(e) == "":
				return bad("%s has an empty entry", l.name)
			case utf8.RuneCountInString(e) > MaxEntryRunes:
				return bad("an entry of %s is longer than %d characters", l.name, MaxEntryRunes)
			}
		}
	}
	return nil
}

// ValidateFolders checks a folder list: at most MaxFolders entries, a mode
// on each, no node twice. Node 0 (not chosen yet) may repeat. The error wraps
// ErrInvalid. A caller that knows which nodes are shared folders must also
// refuse write on those; the store does not know.
func ValidateFolders(list []AgentFolder) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}
	if len(list) > MaxFolders {
		return bad("an agent may have at most %d folders", MaxFolders)
	}
	seen := map[int64]bool{}
	for _, f := range list {
		switch f.Mode {
		case FolderRead, FolderWrite:
		case "":
			return bad("a folder needs a mode, read or write")
		default:
			return bad("the folder mode must be read or write")
		}
		if f.Node < 0 {
			return bad("a folder node cannot be negative")
		}
		if f.Node != 0 && seen[f.Node] {
			return bad("the same folder is listed twice")
		}
		seen[f.Node] = true
	}
	return nil
}

// clone copies the lists so a caller cannot change a stored agent.
func (a Agent) clone() Agent {
	a.MCPServers = append([]string(nil), a.MCPServers...)
	a.Addons = append([]string(nil), a.Addons...)
	a.Collections = append([]string(nil), a.Collections...)
	a.Skills = append([]string(nil), a.Skills...)
	a.BuiltinsOff = append([]string(nil), a.BuiltinsOff...)
	a.BuiltinsOn = append([]string(nil), a.BuiltinsOn...)
	a.Builtins = append([]string(nil), a.Builtins...)
	a.Connections = connsets.Clone(a.Connections)
	a.Resources = resourcesets.Clone(a.Resources)
	return a.normalizeFolders()
}
