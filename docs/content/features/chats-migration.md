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

## What stays

The [collections](/features/runtime-settings/#chats-and-collections-settings) and their API, the terminal agent (`local-ai chat`), and the agent worker of a cluster, which serves the MCP tool calls of the frontend. The LocalAGI and localrecall modules stay as the backend of the collections only.

## What was removed

These APIs answer `404`:

| Removed | Use instead |
|---|---|
| `/api/agents` (agents, actions, SSE, export and import) | `/api/chats/agents` and `/api/chats` |
| `/api/agents/skills` and `/api/agents/git-repos` | `/api/chats/skills` |
| `/api/agent/tasks` and `/api/agent/jobs` | `/api/chats/triggers` |
| `/api/conversations` | `/api/chats` |

`/api/agents/collections` stays: it is the [collections](/features/runtime-settings/#chats-and-collections-settings) API.

A request to `/v1/responses` whose `model` names a former agent is a plain request for a model of that name.

The pages `/app/chat`, `/app/agents`, `/app/agent-jobs` and `/app/skills` redirect to the matching page of the chats. The model in `/app/chat/<model>` becomes `?model=<model>` and the chats page selects it.

The features `agents`, `skills`, `mcp_jobs` and `chat_history` are gone from the user permissions. A user that had them keeps the `chats` and `collections` features as they were; the old keys in a stored permission map are ignored.

## Options that are ignored now

These flags and environment variables are accepted and do nothing, and the server logs one warning that names the ones that were set. Remove them from your configuration.

`LOCALAI_AGENT_JOB_RETENTION_DAYS` (and `AGENT_JOB_RETENTION_DAYS`), `LOCALAI_AGENT_POOL_MULTIMODAL_MODEL`, `LOCALAI_AGENT_POOL_TRANSCRIPTION_MODEL`, `LOCALAI_AGENT_POOL_TRANSCRIPTION_LANGUAGE`, `LOCALAI_AGENT_POOL_TTS_MODEL`, `LOCALAI_AGENT_POOL_TIMEOUT`, `LOCALAI_AGENT_POOL_ENABLE_SKILLS`, `LOCALAI_AGENT_POOL_CUSTOM_ACTIONS_DIR`, `LOCALAI_AGENT_POOL_ENABLE_LOGS` and `LOCALAI_AGENT_HUB_URL`. For `local-ai agent-worker`: `LOCALAI_AGENT_SUBJECT`, `LOCALAI_AGENT_QUEUE`, `LOCALAI_MCP_CI_JOB_TIMEOUT`, `LOCALAI_API_URL` and `LOCALAI_API_TOKEN`.

The `agent_job_retention_days`, `agent_pool_enable_logs` and `agent_pool_agent_hub_url` keys of `runtime_settings.json` are ignored too. The command `local-ai agent` is gone.

Kept, because the collections use them: `LOCALAI_DISABLE_AGENTS` (it now turns off the collections), `LOCALAI_AGENT_POOL_STATE_DIR`, `LOCALAI_AGENT_POOL_DEFAULT_MODEL`, `LOCALAI_AGENT_POOL_API_URL`, `LOCALAI_AGENT_POOL_API_KEY`, `LOCALAI_AGENT_POOL_VECTOR_ENGINE`, `LOCALAI_AGENT_POOL_EMBEDDING_MODEL`, `LOCALAI_AGENT_POOL_DATABASE_URL`, `LOCALAI_AGENT_POOL_MAX_CHUNKING_SIZE`, `LOCALAI_AGENT_POOL_CHUNK_OVERLAP` and `LOCALAI_AGENT_POOL_COLLECTION_DB_PATH`.

## In a cluster

The agent dispatch and job queues are gone from distributed mode: the NATS subjects `agent.*` and `jobs.*`, the queues and their permissions. The agent worker keeps serving the MCP tool and discovery calls. The old tables stay in the database untouched; the conversion reads them, and you can drop them when you no longer need them.

## Storage and clusters

The marks and the report are stored beside the chats: in the database when
there is one, in `conversion.json` (mode `0600`) under the data path
otherwise. This is how the state behaves with several frontends:

| State | Mode | How |
|---|---|---|
| Marks and report | Shared | Rows in the database. |
| Who converts | Single runner | The frontend that holds the advisory lock (`KeyChatsConvert`) converts; another finds the marks when its turn comes and does nothing. |
