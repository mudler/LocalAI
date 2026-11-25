package studio

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

// stampWithout is a short fingerprint of the payload with the given paths
// taken out, so that two payloads that differ only in the time of their thing
// have the same stamp.
func stampWithout(doc any, drop []string) string {
	for _, p := range drop {
		if p == "" {
			continue
		}
		segs := strings.Split(p, ".")
		cur := doc
		for i, seg := range segs {
			m, ok := cur.(map[string]any)
			if !ok {
				break
			}
			if i == len(segs)-1 {
				delete(m, seg)
				break
			}
			cur = m[seg]
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
// as for the hook. A run of a watch or of one of its reminders never wakes
// that watch. The event reaches only the owner's own triggers.
func (h *Triggers) Emit(ctx context.Context, in EmitInput) error {
	if in.Owner == "" || in.Addon == "" {
		return fmt.Errorf("%w: no owner", ErrEventNotDeclared)
	}
	if len(in.Payload) > triggers.MaxEventBody {
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
		if t.Kind != triggers.KindEvent || !t.Enabled || t.IsChange() || !t.MatchesEvent(ev) {
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
	out, err := triggers.Reconcile(ctx, h.o.Store, w, ev, now)
	if err != nil {
		return err
	}
	switch out.Action {
	case triggers.ActionModel:
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
	line, loud := h.watchLine(w, out, now), !actor.ByOwner()
	row := triggers.Run{ID: newRunID(), TriggerID: w.ID, DueKey: key, Due: now.UTC(), Status: triggers.StatusHandled,
		Reason: triggers.CutReason(line), Ended: now.UTC()}
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
		h.watchNote(ctx, w, h.watchMessage(w, out, now))
	default:
		h.watchNote(ctx, w, line)
		h.sayOnChannel(ctx, w, h.watchMessage(w, out, now))
	}
}

// watchNote adds one Inbox line under the watch: its name is the title, so the
// lines of one watch group together, and the link opens the watch.
func (h *Triggers) watchNote(ctx context.Context, w triggers.Trigger, line string) {
	if h.svc.d.Notify == nil {
		return
	}
	n := notify.Notification{Principal: w.Owner, Kind: notify.KindRunEnded, Status: notify.StatusOK, Source: notify.SourceTrigger,
		Title: w.Name, Body: line, Link: "/app/triggers/" + url.PathEscape(w.ID)}
	if _, err := h.svc.d.Notify.Add(ctx, n); err != nil {
		h.log.Warn("studio: could not add a notification", "error", err)
	}
}

// sayOnChannel sends a short message on the channel a watch reports to. A
// watch that reports to the Inbox has its line there already.
func (h *Triggers) sayOnChannel(ctx context.Context, w triggers.Trigger, text string) {
	if w.Route.TargetOrDefault() == triggers.TargetInbox || text == "" {
		return
	}
	always := w
	always.Route.Mode = triggers.ModeAlways
	h.report(ctx, always, triggers.Run{}, text)
}

// whenText writes a time for the owner, in the watch's zone: "Thu 15:45", or
// with the date when it is more than six days away.
func whenText(w triggers.Trigger, t, now time.Time) string {
	loc, err := w.Location()
	if err != nil {
		loc = time.UTC
	}
	t, now = t.In(loc), now.In(loc)
	if t.Sub(now) > 6*24*time.Hour || t.Before(now.Add(-24*time.Hour)) {
		return t.Format("Mon 2 Jan 15:04")
	}
	return t.Format("Mon 15:04")
}

// dueText is the due time of a reminder as the owner reads it, or "" when it
// sets none.
func (h *Triggers) dueText(w, r triggers.Trigger, now time.Time) string {
	if r.Reminder == nil {
		return ""
	}
	loc, err := w.Location()
	if err != nil {
		loc = time.UTC
	}
	tm := triggers.PlanReminder(*r.Reminder, w.Watch, loc, now)
	if tm.None || tm.Skip || tm.Due.IsZero() {
		return ""
	}
	return whenText(w, tm.Due, now)
}

// watchLine is the history and Inbox line of an action of the reconciler.
func (h *Triggers) watchLine(w triggers.Trigger, out triggers.Outcome, now time.Time) string {
	title := thingTitle(out)
	switch out.Action {
	case triggers.ActionRetimed:
		if due := h.dueText(w, out.After, now); due != "" {
			return fmt.Sprintf("Moved the reminder for %q to %s.", title, due)
		}
		return fmt.Sprintf("%q moved; it has already started, so there is no reminder.", title)
	case triggers.ActionCancelled:
		return fmt.Sprintf("Cancelled the reminder for %q: it was removed.", title)
	case triggers.ActionWoken:
		return fmt.Sprintf("%q changed. Checking its reminder now.", title)
	}
	return ""
}

// watchMessage is the short message for a change that someone else made.
func (h *Triggers) watchMessage(w triggers.Trigger, out triggers.Outcome, now time.Time) string {
	title := thingTitle(out)
	switch out.Action {
	case triggers.ActionRetimed:
		if due := h.dueText(w, out.After, now); due != "" {
			return fmt.Sprintf("%q was moved. I'll remind you at %s.", title, due)
		}
		return fmt.Sprintf("%q was moved and has already started.", title)
	case triggers.ActionCancelled:
		return fmt.Sprintf("%q was removed, so I cancelled its reminder.", title)
	case triggers.ActionWoken:
		return fmt.Sprintf("%q changed. I'm checking whether its reminder still fits.", title)
	}
	return ""
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
	n, err := triggers.CountChangesSince(ctx, h.o.Store, w.ID, now.Add(-time.Minute), triggers.MaxChangesPerMinute)
	if err != nil || n <= triggers.MaxChangesPerMinute {
		return
	}
	h.switchOff(w, fmt.Sprintf("it changed more than %d reminders in one minute", triggers.MaxChangesPerMinute))
}

// tellMissed is Options.Missed: one Inbox line under the watch for a reminder
// whose thing began while the server was down. No late reminder is sent.
func (h *Triggers) tellMissed(t triggers.Trigger, _ triggers.Run) {
	ctx := context.Background()
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
	h.watchNote(ctx, w, "Missed: "+title+". The server was down at the reminder time.")
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
	By        string          `json:"by,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

func watchRunPayload(ev triggers.Event) []byte {
	f := watchFacts{Watch: true, By: actorWords(ev.Actor)}
	if th := ev.Thing; th != nil {
		f.Lifecycle, f.Key, f.Title, f.Zone, f.AllDay, f.Stamp = th.Lifecycle, th.Key, th.Title, th.Zone, th.AllDay, th.Stamp
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

func newRunID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
