package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// The tools with which the run of a watch, or of one of its reminders, keeps
// the watch's reminders. They exist only in those runs: the owner approved the
// watch once, and that approval covers what it does with its reminders. A
// chat that is not such a run never gets them.
const (
	SetReminderName    = "set_reminder"
	CancelReminderName = "cancel_reminder"
	ListRemindersName  = "list_reminders"
)

const setReminderDescription = "Create or replace the reminder of this watch for one item of its source. " +
	"key is the item's key as the event gave it, such as calendar:42; only items of this watch's source. " +
	"Time it with offset (relative to the item's time, such as -15m for 15 minutes before), at (a time, 2026-11-26 14:45 in the watch's zone, or RFC 3339), or in (a delay from now, such as 20m or 3d for an item with no time). " +
	"Leave all three out for the watch's default. task is what the reminder run does, in plain words, such as \"remind me\" or \"prepare a short brief from my mail and notes\". " +
	"lead is how long before the reminder time the run starts, so a brief arrives on time (such as 30m); leave it out for a plain reminder. " +
	"For a repeating item, give anchor: the start of its next occurrence (RFC 3339), read from the item. " +
	"The result says the time the owner will hear from you; repeat it in your message once."

const setReminderSchema = `{"type":"object","properties":{` +
	`"key":{"type":"string","maxLength":255,"description":"The item's key, such as calendar:42."},` +
	`"offset":{"type":"string","description":"Relative to the item's time: -15m, -1h, -1d."},` +
	`"at":{"type":"string","description":"A time: 2026-11-26 14:45 in the watch's zone, or RFC 3339."},` +
	`"in":{"type":"string","description":"A delay from now: 20m, 2h, 3d."},` +
	`"task":{"type":"string","maxLength":500,"description":"What the reminder run does."},` +
	`"lead":{"type":"string","description":"How long before the reminder time the run starts: 30m."},` +
	`"anchor":{"type":"string","description":"The item's time, RFC 3339, when it is not the one the event gave (the next occurrence of a repeating item)."},` +
	`"title":{"type":"string","maxLength":200,"description":"The item's title, when the event did not give it."}},` +
	`"required":["key"]}`

const cancelReminderDescription = "Cancel the reminder of this watch for one item. key is the item's key."

const cancelReminderSchema = `{"type":"object","properties":{"key":{"type":"string","maxLength":255}},"required":["key"]}`

const listRemindersDescription = "List the reminders of this watch: the item, its key, when the reminder fires and its task."

const listRemindersSchema = `{"type":"object","properties":{}}`

// registerReminderTools registers set_reminder, cancel_reminder and
// list_reminders. Each is offered only in the run of a watch or of one of its
// reminders.
func (s *Service) registerReminderTools() error {
	for _, tool := range []struct {
		id, label, desc, schema string
		readOnly                bool
		run                     func(context.Context, builtins.Chat, triggers.Trigger, json.RawMessage) (string, error)
	}{
		{SetReminderName, "Set a reminder", setReminderDescription, setReminderSchema, false, s.setReminderTool},
		{CancelReminderName, "Cancel a reminder", cancelReminderDescription, cancelReminderSchema, false, s.cancelReminderTool},
		{ListRemindersName, "List reminders", listRemindersDescription, listRemindersSchema, true, s.listRemindersTool},
	} {
		err := s.builtins.Register(builtins.Tool{
			ID: tool.id, Group: builtins.GroupTriggers, Label: tool.label, Description: tool.desc, Unlisted: true, ReadOnly: tool.readOnly,
			Enabled: func() bool { return s.triggers != nil },
			Build: func(ctx context.Context, c builtins.Chat) (session.Tool, bool, string) {
				w, why := s.watchOfRun(ctx, c)
				if why != "" {
					return session.Tool{}, false, why
				}
				return session.Tool{Description: tool.desc, Schema: json.RawMessage(tool.schema),
					Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return tool.run(ctx, c, w, raw) }}, true, ""
			},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// watchOfRun gives the watch of the run of a chat: the watch itself, or the
// watch of the reminder. Any other chat gets no reminder tools.
func (s *Service) watchOfRun(ctx context.Context, c builtins.Chat) (triggers.Trigger, string) {
	const notHere = "only the run of a watch keeps its reminders"
	if s.triggers == nil || c.Origin != session.OriginTrigger || c.SubAgent {
		return triggers.Trigger{}, notHere
	}
	st := s.triggers.o.Store
	t, err := st.GetTrigger(ctx, c.OriginID)
	if err != nil || principal(t.Owner) != principal(c.Actor.Principal) {
		return triggers.Trigger{}, notHere
	}
	if t.IsReminder() {
		if t, err = st.GetTrigger(ctx, t.Reminder.ParentID); err != nil {
			return triggers.Trigger{}, notHere
		}
	}
	if !t.IsWatch() {
		return triggers.Trigger{}, notHere
	}
	return t, ""
}

// parseWhen reads a time the model gives: RFC 3339, or "2006-01-02 15:04"
// in loc.
func parseWhen(s string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a time such as 2026-11-26 14:45", s)
}

// parseSpan reads a duration such as -15m, 2h, 3d or 1h30m.
func parseSpan(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	sign := time.Duration(1)
	if strings.HasPrefix(s, "-") {
		sign, s = -1, s[1:]
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		var n int
		if _, err := fmt.Sscanf(days, "%d", &n); err == nil && fmt.Sprint(n) == days {
			return sign * time.Duration(n) * 24 * time.Hour, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration such as 15m, 2h or 3d", s)
	}
	return sign * d, nil
}

// thingFacts finds what the host knows about an item of the run's events: the
// outer fields of the payload that the host wrote (see watchRunPayload), never
// the add-on's data.
func (h *Triggers) thingFacts(ctx context.Context, chatID, key string) (watchFacts, bool) {
	run, err := h.o.Store.RunByChat(ctx, chatID)
	if err != nil {
		return watchFacts{}, false
	}
	var found watchFacts
	ok := false
	visit := func(raw []byte) {
		var f watchFacts
		if json.Unmarshal(raw, &f) == nil && f.Watch && f.Key == key {
			found, ok = f, true
		}
	}
	var items []json.RawMessage
	if json.Unmarshal([]byte(run.Payload), &items) == nil {
		for _, it := range items {
			visit(it)
		}
	} else {
		visit([]byte(run.Payload))
	}
	return found, ok
}

func (s *Service) setReminderTool(ctx context.Context, c builtins.Chat, w triggers.Trigger, raw json.RawMessage) (string, error) {
	in, err := session.DecodeArgs[struct {
		Key, Offset, At, In, Task, Lead, Anchor, Title string
	}](raw)
	if err != nil {
		return "", err
	}
	h := s.triggers
	r, err := h.SetReminder(ctx, w, ReminderInput{Key: in.Key, Offset: in.Offset, At: in.At, In: in.In, Task: in.Task, Lead: in.Lead,
		Anchor: in.Anchor, Title: in.Title, ChatID: c.Actor.ChatID})
	if err != nil {
		return "Not set: " + err.Error(), nil
	}
	return h.reminderResult(w, r), nil
}

// ReminderInput is a request to create or replace a reminder of a watch, in
// the words of the tool and the REST API.
type ReminderInput struct {
	Key, Offset, At, In, Task, Lead, Anchor, Title string
	// ChatID is the chat of the run that asks, to read what the event said
	// about the item. Empty for a request of the owner.
	ChatID string
}

// errReminder is a refusal of a reminder that its asker can fix.
var errReminder = errors.New("studio: reminder")

// SetReminder creates or replaces the reminder of the watch w for one item.
func (h *Triggers) SetReminder(ctx context.Context, w triggers.Trigger, in ReminderInput) (triggers.Trigger, error) {
	bad := func(format string, a ...any) (triggers.Trigger, error) {
		return triggers.Trigger{}, fmt.Errorf("%w: %s", errReminder, fmt.Sprintf(format, a...))
	}
	key := strings.TrimSpace(in.Key)
	if !strings.HasPrefix(key, w.Source+":") || len(key) <= len(w.Source)+1 {
		return bad("the key must be an item of this watch's source, such as %s:42", w.Source)
	}
	loc, err := w.Location()
	if err != nil {
		loc = time.UTC
	}
	now := h.o.Clock()
	h.wmu.Lock()
	locked := true
	defer func() {
		if locked {
			h.wmu.Unlock()
		}
	}()
	cur, err := h.o.Store.ReminderByKey(ctx, w.ID, key)
	isNew := errors.Is(err, triggers.ErrNotFound)
	if err != nil && !isNew {
		return triggers.Trigger{}, err
	}
	var rem triggers.Reminder
	if isNew {
		rem = triggers.Reminder{ParentID: w.ID, Key: key, WakeOnChange: true}
		if in.ChatID != "" {
			if f, ok := h.thingFacts(ctx, in.ChatID, key); ok {
				rem.Title, rem.Zone, rem.AllDay, rem.Stamp = f.Title, f.Zone, f.AllDay, f.Stamp
				if f.At != "" {
					rem.Anchor, _ = time.Parse(time.RFC3339, f.At)
				}
			}
		}
		cur = triggers.Trigger{ID: newID(), Owner: w.Owner, Kind: triggers.KindReminder, Enabled: true}
	} else {
		rem = *cur.Reminder
	}
	if in.Title != "" {
		rem.Title = thingLine(in.Title, triggers.MaxThingTitle)
	}
	if in.Anchor != "" {
		if rem.Anchor, err = parseWhen(in.Anchor, loc); err != nil {
			return bad("%v", err)
		}
	}
	rem.SnoozedTo = time.Time{}
	switch {
	case in.At != "":
		if rem.At, err = parseWhen(in.At, loc); err != nil {
			return bad("%v", err)
		}
	case in.In != "":
		d, err := parseSpan(in.In)
		if err != nil || d <= 0 {
			return bad("in must be a delay such as 20m or 3d")
		}
		rem.At = now.Add(d).UTC()
	case in.Offset != "":
		if rem.Offset, err = parseSpan(in.Offset); err != nil {
			return bad("%v", err)
		}
		rem.At = time.Time{}
	case isNew:
		if w.Watch.NoDefault {
			return bad("this watch has no default time: give offset, at or in")
		}
		rem.Offset = w.Watch.Offset
	}
	if in.Lead != "" {
		if rem.Lead, err = parseSpan(in.Lead); err != nil || rem.Lead < 0 {
			return bad("lead must be a duration such as 30m")
		}
	}
	if in.Task != "" {
		rem.Task = strings.TrimSpace(in.Task)
	}
	if rem.Task == "" {
		rem.Task = "remind me"
	}
	tm := triggers.PlanReminder(rem, w.Watch, loc, now)
	switch {
	case tm.Skip:
		return bad("the item has already started, so no reminder is set")
	case tm.None:
		return bad("the watch's all-day rule sets no reminder for an item that lasts all day")
	}
	rem.Adjusted = tm.Adjusted
	next := cur
	next.Owner, next.Kind, next.Enabled = w.Owner, triggers.KindReminder, true
	next.Name = thingLine(firstNonEmpty(rem.Title, "Reminder"), triggers.MaxNameLen)
	next.Prompt = rem.Task
	next.TZ, next.AgentID, next.Model, next.Acts, next.Route = w.TZ, w.AgentID, w.Model, w.Acts, w.Route
	next.Route.Code, next.Route.Expires, next.Route.ProposedAt = "", time.Time{}, time.Time{}
	next.Since = now.UTC()
	next.Reminder = &rem
	if err := next.Validate(); err != nil {
		return bad("%v", err)
	}
	if err := h.o.Store.PutTrigger(ctx, next); err != nil {
		if errors.Is(err, triggers.ErrDuplicate) {
			return bad("this watch already has a reminder for the item")
		}
		return triggers.Trigger{}, err
	}
	// The record below may pause the watch, which takes the write lock.
	h.wmu.Unlock()
	locked = false
	verb := "Changed"
	if isNew {
		verb = "Set"
	}
	line := fmt.Sprintf("%s the reminder for %q: %s.", verb, rem.Title, whenText(w, tm.Due, now))
	h.recordAction(ctx, w, now, line)
	h.Notify()
	return next, nil
}

// recordAction writes one row of the watch's history and one Inbox line for
// an action on its reminders, and applies the per-minute limit.
func (h *Triggers) recordAction(ctx context.Context, w triggers.Trigger, now time.Time, line string) {
	id := newRunID()
	row := triggers.Run{ID: id, TriggerID: w.ID, DueKey: "action@" + id, Due: now.UTC(), Status: triggers.StatusHandled,
		Reason: triggers.CutReason(line), Ended: now.UTC()}
	if _, err := h.o.Store.Claim(ctx, row); err != nil {
		h.log.Warn("studio: could not record a watch action", "watch", w.ID, "error", err)
	}
	h.watchNote(ctx, w, line)
	h.limitChanges(ctx, w, now)
}

// reminderResult tells the model when the owner will hear, once.
func (h *Triggers) reminderResult(w triggers.Trigger, r triggers.Trigger) string {
	now := h.o.Clock()
	loc, _ := w.Location()
	if loc == nil {
		loc = time.UTC
	}
	tm := triggers.PlanReminder(*r.Reminder, w.Watch, loc, now)
	out := fmt.Sprintf("Reminder set for %q at %s.", r.Reminder.Title, whenText(w, tm.Due, now))
	if r.Reminder.Lead > 0 {
		out += fmt.Sprintf(" The task starts %s earlier.", triggers.ShortDuration(r.Reminder.Lead))
	}
	switch tm.Adjusted {
	case triggers.AdjustedQuiet:
		out += " It was moved out of the owner's quiet hours, to the evening before; say so."
	case triggers.AdjustedAllDay:
		out += " The item lasts all day, so the reminder is in the morning of that day; say so."
	}
	if tm.Now {
		out += " The item starts soon, so the run starts now."
	}
	return out
}

func (s *Service) cancelReminderTool(ctx context.Context, _ builtins.Chat, w triggers.Trigger, raw json.RawMessage) (string, error) {
	in, err := session.DecodeArgs[struct{ Key string }](raw)
	if err != nil {
		return "", err
	}
	if err := s.triggers.CancelReminder(ctx, w, in.Key); err != nil {
		if errors.Is(err, triggers.ErrNotFound) {
			return "This watch has no reminder for that item.", nil
		}
		return "", err
	}
	return "Cancelled.", nil
}

// CancelReminder removes the reminder of the watch w for an item.
func (h *Triggers) CancelReminder(ctx context.Context, w triggers.Trigger, key string) error {
	r, err := h.o.Store.ReminderByKey(ctx, w.ID, strings.TrimSpace(key))
	if err != nil {
		return err
	}
	if err := h.o.Store.DeleteTrigger(ctx, r.ID); err != nil {
		return err
	}
	if run := h.currentRunner(); run != nil {
		_ = run.Cancel(ctx, r.ID)
	}
	h.recordAction(ctx, w, h.o.Clock(), fmt.Sprintf("Cancelled the reminder for %q.", r.Reminder.Title))
	return nil
}

func (s *Service) listRemindersTool(ctx context.Context, _ builtins.Chat, w triggers.Trigger, _ json.RawMessage) (string, error) {
	h := s.triggers
	list, err := h.o.Store.Reminders(ctx, w.ID)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "This watch has no reminders.", nil
	}
	now := h.o.Clock()
	var b strings.Builder
	for _, r := range list {
		due := h.dueText(w, r, now)
		if due == "" {
			due = "no time (already started)"
		}
		fmt.Fprintf(&b, "- %s (key %s): %s, task: %s\n", r.Reminder.Title, r.Reminder.Key, due, r.Reminder.Task)
	}
	return b.String(), nil
}
