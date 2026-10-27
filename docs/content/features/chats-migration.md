+++
disableToc = false
title = "Chats: moving from the old chat, agents and skills"
weight = 24
url = "/features/chats-migration/"
+++

[Chats](/features/chats/) replace the old chat page, the old agents, the agent
jobs and the old skills. At start the server reads what those left on disk and
in the database, and makes the matching definitions of the chats. It never
changes or deletes the old data.

## What is converted

| Old | New |
|---|---|
| An agent of the pool | A [saved agent](/features/chats-agents/): instructions, model, the collection named like the agent, selected skills |
| An agent that ran by itself (periodic) | A [trigger](/features/chats-agents/) on the agent, **switched off** |
| A scheduled task with a cron text | A trigger, **switched off**; `{{.name}}` parts of the prompt are filled from the task's parameters |
| A Telegram connector of an agent | A [connector binding](/features/chats-connectors/), **switched off**; the old `admins` become the allowlist |
| A skill folder (shared or personal) | A shared or personal skill |
| A conversation saved in the database | A chat with its transcript (the newest 5000 messages) |

A converted chat keeps what was said, not the model's memory of it. A
conversation kept only in a browser cannot be converted: it was never on the
server.

Nothing converted acts by itself. Switch a trigger or a connector on after you
have read it.

## What is not converted

Every dropped field is named in the report, without its value. The main ones:
actions, MCP servers set on an agent, knowledge-base settings other than the
collection grant, planning and memory switches, agent jobs and webhooks, skills
from a git repository, skill resource files, and connectors other than
Telegram. A name that is taken becomes `Name (2)`.

## The report

```
curl .../api/chats/conversion
```

It returns your items: `kind` (`agent`, `trigger`, `skill`, `chat`,
`connector`), `name`, `status` (`converted`, `skipped`, `failed`, `dropped`)
and a `reason`. It never holds a token or the text of a prompt. An
administrator also gets `totals` for all users. Each user with something new
gets one notification in the [inbox](/features/chats-agents/).

A run is safe to repeat. A converted item is marked and is not converted
again; one that failed is tried again at the next start.

## Storage and clusters

The marks and the report are stored beside the chats: in the database when
there is one, in `conversion.json` (mode `0600`) under the data path
otherwise. This is how the state behaves with several frontends:

| State | Mode | How |
|---|---|---|
| Marks and report | Shared | Rows in the database. |
| Who converts | Single runner | The frontend that holds the advisory lock (`KeyChatsConvert`) converts; another finds the marks when its turn comes and does nothing. |
