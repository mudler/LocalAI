+++
disableToc = false
title = "Chats"
weight = 21
url = "/features/chats/"
+++

Chats are conversations that the server keeps. You can keep typing while the
model works, the model can hand work to sub-agents, long conversations are
compacted for you, and the model can use the MCP servers of its model
configuration.

The chats API sits beside the OpenAI-compatible `/v1/chat/completions` endpoint,
which does not change. Use `/v1` when a client brings its own history and needs
the OpenAI format. Use `/api/chats` when the server should keep the
conversation.

{{% notice note %}}
Chats need the `chats` feature. It is on for every user by
default. An administrator can switch it off for one user in the user's
permissions. Saved agents, triggers, connectors and agent jobs are separate
features that are off by default; see [Permissions](#permissions).
{{% /notice %}}

## Permissions

With authentication on, an administrator grants features to each user in
**Settings → Users and keys**. Plain chats are on by default. The parts that run
without a person, or reach the outside world, are off by default for a user, and an
administrator always has all of them:

| Feature | Default for a user | What it allows |
|---|---|---|
| `chats` | on | Chats, files, skills, notifications, push, your MCP connectors. A chat needs no more. |
| `agents` | off | Saved agents (`/api/chats/agents`) and starting a chat with one. |
| `triggers` | off | Scheduled and file triggers (`/api/chats/triggers`): runs that start by themselves. |
| `connectors` | off | Telegram bindings (`/api/chats/connectors`): a public door to one of your agents. |
| `mcp_jobs` | off | The agent tasks and jobs API (`/api/agent/...`). |

A command-line MCP server (stdio) in your MCP connectors stays for administrators
only, whatever the features. A trigger or a connector whose owner loses its feature
stops running: a trigger run is skipped with the reason, and the bot says it is not
available. The owner can get the feature back without losing the definition.

**Upgrading.** On the first start after the features were split, a one-time rule
keeps what each existing user was using, and nothing else. The rule runs once; a
user who registers later gets the defaults. A value an administrator stored
always wins.

- `agents`: kept if the user has the stored `agents` permission (the one the old
  agent pool had) or owns a saved agent.
- `triggers`: kept if the user owns a trigger, or had `agents` or `mcp_jobs`.
- `connectors`: kept if the user owns a connector binding, or had `agents`.
- `mcp_jobs`: stored as it was.

Items that the conversion of the old agents makes after that first start belong to
users who had the old permissions, which the rule already read.

### What an administrator can see

An administrator sees what every user has set up, each with its owner, and can
stop it. The view is read-only apart from switch-off and delete, and never shows
a bot token or an agent's instructions:

| Route | What it does |
|---|---|
| `GET /api/chats/admin/agents` | Saved agents of every user |
| `GET /api/chats/admin/triggers`, `GET .../triggers/{id}/runs` | Triggers with their last run, and the runs of one |
| `GET /api/chats/admin/connectors` | Connector bindings |
| `POST .../admin/triggers/{id}/disable`, `POST .../admin/connectors/{id}/disable` | Switch one off |
| `DELETE .../admin/agents/{id}`, `.../admin/triggers/{id}`, `.../admin/connectors/{id}` | Delete one |

The agent tasks and jobs have their own view: `?all_users=true` on
`GET /api/agent/tasks` and `GET /api/agent/jobs` (see [Agent tasks and
jobs](/features/agent-jobs/)).

## Switch the chats off

The operator can switch the whole surface or one part off. A part that is off
starts nothing: no loop, no trigger runner, no bot. `GET /api/features` reports
what runs (`chats`, `agents`, `triggers`, `connectors`).

| Setting | Default | Effect |
|---|---|---|
| `LOCALAI_DISABLE_CHATS` | `false` | Turns off the chats: the `/api/chats` API (it answers `404` with the type `chats_off`), saved agents, triggers, connectors, notifications and the agent jobs. The files workspace of the add-ons is not affected. |
| `LOCALAI_DISABLE_TRIGGERS` | `false` | Turns off the trigger runner. Trigger routes answer `409 triggers_off`. |
| `LOCALAI_DISABLE_CONNECTORS` | `false` | Turns off the connector bots. Connector routes answer `409 connectors_off`. |
| `LOCALAI_DISABLE_AGENTS` | `false` | Deprecated. It kept the meaning it had in the old agent pool, which also held the knowledge base: it sets `LOCALAI_DISABLE_CHATS` and `LOCALAI_DISABLE_COLLECTIONS`, and logs a warning. |

`LOCALAI_DISABLE_COLLECTIONS` stays separate and turns off only the
[collections](/features/collections/).

## Start a chat and send messages

```bash
# A chat talks to one model, chosen when it starts.
curl -s localhost:8080/api/chats \
  -H 'Content-Type: application/json' \
  -d '{"model": "my-model"}'
# {"id":"3f1c...","model":"my-model","running":false,...}

curl -s localhost:8080/api/chats/3f1c.../messages \
  -H 'Content-Type: application/json' \
  -d '{"text": "Summarize the three files I mentioned."}'
# {"started":true}
```

The reply is not in that response. Read it from the event stream or from the
transcript:

```bash
# Live server-sent events: text, reasoning, tool_start, tool_end, turn_end, ...
curl -N localhost:8080/api/chats/3f1c.../events

# The transcript, in order. Read it first, then open the event stream: the
# stream carries only what happens after you connect.
curl -s 'localhost:8080/api/chats/3f1c.../messages?after=0'
```

A chat is named after the first line of its first message, unless you gave it a
title. `PATCH /api/chats/{id}` with `{"title": "..."}` renames it (1 to 80
characters; `409` while a turn runs, `404` for another user's chat).

## Keep typing while the model works

Send another message while a turn runs and it waits. The response says so:

```json
{"started": false, "pending_id": "p2"}
```

Waiting messages reach the model together, in the order you sent them, at its
next request. Until then you can change or remove one:

```bash
curl -X PATCH  .../api/chats/3f1c.../pending/p2 -d '{"text": "Use euros."}'
curl -X DELETE .../api/chats/3f1c.../pending/p2
```

Once a message has reached the model, both calls answer `409`. A
`pending_delivered` event tells you when that happened.

If the turn ends normally before a waiting message was delivered, that message
starts the next turn. After a **stop** or a **failed turn**, nothing starts by
itself: the messages stay, and you decide.

```bash
curl -X POST .../api/chats/3f1c.../stop       # end the running turn
curl -X POST .../api/chats/3f1c.../send-now   # start a turn from the waiting messages
```

## Sub-agents

The model can start a sub-agent for a self-contained task. By default it waits
for the answer. With `wait` set to false it carries on, and the result arrives
later as a message in the conversation and as a `sub_agent_done` event.

- A sub-agent is its own conversation, linked to its parent. It appears in the
  list with `kind` set to `subagent` and a `parent_id`.
- A sub-agent gets the parent's tools, or a subset the parent names. It cannot
  start sub-agents of its own.
- A chat runs at most four sub-agents in the background at once.
- Stopping a turn, or a turn that fails, stops its running sub-agents.

## Long conversations

When a chat fills the model's context window, the older part is
compacted so the model can go on. The transcript keeps every message; only what
the model sees shrinks. A `compacted` event reports the size before and after.
The window comes from `context_size` in the model's configuration.

## MCP tools

A model that has MCP servers and sets `mcp.auto: true` offers all of them to its
chats, as it does for `/v1/chat/completions`. A user needs the
`mcp` feature, and `LOCALAI_DISABLE_MCP` turns MCP off for the whole instance.
A tool cannot take the name of a built-in tool such as `spawn_agent`.

You can also add MCP servers of your own, stored on the server: see
[MCP servers](/features/chats-connectors/#mcp-servers). Your enabled connectors
are offered to your chats next to the model's servers; the model's own
configuration is not changed.

### Switch tools off for one chat

The chat panel, **Tools**, lists the add-ons you may use, the MCP servers of the
chat's model and your connectors, with a switch each. What cannot be used now is
shown off, with the reason (a connector that is switched off, a model server
that the model does not use by itself). A switch takes effect at the next turn.
Switches only take away: a chat with a [saved agent](/features/chats-agents/)
lists only what the agent grants and never gets more than that. A trigger run with no
agent copies the switches of the chat that proposed it.

```bash
curl -s localhost:8080/api/chats/$ID/tools
# {"addons":[...],"servers":[{"name":"crm","off":false,"usable":true}],
#  "connectors":[{"id":"c1","name":"Docs","off":false,"usable":true}],"off":[]}
curl -s -X PUT localhost:8080/api/chats/$ID/mcp -d '{"off":["crm","connector:c1"]}'
```

`off` holds the name of a server of the model, or `connector:<id>`. At most 200 switches.

### Interactive tool pages (MCP Apps)

A tool of an MCP server can declare an interactive HTML page in
`_meta.ui.resourceUri`, as the [MCP Apps extension](https://modelcontextprotocol.io/extensions/apps/overview)
defines. When the model calls such a tool, the chat shows the page under the tool
line, with the arguments and the result of the call. This works for your
[connectors](/features/chats-connectors/#mcp-apps) and for the MCP servers of the
chat's model. The page appears as soon as the tool call ends, while the turn
goes on, and it stays when the turn ends or the chat is opened again.

A live `tool_end` event carries the reference of the app (server, tool, resource,
visibility) and `seq`, the number of the call's line in the transcript. The
arguments and the full result are not in the event. A client that wants them reads
that one line with `GET /api/chats/{id}/messages?after=<seq-1>&limit=1`, which has
the same checks and size limits as the whole transcript. Because the line is in
the shared store, this works whichever frontend runs the turn. A client that
misses the event or cannot read the line shows the text result, and finds the
app in the transcript when the turn ends.

The MCP connection stays on the server, so two routes of the chat carry the page
and its calls. Both answer only for a server that the chat may use at that moment:
not switched off in the chat's tools panel, named by the chat's agent if it has
one, and, for a connector, yours and enabled. You need the `chats` and `mcp`
features. Anything else is `403` with the type `mcp_app_refused`.

```bash
# The page of the app. server is connector:<id> or the name of a server of the model.
curl -s "localhost:8080/api/chats/$ID/mcp-apps/resource?server=connector:c1&uri=ui://clock/app.html"
# {"uri":"ui://clock/app.html","mime_type":"text/html;profile=mcp-app","html":"...","meta":{"csp":{...},"permissions":{},"prefersBorder":true}}

# A tool call from the page.
curl -s -X POST localhost:8080/api/chats/$ID/mcp-apps/call \
  -d '{"server":"connector:c1","tool":"refresh","arguments":{"zone":"UTC"}}'
# {"result":{"content":[{"type":"text","text":"..."}],"structuredContent":{...}}}
```

Rules the server applies:

- The resource must be a `ui://` URI that a tool of the same server declares. No
  other resource is read. It must be `text/html` (with the `mcp-app` profile or
  none) and at most 4 MiB.
- A tool call must name a tool of the same server whose `_meta.ui.visibility`
  includes `"app"` (the default is `["model","app"]`). A tool in the add-on
  namespace or with the name of a built-in tool is refused. The call has the same
  time limit as a call of the model, its arguments are at most 16 KiB, and its
  result is cut at 256 KiB.
- A tool with `visibility: ["app"]` is not offered to the model.
- The answers never hold a token or a header of a connector, and an error from the
  server is cleaned of them.
- In distributed mode, apps work for connectors. The MCP servers of the model run
  on workers there, so their apps answer `409` with the type `mcp_app_unsupported`.

The `tool_end` event and the transcript line of a call with a page carry
`app`: `server`, `tool`, `resource_uri` and `visibility`. The transcript line also
has `input` and `result` (the full MCP result, at most 256 KiB), which the page
needs. The text of the line keeps its usual 2 KiB cut, and the model sees only the
text of the result. The event has no `input` or `result`: read the transcript for them.

The web interface shows the page in an iframe with `sandbox="allow-scripts"` and
nothing else. The page has no access to the interface (no same-origin access, no
cookies, no storage), cannot navigate the top window and cannot open windows. Its
content security policy allows no network by default. A page can ask for `https`
and `wss` origins with `_meta.ui.csp` (`connectDomains`, `resourceDomains`,
`frameDomains` and `baseUriDomains`); the server and the interface drop anything
else. Browser permissions such as the camera are not granted. The interface reads
messages only from that frame and ignores the rest. It supports `ui/initialize`,
`tools/call`, `ui/notifications/size-changed` (height between 60 and 600 pixels),
`ui/message`, `ui/open-link` and `ui/request-display-mode`; any other request
gets the JSON-RPC error "method not found". A message that the page sends with
`ui/message` is shown as a suggestion that you send or dismiss, never sent by itself.
A link from `ui/open-link` must be `http` or `https` and opens only when you click
it. When the page cannot be loaded, the chat shows the result as text.

## Skills

A skill is a folder with a `SKILL.md`: a `name`, a one-line `description` and a procedure the model reads when a task matches. At each turn the chat's prompt lists the skills it has, with the path of each, and the model reads a skill with its file tools when it needs it. Other files in the skill's folder, such as references and scripts, are read the same way.

Skills come from four places, nearest first, and the nearest wins when two have the same name:

| Layer | Where | Who writes it |
|---|---|---|
| Folder | any `.skills` folder in your files | you |
| Personal | `.skills` in your home folder | you |
| Shared | for every user | an administrator |
| Built-in | ships with the server | the server |

The format is the same as the skills LocalAI already used, so an existing skill carries over by copying its folder into one of the places above. Git sources and zip import and export are not part of chats.

```bash
curl -X POST .../api/chats/skills -d '{"layer": "personal", "name": "review", "description": "Check a change before it ships", "body": "Read the diff.\nSay what you find."}'
curl .../api/chats/skills?chat=3f1c...            # what the chat has, with its switches
curl -X PUT .../api/chats/3f1c.../skills -d '{"off": ["review"]}'   # switch one off for this chat
```

A chat keeps its own switches. Turning a skill off takes it out of the index at the next turn. If another skill with the same name is further away, it shows through, as the list says. A deleted skill goes to the Trash like any file. Only an administrator writes shared skills, and everyone reads them. Your personal skills are as private as your files.

The **Skills** page for chats lists them by layer and edits personal skills, and a panel in the chat switches them per chat.

The server ships a few skills of its own, in the built-in layer: `docx`, `xlsx`, `pptx`, `pdf` and `writing-skills`. The office skills are adapted from the Feather project (MIT licence); the NOTICE file in the same folder keeps the source and the licence. They tell a model how to work with documents in a sandbox. For most edits the Office add-on's tools are simpler: they read, edit and create Word, Excel and PowerPoint files, and read PDF files, in your files.

## Collections

When you may use collections, a chat has two more tools: `list_collections` and `search_collection` (`collection`, `query`, `limit`). They search your own collections, the ones of the Collections page, and nobody else's: a name that is not yours is answered "collection not found". An answer holds at most 20 passages and about 16 KiB. A sub-agent gets these tools only when its parent names them.

## Add-on tools

A chat can use the tools of the add-ons you may use. The chat panel lists them with a switch for each add-on; a switch applies to that chat from its next turn. A tool runs as you, in your chat, and never as a user a model names.

- A tool that only reads gives the model its result.
- A tool that changes an add-on's data (rows, a schema, a new database, a job) changes it at once. Turning an add-on on for a chat or an agent is your consent, so there is no confirmation card. The model is told what was changed and whether it can be undone, and the tool entry in the chat says the same. Undo is the safety net, so a change the add-on kept a change set for shows an **Undo** button on its entry: Tables keeps one for every row and schema change and for a new database, and Fine-tuning and Quantization keep one for a job start (an undo stops the job while it runs). After you press it the entry says "Undone", or why it could not be: the data changed after the change (undoing would overwrite later work, and nothing is changed), the change is older than 7 days, or the job already finished. Only you can press it: the model is told the change can be undone but has no tool for it, and an API key cannot call it. Some changes keep no change set, such as one whose data was too large to keep, stopping a job or exporting a model; their entry says "This cannot be undone" and gives the reason. The file changes of a sandbox or an Office document are different: they are one change in the history of your files and you can undo them there. Reads and changes use your own permissions, and a change on data that moved since it was read is refused, not applied. A scheduled or file-triggered run uses the same rule, but a run with no agent gets no tool that changes anything: name the add-on in an agent to allow it.
- An *action* tool, such as running a command in a sandbox, runs at once and only in a chat. No other place that offers add-on tools (the OpenAI-compatible API, MCP, agents) lists or runs one.
- A sub-agent gets the add-on tools its parent names, or all of the parent's tools when the parent names none.

```bash
curl -s localhost:8080/api/chats/$ID/addons
# {"addons":[{"id":"office","tools":4,"off":false}]}
curl -s -X PUT localhost:8080/api/chats/$ID/addons -d '{"off":["office"]}'
```

## Metrics

With metrics on, `/metrics` counts every turn of the loop (a chat in the page, a
saved agent, a trigger run, a connector message and an agent job):

| Metric | Type | Labels |
|---|---|---|
| `localai_agent_runs_total` | counter | `agent` (the saved agent's name, empty for a plain chat), `origin` (`chat`, `trigger`, `connector` or `job`), `outcome` (`completed`, `error` or `cancelled`) |
| `localai_agent_run_seconds` | histogram | the same labels |

They keep the names the old agent pool had, so an existing dashboard works. The
`origin` label is new.

## Who can see a chat

A chat belongs to the user who started it. Another user's chat id always
answers `404`. Requests made on the user's behalf go through the same
model access rules and quotas as a direct request to `/v1/chat/completions`.

With authentication off, chats belong to one anonymous owner.

## Your files

Each user has a personal workspace: folders at any depth, uploads, a Trash and
a history with undo. Paths in the API are inside your own home folder, which is
`/`. You cannot name another user's files, and a path with `..` is refused.

```bash
curl -X PUT  ".../api/chats/files/content?path=/Reports/q1.txt" --data-binary @q1.txt
curl         ".../api/chats/files?path=/Reports"                    # list a folder
curl         ".../api/chats/files/content?path=/Reports/q1.txt"     # download
curl -X POST ".../api/chats/files/move" -d '{"from": "/Reports/q1.txt", "to": "/Archive/q1.txt"}'
curl -X DELETE ".../api/chats/files?path=/Archive"                  # to the Trash
curl         ".../api/chats/files/trash"                            # what can be restored
curl -X POST ".../api/chats/files/undo" -d '{"rev": 12}'            # restore it
```

- **Nothing is erased.** A delete goes to the Trash, and undo restores it as a new change. A change you made after the deletion that conflicts with the restore makes it answer `409`; with `skip_conflicts` the rest is restored.
- **Two uploads, one name.** An upload replaces a file only when it says which version it started from (`base`). Otherwise both are kept, and the new one is named `name (from you).ext`; the answer says so in `conflict_of`.
- **Downloads are safe.** Files are served as downloads. With `inline=1` an image, PDF, plain text, audio or video is shown by the browser; anything else, HTML and SVG included, never runs as a page.
- **Attachments.** Send a message as `multipart/form-data` with a `text` field and `files`. The files are saved in the chat's own folder under `Chats/` and the model gets their paths. Files go only with a message that starts a turn: with a turn running, the answer is `409`.
- **Search.** `GET /api/chats/files/search?q=budget` finds files by name and lines inside text files, ignoring case. There is no index: large files (over 1 MiB) and binary files are found by name only, and a very large workspace is scanned up to a limit.
- **The model can use your files.** A chat with files on gives the model `list_files`, `read_file`, `write_file`, `move_file`, `delete_file` and `search_files`, for your home folder only. What it changes is listed in the history as an agent change of that chat, and you can undo it; a delete goes to the Trash. Sub-agents do not get these tools.
- **Pictures and recordings.** `describe_image` lets the model look at an image in your files and `transcribe_audio` turns a recording into text. A voice note attached to a message is transcribed on the way in and its transcript goes with your message; the file is kept as it is. They use helper models, which you choose in the settings (Chat image model, Chat transcription model). Left empty, the first installed model that can see images, or transcribe, is used. With none installed the tool says what to install. The helpers run as you, so your model access and quota apply.
- **A file is at most 256 MiB.**

The **Files** page in the sidebar is a file browser for all of this: folders with a breadcrumb, upload by button or drag and drop, rename, delete to the Trash, a Trash tab that restores, a History tab that undoes a change, one search box over names and text, and a preview of text and images.

Files need a place to live. Where the tree and the bytes go:

| Setup | Tree | File contents |
|---|---|---|
| Authentication on | The auth database. | The data path, or the object store in a cluster. |
| No database, a data path set | A private SQLite file under `<data path>/chats`. | A folder under the data path. |
| No data path | none | none: files are off. |

When files are off, every files route and every attachment answers `409` with
the reason, the list endpoint returns `"files": false` and `files_reason`, and
everything else in the chats works. In a cluster the bytes go to the object
store, so a file written through one frontend is read through another.

## Storage

| Setup | Where conversations live |
|---|---|
| Authentication on (SQLite or PostgreSQL) | The auth database. |
| No database, a data path set | JSON files under `<data path>/chats`. They survive a restart. |
| No database and no data path | Memory only. They are lost on restart. |

When chats live in memory, the chats page says so, and the list endpoint
returns `"persistent": false`.

If a server stops in the middle of a model request, the turn ends as
interrupted. A message you sent while that turn ran and that the request had
already taken is waiting again, and "Send now" starts it.

A sub-agent conversation is read-only: it runs with the tools its parent chose,
and a message to it answers `409`.

The JSON files are for one LocalAI process. A cluster needs PostgreSQL, as
[distributed mode]({{% relref "features/distributed-mode" %}}) already does.

## Clusters

In [distributed mode]({{% relref "features/distributed-mode" %}}) any frontend can serve any request about any chat.

**State mode: shared.** Chats, their messages, the waiting messages and the claim that lets one turn run at a time live in PostgreSQL. Live events travel over the message bus, so a client connected to one frontend sees a turn that another frontend runs. Events are delivered at most once: a client that missed some reads the transcript.

**MCP connectors.** The records of your [MCP connectors](/features/chats-connectors/#mcp-servers) are shared in PostgreSQL. The open MCP sessions are per instance: each frontend opens its own, and a local command (`stdio`) is refused in a cluster.

**Where a turn runs.** The frontend that wins the claim runs the whole turn. The model calls it makes are routed to inference nodes, and its MCP tool calls are sent to agent workers, exactly as for any other request. Agent workers do not run chats: they have no database.

**From any frontend** you can send a message, edit or cancel a waiting message, read the transcript, stop the turn and stream events. A stop travels over the bus to the frontend that runs the turn.

**If a frontend dies.** A running turn records a heartbeat every 15 seconds. One frontend, chosen by a PostgreSQL advisory lock, ends turns that have not beaten for a minute. Such a turn ends as `interrupted`, its waiting messages stay, and `send-now` starts them. A standalone process with a database does the same once at startup, for turns its previous run left behind.

The JSON files are not supported in a cluster, as the Storage table says.

## Saved agents

An agent is a saved set of instructions and tool grants that a chat can start
with. See [Saved agents, triggers and inbox](/features/chats-agents/).

## The LocalAI assistant

An admin can start a chat that has the tools to manage the instance (install
models and backends, edit model configs, check status). It starts from the Home
page with `/assistant`, or over the API with `assistant: true` in
`POST /api/chats`. It is an ordinary chat in the list after that. Only that chat
has the tools, never its sub-agents. The `localai_assistant_enabled` setting
turns the feature on and off. See [LocalAI Assistant](/features/localai-assistant/).

## Moving from the old chat

The old chat page, the agents, the agent jobs and the old skills were replaced by the chats. At the first start the server converts what they left; see [Moving from the old chat and agents](/features/chats-migration/).

## API reference

The routes are listed under the `chats` tag at `/swagger/index.html`, and
`GET /api/instructions/chats` returns a machine-readable summary.
