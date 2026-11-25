package triggers

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"
)

// The reasons a due time differs from the watch's rule. They are codes: the
// host turns them into the owner's words.
const (
	AdjustedQuiet  = "quiet"
	AdjustedAllDay = "all_day"
)

// allDayHour is the hour of the morning reminder of a thing that lasts whole
// days, in the thing's zone.
const allDayHour = 8

// Timing is when a reminder fires.
type Timing struct {
	// Due is when the owner should hear: the reminder time.
	Due time.Time
	// Start is when the run starts: Due minus the lead, or now when that is
	// already past (the thing was added or moved inside the offset and lead).
	Start time.Time
	// Adjusted is AdjustedQuiet or AdjustedAllDay when the rule moved the due
	// time; empty otherwise.
	Adjusted string
	// Now says that Start was moved up to now: the run starts at once.
	Now bool
	// Skip says that the thing has already started: no reminder fires.
	Skip bool
	// None says that the rule sets no reminder (an all-day thing under the
	// all-day rule none).
	None bool
	// Skipped says that the owner skipped this due time.
	Skipped bool
}

// PlanReminder computes when the reminder r fires at now. rule is the watch's
// rule (nil: no rule), owner the zone of the quiet hours. Due times are
// instants: the add-on gives the thing's time with its zone, so a change of
// daylight saving time is already in the anchor, and the offset is a plain
// duration from it. Only the all-day rule and the quiet hours read a clock,
// each in its own zone: the all-day morning in the thing's zone, the quiet
// hours in the owner's.
func PlanReminder(r Reminder, rule *WatchRule, owner *time.Location, now time.Time) Timing {
	if owner == nil {
		owner = time.UTC
	}
	var tm Timing
	explicit := false
	switch {
	case !r.SnoozedTo.IsZero():
		tm.Due, explicit = r.SnoozedTo, true
	case !r.At.IsZero():
		tm.Due, explicit = r.At, true
	case r.AllDay && !r.Anchor.IsZero():
		if rule != nil && rule.AllDayOrDefault() == AllDayNone {
			return Timing{None: true}
		}
		y, m, d, loc := allDayOf(r, owner)
		tm.Due = time.Date(y, m, d, allDayHour, 0, 0, 0, loc)
		tm.Adjusted = AdjustedAllDay
	case r.Anchor.IsZero():
		// A thing with no time left (an event with no occurrence ahead) and
		// no time of its own: nothing to remind of.
		return Timing{None: true}
	default:
		tm.Due = r.Anchor.Add(r.Offset)
	}
	if !explicit && rule != nil {
		if moved, ok := outOfQuiet(tm.Due, *rule, owner); ok {
			tm.Due, tm.Adjusted = moved, AdjustedQuiet
		}
	}
	tm.Due = tm.Due.UTC()
	// A reminder due before the thing begins is missed once the thing has
	// begun. One due at or after its start (at the time, after it, or
	// snoozed past it) still fires at its time.
	if limit, ok := startOf(r, owner); ok && !now.Before(limit) && tm.Due.Before(limit) {
		return Timing{Due: tm.Due, Skip: true}
	}
	if !r.Skipped.IsZero() && r.Skipped.Equal(tm.Due) {
		tm.Skipped = true
	}
	tm.Start = tm.Due.Add(-r.Lead)
	if tm.Start.Before(now) {
		tm.Start, tm.Now = now.UTC(), true
	}
	return tm
}

// PlanFor is PlanReminder for a reminder of the watch w: the watch's rule,
// with the quiet hours in the watch's zone.
func PlanFor(w Trigger, r Reminder, now time.Time) Timing {
	return PlanReminder(r, w.Watch, w.LocationOrUTC(), now)
}

// startOf is the time from which the thing of r counts as begun: its time,
// or the end of its day for a thing that lasts whole days, so the morning
// reminder still fires during the day. false for a thing with no time.
func startOf(r Reminder, owner *time.Location) (time.Time, bool) {
	if r.Anchor.IsZero() {
		return time.Time{}, false
	}
	if r.AllDay {
		// The next midnight on the clock, not 24 hours: a day with a change
		// of daylight saving time is 23 or 25 hours long.
		y, m, d, loc := allDayOf(r, owner)
		return time.Date(y, m, d+1, 0, 0, 0, 0, loc), true
	}
	return r.Anchor, true
}

// allDayOf gives the day of a thing that lasts whole days and the zone of its
// clock. A thing with a zone has both in that zone. A thing with no zone has
// the date of its anchor as written (UTC, the midnight an add-on gives for a
// date) and the owner's clock: reading the date in the owner's zone would put
// it on the day before west of UTC.
func allDayOf(r Reminder, owner *time.Location) (int, time.Month, int, *time.Location) {
	if r.Zone != "" {
		if loc, err := time.LoadLocation(r.Zone); err == nil {
			y, m, d := r.Anchor.In(loc).Date()
			return y, m, d, loc
		}
	}
	y, m, d := r.Anchor.UTC().Date()
	return y, m, d, owner
}

// outOfQuiet moves t out of the quiet hours of rule: a time inside them moves
// to the start of the quiet hours that contain it, which is the evening before
// for a time early in the morning. It returns false for a time outside them or
// a rule with none.
func outOfQuiet(t time.Time, rule WatchRule, loc *time.Location) (time.Time, bool) {
	if rule.QuietFrom == "" || rule.QuietTo == "" {
		return t, false
	}
	from, err1 := time.Parse("15:04", rule.QuietFrom)
	to, err2 := time.Parse("15:04", rule.QuietTo)
	if err1 != nil || err2 != nil || rule.QuietFrom == rule.QuietTo {
		return t, false
	}
	local := t.In(loc)
	at := func(day time.Time, c time.Time) time.Time {
		return time.Date(day.Year(), day.Month(), day.Day(), c.Hour(), c.Minute(), 0, 0, loc)
	}
	// The quiet window that starts today and the one that started yesterday
	// are the only ones that can hold local.
	for _, day := range []time.Time{local.AddDate(0, 0, -1), local} {
		start := at(day, from)
		end := at(day, to)
		if !end.After(start) {
			end = at(day.AddDate(0, 0, 1), to)
		}
		if !local.Before(start) && local.Before(end) {
			return start, true
		}
	}
	return t, false
}

// The actions of the reconciler.
const (
	// ActionModel: no reminder exists for the thing, so the watch's model run
	// decides.
	ActionModel = "model"
	// ActionRetimed: the thing moved and the reminder moved with it.
	ActionRetimed = "retimed"
	// ActionCancelled: the thing is gone and so is its reminder.
	ActionCancelled = "cancelled"
	// ActionWoken: the thing changed in more than its time; the reminder
	// keeps its place and an early run is due.
	ActionWoken = "woken"
	// ActionUnchanged: nothing the reminder depends on changed.
	ActionUnchanged = "unchanged"
	// ActionNone: an event about a thing with no reminder that needs no run,
	// such as the removal of a thing the watch never reminded of.
	ActionNone = "none"
)

// Outcome is what Reconcile did.
type Outcome struct {
	Action string
	// Before is the reminder as it was, After as it is now (zero after a
	// cancel). Both are zero when there was no reminder.
	Before, After Trigger
	// Moved says that the time of the thing changed.
	Moved bool
}

// Reconcile applies an event to the reminder that the watch holds for its
// thing, with no model: it re-times the reminder from the new time of the
// thing, cancels it when the thing is gone, and says when a change of the
// thing beyond its time should wake it early. An event about a thing that has
// no reminder goes to the watch's model run (ActionModel), except a removal,
// which needs nothing (ActionNone). An event with no Thing is for the model.
func Reconcile(ctx context.Context, st Store, watch Trigger, ev Event, now time.Time) (Outcome, error) {
	// Another frontend, or the owner, may write the reminder between the read
	// and the write: the store refuses the stale write and the event is
	// applied again on top of the new version.
	var out Outcome
	err := RetryOnConflict(func() error {
		var err error
		out, err = reconcileOnce(ctx, st, watch, ev, now)
		return err
	})
	return out, err
}

// maxConflictRetries is how many times a writer of a reminder reads it again
// after ErrConflict before it gives up.
const maxConflictRetries = 3

// RetryOnConflict calls write, which reads a reminder, changes it and writes
// it, again while the store refuses the write with ErrConflict: another
// writer changed the reminder since the read. It gives up after a few tries
// and returns the last error.
func RetryOnConflict(write func() error) error {
	for attempt := 0; ; attempt++ {
		err := write()
		if !errors.Is(err, ErrConflict) || attempt == maxConflictRetries {
			return err
		}
	}
}

func reconcileOnce(ctx context.Context, st Store, watch Trigger, ev Event, now time.Time) (Outcome, error) {
	th := ev.Thing
	if th == nil {
		return Outcome{Action: ActionModel}, nil
	}
	cur, err := st.ReminderByKey(ctx, watch.ID, th.Key)
	if errors.Is(err, ErrNotFound) {
		if th.Lifecycle == LifecycleDeleted {
			return Outcome{Action: ActionNone}, nil
		}
		return Outcome{Action: ActionModel}, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{Before: cur}
	if th.Lifecycle == LifecycleDeleted {
		if err := st.DeleteTrigger(ctx, cur.ID); err != nil {
			return Outcome{}, err
		}
		out.Action = ActionCancelled
		return out, nil
	}
	next := cur
	rem := *cur.Reminder
	if th.Title != "" {
		rem.Title, next.Name = CutText(th.Title, MaxThingTitle), CutText(th.Title, MaxNameLen)
	}
	if th.Link != "" {
		rem.Link = th.Link
	}
	out.Moved = !th.Anchor.Equal(rem.Anchor) || th.AllDay != rem.AllDay || th.Zone != rem.Zone
	changed := rem.Stamp != "" && th.Stamp != "" && th.Stamp != rem.Stamp
	rem.Anchor, rem.Zone, rem.AllDay = th.Anchor.UTC(), th.Zone, th.AllDay
	rem.Following, rem.Repeats = th.Following, len(th.Following) > 0
	if th.Stamp != "" {
		rem.Stamp = th.Stamp
	}
	if out.Moved {
		// A time the owner chose was for the old time of the thing.
		rem.SnoozedTo = time.Time{}
		next.Since = now.UTC()
	}
	next.Reminder = &rem
	if err := st.PutTrigger(ctx, next); err != nil {
		return Outcome{}, err
	}
	out.After = next
	switch {
	case changed && rem.WakeOnChange:
		out.Action = ActionWoken
	case out.Moved:
		out.Action = ActionRetimed
	default:
		out.Action = ActionUnchanged
	}
	return out, nil
}

// CutText keeps at most n characters of s.
func CutText(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// Guarded says whether an event must not wake the watch w: the event was
// caused by a run of w itself or of one of its reminders. It may still wake
// other watches. parentOf gives the watch of a reminder by its ID, "" for a
// trigger that is not a reminder.
func Guarded(w Trigger, a Actor, parentOf func(id string) string) bool {
	if a.Kind != ActorAgent || a.TriggerID == "" {
		return false
	}
	if a.TriggerID == w.ID {
		return true
	}
	return parentOf != nil && parentOf(a.TriggerID) == w.ID
}
