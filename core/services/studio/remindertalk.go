package studio

import (
	"context"
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
	verbSkip             = "skip"
	verbOK               = "ok"
	// replyWindow is how long after a reminder a bare word still answers it.
	replyWindow = 2 * time.Hour
)

// reminderActions are the buttons of the report of a reminder run, and the
// hint that replaces them on a connection that shows none. Any other trigger
// gets neither.
func (h *Triggers) reminderActions(ctx context.Context, t triggers.Trigger) ([]contract.MessageAction, string) {
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
	}
	if m := minutesRE.FindStringSubmatch(t); m != nil {
		n, err := strconv.Atoi(m[1])
		if err == nil && n > 0 && n <= 24*60 {
			return "snooze", n, true
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
		if !found {
			return "", false // the word is for the agent
		}
		r, verb, minutes = got, vb, n
	}
	switch {
	case verb == verbOK:
		return v.T("reminder.noted"), true
	case verb == verbSkip:
		yes := true
		if _, err := h.PatchReminder(ctx, r.Owner, r.Reminder.ParentID, r.ID, ReminderPatch{Skip: &yes}); err != nil {
			return v.T("reminder.gone"), true
		}
		return v.T("reminder.skipped"), true
	case minutes > 0:
		view, err := h.PatchReminder(ctx, r.Owner, r.Reminder.ParentID, r.ID, ReminderPatch{SnoozeFor: time.Duration(minutes) * time.Minute})
		if err != nil {
			return v.T("reminder.gone"), true
		}
		w, _ := h.o.Store.GetTrigger(ctx, r.Reminder.ParentID)
		return v.T("reminder.snoozed", "when", whenText(w, view.Due, h.o.Clock())), true
	}
	return v.T("reminder.gone"), true
}

// lastReminderIn is the reminder of the owner that reported last in a chat of
// the binding, within replyWindow.
func (h *Triggers) lastReminderIn(ctx context.Context, b connectors.Binding, chat string) (triggers.Trigger, bool) {
	all, err := h.o.Store.ListTriggers(ctx, principal(b.Owner))
	if err != nil {
		return triggers.Trigger{}, false
	}
	now := h.o.Clock()
	var best triggers.Trigger
	var at time.Time
	for _, t := range all {
		if !t.IsReminder() || t.Route.ChannelBinding() != b.ID || (t.Route.Channel != chat && t.Route.Chat != chat) {
			continue
		}
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
