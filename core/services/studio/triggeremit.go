package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/notify"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ErrEventNotDeclared is the refusal of an event whose type the add-on does
// not declare, or whose payload its declaration cannot read.
var ErrEventNotDeclared = errors.New("studio: the add-on does not declare this event")

// EmitInput is one event of an add-on, as events.emit hands it over. Owner
// and the live call (ChatID, CredentialID) come from the host's record of the
// call; the add-on sets none of them.
type EmitInput struct {
	Owner, Addon string
	Decl         contract.EventDeclaration
	ChatID       string
	CredentialID string
	Payload      []byte
	DedupeKey    string
}

// ThingOf reads what an event is about with the declaration of its type: the
// key "<addon>:<id>", the title, the time and zone, the link and a stamp of
// the payload without its time fields. It reads nothing else.
func ThingOf(addon string, d contract.EventDeclaration, payload []byte) (*triggers.Thing, error) {
	doc, err := triggers.DecodePayload(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: the payload is not JSON", ErrEventNotDeclared)
	}
	id, ok := triggers.LookupPath(doc, d.Key)
	if !ok || id == "" {
		return nil, fmt.Errorf("%w: the payload has no %s", ErrEventNotDeclared, d.Key)
	}
	th := &triggers.Thing{Key: addon + ":" + id, Lifecycle: d.Lifecycle}
	if len(th.Key) > triggers.MaxThingKey {
		return nil, fmt.Errorf("%w: the id is too long", ErrEventNotDeclared)
	}
	if title, ok := triggers.LookupPath(doc, d.Title); ok {
		th.Title = thingLine(title, triggers.MaxThingTitle)
	}
	var drop []string
	if a := d.Anchor; a != nil {
		th.Timed = a.Time != ""
		drop = append(drop, a.Time, a.Zone, a.AllDay)
		drop = append(drop, a.Moves...)
		if at, ok := triggers.LookupPath(doc, a.Time); ok {
			if t, err := time.Parse(time.RFC3339, at); err == nil {
				th.Anchor = t.UTC()
			}
		}
		if a.Zone != "" {
			if z, ok := triggers.LookupPath(doc, a.Zone); ok {
				if _, err := time.LoadLocation(z); err == nil && z != "" && !strings.EqualFold(z, "local") {
					th.Zone = z
				}
			}
		}
		if a.AllDay != "" {
			if v, ok := triggers.LookupPath(doc, a.AllDay); ok && v == "true" {
				th.AllDay = true
			}
		}
		if a.Following != "" {
			drop = append(drop, a.Following)
			if !th.Anchor.IsZero() {
				for _, t := range triggers.LookupTimes(doc, a.Following, contract.MaxAnchorFollowing) {
					// Only later occurrences, in order: the list is the add-on's.
					last := th.Anchor
					if n := len(th.Following); n > 0 {
						last = th.Following[n-1]
					}
					if t.After(last) {
						th.Following = append(th.Following, t)
					}
				}
			}
		}
	}
	if d.Link != "" {
		th.Link = "/app/addons/" + url.PathEscape(addon) + "/" + strings.ReplaceAll(d.Link, "{key}", url.QueryEscape(id))
	}
	th.Stamp = stampWithout(doc, drop)
	return th, nil
}

// thingLine keeps the first line of s, cut to max characters.
func thingLine(s string, max int) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return triggers.CutText(s, max)
}

// stampWithout is a short fingerprint of the payload with the given paths
// taken out, so that two payloads that differ only in the time of their thing
// have the same stamp.
func stampWithout(doc any, drop []string) string {
	for _, p := range drop {
		if p != "" {
			triggers.DropPath(doc, p)
		}
	}
	raw, _ := json.Marshal(doc) // map keys are sorted: the text is canonical
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// ActorOf says who caused a change, from the live call alone: a paired client
// by its credential, the run of a trigger by the origin of its chat, the
// owner for the add-on's page and the owner's own chats.
func (h *Triggers) ActorOf(ctx context.Context, owner, chatID, credentialID string) triggers.Actor {
	if id, ok := strings.CutPrefix(credentialID, contract.ClientCredentialPrefix); ok {
		return triggers.Actor{Kind: triggers.ActorClient, ClientID: id}
	}
	if chatID == "" {
		return triggers.Actor{Kind: triggers.ActorOwner}
	}
	rec, err := h.svc.getChatRaw(ctx, owner, chatID)
	if err != nil {
		// A chat the host cannot read is not proof that the owner acted.
		return triggers.Actor{Kind: triggers.ActorAgent}
	}
	switch rec.Origin {
	case "", session.OriginConnector:
		return triggers.Actor{Kind: triggers.ActorOwner}
	case session.OriginTrigger:
		return triggers.Actor{Kind: triggers.ActorAgent, TriggerID: rec.OriginID}
	}
	return triggers.Actor{Kind: triggers.ActorAgent}
}

// Emit hands an event of an add-on to the owner's triggers: each watch that
// takes it reconciles its reminder with no model, or starts its model run for
// a thing it has no reminder for; each plain event trigger that matches runs
// as for the hook. A paused watch still moves and cancels the reminders it
// holds, so they are right when it is back, but starts no run. A run of a
// watch or of one of its reminders never wakes that watch. The event reaches
// only the owner's own triggers.
func (h *Triggers) Emit(ctx context.Context, in EmitInput) error {
	if in.Owner == "" || in.Addon == "" {
		return fmt.Errorf("%w: no owner", ErrEventNotDeclared)
	}
	if len(in.Payload) > contract.MaxEventPayload {
		return fmt.Errorf("%w: the payload is too large", ErrEventNotDeclared)
	}
	th, err := ThingOf(in.Addon, in.Decl, in.Payload)
	if err != nil {
		return err
	}
	dedupe := in.DedupeKey
	if dedupe == "" {
		sum := sha256.Sum256(append([]byte(in.Decl.Type+"\x00"), in.Payload...))
		dedupe = hex.EncodeToString(sum[:12])
	}
	actor := h.ActorOf(ctx, in.Owner, in.ChatID, in.CredentialID)
	ev := triggers.Event{Source: in.Addon, Type: in.Decl.Type, DedupeKey: in.Addon + ":" + dedupe, Payload: in.Payload, Actor: actor, Thing: th}
	list, err := h.o.Store.ListTriggers(ctx, in.Owner)
	if err != nil {
		return err
	}
	parents := map[string]string{}
	for _, t := range list {
		if t.IsReminder() {
			parents[t.ID] = t.Reminder.ParentID
		}
	}
	parentOf := func(id string) string { return parents[id] }
	var errs []error
	for _, t := range list {
		if t.Kind != triggers.KindEvent || t.IsChange() || !t.MatchesEvent(ev) {
			continue
		}
		if !t.Enabled && (!t.IsWatch() || t.Proposed) {
			continue
		}
		if triggers.Guarded(t, actor, parentOf) {
			continue
		}
		if t.IsWatch() {
			if err := h.watchEvent(ctx, t, ev); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		if _, err := h.PublishRun(ctx, t.ID, ev); err != nil && !errors.Is(err, triggers.ErrNoMatch) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// watchEvent applies one event to a watch.
func (h *Triggers) watchEvent(ctx context.Context, w triggers.Trigger, ev triggers.Event) error {
	ctx = context.WithoutCancel(ctx)
	key := triggers.EventPrefix + ev.DedupeKey
	if _, err := h.o.Store.RunByKey(ctx, w.ID, key); err == nil {
		return nil // the same event again
	} else if !errors.Is(err, triggers.ErrNotFound) {
		return err
	}
	now := h.o.Clock()
	if !w.Enabled {
		// The limit on changes may be what paused the watch: a source that
		// sends changes in a loop must not keep a paused watch busy either.
		if n, err := h.o.Store.CountHandledSince(ctx, w.ID, now.Add(-time.Minute)); err != nil || n > triggers.MaxChangesPerMinute {
			return err
		}
	}
	// The write lock keeps an owner's change on the watch page and this
	// event from overwriting each other's reminder on this frontend.
	h.wmu.Lock()
	out, err := triggers.Reconcile(ctx, h.o.Store, w, ev, now)
	h.wmu.Unlock()
	if err != nil {
		return err
	}
	if !w.Enabled {
		// A paused watch keeps its reminders in step with their items but
		// starts no run: a new item waits for no decision, and a change
		// beyond the time does not wake a reminder that is on hold.
		switch {
		case out.Action == triggers.ActionModel:
			return nil
		case out.Action == triggers.ActionWoken && out.Moved:
			out.Action = triggers.ActionRetimed
		case out.Action == triggers.ActionWoken:
			out.Action = triggers.ActionUnchanged
		}
	}
	switch out.Action {
	case triggers.ActionModel:
		if th := ev.Thing; th != nil && th.Lifecycle == triggers.LifecycleUpdated && len(th.Following) == 0 &&
			((th.Anchor.IsZero() && th.Timed) || (!th.Anchor.IsZero() && now.Sub(th.Anchor) > 24*time.Hour)) {
			// A change of an item that is over (no time left, or one that
			// began over a day ago) whose spent reminder the pass removed:
			// there is nothing to remind of.
			return nil
		}
		_, err := h.PublishRun(ctx, w.ID, watchRunEvent(ev))
		return err
	case triggers.ActionNone, triggers.ActionUnchanged:
		return nil
	}
	if out.Action == triggers.ActionCancelled {
		if run := h.currentRunner(); run != nil {
			_ = run.Cancel(ctx, out.Before.ID) // a run of the gone reminder that waits or goes on
		}
	}
	h.recordWatch(ctx, w, key, now, out, ev.Actor)
	if out.Action == triggers.ActionWoken {
		wake := ev
		wake.Payload = watchRunPayload(ev)
		if _, err := h.publish(ctx, out.After.ID, wake, true); err != nil {
			h.log.Warn("studio: could not wake a reminder early", "reminder", out.After.ID, "error", err)
		}
	}
	h.limitChanges(ctx, w, now)
	return nil
}

// recordWatch writes what the watch did as one row of its history and one
// line in the Inbox. A change that someone else made is also said on the
// watch's channel (decision C); the owner's own change is not.
func (h *Triggers) recordWatch(ctx context.Context, w triggers.Trigger, key string, now time.Time, out triggers.Outcome, actor triggers.Actor) {
	line, message := watchSays(w, out, now)
	// A paused watch says nothing on its channel; its history and the Inbox
	// still get the line.
	loud := !actor.ByOwner() && w.Enabled
	row := triggers.Run{ID: newID(), TriggerID: w.ID, DueKey: key, Due: now.UTC(), Status: triggers.StatusHandled,
		Reason: triggers.CutReason(line.Text), Ended: now.UTC()}
	claimed, err := h.o.Store.Claim(ctx, row)
	if err != nil {
		h.log.Warn("studio: could not record a watch action", "watch", w.ID, "error", err)
	} else if !claimed {
		return // another frontend handled the same event and told the owner
	}
	switch {
	case !loud:
		h.watchNote(ctx, w, line)
	case w.Route.TargetOrDefault() == triggers.TargetInbox:
		// The Inbox is the watch's channel: the one line is the message.
		h.watchNote(ctx, w, message)
	default:
		h.watchNote(ctx, w, line)
		h.sayOnChannel(ctx, w, message)
	}
}

// watchText is one line that a watch writes for the owner: Text is the
// English line, kept in the watch's history and as the Inbox fallback, and
// Code ("watch.<what>") with Params (title, and at and zone for a time) lets
// the interface word it in the owner's language. Key and Args, when set, are
// the same sentence as a key of the host message catalog, for a message on a
// channel of a messaging app.
type watchText struct {
	Code   string
	Params map[string]any
	Text   string
	Key    string
	Args   []any
}

// watchSay makes a line of the watch w about the item title; at is the time
// it names, zero for none.
func watchSay(w triggers.Trigger, code, title string, at time.Time, text string) watchText {
	p := map[string]any{"title": title}
	if !at.IsZero() {
		p["at"] = at.UTC().Format(time.RFC3339)
		if w.TZ != "" {
			p["zone"] = w.TZ
		}
	}
	return watchText{Code: "watch." + code, Params: p, Text: text}
}

// saying adds to t the key of the host message catalog that says it in a
// chat, with its arguments.
func (t watchText) saying(key string, args ...any) watchText {
	t.Key, t.Args = key, args
	return t
}

// watchNote adds one Inbox line under the watch: its name is the title, so the
// lines of one watch group together, and the link opens the watch.
func (h *Triggers) watchNote(ctx context.Context, w triggers.Trigger, line watchText) {
	if h.svc.d.Notify == nil {
		return
	}
	n := notify.Notification{Principal: w.Owner, Kind: notify.KindRunEnded, Status: notify.StatusOK, Source: notify.SourceTrigger,
		Title: w.Name, Body: line.Text, Link: "/app/triggers/" + url.PathEscape(w.ID), Code: line.Code, Params: line.Params}
	if _, err := h.svc.d.Notify.Add(ctx, n); err != nil {
		h.log.Warn("studio: could not add a notification", "error", err)
	}
}

// sayOnChannel sends a short message on the channel a watch reports to, in
// the language of that chat. A watch that reports to the Inbox has its line
// there already.
func (h *Triggers) sayOnChannel(ctx context.Context, w triggers.Trigger, m watchText) {
	if w.Route.TargetOrDefault() == triggers.TargetInbox || m.Key == "" {
		return
	}
	always := w
	always.Route.Mode = triggers.ModeAlways
	h.report(ctx, always, triggers.Run{}, h.voiceOf(ctx, w).T(m.Key, m.Args...))
}

// whenText writes a time for the owner, in the watch's zone: "Thu 15:45", or
// with the date when it is more than six days away.
func whenText(w triggers.Trigger, t, now time.Time) string {
	loc := w.LocationOrUTC()
	t, now = t.In(loc), now.In(loc)
	if t.Sub(now) > 6*24*time.Hour || t.Before(now.Add(-24*time.Hour)) {
		return t.Format("Mon 2 Jan 15:04")
	}
	return t.Format("Mon 15:04")
}

// dueText is the due time of a reminder as the owner reads it, or "" when it
// sets none.
func dueText(w, r triggers.Trigger, now time.Time) string {
	text, _ := dueOf(w, r, now)
	return text
}

// dueOf is dueText with the due time itself, zero when there is none.
func dueOf(w, r triggers.Trigger, now time.Time) (string, time.Time) {
	if r.Reminder == nil {
		return "", time.Time{}
	}
	tm := triggers.PlanFor(w, *r.Reminder, now)
	if tm.None || tm.Skip || tm.Due.IsZero() {
		return "", time.Time{}
	}
	return whenText(w, tm.Due, now), tm.Due
}

// watchSays gives what the watch says of an action of the reconciler: the
// line of its history and the Inbox, and the short message for a change that
// someone else made. An action that says nothing gives two zero lines.
func watchSays(w triggers.Trigger, out triggers.Outcome, now time.Time) (line, message watchText) {
	title := thingTitle(out)
	quoted := strconv.Quote(title)
	switch out.Action {
	case triggers.ActionRetimed:
		if due, at := dueOf(w, out.After, now); due != "" {
			return watchSay(w, "moved", title, at, fmt.Sprintf("Moved the reminder for %s to %s.", quoted, due)),
				watchSay(w, "movedBy", title, at, fmt.Sprintf("%s was moved. I'll remind you at %s.", quoted, due)).
					saying("watch.moved_by", "title", quoted, "when", due)
		}
		return watchSay(w, "movedStarted", title, time.Time{}, fmt.Sprintf("%s moved; it has already started, so there is no reminder.", quoted)),
			watchSay(w, "movedByStarted", title, time.Time{}, fmt.Sprintf("%s was moved and has already started.", quoted)).
				saying("watch.moved_by_started", "title", quoted)
	case triggers.ActionCancelled:
		return watchSay(w, "removed", title, time.Time{}, fmt.Sprintf("Cancelled the reminder for %s: it was removed.", quoted)),
			watchSay(w, "removedBy", title, time.Time{}, fmt.Sprintf("%s was removed, so I cancelled its reminder.", quoted)).
				saying("watch.removed_by", "title", quoted)
	case triggers.ActionWoken:
		return watchSay(w, "woken", title, time.Time{}, fmt.Sprintf("%s changed. Checking its reminder now.", quoted)),
			watchSay(w, "wokenBy", title, time.Time{}, fmt.Sprintf("%s changed. I'm checking whether its reminder still fits.", quoted)).
				saying("watch.woken_by", "title", quoted)
	}
	return watchText{}, watchText{}
}

func thingTitle(out triggers.Outcome) string {
	for _, t := range []triggers.Trigger{out.After, out.Before} {
		if t.Reminder != nil && t.Reminder.Title != "" {
			return t.Reminder.Title
		}
		if t.Name != "" {
			return t.Name
		}
	}
	return "an item"
}

// limitChanges pauses a watch that changed more than MaxChangesPerMinute
// reminders in the last minute, and tells the owner: a source that sends
// changes in a loop must not keep the host busy.
func (h *Triggers) limitChanges(ctx context.Context, w triggers.Trigger, now time.Time) {
	n, err := h.o.Store.CountHandledSince(ctx, w.ID, now.Add(-time.Minute))
	if err != nil || n <= triggers.MaxChangesPerMinute {
		return
	}
	h.switchOff(w, fmt.Sprintf("it changed more than %d reminders in one minute", triggers.MaxChangesPerMinute))
}

// tellAdvanced is Options.Advanced: one line in the watch's history and the
// Inbox when the reminder of a repeating item moved to its next occurrence,
// and when the occurrences the add-on gave run out, so a series never stops
// without a word (the failure rules of the spec).
func (h *Triggers) tellAdvanced(ctx context.Context, t triggers.Trigger, moved, last bool) {
	w, err := h.o.Store.GetTrigger(ctx, t.Reminder.ParentID)
	if err != nil {
		return
	}
	now := h.o.Clock()
	title := t.Reminder.Title
	due, at := dueOf(w, t, now)
	var line watchText
	switch {
	case !moved:
		line = watchSay(w, "seriesEnded", title, time.Time{},
			fmt.Sprintf("No later time of %q is known, so it has no reminder now. A change of the item sets the next one.", title))
	case due == "":
		return
	case last:
		line = watchSay(w, "lastKnown", title, at,
			fmt.Sprintf("Set the reminder for the next %q: %s. It is the last time known; a change of the item sets the ones after it.", title, due))
	default:
		line = watchSay(w, "next", title, at, fmt.Sprintf("Set the reminder for the next %q: %s.", title, due))
	}
	h.recordAction(ctx, w, now, line)
	h.Notify()
}

// tellMissed is Options.Missed: one Inbox line under the watch for a reminder
// whose thing began while the server was down. No late reminder is sent.
func (h *Triggers) tellMissed(ctx context.Context, t triggers.Trigger, _ triggers.Run) {
	if t.Reminder == nil {
		return
	}
	w, err := h.o.Store.GetTrigger(ctx, t.Reminder.ParentID)
	if err != nil {
		return
	}
	title := t.Reminder.Title
	if title == "" {
		title = t.Name
	}
	h.watchNote(ctx, w, watchSay(w, "missed", title, time.Time{}, "Missed: "+title+". The server was down at the reminder time."))
}

// watchRunEvent is the event a watch's model run gets: the host's facts
// about the thing and who changed it, with the add-on's payload inside as
// data.
func watchRunEvent(ev triggers.Event) triggers.Event {
	ev.Payload = watchRunPayload(ev)
	return ev
}

// watchFacts is the envelope of the payload of a watch run. The host writes
// the outer fields; Data is the add-on's payload, which stays untrusted.
type watchFacts struct {
	Watch     bool            `json:"localai_watch"`
	Lifecycle string          `json:"lifecycle,omitempty"`
	Key       string          `json:"key,omitempty"`
	Title     string          `json:"title,omitempty"`
	At        string          `json:"at,omitempty"`
	Zone      string          `json:"zone,omitempty"`
	AllDay    bool            `json:"all_day,omitempty"`
	Stamp     string          `json:"stamp,omitempty"`
	Following []time.Time     `json:"following,omitempty"`
	By        string          `json:"by,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

func watchRunPayload(ev triggers.Event) []byte {
	f := watchFacts{Watch: true, By: actorWords(ev.Actor)}
	if th := ev.Thing; th != nil {
		f.Lifecycle, f.Key, f.Title, f.Zone, f.AllDay, f.Stamp = th.Lifecycle, th.Key, th.Title, th.Zone, th.AllDay, th.Stamp
		f.Following = th.Following
		if !th.Anchor.IsZero() {
			f.At = th.Anchor.Format(time.RFC3339)
		}
	}
	if json.Valid(ev.Payload) {
		f.Data = ev.Payload
	}
	raw, _ := json.Marshal(f)
	return raw
}

// actorWords says who made a change, in the words of the run's prompt.
func actorWords(a triggers.Actor) string {
	switch a.Kind {
	case triggers.ActorOwner:
		return "the owner"
	case triggers.ActorClient:
		return "a paired app of the owner"
	case triggers.ActorImport:
		return "an import"
	case triggers.ActorAgent:
		return "an agent"
	}
	return "someone else"
}
