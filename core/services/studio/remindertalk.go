package studio

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The buttons of a reminder in a messaging app, and the words that stand for
// them where the app shows no buttons. The host answers both with no model:
// a tap names the reminder in its ID, and a word goes to the reminder that
// this chat got last.
const (
	reminderActionPrefix = "reminder:"
	verbSnooze5          = "snooze5"
	verbSnooze30         = "snooze30"
	// verbSnoozeFor is a reply word with a number of minutes; it has no
	// button.
	verbSnoozeFor = "snooze"
	verbSkip      = "skip"
	verbOK        = "ok"
	verbEarlier   = "early60"
	verbNone      = "none"
	// replyWindow is how long after a reminder a bare word still answers it.
	replyWindow = 2 * time.Hour
)

// reminderActions are the buttons of the report of a reminder run, and the
// hint that replaces them on a connection that shows none. Any other trigger
// gets neither.
func (h *Triggers) reminderActions(ctx context.Context, t triggers.Trigger, run triggers.Run) ([]contract.MessageAction, string) {
	if t.IsWatch() {
		return h.headsUpActions(ctx, t, run)
	}
	if !t.IsReminder() {
		return nil, ""
	}
	v := h.voiceOf(ctx, t)
	id := func(verb string) string { return reminderActionPrefix + t.ID + ":" + verb }
	return []contract.MessageAction{
		{ID: id(verbSnooze5), Label: v.T("reminder.snooze")},
		{ID: id(verbSnooze30), Label: v.T("reminder.later")},
		{ID: id(verbSkip), Label: v.T("reminder.skip")},
		{ID: id(verbOK), Label: v.T("reminder.ok")},
	}, v.T("reminder.hint")
}

// headsUpActions are the buttons of a heads-up of a watch run that set one
// reminder: an earlier time, or no reminder (decision B and the message
// actions of the spec). A run that set none, or several, gets none.
func (h *Triggers) headsUpActions(ctx context.Context, w triggers.Trigger, run triggers.Run) ([]contract.MessageAction, string) {
	r, ok := h.setInRun(ctx, w, run)
	if !ok {
		return nil, ""
	}
	v := h.voiceOf(ctx, w)
	id := func(verb string) string { return reminderActionPrefix + r.ID + ":" + verb }
	return []contract.MessageAction{
		{ID: id(verbEarlier), Label: v.T("reminder.earlier")},
		{ID: id(verbNone), Label: v.T("reminder.none")},
	}, v.T("reminder.headsup_hint")
}

// setInRun is the one reminder of the watch that the run set or changed. The
// reminder records the run (Reminder.SetBy), so a later change of the
// reminder, by the owner or by a move of its item, does not lose it.
func (h *Triggers) setInRun(ctx context.Context, w triggers.Trigger, run triggers.Run) (triggers.Trigger, bool) {
	if run.ID == "" {
		return triggers.Trigger{}, false
	}
	list, err := h.o.Store.Reminders(ctx, w.ID)
	if err != nil {
		return triggers.Trigger{}, false
	}
	var found []triggers.Trigger
	for _, r := range list {
		if r.Reminder.SetBy == run.ID {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		return triggers.Trigger{}, false
	}
	return found[0], true
}

var minutesRE = regexp.MustCompile(`^(\d{1,4})\s*(m|min|mins|minute|minutes|minuti|minuten|minutos)$`)

// reminderWord reads a reply that stands for a button: snooze, a number of
// minutes, skip. ok is false for any other text.
func reminderWord(text string) (verb string, minutes int, ok bool) {
	t := strings.ToLower(strings.Trim(strings.TrimSpace(text), ".!"))
	switch t {
	case "snooze", "later":
		return verbSnooze5, 5, true
	case "skip":
		return verbSkip, 0, true
	case "earlier":
		return verbEarlier, 0, true
	case "no reminder", "none":
		return verbNone, 0, true
	}
	if m := minutesRE.FindStringSubmatch(t); m != nil {
		n, err := strconv.Atoi(m[1])
		if err == nil && n > 0 && n <= 24*60 {
			return verbSnoozeFor, n, true
		}
	}
	return "", 0, false
}

// answerReminder answers a tap on a button of a reminder, or a word that
// stands for one in the chat that got the reminder. It reports false for a
// message that is neither, so the message goes on as usual.
func (c *Connectors) answerReminder(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage, v hostmsg.Voice) (string, bool) {
	h := c.svc.triggers
	if h == nil {
		return "", false
	}
	var r triggers.Trigger
	verb, minutes := "", 0
	switch {
	case strings.HasPrefix(m.Action, reminderActionPrefix):
		rest := strings.TrimPrefix(m.Action, reminderActionPrefix)
		id, vb, ok := strings.Cut(rest, ":")
		got, err := h.o.Store.GetTrigger(ctx, id)
		if !ok || err != nil || !got.IsReminder() || got.Owner != principal(b.Owner) || got.Route.ChannelBinding() != b.ID {
			return v.T("reminder.gone"), true
		}
		r, verb = got, vb
		switch verb {
		case verbSnooze5:
			minutes = 5
		case verbSnooze30:
			minutes = 30
		}
	case m.Action != "":
		return v.T("reminder.gone"), true // a button the host does not know
	default:
		vb, n, ok := reminderWord(m.Text)
		if !ok {
			return "", false
		}
		got, found := h.lastReminderIn(ctx, b, m.Chat)
		if vb == verbEarlier || vb == verbNone {
			got, found = h.lastHeadsUpIn(ctx, b, m.Chat)
		}
		if !found {
			return "", false // the word is for the agent
		}
		r, verb, minutes = got, vb, n
	}
	w, err := h.o.Store.GetTrigger(ctx, r.Reminder.ParentID)
	if err != nil {
		return h.reminderFailed(v, err), true
	}
	now := h.o.Clock()
	switch {
	case verb == verbEarlier:
		hourBefore := -time.Hour
		view, err := h.PatchReminder(ctx, r.Owner, w.ID, r.ID, ReminderPatch{Offset: &hourBefore})
		if err != nil {
			return h.reminderFailed(v, err), true
		}
		return v.T("reminder.retimed", "when", whenText(w, view.Due, now)), true
	case verb == verbNone:
		if err := h.DeleteReminder(ctx, r.Owner, w.ID, r.ID); err != nil {
			return h.reminderFailed(v, err), true
		}
		return v.T("reminder.cancelled"), true
	case verb == verbOK:
		return v.T("reminder.noted"), true
	case verb == verbSkip:
		yes := true
		if _, err := h.PatchReminder(ctx, r.Owner, w.ID, r.ID, ReminderPatch{Skip: &yes}); err != nil {
			return h.reminderFailed(v, err), true
		}
		return v.T("reminder.skipped"), true
	case minutes > 0:
		view, err := h.PatchReminder(ctx, r.Owner, w.ID, r.ID, ReminderPatch{SnoozeFor: time.Duration(minutes) * time.Minute})
		if err != nil {
			return h.reminderFailed(v, err), true
		}
		return v.T("reminder.snoozed", "when", whenText(w, view.Due, now)), true
	}
	return v.T("reminder.gone"), true
}

// reminderFailed answers a button or a word whose change did not apply: the
// reminder is gone, or it stays as it was. An error that is not a refusal is
// logged.
func (h *Triggers) reminderFailed(v hostmsg.Voice, err error) string {
	if errors.Is(err, triggers.ErrNotFound) {
		return v.T("reminder.gone")
	}
	if !errors.Is(err, ErrBadTrigger) {
		h.log.Warn("studio: could not apply the answer to a reminder", "error", err)
	}
	return v.T("reminder.unchanged")
}

// lastReminderIn is the reminder of the owner that reported last in a chat of
// the binding, within replyWindow.
func (h *Triggers) lastReminderIn(ctx context.Context, b connectors.Binding, chat string) (triggers.Trigger, bool) {
	now := h.o.Clock()
	var best triggers.Trigger
	var at time.Time
	for _, t := range h.reportingTo(ctx, b, chat, triggers.Trigger.IsReminder) {
		runs, err := h.o.Store.ListRuns(ctx, t.ID, 1)
		if err != nil || len(runs) == 0 || runs[0].Ended.IsZero() || now.Sub(runs[0].Ended) > replyWindow {
			continue
		}
		if runs[0].Ended.After(at) {
			best, at = t, runs[0].Ended
		}
	}
	return best, !at.IsZero()
}

// lastHeadsUpIn is the reminder that the newest heads-up of a watch of the
// owner set, when that heads-up was reported in this chat of the binding
// within replyWindow. Every watch on the chat is looked at, so with two
// watches on one chat the reply goes to the heads-up the owner got last, and
// a run that is still going does not hide an earlier one that ended.
func (h *Triggers) lastHeadsUpIn(ctx context.Context, b connectors.Binding, chat string) (triggers.Trigger, bool) {
	now := h.o.Clock()
	var best triggers.Trigger
	var at time.Time
	for _, w := range h.reportingTo(ctx, b, chat, triggers.Trigger.IsWatch) {
		runs, err := h.o.Store.ListRuns(ctx, w.ID, 20)
		if err != nil {
			continue
		}
		for _, run := range runs {
			if run.Status == triggers.StatusHandled || run.ChatID == "" || run.Ended.IsZero() {
				continue // a history line, or a run that has not reported yet
			}
			if now.Sub(run.Ended) > replyWindow || !run.Ended.After(at) {
				break // the runs are newest first: the rest are older still
			}
			if r, ok := h.setInRun(ctx, w, run); ok {
				best, at = r, run.Ended
			}
			break // only the newest report of this watch is the heads-up a reply answers
		}
	}
	return best, !at.IsZero()
}

// reportingTo lists the owner's triggers of a kind (is) that report to a
// chat of the binding.
func (h *Triggers) reportingTo(ctx context.Context, b connectors.Binding, chat string, is func(triggers.Trigger) bool) []triggers.Trigger {
	all, err := h.o.Store.ListTriggers(ctx, principal(b.Owner))
	if err != nil {
		return nil
	}
	var out []triggers.Trigger
	for _, t := range all {
		if is(t) && t.Route.ChannelBinding() == b.ID && (t.Route.Channel == chat || t.Route.Chat == chat) {
			out = append(out, t)
		}
	}
	return out
}
