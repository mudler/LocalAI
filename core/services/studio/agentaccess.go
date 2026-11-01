package studio

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The access of a chat is resolved from the stores at every turn start: the
// chat record, its saved agent (or, for a sub-agent, the parent chat's agent)
// and the agent's folder node. Nothing is kept between turns, so an edit of
// the agent applies from the next turn on every frontend and chat worker
// (stateless per request, .agents/distributed-state.md).
//
// A chat whose agent has a folder gets a fenced actor: the store refuses
// every node outside the agent's folder, the chat's own folder, the shared
// folders and the skill layers, whoever calls it with that actor. A chat
// whose folder is missing or not chosen does not start
// (ErrAgentFolderMissing); it never falls back to the whole home.

// ErrAgentFolderMissing means the chat's agent has a folder that is deleted,
// that the user can no longer read, or that is not chosen yet, or that the
// chat's agent had a folder and is gone. The chat does not start.
var ErrAgentFolderMissing = errors.New("studio: the folder of this agent is missing")

// scopedChatMount is where the view of a scoped chat mounts the chat's own
// folder, next to the agent's folder at session.GuestHome.
const scopedChatMount = "chat"

// folderScope is the folder of a saved agent as it is now.
type folderScope struct {
	node int64
	mode workspace.Mode
	path string // workspace path
}

// userChatActor is the user's actor in a chat, without a fence: the actor of
// a chat whose agent has no folder, and the base that chatAccess fences. A
// person's own action on a chat (a delete) uses it too. Every other actor of
// a chat comes from chatAccess.
func userChatActor(p, conv string) workspace.Actor {
	a := files.UserActor(p)
	a.ChatID = conv
	return a
}

// ChatAccess returns the access of a chat for this turn, for code outside the
// package (command runners, add-on host operations). The chat of another user
// is session.ErrNotFound.
func (s *Service) ChatAccess(ctx context.Context, user, conv string) (session.Access, error) {
	rec, err := s.d.Store.GetChat(ctx, principal(user), conv)
	if err != nil {
		return session.Access{}, err
	}
	return s.chatAccess(ctx, rec)
}

// resolveAccess is the session's Resolve hook: the access of the chat of a.
func (s *Service) resolveAccess(ctx context.Context, a workspace.Actor) (session.Access, error) {
	return s.ChatAccess(ctx, a.Principal, a.ChatID)
}

// chatAccess is the access of a chat for this turn: its actor, fenced when the
// chat's agent (or, for a sub-agent, the parent's agent) has a folder, and the
// view of its folders. It makes the chat's folder before it fences, because a
// fenced actor cannot make a folder outside its fence. ErrAgentFolderMissing
// when the folder is missing or not chosen.
func (s *Service) chatAccess(ctx context.Context, rec session.ChatRecord) (session.Access, error) {
	base := userChatActor(rec.Principal, rec.ID)
	scopes, holder, err := s.chatScopes(ctx, rec, true)
	if err != nil {
		return session.Access{}, err
	}
	if len(scopes) == 0 {
		return s.mgr.UserAccess(ctx, base)
	}
	owner := base
	owner.ChatID = ""
	chatDir, err := s.mgr.EnsureChatFolder(ctx, owner, holder)
	if err != nil {
		return session.Access{}, fmt.Errorf("studio: make the chat's folder: %w", err)
	}
	actor := fenceScopes(base, scopes, chatDir.ID, s.readOnlyRoots(ctx, base))
	// The innermost folder is the one the chat works in: a sub-agent's own
	// folder inside its parent's. The store caps the mode by every layer.
	work := scopes[len(scopes)-1]
	v, err := s.d.Files.Store().View(ctx, actor, []workspace.MountRequest{
		{Path: work.path, At: session.GuestHome, Mode: work.mode},
		{Path: chatDir.Path, At: scopedChatMount, Mode: workspace.ModeWrite},
	})
	if errors.Is(err, workspace.ErrNotFound) {
		// The folder went away since it was checked, or a sub-agent's folder
		// is outside its parent's.
		return session.Access{}, ErrAgentFolderMissing
	}
	if err != nil {
		return session.Access{}, err
	}
	return session.NewAccess(actor, v)
}

// chatActorFor is the actor of a chat for the code that only lists what a
// chat could use (the tools panel): the same fence as chatAccess, without
// making the chat's folder. A chat whose folder is missing gets an actor that
// reaches nothing.
func (s *Service) chatActorFor(ctx context.Context, rec session.ChatRecord) workspace.Actor {
	base := userChatActor(rec.Principal, rec.ID)
	scopes, holder, err := s.chatScopes(ctx, rec, false)
	if err != nil {
		return base.Fenced()
	}
	if len(scopes) == 0 {
		return base
	}
	var chatNode int64
	if hrec, herr := s.d.Store.GetChat(ctx, rec.Principal, holder); herr == nil && hrec.Folder != "" {
		owner := base
		owner.ChatID = ""
		if n, serr := s.d.Files.Store().Stat(ctx, owner, session.ChatsPath(rec.Principal)+"/"+hrec.Folder); serr == nil {
			chatNode = n.ID
		}
	}
	return fenceScopes(base, scopes, chatNode, s.readOnlyRoots(ctx, base))
}

// fenceScopes fences base with one layer per scope. Each layer holds the
// scope's folder at its mode, the chat's folder (when chatNode is not 0) read
// and write, and the read-only roots; the layers intersect, so a sub-agent
// whose own agent has a folder reaches only what both folders allow.
func fenceScopes(base workspace.Actor, scopes []folderScope, chatNode int64, readOnly []workspace.FenceRoot) workspace.Actor {
	a := base
	for _, sc := range scopes {
		roots := []workspace.FenceRoot{{Node: sc.node, Mode: sc.mode}}
		if chatNode > 0 {
			roots = append(roots, workspace.FenceRoot{Node: chatNode, Mode: workspace.ModeWrite})
		}
		room := workspace.MaxFenceRoots - len(roots)
		roots = append(roots, readOnly[:min(room, len(readOnly))]...)
		a = a.Fenced(roots...)
	}
	return a
}

// readOnlyRoots lists the folders a scoped chat may read besides its own: the
// skill layers and the folders shared with the user, all read only (the
// shared folders' grants may allow more; a scoped chat never writes there).
// A folder that is missing or unreadable is left out. The skill layers come
// first, so a user with more shared folders than a fence holds keeps them.
func (s *Service) readOnlyRoots(ctx context.Context, base workspace.Actor) []workspace.FenceRoot {
	owner := base
	owner.ChatID = ""
	ws := s.d.Files.Store()
	var out []workspace.FenceRoot
	add := func(p string) {
		if n, err := ws.Stat(ctx, owner, p); err == nil && n.Kind == workspace.KindDir {
			out = append(out, workspace.FenceRoot{Node: n.ID, Mode: workspace.ModeRead})
		}
	}
	for _, p := range []string{files.SharedSkillsPath, files.BuiltinSkillsPath, files.AddonSkillsPath} {
		add(p)
	}
	if s.shared != nil {
		shared, err := s.shared.Roots(ctx, owner)
		if err != nil {
			s.log.Warn("studio: a scoped chat sees no shared folder this turn", "chat", base.ChatID, "error", err)
		}
		for _, r := range shared {
			add(r.Path)
		}
	}
	return out
}

// chatScopes returns the agent folders that apply to a chat, outermost first,
// and the chat whose folder is the chat's own folder. A chat applies its own
// agent's folder; a sub-agent applies its parent's agent's folder, and its
// own agent's folder inside it. No scope means the chat works on the whole
// home. With record, the chat's ScopedAgent flag is set or cleared to match
// what its agent has now.
func (s *Service) chatScopes(ctx context.Context, rec session.ChatRecord, record bool) ([]folderScope, string, error) {
	holder := rec.ID
	var scopes []folderScope
	if rec.Kind == session.KindSubAgent && rec.ParentID != "" {
		holder = rec.ParentID
		parent, err := s.d.Store.GetChat(ctx, rec.Principal, rec.ParentID)
		switch {
		case errors.Is(err, session.ErrNotFound):
			// The parent is gone: nothing is known of its folder.
			if rec.ScopedAgent {
				return nil, "", ErrAgentFolderMissing
			}
			holder = rec.ID
		case err != nil:
			return nil, "", err
		default:
			sc, err := s.agentFolderScope(ctx, parent, false)
			if err != nil {
				return nil, "", err
			}
			if sc != nil {
				scopes = append(scopes, *sc)
			}
		}
	}
	sc, err := s.agentFolderScope(ctx, rec, record)
	if err != nil {
		return nil, "", err
	}
	if sc != nil {
		scopes = append(scopes, *sc)
	}
	return scopes, holder, nil
}

// agentFolderScope returns the folder of the saved agent of rec, nil for a
// chat without an agent or whose agent has no folder.
func (s *Service) agentFolderScope(ctx context.Context, rec session.ChatRecord, record bool) (*folderScope, error) {
	if rec.AgentID == "" {
		return nil, nil
	}
	var ag agents.Agent
	err := agents.ErrNotFound
	if s.d.Agents != nil {
		ag, err = s.d.Agents.Get(ctx, rec.Principal, rec.AgentID)
	}
	switch {
	case errors.Is(err, agents.ErrNotFound):
		// A deleted agent leaves no folder to read. A chat that knew its
		// agent had one is refused; one that did not keeps today's rule.
		if rec.ScopedAgent {
			return nil, ErrAgentFolderMissing
		}
		return nil, nil
	case err != nil:
		// Without the agent nothing says whether it has a folder.
		return nil, fmt.Errorf("studio: read the chat's agent: %w", err)
	}
	if record && rec.ScopedAgent != ag.HasFolder() {
		serr := s.d.Store.SetScopedAgent(ctx, rec.Principal, rec.ID, ag.HasFolder())
		if serr != nil && ag.HasFolder() {
			// Without the flag a later delete of the agent would give the
			// chat the whole home: do not run.
			return nil, fmt.Errorf("studio: mark the chat as scoped: %w", serr)
		}
		if serr != nil {
			s.log.Warn("studio: could not clear the scoped mark of a chat", "chat", rec.ID, "error", serr)
		}
	}
	if !ag.HasFolder() {
		return nil, nil
	}
	return s.liveFolder(ctx, rec.Principal, ag)
}

// liveFolder returns the folder of an agent that has one, or
// ErrAgentFolderMissing when it is not chosen, deleted or unreadable.
func (s *Service) liveFolder(ctx context.Context, p string, ag agents.Agent) (*folderScope, error) {
	if ag.FolderID <= 0 {
		return nil, ErrAgentFolderMissing
	}
	path, ok := s.FolderResolver().PathOf(ctx, p, ag.FolderID)
	if !ok {
		return nil, ErrAgentFolderMissing
	}
	mode := workspace.ModeRead
	if ag.FolderMode == agents.FolderWrite {
		mode = workspace.ModeWrite
	}
	return &folderScope{node: ag.FolderID, mode: mode, path: path}, nil
}
