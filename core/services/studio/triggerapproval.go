package studio

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// What a timer that a chat sets is bound by.
const (
	// MinOneOff is the shortest time from now to the run of a one-off trigger.
	MinOneOff = time.Minute
	// MaxOneOffAhead is the farthest time a one-off trigger may be set to.
	MaxOneOffAhead = 365 * 24 * time.Hour
	// cancelWindow is how long after a trigger was made without asking a bare
	// "cancel" in its thread removes it. The code works for as long as the
	// trigger lives.
	cancelWindow = 30 * time.Minute
)

// ---- Times that a chat gives ----

var clockRE = regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?\s*(am|pm)?$`)

// ParseClock reads a time of day: "18:00", "6pm", "6:30 pm".
func ParseClock(text string) (hour, minute int, err error) {
	m := clockRE.FindStringSubmatch(strings.ToLower(strings.TrimSpace(text)))
	if m == nil {
		return 0, 0, errors.New(`the time of day must look like "18:00" or "6pm"`)
	}
	hour, _ = strconv.Atoi(m[1])
	if m[2] != "" {
		minute, _ = strconv.Atoi(m[2])
	}
	switch m[3] {
	case "am", "pm":
		if hour < 1 || hour > 12 {
			return 0, 0, errors.New("with am or pm the hour must be 1 to 12")
		}
		hour %= 12
		if m[3] == "pm" {
			hour += 12
		}
	}
	if hour > 23 || minute > 59 {
		return 0, 0, errors.New("the time of day is not a time")
	}
	return hour, minute, nil
}

// timerSchedule turns "in 10 minutes" (in) or "at 18:00" (at) into a one-off
// schedule text and the zone it is read in. A delay needs no zone (UTC is used
// when none is given); a time of day needs one, since the host does not know
// where the person is. A time of day that is less than a minute away, or past,
// means the next day.
func timerSchedule(now time.Time, in, at, tz string) (schedule, zone string, err error) {
	if (in == "") == (at == "") {
		return "", "", errors.New(`when needs exactly one of "in" (a delay such as "10 minutes") or "at" (a time of day such as "18:00")`)
	}
	if at != "" && tz == "" {
		return "", "", errors.New(`"at" needs time_zone: ask the person where they are, or use "in" with a delay`)
	}
	if tz == "" {
		tz = "UTC"
	}
	loc, lerr := time.LoadLocation(tz)
	if lerr != nil {
		return "", "", fmt.Errorf("time zone %q is not known", tz)
	}
	var target time.Time
	if in != "" {
		d, derr := ParseDelay(in)
		if derr != nil {
			return "", "", derr
		}
		if d < MinOneOff {
			return "", "", errors.New("a timer from a chat is at least one minute away")
		}
		if d > MaxOneOffAhead {
			return "", "", fmt.Errorf("a timer can be at most %d days ahead", int(MaxOneOffAhead/(24*time.Hour)))
		}
		target = now.Add(d)
		if rounded := target.Truncate(time.Minute); rounded.Before(target) {
			target = rounded.Add(time.Minute)
		}
	} else {
		h, m, cerr := ParseClock(at)
		if cerr != nil {
			return "", "", cerr
		}
		y, mo, d := now.In(loc).Date()
		target = time.Date(y, mo, d, h, m, 0, 0, loc)
		if target.Sub(now) < MinOneOff {
			target = time.Date(y, mo, d+1, h, m, 0, 0, loc)
		}
	}
	return "once " + target.In(loc).Format("2006-01-02 15:04"), tz, nil
}

// ---- Approval ----

// ApprovalOf gives the approval of a binding. A binding that cannot be read
// asks: the host never creates a trigger without asking on a doubt.
func (c *Connectors) ApprovalOf(ctx context.Context, bindingID string) connectors.Approval {
	b, err := c.o.Store.GetBinding(ctx, bindingID)
	if err != nil {
		return connectors.Approval{}
	}
	return b.Approval
}

// autoLive are the triggers of the owner that the binding made without asking
// and that are on.
func autoLive(all []triggers.Trigger, binding string) []triggers.Trigger {
	var out []triggers.Trigger
	for _, t := range all {
		if t.Route.Binding == binding && t.Route.Auto && !t.Proposed && t.Enabled {
			out = append(out, t)
		}
	}
	return out
}

// runsPerDay is how many runs a day a trigger made without asking uses of the
// budget of its binding: a recurring trigger as many as its schedule and daily
// limit allow, a one-off trigger one when it is due in the next day.
func (h *Triggers) runsPerDay(t triggers.Trigger, now time.Time) int {
	if at, ok := t.OnceAt(); ok {
		if at.Sub(now) <= 24*time.Hour {
			return 1
		}
		return 0
	}
	return h.MaxRunsPerDay(t)
}

// autoVerdict says why a proposal may not be made without asking under the
// approval of its binding; empty means it may. The checks are the owner's
// limits and, for the read-only mode, what the proposal says about itself.
func (h *Triggers) autoVerdict(ctx context.Context, ap connectors.Approval, t triggers.Trigger) string {
	lim := ap.Limits()
	now := h.o.Clock()
	if ap.ModeOrDefault() == connectors.ApprovalAutoReadonly {
		switch {
		case !t.Route.ReadOnly:
			return "it does not say that its runs only read and report"
		case t.Writes != "" || t.Widget != "":
			return "its runs write a file"
		}
	}
	if len(t.Acts) > 0 {
		// A run that may act is never set up without a yes.
		return "its runs ask for permission to act on their own"
	}
	if t.Route.TargetOrDefault() == triggers.TargetChannel {
		return "it would write to another channel"
	}
	if t.Kind != triggers.KindSchedule {
		return "a trigger on files always asks"
	}
	all, err := h.o.Store.ListTriggers(ctx, t.Owner)
	if err != nil {
		return "I could not count the timers of this connection"
	}
	live := autoLive(all, t.Route.Binding)
	if len(live) >= lim.MaxPending {
		return fmt.Sprintf("this connection already has %d timers and triggers (the most is %d)", len(live), lim.MaxPending)
	}
	days := time.Duration(lim.MaxLifetimeDays) * 24 * time.Hour
	if at, ok := t.OnceAt(); ok {
		if at.Sub(now) > days {
			return fmt.Sprintf("it is more than %d days away", lim.MaxLifetimeDays)
		}
	} else {
		sch, perr := triggers.ParseSchedule(t.Schedule)
		loc, lerr := t.Location()
		if perr != nil || lerr != nil {
			return "its schedule cannot be read"
		}
		if gap := sch.MinGap(now, loc); gap > 0 && gap < time.Duration(lim.MinGapMinutes)*time.Minute {
			return fmt.Sprintf("it runs every %s (the least is %d minutes)", gapText(gap), lim.MinGapMinutes)
		}
	}
	used := 0
	for _, l := range live {
		used += h.runsPerDay(l, now)
	}
	if need := h.runsPerDay(capped(t, lim), now); used+need > lim.MaxRunsPerDay {
		return fmt.Sprintf("it would make %d runs a day with the others (the most is %d)", used+need, lim.MaxRunsPerDay)
	}
	return ""
}

// capped is t with the daily limit of the owner applied.
func capped(t triggers.Trigger, lim connectors.Limits) triggers.Trigger {
	if !t.OneOff() && t.CapOrDefault() > lim.MaxRunsPerDay {
		t.DailyCap = lim.MaxRunsPerDay
	}
	return t
}

// autoCreate turns a fresh proposal into a trigger that is on, bounded by the
// limits of the owner, and writes in the chat what was set up and how to
// cancel it. It reports whether the chat got the text.
func (h *Triggers) autoCreate(ctx context.Context, ap connectors.Approval, p triggers.Trigger) (triggers.Trigger, bool, error) {
	lim := ap.Limits()
	now := h.o.Clock().UTC()
	t, err := h.confirm(ctx, p.Owner, p.ID, "", func(t *triggers.Trigger) {
		*t = capped(*t, lim)
		t.Route.Auto = true
		if !t.OneOff() {
			t.Route.Until = now.Add(time.Duration(lim.MaxLifetimeDays) * 24 * time.Hour)
		}
	})
	if err != nil {
		return triggers.Trigger{}, false, err
	}
	h.audit(ctx, proposals.ActionConfirm, t, "automatic, mode "+ap.ModeOrDefault())
	return t, h.say(ctx, t, h.autoText(t)), nil
}

// say writes text in the chat and thread where a trigger was proposed.
func (h *Triggers) say(ctx context.Context, t triggers.Trigger, text string) bool {
	c := h.svc.connectors
	if c == nil || !t.Route.FromConnector() || t.Route.Chat == "" {
		return false
	}
	if _, err := c.Post(ctx, Post{Binding: t.Route.Binding, Chat: t.Route.Chat, Thread: t.Route.Thread, Text: text}); err != nil {
		h.log.Warn("studio: could not write to the chat of a trigger", "trigger", t.ID, "error", err)
		return false
	}
	return true
}

// costText says what a run costs, for the text of a proposal: every run is a
// model turn.
func (h *Triggers) costText(t triggers.Trigger) string {
	if t.OneOff() {
		return "one model run"
	}
	return "one model run each time it runs"
}

// autoText is what the host writes in the chat when it made a trigger without
// asking.
func (h *Triggers) autoText(t triggers.Trigger) string {
	where := "the Inbox in Studio"
	switch t.Route.TargetOrDefault() {
	case triggers.TargetOrigin:
		where = "this chat"
	case triggers.TargetChannel:
		where = "the channel " + t.Route.Channel
	}
	end := ""
	if !t.Route.Until.IsZero() {
		end = fmt.Sprintf(" It stops by itself on %s.", t.Route.Until.UTC().Format("2006-01-02"))
	}
	return fmt.Sprintf("Set up %q: %s. Cost: %s. Results go to %s.%s\nTo remove it, reply \"cancel\" here, or \"cancel %s\" at any time.",
		t.Name, lowerFirst(h.whenText(t)), h.costText(t), where, end, t.Route.Code)
}

// whenText is summary with the day of a one-off trigger spelled out.
func (h *Triggers) whenText(t triggers.Trigger) string {
	if at, ok := t.OnceAt(); ok {
		loc, _ := t.Location()
		zone := t.TZ
		if zone == "" {
			zone = "UTC"
		}
		return "Once, " + at.In(loc).Format("Mon 2006-01-02 at 15:04") + " (" + zone + ")"
	}
	return h.summary(t)
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// ---- Spent and ended triggers ----

// retire removes the one-off trigger of a chat once its run ended, whatever
// the end was: it can run no more. A trigger made in Studio stays as a record.
func (h *Triggers) retire(ctx context.Context, t triggers.Trigger) {
	if !t.Route.FromConnector() || !t.OneOff() {
		return
	}
	if err := h.DeleteTrigger(ctx, t.Owner, t.ID); err != nil && !errors.Is(err, triggers.ErrNotFound) {
		h.log.Warn("studio: could not remove a spent one-off trigger", "trigger", t.ID, "error", err)
		return
	}
	h.audit(ctx, proposals.ActionDismiss, t, "one-off trigger ran; removed")
}

// spentGrace is how long after its time a one-off trigger of a chat is kept
// when no run took it (the capability was off, the host was down). A run that
// is queued or running keeps it.
const spentGrace = 15 * time.Minute

// sweepTimers removes the one-off triggers of chats whose time has passed
// without a run to finish them, and the recurring triggers that were made
// without asking and reached the end of their lifetime. The leader calls it in
// a pass, at most once a minute.
func (h *Triggers) sweepTimers(ctx context.Context) {
	h.mu.Lock()
	now := h.o.Clock()
	if now.Sub(h.timersSwept) < time.Minute {
		h.mu.Unlock()
		return
	}
	h.timersSwept = now
	h.mu.Unlock()
	all, err := h.o.Store.AllTriggers(ctx)
	if err != nil {
		h.log.Warn("studio: could not list triggers to sweep the timers", "error", err)
		return
	}
	for _, t := range all {
		if t.Proposed || !t.Route.FromConnector() {
			continue
		}
		switch {
		case !t.Route.Until.IsZero() && !now.Before(t.Route.Until):
			if h.DeleteTrigger(ctx, t.Owner, t.ID) == nil {
				h.audit(ctx, proposals.ActionExpire, t, "lifetime ended; removed")
				h.say(ctx, t, fmt.Sprintf("%q reached the end of its lifetime and was removed.", t.Name))
			}
		case t.OneOff():
			at, _ := t.OnceAt()
			if now.Sub(at) < spentGrace || h.hasActiveRun(ctx, t.ID) {
				continue
			}
			h.retire(ctx, t)
		}
	}
}

// hasActiveRun says whether a run of the trigger is queued or running.
func (h *Triggers) hasActiveRun(ctx context.Context, id string) bool {
	runs, err := h.o.Store.ListRuns(ctx, id, 3)
	if err != nil {
		return true
	}
	for _, r := range runs {
		if !r.Status.Final() {
			return true
		}
	}
	return false
}

// afterPropose finishes the proposal that a chat of a messaging app made: under
// the approval of the binding it makes the trigger at once, within the owner's
// limits; otherwise it writes the proposal in the chat and waits for an answer.
// fresh is false for a proposal that already waited.
func (h *Triggers) afterPropose(ctx context.Context, a workspace.Actor, t triggers.Trigger, fresh bool, here triggers.Route) (string, error) {
	note := ""
	if c := h.svc.connectors; c != nil && fresh {
		if ap := c.ApprovalOf(ctx, t.Route.Binding); ap.Auto() {
			note = h.autoVerdict(ctx, ap, t)
			if note == "" {
				made, wrote, err := h.autoCreate(ctx, ap, t)
				if err == nil {
					h.svc.bus.Publish(key(a.Principal, a.ChatID), session.Event{Kind: session.KindTriggerProposed, IDs: []string{made.ID}, Text: made.Name})
					return h.autoResult(made, wrote), nil
				}
				note = toolErr(err).Error()
			}
		}
	}
	return h.announceProposal(ctx, a, t, here, note)
}

// autoResult words, for the model, what happened when a trigger was made
// without asking.
func (h *Triggers) autoResult(t triggers.Trigger, wrote bool) string {
	if wrote {
		return fmt.Sprintf("Set up %q (%s) at once, under the owner's settings. The host told the person in this chat what it is and how to cancel it. "+
			"Say in one short sentence that it is set up; do not repeat the details and do not offer a code.", t.Name, h.whenText(t))
	}
	return fmt.Sprintf("Set up %q (%s) at once, under the owner's settings. The host could not write to this chat. Tell the person it is set up, what it does, "+
		"and that replying \"cancel\" removes it.", t.Name, h.whenText(t))
}
