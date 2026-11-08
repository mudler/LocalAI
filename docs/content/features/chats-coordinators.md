+++
disableToc = false
title = "Chats: coordinator agents"
weight = 25
url = "/features/chats-coordinators/"
+++

A coordinator is a saved agent that keeps working on its own. You write what it
does: the instructions, the standing topics and the rules. It wakes on a
schedule and when something happens, does a slice of work, writes down its state
and sleeps. It can start helper agents for large tasks and contact the people
you named. The server does not decide what the coordinator should do. It only
enforces the limits you set: who it may contact, how much it may send, a pause
switch, an audit log and a budget of runs.

## What a coordinator is

A coordinator is two records with the same ID: a [saved agent]({{%relref "features/chats-agents" %}})
and a coordinator config. A saved agent is a coordinator when it has a config;
the agent record itself does not change, so export, import and the agent list
work as before.

The config has:

| Field | Meaning |
|---|---|
| `contacts` | The people and teams the coordinator may contact, at most 50. Each has an `id` (lowercase letters, digits and dashes, up to 40 characters), a `name`, a `description` and a kind. A `user` contact is a user of this server. An `external` contact is a chat of one of your connector bindings: the binding, the chat to write in, and the sender ID that replies come from (the chat when empty). The list is also the list of channels it may write to. |
| `page` | The name of the page folder `pages/<name>/` that holds the coordinator's state. The folder is created with a starter page and is the only folder the agent may write. Empty: no folder fence. |
| `wake_every` | The schedule: empty for events only, otherwise from 5 minutes to 24 hours. |
| `wake_note` | Text added to every scheduled wake, at most 4000 bytes. |
| `send_per_minute`, `send_per_day` | Caps on the messages it sends. Defaults 10 and 100, at most 60 and 5000. |
| `max_helpers`, `max_depth` | Limits on its helpers. Defaults 8 and 2, at most 64 and 4. |

The operator's ceilings for the helper count and depth, the runs a day, the
coordinators that run at once and the audit retention are
[runtime settings]({{%relref "features/runtime-settings" %}}).

When you create a coordinator the server makes the agent with the grant
`act.files` added, so its chat can write its page, and the starter instructions
when you wrote none. It then makes the one long-lived chat of the coordinator.
The chat has the origin `coordinator`. Nobody watches it, so, like the chat of a
trigger or an agent job, it runs with the `act.*` grants of its agent and
nothing more: a write that the agent does not hold is refused with a sentence
that names the missing grant. If you delete that chat, the next wake makes a new
one. If you delete the agent, its config goes with it and nothing wakes the old
chat again.

A contact must be valid when you save it:

- A `user` contact is you, or a user the server can find.
- An `external` contact is on a binding that you own, that keeps one chat per
  sender or per chat (not per thread or time window), and that accepts the
  sender of the contact.

**State mode.** The configs, the counters and the audit log are in the shared
store, so every frontend of a cluster sees the same state. A standalone server
without a database keeps them in files under the data path. The wake-ups run on
one frontend at a time, the one that holds the reaper lock.

## Pause, budget and held messages

These limits are enforced in the server, never only in the prompt. A message
that cannot run now is not dropped: it waits as a waiting message of the chat,
in the order it arrived, and runs once the coordinator may run again.

- **Pause.** While a coordinator is paused, every message to its chat is held,
  yours too. No turn starts, no loop of the chat runs, and an interrupted turn
  is not resumed. A turn that is still running does not take the held messages.
- **Run budget.** Wakes that come from a schedule, from the reply of a contact
  or from the report of a helper take one run of the budget of the day (UTC)
  of that coordinator. The operator sets the budget with the setting
  `chats_coordinator_runs_per_day`. When it is spent, the wake is held. Your
  own message never takes from the budget. A message that joins a turn that is
  running, or that waits and goes on after it, starts no new run and is not
  counted.
- **Concurrency.** The operator limit `chats_coordinator_max_running` is the
  count of coordinator chats with a turn running, in the whole cluster. A wake
  that would pass it is held. Your own message, and chats that are not
  coordinators, are not limited by it. The count is read just before the turn
  starts, so two coordinators that start in the same instant can pass it by
  one until the next check.

**State mode.** The pause flag and the run counters are in the shared store.
Each frontend reads them at the moment it decides, so all frontends give the
same answer. The budget is taken with one atomic update in the store: of many
senders that race for the last run, one gets it and the others are held. When
a message of a connector is held, the sender gets no reply.

## Pause and resume

One switch stops a coordinator. The owner or any administrator can press it,
from any frontend. Pause does this, in order:

1. It sets the flag in the shared store. From then on every frontend holds the
   messages to the chat (yours too), starts no turn, no loop run and no resume,
   and the contact tool refuses every send, also in a turn that is still
   running when the flag is set.
2. It writes one audit row (kind `pause`, with who pressed it), only when the
   state changed. Pressing it twice writes one row.
3. It stops the turn of the chat and its background helpers, on whichever
   frontend they run, and pauses the goal and the active loops of the chat.

Resume clears the flag, writes one audit row (kind `resume`) and starts the
held messages as one turn, in the order they arrived. The goal and the loops
that pause stopped stay paused: start them again when you want them. A
message that was held because the run budget was spent stays held until the
budget allows it, because resume treats held messages as wakes.

The audit log is readable by the owner for their own coordinators and by an
administrator for all of them. The log keeps the length and a hash of each
message, never the text.

**State mode.** The pause flag is in the shared store, so pressing it on one
frontend takes effect on all of them at the next check. The stop for a turn
that runs on another frontend travels over the message bus, as the stop button
does. If a stop is lost, the flag still holds every new message and denies
every send, and pressing pause again repeats the stop.

## Contacts and caps

A coordinator writes to people only with the `contact` tool. The tool is
offered to the chat of a coordinator and to no other chat: not to an ordinary
chat, and not to a helper of the coordinator. It has two actions.

- `list` shows the contacts the creator named, with the id, the name, the
  description and the kind. It never shows an account, a binding, a chat or a
  sender.
- `send` writes a text (1 to 4000 characters) to the contact in `to`.

A user contact gets an item in their inbox (source `coordinator`), with the
first 320 characters of the text. Only the owner gets a link to the chat. The
creator lists the contacts: the model cannot add one, and an id that is not
listed is refused.

Every `send` is checked in this order, against the shared store, at each call:
pause, the contact list, the text, the cap for the minute, the cap for the day.
A refused send is an answer to the model that says why, not an error, and the
model is told not to try to get round it. The caps are per coordinator:
`SendPerMinute` (default 10, at most 60) and `SendPerDay` (default 100, at most
5000), counted in UTC. A slot is not given back when the delivery fails.

Every attempt, sent or refused, is one audit row with the reason (`paused`,
`not_a_contact`, `bad_text`, `minute_cap`, `day_cap`, `inbox_off`,
`delivery_failed`, `ok`). The row keeps the length and a SHA-256 hash of the
text, never the text. The text is in the transcript of the chat.

**State mode.** The caps are atomic counters in the shared store, so two
frontends together never pass the limit. The pause check reads the store at
every call.

## Messaging apps: contacts and replies

An external contact names a binding, a chat of the messaging app to write in,
and the sender whose replies count (empty: the chat itself). The binding must
belong to the owner of the coordinator, keep one conversation for each sender
or for each chat, and accept that sender. A team is an external contact whose
chat is a group chat.

`contact` writes to such a contact through the binding, with the connector
send kind `coordinator`. The creator's contact list is the authority, so no
capability of a sender is checked. A message never waits in the queue of the
binding: the queue is in memory on one frontend, so it could not be held across
a pause or a restart. When the binding is at its limit of 20 messages a minute,
the call answers "busy" (audit reason `binding_busy`) and the coordinator tries
later. A switched-off binding is audit reason `binding_off`.

A reply goes the other way. When a message arrives on a binding, the host first
checks whether its sender (or chat) is an external contact of a coordinator of
the binding's owner. If it is, the message goes to the chat of that
coordinator, as text that starts with `[Reply from <name>]`, and the answer of
the turn goes back to that contact. The reply counts as a wake from a
contact: it is charged to the run budget, and while the coordinator is paused
it waits as a stored message, with no message to the sender, until resume. A
sender who is not a listed contact never reaches the coordinator: the message
follows the normal path of the binding. If two coordinators list the same
contact, the older one gets the reply.

**State mode.** The routing reads the coordinator store, so every frontend
routes the same way. The queue of the binding stays per frontend, and the
coordinator never uses it.
