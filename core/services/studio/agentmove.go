package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// A person can move a saved agent into a project, between projects, or out of
// a project. A normal save of an agent never does it (SaveAgent refuses a
// changed project), so a stale form cannot move an agent by accident. The
// move is this one operation, with a dry run that tells the person what it
// touches. Nothing is kept in memory between requests: the write of the agent
// record is the commit point, and the checks that run at every turn enforce
// the rest (a chat of the old project is refused, a file trigger outside the
// new folders is switched off at its run).

// ErrAgentMoved means the chat was started before its agent moved to another
// project (or out of its project). The chat stays readable and keeps its
// record, but it takes no new turn: its history belongs to the old fence.
var ErrAgentMoved = errors.New("studio: this agent moved to another project")

// AgentMovedText is what a person reads for ErrAgentMoved.
const AgentMovedText = "This agent moved to another project. Start a new chat."

// ErrMoveBlocked means the move cannot be done as asked. The text lists why.
var ErrMoveBlocked = errors.New("studio: the agent cannot be moved")

// The reasons a move is blocked (see AgentMoveReport.Blockers).
const (
	MoveBlockedBusy     = "busy"
	MoveBlockedShared   = "shared"
	MoveBlockedTriggers = "triggers_outside"
)

// agentNotesMovedDir is the folder of the home where the own notes of an agent
// that moved are kept. They are not deleted.
const agentNotesMovedDir = "Memory/Moved agent notes"

// AgentMove is what the person asks for.
type AgentMove struct {
	// ProjectID is the target: an active project of the owner, or empty to
	// leave the project.
	ProjectID string
	// DryRun reports the effects and writes nothing.
	DryRun bool
	// FilesMode is the mode of the project's files folder for a move into a
	// project: read or write (the default).
	FilesMode string
	// Folders is the folder list the agent keeps when it leaves a project.
	// It is required then, unless WholeHome is set.
	Folders []AgentFolderWrite
	// WholeHome lets an agent that leaves a project work on the whole home.
	// It never happens by default.
	WholeHome bool
	// RevokeShares removes the grants of an agent that is shared, so that it
	// can enter a project (a project agent cannot be shared).
	RevokeShares bool
	// DisableTriggers switches off the file triggers whose folder is outside
	// the new folders, instead of refusing the move.
	DisableTriggers bool
}

// MovedTrigger names a trigger the move touches.
type MovedTrigger struct{ ID, Name string }

// DroppedTools lists what the limits of the new project take away from the
// agent. It is empty for a move out of a project.
type DroppedTools struct {
	Skills, Addons, MCPServers, Collections []string
}

// AttachedTools lists tools a project attaches, by kind: the five lists of
// projects.Defaults. A list is never nil in a report.
type AttachedTools = projects.Defaults

func attachedOf(d projects.Defaults) AttachedTools {
	nn := func(l []string) []string { return append([]string{}, l...) }
	return AttachedTools{Skills: nn(d.Skills), Addons: nn(d.Addons), Connectors: nn(d.Connectors),
		MCPServers: nn(d.MCPServers), Collections: nn(d.Collections)}
}

// attachedMinus returns the entries of d that are not in other.
func attachedMinus(d, other projects.Defaults) AttachedTools {
	f := func(a, b []string) []string { return dropped(a, b) }
	return AttachedTools{Skills: f(d.Skills, other.Skills), Addons: f(d.Addons, other.Addons),
		Connectors: f(d.Connectors, other.Connectors), MCPServers: f(d.MCPServers, other.MCPServers),
		Collections: f(d.Collections, other.Collections)}
}

// AgentMoveReport is what a move does, or what it would do (DryRun).
type AgentMoveReport struct {
	DryRun bool
	// Moved is true when the agent record was written.
	Moved    bool
	From, To string
	// Chats is the number of chats of the agent that stop taking turns, and
	// Loops the number of their loops that end.
	Chats, Loops int
	// TriggersOutside are the enabled file triggers whose folder is not in the
	// new folders. They block the move unless DisableTriggers is set.
	TriggersOutside []MovedTrigger
	// Shares is the number of people the agent is shared with.
	Shares int
	// Proposals is the number of waiting project notes of the agent that are
	// voided.
	Proposals int
	// DroppedTools is what the new project's limits take away.
	DroppedTools DroppedTools
	// AttachedNew is what the new project attaches: the agent gets these on
	// every run once it is moved.
	AttachedNew AttachedTools
	// AttachedOld is what the old project attached and the new one does not:
	// these stop applying to the agent.
	AttachedOld AttachedTools
	// ProjectSkillsSwitch says the agent's project skills change: the skills
	// of the old project stop and those of the new project start. It is false
	// when neither project has a skill in its own layer.
	ProjectSkillsSwitch bool
	// MemoryGrantDropped says the grant to write the project's memory is
	// removed; the person grants it again in the agent's settings.
	MemoryGrantDropped bool
	// Blockers lists why the move cannot be done as asked, by the MoveBlocked
	// codes. A dry run reports them; a real move refuses.
	Blockers []string
	// Warnings lists steps after the commit that did not finish. The turn
	// checks keep the agent safe anyway.
	Warnings []string
}

// movedChat says whether a chat of an agent was started in another fence than
// the one the agent has now. A sub-agent chat follows its parent and is not
// judged here. A chat of a project agent must be in the project of the agent.
// A chat that is in a project because its agent was a project agent is
// scoped; once the agent has left the project (MovedAt is later than the
// chat) it is stale. A chat that the person put in a project, or a chat the
// agent took after it left, is not.
func movedChat(rec session.ChatRecord, ag agents.Agent) bool {
	if rec.Kind == session.KindSubAgent || rec.AgentID == "" || rec.AgentID != ag.ID {
		return false
	}
	if ag.ProjectID != "" {
		return rec.ProjectID != ag.ProjectID
	}
	return rec.ProjectID != "" && rec.ScopedAgent && rec.Created.Before(ag.MovedAt)
}

// MoveAgentProject moves an agent of the user into a project, between two, or
// out of one. See AgentMove for the choices and AgentMoveReport for the
// answer. The checks come before any write. A dry run never writes.
func (s *Service) MoveAgentProject(ctx context.Context, user, id string, in AgentMove) (AgentMoveReport, error) {
	if s.d.Agents == nil {
		return AgentMoveReport{}, ErrAgentsOff
	}
	ag, err := s.ownAgent(ctx, user, id)
	if err != nil {
		return AgentMoveReport{}, err
	}
	rep := AgentMoveReport{DryRun: in.DryRun, From: ag.ProjectID, To: in.ProjectID}
	if in.ProjectID == ag.ProjectID {
		return rep, fmt.Errorf("%w: the agent is already in this project", agents.ErrInvalid)
	}
	next, err := s.moveTarget(ctx, user, ag, in)
	if err != nil {
		return rep, err
	}
	if err := s.moveEffects(ctx, user, ag, next, in, &rep); err != nil {
		return rep, err
	}
	if in.DryRun {
		return rep, nil
	}
	if len(rep.Blockers) > 0 {
		return rep, fmt.Errorf("%w: %s", ErrMoveBlocked, moveBlockedText(rep))
	}
	if err := s.d.Agents.Put(ctx, next); err != nil {
		return rep, err
	}
	rep.Moved = true
	s.moveAfter(ctx, user, ag, next, in, &rep)
	return rep, nil
}

// moveTarget builds the agent as it is after the move: the target project (or
// none), its folders, and no grant to write the memory of a project. Nothing
// is written.
func (s *Service) moveTarget(ctx context.Context, user string, ag agents.Agent, in AgentMove) (agents.Agent, error) {
	p := principal(user)
	next := ag
	next.ProjectID = in.ProjectID
	next.Updated = time.Now().UTC()
	next.MovedAt = next.Updated
	next.Builtins = slices.DeleteFunc(slices.Clone(ag.Builtins), func(g string) bool { return g == builtins.GrantProjectMemoryWrite })
	if in.ProjectID != "" {
		if _, err := s.usableProject(ctx, user, in.ProjectID); err != nil {
			if errors.Is(err, session.ErrInvalid) {
				err = fmt.Errorf("%w: %w", agents.ErrInvalid, err)
			}
			return agents.Agent{}, err
		}
		fence, err := s.fenceOfProject(ctx, p, in.ProjectID)
		if err != nil {
			return agents.Agent{}, projectUnavailableErr(err)
		}
		mode := in.FilesMode
		switch mode {
		case "":
			mode = agents.FolderWrite
		case agents.FolderRead, agents.FolderWrite:
		default:
			return agents.Agent{}, fmt.Errorf("%w: files_mode must be read or write", agents.ErrInvalid)
		}
		next.Folders, next.FolderID, next.FolderMode = projectFolders(fence, mode), 0, ""
	} else {
		// Leaving a project never widens the agent to the whole home by
		// itself: the folders are named, or the person says so.
		switch {
		case len(in.Folders) > 0 && in.WholeHome:
			return agents.Agent{}, fmt.Errorf("%w: send folders or whole_home, not both", agents.ErrInvalid)
		case len(in.Folders) == 0 && !in.WholeHome:
			return agents.Agent{}, fmt.Errorf("%w: choose the folders the agent keeps, or allow the whole home", agents.ErrInvalid)
		}
		list := []agents.AgentFolder{}
		if len(in.Folders) > 0 {
			var err error
			if list, err = s.resolveAgentFolders(ctx, p, in.Folders); err != nil {
				return agents.Agent{}, err
			}
		}
		next.Folders, next.FolderID, next.FolderMode = list, 0, ""
	}
	if err := agents.Validate(next); err != nil {
		return agents.Agent{}, err
	}
	return next, nil
}

// moveEffects reads what the move touches and fills the report. It writes
// nothing.
func (s *Service) moveEffects(ctx context.Context, user string, ag, next agents.Agent, in AgentMove, rep *AgentMoveReport) error {
	p := principal(user)
	rep.MemoryGrantDropped = slices.Contains(ag.Builtins, builtins.GrantProjectMemoryWrite)
	// The chats: the ones that stop, and any that runs a turn now.
	stale := map[string]bool{}
	chats, err := s.ListChatsWhere(ctx, user, 0, func(r session.ChatRecord) bool { return r.AgentID == ag.ID })
	if err != nil {
		return err
	}
	busy := false
	for _, r := range chats {
		if movedChat(r, next) {
			stale[r.ID] = true
		}
		info, err := s.d.Store.Turn(ctx, p, r.ID)
		if err != nil {
			return err
		}
		busy = busy || info.Running
	}
	rep.Chats = len(stale)
	if busy {
		rep.Blockers = append(rep.Blockers, MoveBlockedBusy)
	}
	if s.loops != nil && len(stale) > 0 {
		all, err := s.loops.o.Store.ListOwner(ctx, p)
		if err != nil {
			return err
		}
		for _, l := range all {
			if l.State != loops.StateEnded && stale[l.ChatID] {
				rep.Loops++
			}
		}
	}
	// The people the agent is shared with. A project agent cannot be shared.
	if sh := s.shares(); sh != nil && ag.ShareRef != "" {
		if v, err := sh.Role(ctx, p, ag.ShareRef); err == nil {
			rep.Shares = v.SharedWith
		}
		if rep.Shares > 0 && next.ProjectID != "" && !in.RevokeShares {
			rep.Blockers = append(rep.Blockers, MoveBlockedShared)
		}
	}
	// The file triggers whose folder is outside the new folders.
	if outside, err := s.triggersOutside(ctx, user, ag, next, in); err != nil {
		return err
	} else if len(outside) > 0 {
		rep.TriggersOutside = outside
		if !in.DisableTriggers {
			rep.Blockers = append(rep.Blockers, MoveBlockedTriggers)
		}
	}
	// The waiting notes for the memory of a project.
	if s.changes != nil {
		pend, err := s.changes.store.List(ctx, p, contract.ProposalPending)
		if err != nil {
			return err
		}
		for _, pr := range pend {
			if pr.Agent == ag.ID && pr.Scope == contract.ScopeProject {
				rep.Proposals++
			}
		}
	}
	// What the old and the new project attach. The old project may be gone,
	// archived or broken: then it attached nothing that can be read.
	var oldAtt, newAtt projects.Defaults
	if ag.ProjectID != "" && s.d.Projects != nil {
		if r, err := s.d.Projects.Get(ctx, user, ag.ProjectID); err == nil {
			oldAtt = r.Manifest.Attached
		}
	}
	// The project skills change only when one of the two projects has some.
	rep.ProjectSkillsSwitch = s.hasProjectSkills(ctx, user, ag.ProjectID) || s.hasProjectSkills(ctx, user, next.ProjectID)
	// What the limits of the new project take away.
	if next.ProjectID != "" {
		r, err := s.fenceOfProject(ctx, p, next.ProjectID)
		if err != nil {
			return projectUnavailableErr(err)
		}
		newAtt = r.project.Manifest.Attached
		capped := capAgent(ag, r.project.Manifest.Defaults)
		rep.DroppedTools = DroppedTools{
			Skills: dropped(ag.Skills, capped.Skills), Addons: dropped(ag.Addons, capped.Addons),
			MCPServers: dropped(ag.MCPServers, capped.MCPServers), Collections: dropped(ag.Collections, capped.Collections),
		}
	}
	rep.AttachedNew = attachedOf(newAtt)
	rep.AttachedOld = attachedMinus(oldAtt, newAtt)
	return nil
}

// hasProjectSkills reports whether the project id of user has skills in its
// own layer. No project, a project that cannot be read and a skills folder that
// another person may write all answer no.
func (s *Service) hasProjectSkills(ctx context.Context, user, id string) bool {
	if id == "" {
		return false
	}
	list, blocked, err := s.ProjectSkills(ctx, user, id)
	return err == nil && !blocked && len(list) > 0
}

// dropped returns the entries of before that are not in after, never nil.
func dropped(before, after []string) []string {
	out := []string{}
	for _, v := range before {
		if !slices.Contains(after, v) {
			out = append(out, v)
		}
	}
	return out
}

// triggersOutside lists the enabled file triggers of the agent whose folder is
// not inside one of the folders the agent has after the move. An agent with no
// folders (the whole home) has none outside.
func (s *Service) triggersOutside(ctx context.Context, user string, ag, next agents.Agent, in AgentMove) ([]MovedTrigger, error) {
	if s.triggers == nil || !fencedAgent(next) {
		return nil, nil
	}
	all, err := s.triggers.ListTriggers(ctx, user)
	if err != nil {
		return nil, err
	}
	var roots []string
	for _, f := range next.FolderList() {
		if path, ok := s.FolderResolver().PathOf(ctx, principal(user), f.Node); ok {
			roots = append(roots, path)
		}
	}
	var out []MovedTrigger
	for _, t := range all {
		if t.AgentID != ag.ID || !t.Enabled || t.Kind != triggers.KindFile {
			continue
		}
		if !slices.ContainsFunc(roots, func(r string) bool { return workspace.Within(t.Folder, r) }) {
			out = append(out, MovedTrigger{ID: t.ID, Name: t.Name})
		}
	}
	return out, nil
}

// moveBlockedText is the sentence of a refused move.
func moveBlockedText(rep AgentMoveReport) string {
	var why []string
	for _, b := range rep.Blockers {
		switch b {
		case MoveBlockedBusy:
			why = append(why, "a chat of the agent is running a turn; stop it first")
		case MoveBlockedShared:
			why = append(why, "the agent is shared and a project agent cannot be shared; revoke the shares to continue")
		case MoveBlockedTriggers:
			names := make([]string, 0, len(rep.TriggersOutside))
			for _, t := range rep.TriggersOutside {
				names = append(names, t.Name)
			}
			why = append(why, "a file trigger watches a folder outside the new folders ("+strings.Join(names, ", ")+"); switch it off to continue")
		}
	}
	return strings.Join(why, "; ")
}

// moveAfter runs the effects that follow the commit. Each one is safe to run
// again, and a failure is reported, not fatal: the turn checks refuse a stale
// chat and a trigger outside the folders anyway.
func (s *Service) moveAfter(ctx context.Context, user string, ag, next agents.Agent, in AgentMove, rep *AgentMoveReport) {
	p := principal(user)
	warn := func(what string, err error) {
		s.log.Warn("studio: a step after the move of an agent did not finish", "agent", ag.ID, "step", what, "error", err)
		rep.Warnings = append(rep.Warnings, what)
	}
	if sh := s.shares(); sh != nil && in.RevokeShares && next.ProjectID != "" && ag.ShareRef != "" {
		if err := sh.RevokeAll(ctx, p, ag.ShareRef); err != nil {
			warn("shares", err)
		}
	}
	if s.triggers != nil {
		for _, t := range rep.TriggersOutside {
			if in.DisableTriggers {
				if _, err := s.triggers.SetTriggerEnabled(ctx, user, t.ID, false); err != nil {
					warn("triggers", err)
				}
			}
		}
	}
	if s.changes != nil {
		pend, err := s.changes.store.List(ctx, p, contract.ProposalPending)
		if err != nil {
			warn("proposals", err)
		}
		for _, pr := range pend {
			if pr.Agent != ag.ID || pr.Scope != contract.ScopeProject {
				continue
			}
			if ok, err := s.changes.store.Settle(ctx, p, pr.ID, contract.ProposalPending, contract.ProposalStale, time.Now().UTC(), 0); err != nil {
				warn("proposals", err)
			} else if ok {
				s.changes.audit(ctx, proposals.ActionStale, pr, 0, "voided: the agent moved to another project")
			}
		}
	}
	// The own notes of the agent start empty: the old ones are moved aside, in
	// the home, never deleted.
	if s.d.Files != nil {
		to := fmt.Sprintf("%s/%s-%s", agentNotesMovedDir, ag.ID, time.Now().UTC().Format("20060102-150405"))
		if _, err := s.d.Files.MoveHome(ctx, p, "Memory/agents/"+ag.ID, to); err != nil {
			warn("notes", err)
		}
	}
	if s.changes != nil {
		err := s.changes.store.AddEvent(ctx, proposals.Event{At: time.Now().UTC(), Owner: p, Action: "agent_move", Agent: ag.ID,
			Detail: fmt.Sprintf("moved from project %q to project %q", ag.ProjectID, next.ProjectID)})
		if err != nil {
			warn("audit", err)
		}
	}
}

// chatMoved reads the agent of a chat and says whether the chat is stale for
// it (see movedChat). A chat whose agent cannot be read is not judged here.
func (s *Service) chatMoved(ctx context.Context, rec session.ChatRecord) bool {
	if rec.AgentID == "" || s.d.Agents == nil || rec.Kind == session.KindSubAgent {
		return false
	}
	ag, err := s.storedChatAgent(ctx, rec.Principal, rec.AgentID)
	return err == nil && movedChat(rec, ag)
}
