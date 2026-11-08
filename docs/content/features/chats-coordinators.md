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

## Its page

A coordinator keeps its state in its page folder, `pages/<name>/` (the `page`
field). It writes the page with its own file tools, for example `write_file`
on `index.html`. The write is direct: it is not a proposal, nothing waits for
your confirmation, and `propose_change` is not offered to the chat. The page
stays sandboxed like any page, and only you and the members you shared the
agent with can see it.

The write is a file action of a chat that nobody watches, so it needs the grant
`act.files` on the agent. A new coordinator gets the grant from the preset. If
you remove it, the file tools refuse every write with the sentence that
unattended chats always get, and the model reads that it needs `act.files`; no
proposal is made instead. That is your choice to make, and the server does not
force it.

The folder is a fence. When `page` is set, a write can reach only that folder:
an absolute path is read inside it, and a path that climbs out of it is
refused. A helper writes the page only with a file tool. A helper started with
a list of tools that does not name `write_file` has none. A helper that has the
default tools has the file tools of the coordinator and the same rights, which
are the rights of the coordinator at the top of its tree at any depth, so a
coordinator without `act.files` has helpers that cannot write either.

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

## Helper limits

A chat that is not a coordinator keeps its limits: at most 4 helpers run in the
background at once in a conversation, and a helper starts no helper (depth 1).

A coordinator has its own limits, set by its creator and capped by the operator
(Settings, Tools: the helper ceiling and the depth ceiling):

- `MaxHelpers` (default 8, at most 64) is the most helpers that run in the
  background under the coordinator at once. It counts the whole tree, so a
  helper of a helper counts. The operator ceiling (default 16) lowers the
  effective value without changing what is stored.
- `MaxDepth` (default 2, at most 4) is how many levels of helpers there may be.
  The operator ceiling is 3 by default.

A helper is offered `spawn_agent` and `wait_agents` only while it is above the
depth limit, and its description tells it to wait for its own helpers before
it answers. A helper never has the `contact` tool. A helper has the switches
and the rights of the coordinator at the top of its tree, at any depth. Saved
agents are still refused as helpers of a coordinator. `wait_agents` still
waits at most 10 minutes.

Lowering a limit never stops a helper that runs: it only blocks new ones.
A helper that is refused is one audit row (kind `helper`, reason `helper_limit`
or `depth_limit`).

**State mode.** The count reads the shared chat store, so a helper that runs on
another frontend counts. As for any cap that is read and then used, two
frontends that start a helper at the same moment can pass the limit by one.

## Wake-ups

A coordinator with `WakeEvery` set (5 minutes to 24 hours) is woken on that
schedule. A wake posts one message into the one chat of the coordinator:
`[Coordinator wake: schedule at <time>]` and the wake note the creator wrote
(or a default line that tells the model to read its state, work, write its
state and stop). A new coordinator is due at once, so its first wake comes at
the next pass.

One pass (every 30 seconds) does four things:

1. For each coordinator that is due, it claims the wake in the store and sets
   the next one `WakeEvery` from now. A coordinator that was missed for many
   periods wakes once. A paused coordinator, or one with `WakeEvery` 0, is
   not woken.
2. It skips the wake without a trace when the chat has a turn running or
   messages waiting, so wakes never pile up. When the coordinator may not run
   (over the run budget, or at the concurrency limit), the wake is not sent and
   one audit row (kind `wake`) says why. The next wake time still moves.
3. It starts the messages that wait for a coordinator that may run now: a reply
   that was held for the run budget starts when the budget allows it (the day
   turns at UTC midnight), with no one pressing anything.

4. It delivers the reports of helpers that ended and were not reported yet
   (see Recovery), and drops the reports that are done or older than 7 days.

A wake turn is followed. When it runs longer than the trigger run limit
(Settings, Tools), it is stopped and one audit row (kind `wake`, reason
`timeout`) is written. A chat that was deleted is made again by the next wake.
The same pass prunes the audit rows older than the audit days (at most once an
hour in a process).

**State mode.** Single-runner. The pass runs in the step of the chat reaper, on
the frontend that holds its advisory lock, so no new lock exists. The reaper
runs only on a process that hosts the background work of the chats: a chat
worker or a dedicated add-on owner does not take the lock, so it cannot hold the
wake-ups back. The wake claim is also a conditional update in the shared store,
so a pass that runs twice at once (a lock that changed hands) wakes a
coordinator once. Without a database the one process is the leader.

## Recovery

The coordinator is told how each of its helpers ended, also when the process
that started the helper is gone.

When a coordinator starts a helper in the background, the server writes a
report row in the store. The row stays until the end of the helper was told to
its parent. The parent of a helper is the coordinator chat, or the chat of the
helper that started it. The message the parent reads depends on how the helper
ended:

- Finished: `[Sub-agent finished: <title>]` and what the helper answered. This
  is the same message every chat gets from a helper.
- Stopped, failed or interrupted: `[Sub-agent <status>: <title>]`, what the
  helper wrote before it ended (or "no output") and the line "Its work may be
  incomplete." The coordinator can then start the helper again or change its
  plan. A helper that was cut by a restart is `interrupted`. A helper is not
  resumed by the server: its parent decides.

A report is due when the turn of the helper has ended, the helper has no helper
of its own that runs or whose report is on the way, no loop or wait holds it
and no message waits for it. A helper that started a helper of its own and ended
its first turn is not reported until its own helper ended and its parent read
that report and answered: the end that counts is its last turn. A helper in a
tree of depth 2 is reported to its parent, and its parent is reported to the
coordinator, each at the end of its turn.

The report is a message with the source "helper". It is held when the
coordinator is paused, and it counts for the run budget and the concurrency
limit like any message that wakes a coordinator. While the coordinator is
paused no report is sent, so a pause does not fill the chat with notices.
The reports of the helpers that the pause cut are sent after the resume as
"stopped". A report waits at most 7 days; then it is dropped. A report whose
parent chat was deleted is closed without a message, and the next wake makes
the chat again.

**State mode.** Shared and single-runner. The report rows are in the store, so
every frontend sees them. Two paths deliver a report: the process that started
the helper right when it ends, and the sweeper in the leader pass (the lock of
the chat reaper) for every report that is still open. Both claim the row with a
lease of 2 minutes before they send and mark it done after. A second caller
finds the lease or the done row and sends nothing, so the coordinator gets one
message. A frontend that dies after the claim leaves a lease that runs out; the
next pass of the leader sends the report then. The only case that can send a
report twice is a store that fails right after the message was sent, and it is
logged as an error. The sweeper runs every 30 seconds, so a report that no
process was there to send waits at most one pass plus the lease. With no
database (one process, JSON files) the reports are kept in a file next to the
coordinators and survive a restart.

## API

The routes are under `/api/chats`, in the swagger group `chats`. The six routes
of an owner need the same feature as the saved agents (`agents`, off by default
for a user); the administrator routes need the administrator role. Every route
needs a login. A server without coordinators answers 404 with the type
`coordinators_off`.

| Route | Who | What |
|---|---|---|
| `POST /coordinators` | agents feature | Make a coordinator: the agent, its config and its chat. 201 with the coordinator. |
| `GET /coordinators` | agents feature | List your coordinators. |
| `GET /coordinators/{id}` | agents feature | Read one (`id` is the agent ID). |
| `PUT /coordinators/{id}` | agents feature | Replace the agent and the config. The chat, the pause state and the wake times stay. |
| `POST /coordinators/{id}/pause` | owner or administrator | The pause switch. |
| `POST /coordinators/{id}/resume` | owner or administrator | Lift the pause. |
| `GET /coordinators/{id}/audit` | owner or administrator | The audit log of one coordinator. |
| `GET /admin/coordinators` | administrator | Every user's coordinators, without instructions and contacts. |
| `POST /admin/coordinators/{id}/pause` | administrator | Pause any coordinator. |
| `POST /admin/coordinators/{id}/resume` | administrator | Resume any coordinator. |
| `GET /admin/coordinators/audit` | administrator | The audit log of all coordinators, or one with `?coordinator=<id>`. |

The body of a create or replace has the fields of a saved agent (name,
description, instructions, model, MCP servers, add-ons, collections, skills,
network, memory and builtin lists) and these:

- `page`: the page folder name. Empty: no page.
- `contacts`: a list of `{id, name, description, kind, user | binding, chat, sender}`.
  Kind `user` names a LocalAI user, kind `external` a chat of one of your
  connector bindings.
- `wake_every`: the schedule in seconds, `0` for events only, else 300 to 86400.
- `wake_note`: text added to each scheduled wake message.
- `send_per_minute`, `send_per_day`, `max_helpers`, `max_depth`: `0` takes the
  default. A value above the schema limit, or above the operator ceiling for
  helpers and depth, is a 400 that says why.

A read returns the same fields with the defaults filled in, and also `chat_id`,
`paused`, `paused_by`, `paused_at`, `next_wake`, `last_wake` and `usage`
(`runs_today`, `sent_today`, `runs_limit`, `send_limit`, counted in UTC days).
The agent list (`GET /agents`) carries `coordinator: true` for a coordinator.

Errors: 400 `invalid_request_error` with the reason for a bad body, contact or
limit; 404 `not_found` for a coordinator that does not exist, is another user's,
or is a saved agent that is not a coordinator (all look the same). Pause and
resume answer with the state of the coordinator (no instructions), also on the
owner routes, because an administrator may press them there.

The audit routes take `kind`, `outcome`, `since` and `until` (RFC 3339), `limit`
(default 50, at most 500) and `before`. The answer is `{entries, next_before}`,
newest first; pass `next_before` as `before` for the next page. It is absent on
the last page. A non-administrator only ever sees rows of their own coordinators.
