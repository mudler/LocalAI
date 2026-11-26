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

// spentAfter is how long a reminder that does not repeat is kept once its
// time and the start of its thing have passed: a reply to it ("snooze")
// still finds it, and a late change of its thing is still reconciled. Then
// the pass removes it, so the reminders of a watch do not pile up.
const spentAfter = 24 * time.Hour

// reminderPass claims the run of each reminder whose start has come, and
// removes the reminders that are spent. A reminder of a watch that is off
// waits. trs are the enabled triggers.
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
		if !ok {
			// A reminder left behind by a watch that is gone is removed; one
			// of a paused watch waits.
			if _, err := r.o.Store.GetTrigger(ctx, t.Reminder.ParentID); errors.Is(err, ErrNotFound) {
				if err := r.o.Store.DeleteTrigger(ctx, t.ID); err != nil {
					errs = append(errs, err)
				}
			}
			continue
		}
		if !parent.IsWatch() {
			continue
		}
		if err := r.reminderOne(ctx, t, parent, now); err != nil {
			errs = append(errs, err)
		}
		if err := r.advance(ctx, t, parent, now); err != nil {
			errs = append(errs, err)
		}
		if r.spent(ctx, t, parent, now) {
			if err := r.dropSpent(ctx, t); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// spent says whether the reminder t is used up: it does not repeat, its due
// time and the start of its thing passed more than spentAfter ago, no run of
// it is queued or running, and its last run ended more than spentAfter ago
// (a reminder missed during a long outage is kept as long as one that fired).
func (r *Runner) spent(ctx context.Context, t, parent Trigger, now time.Time) bool {
	rem := *t.Reminder
	if len(rem.Following) > 0 || rem.Repeats {
		return false
	}
	end := PlanFor(parent, rem, now).Due
	if limit, ok := startOf(rem, parent.LocationOrUTC()); ok && limit.After(end) {
		end = limit
	}
	if end.IsZero() || now.Sub(end) <= spentAfter {
		return false
	}
	runs, err := r.o.Store.ListRuns(ctx, t.ID, 3)
	if err != nil {
		return false
	}
	for _, run := range runs {
		if !run.Status.Final() || now.Sub(run.Ended) <= spentAfter {
			return false
		}
	}
	return true
}

func (r *Runner) reminderOne(ctx context.Context, t, parent Trigger, now time.Time) error {
	tm := PlanFor(parent, *t.Reminder, now)
	if tm.None || tm.Skipped || tm.Due.IsZero() {
		return nil
	}
	key := ReminderPrefix + tm.Due.Format(time.RFC3339)
	if _, err := r.o.Store.RunByKey(ctx, t.ID, key); err == nil {
		return nil // this due time fired, or was missed, already
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	// armed is when the reminder last had a chance to fire on time: when it
	// was set or moved, or when its watch was switched back on. A due time
	// before it passed while nothing could fire it on purpose (the watch was
	// paused), so it is neither late nor missed.
	armed := t.Since
	if parent.Since.After(armed) {
		armed = parent.Since
	}
	planned := tm.Due.Add(-t.Reminder.Lead)
	run := Run{ID: newID(), TriggerID: t.ID, DueKey: key, Due: tm.Due}
	switch {
	case tm.Skip:
		// The thing began. When the reminder was armed in time, a pass
		// should have fired it: the host was down. Otherwise the thing had
		// begun before the reminder existed and there is nothing to say.
		if !planned.After(armed) && !armed.IsZero() {
			return nil
		}
		return r.miss(ctx, t, run, now)
	case now.Before(tm.Start):
		return nil
	}
	if planned.After(armed) && now.Sub(planned) > lateGrace {
		// A reminder due at or after the start of its thing has nothing left
		// to prepare for once the thing has begun: after an outage it is
		// missed, not sent late.
		if limit, ok := startOf(*t.Reminder, parent.LocationOrUTC()); ok && !tm.Due.Before(limit) && !now.Before(limit) {
			return r.miss(ctx, t, run, now)
		}
		run.Reason = ReasonLate
	}
	if err := r.admit(ctx, t, &run, now, false, false); err != nil {
		return err
	}
	return r.claim(ctx, run)
}

// dropSpent removes the spent reminder t unless it changed since the pass
// read it: a reconcile on another frontend may have just moved its item ahead.
func (r *Runner) dropSpent(ctx context.Context, t Trigger) error {
	cur, err := r.o.Store.GetTrigger(ctx, t.ID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !cur.IsReminder() || cur.Reminder.Rev != t.Reminder.Rev {
		return nil // changed: the next pass looks again
	}
	return r.o.Store.DeleteTrigger(ctx, t.ID)
}

// miss records the run of a reminder that did not fire because its thing
// began while no pass ran, and tells the host once.
func (r *Runner) miss(ctx context.Context, t Trigger, run Run, now time.Time) error {
	run.Status, run.Reason, run.Ended = StatusSkipped, ReasonMissed, now.UTC()
	claimed, err := r.o.Store.Claim(ctx, run)
	if err != nil || !claimed {
		return err
	}
	r.prune(ctx, t.ID, now)
	if r.o.Missed != nil {
		r.o.Missed(ctx, t, run)
	}
	return nil
}

// parentState says whether the watch of reminder t is on, and whether it is
// gone: a reminder whose watch was deleted has nothing to wait for.
func (r *Runner) parentState(ctx context.Context, t Trigger) (on, gone bool, err error) {
	p, err := r.o.Store.GetTrigger(ctx, t.Reminder.ParentID)
	if errors.Is(err, ErrNotFound) {
		return false, true, nil
	}
	if err != nil {
		return false, false, err
	}
	return p.Enabled, false, nil
}

// advance moves the reminder of a thing that repeats to its next occurrence
// once the time of the current one has passed: after the thing began, after
// the reminder's own time, and after its run ended. It needs no model and
// does not depend on the run: a failed or missed run does not stop the
// series. The next occurrence is the first of Reminder.Following that is
// still ahead, so a server that was down for days does not send the ones it
// missed. A reminder set to a fixed time (At) is left alone.
func (r *Runner) advance(ctx context.Context, t, parent Trigger, now time.Time) error {
	rem := t.Reminder
	if (len(rem.Following) == 0 && !rem.Repeats) || rem.Anchor.IsZero() || !rem.At.IsZero() || now.Before(rem.Anchor) {
		return nil
	}
	if tm := PlanFor(parent, *rem, now); !tm.Due.IsZero() && now.Before(tm.Due) {
		return nil // a reminder after the start of the thing has not come yet
	}
	if runs, err := r.o.Store.ListRuns(ctx, t.ID, 1); err != nil {
		return err
	} else if len(runs) == 1 && !runs[0].Status.Final() {
		return nil // the run of this occurrence is still going
	}
	cur, err := r.o.Store.GetTrigger(ctx, t.ID)
	if err != nil || !cur.IsReminder() || !cur.Reminder.Anchor.Equal(rem.Anchor) {
		return nil // removed or changed since the pass read it
	}
	next := *cur.Reminder
	i := 0
	for i < len(next.Following) && !next.Following[i].After(now) {
		i++
	}
	if i == len(next.Following) {
		// No known occurrence is ahead: say it once and stop looking.
		next.Following, next.Repeats = nil, false
	} else {
		next.Anchor, next.Following = next.Following[i], next.Following[i+1:]
		next.SnoozedTo, next.Skipped = time.Time{}, time.Time{}
		next.Repeats = true
		cur.Since = now.UTC()
	}
	moved := !next.Anchor.Equal(rem.Anchor)
	cur.Reminder = &next
	if err := r.o.Store.PutTrigger(ctx, cur); err != nil {
		if errors.Is(err, ErrConflict) {
			return nil // another writer won; the next pass looks again
		}
		return err
	}
	if r.o.Advanced != nil {
		r.o.Advanced(ctx, cur, moved, len(next.Following) == 0)
	}
	return nil
}
