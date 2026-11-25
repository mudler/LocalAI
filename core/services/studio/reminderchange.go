package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// The person changes the reminders of a watch by asking in a chat in Studio.
// The model there has the same set_reminder and cancel_reminder tools as the
// run of the watch, but in a chat they only propose: the change is a change
// record, as for a trigger (see triggerchanges.go), whose Kind is reminder and
// whose Reminder is the reminder as the change leaves it. The chat shows the
// change card, and nothing changes until the person approves it. Such a record
// is never the watch's reminder for its item (Trigger.IsReminder), so it
// neither fires nor blocks the one reminder per item.
//
// Change.Op is ChangeCreate for a new reminder, ChangeUpdate for a new time
// or task and ChangeDelete for a cancel; Change.Target is the reminder's ID
// (empty for a new one until it is applied). Once applied, the record keeps
// the reminder as it was before, so Undo can put it back.

// isReminderChange says whether rec is a change record of a reminder.
func isReminderChange(rec triggers.Trigger) bool {
	return rec.IsChange() && rec.Kind == triggers.KindReminder && rec.Reminder != nil
}

// reminderStamp is a fingerprint of a stored reminder: anything that changes
// it, the reconciler's re-time included, makes a waiting change stale.
func reminderStamp(t triggers.Trigger) string {
	if t.Reminder == nil {
		return ""
	}
	r := *t.Reminder
	r.Rev = 0 // the store's version, not part of what the reminder does
	raw, _ := json.Marshal(r)
	sum := sha256.Sum256([]byte(t.Prompt + "|" + string(raw)))
	return hex.EncodeToString(sum[:8])
}

// ReminderChange is a change of a reminder as the chat card shows it.
type ReminderChange struct {
	WatchID, WatchName, Title string
	// WatchZone is the time zone of the watch, in which the card writes the
	// times, as the watch page does.
	WatchZone string
	// Before and After are the reminder before and after the change; nil
	// when there is none (a new reminder has no Before, a cancel no After).
	Before, After *ReminderView
}

// ProposeReminder asks, from the chat chatID, to set the reminder of the
// user's watch w for one item as in says. Nothing changes until the person
// applies the change on its card.
func (h *Triggers) ProposeReminder(ctx context.Context, user, chatID string, w triggers.Trigger, in ReminderInput) (triggers.Trigger, error) {
	if w.Owner != principal(user) || !w.IsWatch() {
		return triggers.Trigger{}, triggers.ErrNotFound
	}
	in.ChatID = "" // a chat of the person holds no event of the watch
	p, err := h.planReminder(ctx, w, in, h.o.Clock())
	if err != nil {
		return triggers.Trigger{}, err
	}
	rec := p.next
	rec.ID, rec.Since = "", time.Time{}
	rec.Change = triggers.Change{Op: triggers.ChangeCreate, Chat: chatID}
	if !p.isNew {
		if reminderStamp(p.cur) == reminderStamp(p.next) {
			return triggers.Trigger{}, reminderErr("nothing changes: the reminder already has these values")
		}
		rec.Change.Op, rec.Change.Target, rec.Change.Stamp = triggers.ChangeUpdate, p.cur.ID, reminderStamp(p.cur)
	}
	return h.keepChange(ctx, w.Owner, rec, nil)
}

// ProposeCancelReminder asks, from the chat chatID, to cancel the reminder of
// the user's watch w for an item.
func (h *Triggers) ProposeCancelReminder(ctx context.Context, user, chatID string, w triggers.Trigger, key string) (triggers.Trigger, error) {
	if w.Owner != principal(user) || !w.IsWatch() {
		return triggers.Trigger{}, triggers.ErrNotFound
	}
	cur, err := h.o.Store.ReminderByKey(ctx, w.ID, key)
	if err != nil {
		return triggers.Trigger{}, err
	}
	rec := cur
	rec.ID, rec.Since = "", time.Time{}
	rec.Change = triggers.Change{Op: triggers.ChangeDelete, Target: cur.ID, Stamp: reminderStamp(cur), Chat: chatID}
	return h.keepChange(ctx, w.Owner, rec, nil)
}

// reminderWatch is the watch of a reminder change of the user.
func (h *Triggers) reminderWatch(ctx context.Context, user string, rec triggers.Trigger) (triggers.Trigger, error) {
	w, err := h.owned(ctx, user, rec.Reminder.ParentID)
	if err != nil || !w.IsWatch() {
		return triggers.Trigger{}, triggers.ErrNotFound
	}
	return w, nil
}

// applyReminderChange is ApplyChange for a change of a reminder.
func (h *Triggers) applyReminderChange(ctx context.Context, user, id string) (triggers.Trigger, error) {
	t, act, err := h.applyReminderChangeLocked(ctx, user, id)
	if err != nil {
		return triggers.Trigger{}, err
	}
	h.record(ctx, act)
	return t, nil
}

// applyReminderChangeLocked applies the change under the write lock, and
// gives the action to record once the lock is let go.
func (h *Triggers) applyReminderChangeLocked(ctx context.Context, user, id string) (triggers.Trigger, reminderAction, error) {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	fail := func(err error) (triggers.Trigger, reminderAction, error) {
		return triggers.Trigger{}, reminderAction{}, err
	}
	rec, err := h.ownedChange(ctx, user, id)
	if err != nil {
		return fail(err)
	}
	if rec.Outcome != "" {
		return fail(ErrProposalAnswered)
	}
	w, err := h.reminderWatch(ctx, user, rec)
	if err != nil {
		return fail(fmt.Errorf("%w: the watch was removed", ErrChangeStale))
	}
	var live triggers.Trigger
	if rec.Change.Op == triggers.ChangeCreate {
		if _, err := h.o.Store.ReminderByKey(ctx, w.ID, rec.Reminder.Key); err == nil {
			return fail(fmt.Errorf("%w: the item has a reminder since; ask for the change again", ErrChangeStale))
		}
	} else {
		live, err = h.o.Store.GetTrigger(ctx, rec.Change.Target)
		if err != nil || !live.IsReminder() || live.Reminder.ParentID != w.ID {
			return fail(fmt.Errorf("%w: the reminder was removed", ErrChangeStale))
		}
		if reminderStamp(live) != rec.Change.Stamp {
			return fail(fmt.Errorf("%w: ask for the change again", ErrChangeStale))
		}
	}
	now := h.o.Clock()
	var next triggers.Trigger
	var line watchText
	if rec.Change.Op == triggers.ChangeDelete {
		line = reminderCancelledLine(w, live.Reminder.Title)
	} else {
		base := live
		if rec.Change.Op == triggers.ChangeCreate {
			base = triggers.Trigger{ID: newID(), Owner: w.Owner}
		}
		rem := *rec.Reminder
		rem.Rev = 0
		if base.Reminder != nil {
			rem.Rev = base.Reminder.Rev // the version checked against the stamp above
		}
		// The person may answer later than the change was asked for: the
		// time is worked out again now.
		tm := triggers.PlanFor(w, rem, now)
		if tm.Skip || tm.None {
			return fail(bad("the item has already started, so the reminder cannot be set"))
		}
		rem.Adjusted = tm.Adjusted
		next = reminderTrigger(base, w, rem)
		next.Since = now.UTC()
		if err := next.Validate(); err != nil {
			return fail(bad("%v", err))
		}
		line = reminderSetLine(w, rec.Change.Op == triggers.ChangeCreate, rem.Title, tm.Due, now)
	}
	at := now.UTC()
	if err := h.claimChange(ctx, rec, "", triggers.OutcomeAccepted, at); err != nil {
		return fail(err)
	}
	if rec.Change.Op == triggers.ChangeDelete {
		if err := h.o.Store.DeleteTrigger(ctx, live.ID); err != nil {
			h.releaseChange(ctx, rec, triggers.OutcomeAccepted)
			return fail(err)
		}
		if run := h.currentRunner(); run != nil {
			_ = run.Cancel(ctx, live.ID)
		}
	} else if err := h.o.Store.PutTrigger(ctx, next); err != nil {
		h.releaseChange(ctx, rec, triggers.OutcomeAccepted)
		if errors.Is(err, triggers.ErrDuplicate) || errors.Is(err, triggers.ErrConflict) {
			return fail(fmt.Errorf("%w: ask for the change again", ErrChangeStale))
		}
		return fail(err)
	}
	// The record keeps the reminder as it was, for Undo, and the stamp of the
	// reminder the change left, to tell a later edit.
	kept := rec
	if rec.Change.Op == triggers.ChangeCreate {
		kept.Change.Target = next.ID
	} else {
		kept.Reminder, kept.Prompt, kept.Name = live.Reminder, live.Prompt, live.Name
	}
	kept.Change.Stamp = ""
	if rec.Change.Op != triggers.ChangeDelete {
		if stored, err := h.o.Store.GetTrigger(ctx, next.ID); err == nil {
			kept.Change.Stamp = reminderStamp(stored)
		}
	}
	kept.Outcome, kept.OutcomeAt = triggers.OutcomeAccepted, at
	if err := h.o.Store.PutTrigger(ctx, kept); err != nil {
		h.log.Warn("studio: could not keep the copy that undoes a reminder change", "change", rec.ID, "error", err)
	}
	act := reminderAction{w: w, now: now, line: line}
	if rec.Change.Op == triggers.ChangeDelete {
		return live, act, nil
	}
	return next, act, nil
}

// reminderUndoBlock says why an applied change of a reminder cannot be
// undone, or "".
func (h *Triggers) reminderUndoBlock(ctx context.Context, rec triggers.Trigger) string {
	if h.o.Clock().Sub(rec.OutcomeAt) > ChangeUndoWindow {
		return UndoExpired
	}
	if rec.Change.Op == triggers.ChangeDelete {
		if _, err := h.o.Store.ReminderByKey(ctx, rec.Reminder.ParentID, rec.Reminder.Key); err == nil {
			return UndoEdited // the item has a reminder again
		}
		if _, err := h.o.Store.GetTrigger(ctx, rec.Reminder.ParentID); err != nil {
			return UndoRemoved // the watch is gone
		}
		return ""
	}
	live, err := h.o.Store.GetTrigger(ctx, rec.Change.Target)
	if err != nil || !live.IsReminder() {
		return UndoRemoved
	}
	if reminderStamp(live) != rec.Change.Stamp {
		return UndoEdited
	}
	return ""
}

// undoReminderChange is UndoChange for a change of a reminder: a new
// reminder is cancelled, a changed one gets its time and task back and a
// cancelled one is set again.
func (h *Triggers) undoReminderChange(ctx context.Context, user, id string) (triggers.Trigger, error) {
	t, act, err := h.undoReminderChangeLocked(ctx, user, id)
	if err != nil {
		return triggers.Trigger{}, err
	}
	h.record(ctx, act)
	return t, nil
}

// undoReminderChangeLocked undoes the change under the write lock, and gives
// the action to record once the lock is let go.
func (h *Triggers) undoReminderChangeLocked(ctx context.Context, user, id string) (triggers.Trigger, reminderAction, error) {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	fail := func(err error) (triggers.Trigger, reminderAction, error) {
		return triggers.Trigger{}, reminderAction{}, err
	}
	rec, err := h.ownedChange(ctx, user, id)
	if err != nil {
		return fail(err)
	}
	if rec.Outcome != triggers.OutcomeAccepted {
		return fail(ErrProposalAnswered)
	}
	if block := h.reminderUndoBlock(ctx, rec); block != "" {
		return fail(fmt.Errorf("%w: %s", ErrCannotUndo, reminderUndoText[block]))
	}
	w, err := h.reminderWatch(ctx, user, rec)
	if err != nil {
		return fail(fmt.Errorf("%w: %s", ErrCannotUndo, reminderUndoText[UndoRemoved]))
	}
	now := h.o.Clock()
	at := now.UTC()
	if err := h.claimChange(ctx, rec, triggers.OutcomeAccepted, triggers.OutcomeUndone, at); err != nil {
		return fail(err)
	}
	var t triggers.Trigger
	var line watchText
	switch rec.Change.Op {
	case triggers.ChangeCreate:
		// The delete is not versioned: an edit that another frontend makes
		// between the undo check and the delete is dropped with the
		// reminder, which is what undoing its creation asks for.
		live, gerr := h.o.Store.GetTrigger(ctx, rec.Change.Target)
		if gerr == nil {
			err = h.o.Store.DeleteTrigger(ctx, live.ID)
		}
		if err == nil {
			if run := h.currentRunner(); run != nil {
				_ = run.Cancel(ctx, rec.Change.Target)
			}
		}
		t, line = live, reminderCancelledLine(w, rec.Reminder.Title)
	default:
		base := triggers.Trigger{ID: rec.Change.Target, Owner: w.Owner}
		rem := *rec.Reminder
		rem.Rev = 0 // a cancelled reminder is created again
		if rec.Change.Op == triggers.ChangeUpdate {
			live, gerr := h.o.Store.GetTrigger(ctx, rec.Change.Target)
			if gerr != nil {
				err = gerr
				break
			}
			base, rem.Rev = live, live.Reminder.Rev
		}
		t = reminderTrigger(base, w, rem)
		t.Since = at
		err = h.o.Store.PutTrigger(ctx, t)
		due := triggers.PlanFor(w, rem, now).Due
		line = watchSay(w, "restored", rem.Title, due, fmt.Sprintf("Put back the reminder for %q: %s.", rem.Title, whenText(w, due, now)))
	}
	if err != nil {
		h.releaseChange(ctx, rec, triggers.OutcomeUndone)
		if errors.Is(err, triggers.ErrDuplicate) || errors.Is(err, triggers.ErrConflict) {
			return fail(fmt.Errorf("%w: %s", ErrCannotUndo, reminderUndoText[UndoEdited]))
		}
		return fail(err)
	}
	rec.Outcome, rec.OutcomeAt = triggers.OutcomeUndone, at
	if err := h.o.Store.PutTrigger(ctx, rec); err != nil {
		h.log.Warn("studio: could not mark a reminder change undone", "change", rec.ID, "error", err)
	}
	return t, reminderAction{w: w, now: now, line: line}, nil
}

var reminderUndoText = map[string]string{
	UndoExpired: "The time to undo it is over; change the reminder on the watch's page.",
	UndoEdited:  "The reminder was changed since; change it on the watch's page.",
	UndoRemoved: "The reminder or its watch was removed since.",
}

// reminderChangeStatus is ChangeStatus for a change of a reminder.
func (h *Triggers) reminderChangeStatus(ctx context.Context, user string, rec triggers.Trigger) (ChangeView, error) {
	v := ChangeView{State: changeStateOf(rec), Op: rec.Change.Op, ID: rec.ID, Target: rec.Change.Target, At: rec.OutcomeAt}
	rc := &ReminderChange{Title: rec.Reminder.Title, WatchID: rec.Reminder.ParentID}
	v.Reminder = rc
	w, werr := h.reminderWatch(ctx, user, rec)
	if werr == nil {
		rc.WatchName, rc.WatchZone = w.Name, w.TZ
		v.Before = w
	} else {
		w = triggers.Trigger{ID: rec.Reminder.ParentID, Owner: rec.Owner, TZ: rec.TZ}
	}
	now := h.o.Clock()
	view := func(r triggers.Trigger) *ReminderView {
		rv := reminderView(w, r, now)
		return &rv
	}
	var live triggers.Trigger
	has := false
	if rec.Change.Target != "" {
		if t, err := h.o.Store.GetTrigger(ctx, rec.Change.Target); err == nil && t.IsReminder() {
			live, has = t, true
		}
	}
	kept := rec
	kept.ID = rec.Change.Target
	switch v.State {
	case ChangePending:
		switch rec.Change.Op {
		case triggers.ChangeCreate:
			_, err := h.o.Store.ReminderByKey(ctx, rec.Reminder.ParentID, rec.Reminder.Key)
			v.Stale = err == nil || werr != nil
		default:
			v.Stale = !has || reminderStamp(live) != rec.Change.Stamp || werr != nil
		}
		if has {
			rc.Before = view(live)
		}
		if rec.Change.Op != triggers.ChangeDelete {
			rc.After = view(kept)
		}
	case ChangeApplied:
		if rec.Change.Op != triggers.ChangeCreate {
			rc.Before = view(kept)
		}
		if has {
			rc.After = view(live)
		}
		v.UndoBlocked = h.reminderUndoBlock(ctx, rec)
		v.CanUndo = v.UndoBlocked == ""
	default: // dismissed or undone: the reminder as it is, and what the change asked for
		if has {
			rc.Before = view(live)
		}
		if rec.Change.Op != triggers.ChangeDelete {
			rc.After = view(kept)
		}
	}
	return v, nil
}

// reminderChangeNote is how the model hears of a reminder change it asked
// for in a chat.
func reminderChangeNote(t triggers.Trigger) string {
	title := cutRunes(t.Reminder.Title, 60)
	what := "the change to the reminder for " + fmt.Sprintf("%q", title)
	switch t.Change.Op {
	case triggers.ChangeCreate:
		what = fmt.Sprintf("the new reminder for %q", title)
	case triggers.ChangeDelete:
		what = fmt.Sprintf("cancelling the reminder for %q", title)
	}
	switch t.Outcome {
	case triggers.OutcomeAccepted:
		return what + " approved by the user and applied"
	case triggers.OutcomeDeclined:
		return what + " declined by the user, so the reminders are as they were"
	case triggers.OutcomeUndone:
		return what + " undone by the user"
	}
	return ""
}
