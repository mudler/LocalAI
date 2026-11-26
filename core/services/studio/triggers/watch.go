package triggers

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// KindReminder is a one-off trigger that a watch owns and that is tied to one
// thing of the watch's source, such as one calendar event. It runs once at its
// due time, or earlier when the thing changes (see Reminder).
const KindReminder Kind = "reminder"

// StatusHandled is the state of a row that records what a watch did with an
// event without a model run: a reminder moved, cancelled or held. It is final,
// never started, and the daily limit does not count it.
const StatusHandled Status = "handled"

// Bounds of a watch and its reminders.
const (
	// MaxWatchTasks is the longest plain-words task defaults of a watch, in
	// characters.
	MaxWatchTasks = 1000
	// MaxReminderTask is the longest task of a reminder, in characters.
	MaxReminderTask = 500
	// MaxReminderOffset bounds the offset and the lead of a reminder.
	MaxReminderOffset = 30 * 24 * time.Hour
	// MaxThingKey is the longest key of a thing, in bytes.
	MaxThingKey = 255
	// MaxThingTitle is the longest title of a thing kept on a reminder.
	MaxThingTitle = 200
	// MaxChangesPerMinute is how many reminder changes one watch may make
	// in a minute before it is paused.
	MaxChangesPerMinute = 30
	// maxTriggerID is the longest ID of a trigger, the size of its column in
	// the database store: a reminder's watch is never longer.
	maxTriggerID = 128
)

// The all-day rules of a watch: what a reminder for a thing that lasts whole
// days does. Morning reminds at 08:00 of the thing's zone on that day (the
// owner's zone for a thing with no zone); none sets no reminder.
const (
	AllDayMorning = "morning"
	AllDayNone    = "none"
)

// WatchRule is the reminder rule of a watch: an event trigger with a rule is a
// watch. The host reads it when a thing is new and when a reminder is timed.
type WatchRule struct {
	// Offset is the default time from the thing's time to the reminder, as a
	// signed duration: -15m is 15 minutes before. NoDefault says that the
	// watch sets no reminder unless its agent decides on one.
	Offset    time.Duration `json:",omitempty"`
	NoDefault bool          `json:",omitempty"`
	// AllDay is AllDayMorning (the default) or AllDayNone.
	AllDay string `json:",omitempty"`
	// QuietFrom and QuietTo are the quiet hours of the owner, HH:MM in the
	// watch's TZ. A reminder due inside them moves to QuietFrom of the evening
	// before. Both empty: no quiet hours.
	QuietFrom string `json:",omitempty"`
	QuietTo   string `json:",omitempty"`
	// Tasks are the task defaults in the owner's own words, such as "for
	// calls with other people, prepare a brief 30 min ahead".
	Tasks string `json:",omitempty"`
	// Types are the event types of the source that the watch takes, such as
	// calendar.event.created. Empty: every type the source declares.
	Types []string `json:",omitempty"`
}

var clockRE = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// Validate checks the rule.
func (w WatchRule) Validate() error {
	switch {
	case w.Offset < -MaxReminderOffset || w.Offset > MaxReminderOffset:
		return fmt.Errorf("the reminder time must be within %s of the event", ShortDuration(MaxReminderOffset))
	case w.AllDay != "" && w.AllDay != AllDayMorning && w.AllDay != AllDayNone:
		return fmt.Errorf("the all-day rule must be %q or %q", AllDayMorning, AllDayNone)
	case (w.QuietFrom == "") != (w.QuietTo == ""):
		return errors.New("quiet hours need a start and an end")
	case w.QuietFrom != "" && (!clockRE.MatchString(w.QuietFrom) || !clockRE.MatchString(w.QuietTo)):
		return errors.New("quiet hours are times such as 22:00")
	case utf8.RuneCountInString(w.Tasks) > MaxWatchTasks || hasControl(w.Tasks, true):
		return fmt.Errorf("the task defaults are text of at most %d characters", MaxWatchTasks)
	case len(w.Types) > MaxMatches:
		return fmt.Errorf("a watch takes at most %d event types", MaxMatches)
	}
	for _, ty := range w.Types {
		if ty == "" || len(ty) > MaxEventField || hasControl(ty, false) {
			return fmt.Errorf("an event type is one line of at most %d bytes", MaxEventField)
		}
	}
	return nil
}

// AllDayOrDefault gives AllDay, or AllDayMorning when it is empty.
func (w WatchRule) AllDayOrDefault() string {
	if w.AllDay == "" {
		return AllDayMorning
	}
	return w.AllDay
}

// Reminder holds the fields of a reminder trigger.
type Reminder struct {
	// ParentID is the watch that owns the reminder.
	ParentID string
	// Key is "<add-on>:<id>" of the thing. A watch has one reminder per key.
	Key string
	// Title and Link name the thing to the owner, as the last event of the
	// thing gave them. Link is a route of the add-on's page.
	Title string `json:",omitempty"`
	Link  string `json:",omitempty"`
	// Anchor is the time of the thing (zero: the thing has no time), Zone its
	// IANA zone and AllDay that it lasts whole days.
	Anchor time.Time `json:",omitzero"`
	Zone   string    `json:",omitempty"`
	AllDay bool      `json:",omitempty"`
	// Offset is the due time relative to Anchor; At is an absolute due time
	// and wins over Offset. A thing with no anchor needs At.
	Offset time.Duration `json:",omitempty"`
	At     time.Time     `json:",omitzero"`
	// Task is what the run does, in plain words ("remind me", "prepare a
	// brief"). Lead is how long before the due time the run starts, so its
	// result arrives at the due time.
	Task string        `json:",omitempty"`
	Lead time.Duration `json:",omitempty"`
	// WakeOnChange starts an early run when the thing changes in more than
	// its time.
	WakeOnChange bool `json:",omitempty"`
	// Stamp is a fingerprint of the thing without its time: a different stamp
	// on an update is a change beyond a move.
	Stamp string `json:",omitempty"`
	// Adjusted says why the due time differs from the rule: AdjustedQuiet or
	// AdjustedAllDay, a code that the host words for the owner. Empty: the
	// rule as it is.
	Adjusted string `json:",omitempty"`
	// SnoozedTo, when set, is a due time the owner chose that wins over the
	// computed one until the thing moves.
	SnoozedTo time.Time `json:",omitzero"`
	// Skipped is a due time the owner skipped: the reminder does not fire at
	// it. A new due time (the thing moved, a snooze) fires again.
	Skipped time.Time `json:",omitzero"`
	// SetBy is the ID of the run that last set or changed the reminder with
	// set_reminder. The heads-up of that run offers buttons for this
	// reminder, and a reply to the heads-up ("earlier", "no reminder") finds
	// it by this ID, whatever changed the reminder since.
	SetBy string `json:",omitempty"`
	// Following are the starts of the later occurrences of the thing, as its
	// add-on last gave them, soonest first. When the thing's time has passed,
	// the runner moves the reminder to the next of them (see Advance).
	Following []time.Time `json:",omitempty"`
	// Repeats says that the add-on gave later occurrences of the thing, so
	// the runner says so when it has used them all up (see Advance).
	Repeats bool `json:",omitempty"`
	// Rev is the version of the stored reminder. The store sets it, and
	// PutTrigger refuses with ErrConflict a reminder whose Rev is not the
	// stored one: two writers (two frontends, the reconciler and the
	// owner) that read the same version cannot both write, so neither undoes
	// the other's change.
	Rev int64 `json:",omitempty"`
}

// Validate checks a reminder's own fields.
func (r Reminder) Validate() error {
	switch {
	case r.ParentID == "" || len(r.ParentID) > maxTriggerID:
		return errors.New("a reminder belongs to a watch")
	case r.Key == "" || len(r.Key) > MaxThingKey || hasControl(r.Key, false) || !strings.Contains(r.Key, ":"):
		return errors.New("a reminder names the thing it is for")
	case utf8.RuneCountInString(r.Title) > MaxThingTitle || hasControl(r.Title, false):
		return fmt.Errorf("the title of a reminder has at most %d characters", MaxThingTitle)
	case len(r.Link) > MaxPlanPath || hasControl(r.Link, false):
		return errors.New("the link of a reminder is too long")
	case utf8.RuneCountInString(r.Task) > MaxReminderTask || hasControl(r.Task, true):
		return fmt.Errorf("the task of a reminder has at most %d characters", MaxReminderTask)
	case r.Offset < -MaxReminderOffset || r.Offset > MaxReminderOffset:
		return fmt.Errorf("the reminder time must be within %s of the event", ShortDuration(MaxReminderOffset))
	case r.Lead < 0 || r.Lead > MaxReminderOffset:
		return fmt.Errorf("the lead time must be 0 to %s", ShortDuration(MaxReminderOffset))
	case r.Anchor.IsZero() && r.At.IsZero():
		return errors.New("a reminder for a thing with no time needs a time")
	}
	if r.Zone != "" {
		if _, err := time.LoadLocation(r.Zone); err != nil {
			return fmt.Errorf("time zone %q is not known", r.Zone)
		}
	}
	return nil
}

// The kinds of actor: who caused an event. The host sets them; an add-on
// cannot.
const (
	ActorOwner  = "owner"
	ActorAgent  = "agent"
	ActorClient = "client"
	ActorImport = "import"
)

// Actor is who caused an event: the owner (through the UI or a chat), an
// agent run (TriggerID names its trigger), a paired client (ClientID), or an
// import. The zero value is unknown and is treated like someone else.
type Actor struct {
	Kind      string `json:",omitempty"`
	TriggerID string `json:",omitempty"`
	ClientID  string `json:",omitempty"`
}

// ByOwner says whether the owner caused the event themselves.
func (a Actor) ByOwner() bool { return a.Kind == ActorOwner }

// Thing is what an event of an add-on is about, as the host read it with the
// add-on's declaration. The host never reads any other part of the payload.
type Thing struct {
	// Key is "<add-on>:<id>".
	Key string
	// Lifecycle is created, updated or deleted.
	Lifecycle string
	Title     string
	Link      string
	Anchor    time.Time
	Zone      string
	AllDay    bool
	// Stamp is a fingerprint of the payload without its time fields.
	Stamp string
	// Following are the starts of the later occurrences of a thing that
	// repeats, soonest first, all after Anchor.
	Following []time.Time
	// Timed says that the declaration of the event has a time: such a thing
	// with no Anchor has no time left (an event with no occurrence ahead),
	// unlike a thing that never has one.
	Timed bool
}

// The lifecycles of a Thing. They are the words of the add-on contract.
const (
	LifecycleCreated = "created"
	LifecycleUpdated = "updated"
	LifecycleDeleted = "deleted"
)

// IsWatch says whether t is a watch: an event trigger with a reminder rule.
func (t Trigger) IsWatch() bool { return t.Kind == KindEvent && t.Watch != nil }

// IsReminder says whether t is a reminder of a watch. A change record that
// holds a reminder a chat proposed is not one: it never fires and is not
// counted as the watch's reminder for its thing.
func (t Trigger) IsReminder() bool {
	return t.Kind == KindReminder && t.Reminder != nil && !t.IsChange()
}

// validateReminder checks a reminder trigger.
func (t Trigger) validateReminder() error {
	if t.Reminder == nil {
		return errors.New("a reminder needs its watch and its thing")
	}
	if t.Source != "" || t.EventType != "" || len(t.Matches) > 0 || t.Secret != "" || t.Watch != nil {
		return errors.New("a reminder has no event fields of its own")
	}
	return t.Reminder.Validate()
}
