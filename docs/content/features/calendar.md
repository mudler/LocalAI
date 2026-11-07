+++
disableToc = false
title = "Calendar"
weight = 25
url = "/features/calendar/"
+++

The Calendar add-on gives you a calendar inside LocalAI. Ask a chat or a [saved agent](/features/chats-agents/) "what is on Thursday?" or "add dinner with Sam at 8" and it reads or changes the calendar. There is no account to connect, no sync and no setup: install the add-on and use it.

## What it keeps

There is **one calendar for each user**, shared by all of that user's chats and agents. It is one SQLite file in the data directory of the add-on. An event has:

- a title, a start and an end, or a whole day (an all-day event);
- notes, a place and the attendees, each as free text;
- a time zone, as an IANA name such as `Europe/Rome`, and optionally a repeat.

A time is a local date and time with a zone name, the way a person says it. Set the **default time zone** once, on the Calendar page (it starts as the zone of the server). A new event uses it unless you or the agent name another one. An event keeps the zone it was written in when you change the default.

A repeat is daily, weekly, monthly or yearly, with an interval ("every 2 weeks") and an optional last day. The local time stays the same across a change of daylight saving time. A monthly repeat on the 31st skips months that have no 31st. You can change or delete the whole series, or skip one occurrence.

There is no sync with another calendar, no invitation, no second calendar and no reminder that pushes a message. You can export the whole calendar as one ICS file from the page and import it elsewhere.

## The page

Open **Calendar** in the add-ons. It has a month view, an agenda list of the next 60 days, a form to create or change an event, the default zone and the export button. Click a day to add an event, or an event to change, skip or delete it. The page speaks the language of the web UI.

## What the agents can do

| Tool | Does |
|---|---|
| `events-list` | Lists a range of days with repeats expanded; with no arguments, today and six more days. |
| `events-search` | Finds events by words in the title, notes, place or attendees. |
| `event-get` | Reads one event in full. |
| `event-create`, `event-update`, `event-delete` | Add, change and delete an event. With `occurrence` (a date), `event-delete` skips only that day of a repeating event. |

The reads run at once. The writes are action tools: they run at once, as the user, with no confirmation card, and an unattended run (a trigger or a job) needs the `act.addons` grant for them. The add-on ships a skill that tells the model when to use them.

In a messaging chat, the owner and the senders paired with the bot can read and change the calendar. Every other sender the bot lets in can read it but not change it (the `tools.write` rule, see [Add-ons that keep the owner's own data](/features/addons/#add-ons-that-keep-the-owners-own-data)).

## Limits

An event title has at most 200 characters, notes 4000, a place 200 and attendees 500. One user keeps at most 5000 events. A listing covers at most 400 days and returns at most 500 occurrences; the page says when a list was cut.
