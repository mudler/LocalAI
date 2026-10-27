package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Errors of the trigger service.
var (
	// ErrBadTrigger wraps the reason a trigger is refused, written for a person.
	ErrBadTrigger = errors.New("studio: invalid trigger")
	// ErrTooManyTriggers means the user has the most triggers they may have.
	ErrTooManyTriggers = fmt.Errorf("studio: a user can have at most %d triggers", maxTriggersPerUser)
	// ErrTriggersOff means the service has no trigger host.
	ErrTriggersOff = errors.New("studio: triggers are not available")
)

// TriggerInput is the definition of a trigger as a user writes it.
type TriggerInput struct {
	Name, Prompt string
	Kind         triggers.Kind
	// Schedule and TZ are for a schedule trigger: see triggers.ParseSchedule.
	Schedule, TZ string
	// Folder is a path inside the user's home, such as /Contracts, for a file
	// trigger. Quiet and DailyCap are optional.
	Folder   string
	Quiet    time.Duration
	DailyCap int
	// AgentID is optional. Model is needed unless the agent or the source chat
	// names one. SourceChatID is the chat the trigger was made from.
	AgentID, Model, SourceChatID string
	Enabled                      bool
}

func bad(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBadTrigger, fmt.Sprintf(format, args...))
}

// Triggers returns the trigger host, or nil.
func (s *Service) Triggers() *Triggers { return s.triggers }

// SetTriggers gives the service its trigger host. The application calls it
// once, before the service is shared.
func (s *Service) SetTriggers(h *Triggers) { s.triggers = h }

func (h *Triggers) owned(ctx context.Context, user, id string) (triggers.Trigger, error) {
	t, err := h.o.Store.GetTrigger(ctx, id)
	if err != nil || t.Owner != principal(user) || t.Proposed {
		return triggers.Trigger{}, triggers.ErrNotFound // another user's trigger, or a proposal, is a missing one
	}
	return t, nil
}

func (h *Triggers) ownedProposal(ctx context.Context, user, id string) (triggers.Trigger, error) {
	t, err := h.o.Store.GetTrigger(ctx, id)
	if err != nil || t.Owner != principal(user) || !t.Proposed {
		return triggers.Trigger{}, triggers.ErrNotFound
	}
	return t, nil
}

// build turns an input into a trigger of the owner, checking everything that can
// be checked now. It does not set Since.
func (h *Triggers) build(ctx context.Context, owner string, in TriggerInput) (triggers.Trigger, error) {
	t := triggers.Trigger{Owner: owner, Name: strings.TrimSpace(in.Name), Prompt: in.Prompt, Enabled: in.Enabled, Kind: in.Kind,
		TZ: in.TZ, Quiet: in.Quiet, DailyCap: in.DailyCap, AgentID: in.AgentID, Model: in.Model, SourceChatID: in.SourceChatID}
	switch in.Kind {
	case triggers.KindSchedule:
		sch, err := triggers.ParseSchedule(in.Schedule)
		if err != nil {
			return t, bad("%v", err)
		}
		t.Schedule = sch.Text()
	case triggers.KindFile:
		if !h.svc.d.Files.Available() {
			return t, fmt.Errorf("%w: %s", files.ErrUnavailable, h.svc.d.Files.Unavailable())
		}
		abs, err := h.svc.d.Files.Abs(owner, in.Folder)
		if err != nil {
			return t, bad("the folder is not a path in your files")
		}
		n, err := h.svc.d.Files.Store().Stat(ctx, h.svc.d.Files.Actor(owner), abs)
		if err != nil || n.Kind != workspace.KindDir {
			return t, bad("the folder %s does not exist in your files", in.Folder)
		}
		t.Folder = abs
	}
	if t.AgentID != "" {
		ag, err := h.svc.Agent(ctx, owner, t.AgentID)
		if err != nil {
			return t, err
		}
		if t.Model == "" {
			t.Model = ag.Model
		}
	}
	if t.Model == "" && t.SourceChatID != "" {
		if src, err := h.svc.d.Store.GetChat(ctx, owner, t.SourceChatID); err == nil {
			t.Model = src.Model
		}
	}
	if t.Model == "" {
		return t, bad("choose a model: neither the agent nor a chat names one")
	}
	if err := t.Validate(); err != nil {
		return t, bad("%v", err)
	}
	return t, nil
}

// CreateTrigger makes a trigger of the user. It is armed now: it runs nothing
// for the time before.
func (h *Triggers) CreateTrigger(ctx context.Context, user string, in TriggerInput) (triggers.Trigger, error) {
	owner := principal(user)
	t, err := h.build(ctx, owner, in)
	if err != nil {
		return triggers.Trigger{}, err
	}
	h.wmu.Lock()
	defer h.wmu.Unlock()
	mine, err := h.o.Store.ListTriggers(ctx, owner)
	if err != nil {
		return triggers.Trigger{}, err
	}
	if len(mine) >= maxTriggersPerUser {
		return triggers.Trigger{}, ErrTooManyTriggers
	}
	t.ID = newID()
	t = triggers.Arm(nil, t, h.o.Clock())
	if err := h.o.Store.PutTrigger(ctx, t); err != nil {
		return triggers.Trigger{}, err
	}
	h.Notify()
	return t, nil
}

// UpdateTrigger replaces the definition of a trigger of the user. A change of
// when it runs, or switching it on, re-arms it.
func (h *Triggers) UpdateTrigger(ctx context.Context, user, id string, in TriggerInput) (triggers.Trigger, error) {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	old, err := h.owned(ctx, user, id)
	if err != nil {
		return triggers.Trigger{}, err
	}
	t, err := h.build(ctx, old.Owner, in)
	if err != nil {
		return triggers.Trigger{}, err
	}
	t.ID = old.ID
	t = triggers.Arm(&old, t, h.o.Clock())
	if err := h.o.Store.PutTrigger(ctx, t); err != nil {
		return triggers.Trigger{}, err
	}
	h.Notify()
	return t, nil
}

// SetTriggerEnabled switches a trigger on or off.
func (h *Triggers) SetTriggerEnabled(ctx context.Context, user, id string, on bool) (triggers.Trigger, error) {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	old, err := h.owned(ctx, user, id)
	if err != nil {
		return triggers.Trigger{}, err
	}
	t := old
	t.Enabled = on
	t = triggers.Arm(&old, t, h.o.Clock())
	if err := h.o.Store.PutTrigger(ctx, t); err != nil {
		return triggers.Trigger{}, err
	}
	h.Notify()
	return t, nil
}

// GetTrigger returns a trigger of the user.
func (h *Triggers) GetTrigger(ctx context.Context, user, id string) (triggers.Trigger, error) {
	return h.owned(ctx, user, id)
}

// ListTriggers returns the user's triggers sorted by name, without proposals.
func (h *Triggers) ListTriggers(ctx context.Context, user string) ([]triggers.Trigger, error) {
	all, err := h.o.Store.ListTriggers(ctx, principal(user))
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(t triggers.Trigger) bool { return t.Proposed }), nil
}

// maxProposals is how many proposals of a user may wait for an answer.
const maxProposals = 10

// ErrTooManyProposals means the user has the most proposals they may have
// waiting.
var ErrTooManyProposals = fmt.Errorf("studio: %d trigger proposals are waiting; confirm or dismiss one first", maxProposals)

// Proposals returns the proposals of the user that wait for an answer.
func (h *Triggers) Proposals(ctx context.Context, user string) ([]triggers.Trigger, error) {
	all, err := h.o.Store.ListTriggers(ctx, principal(user))
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(t triggers.Trigger) bool { return !t.Proposed }), nil
}

// ProposeTrigger stores a trigger that a model proposed. It is never enabled.
// Nothing runs until the owner confirms it.
func (h *Triggers) ProposeTrigger(ctx context.Context, user string, in TriggerInput) (triggers.Trigger, error) {
	owner := principal(user)
	in.Enabled = false
	t, err := h.build(ctx, owner, in)
	if err != nil {
		return triggers.Trigger{}, err
	}
	h.wmu.Lock()
	defer h.wmu.Unlock()
	mine, err := h.o.Store.ListTriggers(ctx, owner)
	if err != nil {
		return triggers.Trigger{}, err
	}
	pending := 0
	for _, m := range mine {
		if !m.Proposed {
			continue
		}
		// A model that proposes the same thing again, in this chat or the next,
		// gets the proposal that waits.
		if m.Name == t.Name && m.Prompt == t.Prompt && m.Kind == t.Kind && m.Schedule == t.Schedule &&
			m.TZ == t.TZ && m.Folder == t.Folder && m.AgentID == t.AgentID && m.Model == t.Model {
			return m, nil
		}
		pending++
	}
	switch {
	case pending >= maxProposals:
		return triggers.Trigger{}, ErrTooManyProposals
	case len(mine) >= maxTriggersPerUser:
		return triggers.Trigger{}, ErrTooManyTriggers
	}
	t.ID, t.Proposed = newID(), true
	if err := h.o.Store.PutTrigger(ctx, t); err != nil {
		return triggers.Trigger{}, err
	}
	return t, nil
}

// ConfirmProposal turns a proposal into a trigger that is on. A schedule
// proposal that names no time zone takes tz (the zone of the person who
// confirms); without one it runs in UTC.
func (h *Triggers) ConfirmProposal(ctx context.Context, user, id, tz string) (triggers.Trigger, error) {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	old, err := h.ownedProposal(ctx, user, id)
	if err != nil {
		return triggers.Trigger{}, err
	}
	t := old
	if t.Kind == triggers.KindSchedule && t.TZ == "" {
		t.TZ = tz
	}
	t.Proposed, t.Enabled = false, true
	if err := t.Validate(); err != nil {
		return triggers.Trigger{}, bad("%v", err)
	}
	// A folder that was readable when it was proposed may be gone now.
	if t.Kind == triggers.KindFile {
		if n, err := h.svc.d.Files.Store().Stat(ctx, h.svc.d.Files.Actor(t.Owner), t.Folder); err != nil || n.Kind != workspace.KindDir {
			return triggers.Trigger{}, bad("the folder does not exist in your files any more")
		}
	}
	t = triggers.Arm(nil, t, h.o.Clock())
	if err := h.o.Store.PutTrigger(ctx, t); err != nil {
		return triggers.Trigger{}, err
	}
	h.Notify()
	return t, nil
}

// EditProposal replaces the fields of a proposal that waits for an answer. It
// stays a proposal: it is not enabled and runs nothing until it is confirmed.
func (h *Triggers) EditProposal(ctx context.Context, user, id string, in TriggerInput) (triggers.Trigger, error) {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	old, err := h.ownedProposal(ctx, user, id)
	if err != nil {
		return triggers.Trigger{}, err
	}
	in.Enabled = false
	t, err := h.build(ctx, old.Owner, in)
	if err != nil {
		return triggers.Trigger{}, err
	}
	t.ID, t.Proposed = old.ID, true
	if t.SourceChatID == "" {
		t.SourceChatID = old.SourceChatID
	}
	if err := h.o.Store.PutTrigger(ctx, t); err != nil {
		return triggers.Trigger{}, err
	}
	return t, nil
}

// DismissProposal deletes a proposal.
func (h *Triggers) DismissProposal(ctx context.Context, user, id string) error {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	if _, err := h.ownedProposal(ctx, user, id); err != nil {
		return err
	}
	return h.o.Store.DeleteTrigger(ctx, id)
}

// TriggerRuns returns the latest runs of a trigger of the user, newest first.
func (h *Triggers) TriggerRuns(ctx context.Context, user, id string, limit int) ([]triggers.Run, error) {
	if _, err := h.owned(ctx, user, id); err != nil {
		return nil, err
	}
	return h.o.Store.ListRuns(ctx, id, limit)
}

// DeleteTrigger removes a trigger of the user: it is switched off, its queued
// runs are dropped, its running run is stopped, and its chats stay.
func (h *Triggers) DeleteTrigger(ctx context.Context, user, id string) error {
	h.wmu.Lock()
	t, err := h.owned(ctx, user, id)
	if err != nil {
		h.wmu.Unlock()
		return err
	}
	// Off first: no pass starts a new run while the old ones are dropped.
	t.Enabled = false
	if err := h.o.Store.PutTrigger(ctx, t); err != nil {
		h.wmu.Unlock()
		return err
	}
	h.wmu.Unlock()

	runs, err := h.o.Store.ListRuns(ctx, id, 0)
	if err != nil {
		return err
	}
	for _, run := range runs {
		switch run.Status {
		case triggers.StatusQueued:
			run.Status, run.Reason, run.Ended = triggers.StatusSkipped, triggers.ReasonDropped, h.o.Clock().UTC()
			if err := h.o.Store.SetRunStatus(ctx, run); err != nil && !errors.Is(err, triggers.ErrNotFound) {
				return err
			}
		case triggers.StatusRunning:
			if run.ChatID != "" {
				h.stopRun(run.ChatID)
			}
		}
	}
	h.wmu.Lock()
	defer h.wmu.Unlock()
	return h.o.Store.DeleteTrigger(ctx, id)
}

// RunTrigger starts a run of a trigger of the user now, whether the trigger is
// on or not. On the leader the Runner does it; elsewhere the run is claimed
// queued and the leader starts it at its next pass.
func (h *Triggers) RunTrigger(ctx context.Context, user, id string) (triggers.Run, error) {
	t, err := h.owned(ctx, user, id)
	if err != nil {
		return triggers.Run{}, err
	}
	if r := h.currentRunner(); r != nil {
		run, err := r.RunNow(ctx, t.ID)
		if errors.Is(err, triggers.ErrStopped) {
			return triggers.Run{}, ErrTriggersClosed
		}
		return run, err
	}
	if h.closed() {
		return triggers.Run{}, ErrTriggersClosed
	}
	runID := newID()
	run := triggers.Run{ID: runID, TriggerID: t.ID, DueKey: triggers.ManualPrefix + runID, Due: h.o.Clock().UTC(), Status: triggers.StatusQueued}
	if _, err := h.o.Store.Claim(ctx, run); err != nil {
		return triggers.Run{}, err
	}
	h.Notify()
	return run, nil
}

// PreviewTrigger returns the next n times a schedule text runs in a time zone.
func (h *Triggers) PreviewTrigger(text, tz string, n int) ([]time.Time, error) {
	sch, err := triggers.ParseSchedule(text)
	if err != nil {
		return nil, bad("%v", err)
	}
	loc, err := triggers.Trigger{TZ: tz}.Location()
	if err != nil {
		return nil, bad("%v", err)
	}
	out := make([]time.Time, 0, n)
	at := h.o.Clock()
	for len(out) < n {
		next, ok := sch.Next(at, loc)
		if !ok {
			break
		}
		out = append(out, next)
		at = next
	}
	return out, nil
}

// NextRun returns when a schedule trigger runs next; false for a trigger that
// is off, a file trigger, or a schedule with no later time.
func (h *Triggers) NextRun(t triggers.Trigger) (time.Time, bool) {
	if !t.Enabled || t.Kind != triggers.KindSchedule {
		return time.Time{}, false
	}
	sch, err := triggers.ParseSchedule(t.Schedule)
	if err != nil {
		return time.Time{}, false
	}
	loc, err := t.Location()
	if err != nil {
		return time.Time{}, false
	}
	return sch.Next(h.o.Clock(), loc)
}

// RelPath returns a path of the owner's home as the owner sees it ("/Inbox/a.txt"),
// or the path itself when it is outside the home.
func (h *Triggers) RelPath(user, abs string) string {
	if rel := h.svc.d.Files.Rel(principal(user), abs); rel != "" {
		return rel
	}
	return abs
}

// RelFolder returns a file trigger's folder as a path in its owner's home.
func (h *Triggers) RelFolder(t triggers.Trigger) string {
	if t.Kind != triggers.KindFile {
		return ""
	}
	if rel := h.svc.d.Files.Rel(t.Owner, t.Folder); rel != "" {
		return rel
	}
	return t.Folder
}
