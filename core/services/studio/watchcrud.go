package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// WatchSource is an add-on whose events a watch can take: its ID, its title
// and the event types it declares.
type WatchSource struct {
	ID     string
	Title  contract.Localized
	Events []contract.EventDeclaration
}

// WatchSources lists the sources a watch can take. It is empty when no
// installed add-on declares events, or when the server has no add-ons.
func (h *Triggers) WatchSources(ctx context.Context) ([]WatchSource, error) {
	if h.o.Sources == nil {
		return nil, nil
	}
	return h.o.Sources(ctx)
}

// checkWatchSource refuses a watch whose source declares no events, or that
// names a type its source does not declare. A server that cannot list the
// sources (no add-ons) refuses every watch: nothing could feed it.
func (h *Triggers) checkWatchSource(ctx context.Context, t triggers.Trigger) error {
	if t.Kind != triggers.KindEvent {
		return bad("a reminder rule belongs to a watch")
	}
	srcs, err := h.WatchSources(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(srcs, func(s WatchSource) bool { return s.ID == t.Source })
	if i < 0 {
		return bad("no installed add-on called %q reports changes", t.Source)
	}
	for _, ty := range t.Watch.Types {
		if !slices.ContainsFunc(srcs[i].Events, func(d contract.EventDeclaration) bool { return d.Type == ty }) {
			return bad("%s does not report %q", t.Source, ty)
		}
	}
	return nil
}

// ReminderView is a reminder as the watch page shows it.
type ReminderView struct {
	ID, Key, Title, Link, Task string
	// Due is when the owner hears (zero: none, because the item began or the
	// rule sets none); Start is when the run starts.
	Due, Start, Anchor time.Time
	Offset, Lead       time.Duration
	At, SnoozedTo      time.Time
	AllDay, Skipped    bool
	Adjusted           string
	// Snoozable says the reminder is due soon enough that snoozing it makes
	// sense: within the next two hours.
	Snoozable bool
}

// snoozeWindow is how close a reminder must be for the page to offer Snooze.
const snoozeWindow = 2 * time.Hour

func reminderView(w, r triggers.Trigger, now time.Time) ReminderView {
	rem := *r.Reminder
	v := ReminderView{ID: r.ID, Key: rem.Key, Title: rem.Title, Link: rem.Link, Task: rem.Task, Anchor: rem.Anchor, Offset: rem.Offset,
		Lead: rem.Lead, At: rem.At, SnoozedTo: rem.SnoozedTo, AllDay: rem.AllDay}
	tm := triggers.PlanFor(w, rem, now)
	if !tm.None && !tm.Skip {
		v.Due, v.Start, v.Adjusted, v.Skipped = tm.Due, tm.Start, tm.Adjusted, tm.Skipped
		v.Snoozable = !tm.Skipped && tm.Due.Sub(now) <= snoozeWindow
	}
	return v
}

// Reminders lists the reminders of a watch of the user, soonest first; a
// reminder with no time left comes last.
func (h *Triggers) Reminders(ctx context.Context, user, watchID string) ([]ReminderView, error) {
	w, err := h.owned(ctx, user, watchID)
	if err != nil {
		return nil, err
	}
	if !w.IsWatch() {
		return nil, bad("this trigger is not a watch")
	}
	list, err := h.o.Store.Reminders(ctx, w.ID)
	if err != nil {
		return nil, err
	}
	now := h.o.Clock()
	out := make([]ReminderView, 0, len(list))
	for _, r := range list {
		out = append(out, reminderView(w, r, now))
	}
	slices.SortStableFunc(out, func(a, b ReminderView) int {
		switch {
		case a.Due.IsZero() != b.Due.IsZero():
			if a.Due.IsZero() {
				return 1
			}
			return -1
		}
		return a.Due.Compare(b.Due)
	})
	return out, nil
}

// ReminderPatch is what the owner changes on one reminder from the watch
// page. Each field that is set applies; the page undoes a change by sending
// the old values back.
type ReminderPatch struct {
	// SnoozeFor moves the reminder to now plus this much.
	SnoozeFor time.Duration
	// SnoozedTo, when set, replaces the snooze: an empty string clears it.
	SnoozedTo *string
	// Skip skips the coming due time; false takes the skip back.
	Skip *bool
	// At and Offset re-time the reminder as set_reminder does: At is a time
	// in the words of the tool, Offset a time relative to the item's.
	At     string
	Offset *time.Duration
	Task   *string
	// Source, when set, is the add-on whose page asks (sdk.reminders.skip):
	// a reminder of a watch on another source is refused as missing, so a
	// page changes only the reminders of its own items.
	Source string
}

// PatchReminder applies a change of the owner to a reminder of their watch.
func (h *Triggers) PatchReminder(ctx context.Context, user, watchID, reminderID string, p ReminderPatch) (ReminderView, error) {
	w, err := h.owned(ctx, user, watchID)
	if err != nil {
		return ReminderView{}, err
	}
	if p.Source != "" && (!w.IsWatch() || w.Source != p.Source) {
		return ReminderView{}, triggers.ErrNotFound
	}
	r, err := h.o.Store.GetTrigger(ctx, reminderID)
	if err != nil || !r.IsReminder() || r.Reminder.ParentID != w.ID {
		return ReminderView{}, triggers.ErrNotFound
	}
	if p.At != "" || p.Offset != nil {
		_, err := h.SetReminder(ctx, w, ReminderInput{Key: r.Reminder.Key, At: p.At, offset: p.Offset})
		if why, ok := reminderRefusal(err); ok {
			return ReminderView{}, bad("%s", why)
		}
		if err != nil {
			return ReminderView{}, err
		}
	}
	now := h.o.Clock()
	r, line, err := h.writePatch(ctx, w, reminderID, p, now)
	if err != nil {
		return ReminderView{}, err
	}
	if line.Code != "" {
		h.record(ctx, reminderAction{w: w, now: now, line: line})
	}
	return reminderView(w, r, now), nil
}

// writePatch applies the owner's change p to the reminder under the write
// lock, and gives the reminder as it is now and the line of the watch's
// history, with no Code when nothing changed. It reads the reminder again
// under the lock, so a change of the thing reconciled meanwhile is kept and
// this change applies on top. The lock covers this process; the version
// check of the store covers another frontend, and a refused write reads the
// reminder again.
func (h *Triggers) writePatch(ctx context.Context, w triggers.Trigger, reminderID string, p ReminderPatch, now time.Time) (triggers.Trigger, watchText, error) {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	var r triggers.Trigger
	var line watchText
	err := triggers.RetryOnConflict(func() error {
		var err error
		if r, err = h.o.Store.GetTrigger(ctx, reminderID); err != nil || !r.IsReminder() {
			return triggers.ErrNotFound
		}
		if line, err = applyReminderPatch(&r, w, p, w.LocationOrUTC(), now); err != nil || line.Code == "" {
			return err
		}
		return h.o.Store.PutTrigger(ctx, r)
	})
	return r, line, err
}

// applyReminderPatch applies the owner's change p to the reminder r and gives
// the line of the watch's history, "" when nothing changes.
func applyReminderPatch(r *triggers.Trigger, w triggers.Trigger, p ReminderPatch, loc *time.Location, now time.Time) (watchText, error) {
	rem := *r.Reminder
	var line watchText
	switch {
	case p.SnoozeFor > 0:
		if p.SnoozeFor > 24*time.Hour {
			return watchText{}, bad("snooze for at most a day")
		}
		rem.SnoozedTo = now.Add(p.SnoozeFor).UTC()
		line = watchSay(w, "snoozed", rem.Title, rem.SnoozedTo, fmt.Sprintf("Snoozed the reminder for %q to %s.", rem.Title, whenText(w, rem.SnoozedTo, now)))
	case p.SnoozedTo != nil:
		if *p.SnoozedTo == "" {
			rem.SnoozedTo = time.Time{}
			line = watchSay(w, "snoozeUndone", rem.Title, time.Time{}, fmt.Sprintf("Took back the snooze of the reminder for %q.", rem.Title))
		} else {
			at, err := parseWhen(*p.SnoozedTo, loc)
			if err != nil {
				return watchText{}, bad("%v", err)
			}
			rem.SnoozedTo = at
			line = watchSay(w, "snoozed", rem.Title, at, fmt.Sprintf("Snoozed the reminder for %q to %s.", rem.Title, whenText(w, at, now)))
		}
	}
	if p.Skip != nil {
		if *p.Skip {
			tm := triggers.PlanFor(w, rem, now)
			rem.Skipped = tm.Due
			line = watchSay(w, "skipped", rem.Title, time.Time{}, fmt.Sprintf("Skipped the reminder for %q.", rem.Title))
		} else {
			rem.Skipped = time.Time{}
			line = watchSay(w, "skipUndone", rem.Title, time.Time{}, fmt.Sprintf("Took back the skip of the reminder for %q.", rem.Title))
		}
	}
	if p.Task != nil {
		task := strings.TrimSpace(*p.Task)
		if task == "" {
			task = "remind me"
		}
		rem.Task, r.Prompt = task, task
		line = watchSay(w, "taskChanged", rem.Title, time.Time{}, fmt.Sprintf("Changed the task of the reminder for %q.", rem.Title))
	}
	if line.Code == "" {
		return watchText{}, nil
	}
	r.Reminder = &rem
	if err := r.Validate(); err != nil {
		return watchText{}, bad("%v", err)
	}
	return line, nil
}

// DeleteReminder cancels a reminder of the user's watch.
func (h *Triggers) DeleteReminder(ctx context.Context, user, watchID, reminderID string) error {
	w, err := h.owned(ctx, user, watchID)
	if err != nil {
		return err
	}
	r, err := h.o.Store.GetTrigger(ctx, reminderID)
	if err != nil || !r.IsReminder() || r.Reminder.ParentID != w.ID {
		return triggers.ErrNotFound
	}
	return h.CancelReminder(ctx, w, r.Reminder.Key)
}

// holdReminders lists in the Inbox each reminder of a watch that was paused:
// they wait with it and fire again when it is back on.
func (h *Triggers) holdReminders(ctx context.Context, w triggers.Trigger) {
	list, err := h.o.Store.Reminders(ctx, w.ID)
	if err != nil {
		return
	}
	now := h.o.Clock()
	for _, r := range list {
		if dueText(w, r, now) == "" {
			continue // nothing left to hold
		}
		h.watchNote(ctx, w, watchSay(w, "onHold", r.Reminder.Title, time.Time{}, fmt.Sprintf("The reminder for %q is on hold while this watch is paused.", r.Reminder.Title)))
	}
}

// dropReminders removes the reminders of a watch that is being deleted, and
// lists each in the Inbox. It fails when a reminder stays, so the watch is
// not deleted before its reminders: a delete that is tried again finishes.
func (h *Triggers) dropReminders(ctx context.Context, w triggers.Trigger) error {
	list, err := h.o.Store.Reminders(ctx, w.ID)
	if err != nil {
		return err
	}
	run := h.currentRunner()
	var errs []error
	for _, r := range list {
		if run != nil {
			_ = run.Cancel(ctx, r.ID)
		}
		if err := h.o.Store.DeleteTrigger(ctx, r.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		h.watchNote(ctx, w, watchSay(w, "dropped", r.Reminder.Title, time.Time{}, fmt.Sprintf("The reminder for %q was removed with its watch.", r.Reminder.Title)))
	}
	return errors.Join(errs...)
}

// SourceReminder is a reminder of one of the owner's watches, for the page of
// the add-on whose item it is: the id of the item in the add-on, the watch and
// when the reminder fires.
type SourceReminder struct {
	ItemID, WatchID string
	View            ReminderView
}

// SourceReminders lists the reminders of the user's watches for the items of
// one add-on, so its page can mark them (the bell of the calendar).
func (h *Triggers) SourceReminders(ctx context.Context, user, source string) ([]SourceReminder, error) {
	if strings.TrimSpace(source) == "" {
		return nil, bad("give the add-on whose reminders to list")
	}
	all, err := h.o.Store.ListTriggers(ctx, principal(user))
	if err != nil {
		return nil, err
	}
	watches := map[string]triggers.Trigger{}
	for _, t := range all {
		if t.IsWatch() && t.Source == source {
			watches[t.ID] = t
		}
	}
	now := h.o.Clock()
	var out []SourceReminder
	for _, r := range all {
		if !r.IsReminder() {
			continue
		}
		w, ok := watches[r.Reminder.ParentID]
		item, found := strings.CutPrefix(r.Reminder.Key, source+":")
		if !ok || !found {
			continue
		}
		out = append(out, SourceReminder{ItemID: item, WatchID: w.ID, View: reminderView(w, r, now)})
	}
	return out, nil
}
