+++
url = "/features/chats-watches/"
disableToc = false
title = "Watches and reminders"
description = "Get reminders for the things in your add-ons, such as calendar events, that follow the thing when it moves."
weight = 15
devbuild = "feature"
+++

A watch follows the changes of an add-on that keeps your own data, such as the Calendar add-on, and keeps a reminder for each thing in it. When you add an event, the watch sets a reminder. When the event moves, the reminder moves with it. When the event is removed, the reminder goes too. Watches are on the **Triggers** page.

## Make a watch

On the Triggers page, choose **New watch**. The form is the trigger form, with a **Watch** section instead of the schedule:

| Field | What it does |
|---|---|
| Watch | The add-on to watch. Only add-ons that report their changes are listed. With none installed, the section says how to get one. |
| Changes to take | Which changes start the watch: an event is added, changes, is removed. None ticked takes every change. |
| Remind me | The default reminder time: at the time of the thing, or some minutes, hours or days before. "No default reminder" sets one only when your task defaults call for it. |
| Things that last all day | A reminder at 08:00 on the day, or none. |
| Quiet hours | A start and an end, in the time zone of the watch. A reminder that falls inside moves to the start of the quiet hours, the evening before. |
| Task defaults | Plain words for what a reminder does, such as "for calls with other people, prepare a brief 30 min ahead". |

The rest of the form is the same as for a trigger: the agent and model, where the runs report (the Inbox or a channel of a messaging app), the daily limit and what a run may do. You approve a watch once. That covers every reminder it sets, moves and cancels later, within the permissions you gave it. You can change the permissions at any time with **Edit permissions** on the watch's page.

A chat can propose a watch too. The proposal card lists what the watch does and when it tells you, with **Approve**, **Edit** and **Not now**.

## What happens

- **A thing is added.** The watch's agent reads it once and sets a reminder, usually the default time. It may adjust the time for one thing (a call with other people gets a brief with a lead time, a thing in your quiet hours moves to the evening before) and says so. It sends you a message only when it adds something: someone else added the thing, it planned a task, or the thing clashes with another one. Otherwise you get one line in the Inbox.
- **A thing moves.** The reminder moves with it, with no model run. Your own change gives one Inbox line. A change by someone else gives a short message.
- **A thing changes in more than its time** (new attendees, say). The reminder's agent runs early, can change the task or the time, and the reminder still fires at its time.
- **A thing is removed.** The reminder is cancelled, with one Inbox line.
- **The reminder fires.** The run starts the lead time before the reminder time, reads the thing again, and does its task. If the task fails or runs out of time, you still get the plain reminder on time. For a repeating event, LocalAI then moves the reminder to the next occurrence by itself, with one Inbox line, also when the task failed. Occurrences that passed while the server was down are not sent. LocalAI knows the next 64 occurrences from the add-on and learns more with each change of the event; when it reaches the last one it knows, the Inbox says so.

The Inbox lines of a watch are in the language you chose for the interface. The short messages that LocalAI itself sends to a channel of a messaging app (a change by someone else, the plain reminder after a failed task) are in the language of that chat; what the agent writes is in the language of the conversation.

A reminder that has fired is removed a day after its thing began, and its history goes with it.

A change made by a watch's own run never wakes that watch. More than 30 reminder changes in a minute pause the watch and tell you.

## The watch's page

The page of a watch shows whether it is on, what it watches, where it reports and its upcoming reminders. Each reminder has **Snooze** (when it is due soon), **Skip**, **Change time…** and **Open**, which opens the thing in its add-on. Each action can be undone. The history lists every action, the quiet ones too. Pausing a watch holds its reminders; deleting it removes them. Both are listed in the Inbox. While a watch is paused, its reminders still move and are cancelled with their things, but no run starts and nothing is sent to its channel. When you resume it, a reminder whose time passed during the pause fires at once if its thing has not begun, without a note that the server was down.

The Calendar add-on marks an event that has a reminder with a bell and the reminder time, in the agenda and in the event's details. From the details you can skip the reminder or open the watch to change its time.

You can also change a reminder by asking in a chat in Studio, for example "remind me 30 minutes before the dentist instead" or "cancel the reminder for the standup". The agent proposes the change and the chat shows a card with the reminder before and after, with **Approve**, **Edit** (opens the watch's page) and **Not now**. Nothing changes until you approve. Once applied, the card has **Undo** for a day. A chat gets these reminder tools only when you have at least one watch, and not when the chat has the triggers switched off.

In a messaging app that shows buttons (Telegram), a reminder comes with **Snooze 5 min**, **In 30 min**, **Skip** and **Got it**. In other apps, reply "snooze", "30 min" or "skip" to the reminder.

## With several frontends

Watches and reminders are kept in the database that all frontends share, so every frontend shows and changes the same reminders. A reminder has a version: when two frontends (or the owner and a moved item) change the same reminder at once, the store keeps the first write and the other one reads the reminder again and applies its change on top. A run of a reminder is claimed once in the database, so it fires on one frontend only.

## When the server was down

A reminder whose time passed while the server was down fires late, with a note, when the thing has not begun yet. When the thing has begun, you get one Inbox line, "Missed: …", and no late reminder.

## API

| Method and path | What it does |
|---|---|
| `POST /api/chats/triggers` with `kind: "event"`, `source` and `watch` | Makes a watch. `watch` is `{offset_minutes, no_default, all_day, quiet_from, quiet_to, tasks, types}`. |
| `GET /api/chats/triggers/sources` | The add-ons a watch can watch, with their event types. |
| `GET /api/chats/triggers/{id}/reminders` | The reminders of a watch, soonest first. |
| `PATCH /api/chats/triggers/{id}/reminders/{rid}` | Snooze (`snooze_minutes`, or `snoozed_to`; empty takes it back), skip (`skip`), re-time (`at` or `offset_minutes`) or change the task (`task`). With `?source=<add-on>`, a reminder of a watch on another add-on is not found: an add-on page skips a reminder with `sdk.reminders.skip(watchId, id)`, which always sends its own ID. This keeps a well-behaved add-on page to its own items; it is not a security boundary, since an add-on page runs in the same session as the rest of the interface. |
| `DELETE /api/chats/triggers/{id}/reminders/{rid}` | Cancels a reminder. |
| `GET /api/chats/triggers/reminders?source=<add-on>` | The reminders of your watches for the items of one add-on, with the item ID and the watch. `source` is required. An add-on page reads them with `sdk.reminders.list()`: each has `id`, `itemId`, `watchId`, `due`, `itemTime` (the time of the item the reminder is for, which tells one occurrence of a repeating item from the others) and `skipped`. |

For add-on authors: an add-on declares its event types and reports its changes with the `events.emit` host operation. See [Events]({{% relref "/addons/contract-reference/host-operations#events" %}}).
