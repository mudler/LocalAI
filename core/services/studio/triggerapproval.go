package studio

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
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
func (h *Triggers) autoVerdict(ctx context.Context, v hostmsg.Voice, a workspace.Actor, ap connectors.Approval, t triggers.Trigger, dr DroppedActs) string {
	lim := ap.Limits()
	now := h.o.Clock()
	if ap.ModeOrDefault() == connectors.ApprovalAutoReadonly {
		switch {
		case !t.Route.ReadOnly:
			return v.T("verdict.not_readonly")
		case t.Writes != "" || t.Widget != "":
			return v.T("verdict.writes")
		}
	}
	if !dr.Empty() {
		// The model asked for more than is allowed here. The person must see
		// what is left out before anything starts.
		return v.T("verdict.dropped")
	}
	if why := h.actsVerdict(ctx, v, a, ap, t); why != "" {
		return why
	}
	if t.Route.TargetOrDefault() == triggers.TargetChannel {
		return v.T("verdict.channel")
	}
	if t.Kind != triggers.KindSchedule {
		return v.T("verdict.files")
	}
	all, err := h.o.Store.ListTriggers(ctx, t.Owner)
	if err != nil {
		return v.T("verdict.count_fail")
	}
	live := autoLive(all, t.Route.Binding)
	if len(live) >= lim.MaxPending {
		return v.T("verdict.too_many", "n", len(live), "max", lim.MaxPending)
	}
	days := time.Duration(lim.MaxLifetimeDays) * 24 * time.Hour
	if at, ok := t.OnceAt(); ok {
		if at.Sub(now) > days {
			return v.T("verdict.far", "days", lim.MaxLifetimeDays)
		}
	} else {
		sch, perr := triggers.ParseSchedule(t.Schedule)
		loc, lerr := t.Location()
		if perr != nil || lerr != nil {
			return v.T("verdict.schedule")
		}
		if gap := sch.MinGap(now, loc); gap > 0 && gap < time.Duration(lim.MinGapMinutes)*time.Minute {
			return v.T("verdict.gap", "gap", gapTextIn(v, gap), "min", lim.MinGapMinutes)
		}
	}
	used := 0
	for _, l := range live {
		used += h.runsPerDay(l, now)
	}
	if need := h.runsPerDay(capped(t, lim), now); used+need > lim.MaxRunsPerDay {
		return v.T("verdict.runs", "n", used+need, "max", lim.MaxRunsPerDay)
	}
	return ""
}

// actsVerdict says why a proposal with acts may not be made without asking;
// empty means it may, also when there are no acts. The rules:
//
//   - auto_readonly never makes a trigger whose runs act: it asks.
//   - auto makes it at once only when every act, act.files included when the
//     plan writes a file, is one the owner allowed for the binding (Acts).
//   - every act is sensitive (builtins grants), so the sender of the turn must
//     also be paired: a role or the allowlist alone is not enough.
//
// The limits of the approval apply on top, as for any trigger.
func (h *Triggers) actsVerdict(ctx context.Context, v hostmsg.Voice, a workspace.Actor, ap connectors.Approval, t triggers.Trigger) string {
	acts := effectiveActs(t)
	if len(acts) == 0 {
		return ""
	}
	if ap.ModeOrDefault() == connectors.ApprovalAutoReadonly {
		return v.T("verdict.readonly_acts")
	}
	var sensitive bool
	for _, n := range acts {
		if !ap.AllowsAct(n) {
			return v.T("verdict.not_allowed", "act", actLabel(v, n))
		}
		sensitive = sensitive || actSensitive(n)
	}
	if sensitive && !h.senderPaired(ctx, a, t) {
		return v.T("verdict.paired")
	}
	return ""
}

// senderPaired says whether every sender of the turn that proposed t is paired
// with the binding. It asks the capability gate with the Sensitive mark, which
// ignores roles and counts a pairing only. Any doubt is false.
func (h *Triggers) senderPaired(ctx context.Context, a workspace.Actor, t triggers.Trigger) bool {
	g, ok := h.svc.access.get(a.ChatID)
	if !ok || len(g.Senders) == 0 {
		return false
	}
	for _, sender := range g.Senders {
		if !h.svc.allowCap(ctx, CapabilityRequest{Capability: CapTriggersPropose, Binding: t.Route.Binding, Owner: t.Owner,
			Agent: t.AgentID, Sender: sender, Kind: g.Kind, Sensitive: true}) {
			return false
		}
	}
	return true
}

// canAskActs says whether the binding of a proposal lets runs act within what
// the agent holds, so a read only proposal can say that the person may ask.
func (h *Triggers) canAskActs(ctx context.Context, t triggers.Trigger) bool {
	if h.svc.connectors == nil || t.Route.Binding == "" {
		return false
	}
	return len(requestableActs(h.svc.connectors.ApprovalOf(ctx, t.Route.Binding), h.svc.AgentGrants(ctx, t.Owner, t.AgentID))) > 0
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
func (h *Triggers) autoCreate(ctx context.Context, ap connectors.Approval, p triggers.Trigger, dr DroppedActs) (triggers.Trigger, bool, error) {
	lim := ap.Limits()
	now := h.o.Clock().UTC()
	t, err := h.confirm(ctx, p.Owner, p.ID, "", func(t *triggers.Trigger) {
		*t = capped(*t, lim)
		// The owner's approval has no time limit of its own, so only the
		// administrator's ceiling applies to a run limit.
		if ceiling := h.RunLimit(); t.RunLimit > ceiling {
			t.RunLimit = ceiling
		}
		t.Route.Auto = true
		if !t.OneOff() {
			t.Route.Until = now.Add(time.Duration(lim.MaxLifetimeDays) * 24 * time.Hour)
		}
	})
	if err != nil {
		return triggers.Trigger{}, false, err
	}
	h.audit(ctx, proposals.ActionConfirm, t, "automatic, mode "+ap.ModeOrDefault())
	return t, h.say(ctx, t, h.autoText(h.voiceOf(ctx, t), t, h.channelLabel(ctx, t), dr)), nil
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
func (h *Triggers) costText(v hostmsg.Voice, t triggers.Trigger) string {
	if t.OneOff() {
		return v.T("trigger.cost.once")
	}
	return v.T("trigger.cost.each")
}

// autoText is what the host writes in the chat when it made a trigger without
// asking.
func (h *Triggers) autoText(v hostmsg.Voice, t triggers.Trigger, where string, dr DroppedActs) string {
	end := ""
	if !t.Route.Until.IsZero() {
		end = v.T("trigger.auto_stops", "date", t.Route.Until.UTC().Format("2006-01-02"))
	}
	if d := dr.TextIn(v); d != "" {
		end += " " + d
	}
	if acts := effectiveActs(t); len(acts) > 0 {
		end += " " + actsText(v, t, false)
	}
	return v.T("trigger.auto", "name", strconv.Quote(t.Name), "when", lowerFirst(h.whenTextIn(v, t)), "cost", h.costText(v, t), "where", resultsGo(v, t, where), "end", end, "code", t.Route.Code)
}

// whenText is summary with the day of a one-off trigger spelled out.
func (h *Triggers) whenText(t triggers.Trigger) string { return h.whenTextIn(hostmsg.Voice{}, t) }

// whenTextIn is whenText in the language of the voice.
func (h *Triggers) whenTextIn(v hostmsg.Voice, t triggers.Trigger) string {
	if at, ok := t.OnceAt(); ok {
		loc, _ := t.Location()
		zone := t.TZ
		if zone == "" {
			zone = "UTC"
		}
		local := at.In(loc)
		return v.T("when.once_at", "weekday", v.T("day.short."+strconv.Itoa(int(local.Weekday()))), "date", local.Format("2006-01-02"), "time", local.Format("15:04"), "zone", zone)
	}
	return h.summaryIn(v, t)
}

func lowerFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToLower(r)) + s[n:]
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
				h.say(ctx, t, h.voiceOf(ctx, t).T("trigger.lifetime", "name", strconv.Quote(t.Name)))
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
func (h *Triggers) afterPropose(ctx context.Context, a workspace.Actor, t triggers.Trigger, fresh bool, here triggers.Route, dr DroppedActs, may []string) (string, error) {
	note := ""
	if c := h.svc.connectors; c != nil && fresh {
		if ap := c.ApprovalOf(ctx, t.Route.Binding); ap.Auto() {
			note = h.autoVerdict(ctx, h.voiceOf(ctx, t), a, ap, t, dr)
			if note == "" {
				made, wrote, err := h.autoCreate(ctx, ap, t, dr)
				if err == nil {
					h.svc.bus.Publish(key(a.Principal, a.ChatID), session.Event{Kind: session.KindTriggerProposed, IDs: []string{made.ID}, Text: made.Name})
					return h.autoResult(made, wrote, dr), nil
				}
				note = toolErr(err).Error()
			}
		}
	}
	return h.announceProposal(ctx, a, t, here, note, dr, may)
}

// autoResult words, for the model, what happened when a trigger was made
// without asking.
func (h *Triggers) autoResult(t triggers.Trigger, wrote bool, dr DroppedActs) string {
	tail := ""
	if !dr.Empty() {
		tail = " Acts that were asked for and dropped, as the host told the person: " + strings.Join(append(slices.Clone(dr.Binding), dr.Agent...), ", ") + "."
	}
	if wrote {
		return fmt.Sprintf("Set up %q (%s) at once, under the owner's settings. The host told the person in this chat what it is and how to cancel it. "+
			"Say in one short sentence that it is set up; do not repeat the details and do not offer a code.%s", t.Name, h.whenText(t), tail)
	}
	return fmt.Sprintf("Set up %q (%s) at once, under the owner's settings. The host could not write to this chat. Tell the person it is set up, what it does, "+
		"and that replying \"cancel\" removes it.%s", t.Name, h.whenText(t), tail)
}
