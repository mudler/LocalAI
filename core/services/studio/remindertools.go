package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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

// In a chat in Studio the same tools serve the person, who may change the
// reminders of their watches by asking: there they take the watch, and a set
// or a cancel only proposes the change, which the person approves on its card
// (see reminderchange.go).
const chatWatchArg = `"watch":{"type":"string","maxLength":255,"description":"The watch: its ref from list_reminders, or its name. Leave it out when you have one watch."},`

const setReminderChatDescription = "Propose to set or change the reminder of one of the user's watches for one item. " +
	"watch is the watch (its ref from list_reminders); key is the item's key as list_reminders shows it, or <source>:<id> for an item that has no reminder yet. " +
	"Time it with offset (relative to the item's time, such as -15m), at (a time, 2026-11-26 14:45 in the watch's zone, or RFC 3339), or in (a delay from now, such as 20m). " +
	"task is what the reminder does, in plain words; lead is how long before the reminder time its run starts. " +
	"For an item with no reminder yet, give title and anchor (the item's time, RFC 3339) as you read them from the item. " +
	"Nothing changes until the user presses Approve on the card in the chat: say that, and do not say it changed until they do."

const cancelReminderChatDescription = "Propose to cancel the reminder of one of the user's watches for one item. watch is the watch (its ref from list_reminders); key is the item's key. " +
	"Nothing changes until the user presses Approve on the card in the chat."

const listRemindersChatDescription = "List the reminders of the user's watches: each watch with its ref, and for each reminder the item, its key, when it fires and its task. watch narrows the list to one watch."

// registerReminderTools registers set_reminder, cancel_reminder and
// list_reminders. They are offered in the run of a watch or of one of its
// reminders, where they act at once, and in a chat in Studio of a person who
// has a watch, where a set or a cancel waits for the person's approval.
func (s *Service) registerReminderTools() error {
	type runFn func(context.Context, builtins.Chat, triggers.Trigger, json.RawMessage) (string, error)
	type chatFn func(context.Context, builtins.Chat, []triggers.Trigger, json.RawMessage) (string, error)
	for _, tool := range []struct {
		id, label, desc, schema string
		chatDesc, chatSchema    string
		readOnly                bool
		run                     runFn
		chat                    chatFn
	}{
		{SetReminderName, "Set a reminder", setReminderDescription, setReminderSchema, setReminderChatDescription,
			strings.Replace(setReminderSchema, `"properties":{`, `"properties":{`+chatWatchArg, 1), false, s.setReminderTool, s.setReminderChatTool},
		{CancelReminderName, "Cancel a reminder", cancelReminderDescription, cancelReminderSchema, cancelReminderChatDescription,
			strings.Replace(cancelReminderSchema, `"properties":{`, `"properties":{`+chatWatchArg, 1), false, s.cancelReminderTool, s.cancelReminderChatTool},
		{ListRemindersName, "List reminders", listRemindersDescription, listRemindersSchema, listRemindersChatDescription,
			`{"type":"object","properties":{` + strings.TrimSuffix(chatWatchArg, ",") + `}}`, true, s.listRemindersTool, s.listRemindersChatTool},
	} {
		err := s.builtins.Register(builtins.Tool{
			ID: tool.id, Label: tool.label, Description: tool.desc, Unlisted: true, ReadOnly: tool.readOnly,
			Enabled: func() bool { return s.triggers != nil },
			Build: func(ctx context.Context, c builtins.Chat) (session.Tool, bool, string) {
				if w, why := s.watchOfRun(ctx, c); why == "" {
					return session.Tool{Description: tool.desc, Schema: json.RawMessage(tool.schema),
						Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return tool.run(ctx, c, w, raw) }}, true, ""
				}
				watches, why := s.watchesOfChat(ctx, c)
				if why != "" {
					return session.Tool{}, false, why
				}
				return session.Tool{Description: tool.chatDesc, Schema: json.RawMessage(tool.chatSchema),
					Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return tool.chat(ctx, c, watches, raw) }}, true, ""
			},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// watchesOfChat gives the watches of the person of a chat in Studio, for the
// reminder tools of that chat. A chat that is not one a person watches, a
// sub-agent, a chat that switched the triggers off and a person with no watch
// get no reminder tools: they would cost tokens at each turn for nothing.
func (s *Service) watchesOfChat(ctx context.Context, c builtins.Chat) ([]triggers.Trigger, string) {
	const notHere = "reminders are kept by the run of their watch, or changed in a chat in Studio"
	h := s.triggers
	switch {
	case h == nil || c.Origin != "" || c.SubAgent:
		return nil, notHere
	case slices.Contains(c.GroupsOff, builtins.GroupTriggers):
		return nil, "this chat switched the triggers off"
	}
	if err := s.mayUseTriggers(ctx, c.Actor.Principal); err != nil {
		return nil, err.Error()
	}
	all, err := h.o.Store.ListTriggers(ctx, principal(c.Actor.Principal))
	if err != nil {
		return nil, notHere
	}
	var out []triggers.Trigger
	for _, t := range all {
		if t.IsWatch() && !t.Proposed {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, "the user has no watch"
	}
	return out, ""
}

// chatWatch picks the watch a tool call in a chat names.
func chatWatch(watches []triggers.Trigger, ref string) (triggers.Trigger, error) {
	if strings.TrimSpace(ref) == "" {
		if len(watches) == 1 {
			return watches[0], nil
		}
		names := make([]string, 0, len(watches))
		for _, w := range watches {
			names = append(names, fmt.Sprintf("%s (%s)", w.Name, TriggerRef(w.ID)))
		}
		return triggers.Trigger{}, fmt.Errorf("give watch, one of: %s", strings.Join(names, ", "))
	}
	return resolveTriggerIn(watches, ref)
}

func (s *Service) setReminderChatTool(ctx context.Context, c builtins.Chat, watches []triggers.Trigger, raw json.RawMessage) (string, error) {
	in, err := session.DecodeArgs[struct {
		Watch, Key, Offset, At, In, Task, Lead, Anchor, Title string
	}](raw)
	if err != nil {
		return "", err
	}
	w, err := chatWatch(watches, in.Watch)
	if err != nil {
		return "", toolErr(err)
	}
	h := s.triggers
	rec, err := h.ProposeReminder(ctx, c.Actor.Principal, c.Actor.ChatID, w, ReminderInput{Key: in.Key, Offset: in.Offset, At: in.At, In: in.In,
		Task: in.Task, Lead: in.Lead, Anchor: in.Anchor, Title: in.Title})
	if why, ok := reminderRefusal(err); ok {
		return "Not proposed: " + why, nil
	}
	if err != nil {
		return "", toolErr(err)
	}
	h.changeCard(ctx, c.Actor, rec)
	due := triggers.PlanFor(w, *rec.Reminder, h.o.Clock()).Due
	return fmt.Sprintf("Asked to set the reminder for %q at %s. Nothing changes until the user presses Approve on the card in the chat; do not say it is set until they do.",
		rec.Reminder.Title, whenText(w, due, h.o.Clock())), nil
}

func (s *Service) cancelReminderChatTool(ctx context.Context, c builtins.Chat, watches []triggers.Trigger, raw json.RawMessage) (string, error) {
	in, err := session.DecodeArgs[struct{ Watch, Key string }](raw)
	if err != nil {
		return "", err
	}
	w, err := chatWatch(watches, in.Watch)
	if err != nil {
		return "", toolErr(err)
	}
	h := s.triggers
	rec, err := h.ProposeCancelReminder(ctx, c.Actor.Principal, c.Actor.ChatID, w, strings.TrimSpace(in.Key))
	if errors.Is(err, triggers.ErrNotFound) {
		return "This watch has no reminder for that item.", nil
	}
	if err != nil {
		return "", toolErr(err)
	}
	h.changeCard(ctx, c.Actor, rec)
	return fmt.Sprintf("Asked to cancel the reminder for %q. Nothing changes until the user presses Approve on the card in the chat; do not say it is cancelled until they do.",
		rec.Reminder.Title), nil
}

func (s *Service) listRemindersChatTool(ctx context.Context, c builtins.Chat, watches []triggers.Trigger, raw json.RawMessage) (string, error) {
	in, err := session.DecodeArgs[struct{ Watch string }](raw)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Watch) != "" {
		w, err := chatWatch(watches, in.Watch)
		if err != nil {
			return "", toolErr(err)
		}
		watches = []triggers.Trigger{w}
	}
	var b strings.Builder
	for _, w := range watches {
		fmt.Fprintf(&b, "Watch %q (ref %s) on %s:\n", w.Name, TriggerRef(w.ID), w.Source)
		list, err := s.listRemindersTool(ctx, c, w, nil)
		if err != nil {
			return "", err
		}
		b.WriteString(list)
		if !strings.HasSuffix(list, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String(), nil
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
// the add-on's data. The newest event of the item wins.
func (h *Triggers) thingFacts(ctx context.Context, chatID, key string) (watchFacts, bool) {
	run, err := h.o.Store.RunByChat(ctx, chatID)
	if err != nil {
		return watchFacts{}, false
	}
	var found watchFacts
	ok := false
	for _, f := range factsOf(run.Payload) {
		if f.Key == key {
			found, ok = f, true
		}
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
	if why, ok := reminderRefusal(err); ok {
		return "Not set: " + why, nil
	}
	if err != nil {
		return "", toolErr(err)
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
	// offset is Offset for a caller that has it as a duration already.
	offset *time.Duration
}

// reminderError is a refusal of a reminder that its asker can fix. Its text
// is written for them: the model of a run, or the person through the API.
type reminderError struct{ why string }

func (e reminderError) Error() string { return e.why }

// reminderErr makes a reminderError.
func reminderErr(format string, a ...any) error {
	return reminderError{why: fmt.Sprintf(format, a...)}
}

// reminderRefusal gives the text of a reminderError in err, and false for
// any other error, nil included.
func reminderRefusal(err error) (string, bool) {
	var re reminderError
	if errors.As(err, &re) {
		return re.why, true
	}
	return "", false
}

// SetReminder creates or replaces the reminder of the watch w for one item.
func (h *Triggers) SetReminder(ctx context.Context, w triggers.Trigger, in ReminderInput) (triggers.Trigger, error) {
	now := h.o.Clock()
	p, err := h.storeReminder(ctx, w, in, now)
	if err != nil {
		return triggers.Trigger{}, err
	}
	h.record(ctx, reminderAction{w: w, now: now, line: p.line(w, now)})
	return p.next, nil
}

// storeReminder plans the reminder and writes it, under the write lock. A
// write that another frontend beat is planned again on top of what it wrote:
// a change of the reminder (ErrConflict), or the reminder it made for the
// same item meanwhile (ErrDuplicate), which this write then changes.
func (h *Triggers) storeReminder(ctx context.Context, w triggers.Trigger, in ReminderInput, now time.Time) (reminderPlan, error) {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	var p reminderPlan
	err := triggers.RetryOnConflict(func() error {
		var err error
		if p, err = h.planReminder(ctx, w, in, now); err != nil {
			return err
		}
		err = h.o.Store.PutTrigger(ctx, p.next)
		if p.isNew && errors.Is(err, triggers.ErrDuplicate) {
			return triggers.ErrConflict
		}
		return err
	})
	return p, err
}

// reminderAction is an action on the reminders of a watch, to record once
// the write lock is let go.
type reminderAction struct {
	w    triggers.Trigger
	now  time.Time
	line watchText
}

// record writes the action to the watch's history and the Inbox. The caller
// must not hold the write lock: the per-minute limit may pause the watch,
// which takes it.
func (h *Triggers) record(ctx context.Context, a reminderAction) {
	h.recordAction(ctx, a.w, a.now, a.line)
	h.Notify()
}

// reminderPlan is the reminder of a watch for one item as a request leaves
// it, before it is stored.
type reminderPlan struct {
	// cur is the reminder as it is; zero when isNew.
	cur   triggers.Trigger
	next  triggers.Trigger
	isNew bool
	due   time.Time
}

// line is the history and Inbox line of the plan once it is stored.
func (p reminderPlan) line(w triggers.Trigger, now time.Time) watchText {
	return reminderSetLine(w, p.isNew, p.next.Reminder.Title, p.due, now)
}

// reminderSetLine is the line of a reminder that was set (isNew) or changed.
func reminderSetLine(w triggers.Trigger, isNew bool, title string, due, now time.Time) watchText {
	if isNew {
		return watchSay(w, "set", title, due, fmt.Sprintf("Set the reminder for %q: %s.", title, whenText(w, due, now)))
	}
	return watchSay(w, "changed", title, due, fmt.Sprintf("Changed the reminder for %q: %s.", title, whenText(w, due, now)))
}

// reminderCancelledLine is the line of a reminder that was cancelled.
func reminderCancelledLine(w triggers.Trigger, title string) watchText {
	return watchSay(w, "cancelled", title, time.Time{}, fmt.Sprintf("Cancelled the reminder for %q.", title))
}

// planReminder works out the reminder of the watch w for one item as the
// request in leaves it, from the reminder that is stored now. It stores
// nothing.
func (h *Triggers) planReminder(ctx context.Context, w triggers.Trigger, in ReminderInput, now time.Time) (reminderPlan, error) {
	bad := func(format string, a ...any) (reminderPlan, error) {
		return reminderPlan{}, reminderErr(format, a...)
	}
	key := strings.TrimSpace(in.Key)
	if !strings.HasPrefix(key, w.Source+":") || len(key) <= len(w.Source)+1 {
		return bad("the key must be an item of this watch's source, such as %s:42", w.Source)
	}
	loc := w.LocationOrUTC()
	cur, err := h.o.Store.ReminderByKey(ctx, w.ID, key)
	isNew := errors.Is(err, triggers.ErrNotFound)
	if err != nil && !isNew {
		return reminderPlan{}, err
	}
	var rem triggers.Reminder
	if isNew {
		rem = triggers.Reminder{ParentID: w.ID, Key: key, WakeOnChange: true}
		if in.ChatID != "" {
			if f, ok := h.thingFacts(ctx, in.ChatID, key); ok {
				rem.Title, rem.Zone, rem.AllDay, rem.Stamp = f.Title, f.Zone, f.AllDay, f.Stamp
				rem.Following, rem.Repeats = f.Following, len(f.Following) > 0
				if f.At != "" {
					rem.Anchor, _ = time.Parse(time.RFC3339, f.At)
				}
			}
		}
		cur = triggers.Trigger{ID: newID(), Owner: w.Owner, Kind: triggers.KindReminder, Enabled: true}
	} else {
		rem = *cur.Reminder
	}
	if in.ChatID != "" {
		if run, err := h.o.Store.RunByChat(ctx, in.ChatID); err == nil {
			rem.SetBy = run.ID
		}
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
	case in.offset != nil:
		rem.Offset, rem.At = *in.offset, time.Time{}
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
	tm := triggers.PlanFor(w, rem, now)
	switch {
	case tm.Skip:
		return bad("the item has already started, so no reminder is set")
	case tm.None:
		return bad("the watch's all-day rule sets no reminder for an item that lasts all day")
	}
	rem.Adjusted = tm.Adjusted
	next := reminderTrigger(cur, w, rem)
	next.Since = now.UTC()
	if err := next.Validate(); err != nil {
		return bad("%v", err)
	}
	p := reminderPlan{next: next, isNew: isNew, due: tm.Due}
	if !isNew {
		p.cur = cur
	}
	return p, nil
}

// reminderTrigger is the trigger of the reminder rem of the watch w, on the
// base of the stored reminder cur: what a run does, and where it reports, come
// from the watch.
func reminderTrigger(cur, w triggers.Trigger, rem triggers.Reminder) triggers.Trigger {
	next := cur
	next.Owner, next.Kind, next.Enabled = w.Owner, triggers.KindReminder, true
	next.Name = thingLine(firstNonEmpty(rem.Title, "Reminder"), triggers.MaxNameLen)
	next.Prompt = rem.Task
	next.TZ, next.AgentID, next.Model, next.Acts, next.Route = w.TZ, w.AgentID, w.Model, w.Acts, w.Route
	next.Route.Code, next.Route.Expires, next.Route.ProposedAt = "", time.Time{}, time.Time{}
	next.Reminder = &rem
	return next
}

// recordAction writes one row of the watch's history and one Inbox line for
// an action on its reminders, and applies the per-minute limit.
func (h *Triggers) recordAction(ctx context.Context, w triggers.Trigger, now time.Time, line watchText) {
	id := newID()
	row := triggers.Run{ID: id, TriggerID: w.ID, DueKey: "action@" + id, Due: now.UTC(), Status: triggers.StatusHandled,
		Reason: triggers.CutReason(line.Text), Ended: now.UTC()}
	if _, err := h.o.Store.Claim(ctx, row); err != nil {
		h.log.Warn("studio: could not record a watch action", "watch", w.ID, "error", err)
	}
	h.watchNote(ctx, w, line)
	h.limitChanges(ctx, w, now)
}

// reminderResult tells the model when the owner will hear, once.
func (h *Triggers) reminderResult(w triggers.Trigger, r triggers.Trigger) string {
	now := h.o.Clock()
	tm := triggers.PlanFor(w, *r.Reminder, now)
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

// CancelReminder removes the reminder of the watch w for an item. The delete
// is not versioned: a change another frontend makes meanwhile goes with the
// reminder, which is what a cancel asks for.
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
	h.recordAction(ctx, w, h.o.Clock(), reminderCancelledLine(w, r.Reminder.Title))
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
		due := dueText(w, r, now)
		if due == "" {
			due = "no time (already started)"
		}
		fmt.Fprintf(&b, "- %s (key %s): %s, task: %s\n", r.Reminder.Title, r.Reminder.Key, due, r.Reminder.Task)
	}
	return b.String(), nil
}
