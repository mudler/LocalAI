package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// A saved agent made in a project (agents.Agent.ProjectID) is fenced to that
// project: its folders are the project's files folder and its memory folder,
// the memory one read only. The project is read from the stores at every
// check and nothing is kept, so an archive, a delete, a restore or a rename
// applies at once on every frontend: the agent is available exactly while its
// project exists, is active and has a valid manifest and both folders.

// ErrProjectUnavailable means the project of a saved agent is missing,
// archived or broken, or the agent's folders are no longer the project's two
// folders. The agent does not start; nothing falls back to the whole home.
var ErrProjectUnavailable = errors.New("project unavailable")

// ErrProjectFoldersChanged is ErrProjectUnavailable for an agent whose folders
// are no longer the project's two folders: one of them was replaced (deleted
// and made again, so its node changed). The project is fine; saving the agent
// again reconnects it. Every check that refuses on ErrProjectUnavailable
// refuses on this too.
var ErrProjectFoldersChanged = fmt.Errorf("%w: the folders of the project changed", ErrProjectUnavailable)

// ProjectFoldersChangedText is what a person reads for ErrProjectFoldersChanged.
const ProjectFoldersChangedText = "The folders of this agent's project changed. Save the agent again to reconnect it to the files and memory folders of the project."

// ProjectUnavailableText is what a person reads when ErrProjectUnavailable
// stops a chat or a request.
const ProjectUnavailableText = "The project of this agent is archived, deleted or broken, so the agent cannot work now. Restore the project to use it again."

// fencedAgent says whether the chats of an agent work inside a fence: it has a
// folder, or it belongs to a project. A project agent is fenced even when its
// list of folders is empty (an old or hand-made record): it never gets the
// whole home.
func fencedAgent(ag agents.Agent) bool { return ag.HasFolder() || ag.ProjectID != "" }

// projectFence is the project of a project agent with its two folders as they
// are now.
type projectFence struct {
	project       projects.Resolved
	files, memory projects.FenceNode
}

// projectOfAgent returns the project of a project agent, or ErrProjectUnavailable
// unless the project exists, is active and has a valid manifest. An error of
// the store itself is returned as it is: the caller refuses on any error.
func (s *Service) projectOfAgent(ctx context.Context, ag agents.Agent) (projects.Resolved, error) {
	f, err := s.fenceOfAgent(ctx, ag)
	return f.project, err
}

// fenceOfAgent is projectOfAgent with the project's files and memory nodes.
func (s *Service) fenceOfAgent(ctx context.Context, ag agents.Agent) (projectFence, error) {
	if ag.ProjectID == "" {
		return projectFence{}, ErrProjectUnavailable
	}
	return s.fenceOfProject(ctx, principal(ag.Principal), ag.ProjectID)
}

// fenceOfProject resolves a project once: it must exist, be active and have a
// valid manifest, and then its two fence nodes are read. ErrProjectUnavailable
// otherwise; an error of the store itself is returned as it is.
func (s *Service) fenceOfProject(ctx context.Context, owner, projectID string) (projectFence, error) {
	if projectID == "" || s.d.Projects == nil {
		return projectFence{}, ErrProjectUnavailable
	}
	ag := agents.Agent{ProjectID: projectID}
	r, err := s.d.Projects.Get(ctx, owner, ag.ProjectID)
	if errors.Is(err, projects.ErrNotFound) {
		// A project that was deleted has no row, but its folder may be back
		// from the Trash: the manifests are the truth, so list once (which
		// reads them again) and look again. Without a folder it stays gone.
		// The list is the one reindex of this check: a row it brings back is
		// judged by its status, and only an active one is read again.
		rows, lerr := s.d.Projects.List(ctx, owner)
		if lerr != nil || !slices.ContainsFunc(rows, func(p projects.Project) bool { return p.ID == ag.ProjectID && p.Status == projects.StatusActive }) {
			return projectFence{}, ErrProjectUnavailable
		}
		r, err = s.d.Projects.Get(ctx, owner, ag.ProjectID)
	}
	switch {
	case errors.Is(err, projects.ErrNotFound), errors.Is(err, projects.ErrInvalid), errors.Is(err, projects.ErrForbidden):
		return projectFence{}, ErrProjectUnavailable
	case err != nil:
		return projectFence{}, err
	case r.Status != projects.StatusActive:
		return projectFence{}, ErrProjectUnavailable
	}
	files, memory, err := s.d.Projects.FenceRootsOf(ctx, owner, r)
	switch {
	case errors.Is(err, projects.ErrInvalid):
		return projectFence{}, ErrProjectUnavailable
	case err != nil:
		return projectFence{}, err
	}
	return projectFence{project: r, files: files, memory: memory}, nil
}

// projectFolders is the list a project agent keeps for the project: the files
// folder at mode (read or write) and the memory folder at read, in that order.
func projectFolders(f projectFence, filesMode string) []agents.AgentFolder {
	if filesMode != agents.FolderRead {
		filesMode = agents.FolderWrite
	}
	return []agents.AgentFolder{{Node: f.files.Node, Mode: filesMode}, {Node: f.memory.Node, Mode: agents.FolderRead}}
}

// fenceProjectFolders checks the folders of a project agent at save and
// returns the list to keep. The folders are only the project's files and
// memory folders, the files folder is always there, and the memory folder is
// always read: a write on it is lowered to read (clamped is true). No folders
// at all become files at write and memory at read.
func fenceProjectFolders(f projectFence, list []agents.AgentFolder) (out []agents.AgentFolder, clamped bool, err error) {
	if len(list) == 0 {
		return projectFolders(f, agents.FolderWrite), false, nil
	}
	filesMode, haveFiles := "", false
	for _, l := range list {
		switch l.Node {
		case f.files.Node:
			filesMode, haveFiles = l.Mode, true
		case f.memory.Node:
			clamped = clamped || l.Mode == agents.FolderWrite
		default:
			return nil, false, folderRefused("a project agent can only use the files and memory folders of its project")
		}
	}
	if !haveFiles {
		return nil, false, folderRefused("a project agent needs the files folder of its project")
	}
	return projectFolders(f, filesMode), clamped, nil
}

// liveProjectFolder is liveFolder for a project agent: the project must be
// available now and the agent's folders must still be exactly the project's
// files and memory nodes. The nodes follow a rename of the project folder; a
// folder that was replaced by another, or an agent record with other folders,
// is ErrProjectUnavailable, never a wider fence.
func (s *Service) liveProjectFolder(ctx context.Context, p string, ag agents.Agent) (*folderScope, error) {
	if principal(ag.Principal) != principal(p) {
		return nil, ErrProjectUnavailable
	}
	f, err := s.fenceOfAgent(ctx, ag)
	if err != nil {
		return nil, err
	}
	if !foldersFitFence(ag, f) {
		return nil, ErrProjectFoldersChanged
	}
	list := ag.FolderList()
	sc := &folderScope{}
	for i, nd := range []projects.FenceNode{f.files, f.memory} {
		path, ok := s.FolderResolver().PathOf(ctx, p, nd.Node)
		if !ok {
			return nil, ErrProjectUnavailable
		}
		// The memory folder is read only whatever the record says.
		mode := workspace.ModeRead
		if i == 0 && list[0].Mode == agents.FolderWrite {
			mode = workspace.ModeWrite
		}
		sc.folders = append(sc.folders, scopedFolder{node: nd.Node, mode: mode, path: path})
	}
	capModes(sc.folders)
	return sc, nil
}

// foldersFitFence says whether the folders of an agent are exactly the files
// and memory nodes of its project's fence. The run-time check and the
// unavailable flag of the project's agent list both use it.
func foldersFitFence(ag agents.Agent, f projectFence) bool {
	list := ag.FolderList()
	return len(list) == 2 && list[0].Node == f.files.Node && list[1].Node == f.memory.Node
}

// checkProjectAgent is the save-time rule: the project of the agent must be
// available now, and the agent's folders are fenced to it. It returns the
// agent with the folders it keeps.
//
// fromOld says the folders are the ones of the stored record (a replace that
// sent no folder list). Then both folders are rebuilt from the project at the
// stored files mode, so a save reconnects the agent whatever happened to the
// folders. The nodes always come from the project itself, so nothing of the
// record that is foreign is kept. A folder list the caller sends is never
// healed: a foreign node in it is refused.
func (s *Service) checkProjectAgent(ctx context.Context, a agents.Agent, fromOld bool) (agents.Agent, error) {
	f, err := s.fenceOfAgent(ctx, a)
	if err != nil {
		return agents.Agent{}, err
	}
	in := a.FolderList()
	if fromOld {
		in = healFolders(f, in)
	}
	list, clamped, err := fenceProjectFolders(f, in)
	if err != nil {
		return agents.Agent{}, err
	}
	if clamped {
		s.log.Warn("studio: the memory folder of a project agent is read only; its write mode was lowered to read", "agent", a.ID, "project", a.ProjectID)
	}
	a.Folders, a.FolderID, a.FolderMode = list, 0, ""
	return a, nil
}

// projectUnavailableErr is the error of a save for a project that cannot take
// agents now.
func projectUnavailableErr(err error) error {
	if errors.Is(err, ErrProjectUnavailable) {
		return fmt.Errorf("%w: %w", agents.ErrInvalid, err)
	}
	return err
}

// healFolders rebuilds the folders of a stored record from the project: the
// files folder at the mode the record had and the memory folder at read. The
// nodes always come from the project itself, so a node of the record that is
// not one of the project's two (a folder that was replaced, renamed away or
// moved, or a foreign one) is dropped and never kept. The stored mode is the
// one of the files folder when the record still has it, else the one of the
// first entry that is not the memory folder: a read only agent stays read
// only. It is never widened.
func healFolders(f projectFence, list []agents.AgentFolder) []agents.AgentFolder {
	mode := agents.FolderWrite
	found := false
	for _, l := range list {
		if l.Node == f.files.Node {
			mode, found = l.Mode, true
			break
		}
	}
	if !found {
		for _, l := range list {
			if l.Node != f.memory.Node {
				mode = l.Mode
				break
			}
		}
	}
	return projectFolders(f, mode)
}
