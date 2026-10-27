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
| `mcp_servers` | Names of servers in the model's MCP configuration. |
| `addons` | IDs of installed add-ons. |
| `collections` | Names of your collections. |
| `skills` | Names of skills. |

Each list is an exact grant. An empty list grants nothing: a chat with an agent
that names no add-ons has no add-on tools, even when you could use some. A
plain chat, with no agent, is not changed.

### An agent never gives more than you have

A chat gets the intersection of the agent's lists and what you may use at that
moment. If an add-on is disabled, a collection is deleted or you lose the MCP
feature, the chat still starts. The thing is absent, and the model gets one
line at the start of the turn, for example `unavailable this turn: add-on
office, collection docs`. A sub-agent of the chat can only get tools the chat
has.

If you delete an agent, the chats that ran with it keep their history. Their
next turn runs without the agent's tools, and the model is told that the agent
was deleted.

### API

```
curl -X POST .../api/chats/agents -d '{"name": "Reviewer", "instructions": "Answer in French.", "addons": ["office"], "skills": ["review"]}'
curl .../api/chats/agents
curl -X POST .../api/chats -d '{"agent_id": "<id>", "model": "my-model"}'
```

`GET`, `PUT` and `DELETE /api/chats/agents/{id}` read, replace and delete one.
A `PUT` replaces every field. Chats that already run with the agent use the new
definition from their next turn. Another user's agent is always `404`.

Agents belong to the `chats` feature. They have no administration tools, as
they are your own settings.

## Inbox

The inbox lists what ended while you were elsewhere: a trigger run that
finished or failed, or something that needs you. Each item has a kind
(`run_ended`, `run_failed` or `needs_attention`), a status, a short title and
text, and a link to the chat it is about. A chat that a run started is unread
until you open it: opening the chat marks its notifications read.

You keep at most 500 notifications, none older than 90 days.

```
curl ".../api/chats/notifications?unread=1"          # the unread ones, and the unread count
curl .../api/chats/notifications/count
curl -X POST .../api/chats/notifications/read -d '{"all": true}'
curl -X DELETE .../api/chats/notifications -d '{"ids": [12, 13]}'
curl -N .../api/chats/notifications/events            # new ones as server-sent events
```

Events are live only. A client that was away lists the inbox. In a cluster, a
notification added on one frontend reaches the clients connected to any other.

## Storage and clusters

Saved agents and notifications are stored beside your chats: in the database when there is one,
in JSON files under the data path otherwise, and in memory when there is no
data path (the page then says persistent features are off). A cluster needs
PostgreSQL, as the chats do. The JSON store is for a single process.
