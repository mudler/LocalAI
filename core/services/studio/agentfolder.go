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
	Name  string
	Mode  string
	State string
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

// AgentFolder returns the folder of an agent as it is now, and false for an
// agent that works on the whole home.
func (s *Service) AgentFolder(ctx context.Context, user string, a agents.Agent) (AgentFolderState, bool) {
	st, ok := s.AgentFolders(ctx, user, []agents.Agent{a})[a.ID]
	return st, ok
}

// AgentFolders returns the folders of the agents, by agent ID. An agent
// without a folder is not in the result.
func (s *Service) AgentFolders(ctx context.Context, user string, list []agents.Agent) map[string]AgentFolderState {
	p := principal(user)
	out := map[string]AgentFolderState{}
	var nodes []int64
	for _, a := range list {
		if a.HasFolder() && a.FolderID > 0 {
			nodes = append(nodes, a.FolderID)
		}
	}
	paths := folderResolver{f: s.d.Files}.pathsOf(ctx, p, nodes)
	for _, a := range list {
		if !a.HasFolder() {
			continue
		}
		st := AgentFolderState{Mode: a.FolderMode, State: AgentFolderUnset}
		if a.FolderID > 0 {
			st.State = AgentFolderMissing
			if abs, ok := paths[a.FolderID]; ok {
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
			}
		}
		out[a.ID] = st
	}
	return out
}
