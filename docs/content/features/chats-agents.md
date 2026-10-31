+++
disableToc = false
title = "Chats: saved agents, triggers and inbox"
weight = 22
url = "/features/chats-agents/"
+++

A saved agent is a definition you can start a [chat](/features/chats/) with.
It has instructions and four lists: the MCP servers, add-ons, collections and
skills the chat may use. Triggers and the inbox build on agents and are
described on this page too.

## Saved agents

An agent has:

| Field | Meaning |
|---|---|
| `name` | 1 to 80 characters. Unique for you, ignoring case. |
| `description` | A short note for you. |
| `instructions` | Text added to the system prompt of every turn. At most 32 KiB. |
| `model` | Optional. The chat uses this model. Without it, the chat is created with the model you choose. |
| `mcp_servers` | Names of servers in the model's MCP configuration, and your [MCP connectors](/features/chats-connectors/#mcp-servers) as `connector:<id>`. The form lists both. |
| `addons` | IDs of installed add-ons. |
| `collections` | Names of your collections. |
| `skills` | Names of skills. The index of skills and the `load_skill` tool of the chat offer only these. |
| `network` | The network of the [Sandbox]({{%relref "features/addons#network-access" %}}) commands of the agent's trigger and connector runs: `off` (the default), `registries` (the package registries) or `all` (every public host), capped by the administrator's `chats_egress_ceiling`. A run never shows a card: a host outside it is blocked, and the block is in the run's output. A chat you start with the agent starts with this preset too; you change it in the chat's Tools menu. The agent form sets it in the add-ons fold. |

Each list is an exact grant. An empty list grants nothing: a chat with an agent
that names no add-ons has no add-on tools, even when you could use some. A
plain chat, with no agent, is not changed.

### Add-on changes apply at once

An add-on you turn on for a chat, or name in an agent, is allowed to change
your data in that chat. A tool call that adds rows, changes a schema, creates a
database or starts a job is applied at once, as you, with the same permission
and stale-data checks as the Add-ons page. There is no confirmation card. The
model is told what was applied, and the tool entry in the chat says it. A change the add-on kept a change set for can be undone from the
chat with an Undo button (Tables rows, schema and databases, and Fine-tuning and
Quantization job starts); the result says whether a change can be undone and, if
not, why. File changes of the Sandbox and Office add-ons are one change set in the
history of your files and can be undone there. A trigger or a scheduled run behaves the same, with no
proposal waiting for a browser. Other ways to call add-on tools, such as the
OpenAI-compatible API and MCP, still store a proposal that only your own
browser session can confirm.

### An agent never gives more than you have

A chat gets the intersection of the agent's lists and what you may use at that
moment. If an add-on is disabled, a collection is deleted or you lose the MCP
feature, the chat still starts. The thing is absent, and the model gets one
line at the start of the turn, for example `unavailable this turn: add-on
office, collection docs`. A sub-agent of the chat can only get tools the chat
has.

A chat can also start a saved agent as a sub-agent, when you switch **Saved
agents** on in its Tools menu. See [Saved agents as
sub-agents]({{%relref "features/chats#saved-agents-as-sub-agents" %}}).

If you delete an agent, the chats that ran with it keep their history. Their
next turn runs without the agent's tools, and the model is told that the agent
was deleted.

### Pages

The Agents page (**Agents** in the sidebar, `/app/agents`) lists your agents with the chats that ran with
each one, and links to the **Triggers** and the [**Tasks**](/features/agent-jobs/).
A page for one agent (`/app/agents/{id}`) shows its definition, the triggers that
start it and its run history from the server (the chats you had with it and the
ones its triggers and connectors started). Create and edit an agent in a form of
folds with a preview of the definition and of what changed. **Export** and
**Import** use the server's agent file (see below); the import opens the new
agent and says what the server changed. It also reads the JSON export of an
older agent, and names what it could not take over. With no agents yet, the
page offers the server's starter agents. Chat with an agent from its row: the
chat page opens empty with the agent chosen.

### API

```
curl -X POST .../api/chats/agents -d '{"name": "Reviewer", "instructions": "Answer in French.", "addons": ["office"], "skills": ["review"]}'
curl .../api/chats/agents
curl -X POST .../api/chats -d '{"agent_id": "<id>", "model": "my-model"}'
```

`GET`, `PUT` and `DELETE /api/chats/agents/{id}` read, replace and delete one.
A `PUT` replaces every field. Chats that already run with the agent use the new
definition from their next turn. Another user's agent is always `404`.

Agents need the `agents` feature, which is off by default for a user (see
[Chats permissions](/features/chats/#permissions)). They have no administration
tools, as they are your own settings.

### Starter agents

`GET /api/chats/agents/templates` lists ready-made definitions (a research
assistant, a code reviewer, a daily digest, a writing editor). They grant
nothing. `POST /api/chats/agents/templates/{id}/create` saves one as your own
(`{"name": "..."}` is optional); then grant it tools with `PUT`.

### Export and import

```
curl .../api/chats/agents/<id>/export > reviewer.json
curl -X POST .../api/chats/agents/import -d @reviewer.json
curl -X POST .../api/chats/agents/import -F file=@reviewer.json
```

The file holds the definition only (version 1, kind `localai-agent`): no ID,
owner or time. An import always makes a new agent. A name you already use gets
" (2)", " (3)" and so on, and a model that this server does not have, or that you
may not use, is dropped. The response lists such changes under `warnings`. The
grants (MCP servers, add-ons, collections, skills) are kept as names; a grant
that names something you do not have gives the chat nothing, as for any agent.
Secrets are never part of an agent, so the file has none.

### Run history

`GET /api/chats/agents/{id}/runs` lists the chats that ran with the agent,
newest first: the ones you had with it, the ones its triggers started
(`origin: trigger`) and the ones its connectors started (`origin: connector`),
each with `running` and `last_turn`. `total`, `failed` and `running` count every
run among your newest 5,000 chats. Open a run with `GET /api/chats/{chat_id}`.

## Connectors

A [connector](/features/chats-connectors/) binds a Telegram bot or a Slack app to an agent, so a person can talk to it from Telegram or Slack. An [MCP connector](/features/chats-connectors/#mcp-servers) is a tool server that an agent can name in its `mcp_servers` list.

## Triggers

A trigger starts a chat as you, by itself. Each run is an ordinary chat of
yours: it uses your files, your quota and your model allowlist, you can open it
and undo what it changed, and it is marked with its trigger. To repeat a
prompt inside one chat, every few minutes, use a
[loop]({{%relref "features/chats#loops-repeat-a-prompt-in-a-chat" %}}) instead
(see *Loop or trigger?* there). There are two kinds.

| Kind | Runs | Fields |
|---|---|---|
| `schedule` | At a time | `schedule`, `time_zone` (an IANA name; UTC when empty) |
| `file` | When a file arrives in a folder of your files | `folder` (a path inside your files, such as `/Contracts`), `quiet_seconds` (default 30), `min_gap_seconds` (default 60) |

A schedule is one of `daily 08:00`, `weekdays 09:30`, `weekly mon,thu 09:30`,
`monthly 15 08:00`, `once 2026-12-24 18:00` or five cron fields. The cron fields are numbers, lists, ranges and steps (`*/15`, `1-5`, `1,15`); names such as `mon` or `jan` are not read, and `weekly` takes day names. A time that
does not exist in the time zone (a daylight-saving gap) moves to the first valid
minute after it, and a repeated hour runs once. `GET /api/chats/triggers/preview?when=...&tz=...`
shows the next times and says what the text means.

A file trigger waits until the file has not changed for the quiet period, then
starts one run for that version of the file. The first message of the run says
`A new file arrived: /Contracts/a.txt`. A run's own writes never start the same
trigger. A file that changes again after a run of the trigger began waits for
the trigger's minimum gap (`min_gap_seconds`, 60 by default, at most a day)
before it starts another run, and further changes while it waits start nothing
extra. Files that were already waiting when the run began still run. With the
daily limit, this keeps a writer that reacts to the run's output, such as a
page that saves after each change it sees, from starting runs in a loop: at
most one run a minute and 24 a day by default. An `AGENTS.md` file in a folder, or above it, is added to the first
message as rules for the run. Treat it as text from whoever can write there.

### What a run gets

- **Its message.** The first message is the prompt of the trigger, the folder
  rules and, for a file trigger, the file. LocalAI adds one closing line to every
  trigger run: "When you are done, answer with one short plain sentence about what
  changed. Do not print JSON or code unless the task asks the user for it." The
  answer is what you read in the chat and the inbox, so write the prompt without
  a request for JSON output. A connector chat is a talk with a person and does not
  get the line.
- **Who it is.** The run is a chat of the trigger's owner, with the owner's
  grants. If the owner is over quota, the run fails with that reason and the
  owner gets a notification.
- **Which tools.** With an agent (`agent_id`), the tools of that agent. Without
  one, the run uses the model and the add-on and skill switches of the chat that
  made the trigger (`source_chat_id`), or the defaults of a new chat. An
  unattended run with no agent gets no add-on tool that acts at once, such as the
  Sandbox: name the add-on in an agent to allow it. A run never gets
  `propose_trigger` and cannot change its own trigger.
- **Network.** The Sandbox commands of a run reach the network of the agent's
  `network` field, under the administrator's ceiling, and nothing without an
  agent. Nobody watches a run, so no card asks: a blocked host stays blocked.
  A sub-agent follows the network of the chat that started it, its rules
  included, and never shows a card either.
- **Limits.** A run has 15 minutes. At most 2 runs go at once, one per trigger,
  and a trigger has a daily limit (`daily_cap`, default 24). A run that is skipped
  or fails shows its reason on the trigger.
- **Cost.** A run is a chat turn on the trigger's model, so it uses tokens. A run
  records the prompt and completion tokens of its model requests (`tokens` on the
  run), and a trigger reports the average of its last runs, the most runs one day
  can have, and whether the model is a remote service (`cost` on the trigger).
  LocalAI keeps no price list, so the cost is in tokens only.
- **History.** A trigger keeps its last 200 finished runs, and every run of
  the last two days. A finished run older than 30 days is removed, whatever the
  count. A run that is queued or running is never removed. Set
  `--trigger-run-keep` and `--trigger-run-retention-days` (`-1` keeps all) to
  change this. A file version that ran can run again once its old run
  is removed.
- **When it cannot run.** A deleted agent fails the run with "the agent was
  deleted" and switches the trigger off. A disabled owner skips the run. An owner
  who no longer exists, or a folder that is gone, skips the run and switches the
  trigger off. Each of these tells the owner.

A trigger runs nothing for the time before it was saved or switched on. After
downtime, a missed schedule runs once, not once for each missed time.

### Proposals

The four trigger tools (`propose_trigger`, `list_triggers`, `update_trigger`
and `delete_trigger`) form the **Triggers** group in the chat's Tools menu. They
are on in a chat you start, and one switch turns all four off for that chat
(`PUT /api/chats/{id}/builtins {"off": ["triggers"]}`; see
[the Tools menu](/features/chats/#switch-tools-off-for-one-chat)). Switch it off in the Home
composer before you start the chat to have none from the first message.

In a chat, the model can propose a trigger with `propose_trigger` and list yours
with `list_triggers`. A proposal is not active and runs nothing. It waits for you
in the chat and on the Triggers page (`/app/triggers`, `GET /api/chats/triggers/proposals`, with
`?chat={id}` for the ones made in one chat). Change it first if you want
(`PUT .../proposals/{id}` with the same body as a trigger; it stays a proposal),
then confirm it (`POST .../proposals/{id}/confirm`, with your `time_zone` when it
names none) or dismiss it (`DELETE .../proposals/{id}`). The chat carries the
proposal: the tool line of `propose_trigger` has a `card` of kind
`trigger_proposal` whose `id` is the proposal's id, in the live `tool_end` event
and in the transcript, so it shows again after a reload, and a
`trigger_proposed` event announces it to a client that is watching. If a proposal was made in a chat that
has an agent, the trigger keeps that agent. At most 10 proposals wait at a time,
and you can have 50 triggers.

The card sits below the text of the message it belongs to. When you answer it,
it does not go away: it turns into a receipt, and the answer is kept on the
server with the proposal (`GET .../proposals/{id}` returns `state`: `pending`,
`accepted`, `declined` or `undone`), so the receipt shows again after a reload,
in another browser and on every frontend of a cluster.

- **Accepted** shows what was scheduled in plain words ("Scheduled: Every Monday
  at 09:00 (Europe/Rome)"), the name of the trigger, its next run, **View
  trigger** (the trigger has the proposal's id) and **Undo**.
- **Undo** (`POST .../proposals/{id}/undo`) switches the trigger off and removes it
  from the Triggers page. The proposal stays as an undone record, and **Schedule
  it again** (`POST .../proposals/{id}/reopen`) makes it wait for your answer
  again. Undo is refused with `409` once the trigger ran (a run started by hand
  counts) or you edited or switched it on the Triggers page; the receipt then
  tells you to remove it there. The response carries `can_undo` and `undo_blocked`
  (`ran` or `edited`).
- **Declined** leaves a quiet one-line receipt with **Restore**.
- A trigger you delete on the Triggers page ends the receipt: the card says the
  trigger was removed (`404`).

At the start of its next turn the model is told what became of the triggers it
proposed in the chat (accepted, declined or undone), so it does not say a
trigger runs when it was undone, and does not propose a declined one again
unless you ask. A declined or undone proposal does not count towards the limit
of 10 waiting proposals or 50 triggers.

A schedule proposed in a chat runs at most once an hour. A shorter one (for
example `*/15 * * * *`) is refused with the reason, and the model is told to
propose an hourly or slower one. An edit of the proposal keeps the same floor.
On the Triggers page you can still choose a shorter schedule yourself.

A proposal also carries a plan, so you see what it does before you create it:
what a run reads (`reads`), the file in your home it rewrites (`writes`), the
Home widget that shows that file (`widget`, the widget's `id` in `LocalAI/home.json`)
and about how many tokens one run uses (`estimated_tokens`). The card lists
them with when it runs and its first run, that it runs as you with the chat's
model, the cost (and whether the tokens leave the server) and how to stop it.
When you create a trigger whose plan names a widget, LocalAI writes the
trigger's id into that widget as `trigger`, so the widget can show when it runs.
A form that edits a trigger and does not send the plan keeps it.

### Changes from a chat

The model can also change the triggers you already have. `list_triggers` gives
each trigger a short `ref` (for example `t1a2b3c4d`): it stays the same for the
trigger and it is not the trigger's id. The list shows no prompts. To read the
prompt and the other settings of one trigger, the model passes its `ref` with
`include_prompt: true`. A `ref` of another user's trigger, or one that does not
exist, is an error for the model. The model can also name a trigger by its
exact name when only one trigger has that name.

- `update_trigger {ref, name, prompt, when, time_zone, daily_cap, enabled, reads, writes, widget, estimated_tokens}`
  changes the fields it names and keeps the others. The same rules as for a
  proposal apply: a schedule that changes runs at most once an hour, the
  folder and the `writes` file must be in your files. A change of anything
  other than `enabled` does nothing by itself: a card shows each changed field
  before and after, with **Apply** and **Dismiss**. A change that only pauses
  (`enabled: false`) or resumes (`enabled: true`) the trigger applies at once,
  because you can take it back, and its card is a receipt with **Undo**.
- `delete_trigger {ref}` deletes nothing by itself: a card shows the trigger's
  name, when it runs, whether it is on and its last run, with **Delete** and
  **Keep**.

The tool descriptions tell the model which change waits and which applies at
once, so it does not say a change is made before you apply it. At the start of
its next turn the model is told what you did with the changes it asked for in
the chat.

After you apply a change the card is a receipt with **Undo** for one day.
**Undo** puts the trigger back as it was before the change. For a delete it
restores the trigger with the same id and settings; its run history is not
restored, and the chats of its runs were never removed. **Undo** is refused
(`409`, with the reason) after the day, when the trigger was changed on the
Triggers page since, or, for a delete, when you already have 50 triggers. A
change that waits cannot apply once the trigger was changed or removed after
the model asked for it: the card says so and keeps only **Dismiss**.

The card's change is kept on the server, so the card shows the same state
after a reload, in another browser and on every frontend of a cluster. The
tool line has a `card` of kind `trigger_change` whose `id` is the change's id,
and a `trigger_proposed` event announces it to a client that is watching.

| Route | What it does |
|---|---|
| `GET /api/chats/triggers/changes/{id}` | The change: `op` (`update`, `delete`, `pause` or `resume`), `state` (`pending`, `applied`, `dismissed` or `undone`), `trigger_id`, `before`, `after` (absent for a delete), `fields`, `stale`, `can_undo`, `undo_blocked` (`expired`, `edited`, `removed` or `full`) and `last_run`. `404` a week after it was answered, when it is no longer kept. |
| `POST .../changes/{id}/apply` | Makes the edit or deletes the trigger. `409` when the trigger changed since or the change was answered. |
| `DELETE .../changes/{id}` | Dismisses the change (**Keep** for a delete). The trigger stays as it is. |
| `POST .../changes/{id}/undo` | Takes an applied change back, for one day. |

A change does not count towards the 50 triggers or the 10 waiting proposals. At
most 10 changes wait at a time. Like the other trigger tools, these tools are
offered only in a chat a person started: a trigger run, a connector run and a
sub-agent never get them.

### Clusters

In a cluster, one frontend runs the triggers at a time: the one that holds an
advisory lock in the database. A due time or a file version is one unique row,
so it runs once even when two frontends try. If that frontend stops, another
takes over, ends the runs whose chat is over as failed ("interrupted by a
restart") and tells the owner, and goes on. Triggers need the database; the
JSON store is for a single process.

With [chat workers](/features/distributed-mode/#chat-workers), the frontend that
leads still starts and watches each run, and a chat worker runs its turns.

The same holds for one server. A run that a stop or a restart cuts short is
recorded as interrupted, its chat shows the turn as interrupted (not done, even
if some text was written), and its owner gets a "failed" notification. The
triggers are checked every 5 seconds, so a schedule starts within 5 seconds of
its time and a file trigger within 5 seconds of the end of its quiet time.

### API

```
curl -X POST .../api/chats/triggers -d '{"name": "Morning digest", "prompt": "Summarise yesterday.", "kind": "schedule", "schedule": "weekdays 09:00", "time_zone": "Europe/Rome", "enabled": true, "model": "my-model"}'
curl .../api/chats/triggers
curl -X POST .../api/chats/triggers/<id>/run      # start a run now
curl .../api/chats/triggers/<id>/runs
```

A trigger in a response also has `plan` (`reads`, `writes`, `widget`), `cost`
(`estimated_tokens`, `average_tokens` and `runs`, `max_runs_per_day`, `remote`),
`last_done` (the end of the newest run that ended well), `min_gap_seconds` for a
file trigger, and each run its `tokens`. A create or replace takes `plan`,
`estimated_tokens` and `min_gap_seconds`; an empty one keeps the stored value.

`GET`, `PUT` and `DELETE /api/chats/triggers/{id}` read, replace and delete one,
and `POST .../enable` and `.../disable` switch it. Deleting a trigger stops its
running run and drops its queued runs; the chats it made stay. Another user's
trigger is always `404`. Triggers are your own settings, so they have no
administration tools.

## Inbox

The inbox lists what ended while you were elsewhere: a trigger run that
finished or failed, or something that needs you. Each item has a kind
(`run_ended`, `run_failed` or `needs_attention`), a status (`ok` or `failed`),
a source (`trigger`, `connector` or `system`; empty on items stored before
sources existed), a short title and text, and a link to the chat it is about.
A chat that a run started is unread until you open it: opening the chat marks
its notifications read.

In the chat page, the **Notifications** button in the header shows the unread
count (99+ above 99) and opens the panel (a sheet over the bottom of the
screen on a phone):

- **All**, **Unread** and **Failed** filter the list. **Failed** shows every
  failed run and every trigger or connector that was switched off.
- Items are grouped under **Today**, **Yesterday** and **Earlier**. Each one
  shows what sent it, when (the exact time on hover) and, for a failure, the
  reason.
- A click on an item marks it read and opens its chat. A connector that was
  switched off opens the connectors page.
- Hover an item (or focus it) to mark it read or delete it. With the keyboard,
  the arrow keys, Home and End move between items, Enter opens one, Delete
  removes it and Esc closes the panel.
- **Mark all read** is in the panel header; **Clear read** and **Clear all**
  are in its menu.
- A notification that arrives while the panel is open slides in at the top
  and is highlighted for a few seconds.

You keep at most 500 notifications, none older than 90 days. The age limit applies when you read: an older notification is not listed or counted, and it is deleted the next time one is added.

```
curl ".../api/chats/notifications?unread=1"          # the unread ones, and the unread count
curl .../api/chats/notifications/count
curl -X POST .../api/chats/notifications/read -d '{"all": true}'
curl -X DELETE .../api/chats/notifications -d '{"ids": [12, 13]}'
curl -X DELETE .../api/chats/notifications -d '{"all": true}'                    # empty the inbox
curl -X DELETE .../api/chats/notifications -d '{"all": true, "read_only": true}' # remove only the read ones
curl -N .../api/chats/notifications/events            # new ones as server-sent events
```

**Clear all** asks first, in the
panel, and says how many notifications it removes. **Clear read** keeps the
unread ones. A removed notification cannot be restored. The list answers with
`total`, everything you keep, because it returns at most `limit` items (50 by
default, 500 at most); the panel loads more on request. A delete answers with
the number removed in `deleted` and the unread count left. You can only ever
remove your own notifications.

Events are live only. A client that was away lists the inbox. In a cluster, a
notification added on one frontend reaches the clients connected to any other.
When notifications are read or deleted, the stream sends a
`notifications_changed` event, so your other open tabs read the list again and
their unread badge follows. The notifications are rows in the shared database,
so clearing the inbox on one frontend clears it for all of them.

### Browser notifications

The inbox page can ask the browser for permission and subscribe it to push. A
notification is then pushed to that browser, with its title and link and nothing
else, also when no LocalAI tab is open. Clicking it opens the chat.

```
curl .../api/chats/push/key                       # the server's VAPID public key
curl -X POST .../api/chats/push/subscribe -d '{"endpoint": "https://...", "keys": {"p256dh": "...", "auth": "..."}}'
curl -X DELETE .../api/chats/push/subscribe -d '{"endpoint": "https://..."}'
```

- The server makes its VAPID key pair the first time it is needed and keeps it
  with the subscriptions: in the database, or in `push.json` (mode `0600`) under
  the data path. With neither, push is off and these routes answer `409`.
- The endpoint must be an `https` address on the public internet. The server
  refuses private, loopback and link-local addresses, both when a browser
  subscribes and when it sends, and it does not follow redirects.
- You can have 10 subscribed browsers; the oldest go first. A subscription that
  the push service reports gone is removed.
- In a cluster, the frontend that stores a notification sends the push, so it is
  sent once.

## Storage and clusters

Saved agents, triggers, runs, notifications and push subscriptions are stored
beside your chats: in the database when there is one, in JSON files under the
data path otherwise, and in memory when there is no data path (the page then says
persistent features are off, and push is off). The JSON stores are for a single
process.

A cluster needs PostgreSQL, as the chats do. This is how each part of the state
behaves with several frontends:

| State | Mode | How |
|---|---|---|
| Agents, triggers, runs, notifications, push subscriptions | Shared | Rows in the database. Any frontend reads and writes them. |
| Which frontend runs the triggers | Single runner | The frontend that holds the advisory lock (`KeyChatsTriggers`) runs the Runner. When it stops, another takes the lock. |
| A due time or a file version | Shared | One unique row for each trigger and due key. Two frontends cannot start the same run. |
| Trigger proposals and changes asked for in a chat | Stateless | Rows in the trigger table. Each request reads and writes them in the database, so any frontend can show, apply or undo them. The answer to a change (apply, dismiss, undo) is a conditional update of its row: when two frontends answer the same change at once, one wins and the other answers `409`, so a change is applied or undone once. |
| Quiet periods of waiting files, the memory of a run's own writes | Per leader | Kept by the Runner of the current leader. A new leader reads the change feed again from its saved position, which stays below the oldest waiting file, so a file that was waiting is found again. |
| Live notifications | Shared, live only | Sent over the message bus to every frontend, with the "read or deleted" signal. A client that missed one lists the inbox, which is the record. |
| The supervisor of a running run | Per instance | The frontend that started the run watches it. The leader also ends any run that nobody watches whose chat is over, so a run is not lost when its frontend dies. |
| Where the turn of a run runs | Stateless | On the frontend that started it, or on a chat worker when the frontends send unattended turns there. The turn claim, its heartbeat and the waiting messages are in the database, so either one can run it and only one does. |
