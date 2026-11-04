package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// SaveAgent creates a saved agent of the user (a.ID empty) or replaces one (a.ID
// set; it must be the user's). The caller's Principal, Created and Updated are
// ignored. A replace keeps the stored folder and mode: SaveAgentWithFolder
// changes them. It returns the stored agent.
func (s *Service) SaveAgent(ctx context.Context, user string, a agents.Agent) (agents.Agent, error) {
	return s.SaveAgentWithFolder(ctx, user, a, nil)
}

// SaveAgentWithFolder is SaveAgent with a choice of the agent's folder. A nil
// folder keeps the stored folders on a replace, so a caller that does not know
// about folders cannot widen an agent, and takes a.Folders (or the old
// FolderID and FolderMode) as they are on a create. A folder with an empty
// path clears the folders; any other path must be a folder of the user's home
// (see FolderResolver.NodeOf). It sets exactly one folder: SaveAgentWithFolders
// takes a list.
func (s *Service) SaveAgentWithFolder(ctx context.Context, user string, a agents.Agent, folder *AgentFolderWrite) (agents.Agent, error) {
	if folder == nil {
		return s.SaveAgentWithFolders(ctx, user, a, nil)
	}
	list := []AgentFolderWrite{}
	if folder.Path != "" {
		list = append(list, *folder)
	}
	return s.SaveAgentWithFolders(ctx, user, a, &list)
}

// SaveAgentWithFolders is SaveAgent with a choice of the agent's folders. A
// nil list keeps the stored folders on a replace and takes a.Folders as they
// are on a create. An empty list clears them. Otherwise each row is a folder of
// the user's home and its mode; at most agents.MaxFolders rows.
func (s *Service) SaveAgentWithFolders(ctx context.Context, user string, a agents.Agent, folders *[]AgentFolderWrite) (agents.Agent, error) {
	if s.d.Agents == nil {
		return agents.Agent{}, ErrAgentsOff
	}
	p := principal(user)
	keepOff, keepOn, keepGrants := a.BuiltinsOff == nil, a.BuiltinsOn == nil, a.Builtins == nil
	keepConnections := a.Connections == nil
	grants, err := cleanGrants(a.Builtins, false)
	if err != nil {
		return agents.Agent{}, fmt.Errorf("%w: %w", agents.ErrInvalid, err)
	}
	a.Builtins = grants
	if folders != nil {
		list, err := s.resolveAgentFolders(ctx, p, *folders)
		if err != nil {
			return agents.Agent{}, err
		}
		a.Folders, a.FolderID, a.FolderMode = list, 0, ""
	}
	if err := agents.Validate(a); err != nil {
		return agents.Agent{}, err
	}
	if err := s.checkConnections(ctx, p, a.Connections); err != nil {
		return agents.Agent{}, err
	}
	a.BuiltinsOff, a.BuiltinsOn = s.agentGroups(a.BuiltinsOff, false), s.agentGroups(a.BuiltinsOn, true)
	if folders == nil {
		if err := s.checkSharedRead(ctx, p, a.FolderList()); err != nil {
			return agents.Agent{}, err
		}
	}
	a.Principal, a.Name = p, trimName(a.Name)
	now := time.Now().UTC()
	a.Updated = now
	if a.ID == "" {
		a.ID, a.Created = newID(), now
	} else {
		old, err := s.d.Agents.Get(ctx, p, a.ID)
		if err != nil {
			return agents.Agent{}, err
		}
		if folders == nil {
			a.Folders, a.FolderID, a.FolderMode = old.Folders, old.FolderID, old.FolderMode
		}
		// A write that does not mention the tool switches keeps them.
		if keepOff {
			a.BuiltinsOff = old.BuiltinsOff
		}
		if keepOn {
			a.BuiltinsOn = old.BuiltinsOn
		}
		// Likewise for the grants: a client that does not know them cannot
		// widen or narrow the agent.
		if keepGrants {
			a.Builtins = old.Builtins
		}
		// And for the connections: only the owner who sends the field changes
		// which credential the agent's chats use.
		if keepConnections {
			a.Connections = old.Connections
		}
	}
	// The grant saved_agents is the opt-in group of the same name: the chats of
	// the agent start with it on.
	if slices.Contains(a.Builtins, builtins.GrantSavedAgents) && s.builtins.IsGroup(builtins.GroupSavedAgents) && !slices.Contains(a.BuiltinsOn, builtins.GroupSavedAgents) {
		a.BuiltinsOn = append(slices.Clone(a.BuiltinsOn), builtins.GroupSavedAgents)
	}
	if err := s.d.Agents.Put(ctx, a); err != nil {
		return agents.Agent{}, err
	}
	return s.d.Agents.Get(ctx, p, a.ID)
}

// Agent returns a saved agent of the user.
func (s *Service) Agent(ctx context.Context, user, id string) (agents.Agent, error) {
	if s.d.Agents == nil {
		return agents.Agent{}, ErrAgentsOff
	}
	return s.d.Agents.Get(ctx, principal(user), id)
}

// Agents lists the saved agents of the user, sorted by name.
func (s *Service) Agents(ctx context.Context, user string) ([]agents.Agent, error) {
	if s.d.Agents == nil {
		return nil, ErrAgentsOff
	}
	return s.d.Agents.List(ctx, principal(user))
}

// DeleteAgent removes a saved agent. Chats that ran with it keep their record;
// their next turn runs without tools and says the agent was deleted.
func (s *Service) DeleteAgent(ctx context.Context, user, id string) error {
	if s.d.Agents == nil {
		return ErrAgentsOff
	}
	return s.d.Agents.Delete(ctx, principal(user), id)
}

func trimName(n string) string { return strings.TrimSpace(n) }

// ErrNoTemplate means no starter agent has this ID.
var ErrNoTemplate = errors.New("studio: no such agent template")

// ImportAgent saves an agent that came from a file as a new agent of the user.
// When the user already has an agent with the name, " (2)", " (3)" and so on
// is added to the name. The ID, owner and times of the file are not used.
//
// folder is the folder the file names, or nil for a file without one. Its
// path is looked up in the importing user's home. When there is no such
// folder (or the path is empty), the agent keeps the mode and gets no folder,
// the unset state: it does not start until the user chooses one. It is never
// saved as an agent without a folder.
func (s *Service) ImportAgent(ctx context.Context, user string, a agents.Agent, folder *AgentFolderWrite) (agents.Agent, error) {
	if folder == nil {
		return s.ImportAgentFolders(ctx, user, a, nil)
	}
	return s.ImportAgentFolders(ctx, user, a, []AgentFolderWrite{*folder})
}

// ImportAgentFolders is ImportAgent for a file that lists several folders. A
// folder that is not found in the importing user's files stays in the list as
// a row that is not chosen, so the person can see which one to fix.
func (s *Service) ImportAgentFolders(ctx context.Context, user string, a agents.Agent, folders []AgentFolderWrite) (agents.Agent, error) {
	a.ID, a.FolderID, a.FolderMode, a.Folders = "", 0, "", nil
	// A file from a server that has more grants still imports; the grants
	// this server does not declare are dropped.
	a.Builtins, _ = cleanGrants(a.Builtins, true)
	if len(folders) > agents.MaxFolders {
		return agents.Agent{}, folderRefused("an agent may have at most %d folders", agents.MaxFolders)
	}
	p := principal(user)
	var list []agents.AgentFolder
	for _, folder := range folders {
		if folder.Mode != agents.FolderRead && folder.Mode != agents.FolderWrite {
			return agents.Agent{}, folderRefused("the mode must be read or write")
		}
		row := agents.AgentFolder{Mode: folder.Mode}
		if folder.Path != "" {
			id, _, err := s.resolveAgentFolder(ctx, p, folder)
			switch {
			case err == nil:
				row.Node = id
			case errors.Is(err, ErrAgentFolder), errors.Is(err, files.ErrUnavailable):
				// Unset: the user chooses the folder here.
			default:
				return agents.Agent{}, err
			}
		}
		// The same folder twice, or a shared folder with write, is not
		// carried over: the row is left unset.
		for _, o := range list {
			if row.Node != 0 && o.Node == row.Node {
				row.Node = 0
			}
		}
		list = append(list, row)
	}
	if err := s.checkSharedRead(ctx, p, list); err != nil {
		for i := range list {
			if list[i].Mode == agents.FolderWrite && list[i].Node > 0 {
				if path, ok := s.FolderResolver().PathOf(ctx, p, list[i].Node); ok && isSharedPath(path) {
					list[i].Mode = agents.FolderRead
				}
			}
		}
	}
	a.Folders = list
	name := trimName(a.Name)
	for n := 1; n <= maxImportRenames; n++ {
		a.Name = name
		if n > 1 {
			a.Name = fmt.Sprintf("%s (%d)", name, n)
		}
		saved, err := s.SaveAgent(ctx, user, a)
		if !errors.Is(err, agents.ErrNameTaken) {
			return saved, err
		}
	}
	return agents.Agent{}, agents.ErrNameTaken
}

// maxImportRenames is how many names an import tries before it gives up.
const maxImportRenames = 20

// CreateAgentFromTemplate saves a starter agent as an agent of the user. A
// non-empty name replaces the template's.
func (s *Service) CreateAgentFromTemplate(ctx context.Context, user, id, name string) (agents.Agent, error) {
	t, ok := agents.TemplateByID(id)
	if !ok {
		return agents.Agent{}, ErrNoTemplate
	}
	if strings.TrimSpace(name) == "" {
		name = t.Name
	}
	return s.SaveAgent(ctx, user, agents.Agent{Name: name, Description: t.Description, Instructions: t.Instructions})
}

// AgentRun is a chat that ran with a saved agent: one the person had with it,
// one a trigger started, or one a connector started.
type AgentRun struct {
	Chat session.ChatRecord
	Turn store.TurnInfo
}

// AgentHistory is the runs of a saved agent, newest first, with the counts of
// all of them.
type AgentHistory struct {
	Runs                   []AgentRun
	Total, Failed, Running int
}

// maxHistoryScan is how many of the user's newest chats are searched for the
// runs of an agent.
const maxHistoryScan = 5000

// AgentRuns returns the chats of the user that ran with the agent, newest
// first, up to limit, and counts all that are in the newest maxHistoryScan.
func (s *Service) AgentRuns(ctx context.Context, user, id string, limit int) (AgentHistory, error) {
	if _, err := s.Agent(ctx, user, id); err != nil {
		return AgentHistory{}, err
	}
	p := principal(user)
	var (
		h      AgentHistory
		cursor session.ChatCursor
		seen   int
	)
	for seen < maxHistoryScan {
		page, err := s.d.Store.ListChats(ctx, p, cursor, 200)
		if err != nil {
			return AgentHistory{}, err
		}
		for _, rec := range page {
			if rec.AgentID != id || rec.Kind == session.KindSubAgent {
				continue
			}
			info, err := s.d.Store.Turn(ctx, p, rec.ID)
			if err != nil {
				continue // deleted meanwhile
			}
			h.Total++
			switch {
			case info.Running:
				h.Running++
			case info.Last == store.TurnFailed || info.Last == store.TurnInterrupted:
				h.Failed++
			}
			if limit <= 0 || len(h.Runs) < limit {
				h.Runs = append(h.Runs, AgentRun{Chat: rec, Turn: info})
			}
		}
		seen += len(page)
		if len(page) < 200 {
			break
		}
		last := page[len(page)-1]
		cursor = session.ChatCursor{Updated: last.Updated, ID: last.ID}
	}
	return h, nil
}

// agentGroups keeps the switch groups of an agent that this server has, with
// the opt-in ones in on and the others in off. A group this server does not
// offer is dropped, not refused: an agent file from a server with more tools
// (files or collections on) must still import. A chat of the agent has the
// groups the server offers.
func (s *Service) agentGroups(groups []string, optIn bool) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		if !seen[g] && s.builtins.IsGroup(g) && s.builtins.IsOptIn(g) == optIn {
			seen[g] = true
			out = append(out, g)
		}
	}
	return out
}
