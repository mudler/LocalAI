package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/sharedfolders"
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

// ErrAgentFolderLimit means the complete scoped access exceeds the fence budget.
// The caller may retry after the available shared folders change.
var ErrAgentFolderLimit = errors.New("studio: the file access of this agent exceeds the folder limit")

// scopedFolder is one folder of a saved agent as it is now, at the mode the
// agent may use it: the mode it was given, capped by every folder of the same
// agent that holds it.
type scopedFolder struct {
	node int64
	mode workspace.Mode
	path string // workspace path
}

// folderScope is the folders of a saved agent as they are now, in the order
// the agent lists them. The first is the one the file tools call "home".
type folderScope struct {
	folders []scopedFolder
	// chatOnly is the scope of an agent saved with No files: it has no
	// folder, and its chats reach only their own folder and the skill layers.
	// The home, the shared folders and every other file stay out of reach for
	// each tool, not only for the file tools.
	chatOnly bool
}

// primary is the first folder of the scope.
func (sc *folderScope) primary() scopedFolder { return sc.folders[0] }

// extraPaths are the workspace paths of the folders after the first.
func (sc *folderScope) extraPaths() []string {
	out := make([]string, 0, len(sc.folders)-1)
	for _, f := range sc.folders[1:] {
		out = append(out, f.path)
	}
	return out
}

// roots are the fence roots of the scope.
func (sc *folderScope) roots() []workspace.FenceRoot {
	out := make([]workspace.FenceRoot, 0, len(sc.folders))
	for _, f := range sc.folders {
		out = append(out, workspace.FenceRoot{Node: f.node, Mode: f.mode})
	}
	return out
}

// userChatActor is the user's actor in a chat, without a fence: the actor of
// a chat whose agent has no folder, and the base that chatAccess and
// agentReader fence. A person's own action on a chat (a delete) uses it too.
// Every other actor of a chat comes from chatAccess.
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
	readOnly, _, err := s.scopedRoots(ctx, base, scopes)
	if err != nil {
		return session.Access{}, err
	}
	owner := base
	owner.ChatID = ""
	chatDir, err := s.mgr.EnsureChatFolder(ctx, owner, holder)
	if err != nil {
		return session.Access{}, fmt.Errorf("studio: make the chat's folder: %w", err)
	}
	actor, err := fenceScopes(base, scopes, chatDir.ID, readOnly)
	if err != nil {
		return session.Access{}, err
	}
	// The innermost folder is the one the chat works in: a sub-agent's own
	// folder inside its parent's. The store caps the mode by every layer.
	// The view is the session's one view builder: the folder at home, the
	// chat folder at chat, the shared folders read only.
	work := scopes[len(scopes)-1]
	primary, extra := chatDir.Path, []string(nil)
	if !work.chatOnly {
		primary, extra = work.primary().path, work.extraPaths()
	}
	x, err := s.mgr.ScopedAccessFolders(ctx, actor, primary, extra, chatDir.Path)
	if errors.Is(err, session.ErrNoHome) || errors.Is(err, workspace.ErrNotFound) {
		// The folder went away since it was checked, or a sub-agent's folder
		// is outside its parent's.
		return session.Access{}, ErrAgentFolderMissing
	}
	if err != nil {
		return session.Access{}, err
	}
	return x, nil
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
	readOnly, _, err := s.scopedRoots(ctx, base, scopes)
	if err != nil {
		return base.Fenced()
	}
	var chatNode int64
	if hrec, herr := s.d.Store.GetChat(ctx, rec.Principal, holder); herr == nil && hrec.Folder != "" {
		owner := base
		owner.ChatID = ""
		if n, serr := s.d.Files.Store().Stat(ctx, owner, session.ChatsPath(rec.Principal)+"/"+hrec.Folder); serr == nil {
			chatNode = n.ID
		}
	}
	actor, err := fenceScopes(base, scopes, chatNode, readOnly)
	if err != nil {
		return base.Fenced()
	}
	return actor
}

// fenceScopes fences base with one layer per scope. Each layer holds the
// scope's folder at its mode, the chat's folder (when chatNode is not 0) read
// and write, and the read-only roots; the layers intersect, so a sub-agent
// whose own agent has a folder reaches only what both folders allow.
func fenceScopes(base workspace.Actor, scopes []*folderScope, chatNode int64, readOnly []workspace.FenceRoot) (workspace.Actor, error) {
	// Reserve the chat root even before its folder exists. Check every
	// layer before building any actor; roots must never be truncated.
	for _, sc := range scopes {
		if 1+len(sc.folders)+len(readOnly) > workspace.MaxFenceRoots {
			return workspace.Actor{}, ErrAgentFolderLimit
		}
	}
	a := base
	for _, sc := range scopes {
		roots := sc.roots()
		if chatNode > 0 {
			roots = append(roots, workspace.FenceRoot{Node: chatNode, Mode: workspace.ModeWrite})
		}
		roots = append(roots, readOnly...)
		a = a.Fenced(roots...)
	}
	return a, nil
}

// scopedRoots resolves the complete optional read set and validates the budget
// without making a chat folder. Creation and every turn use the same check.
// notMounted counts the shared folders left out to fit the budget.
func (s *Service) scopedRoots(ctx context.Context, base workspace.Actor, scopes []*folderScope) ([]workspace.FenceRoot, int, error) {
	var mandatory []string
	reserve := 0
	for _, sc := range scopes {
		reserve = max(reserve, len(sc.folders))
		for _, f := range sc.folders {
			mandatory = append(mandatory, f.path)
		}
	}
	if slices.ContainsFunc(scopes, func(sc *folderScope) bool { return sc.chatOnly }) {
		// No files: the skill layers stay readable, nothing shared does.
		roots, _ := s.skillRoots(ctx, base)
		if _, err := fenceScopes(base, scopes, 0, roots); err != nil {
			return nil, 0, err
		}
		return roots, 0, nil
	}
	roots, dropped, err := s.readOnlyRoots(ctx, base, reserve, mandatory)
	if err != nil {
		return nil, 0, err
	}
	if _, err := fenceScopes(base, scopes, 0, roots); err != nil {
		return nil, 0, err
	}
	return roots, dropped, nil
}

// readOnlyRoots lists the folders a scoped chat may read besides its own: the
// skill layers and the folders shared with the user, all read only (the
// shared folders' grants may allow more; a scoped chat never writes there).
// A folder that is missing or unreadable is left out. Enumeration failures
// refuse access rather than silently omitting shared folders.
//
// A fence layer holds at most workspace.MaxFenceRoots roots: the chat folder,
// reserve folders of the agent, the skill layers and the rest for shared
// folders. The skill layers and the agent's folders are always kept. A shared
// folder inside a root of the layer is left out (the root already reaches it).
// Of the others the ones with the lowest node IDs are kept until the budget is
// full, and dropped counts the rest. Dropping a root only removes access.
// mandatory are the workspace paths of the agent's folders.
func (s *Service) readOnlyRoots(ctx context.Context, base workspace.Actor, reserve int, mandatory []string) ([]workspace.FenceRoot, int, error) {
	owner := base
	owner.ChatID = ""
	ws := s.d.Files.Store()
	stat := func(p string) (workspace.Node, bool) {
		n, err := ws.Stat(ctx, owner, p)
		return n, err == nil && n.Kind == workspace.KindDir
	}
	out, skillPaths := s.skillRoots(ctx, base)
	var held []string // paths of the roots already in the layer
	held = append(held, mandatory...)
	held = append(held, skillPaths...)
	if s.shared == nil {
		return out, 0, nil
	}
	shared, err := s.shared.Roots(ctx, owner)
	if err != nil {
		return nil, 0, fmt.Errorf("studio: list shared folders for scoped access: %w", err)
	}
	var cand []workspace.Node
	for _, r := range shared {
		if n, ok := stat(r.Path); ok {
			cand = append(cand, n)
		}
	}
	sort.Slice(cand, func(i, j int) bool { return cand[i].ID < cand[j].ID })
	room := workspace.MaxFenceRoots - 1 - reserve - len(out)
	dropped := 0
	for _, n := range cand {
		if insideAny(n.Path, held) {
			continue
		}
		if room <= 0 {
			dropped++
			continue
		}
		room--
		out = append(out, workspace.FenceRoot{Node: n.ID, Mode: workspace.ModeRead})
		held = append(held, n.Path)
	}
	return out, dropped, nil
}

// skillRoots are the skill layers that exist, read only, and their paths.
func (s *Service) skillRoots(ctx context.Context, base workspace.Actor) ([]workspace.FenceRoot, []string) {
	owner := base
	owner.ChatID = ""
	var out []workspace.FenceRoot
	var paths []string
	for _, p := range []string{files.SharedSkillsPath, files.BuiltinSkillsPath, files.AddonSkillsPath} {
		if n, err := s.d.Files.Store().Stat(ctx, owner, p); err == nil && n.Kind == workspace.KindDir {
			out = append(out, workspace.FenceRoot{Node: n.ID, Mode: workspace.ModeRead})
			paths = append(paths, n.Path)
		}
	}
	return out, paths
}

// insideAny says whether p is one of roots or inside one.
func insideAny(p string, roots []string) bool {
	for _, r := range roots {
		if workspace.Within(p, r) {
			return true
		}
	}
	return false
}

// chatScopes returns the agent folders that apply to a chat, outermost first,
// and the chat whose folder is the chat's own folder. A chat applies its own
// agent's folder; a sub-agent applies its parent's agent's folder, and its
// own agent's folder inside it. No scope means the chat works on the whole
// home. With record, the chat's ScopedAgent flag is set or cleared to match
// what its agent has now.
func (s *Service) chatScopes(ctx context.Context, rec session.ChatRecord, record bool) ([]*folderScope, string, error) {
	holder := rec.ID
	var scopes []*folderScope
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
				scopes = append(scopes, sc)
			}
		}
	}
	// A sub-agent's flag follows both folders, not only its own agent's: a
	// child of a scoped chat stays refused, never given the whole home, when
	// its parent's folder can no longer be found.
	sub := rec.Kind == session.KindSubAgent && rec.ParentID != ""
	sc, err := s.agentFolderScope(ctx, rec, record && !sub)
	if err != nil {
		return nil, "", err
	}
	if sc != nil {
		scopes = append(scopes, sc)
	}
	if record && sub {
		if err := s.markScoped(ctx, rec, len(scopes) > 0); err != nil {
			return nil, "", err
		}
	}
	return scopes, holder, nil
}

// markScoped sets the chat's ScopedAgent flag to match what its folders are
// now. Failing to set it is an error only when the chat is scoped: without the
// flag a later delete of the agent would give the chat the whole home.
func (s *Service) markScoped(ctx context.Context, rec session.ChatRecord, scoped bool) error {
	if rec.ScopedAgent == scoped {
		return nil
	}
	err := s.d.Store.SetScopedAgent(ctx, rec.Principal, rec.ID, scoped)
	switch {
	case err != nil && scoped:
		return fmt.Errorf("studio: mark the chat as scoped: %w", err)
	case err != nil:
		s.log.Warn("studio: could not clear the scoped mark of a chat", "chat", rec.ID, "error", err)
	}
	return nil
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
		ag, err = s.chatAgent(ctx, rec.Principal, rec.AgentID)
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
	// An agent saved with No files (the files group off) is scoped to its
	// chat's own folder, whatever folders it still holds: they are ignored,
	// so a missing one cannot block the chat either.
	noFiles := slices.Contains(ag.BuiltinsOff, builtins.GroupFiles)
	if record {
		if err := s.markScoped(ctx, rec, ag.HasFolder() || noFiles); err != nil {
			return nil, err
		}
	}
	if noFiles {
		return &folderScope{chatOnly: true}, nil
	}
	if !ag.HasFolder() {
		return nil, nil
	}
	return s.liveFolder(ctx, rec.Principal, ag)
}

// liveFolder returns the folders of an agent that has some, or
// ErrAgentFolderMissing when one is not chosen, deleted or unreadable. The
// error names the row, so the person knows which folder to fix. The limits of
// the list are checked again here: a store may hold an older shape.
func (s *Service) liveFolder(ctx context.Context, p string, ag agents.Agent) (*folderScope, error) {
	list := ag.FolderList()
	if err := agents.ValidateFolders(list); err != nil {
		return nil, err
	}
	sc := &folderScope{}
	for i, f := range list {
		if f.Node <= 0 {
			return nil, fmt.Errorf("%w: folder %d is not chosen", ErrAgentFolderMissing, i+1)
		}
		path, ok := s.FolderResolver().PathOf(ctx, p, f.Node)
		if !ok {
			return nil, fmt.Errorf("%w: folder %d was deleted or can no longer be read", ErrAgentFolderMissing, i+1)
		}
		mode := workspace.ModeRead
		// A shared folder is read only, whatever the list says.
		if f.Mode == agents.FolderWrite && !isSharedPath(path) {
			mode = workspace.ModeWrite
		}
		sc.folders = append(sc.folders, scopedFolder{node: f.Node, mode: mode, path: path})
	}
	capModes(sc.folders)
	return sc, nil
}

// isSharedPath says whether a workspace path is in the shared folders.
func isSharedPath(p string) bool { return workspace.Within(p, sharedfolders.Root) }

// capModes applies the rule of nested folders: where one folder holds
// another, the mode of the outer folder is the mode of the inner one. The
// store's fence gives a node the highest mode among the roots above it, so a
// read only folder inside a write folder cannot be enforced; the inner folder
// therefore takes the outer mode, in both directions, and the editor shows it.
func capModes(fs []scopedFolder) {
	eff := make([]workspace.Mode, len(fs))
	for i, f := range fs {
		eff[i] = f.mode
		outer := -1
		for j, o := range fs {
			if i != j && f.path != o.path && workspace.Within(f.path, o.path) && (outer < 0 || len(o.path) < len(fs[outer].path)) {
				outer = j
			}
		}
		if outer >= 0 {
			eff[i] = fs[outer].mode
		}
	}
	for i := range fs {
		fs[i].mode = eff[i]
	}
}
