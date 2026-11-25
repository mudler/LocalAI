package schema

import "time"

// ChatsWatchRule is the reminder rule of a watch: an event trigger of an
// add-on that keeps reminders for the items it reports.
type ChatsWatchRule struct {
	// OffsetMinutes is the default reminder time relative to the item's time:
	// -15 is 15 minutes before. NoDefault sets no reminder unless the task
	// defaults call for one.
	OffsetMinutes int  `json:"offset_minutes"`
	NoDefault     bool `json:"no_default,omitempty"`
	// AllDay is morning (the default: 08:00 on the day) or none.
	AllDay string `json:"all_day,omitempty"`
	// QuietFrom and QuietTo are quiet hours (HH:MM, in the watch's time zone).
	QuietFrom string `json:"quiet_from,omitempty"`
	QuietTo   string `json:"quiet_to,omitempty"`
	// Tasks are the task defaults in the owner's words.
	Tasks string `json:"tasks,omitempty"`
	// Types are the event types the watch takes; empty is every type of the
	// source.
	Types []string `json:"types,omitempty"`
}

// ChatsWatchEventType is one event type an add-on reports.
type ChatsWatchEventType struct {
	Type      string `json:"type"`
	Lifecycle string `json:"lifecycle"`
	// Label is the name of the event in the language of the request.
	Label string `json:"label"`
}

// ChatsWatchSource is an installed add-on whose events a watch can take.
type ChatsWatchSource struct {
	ID     string                `json:"id"`
	Title  string                `json:"title"`
	Events []ChatsWatchEventType `json:"events"`
}

// ChatsWatchSources lists the sources; empty when no installed add-on
// reports changes.
type ChatsWatchSources struct {
	Sources []ChatsWatchSource `json:"sources"`
}

// ChatsReminderChange is a change of a reminder that a chat proposed: the
// watch, the item and the reminder before and after the change. before is
// absent for a new reminder, after for a cancel.
type ChatsReminderChange struct {
	WatchID   string         `json:"watch_id"`
	WatchName string         `json:"watch_name,omitempty"`
	WatchZone string         `json:"watch_zone,omitempty"`
	Title     string         `json:"title"`
	Before    *ChatsReminder `json:"before,omitempty"`
	After     *ChatsReminder `json:"after,omitempty"`
}

// ChatsReminder is one reminder of a watch.
type ChatsReminder struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Link opens the item in its add-on.
	Link string `json:"link,omitempty"`
	Task string `json:"task"`
	// Due is when the owner hears; Start is when the run starts (Due minus
	// the lead). Both are absent when the item has begun.
	Due   *time.Time `json:"due,omitempty"`
	Start *time.Time `json:"start,omitempty"`
	// ItemTime is the time of the item, absent for an item with no time.
	ItemTime      *time.Time `json:"item_time,omitempty"`
	AllDay        bool       `json:"all_day,omitempty"`
	OffsetMinutes int        `json:"offset_minutes"`
	LeadMinutes   int        `json:"lead_minutes,omitempty"`
	// At is a time the reminder was set to, SnoozedTo a time the owner
	// snoozed it to.
	At        *time.Time `json:"at,omitempty"`
	SnoozedTo *time.Time `json:"snoozed_to,omitempty"`
	// Skipped says the owner skipped the coming time. Adjusted is quiet or
	// all_day when the rule moved the time.
	Skipped   bool   `json:"skipped,omitempty"`
	Adjusted  string `json:"adjusted,omitempty"`
	Snoozable bool   `json:"snoozable,omitempty"`
}

// ChatsSourceReminder is a reminder of one of your watches for an item of an
// add-on: the id of the item in that add-on and the watch it belongs to.
type ChatsSourceReminder struct {
	ItemID  string `json:"item_id"`
	WatchID string `json:"watch_id"`
	ChatsReminder
}

// ChatsSourceReminders lists the reminders for the items of one add-on.
type ChatsSourceReminders struct {
	Reminders []ChatsSourceReminder `json:"reminders"`
}

// ChatsReminderList lists the reminders of a watch, soonest first.
type ChatsReminderList struct {
	Reminders []ChatsReminder `json:"reminders"`
}

// ChatsReminderPatch changes one reminder. Each field that is set applies:
// snooze_minutes moves it to now plus that much; snoozed_to sets or (empty)
// clears the snooze; skip skips or un-skips the coming time; at (a time) or
// offset_minutes re-time it; task changes what the run does.
type ChatsReminderPatch struct {
	SnoozeMinutes int     `json:"snooze_minutes,omitempty"`
	SnoozedTo     *string `json:"snoozed_to,omitempty"`
	Skip          *bool   `json:"skip,omitempty"`
	At            string  `json:"at,omitempty"`
	OffsetMinutes *int    `json:"offset_minutes,omitempty"`
	Task          *string `json:"task,omitempty"`
}
