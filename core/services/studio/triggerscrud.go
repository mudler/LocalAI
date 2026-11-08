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
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/wellknown"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Errors of the trigger service.
var (
	// ErrBadTrigger wraps the reason a trigger is refused, written for a person.
	ErrBadTrigger = errors.New("studio: invalid trigger")
	// ErrTooManyTriggers means the user has the most triggers they may have.
	// The host returns it with the limit that applied, in its text.
	ErrTooManyTriggers = errors.New("studio: too many triggers")
	// ErrProposalAnswered means the proposal is not waiting: it was accepted,
	// declined or undone already, maybe from another tab.
	ErrProposalAnswered = errors.New("studio: this proposal was already answered")
	// ErrCannotUndo means an accepted trigger cannot be taken back; the text
	// after it says why, for the person.
	ErrCannotUndo = errors.New("studio: the trigger cannot be undone")
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
	// RunLimit is the longest a run may take; zero means the administrator's
	// run limit. A value above it is kept and runs clamped to it.
	RunLimit time.Duration
	// AgentID is optional. Model is needed unless the agent or the source chat
	// names one. SourceChatID is the chat the trigger was made from.
	AgentID, Model, SourceChatID string
	Enabled                      bool
	// MinGap is the shortest time between two runs of a file trigger; zero
	// is the default of one minute.
	MinGap time.Duration
	// The plan of a run (see triggers.Trigger): what it reads, the file it
	// writes (a path in the user's home), the Home widget that shows that
	// file, and about how many tokens one run uses. All optional.
	Reads, Writes, Widget string
	EstTokens             int
	// Route is the connector place a proposal came from and where its runs
	// report. A person's form leaves it empty and an edit keeps the old one.
	Route triggers.Route
	// Acts are the action classes an unattended run of the trigger may use
	// (act.files, act.addons, act.addons:<id>, act.send, act.spend). Nil keeps
	// what the trigger has; an empty list takes everything away. Only a
	// person sets them: a model's proposal never carries acts.
	Acts []string
}

// keepPlan fills the fields of the plan and the gap that in leaves empty from
// old: the forms that edit a trigger do not show them, and a save from a form
// must not drop what the model described.
func keepPlan(old triggers.Trigger, in TriggerInput) TriggerInput {
	if in.Reads == "" {
		in.Reads = old.Reads
	}
	if in.Writes == "" {
		in.Writes = old.Writes
	}
	if in.Widget == "" {
		in.Widget = old.Widget
	}
	if in.EstTokens == 0 {
		in.EstTokens = old.EstTokens
	}
	if in.MinGap == 0 {
		in.MinGap = old.MinGap
	}
	if in.Acts == nil {
		in.Acts = old.Acts
	}
	return in
}

// MinChatCadence is the shortest time between two runs of a schedule that a
// model proposes in a chat. A person can choose a shorter one on the Triggers
// page; a model cannot, so a chat cannot quietly set up a costly loop.
const MinChatCadence = time.Hour

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
	if err != nil || t.Owner != principal(user) || !t.Proposed || t.IsChange() {
		return triggers.Trigger{}, triggers.ErrNotFound
	}
	return t, nil
}

// pendingProposal is ownedProposal for a proposal that still waits: one that
// was declined or undone answers ErrProposalAnswered.
func (h *Triggers) pendingProposal(ctx context.Context, user, id string) (triggers.Trigger, error) {
	t, err := h.ownedProposal(ctx, user, id)
	if err != nil {
		return t, err
	}
	if t.Expired(h.o.Clock()) {
		h.lapse(ctx, t)
		return triggers.Trigger{}, ErrProposalExpired
	}
	if !t.Pending() {
		return triggers.Trigger{}, ErrProposalAnswered
	}
	return t, nil
}

// countLive is how many of ts are triggers or waiting proposals: the records
// of a declined or undone proposal, and change records, take no place.
func countLive(ts []triggers.Trigger) int {
	n := 0
	for _, t := range ts {
		if !t.Answered() && !t.IsChange() {
			n++
		}
	}
	return n
}

// build turns an input into a trigger of the owner, checking everything that can
// be checked now. It does not set Since.
func (h *Triggers) build(ctx context.Context, owner string, in TriggerInput) (triggers.Trigger, error) {
	t := triggers.Trigger{Owner: owner, Name: strings.TrimSpace(in.Name), Prompt: in.Prompt, Enabled: in.Enabled, Kind: in.Kind,
		TZ: in.TZ, Quiet: in.Quiet, DailyCap: in.DailyCap, RunLimit: in.RunLimit, AgentID: in.AgentID, Model: in.Model, SourceChatID: in.SourceChatID,
		MinGap: in.MinGap, Reads: strings.TrimSpace(in.Reads), Widget: in.Widget, EstTokens: in.EstTokens, Route: in.Route}
	acts, err := cleanActs(in.Acts)
	if err != nil {
		return t, err
	}
	t.Acts = acts
	if w := strings.TrimSpace(in.Writes); w != "" {
		// A path of the home as the person writes it, stored as they see it:
		// the widget footer and the proposal card show it back.
		if _, err := h.svc.d.Files.Abs(owner, w); err != nil {
			return t, bad("writes is not a path in your files")
		}
		t.Writes = strings.TrimPrefix(w, "/")
	}
	// The agent comes first: a scoped agent's folder bounds a file trigger.
	var scope *folderScope
	if t.AgentID != "" {
		ag, err := h.svc.ownAgent(ctx, owner, t.AgentID)
		if err != nil {
			return t, err
		}
		if t.Model == "" {
			t.Model = ag.Model
		}
		if ag.HasFolder() {
			if scope, err = h.svc.liveFolder(ctx, owner, ag); err != nil {
				return t, scopeErr(err)
			}
		}
	}
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
		if err := h.checkTriggerFolder(ctx, owner, abs, scope); err != nil {
			return t, err
		}
		t.Folder = abs
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
	if max := h.Limits().MaxDailyCap; t.DailyCap > max {
		return t, bad("daily limit must be 0 to %d", max)
	}
	return t, nil
}

// errOutsideFolder is the refusal of a file trigger of a scoped agent whose
// folder is outside the agent's folder.
var errOutsideFolder = bad("the trigger's folder must be inside the agent's folder")

// scopeErr is the refusal of a trigger whose agent's folder cannot be found.
func scopeErr(err error) error {
	if errors.Is(err, ErrAgentFolderMissing) {
		return bad("the agent's folder is missing: choose a folder in the agent's settings")
	}
	return err
}

// checkTriggerFolder checks the folder abs of a file trigger: inside the
// agent's folder for a scoped agent (scope not nil), and a folder the agent
// (or, without a folder, the owner) can read. The check reads as the agent,
// so it says nothing about a folder outside.
func (h *Triggers) checkTriggerFolder(ctx context.Context, owner, abs string, scope *folderScope) error {
	if scope != nil && !scope.contains(abs) {
		return errOutsideFolder
	}
	n, err := h.svc.d.Files.Store().Stat(ctx, agentReader(owner, scope), abs)
	if err != nil || n.Kind != workspace.KindDir {
		shown := h.RelPath(owner, abs)
		if g, ok := scope.guest(abs); ok {
			shown = g
		}
		return bad("the folder %s does not exist in your files", shown)
	}
	return nil
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
	if countLive(mine) >= h.Limits().MaxPerUser {
		return triggers.Trigger{}, h.errTooMany()
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
	t, err := h.build(ctx, old.Owner, keepPlan(old, in))
	if err != nil {
		return triggers.Trigger{}, err
	}
	t.ID, t.Route = old.ID, old.Route
	// The answer to the proposal stays, and the edit is the reason the card
	// can no longer undo it.
	t.Outcome, t.OutcomeAt, t.Touched = old.Outcome, old.OutcomeAt, old.Touched || old.Outcome != ""
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
	t.Touched = old.Touched || old.Outcome != ""
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

// HasProposal says whether a trigger the chat proposed, or a change of one it
// asked for, waits for an answer. An error counts as yes: the caller holds
// back what it would add.
func (h *Triggers) HasProposal(ctx context.Context, user, chatID string) bool {
	all, err := h.o.Store.ListTriggers(ctx, principal(user))
	if err != nil {
		return true
	}
	return slices.ContainsFunc(all, func(t triggers.Trigger) bool {
		return (t.Pending() && !t.Expired(h.o.Clock()) && t.SourceChatID == chatID) || (t.IsChange() && t.Outcome == "" && t.Change.Chat == chatID)
	})
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
	now := h.o.Clock()
	return slices.DeleteFunc(all, func(t triggers.Trigger) bool { return !t.Pending() || t.Expired(now) }), nil
}

// ProposeTrigger stores a trigger that a model proposed. It is never enabled.
// Nothing runs until the owner confirms it. A proposal that a connector chat
// made (in.Route names the place) also gets a code to confirm it by text and a
// time after which it lapses, and counts against the hourly limit of its
// binding.
func (h *Triggers) ProposeTrigger(ctx context.Context, user string, in TriggerInput) (triggers.Trigger, error) {
	t, _, err := h.propose(ctx, user, in)
	return t, err
}

// propose is ProposeTrigger that also says whether the proposal is new: false
// when it returns the equal proposal that already waits.
func (h *Triggers) propose(ctx context.Context, user string, in TriggerInput) (triggers.Trigger, bool, error) {
	owner := principal(user)
	in.Enabled = false
	now := h.o.Clock()
	if in.Route.FromConnector() {
		in.Route.ProposedAt, in.Route.Expires = now.UTC(), now.UTC().Add(ProposalTTL)
		in.Route.Code = ""
	}
	t, err := h.build(ctx, owner, in)
	if err != nil {
		return triggers.Trigger{}, false, err
	}
	if err := h.checkCadence(t); err != nil {
		return triggers.Trigger{}, false, err
	}
	if err := h.checkOneOff(t); err != nil {
		return triggers.Trigger{}, false, err
	}
	h.wmu.Lock()
	defer h.wmu.Unlock()
	mine, err := h.o.Store.ListTriggers(ctx, owner)
	if err != nil {
		return triggers.Trigger{}, false, err
	}
	pending := 0
	for _, m := range mine {
		if !m.Pending() || m.Expired(now) {
			continue
		}
		// A model that proposes the same thing again, in this chat or the next,
		// gets the proposal that waits.
		if m.Name == t.Name && m.Prompt == t.Prompt && m.Kind == t.Kind && m.Schedule == t.Schedule &&
			m.TZ == t.TZ && m.Folder == t.Folder && m.AgentID == t.AgentID && m.Model == t.Model && sameActs(m.Acts, t.Acts) {
			return m, false, nil
		}
		pending++
	}
	switch {
	case pending >= maxProposals:
		return triggers.Trigger{}, false, ErrTooManyProposals
	case countLive(mine) >= h.Limits().MaxPerUser:
		return triggers.Trigger{}, false, h.errTooMany()
	}
	if t.Route.FromConnector() {
		if proposalsInHour(mine, t.Route.Binding, now) >= MaxProposalsPerHour {
			h.audit(ctx, proposals.ActionRefuse, t, "too many proposals from this binding in the last hour")
			return triggers.Trigger{}, false, ErrProposalRate
		}
		t.Route.Code = freshCode(mine, t.Route.Binding)
	}
	t.ID, t.Proposed = newID(), true
	if err := h.o.Store.PutTrigger(ctx, t); err != nil {
		return triggers.Trigger{}, false, err
	}
	if t.Route.FromConnector() {
		h.audit(ctx, proposals.ActionPropose, t, "")
	}
	return t, true, nil
}

// checkCadence refuses a schedule that runs more often than MinChatCadence.
// The zone does not change the gap enough to matter, so UTC is used when the
// proposal names none.
func (h *Triggers) checkCadence(t triggers.Trigger) error {
	if t.Kind != triggers.KindSchedule {
		return nil
	}
	sch, err := triggers.ParseSchedule(t.Schedule)
	if err != nil {
		return bad("%v", err)
	}
	loc, err := t.Location()
	if err != nil {
		return bad("%v", err)
	}
	if gap := sch.MinGap(h.o.Clock(), loc); gap > 0 && gap < MinChatCadence {
		return bad("a trigger proposed in a chat runs at most once an hour, and %q runs every %s. "+
			"Propose an hourly or slower schedule; the person can choose a shorter one on the Triggers page",
			t.Schedule, gapText(gap))
	}
	return nil
}

// checkOneOff refuses a one-off trigger whose time is too close, is past, or
// is too far ahead. A timer that a chat sets is at least a minute away, so it
// cannot become a tight loop, and at most MaxOneOffAhead away.
func (h *Triggers) checkOneOff(t triggers.Trigger) error {
	at, ok := t.OnceAt()
	if !ok {
		return nil
	}
	d := at.Sub(h.o.Clock())
	switch {
	case d < MinOneOff-30*time.Second:
		return bad("a one-off trigger must run at least a minute from now; %q is %s", t.Schedule, gapAgo(d))
	case d > MaxOneOffAhead:
		return bad("a one-off trigger can be at most %d days ahead", int(MaxOneOffAhead/(24*time.Hour)))
	}
	return nil
}

func gapAgo(d time.Duration) string {
	if d <= 0 {
		return "in the past"
	}
	return "only " + gapText(d) + " away"
}

func gapText(d time.Duration) string { return gapTextIn(hostmsg.Voice{}, d) }

// gapTextIn is gapText in the language of the voice.
func gapTextIn(v hostmsg.Voice, d time.Duration) string {
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	if m := int(d / time.Minute); m < 60 {
		if m == 1 {
			return v.T("gap.minute")
		}
		return v.T("gap.minutes", "n", m)
	}
	return d.Round(time.Minute).String()
}

// ConfirmProposal turns a proposal into a trigger that is on. A schedule
// proposal that names no time zone takes tz (the zone of the person who
// confirms); without one it runs in UTC.
func (h *Triggers) ConfirmProposal(ctx context.Context, user, id, tz string) (triggers.Trigger, error) {
	return h.confirm(ctx, user, id, tz, nil)
}

// confirm is ConfirmProposal that lets the caller change the trigger first, as
// the approval modes do to bound it. mod runs on the proposal before it is
// checked and stored.
func (h *Triggers) confirm(ctx context.Context, user, id, tz string, mod func(*triggers.Trigger)) (triggers.Trigger, error) {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	old, err := h.pendingProposal(ctx, user, id)
	if err != nil {
		return triggers.Trigger{}, err
	}
	t := old
	if t.Kind == triggers.KindSchedule && t.TZ == "" {
		t.TZ = tz
	}
	t.Proposed, t.Enabled = false, true
	t.Outcome, t.OutcomeAt, t.Touched = triggers.OutcomeAccepted, h.o.Clock().UTC(), false
	if mod != nil {
		mod(&t)
	}
	if err := t.Validate(); err != nil {
		return triggers.Trigger{}, bad("%v", err)
	}
	// A folder that was readable when it was proposed may be gone now, and
	// the agent's folder may have moved or gone since.
	scope, err := h.svc.agentScope(ctx, t.Owner, t.AgentID)
	switch {
	case errors.Is(err, agents.ErrNotFound):
		// The run says the agent was deleted, as for any trigger.
	case err != nil:
		return triggers.Trigger{}, scopeErr(err)
	}
	if t.Kind == triggers.KindFile {
		if scope != nil && !scope.contains(t.Folder) {
			return triggers.Trigger{}, errOutsideFolder
		}
		if n, err := h.svc.d.Files.Store().Stat(ctx, agentReader(t.Owner, scope), t.Folder); err != nil || n.Kind != workspace.KindDir {
			return triggers.Trigger{}, bad("the folder does not exist in your files any more")
		}
	}
	t = triggers.Arm(nil, t, h.o.Clock())
	if err := h.o.Store.PutTrigger(ctx, t); err != nil {
		return triggers.Trigger{}, err
	}
	h.Notify()
	h.linkWidget(ctx, t)
	return t, nil
}

// linkWidget names the confirmed trigger in the Home widget its plan names, so
// the widget shows when it runs. A missing widget or file is not an error: the
// widget can be added later and still finds its trigger by the plan.
func (h *Triggers) linkWidget(ctx context.Context, t triggers.Trigger) {
	if t.Widget == "" || !h.svc.d.Files.Available() {
		return
	}
	if _, err := wellknown.New(h.svc.d.Files).LinkWidget(ctx, t.Owner, t.Widget, t.ID); err != nil {
		h.log.Warn("studio: could not link a Home widget to its trigger", "trigger", t.ID, "error", err)
	}
}

// EditProposal replaces the fields of a proposal that waits for an answer. It
// stays a proposal: it is not enabled and runs nothing until it is confirmed.
func (h *Triggers) EditProposal(ctx context.Context, user, id string, in TriggerInput) (triggers.Trigger, error) {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	old, err := h.pendingProposal(ctx, user, id)
	if err != nil {
		return triggers.Trigger{}, err
	}
	in.Enabled = false
	t, err := h.build(ctx, old.Owner, keepPlan(old, in))
	if err != nil {
		return triggers.Trigger{}, err
	}
	if err := h.checkCadence(t); err != nil {
		return triggers.Trigger{}, err
	}
	t.ID, t.Proposed, t.Route = old.ID, true, old.Route
	if t.SourceChatID == "" {
		t.SourceChatID = old.SourceChatID
	}
	if err := h.o.Store.PutTrigger(ctx, t); err != nil {
		return triggers.Trigger{}, err
	}
	return t, nil
}

// DismissProposal declines a proposal. The record stays, hidden from the lists,
// so the chat can say it was declined and offer to restore it. Declining one
// twice is not an error.
func (h *Triggers) DismissProposal(ctx context.Context, user, id string) error {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	t, err := h.ownedProposal(ctx, user, id)
	if err != nil {
		return err
	}
	switch t.Outcome {
	case triggers.OutcomeDeclined:
		return nil
	case "":
		t.Outcome, t.OutcomeAt = triggers.OutcomeDeclined, h.o.Clock().UTC()
		return h.o.Store.PutTrigger(ctx, t)
	}
	return ErrProposalAnswered
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

// MaxRunsPerDay is the most runs one day of t can have: its daily limit, or
// fewer for a schedule that runs less often. A run started by hand is not
// counted.
func (h *Triggers) MaxRunsPerDay(t triggers.Trigger) int {
	limit := t.CapOrDefault()
	if t.Kind != triggers.KindSchedule {
		return limit
	}
	sch, err := triggers.ParseSchedule(t.Schedule)
	if err != nil {
		return limit
	}
	loc, err := t.Location()
	if err != nil {
		return limit
	}
	gap := sch.MinGap(h.o.Clock(), loc)
	if gap <= 0 {
		return 1 // runs once
	}
	if per := int((24*time.Hour + gap - 1) / gap); per < limit {
		return per
	}
	return limit
}

// RemoteModel says whether the model of a trigger runs on a remote service.
func (h *Triggers) RemoteModel(model string) bool { return h.svc.RemoteModel(model) }

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

// cleanActs checks the act names of a trigger and removes repeats.
func cleanActs(names []string) ([]string, error) {
	var out []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		switch {
		case !builtins.ValidActGrant(n):
			return nil, bad("%q is not an act; use act.files, act.addons, act.addons:<id>, act.send or act.spend", n)
		case !slices.Contains(out, n):
			out = append(out, n)
		}
	}
	return out, nil
}
