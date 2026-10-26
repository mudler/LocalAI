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
permissions.
{{% /notice %}}

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
- **A file is at most 256 MiB.**

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

**Where a turn runs.** The frontend that wins the claim runs the whole turn. The model calls it makes are routed to inference nodes, and its MCP tool calls are sent to agent workers, exactly as for any other request. Agent workers do not run chats: they have no database.

**From any frontend** you can send a message, edit or cancel a waiting message, read the transcript, stop the turn and stream events. A stop travels over the bus to the frontend that runs the turn.

**If a frontend dies.** A running turn records a heartbeat every 15 seconds. One frontend, chosen by a PostgreSQL advisory lock, ends turns that have not beaten for a minute. Such a turn ends as `interrupted`, its waiting messages stay, and `send-now` starts them. A standalone process with a database does the same once at startup, for turns its previous run left behind.

The JSON files are not supported in a cluster, as the Storage table says.

## API reference

The routes are listed under the `chats` tag at `/swagger/index.html`, and
`GET /api/instructions/chats` returns a machine-readable summary.
