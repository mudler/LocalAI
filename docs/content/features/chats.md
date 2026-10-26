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
