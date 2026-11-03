package studio

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// ErrAgentFolder wraps why a path cannot be the folder of a saved agent. The
// text after it is written for the person who chose the folder.
var ErrAgentFolder = errors.New("studio: the agent's folder")

// The states of an agent's folder, as AgentFolderState reports them.
const (
	// AgentFolderOK: the folder is live and the user can read it.
	AgentFolderOK = "ok"
	// AgentFolderMissing: the folder was deleted or the user can no longer
	// read it.
	AgentFolderMissing = "missing"
	// AgentFolderUnset: the agent has a mode but no folder yet (an import
	// whose folder was not found).
	AgentFolderUnset = "unset"
)

// AgentFolderState is the folder of an agent as it is now.
type AgentFolderState struct {
	// Path is where the folder is now, inside the user's home ("/" is the
	// whole home). A folder moved into a shared folder shows its path there
	// (/shared/<name>/...). Empty when missing, unset, or outside these public
	// locations; an unnamed readable folder still has state ok.
	Path string
	// Name is the folder's own name; empty for the whole home.
	Name string
	// Mode is the mode the person chose. Effective is the mode the folder has
	// in a chat: lower when the folder is shared (always read) or inside
	// another folder of the agent with a lower mode.
	Mode      string
	Effective string
	State     string
}

// AgentFolderWrite chooses the folder of an agent. Path is a folder of the
// user's home, "/" based; "" clears the folder. Mode is agents.FolderRead or
// agents.FolderWrite.
type AgentFolderWrite struct {
	Path string
	Mode string
}

// FolderResolver turns the folder a person chooses for an agent into the
// workspace node the agent keeps, and back. Both directions act as the user,
// never as the administrator, so neither can name a folder the user cannot
// read.
type FolderResolver interface {
	// NodeOf returns the node of a folder of the user's home (path is "/"
	// based, as the user sees it). The folder must be live and readable, and
	// not LocalAI/ or Chats/ or a folder inside them. A refusal wraps
	// ErrAgentFolder; files.ErrUnavailable means the server has no files.
	NodeOf(ctx context.Context, principal, path string) (int64, error)
	// PathOf returns the workspace path of a live folder the user can read,
	// and false when the node is deleted, unreadable or not a folder.
	PathOf(ctx context.Context, principal string, node int64) (path string, ok bool)
}

// FolderResolver returns the resolver of agent folders on the user's files.
func (s *Service) FolderResolver() FolderResolver { return folderResolver{f: s.d.Files} }

type folderResolver struct{ f *files.Service }

func folderRefused(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrAgentFolder, fmt.Sprintf(format, args...))
}

// managedFolder says whether a path inside the home is LocalAI/ or Chats/,
// or inside one of them. LocalAI keeps its settings and the chat folders
// there, and a chat's own folder is given to it apart from the agent's.
func managedFolder(rel string) bool {
	for _, dir := range []string{session.SettingsDir, session.ChatsDir} {
		if rel == "/"+dir || strings.HasPrefix(rel, "/"+dir+"/") {
			return true
		}
	}
	return false
}

func (r folderResolver) NodeOf(ctx context.Context, principal, p string) (int64, error) {
	if !r.f.Available() {
		return 0, fmt.Errorf("%w: %s", files.ErrUnavailable, r.f.Unavailable())
	}
	if err := r.f.EnsureHome(ctx, principal); err != nil {
		return 0, err
	}
	abs, err := r.f.Abs(principal, p)
	if err != nil {
		return 0, folderRefused("%q is not a folder path", p)
	}
	rel := r.f.Rel(principal, abs)
	if managedFolder(rel) {
		return 0, folderRefused("LocalAI manages %s; choose another folder", rel)
	}
	n, err := r.f.Store().Stat(ctx, r.f.Actor(principal), abs)
	switch {
	case errors.Is(err, workspace.ErrNotFound):
		return 0, folderRefused("there is no folder %s in your files", rel)
	case err != nil:
		return 0, err
	case n.Kind != workspace.KindDir:
		return 0, folderRefused("%s is a file, not a folder", rel)
	}
	return n.ID, nil
}

func (r folderResolver) PathOf(ctx context.Context, principal string, node int64) (string, bool) {
	p, ok := r.pathsOf(ctx, principal, []int64{node})[node]
	return p, ok
}

// pathsOf finds where each node is now, as the user sees it. A node that is
// missing from the result is deleted, unreadable or not a folder. It reads
// the store at every call: nothing is cached, so every frontend gives the
// same answer.
func (r folderResolver) pathsOf(ctx context.Context, principal string, nodes []int64) map[int64]string {
	out := map[int64]string{}
	if !r.f.Available() || len(nodes) == 0 {
		return out
	}
	ws, actor := r.f.Store(), r.f.Actor(principal)
	for _, id := range nodes {
		if _, done := out[id]; done || id <= 0 {
			continue
		}
		if n, err := ws.NodeByID(ctx, actor, id); err == nil && n.Kind == workspace.KindDir {
			out[id] = n.Path
		}
	}
	return out
}

// resolveAgentFolder turns a folder choice into the node and the mode an
// agent keeps. An empty path clears the folder.
func (s *Service) resolveAgentFolder(ctx context.Context, p string, w AgentFolderWrite) (int64, string, error) {
	if w.Path == "" {
		return 0, "", nil
	}
	if w.Mode != agents.FolderRead && w.Mode != agents.FolderWrite {
		return 0, "", folderRefused("the mode must be read or write")
	}
	id, err := s.FolderResolver().NodeOf(ctx, p, w.Path)
	if err != nil {
		return 0, "", err
	}
	return id, w.Mode, nil
}

// resolveAgentFolders turns the choices of a person into the list an agent
// keeps. Every row needs a path and a mode. The list may have at most
// agents.MaxFolders rows, no folder twice, and a shared folder only at read.
func (s *Service) resolveAgentFolders(ctx context.Context, p string, ws []AgentFolderWrite) ([]agents.AgentFolder, error) {
	if len(ws) > agents.MaxFolders {
		return nil, folderRefused("an agent may have at most %d folders", agents.MaxFolders)
	}
	out := make([]agents.AgentFolder, 0, len(ws))
	for i, w := range ws {
		if w.Path == "" {
			return nil, folderRefused("folder %d has no path", i+1)
		}
		id, mode, err := s.resolveAgentFolder(ctx, p, w)
		if err != nil {
			return nil, err
		}
		out = append(out, agents.AgentFolder{Node: id, Mode: mode})
	}
	if err := agents.ValidateFolders(out); err != nil {
		return nil, folderRefused("%s", strings.TrimPrefix(err.Error(), agents.ErrInvalid.Error()+": "))
	}
	return out, s.checkSharedRead(ctx, p, out)
}

// checkSharedRead refuses write on a folder that is in the shared folders.
func (s *Service) checkSharedRead(ctx context.Context, p string, list []agents.AgentFolder) error {
	for i, f := range list {
		if f.Mode != agents.FolderWrite || f.Node <= 0 {
			continue
		}
		if path, ok := s.FolderResolver().PathOf(ctx, p, f.Node); ok && isSharedPath(path) {
			return folderRefused("folder %d is a shared folder; it can only be read", i+1)
		}
	}
	return nil
}

// AgentFolder returns the first folder of an agent as it is now, and false
// for an agent that works on the whole home.
func (s *Service) AgentFolder(ctx context.Context, user string, a agents.Agent) (AgentFolderState, bool) {
	rows := s.AgentFolderRows(ctx, user, []agents.Agent{a})[a.ID]
	if len(rows) == 0 {
		return AgentFolderState{}, false
	}
	return rows[0], true
}

// AgentFolders returns the first folder of each agent, by agent ID. An agent
// without a folder is not in the result.
func (s *Service) AgentFolders(ctx context.Context, user string, list []agents.Agent) map[string]AgentFolderState {
	out := map[string]AgentFolderState{}
	for id, rows := range s.AgentFolderRows(ctx, user, list) {
		out[id] = rows[0]
	}
	return out
}

// AgentFolderRows returns every folder of the agents as it is now, by agent
// ID, in the order of the agent's list. An agent without a folder is not in
// the result.
func (s *Service) AgentFolderRows(ctx context.Context, user string, list []agents.Agent) map[string][]AgentFolderState {
	p := principal(user)
	out := map[string][]AgentFolderState{}
	var nodes []int64
	for _, a := range list {
		for _, f := range a.FolderList() {
			if f.Node > 0 {
				nodes = append(nodes, f.Node)
			}
		}
	}
	paths := folderResolver{f: s.d.Files}.pathsOf(ctx, p, nodes)
	for _, a := range list {
		fl := a.FolderList()
		if len(fl) == 0 {
			continue
		}
		// The effective modes follow the same rule as a chat's access.
		var live []scopedFolder
		for _, f := range fl {
			if abs, ok := paths[f.Node]; ok {
				mode := workspace.ModeRead
				if f.Mode == agents.FolderWrite && !isSharedPath(abs) {
					mode = workspace.ModeWrite
				}
				live = append(live, scopedFolder{node: f.Node, mode: mode, path: abs})
			}
		}
		capModes(live)
		effective := map[int64]workspace.Mode{}
		for _, l := range live {
			effective[l.node] = l.mode
		}
		for _, f := range fl {
			st := AgentFolderState{Mode: f.Mode, State: AgentFolderUnset}
			if f.Node > 0 {
				st.State = AgentFolderMissing
				if abs, ok := paths[f.Node]; ok {
					st.State = AgentFolderOK
					st.Path = s.d.Files.Rel(p, abs)
					// Shared paths are already public Files paths. Never expose an
					// internal workspace path for any other readable location.
					if st.Path == "" && strings.HasPrefix(abs, "/shared/") {
						st.Path = abs
					}
					if st.Path != "/" {
						st.Name = path.Base(abs)
					}
					st.Effective = agents.FolderRead
					if effective[f.Node] >= workspace.ModeWrite {
						st.Effective = agents.FolderWrite
					}
				}
			}
			out[a.ID] = append(out[a.ID], st)
		}
	}
	return out
}

// SharedNotMounted counts the shared folders that a chat of the agent leaves
// out because they do not fit the access limit. It is 0 for an agent without
// folders or whose folders cannot be found.
func (s *Service) SharedNotMounted(ctx context.Context, user string, a agents.Agent) int {
	if !a.HasFolder() {
		return 0
	}
	p := principal(user)
	sc, err := s.liveFolder(ctx, p, a)
	if err != nil {
		return 0
	}
	_, dropped, err := s.scopedRoots(ctx, userChatActor(p, ""), []*folderScope{sc})
	if err != nil {
		return 0
	}
	return dropped
}
