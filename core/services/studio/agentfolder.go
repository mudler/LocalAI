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
	"github.com/mudler/LocalAI/core/services/studio/sharedfolders"
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
	// (/shared/<name>/...). Empty when the folder is missing or unset.
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

// nodeFinder is a workspace store that finds a live node by its ID for an
// actor. The stores do not have it yet; when one does, PathOf uses it instead
// of searching.
type nodeFinder interface {
	NodeByID(ctx context.Context, a workspace.Actor, id int64) (workspace.Node, error)
}

// Bounds of the search for a folder by node.
const (
	// folderJournalScan is how many of the newest change sets of the home
	// are read to find where a node is now.
	folderJournalScan = 1000
	// maxFolderWalk is how many folders the search lists, at most, when the
	// journal did not say where the node is.
	maxFolderWalk = 20000
)

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

// pathsOf finds where each node is now, as the user sees it, in one search.
// A node that is missing from the result is deleted, unreadable or not a
// folder. The search reads the store at every call: nothing is cached, so
// every frontend gives the same answer.
func (r folderResolver) pathsOf(ctx context.Context, principal string, nodes []int64) map[int64]string {
	out := map[int64]string{}
	if !r.f.Available() || len(nodes) == 0 {
		return out
	}
	ws, actor := r.f.Store(), r.f.Actor(principal)
	want := map[int64]bool{}
	for _, n := range nodes {
		if n > 0 {
			want[n] = true
		}
	}
	found := func(n workspace.Node) {
		if want[n.ID] && n.Kind == workspace.KindDir {
			out[n.ID] = n.Path
			delete(want, n.ID)
		}
	}
	if nf, ok := ws.(nodeFinder); ok {
		for id := range want {
			if n, err := nf.NodeByID(ctx, actor, id); err == nil {
				found(n)
			}
		}
		return out
	}
	// The journal names the newest path of a node that changed lately. The
	// path is checked with Stat, because a move or a delete of a parent
	// changes the node without a row of its own.
	home := session.HomePath(principal)
	if hist, err := ws.History(ctx, actor, workspace.HistoryQuery{Path: home, Limit: folderJournalScan}); err == nil {
		seen := map[int64]bool{}
		for _, cs := range hist {
			for _, op := range cs.Ops {
				if !want[op.NodeID] || seen[op.NodeID] {
					continue
				}
				seen[op.NodeID] = true
				if op.Kind == workspace.OpDelete || op.NewPath == "" {
					continue
				}
				if n, err := ws.Stat(ctx, actor, op.NewPath); err == nil && n.ID == op.NodeID {
					found(n)
				}
			}
		}
	}
	if len(want) == 0 {
		return out
	}
	// Last, list the folders the user can read: the home and the shared
	// folders, breadth first.
	queue := []string{home}
	if prefixes, err := ws.ReadablePrefixes(ctx, actor); err == nil {
		for _, p := range prefixes {
			if strings.HasPrefix(p, sharedfolders.Root+"/") {
				queue = append(queue, p)
			}
		}
	}
	for _, root := range queue {
		if n, err := ws.Stat(ctx, actor, root); err == nil {
			found(n)
		}
	}
	for listed := 0; len(queue) > 0 && len(want) > 0 && listed < maxFolderWalk; listed++ {
		dir := queue[0]
		queue = queue[1:]
		children, err := ws.List(ctx, actor, dir)
		if err != nil {
			continue
		}
		for _, c := range children {
			if c.Kind == workspace.KindDir {
				found(c)
				queue = append(queue, c.Path)
			}
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

// AgentFolders returns the folders of the agents, by agent ID, with one search
// of the user's files. An agent without a folder is not in the result.
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
				st.State, st.Path = AgentFolderOK, abs
				if rel := s.d.Files.Rel(p, abs); rel != "" {
					st.Path = rel
				}
				if st.Path != "/" {
					st.Name = path.Base(st.Path)
				}
			}
		}
		out[a.ID] = st
	}
	return out
}
