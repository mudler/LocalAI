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
| An MCP server of an agent (`mcp_servers`: address and token) | An [MCP connector](/features/chats-connectors/#mcp-servers) of the agent's owner, **switched off**, named in the new agent as `connector:<id>` |
| A local MCP command of an agent (`mcp_stdio_servers`) | The same, only when the owner is an administrator; otherwise it is dropped, with the reason |
| A Telegram connector of an agent | A [connector binding](/features/chats-connectors/), **switched off**; the old `admins` become the allowlist |
| A skill folder (shared or personal) | A shared or personal skill |
| A conversation saved in the database | A chat with its transcript (the newest 5000 messages) |

A scheduled task ran in the time zone of the server. The trigger made from it
keeps that zone, and the report names it. When the server's zone has no name
(plain local time), the trigger runs in UTC and the report says so: check the
schedule.

An agent that the old pool deleted (a row with status `deleted`) is not
converted, and neither are its periodic trigger and its Telegram connector.

A converted chat keeps what was said, not the model's memory of it. A
conversation kept only in a browser cannot be converted: it was never on the
server.

Nothing converted acts by itself. Switch a trigger, a Telegram connector or an
MCP connector on after you have read it. A converted MCP connector keeps the
token or the environment it had, sealed on the server; test it, check its
address (an address on a private network cannot be switched on unless an
administrator allows it), then turn it on. The report names each one as
`connector`, `Agent: MCP server 1`, without the address. The agent is converted
with the connectors it names; if one of them fails for a reason that may pass
(a full disk), the agent waits for the next start so that its grant is not lost.
An agent that an earlier start already converted keeps what it had: make its MCP
connectors again on the Connectors page.

## What is not converted

Every dropped field is named in the report, without its value. The main ones:
actions, the prepare script of MCP servers, knowledge-base settings other than the
collection grant, planning and memory switches, the webhooks of old tasks, skills
from a git repository, skill resource files, and connectors other than
Telegram. A name that is taken becomes `Name (2)`. A task is converted into a
trigger; to run it through the [agent jobs API](/features/agent-jobs/) with
parameters and a webhook, make it again as a task.

The Telegram connector of the chats reads private text messages only. A bot
converted from the old pool no longer answers photos, voice notes or group
chats. Saved agents have no export or import: copy the definition through
`GET /api/chats/agents/{id}` and `POST /api/chats/agents`.

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
again. An item that cannot convert for good, such as a task with no cron text,
a name or an instruction text that is too long, a skill name that is not valid,
a cron text with month or day names, or more than 50 triggers, is marked as
dropped, with the reason, and is not met again. One that failed for another
reason, such as a full disk, is tried again at the next start.

The report keeps the first word on an item: the status and the reason of the
run that converted or dropped it (for example `renamed to Name (2)`, or
`dropped: actions, enable_planning`) stay when later starts only find it done.
You are told once about what is new. A failure that comes back with the same
reason at each start does not send a new notification.

An agent with a knowledge base gets the grant on the collection named like it.
The conversion runs after the collections have started. When the collections
are off or cannot be listed, the agent is not converted: it is reported as
failed and converts at a start with the collections on. It is never marked
converted without its grant.

A source that cannot be read (a cut `pool.json`, a broken `agent_tasks.json` of
one user, an agent row whose configuration is not valid JSON) is a failed item.
The other sources and users convert all the same.

The history is read page by page and one conversation at a time, off the start-up
path. A conversation is marked before its import. If the server stops in the
middle of an import, the next start removes the half chat and imports again, so
a chat is never twice.

## What stays

The [collections](/features/runtime-settings/#chats-and-collections-settings) and their API, the terminal agent (`local-ai chat`), and the agent worker of a cluster, which serves the MCP tool calls of the frontend. The collections backend is built on localrecall; LocalAI no longer depends on LocalAGI.

## What was removed

These APIs answer `404`:

| Removed | Use instead |
|---|---|
| `/api/agents` (agents, actions, SSE) | `/api/chats/agents` and `/api/chats`. Export and import of an agent have no replacement. |
| `/api/agents/skills` and `/api/agents/git-repos` | `/api/chats/skills` |
| `/api/conversations` | `/api/chats` |

The [collections](/features/runtime-settings/#chats-and-collections-settings) API moved from `/api/agents/collections` to `/api/collections`. The old path still works and answers with a `Deprecation: true` header; switch to the new one.

A request to `/v1/responses` whose `model` names a former agent is a plain request for a model of that name.

The agent tasks and jobs API (`/api/agent/tasks` and `/api/agent/jobs`) is back, with the paths and JSON it had; see [Agent jobs](/features/agent-jobs/). A job is now a background chat of its owner. Scheduling a task is done with a [trigger](/features/chats-agents/#triggers), not with a cron text on the task.

The pages `/app/chat`, `/app/agents`, `/app/agent-jobs` and `/app/skills` redirect to the matching page of the chats. The model in `/app/chat/<model>` becomes `?model=<model>` and the chats page selects it.

The features `skills` and `chat_history` are gone from the user permissions; their old keys in a stored permission map are ignored. The features `agents` and `mcp_jobs` are back, with `triggers` and `connectors` beside them, all off by default for a user; a stored `agents` or `mcp_jobs` value carries over, and a one-time rule keeps the access of the users who use them (see [Permissions](/features/chats/#permissions)).

## Options that are ignored now

These flags and environment variables are accepted and do nothing, and the server logs one warning that names the ones that were set. Remove them from your configuration.

`LOCALAI_AGENT_POOL_MULTIMODAL_MODEL`, `LOCALAI_AGENT_POOL_TRANSCRIPTION_MODEL`, `LOCALAI_AGENT_POOL_TRANSCRIPTION_LANGUAGE`, `LOCALAI_AGENT_POOL_TTS_MODEL`, `LOCALAI_AGENT_POOL_TIMEOUT`, `LOCALAI_AGENT_POOL_ENABLE_SKILLS`, `LOCALAI_AGENT_POOL_CUSTOM_ACTIONS_DIR`, `LOCALAI_AGENT_POOL_ENABLE_LOGS` and `LOCALAI_AGENT_HUB_URL`. For `local-ai agent-worker`: `LOCALAI_AGENT_SUBJECT`, `LOCALAI_AGENT_QUEUE`, `LOCALAI_MCP_CI_JOB_TIMEOUT`, `LOCALAI_API_URL` and `LOCALAI_API_TOKEN`.

`LOCALAI_AGENT_JOB_RETENTION_DAYS` (and `AGENT_JOB_RETENTION_DAYS`) and `LOCALAI_MCP_CI_JOB_TIMEOUT` work again for the server: they set how long ended [agent jobs](/features/agent-jobs/) are kept and how long a job may run. For `local-ai agent-worker` the timeout is still ignored.

The `agent_job_retention_days`, `agent_pool_enable_logs` and `agent_pool_agent_hub_url` keys of `runtime_settings.json` are ignored too: set the retention with the flag or the environment variable. The command `local-ai agent` is gone.

Renamed, because the [collections](/features/collections/) use them. The old names still work and the server logs one deprecation warning for each one that is set. When both names are set, the new one wins.

| Old name | New name |
|---|---|
| `LOCALAI_DISABLE_AGENTS` | `LOCALAI_DISABLE_CHATS` and `LOCALAI_DISABLE_COLLECTIONS`. The old name keeps its meaning (the whole agent pool, knowledge base included). `LOCALAI_DISABLE_TRIGGERS` and `LOCALAI_DISABLE_CONNECTORS` switch off one part. See [Chats](/features/chats/#switch-the-chats-off). |
| `LOCALAI_AGENT_POOL_STATE_DIR` | `LOCALAI_COLLECTIONS_STATE_DIR` |
| `LOCALAI_AGENT_POOL_DEFAULT_MODEL` | `LOCALAI_COLLECTIONS_DEFAULT_MODEL` |
| `LOCALAI_AGENT_POOL_API_URL` | `LOCALAI_COLLECTIONS_API_URL` |
| `LOCALAI_AGENT_POOL_API_KEY` | `LOCALAI_COLLECTIONS_API_KEY` |
| `LOCALAI_AGENT_POOL_VECTOR_ENGINE` | `LOCALAI_COLLECTIONS_VECTOR_ENGINE` |
| `LOCALAI_AGENT_POOL_EMBEDDING_MODEL` | `LOCALAI_COLLECTIONS_EMBEDDING_MODEL` |
| `LOCALAI_AGENT_POOL_DATABASE_URL` | `LOCALAI_COLLECTIONS_DATABASE_URL` |
| `LOCALAI_AGENT_POOL_MAX_CHUNKING_SIZE` | `LOCALAI_COLLECTIONS_MAX_CHUNKING_SIZE` |
| `LOCALAI_AGENT_POOL_CHUNK_OVERLAP` | `LOCALAI_COLLECTIONS_CHUNK_OVERLAP` |
| `LOCALAI_AGENT_POOL_COLLECTION_DB_PATH` | `LOCALAI_COLLECTIONS_DB_PATH` |

The keys of `runtime_settings.json` changed the same way: `agent_pool_enabled`, `agent_pool_default_model`, `agent_pool_embedding_model`, `agent_pool_max_chunking_size`, `agent_pool_chunk_overlap`, `agent_pool_collection_db_path`, `agent_pool_vector_engine` and `agent_pool_database_url` are now `collections_enabled`, `collections_default_model`, `collections_embedding_model`, `collections_max_chunking_size`, `collections_chunk_overlap`, `collections_db_path`, `collections_vector_engine` and `collections_database_url`. The file is still read with the old keys; the server writes the new ones.

## In a cluster

The agent dispatch and job queues are gone from distributed mode: the NATS subjects `agent.*` and `jobs.*`, the queues and their permissions. The agent worker keeps serving the MCP tool and discovery calls. A worker no longer gets an API user or key when it registers, and the old worker user of a node is removed when that node registers again. On the collections routes, `?user_id=` is now for administrators only. If you made NATS credentials with `scripts/nats-auth-setup.sh`, make them again: the frontend user needs the `chats.>` subjects. The old tables stay in the database untouched; the conversion reads them, and you can drop them when you no longer need them.

## Storage and clusters

The marks and the report are stored beside the chats: in the database when
there is one, in `conversion.json` (mode `0600`) under the data path
otherwise. This is how the state behaves with several frontends:

| State | Mode | How |
|---|---|---|
| Marks and report | Shared | Rows in the database. |
| Who converts | Single runner | The frontend that holds the advisory lock (`KeyChatsConvert`) converts; another finds the marks when its turn comes and does nothing. |
