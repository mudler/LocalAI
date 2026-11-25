package triggers

import (
	"context"
	"errors"
	"time"
)

// ReminderPrefix starts the DueKey of the run of a reminder at its due time:
// "reminder@" and the due time (RFC 3339, UTC). A reminder that moves gets a
// new due time and so a new key; one that fired stays fired for that time.
const ReminderPrefix = "reminder@"

// Reasons of the runs of reminders.
const (
	// ReasonLate is the reason of a reminder run that starts after its time
	// because no pass ran then, while its thing has not begun yet.
	ReasonLate = "started late after the server was down"
	// ReasonMissed is the reason of a reminder that did not fire because its
	// thing began while no pass ran.
	ReasonMissed = "missed while the server was down"
)

// lateGrace is how long after its start a reminder run counts as on time: a
// pass runs every few seconds, so a run later than this was held up by a
// stop of the host.
const lateGrace = 2 * time.Minute

// reminderPass claims the run of each reminder whose start has come. A
// reminder of a watch that is off waits. trs are the enabled triggers.
func (r *Runner) reminderPass(ctx context.Context, trs []Trigger, now time.Time) error {
	on := make(map[string]Trigger, len(trs))
	for _, t := range trs {
		on[t.ID] = t
	}
	var errs []error
	for _, t := range trs {
		if !t.IsReminder() {
			continue
		}
		parent, ok := on[t.Reminder.ParentID]
		if !ok || !parent.IsWatch() {
			continue
		}
		if err := r.reminderOne(ctx, t, parent, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *Runner) reminderOne(ctx context.Context, t, parent Trigger, now time.Time) error {
	loc, err := parent.Location()
	if err != nil {
		loc = time.UTC
	}
	tm := PlanReminder(*t.Reminder, parent.Watch, loc, now)
	if tm.None || tm.Skipped || tm.Due.IsZero() {
		return nil
	}
	key := ReminderPrefix + tm.Due.Format(time.RFC3339)
	if _, err := r.o.Store.RunByKey(ctx, t.ID, key); err == nil {
		return nil // this due time fired, or was missed, already
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	planned := tm.Due.Add(-t.Reminder.Lead)
	run := Run{ID: newID(), TriggerID: t.ID, DueKey: key, Due: tm.Due}
	switch {
	case tm.Skip:
		// The thing began. When the reminder was armed in time, a pass
		// should have fired it: the host was down. Otherwise the thing had
		// begun before the reminder existed and there is nothing to say.
		if !planned.After(t.Since) && !t.Since.IsZero() {
			return nil
		}
		run.Status, run.Reason, run.Ended = StatusSkipped, ReasonMissed, now.UTC()
		claimed, err := r.o.Store.Claim(ctx, run)
		if err != nil || !claimed {
			return err
		}
		r.prune(ctx, t.ID, now)
		if r.o.Missed != nil {
			r.o.Missed(t, run)
		}
		return nil
	case now.Before(tm.Start):
		return nil
	}
	if planned.After(t.Since) && now.Sub(planned) > lateGrace {
		run.Reason = ReasonLate
	}
	if err := r.admit(ctx, t, &run, now, false, false); err != nil {
		return err
	}
	return r.claim(ctx, run)
}

// parentOn says whether the watch of reminder t is on.
func (r *Runner) parentOn(ctx context.Context, t Trigger) bool {
	p, err := r.o.Store.GetTrigger(ctx, t.Reminder.ParentID)
	return err == nil && p.Enabled
}
