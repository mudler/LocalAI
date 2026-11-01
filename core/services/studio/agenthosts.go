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
	return a.Fenced(workspace.FenceRoot{Node: sc.node, Mode: workspace.ModeRead})
}

// contains says whether the workspace path abs is the folder or inside it.
func (sc *folderScope) contains(abs string) bool {
	return abs == sc.path || strings.HasPrefix(abs, sc.path+"/")
}

// guestHome is where a chat of a scoped agent sees the agent's folder.
const guestHome = session.GuestRoot + "/" + session.GuestHome

// guest returns abs as the agent's chats see it, under /workspace/home, and
// false for a path outside the folder or no folder.
func (sc *folderScope) guest(abs string) (string, bool) {
	if sc == nil || !sc.contains(abs) {
		return "", false
	}
	return guestHome + strings.TrimPrefix(abs, sc.path), true
}

// inFolder turns a path that a chat of a scoped agent names (a path of its
// place: "/In", "In" or "/workspace/home/In") into the workspace path inside
// the agent's folder. Any other mount of the place, a parent and a bad name
// are refused: the agent's own chats can only name its folder.
func (sc *folderScope) inFolder(p string) (string, error) {
	if err := session.CheckGuestPath(p); err != nil {
		return "", err
	}
	p = "/" + strings.Trim(p, "/")
	switch {
	case p == guestHome:
		p = "/"
	case strings.HasPrefix(p, guestHome+"/"):
		p = strings.TrimPrefix(p, guestHome)
	case p == session.GuestRoot || strings.HasPrefix(p, session.GuestRoot+"/"):
		return "", workspace.ErrNotFound
	}
	segs, err := workspace.SplitPath(p)
	if err != nil {
		return "", err
	}
	return sc.path + strings.TrimSuffix(workspace.JoinPath(segs), "/"), nil
}
