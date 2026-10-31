package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// A model in a chat can ask to edit, pause, resume or delete a trigger of the
// person. The request is a change record in the trigger store (a Trigger with
// Change set, Proposed and never enabled), so it is kept in the database like a
// proposal and every frontend reads the same state: nothing of it lives in
// memory. An edit or a delete waits for the person to apply it on its card; a
// pause or a resume, which the person can take back at once, applies when the
// model asks and the card is a receipt with Undo. While a change waits, the
// record holds the definition it would give the trigger; once applied, the
// definition the trigger had before, so Undo can put it back.

// ChangeCardKind is the Kind of the tool card that points at a change record;
// the card's ID is the record's ID.
const ChangeCardKind = "trigger_change"

// ChangeUndoWindow is how long an applied change can be undone. After it the
// person changes the trigger on the Triggers page.
const ChangeUndoWindow = 24 * time.Hour

// changeRetention is how long an answered change record is kept, so the card
// in the chat still shows its receipt. Older records are removed the next time
// the owner's model asks for a change.
const changeRetention = 7 * 24 * time.Hour

// maxPendingChanges is how many changes of a user may wait for an answer.
const maxPendingChanges = 10

// Why an applied change cannot be undone. The interface words the codes.
const (
	UndoExpired = "expired"
	UndoRemoved = "removed"
	// UndoFull is set on a delete that cannot be undone because the user has
	// as many triggers as they may.
	UndoFull = "full"
)

var (
	// ErrChangeStale means the trigger changed, or was removed, after the
	// change was asked for; the text after it says which.
	ErrChangeStale = errors.New("studio: the trigger changed since this change was proposed")
	// ErrTooManyChanges means the user has the most changes they may have
	// waiting.
	ErrTooManyChanges = fmt.Errorf("studio: %d trigger changes are waiting; apply or dismiss one first", maxPendingChanges)
	// ErrUnknownTrigger means a reference names no trigger of the user.
	ErrUnknownTrigger = errors.New("studio: no trigger of yours has this reference")
)

// ChangeState is what became of a change.
type ChangeState string

// The states of a change.
const (
	ChangePending   ChangeState = "pending"
	ChangeApplied   ChangeState = "applied"
	ChangeDismissed ChangeState = "dismissed"
	ChangeUndone    ChangeState = "undone"
)

// ChangeView is a change as the chat card shows it.
type ChangeView struct {
	State ChangeState
	// Op is triggers.ChangeUpdate, ChangeDelete, ChangePause or ChangeResume.
	Op string
	// ID is the change record's ID, Target the trigger's.
	ID, Target string
	// Before and After are the trigger before and after the change. After is
	// empty for a delete, and Before is the kept copy once a delete applied.
	Before, After triggers.Trigger
	// HasAfter is false for a delete.
	HasAfter bool
	// Fields are the fields the change sets: name, prompt, when, time_zone,
	// daily_cap, enabled, reads, writes, widget, estimated_tokens.
	Fields []string
	// At is when the change was answered; zero while it waits.
	At time.Time
	// Stale is set on a waiting change whose trigger changed or is gone, so it
	// can no longer apply.
	Stale bool
	// CanUndo is set on an applied change that can still be undone;
	// UndoBlocked says why not otherwise: UndoExpired, UndoEdited,
	// UndoRemoved or UndoFull.
	CanUndo     bool
	UndoBlocked string
	// LastRun is the newest run of the trigger, if any.
	LastRun *triggers.Run
}

// TriggerRef is the short reference of a trigger that list_triggers gives a
// model: stable for the trigger, and not its ID, which the model never sees.
func TriggerRef(id string) string {
	sum := sha256.Sum256([]byte("trigger-ref:" + id))
	return "t" + hex.EncodeToString(sum[:4])
}

// ResolveTrigger finds a trigger of the user by its reference or, failing
// that, by its exact name (letter case aside). Another user's trigger, a
// proposal and a change record are never found.
func (h *Triggers) ResolveTrigger(ctx context.Context, user, ref string) (triggers.Trigger, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return triggers.Trigger{}, fmt.Errorf("%w: give the ref from list_triggers", ErrUnknownTrigger)
	}
	all, err := h.ListTriggers(ctx, user)
	if err != nil {
		return triggers.Trigger{}, err
	}
	var named []triggers.Trigger
	for _, t := range all {
		if TriggerRef(t.ID) == ref {
			return t, nil
		}
		if strings.EqualFold(strings.TrimSpace(t.Name), ref) {
			named = append(named, t)
		}
	}
	switch len(named) {
	case 1:
		return named[0], nil
	case 0:
		return triggers.Trigger{}, fmt.Errorf("%w: %q; call list_triggers for the refs", ErrUnknownTrigger, ref)
	}
	return triggers.Trigger{}, fmt.Errorf("%w: %d triggers are named %q; use the ref from list_triggers", ErrUnknownTrigger, len(named), ref)
}

// stamp is a fingerprint of what a trigger does: its definition and whether
// it is on, not when it was armed or how a proposal of it was answered.
func stamp(t triggers.Trigger) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%q|%q|%t|%s|%q|%q|%q|%d|%d|%q|%q|%d|%q|%q|%q|%d", t.Name, t.Prompt, t.Enabled, t.Kind, t.Schedule, t.TZ,
		t.Folder, t.Quiet, t.DailyCap, t.AgentID, t.Model, t.MinGap, t.Reads, t.Writes, t.Widget, t.EstTokens)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// withDefinition returns dst with the definition of src: what a run does and
// when. The ID, owner, state and source chat of dst stay.
func withDefinition(dst, src triggers.Trigger) triggers.Trigger {
	dst.Name, dst.Prompt, dst.Kind, dst.Schedule, dst.TZ = src.Name, src.Prompt, src.Kind, src.Schedule, src.TZ
	dst.Folder, dst.Quiet, dst.DailyCap, dst.AgentID, dst.Model = src.Folder, src.Quiet, src.DailyCap, src.AgentID, src.Model
	dst.MinGap, dst.Reads, dst.Writes, dst.Widget, dst.EstTokens = src.MinGap, src.Reads, src.Writes, src.Widget, src.EstTokens
	return dst
}

// changedFields lists the fields that differ between two triggers, in the
// order the card shows them.
func changedFields(a, b triggers.Trigger) []string {
	var out []string
	add := func(name string, differ bool) {
		if differ {
			out = append(out, name)
		}
	}
	add("name", a.Name != b.Name)
	add("prompt", a.Prompt != b.Prompt)
	add("when", a.Kind != b.Kind || a.Schedule != b.Schedule || a.Folder != b.Folder)
	add("time_zone", a.TZ != b.TZ)
	add("daily_cap", a.DailyCap != b.DailyCap)
	add("enabled", a.Enabled != b.Enabled)
	add("reads", a.Reads != b.Reads)
	add("writes", a.Writes != b.Writes)
	add("widget", a.Widget != b.Widget)
	add("estimated_tokens", a.EstTokens != b.EstTokens)
	add("agent", a.AgentID != b.AgentID)
	add("model", a.Model != b.Model)
	return out
}

// inputOf is the input that builds t again, with its folder as a path of the
// owner's home.
func (h *Triggers) inputOf(t triggers.Trigger) TriggerInput {
	return TriggerInput{Name: t.Name, Prompt: t.Prompt, Kind: t.Kind, Schedule: t.Schedule, TZ: t.TZ, Folder: h.RelFolder(t),
		Quiet: t.Quiet, DailyCap: t.DailyCap, AgentID: t.AgentID, Model: t.Model, SourceChatID: t.SourceChatID, Enabled: t.Enabled,
		MinGap: t.MinGap, Reads: t.Reads, Writes: t.Writes, Widget: t.Widget, EstTokens: t.EstTokens}
}

// TriggerPatch is what a model asks to change in a trigger. A nil field, and
// When with an empty Kind, stay as they are.
type TriggerPatch struct {
	Name, Prompt *string
	// Kind is set when the schedule or the folder changes: Schedule for a
	// schedule trigger, Folder (a path of the user's home) for a file trigger.
	Kind             triggers.Kind
	Schedule, Folder string
	TZ               *string
	DailyCap         *int
	Enabled          *bool
	Reads, Writes    *string
	Widget           *string
	EstTokens        *int
}

func (p TriggerPatch) apply(in TriggerInput, oldKind triggers.Kind) TriggerInput {
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&in.Name, p.Name)
	set(&in.Prompt, p.Prompt)
	set(&in.TZ, p.TZ)
	set(&in.Reads, p.Reads)
	set(&in.Writes, p.Writes)
	set(&in.Widget, p.Widget)
	switch p.Kind {
	case triggers.KindSchedule:
		in.Kind, in.Schedule, in.Folder = triggers.KindSchedule, p.Schedule, ""
	case triggers.KindFile:
		in.Kind, in.Folder, in.Schedule = triggers.KindFile, p.Folder, ""
		if oldKind != triggers.KindFile && p.TZ == nil {
			in.TZ = "" // a schedule's zone does not carry over to a folder
		}
	}
	if p.DailyCap != nil {
		in.DailyCap = *p.DailyCap
	}
	if p.EstTokens != nil {
		in.EstTokens = *p.EstTokens
	}
	if p.Enabled != nil {
		in.Enabled = *p.Enabled
	}
	return in
}

// ProposeChange asks for a change of the trigger target of the user, from the
// chat chatID. A change of the definition waits for the person's answer and
// applied is false. A change that only switches the trigger off or on applies
// at once, applied is true, and the person can undo it. It is checked as a
// proposal is: a schedule that changes runs at most once an hour.
func (h *Triggers) ProposeChange(ctx context.Context, user, chatID string, target triggers.Trigger, p TriggerPatch) (rec triggers.Trigger, applied bool, err error) {
	owner := principal(user)
	if target.Owner != owner || target.Proposed {
		return triggers.Trigger{}, false, triggers.ErrNotFound
	}
	in := p.apply(h.inputOf(target), target.Kind)
	after, err := h.build(ctx, owner, in)
	if err != nil {
		return triggers.Trigger{}, false, err
	}
	if p.Kind != "" || p.TZ != nil {
		if err := h.checkCadence(after); err != nil {
			return triggers.Trigger{}, false, err
		}
	}
	fields := changedFields(target, after)
	if len(fields) == 0 {
		return triggers.Trigger{}, false, bad("nothing changes: the trigger already has these values")
	}
	if len(fields) == 1 && fields[0] == "enabled" {
		rec, err := h.switchByChange(ctx, user, chatID, target.ID, after.Enabled)
		return rec, err == nil, err
	}
	rec = after
	rec.SourceChatID = target.SourceChatID
	rec.Change = triggers.Change{Op: triggers.ChangeUpdate, Target: target.ID, Stamp: stamp(target), On: after.Enabled, Chat: chatID}
	rec, err = h.keepChange(ctx, owner, rec)
	return rec, false, err
}

// ProposeDelete asks for the deletion of the trigger target of the user, from
// the chat chatID. Nothing is deleted until the person answers.
func (h *Triggers) ProposeDelete(ctx context.Context, user, chatID string, target triggers.Trigger) (triggers.Trigger, error) {
	owner := principal(user)
	if target.Owner != owner || target.Proposed {
		return triggers.Trigger{}, triggers.ErrNotFound
	}
	rec := withDefinition(triggers.Trigger{Owner: owner, SourceChatID: target.SourceChatID}, target)
	rec.Change = triggers.Change{Op: triggers.ChangeDelete, Target: target.ID, Stamp: stamp(target), On: target.Enabled, Chat: chatID}
	return h.keepChange(ctx, owner, rec)
}

// keepChange stores a waiting change record. The same change of the same
// trigger asked again returns the one that waits.
func (h *Triggers) keepChange(ctx context.Context, owner string, rec triggers.Trigger) (triggers.Trigger, error) {
	rec.Enabled = false // the record never runs; Change.On says what the change wants
	h.wmu.Lock()
	defer h.wmu.Unlock()
	mine, err := h.o.Store.ListTriggers(ctx, owner)
	if err != nil {
		return triggers.Trigger{}, err
	}
	h.pruneChanges(ctx, mine)
	pending := 0
	for _, m := range mine {
		if !m.IsChange() || m.Outcome != "" {
			continue
		}
		if m.Change.Op == rec.Change.Op && m.Change.Target == rec.Change.Target && m.Change.Stamp == rec.Change.Stamp &&
			m.Change.On == rec.Change.On && stamp(m) == stamp(rec) {
			return m, nil
		}
		pending++
	}
	if pending >= maxPendingChanges {
		return triggers.Trigger{}, ErrTooManyChanges
	}
	rec.ID, rec.Owner, rec.Proposed, rec.Enabled = newID(), owner, true, false
	rec.Outcome, rec.OutcomeAt, rec.Touched, rec.Since = "", time.Time{}, false, time.Time{}
	if err := h.o.Store.PutTrigger(ctx, rec); err != nil {
		return triggers.Trigger{}, err
	}
	return rec, nil
}

// pruneChanges removes the answered change records of mine that are older than
// changeRetention. The caller holds wmu.
func (h *Triggers) pruneChanges(ctx context.Context, mine []triggers.Trigger) {
	cut := h.o.Clock().Add(-changeRetention)
	for _, m := range mine {
		if m.IsChange() && m.Outcome != "" && m.OutcomeAt.Before(cut) {
			if err := h.o.Store.DeleteTrigger(ctx, m.ID); err != nil {
				h.log.Warn("studio: could not remove an old trigger change", "change", m.ID, "error", err)
			}
		}
	}
}

// switchByChange switches the trigger off or on at once and keeps an applied
// change record of it, so the person can undo it.
func (h *Triggers) switchByChange(ctx context.Context, user, chatID, id string, on bool) (triggers.Trigger, error) {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	live, err := h.owned(ctx, user, id)
	if err != nil {
		return triggers.Trigger{}, err
	}
	t := live
	t.Enabled = on
	t.Touched = live.Touched || live.Outcome != ""
	t = triggers.Arm(&live, t, h.o.Clock())
	if err := h.o.Store.PutTrigger(ctx, t); err != nil {
		return triggers.Trigger{}, err
	}
	op := triggers.ChangePause
	if on {
		op = triggers.ChangeResume
	}
	rec := withDefinition(triggers.Trigger{ID: newID(), Owner: live.Owner, SourceChatID: live.SourceChatID, Proposed: true}, live)
	rec.Change = triggers.Change{Op: op, Target: id, Stamp: stamp(t), On: live.Enabled, Chat: chatID}
	rec.Outcome, rec.OutcomeAt = triggers.OutcomeAccepted, h.o.Clock().UTC()
	if err := h.o.Store.PutTrigger(ctx, rec); err != nil {
		// The switch stays: it is what the model asked for, and the Triggers
		// page shows it; only the receipt is missing.
		h.log.Warn("studio: could not keep the receipt of a trigger switch", "trigger", id, "error", err)
	}
	h.Notify()
	return rec, nil
}

// ownedChange returns a change record of the user.
func (h *Triggers) ownedChange(ctx context.Context, user, id string) (triggers.Trigger, error) {
	t, err := h.o.Store.GetTrigger(ctx, id)
	if err != nil || t.Owner != principal(user) || !t.IsChange() {
		return triggers.Trigger{}, triggers.ErrNotFound
	}
	return t, nil
}

func changeStateOf(t triggers.Trigger) ChangeState {
	switch t.Outcome {
	case triggers.OutcomeAccepted:
		return ChangeApplied
	case triggers.OutcomeDeclined:
		return ChangeDismissed
	case triggers.OutcomeUndone:
		return ChangeUndone
	}
	return ChangePending
}

// asTrigger is the definition a change record holds as the trigger it is for:
// its ID, and on or off as the record says.
func asTrigger(rec triggers.Trigger) triggers.Trigger {
	t := withDefinition(triggers.Trigger{ID: rec.Change.Target, Owner: rec.Owner, SourceChatID: rec.SourceChatID}, rec)
	t.Enabled = rec.Change.On
	return t
}

// ChangeStatus reads a change of the user and what became of it. A change of
// another user is triggers.ErrNotFound.
func (h *Triggers) ChangeStatus(ctx context.Context, user, id string) (ChangeView, error) {
	rec, err := h.ownedChange(ctx, user, id)
	if err != nil {
		return ChangeView{}, err
	}
	v := ChangeView{State: changeStateOf(rec), Op: rec.Change.Op, ID: rec.ID, Target: rec.Change.Target, At: rec.OutcomeAt}
	live, lerr := h.owned(ctx, user, rec.Change.Target)
	if lerr != nil && !errors.Is(lerr, triggers.ErrNotFound) {
		return ChangeView{}, lerr
	}
	has := lerr == nil
	kept := asTrigger(rec)
	isDelete := rec.Change.Op == triggers.ChangeDelete
	switch {
	case v.State == ChangePending:
		v.Stale = !has || stamp(live) != rec.Change.Stamp
		if has {
			v.Before = live
		} else {
			v.Before = kept
		}
		if !isDelete {
			v.After, v.HasAfter = kept, true
		}
	case isDelete:
		v.Before = kept
		if has { // undone, or never applied
			v.Before = live
		}
	case v.State == ChangeApplied:
		v.Before = kept
		if has {
			v.After, v.HasAfter = live, true
		}
	default: // dismissed or undone: the record holds the side that is not live
		if has {
			v.Before = live
		}
		v.After, v.HasAfter = kept, true
		if v.State == ChangeUndone {
			v.Before, v.After = kept, live
			v.HasAfter = has
		}
	}
	if v.HasAfter {
		v.Fields = changedFields(v.Before, v.After)
	}
	if v.State == ChangeApplied {
		v.UndoBlocked = h.changeUndoBlock(ctx, rec, live, has)
		v.CanUndo = v.UndoBlocked == ""
	}
	if has {
		if runs, err := h.o.Store.ListRuns(ctx, live.ID, 1); err == nil && len(runs) == 1 {
			v.LastRun = &runs[0]
		}
	}
	return v, nil
}

// changeUndoBlock says why an applied change cannot be undone, or "".
func (h *Triggers) changeUndoBlock(ctx context.Context, rec, live triggers.Trigger, has bool) string {
	if h.o.Clock().Sub(rec.OutcomeAt) > ChangeUndoWindow {
		return UndoExpired
	}
	if rec.Change.Op == triggers.ChangeDelete {
		if has {
			return UndoEdited // a trigger with its ID is back
		}
		if mine, err := h.o.Store.ListTriggers(ctx, rec.Owner); err != nil || countLive(mine) >= maxTriggersPerUser {
			return UndoFull
		}
		return ""
	}
	if !has {
		return UndoRemoved
	}
	if stamp(live) != rec.Change.Stamp {
		return UndoEdited
	}
	return ""
}

var changeUndoText = map[string]string{
	UndoExpired: "The time to undo it is over; change the trigger on the Triggers page.",
	UndoEdited:  "The trigger was changed since; change it on the Triggers page.",
	UndoRemoved: "The trigger was removed since.",
}

// ApplyChange applies a waiting change of the user: the edit, or the delete.
// It is refused with ErrChangeStale when the trigger changed or was removed
// since the change was asked for, and with ErrProposalAnswered when the change
// was answered already. The answer is decided in the store (claimChange), so
// two frontends that apply the same change at once apply it once.
func (h *Triggers) ApplyChange(ctx context.Context, user, id string) (triggers.Trigger, error) {
	h.wmu.Lock()
	rec, err := h.ownedChange(ctx, user, id)
	if err != nil {
		h.wmu.Unlock()
		return triggers.Trigger{}, err
	}
	if rec.Outcome != "" {
		h.wmu.Unlock()
		return triggers.Trigger{}, ErrProposalAnswered
	}
	live, err := h.owned(ctx, user, rec.Change.Target)
	if errors.Is(err, triggers.ErrNotFound) {
		h.wmu.Unlock()
		return triggers.Trigger{}, fmt.Errorf("%w: it was removed", ErrChangeStale)
	}
	if err != nil {
		h.wmu.Unlock()
		return triggers.Trigger{}, err
	}
	if rec.Change.Op != triggers.ChangeDelete && stamp(live) != rec.Change.Stamp {
		h.wmu.Unlock()
		return triggers.Trigger{}, fmt.Errorf("%w: ask for the change again", ErrChangeStale)
	}
	at := h.o.Clock().UTC()
	if err := h.claimChange(ctx, rec, "", triggers.OutcomeAccepted, at); err != nil {
		h.wmu.Unlock()
		return triggers.Trigger{}, err
	}
	if rec.Change.Op == triggers.ChangeDelete {
		// The copy that Undo restores is the trigger as it is now. It is kept
		// before the delete, so a failure cannot lose the trigger.
		kept := withDefinition(rec, live)
		kept.SourceChatID = live.SourceChatID
		kept.Change.On, kept.Change.Stamp = live.Enabled, stamp(live)
		kept.Outcome, kept.OutcomeAt = triggers.OutcomeAccepted, at
		err := h.o.Store.PutTrigger(ctx, kept)
		if err != nil {
			h.releaseChange(ctx, rec, triggers.OutcomeAccepted)
			h.wmu.Unlock()
			return triggers.Trigger{}, err
		}
		h.wmu.Unlock()
		if err := h.DeleteTrigger(ctx, user, live.ID); err != nil {
			h.wmu.Lock()
			if perr := h.o.Store.PutTrigger(ctx, rec); perr != nil {
				h.log.Warn("studio: could not put a trigger change back", "change", rec.ID, "error", perr)
			}
			h.wmu.Unlock()
			return triggers.Trigger{}, err
		}
		return kept, nil
	}
	defer h.wmu.Unlock()
	in := h.inputOf(rec)
	in.Enabled, in.SourceChatID = rec.Change.On, live.SourceChatID
	t, err := h.build(ctx, live.Owner, in)
	if err != nil {
		h.releaseChange(ctx, rec, triggers.OutcomeAccepted)
		return triggers.Trigger{}, err
	}
	t.ID = live.ID
	t.Outcome, t.OutcomeAt, t.Touched = live.Outcome, live.OutcomeAt, live.Touched || live.Outcome != ""
	t = triggers.Arm(&live, t, h.o.Clock())
	if err := h.o.Store.PutTrigger(ctx, t); err != nil {
		h.releaseChange(ctx, rec, triggers.OutcomeAccepted)
		return triggers.Trigger{}, err
	}
	kept := withDefinition(rec, live)
	kept.Change.On, kept.Change.Stamp = live.Enabled, stamp(t)
	kept.Outcome, kept.OutcomeAt = triggers.OutcomeAccepted, at
	if err := h.o.Store.PutTrigger(ctx, kept); err != nil {
		// The edit stays; only Undo is lost.
		h.log.Warn("studio: could not keep the copy that undoes a trigger change", "change", rec.ID, "error", err)
	}
	h.Notify()
	h.linkWidget(ctx, t)
	return t, nil
}

// claimChange moves the outcome of a change record from from to to in the
// store, or answers ErrProposalAnswered when another request (on this or
// another frontend) answered it first. The mutex of the host covers one
// process; this covers the cluster.
func (h *Triggers) claimChange(ctx context.Context, rec triggers.Trigger, from, to string, at time.Time) error {
	ok, err := h.o.Store.SetOutcome(ctx, rec.ID, from, to, at)
	if err != nil {
		return err
	}
	if !ok {
		return ErrProposalAnswered
	}
	return nil
}

// releaseChange gives a claimed change record its outcome back after the
// change itself failed, so it can be answered again.
func (h *Triggers) releaseChange(ctx context.Context, rec triggers.Trigger, claimed string) {
	if _, err := h.o.Store.SetOutcome(ctx, rec.ID, claimed, rec.Outcome, rec.OutcomeAt); err != nil {
		h.log.Warn("studio: could not give a trigger change its answer back", "change", rec.ID, "error", err)
	}
}

// DismissChange declines a waiting change: the trigger stays as it is.
// Declining one twice is not an error.
func (h *Triggers) DismissChange(ctx context.Context, user, id string) error {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	rec, err := h.ownedChange(ctx, user, id)
	if err != nil {
		return err
	}
	switch rec.Outcome {
	case triggers.OutcomeDeclined:
		return nil
	case "":
		err := h.claimChange(ctx, rec, "", triggers.OutcomeDeclined, h.o.Clock().UTC())
		if !errors.Is(err, ErrProposalAnswered) {
			return err
		}
		// Answered meanwhile, maybe declined by another frontend.
		if again, gerr := h.ownedChange(ctx, user, id); gerr == nil && again.Outcome == triggers.OutcomeDeclined {
			return nil
		}
	}
	return ErrProposalAnswered
}

// UndoChange takes back an applied change within ChangeUndoWindow: an edit,
// a pause or a resume puts the trigger back as it was, and a delete restores
// the trigger with its ID (its run history is not restored). It is refused with
// ErrCannotUndo once the time is over or the trigger changed since, and with
// ErrProposalAnswered when it was undone already, on any frontend.
func (h *Triggers) UndoChange(ctx context.Context, user, id string) (triggers.Trigger, error) {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	rec, err := h.ownedChange(ctx, user, id)
	if err != nil {
		return triggers.Trigger{}, err
	}
	if rec.Outcome != triggers.OutcomeAccepted {
		return triggers.Trigger{}, ErrProposalAnswered
	}
	live, lerr := h.owned(ctx, user, rec.Change.Target)
	if lerr != nil && !errors.Is(lerr, triggers.ErrNotFound) {
		return triggers.Trigger{}, lerr
	}
	if block := h.changeUndoBlock(ctx, rec, live, lerr == nil); block != "" {
		if block == UndoFull {
			return triggers.Trigger{}, ErrTooManyTriggers
		}
		return triggers.Trigger{}, fmt.Errorf("%w: %s", ErrCannotUndo, changeUndoText[block])
	}
	var t triggers.Trigger
	if rec.Change.Op == triggers.ChangeDelete {
		t = triggers.Arm(nil, asTrigger(rec), h.o.Clock())
	} else {
		t = withDefinition(live, rec)
		t.Enabled = rec.Change.On
		t = triggers.Arm(&live, t, h.o.Clock())
	}
	if err := t.Validate(); err != nil {
		return triggers.Trigger{}, bad("%v", err)
	}
	at := h.o.Clock().UTC()
	if err := h.claimChange(ctx, rec, triggers.OutcomeAccepted, triggers.OutcomeUndone, at); err != nil {
		return triggers.Trigger{}, err
	}
	if err := h.o.Store.PutTrigger(ctx, t); err != nil {
		h.releaseChange(ctx, rec, triggers.OutcomeUndone)
		return triggers.Trigger{}, err
	}
	if rec.Change.Op != triggers.ChangeDelete {
		rec = withDefinition(rec, live)
		rec.Change.On = live.Enabled
	}
	rec.Outcome, rec.OutcomeAt = triggers.OutcomeUndone, at
	if err := h.o.Store.PutTrigger(ctx, rec); err != nil {
		h.log.Warn("studio: could not mark a trigger change undone", "change", rec.ID, "error", err)
	}
	h.Notify()
	return t, nil
}

// changeNote is how the model hears of a change it asked for in a chat.
func changeNote(t triggers.Trigger, name string) string {
	switch t.Change.Op + "/" + t.Outcome {
	case triggers.ChangeUpdate + "/" + triggers.OutcomeAccepted:
		return fmt.Sprintf("the change to %q applied by the user", name)
	case triggers.ChangeUpdate + "/" + triggers.OutcomeDeclined:
		return fmt.Sprintf("the change to %q dismissed by the user, so the trigger is as it was", name)
	case triggers.ChangeDelete + "/" + triggers.OutcomeAccepted:
		return fmt.Sprintf("%q deleted by the user", name)
	case triggers.ChangeDelete + "/" + triggers.OutcomeDeclined:
		return fmt.Sprintf("%q kept by the user, not deleted", name)
	case triggers.ChangePause + "/" + triggers.OutcomeAccepted:
		return fmt.Sprintf("%q paused", name)
	case triggers.ChangeResume + "/" + triggers.OutcomeAccepted:
		return fmt.Sprintf("%q resumed", name)
	}
	if t.Outcome == triggers.OutcomeUndone {
		return fmt.Sprintf("the %s of %q undone by the user", t.Change.Op, name)
	}
	return ""
}
