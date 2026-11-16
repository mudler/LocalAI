package studio

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// maxPulledProjects bounds the other projects one chat reads from.
const maxPulledProjects = 8

// availableSets is what a user has installed and may switch, the sources the
// project defaults are matched against. Each list is sorted and has no
// duplicates.
type availableSets struct {
	Skills, Addons, MCPServers []string
	// Connectors are the IDs of the user's enabled MCP connectors.
	Connectors []string
	// Collections are the names of the user's collections.
	Collections []string
}

// projectSwitches applies the defaults of a project to the switches a new chat
// asks for. For each of skills, add-ons, MCP servers and connectors, a
// non-empty default list means only those that are available are on, so every
// other available one is switched off. An empty list, or a list that names
// nothing available, is no override. A kind the request already switched
// explicitly keeps the request's list. MCP servers and connectors share the
// McpOff list: an entry "connector:<id>" is a connector, any other entry is a
// server, and the request's entries of one kind suppress only the default of
// that kind. Collections are a per-chat restriction, not a switch: see
// projectCollections. The off list is a snapshot of the connectors enabled
// when the chat starts, so a connector created later is on. The list stays
// within maxMCPSwitches: the request's entries are kept and the project's
// are cut. The function is pure.
func projectSwitches(d projects.Defaults, in Switches, avail availableSets) Switches {
	in.SkillsOff = offFor(in.SkillsOff, d.Skills, avail.Skills)
	in.AddonsOff = offFor(in.AddonsOff, d.Addons, avail.Addons)

	var reqServers, reqConns []string
	for _, n := range in.McpOff {
		if strings.HasPrefix(n, mcpconns.Prefix) {
			reqConns = append(reqConns, n)
		} else {
			reqServers = append(reqServers, n)
		}
	}
	servers := offFor(reqServers, d.MCPServers, avail.MCPServers)
	keep := make([]string, len(d.Connectors))
	for i, id := range d.Connectors {
		keep[i] = mcpconns.Prefix + id
	}
	availConns := make([]string, len(avail.Connectors))
	for i, id := range avail.Connectors {
		availConns[i] = mcpconns.Prefix + id
	}
	conns := offFor(reqConns, keep, availConns)
	if len(servers) > 0 || len(conns) > 0 {
		merged := slices.Concat(servers, conns)
		if len(merged) > maxMCPSwitches {
			// Keep the request's own entries and cut the project's, in sorted
			// order, so the result is stable and the chat never fails here.
			own := in.McpOff
			var added []string
			for _, n := range merged {
				if !slices.Contains(own, n) {
					added = append(added, n)
				}
			}
			slices.Sort(added)
			merged = slices.Concat(own, added[:max(0, min(len(added), maxMCPSwitches-len(own)))])
		}
		in.McpOff = merged
	}
	return in
}

// maxChatCollections and maxCollectionNameRunes bound a chat's collection
// restriction.
const (
	maxChatCollections     = 100
	maxCollectionNameRunes = 200
)

// cleanCollections checks a requested restriction and drops repeated names.
func cleanCollections(names []string) ([]string, error) {
	if len(names) > maxChatCollections {
		return nil, fmt.Errorf("%w: at most %d collections", session.ErrInvalid, maxChatCollections)
	}
	var out []string
	for _, n := range names {
		switch {
		case strings.TrimSpace(n) == "":
			return nil, fmt.Errorf("%w: a collection name is empty", session.ErrInvalid)
		case utf8.RuneCountInString(n) > maxCollectionNameRunes:
			return nil, fmt.Errorf("%w: a collection name is longer than %d characters", session.ErrInvalid, maxCollectionNameRunes)
		case !slices.Contains(out, n):
			out = append(out, n)
		}
	}
	return out, nil
}

// projectCollections returns the restriction a new chat starts with. A request
// that names collections wins. Otherwise a non-empty default restricts the chat
// to the listed collections the user has; a default that lists none of them is
// no restriction, so a collection that was deleted never locks a chat out of
// all of them. The function is pure.
func projectCollections(d projects.Defaults, requested []string, avail availableSets) []string {
	if len(requested) > 0 {
		return requested
	}
	var out []string
	for _, n := range d.Collections {
		if slices.Contains(avail.Collections, n) && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// offFor returns the off list for one kind: the request when it chose
// something, else every available name that the default does not keep on.
func offFor(requested, keep, available []string) []string {
	if len(requested) > 0 {
		return requested
	}
	on := 0
	for _, n := range keep {
		if slices.Contains(available, n) {
			on++
		}
	}
	if on == 0 {
		return requested
	}
	var off []string
	for _, n := range available {
		if !slices.Contains(keep, n) {
			off = append(off, n)
		}
	}
	return off
}

// available lists what the user could switch now, for the kinds the defaults
// name. A source that is off or fails contributes nothing, so a default that
// names it is skipped.
func (s *Service) available(ctx context.Context, user, model string, d projects.Defaults) availableSets {
	var a availableSets
	p := principal(user)
	if len(d.Skills) > 0 && s.d.Files.Available() {
		if list, _, err := s.Skills(ctx, user, ""); err == nil {
			for _, sk := range list {
				if sk.Invalid == "" && !sk.Locked {
					a.Skills = append(a.Skills, sk.Name)
				}
			}
		}
	}
	if len(d.Addons) > 0 && s.d.AddonTools != nil {
		if tools, err := s.d.AddonTools.Tools(ctx, p); err == nil {
			for _, t := range tools {
				a.Addons = append(a.Addons, t.AddonID)
			}
		}
	}
	if len(d.MCPServers) > 0 && s.d.MCPServers != nil {
		for _, m := range s.d.MCPServers(ctx, p, model) {
			a.MCPServers = append(a.MCPServers, m.Name)
		}
	}
	if len(d.Connectors) > 0 && s.d.Connectors != nil {
		if list, err := s.d.Connectors.List(ctx, principal(user)); err == nil {
			for _, c := range list {
				if c.Enabled {
					a.Connectors = append(a.Connectors, c.ID)
				}
			}
		}
	}
	if c := s.d.Collections; len(d.Collections) > 0 && c != nil && c.Allowed(ctx, user) {
		if names, err := c.List(ctx, user); err == nil {
			a.Collections = names
		}
	}
	for _, l := range []*[]string{&a.Skills, &a.Addons, &a.MCPServers, &a.Connectors, &a.Collections} {
		slices.Sort(*l)
		*l = slices.Compact(*l)
	}
	return a
}

// usableProject returns the project of the user that a chat may be put in. It
// is projects.ErrNotFound for an unknown project or another owner's (a project
// that is shared with the user is another owner's), session.ErrInvalid for the
// reference of a shared project, and
// session.ErrInvalid for an archived one: an archived project takes no new
// chats.
func (s *Service) usableProject(ctx context.Context, user, id string) (projects.Resolved, error) {
	if s.d.Projects == nil {
		return projects.Resolved{}, fmt.Errorf("%w: projects are not available", session.ErrInvalid)
	}
	if projects.IsSharedRef(id) {
		// A project that another user shares can be read from a chat, never
		// be its project or the project of an agent.
		return projects.Resolved{}, fmt.Errorf("%w: a project shared with you cannot be the project of a chat or an agent", session.ErrInvalid)
	}
	r, err := s.d.Projects.Get(ctx, principal(user), id)
	if err != nil {
		return projects.Resolved{}, err
	}
	if r.Status == projects.StatusArchived {
		return projects.Resolved{}, fmt.Errorf("%w: project %q is archived; restore it first", session.ErrInvalid, r.Name)
	}
	return r, nil
}

// resolveHome returns the project a chat belongs to. It reports false, and
// never an error, for a chat with no project and for one whose project is gone
// or unreadable: deleting a project must not break its chats.
func (s *Service) resolveHome(ctx context.Context, user string, rec session.ChatRecord) (projects.Resolved, bool) {
	if rec.ProjectID == "" || s.d.Projects == nil {
		return projects.Resolved{}, false
	}
	r, err := s.d.Projects.Get(ctx, principal(user), rec.ProjectID)
	if err != nil {
		if !errors.Is(err, projects.ErrNotFound) {
			s.log.Warn("studio: project of a chat is unreadable", "chat", rec.ID, "project", rec.ProjectID, "error", err)
		}
		return projects.Resolved{}, false
	}
	return r, true
}

// SetChatProject moves a chat into a project, or out of any with "". The chat
// must be idle, as for a change of title or model: a running turn is
// ErrChatBusy. The chat's tool switches stay as they are; the project's
// defaults apply to new chats only. The project of another user, an unknown
// one and a chat of another user are not found; an archived project is refused.
func (s *Service) SetChatProject(ctx context.Context, user, chatID, projectID string) error {
	p := principal(user)
	rec, err := s.getChatEffective(ctx, p, chatID)
	if err != nil {
		return err
	}
	if rec.Kind == session.KindSubAgent {
		return ErrSubAgentReadOnly
	}
	_, isAgent, aerr := s.projectAgentOf(ctx, rec)
	if aerr != nil {
		return aerr
	}
	if isAgent {
		// The chat belongs to the project of its agent, whatever is asked.
		return fmt.Errorf("%w: a chat of a project agent stays in the project of the agent", session.ErrInvalid)
	}
	if projectID != "" {
		if err := s.refuseFenced(ctx, rec); err != nil {
			return err
		}
		if _, err := s.usableProject(ctx, user, projectID); err != nil {
			return err
		}
	}
	return s.withClaim(ctx, p, chatID, func(cur session.ChatRecord) error {
		if err := s.d.Store.SetProject(ctx, p, chatID, projectID); err != nil {
			return err
		}
		// A project cannot be both home and pulled.
		if projectID != "" && slices.Contains(cur.PulledProjects, projectID) {
			rest := slices.DeleteFunc(slices.Clone(cur.PulledProjects), func(id string) bool { return id == projectID })
			return s.d.Store.SetPulledProjects(ctx, p, chatID, rest)
		}
		return nil
	})
}

// SetChatPulled sets the other projects a chat reads from, replacing the
// earlier list. There are at most eight, none twice, none the chat's own
// project, and each one is an active project of the user, or the reference
// "s:<node>" of an active project that another user shares with them now. A
// shared project is never named by the id in its manifest. An empty list
// clears it.
func (s *Service) SetChatPulled(ctx context.Context, user, chatID string, ids []string) error {
	p := principal(user)
	rec, err := s.getChatEffective(ctx, p, chatID)
	if err != nil {
		return err
	}
	if rec.Kind == session.KindSubAgent {
		return ErrSubAgentReadOnly
	}
	if len(ids) > 0 {
		if err := s.refuseFenced(ctx, rec); err != nil {
			return err
		}
	}
	if len(ids) > maxPulledProjects {
		return fmt.Errorf("%w: at most %d pulled projects", session.ErrInvalid, maxPulledProjects)
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		switch {
		case id == "":
			return fmt.Errorf("%w: empty project ID", session.ErrInvalid)
		case seen[id]:
			return fmt.Errorf("%w: project %q is listed twice", session.ErrInvalid, id)
		case id == rec.ProjectID:
			return fmt.Errorf("%w: project %q is already the project of this chat", session.ErrInvalid, id)
		}
		seen[id] = true
		if _, _, err := s.resolvePulled(ctx, user, id); err != nil {
			return err
		}
	}
	// Like SetChatProject, the change waits for an idle chat. The home project
	// is read again under the claim, so a move that ran meanwhile is seen.
	return s.withClaim(ctx, p, chatID, func(cur session.ChatRecord) error {
		if slices.Contains(ids, cur.ProjectID) && cur.ProjectID != "" {
			return fmt.Errorf("%w: project %q is already the project of this chat", session.ErrInvalid, cur.ProjectID)
		}
		return s.d.Store.SetPulledProjects(ctx, p, chatID, ids)
	})
}

// chatFenced says whether a chat is fenced to the folder of its saved agent, by
// the same test chatAccess uses: the chat has folder scopes (for a sub-agent,
// its own or its parent's). A chat whose folder cannot be checked counts as
// fenced: the answer fails closed.
func (s *Service) chatFenced(ctx context.Context, rec session.ChatRecord) bool {
	if rec.ScopedAgent {
		return true
	}
	scopes, _, err := s.chatScopes(ctx, rec, false)
	return err != nil || len(scopes) > 0
}

// projectAgentOf returns the project agent that a chat works for: the chat's
// own agent, or for a sub-agent the agent of its parent when its own is not one.
// found is false for any other chat, and for a record that is not there (not
// found is the answer "no project agent"). Any other error of the stored reads
// (the chat's agent, the parent chat, the parent's agent) is returned: the
// answer is then unknown, and every caller that gates on it fails closed. Only
// the stored records are read; the project itself is not.
func (s *Service) projectAgentOf(ctx context.Context, rec session.ChatRecord) (agents.Agent, bool, error) {
	if s.d.Agents == nil {
		return agents.Agent{}, false, nil
	}
	byID := func(id string) (agents.Agent, bool, error) {
		if id == "" {
			return agents.Agent{}, false, nil
		}
		ag, err := s.storedChatAgent(ctx, rec.Principal, id)
		switch {
		case errors.Is(err, agents.ErrNotFound):
			return agents.Agent{}, false, nil
		case err != nil:
			return agents.Agent{}, false, err
		}
		return ag, ag.ProjectID != "", nil
	}
	if ag, ok, err := byID(rec.AgentID); ok || err != nil {
		return ag, ok, err
	}
	if rec.Kind != session.KindSubAgent || rec.ParentID == "" {
		return agents.Agent{}, false, nil
	}
	parent, err := s.getChatEffective(ctx, rec.Principal, rec.ParentID)
	switch {
	case errors.Is(err, session.ErrNotFound):
		return agents.Agent{}, false, nil
	case err != nil:
		return agents.Agent{}, false, err
	}
	return byID(parent.AgentID)
}

// projectNotes are the sentences of the prompt of a fenced chat that works for
// a project agent: the project, and what its two folders are. The roots are
// named only when the chat works in the project's own folders; a sub-agent
// whose saved agent has a folder of its own hears of the project only. work is
// the innermost folder scope of the chat. open says whether propose_change
// offers the project scope to the chat now (chatProjectScopeOpen); the last
// sentence follows it, so the model is never told to propose what it cannot.
// It is called at most once, and only when the sentence is needed, so a chat
// without a project pays nothing. A chat with no such project gets nothing.
func (s *Service) projectNotes(ctx context.Context, rec session.ChatRecord, work *folderScope, open func() bool) []string {
	r, ok := s.activeHome(ctx, rec)
	if !ok {
		return nil
	}
	notes := []string{projectLine(r)}
	// The chat works in the folders of its project when its own agent is the
	// project agent, or it is a sub-agent with no folder of its own.
	ag, _, err := s.projectAgentOf(ctx, rec)
	if err != nil {
		return notes
	}
	mine := rec.AgentID != "" && ag.ID == rec.AgentID
	if !(mine || rec.AgentID == "") || work.chatOnly {
		return notes
	}
	// The two folders are found by node, not by position.
	fence, err := s.fenceOfAgent(ctx, ag)
	if err != nil {
		return notes
	}
	var files *scopedFolder
	memAt := ""
	paths := work.extraPaths()
	names := session.FolderMountNames(paths)
	for i := range work.folders {
		switch work.folders[i].node {
		case fence.files.Node:
			files = &work.folders[i]
		case fence.memory.Node:
			if i > 0 {
				memAt = names[i-1]
			}
		}
	}
	if files == nil || memAt == "" || work.folders[0].node != fence.files.Node {
		return notes
	}
	save := "To save a note, propose a change in the project scope; the user confirms it."
	if !open() {
		save = "This folder is read only for you; you cannot save notes in the project."
	}
	return append(notes, fmt.Sprintf("%s/%s holds the files of the project (%s). %s/%s holds the memory of the project (read only): do not write there. %s",
		session.GuestRoot, session.GuestHome, accessWord(files.mode), session.GuestRoot, memAt, save))
}

// chatProjectScopeOpen is the answer of projectScopeOpen for a chat record,
// with the same inputs the tool builder gives it.
func (s *Service) chatProjectScopeOpen(ctx context.Context, rec session.ChatRecord) bool {
	if s.scopeChecks != nil {
		s.scopeChecks()
	}
	c := s.builtinChat(ctx, userChatActor(rec.Principal, rec.ID), rec, rec.Kind == session.KindSubAgent)
	return s.projectScopeOpen(ctx, c, s.memoryChatScopes(ctx, c))
}

func accessWord(m workspace.Mode) string {
	if m < workspace.ModeWrite {
		return "read only"
	}
	return "read and write"
}

// refuseFenced is the error for a project on a fenced chat: a chat of a saved
// agent that has a folder has no project of its own and pulls none, it sees
// only that folder. A project agent's chat is in the project of its agent.
func (s *Service) refuseFenced(ctx context.Context, rec session.ChatRecord) error {
	if s.chatFenced(ctx, rec) {
		return fmt.Errorf("%w: a chat of an agent that has a folder cannot be in a project or read one", session.ErrInvalid)
	}
	return nil
}

// projectLine is the sentence that tells the chat which project it is in: the
// name and the start of the description, on one line.
func projectLine(r projects.Resolved) string {
	flat := func(t string) string { return strings.Join(strings.Fields(t), " ") }
	line := fmt.Sprintf("This chat belongs to the project %q.", flat(r.Name))
	if d := []rune(flat(r.Manifest.Description)); len(d) > 0 {
		if len(d) > maxProjectDescriptionRunes {
			d = append(d[:maxProjectDescriptionRunes-1], '…')
		}
		line += " Project description: " + string(d)
	}
	return line
}

// maxProjectDescriptionRunes caps the description in the prompt.
const maxProjectDescriptionRunes = 200

// projectMounts returns the mounts of a chat's projects for this turn, and a
// note when pulled projects were left out. It reads the projects each turn and
// keeps nothing, and it never changes the chat's stored list. The home project's
// files are mounted read and write and its memory read only: a chat changes the
// memory by a proposal that the owner confirms. A pulled project is mounted
// read only when it is an active project of the chat's owner; a missing,
// archived, broken or foreign one is dropped. A chat whose home project is
// gone gets no project mounts and no note.
func (s *Service) projectMounts(ctx context.Context, rec session.ChatRecord) ([]workspace.MountRequest, []string) {
	if rec.Kind == session.KindSubAgent {
		// A sub-agent works for its parent: it mounts what the parent's
		// project is now. The parent is read each turn, so a project that the
		// parent leaves or pulls follows at the child's next turn. A parent
		// that is gone leaves the child without project mounts. Whether the
		// child is fenced is decided by chatAccess before this is called.
		parent, err := s.getChatEffective(ctx, rec.Principal, rec.ParentID)
		if err != nil {
			return nil, nil
		}
		parent.Principal = rec.Principal
		rec = parent
	}
	home := session.HomePath(rec.Principal)
	abs := func(rel string) string { return path.Join(home, rel) }
	var out []workspace.MountRequest
	var notes []string
	homeID := ""
	if r, ok := s.resolveHome(ctx, rec.Principal, rec); ok {
		homeID = r.ID
		notes = append(notes, projectLine(r))
		out = append(out,
			workspace.MountRequest{Path: abs(r.FilesPath), At: session.GuestProjectName, Mode: workspace.ModeWrite, Role: workspace.RoleProjectFiles},
			// The memory is read only here: a chat changes it only by a
			// proposal that the owner confirms.
			workspace.MountRequest{Path: abs(r.MemoryPath), At: session.GuestProjectName + "-memory", Mode: workspace.ModeRead, Role: workspace.RoleProjectMemory})
	}
	dropped := 0
	for _, id := range rec.PulledProjects {
		if id == homeID {
			continue
		}
		reqs, _, err := s.resolvePulled(ctx, rec.Principal, id)
		if err != nil {
			if !errors.Is(err, projects.ErrNotFound) && !errors.Is(err, projects.ErrInvalid) && !errors.Is(err, session.ErrInvalid) {
				s.log.Warn("studio: pulled project is unreadable", "chat", rec.ID, "project", id, "error", err)
			}
			dropped++
			continue
		}
		out = append(out, reqs...)
	}
	switch {
	case dropped == 1:
		notes = append(notes, "1 pulled project is not available this turn and is not mounted.")
	case dropped > 1:
		notes = append(notes, fmt.Sprintf("%d pulled projects are not available this turn and are not mounted.", dropped))
	}
	return out, notes
}

// resolvePulled checks one entry of a chat's pulled list for the user and
// returns the mounts it asks for and the project name. An entry is an id of the
// user's own active project, or the reference of an active project that
// another user shares with the user now (the reference is the folder node, so
// a copied manifest id reaches nothing). The mounts of a shared project are the
// files, at the mode of the grant, and the memory, always read only; the paths
// come from the store's own nodes and the store's view clamps each mode again.
// The error is projects.ErrNotFound for anything that is not available.
func (s *Service) resolvePulled(ctx context.Context, user, id string) ([]workspace.MountRequest, string, error) {
	if s.d.Projects == nil {
		return nil, "", fmt.Errorf("%w: projects are not available", session.ErrInvalid)
	}
	if projects.IsSharedRef(id) {
		sh, err := s.d.Projects.ResolveSharedRef(ctx, principal(user), id)
		if errors.Is(err, projects.ErrInvalid) {
			return nil, "", fmt.Errorf("%w: a project shared with you is not usable now", session.ErrInvalid)
		}
		if err != nil {
			return nil, "", err
		}
		if sh.Status != projects.StatusActive {
			return nil, "", fmt.Errorf("%w: project %q is archived by its owner", session.ErrInvalid, sh.Name)
		}
		mode := workspace.ModeRead
		if sh.Mode == projects.ModeWrite {
			mode = workspace.ModeWrite
		}
		at := session.GuestPulledPrefix + sh.Slug
		return []workspace.MountRequest{
			{Path: sh.FilesPath, At: at, Mode: mode, Role: workspace.RoleSharedFiles, Label: sh.OwnerName},
			// The memory of a project that is not the user's is read only
			// whatever the grant says.
			{Path: sh.MemoryPath, At: at + "-memory", Mode: workspace.ModeRead, Role: workspace.RoleSharedMemory, Label: sh.OwnerName},
		}, sh.Name, nil
	}
	r, err := s.usableProject(ctx, user, id)
	if err != nil {
		return nil, "", err
	}
	home := session.HomePath(principal(user))
	at := session.GuestPulledPrefix + r.Slug
	return []workspace.MountRequest{
		{Path: path.Join(home, r.FilesPath), At: at, Mode: workspace.ModeRead, Role: workspace.RolePulledFiles},
		{Path: path.Join(home, r.MemoryPath), At: at + "-memory", Mode: workspace.ModeRead, Role: workspace.RolePulledMemory},
	}, r.Name, nil
}

// ListChatsInProject returns the chats of the user that belong to a project,
// newest first, at most limit of them (the store's own bound when limit is not
// positive). The project must be one of the user's: an unknown or foreign one
// is projects.ErrNotFound. An archived project is listed too.
func (s *Service) ListChatsInProject(ctx context.Context, user, projectID string, limit int) ([]session.ChatRecord, error) {
	if s.d.Projects == nil {
		return nil, fmt.Errorf("%w: projects are not available", session.ErrInvalid)
	}
	if _, err := s.d.Projects.Get(ctx, principal(user), projectID); err != nil {
		return nil, err
	}
	return s.d.Store.ListChatsInProject(ctx, principal(user), projectID, limit)
}

// CountChatsInProject returns how many chats of the user are in a project, up
// to limit. It does not check the project: the store lists only the chats of
// the user, so a foreign or unknown ID counts zero. Callers that must tell
// "none" from "not yours" use ListChatsInProject.
func (s *Service) CountChatsInProject(ctx context.Context, user, projectID string, limit int) (int, error) {
	recs, err := s.d.Store.ListChatsInProject(ctx, principal(user), projectID, limit)
	return len(recs), err
}

// ownProjectID is the ID of the project a chat works for: the project of its
// agent (a sub-agent's: its parent's), else the project in its record. It is
// read now, and "" means none, or unknown: a caller that gates on it refuses.
func (s *Service) ownProjectID(ctx context.Context, rec session.ChatRecord) string {
	ag, isAgent, err := s.projectAgentOf(ctx, rec)
	switch {
	case err != nil:
		return ""
	case isAgent:
		return ag.ProjectID
	}
	return rec.ProjectID
}
