package studio

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/contextfiles"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// projectMemorySource is the index file of the memory of a project. The host
// adds it to the sources of a turn only for a chat that has a home project; the
// notes of a project a chat only reads from are not put in the prompt.
var projectMemorySource = contextfiles.Source{ID: MemoryAddonID, Scope: contextfiles.ScopeProject, Path: proposals.ProjectMemoryDir("{project}") + "/index.md"}

// errNoHomeProject is why a project note has nowhere to go.
var errNoHomeProject = errors.New("this chat does not belong to a project, so there is no project memory to change. " +
	"A project of your own that this chat only reads from is never a target; a project shared with you for editing is one when you name it with the project argument. Use another scope, or leave the note out")

// projectDirName is the folder name of a project, from where its memory is now.
// It is the one name the paths of the project's memory are built from.
func projectDirName(r projects.Resolved) (string, bool) {
	mem := strings.TrimPrefix(r.MemoryPath, "/")
	dir := path.Base(path.Dir(mem))
	if !proposals.IDSafe(dir) || proposals.ProjectMemoryDir(dir) != mem {
		return "", false
	}
	return dir, true
}

// homeProjectOf returns the project the chat of the tool belongs to, read now
// from the chat's record: the model never names it. It reports false for a chat
// with no project, a project that is gone and one that is archived, which takes
// no new notes.
func (s *Service) homeProjectOf(ctx context.Context, c builtins.Chat) (projects.Resolved, bool) {
	if c.Actor.ChatID == "" {
		return projects.Resolved{}, false
	}
	rec, err := s.getChatEffective(ctx, principal(c.Actor.Principal), c.Actor.ChatID)
	if err != nil {
		return projects.Resolved{}, false
	}
	// A project agent's chat has the project of its agent, and only when it
	// holds the project memory grant (a sub-agent and a run that starts by
	// itself never do). Any other chat has a home project of its own, or none.
	_, isAgent, err := s.projectAgentOf(ctx, rec)
	if err != nil {
		// Unknown whether the chat belongs to a project agent: no project.
		return projects.Resolved{}, false
	}
	if isAgent {
		if !c.HoldsGrant(builtins.GrantProjectMemoryWrite) {
			return projects.Resolved{}, false
		}
		r, _, ok := s.projectAgentHome(ctx, rec)
		return r, ok
	}
	return s.ownHome(ctx, rec)
}

// activeHome is resolveHome for a project that takes notes: an active one. A
// chat of a project agent has the project of its agent (a sub-agent, the one
// of its parent), read now and never from the chat record or a request. Any
// other fenced chat has no project: it gets no project notes.
func (s *Service) activeHome(ctx context.Context, rec session.ChatRecord) (projects.Resolved, bool) {
	if r, isAgent, ok := s.projectAgentHome(ctx, rec); isAgent {
		return r, ok
	}
	return s.ownHome(ctx, rec)
}

// ownHome is the active project that a chat that is not fenced belongs to,
// by its record. A fenced chat has none here, even when its record still names
// one.
func (s *Service) ownHome(ctx context.Context, rec session.ChatRecord) (projects.Resolved, bool) {
	r, ok := s.resolveHome(ctx, rec.Principal, rec)
	if !ok || r.Status != projects.StatusActive {
		return projects.Resolved{}, false
	}
	if s.chatFenced(ctx, rec) {
		return projects.Resolved{}, false
	}
	return r, true
}

// projectAgentHome is the project of a chat through its agent. isAgent says
// the chat works for a project agent (its own or, for a sub-agent, its
// parent's); ok says that the project is available now.
func (s *Service) projectAgentHome(ctx context.Context, rec session.ChatRecord) (r projects.Resolved, isAgent, ok bool) {
	ag, found, err := s.projectAgentOf(ctx, rec)
	if err != nil {
		// An unknown answer counts as a project agent whose project is not
		// available: fenced, never an open chat.
		return projects.Resolved{}, true, false
	}
	if !found {
		return projects.Resolved{}, false, false
	}
	if principal(ag.Principal) != principal(rec.Principal) {
		return projects.Resolved{}, true, false
	}
	r, err = s.projectOfAgent(ctx, ag)
	if err != nil || r.Status != projects.StatusActive {
		return projects.Resolved{}, true, false
	}
	return r, true, true
}

// readsProjectContext says whether the chat works for a project agent and may
// read the index of its project (see projectContextOpen).
func (s *Service) readsProjectContext(ctx context.Context, rec session.ChatRecord) bool {
	_, isAgent, ok := s.projectAgentHome(ctx, rec)
	return isAgent && ok && projectContextOpen(rec)
}

// projectContextOpen says whether a chat of a project agent reads the index of
// its project. The index is project context, not the agent's own notes: the
// agent's choice of notes (even None) does not hold it back. A private chat
// does.
func projectContextOpen(rec session.ChatRecord) bool {
	return !slices.Contains(rec.BuiltinsOff, builtins.GroupMemory) && !slices.Contains(rec.AddonsOff, MemoryAddonID)
}

// projectMemoryDirOf is the folder name that a proposal for the project writes
// under. The project is read now, so a proposal made after a rename or a delete
// follows the folder. An unknown or foreign ID, and an archived project, are
// refused.
func (s *Service) projectMemoryDirOf(ctx context.Context, user, id string) (string, error) {
	if id == "" || s.d.Projects == nil {
		return "", errNoHomeProject
	}
	r, err := s.d.Projects.Get(ctx, principal(user), id)
	switch {
	case errors.Is(err, projects.ErrNotFound):
		return "", errNoHomeProject
	case err != nil:
		return "", fmt.Errorf("the project cannot be read now: %w", err)
	case r.Status != projects.StatusActive:
		return "", fmt.Errorf("project %q is archived; restore it before it takes notes", r.Name)
	}
	dir, ok := projectDirName(r)
	if !ok {
		return "", errors.New("the folder of the project is not where it is expected; nothing was proposed")
	}
	return dir, nil
}

// withProjectDir fills the project folder of a subject from the chat's home
// project, read at each turn and kept nowhere.
func (s *Service) withProjectDir(ctx context.Context, rec session.ChatRecord, sub contextfiles.Subject) contextfiles.Subject {
	if r, ok := s.activeHome(ctx, rec); ok {
		if dir, ok := projectDirName(r); ok {
			sub.ProjectDir = dir
		}
	}
	return sub
}

// projectSourcesFor returns the project index source for a chat that has a
// home project and reads the notes, or nil. choice is the agent's choice of
// notes: with None nothing is read.
func (s *Service) projectSourcesFor(ctx context.Context, rec session.ChatRecord, choice string) []contextfiles.Source {
	if _, isAgent, ok := s.projectAgentHome(ctx, rec); isAgent {
		// A project agent reads its project whatever its choice of notes.
		if !ok || !projectContextOpen(rec) {
			return nil
		}
		return []contextfiles.Source{projectMemorySource}
	}
	if choice == agents.MemoryNone {
		return nil
	}
	if _, ok := s.activeHome(ctx, rec); !ok {
		return nil
	}
	return []contextfiles.Source{projectMemorySource}
}

// senderPaired says whether the sender of a messaging chat is paired with the
// bot, as the personal notes require.
func (s *Service) senderPaired(c builtins.Chat) bool {
	_, paired := s.messagingFacts(c.Actor.ChatID)
	return paired
}

// projectScopeOpen says whether propose_change offers the scope project to a
// chat. It is the one answer for the tool and for the sentence of the prompt,
// so they never disagree. All of these hold: the run is one that may propose
// (not a sub-agent, not a run that started by itself, a paired sender in a
// messaging chat); memory is on for the chat and the agent's choice of notes
// is not None (fixed is memoryChatScopes); saving is not turned off; the chat
// may propose a memory note at all; and the chat has a project to write: its
// own, or for a project agent the one of its agent, which also needs the grant
// project.memory.write.
func (s *Service) projectScopeOpen(ctx context.Context, c builtins.Chat, fixed []string) bool {
	switch {
	case fixed != nil && len(fixed) == 0:
		return false
	case c.SubAgent, c.Origin != "" && c.Origin != session.OriginConnector:
		return false
	case c.Origin == session.OriginConnector && !s.senderPaired(c):
		return false
	case !s.d.Files.Available() || s.changes == nil:
		return false
	case !s.memoryWritable(ctx, c) || s.changes.saveModeOf(ctx, c) == SaveNever:
		return false
	case !slices.Contains(s.changes.kindsOffered(ctx, c), contract.ChangeMemory):
		return false
	}
	if _, ok := s.homeProjectOf(ctx, c); ok {
		return true
	}
	// No project of its own: a project that is shared for editing and pulled
	// can still take a note, named by its reference.
	return len(s.pulledWritable(ctx, c)) > 0
}

// projectAgentID says whether the saved agent belongs to a project.
func (s *Service) projectAgentID(ctx context.Context, user, id string) bool {
	if id == "" || s.d.Agents == nil {
		return false
	}
	ag, err := s.storedChatAgent(ctx, principal(user), id)
	return err == nil && ag.ProjectID != ""
}

// sharedWriteRefusal says why a note cannot go to a shared project, or "" when
// it can: the project must be active and the grant a write grant.
func sharedWriteRefusal(sh projects.Shared) string {
	switch {
	case sh.OwnerPrincipal == "":
		return "the project is not shared with you"
	case sh.Status != projects.StatusActive:
		return "the project is archived"
	case sh.Mode != projects.ModeWrite:
		return "this project is shared read-only: only its owner can change its notes"
	}
	return ""
}

// sharedWritable resolves the reference of a project that another person
// shares with viewer, now, and says whether a note may be proposed for it. The
// reference is the folder node, never an ID from a manifest.
func (s *Service) sharedWritable(ctx context.Context, viewer, ref string) (projects.Shared, error) {
	if s.d.Projects == nil {
		return projects.Shared{}, errNoHomeProject
	}
	sh, err := s.d.Projects.ResolveSharedRef(ctx, principal(viewer), ref)
	switch {
	case errors.Is(err, projects.ErrNotFound), errors.Is(err, projects.ErrForbidden):
		return projects.Shared{}, errors.New("this project is not shared with you (any more), so nothing was proposed")
	case err != nil:
		// The cause may name a path or an error of the owner's side: the model
		// and the person read a neutral text.
		s.log.Warn("studio: a shared project could not be resolved", "error", err)
		return projects.Shared{}, errors.New("the project cannot be read now, so nothing was proposed")
	}
	if why := sharedWriteRefusal(sh); why != "" {
		return projects.Shared{}, errors.New(why + ", so nothing was proposed")
	}
	return sh, nil
}

// pulledWritable lists the projects shared with the chat's user for editing
// that the chat has pulled, as they are now. A fenced chat has none.
func (s *Service) pulledWritable(ctx context.Context, c builtins.Chat) []projects.Shared {
	if c.Actor.ChatID == "" || s.d.Projects == nil {
		return nil
	}
	rec, err := s.getChatEffective(ctx, principal(c.Actor.Principal), c.Actor.ChatID)
	if err != nil || s.chatFenced(ctx, rec) {
		return nil
	}
	var out []projects.Shared
	for _, id := range rec.PulledProjects {
		if !projects.IsSharedRef(id) {
			continue
		}
		if sh, err := s.sharedWritable(ctx, rec.Principal, id); err == nil {
			out = append(out, sh)
		}
	}
	return out
}

// pulledSharedRef checks that ref is, character for character, an entry of the
// pulled list of the chat of the tool, and that the chat may still write to
// that project. A bare node, an ID, a slug and any change of case, space or
// zeros is not an entry. The home project and the person's own projects are not
// named this way.
func (s *Service) pulledSharedRef(ctx context.Context, c builtins.Chat, ref string) (string, error) {
	if c.Actor.ChatID == "" || !projects.IsSharedRef(ref) {
		return "", errors.New("project names a project that is shared with you and that this chat has pulled, by its reference such as s:12. " +
			"Leave it out for the project of this chat")
	}
	rec, err := s.getChatEffective(ctx, principal(c.Actor.Principal), c.Actor.ChatID)
	if err != nil || s.chatFenced(ctx, rec) || !slices.Contains(rec.PulledProjects, ref) {
		return "", errors.New("this chat has not pulled that project, so nothing was proposed. Use a reference from the list in this tool description")
	}
	return ref, nil
}

// sharedMemoryDir is the memory folder of a shared project relative to the
// home of its owner, taken from the folder the store resolved and not built
// from a fixed root: it must be exactly Projects/<folder>/memory.
func sharedMemoryDir(sh projects.Shared) (string, bool) {
	rel, ok := strings.CutPrefix(sh.MemoryPath, "/users/"+sh.OwnerPrincipal+"/")
	if !ok || sh.OwnerPrincipal == "" || rel != proposals.ProjectMemoryDir(sh.Slug) || !strings.HasPrefix(rel, projects.RootDir+"/") {
		return "", false
	}
	return rel, true
}

// sharedDirIs says whether the note target is inside the memory folder of the
// shared project as it is now: the whole folder, not only its name.
func sharedDirIs(sh projects.Shared, target string) bool {
	dir, ok := sharedMemoryDir(sh)
	return ok && path.Dir(target) == dir
}
