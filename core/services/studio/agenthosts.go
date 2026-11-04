package studio

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The hosts that start a chat of a saved agent by themselves (trigger runs
// and connectors) work before the chat exists: they check the agent's folder,
// read the folder rules and name files for the first message. They use the
// helpers here, so a host never reads more than the agent's chats can. The
// chat itself gets its access from chatAccess at every turn.

// agentScope returns the folder of the saved agent agentID of user p as it is
// now: nil for no agent or an agent without a folder, ErrAgentFolderMissing
// when the folder is missing or not chosen. The hosts that start a chat of an
// agent by themselves (trigger runs, connectors) check it before they make
// the chat or read a file for it.
func (s *Service) agentScope(ctx context.Context, p, agentID string) (*folderScope, error) {
	if agentID == "" {
		return nil, nil
	}
	ag, err := s.Agent(ctx, p, agentID)
	if err != nil {
		return nil, err
	}
	if !ag.HasFolder() {
		return nil, nil
	}
	return s.liveFolder(ctx, p, ag)
}

// agentReader is the actor with which a host reads the user's files for a
// saved agent before the agent's chat exists: the rules of a trigger run and
// the folder a file trigger watches. With a folder it is fenced to that
// folder, read only, so the host reads nothing the agent's chats could not;
// without one it is the user's actor, as a chat without a folder has.
func agentReader(p string, sc *folderScope) workspace.Actor {
	a := userChatActor(p, "")
	if sc == nil {
		return a
	}
	roots := make([]workspace.FenceRoot, 0, len(sc.folders))
	for _, f := range sc.folders {
		roots = append(roots, workspace.FenceRoot{Node: f.node, Mode: workspace.ModeRead})
	}
	return a.Fenced(roots...)
}

// contains says whether the workspace path abs is one of the folders or
// inside one.
func (sc *folderScope) contains(abs string) bool {
	_, ok := sc.holder(abs)
	return ok
}

// holder returns the folder that holds abs, and its index. When two folders
// hold it, the innermost one.
func (sc *folderScope) holder(abs string) (int, bool) {
	best := -1
	for i, f := range sc.folders {
		if workspace.Within(abs, f.path) && (best < 0 || len(f.path) > len(sc.folders[best].path)) {
			best = i
		}
	}
	return best, best >= 0
}

// mountNames are the names the chat's view gives the folders of the scope:
// "home" for the first, and "folder-<name>" for the others.
func (sc *folderScope) mountNames() []string {
	out := []string{session.GuestHome}
	return append(out, session.FolderMountNames(sc.extraPaths())...)
}

// guestHome is where a chat of a scoped agent sees the agent's first folder.
const guestHome = session.GuestRoot + "/" + session.GuestHome

// guest returns abs as the agent's chats see it: under /workspace/home for the
// first folder and under /workspace/folder-<name> for another. It is false for
// a path outside the folders or no folders.
func (sc *folderScope) guest(abs string) (string, bool) {
	if sc == nil {
		return "", false
	}
	i, ok := sc.holder(abs)
	if !ok {
		return "", false
	}
	return session.GuestRoot + "/" + sc.mountNames()[i] + strings.TrimPrefix(abs, sc.folders[i].path), true
}

// inFolder turns a path that a chat of a scoped agent names (a path of its
// place: "/In", "In", "/workspace/home/In" or "/workspace/folder-<name>/In")
// into the workspace path inside the agent's folders. Any other mount of the
// place, a parent and a bad name are refused: the agent's own chats can only
// name its folders.
func (sc *folderScope) inFolder(p string) (string, error) {
	if err := session.CheckGuestPath(p); err != nil {
		return "", err
	}
	p = "/" + strings.Trim(p, "/")
	root := sc.folders[0].path
	switch {
	case p == guestHome:
		p = "/"
	case strings.HasPrefix(p, guestHome+"/"):
		p = strings.TrimPrefix(p, guestHome)
	case p == session.GuestRoot || strings.HasPrefix(p, session.GuestRoot+"/"):
		name, rest, _ := strings.Cut(strings.TrimPrefix(p, session.GuestRoot+"/"), "/")
		found := false
		for i, at := range sc.mountNames() {
			if i > 0 && at == name {
				root, p, found = sc.folders[i].path, "/"+rest, true
				break
			}
		}
		if !found {
			return "", workspace.ErrNotFound
		}
	}
	segs, err := workspace.SplitPath(p)
	if err != nil {
		return "", err
	}
	return root + strings.TrimSuffix(workspace.JoinPath(segs), "/"), nil
}
