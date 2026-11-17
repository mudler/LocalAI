+++
url = "/features/agent-jobs/"
disableToc = false
title = "Agent tasks and jobs"
weight = 30
devbuild = "feature"
+++

A **task** is a saved prompt template. A **job** is one run of a task. It runs in the background as a [chat]({{% relref "/features/chats-and-agents/chats" %}}) that you can open later. Use them to call LocalAI from a script, a CI pipeline or another service: start a job with parameters, poll it and read the answer, or have LocalAI call a webhook when it ends.

A job is an ordinary chat with origin `job`, so you can open it from **All chats** and read its transcript.

{{% callout style="note" %}}
The API needs the `mcp_jobs` feature, which is off by default for a user. An administrator has it always and can grant it to a user under **Users and keys**. See [Chats permissions]({{% relref "/administration/chats-deployment#permissions" %}}). `LOCALAI_DISABLE_CHATS` turns the jobs off with the rest of the chats. The examples below assume no authentication. When auth is on, add `-H "Authorization: Bearer $API_KEY"` to every call.
{{% /callout %}}

## A task

```bash
curl -X POST http://localhost:8080/api/agent/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "name": "weekly-summary",
    "model": "gemma-4-e2b-it-qat-q4_0",
    "prompt": "Summarise {{.topic}} in five lines.",
    "webhooks": [{"url": "https://ci.example.com/hook", "headers": {"Authorization": "Bearer <token>"}}]
  }'

| Field | Meaning |
|---|---|
| `name` | Unique per user, ignoring case. |
| `model` | The model the job talks to. May be empty when `agent_id` names a [saved agent]({{% relref "/features/chats-and-agents/chats-agents" %}}) that has a model. |
| `agent_id` | Optional. The job runs with the instructions and the grants of the saved agent (MCP servers, add-ons, collections, skills), as a chat with that agent does. Without it the job gets the tools a plain chat gets: the model's MCP servers when its `mcp.auto` is on, your MCP connectors and the add-ons you may use. To pin the MCP servers of a job, use an agent. |
| `prompt` | A Go template. `{{.name}}` is a parameter. A parameter the template names and the caller leaves out is a `400`. |
| `enabled` | `false` switches the task off without deleting it. |
| `webhooks` | Calls made when a job ends, see below. |

`GET`, `PUT` and `DELETE /api/agent/tasks/{id}` read, replace and delete one;
`GET /api/agent/tasks` lists yours. Deleting a task keeps its jobs until their
retention passes.

`cron`, `cron_parameters` and `multimedia_sources` are refused. A task does not
schedule itself: put the saved agent on a [trigger]({{% relref "/features/chats-and-agents/chats-agents" %}}#triggers).


## A job

```bash
curl -X POST http://localhost:8080/api/agent/jobs/execute \
  -H "Content-Type: application/json" \
  -d '{"task_id": "<id>", "parameters": {"topic": "the week"}}'
curl -X POST http://localhost:8080/api/agent/tasks/weekly-summary/execute \
  -H "Content-Type: application/json" \
  -d '{"topic": "the week"}'
curl http://localhost:8080/api/agent/jobs/<job_id>
```

Both calls answer `201` with `job_id`, `status` and the `url` to poll. The job
runs in the background.

| Job field | Meaning |
|---|---|
| `status` | `pending`, `running`, `completed`, `failed` or `cancelled`. Read from the chat. The Tasks page shows "in progress" for `running` and "done" for `completed`. |
| `result` | The last answer of the chat. |
| `error` | Why it failed, or `took longer than ...`. |
| `traces` | The reasoning and the tool results, in order. `GET` of one job only. |
| `parameters`, `triggered_by` | The inputs and where the run came from: `api` or `rerun`. |
| `started_at`, `completed_at`, `created_at` | Times. |
| `webhook_sent`, `webhook_sent_at`, `webhook_error` | The delivery of the webhooks. |

`GET /api/agent/jobs` lists your jobs, newest first, and takes `task_id`,
`status` and `limit`. `POST /api/agent/jobs/{id}/cancel` stops a running job
(`409` when it has ended). `POST .../{id}/rerun` starts a new job with the same
task and parameters. `DELETE .../{id}` stops the job if it runs and deletes it
with its chat.

**Attachments.** `images`, `videos`, `audios` and `files` take base64 data URIs
(`data:<type>;base64,<data>`), at most 8 of up to 20 MB each. They are saved in
your files for the job to read, so the files must be on (a data path is set).
Attachments must be data URIs. A URL is refused with a `400` that names the field. A rerun doesn't keep them.

**Limits.** A user can have 10 jobs running at once (`429`) and 200 tasks. A
job that runs longer than `LOCALAI_MCP_CI_JOB_TIMEOUT` (default `10m`; the server command reads it, the `agent-worker` command ignores it) is
stopped and ends as `cancelled` with the reason. Ended jobs and their chats are
removed after `LOCALAI_AGENT_JOB_RETENTION_DAYS` (default `30`).


## Webhooks

When a job ends the server calls each webhook of the task: `url`, `method`
(`POST`, `PUT` or `PATCH`; default `POST`), `headers` and an optional
`payload_template`. Without a template the body is JSON with `job_id`,
`task_id`, `task_name`, `status`, `result`, `error`, `parameters`, `started_at`
and `completed_at`. A template is a Go template with sprig functions and `json`
(not `env`, `expandenv` and `getHostByName`) over `.Job`, `.Task`, `.Result`,
`.Error`, `.Parameters` and `.Status`.

A failed call is retried three times, after 1, 2 and 4 seconds. The job keeps
`webhook_sent` and, when something failed, `webhook_error`. The status of the
job does not change.

The address must be `http` or `https`, with no credentials in it. An address on
a private or internal network (localhost, `10.x`, `192.168.x`, `.local`) is
refused, when the task is saved and again when the call is made, unless an
administrator allows it with the `mcp_connectors_allow_private` setting. Header
values may hold a credential, so the API never returns them: a header comes back
as `[hidden]`, and sending `[hidden]` back in an update keeps the stored value.
Headers are stored in the task store, not encrypted separately. Use a token you can rotate.


## The Tasks page

**Agents**, then **Tasks** (`/app/agent-tasks`) lists your tasks with who runs them,
the values their prompt asks for, and how the last run went. Search the list,
or show only the tasks that are on or off. Each row has an **On** switch,
**Run** and a menu with **Open**, **Edit** and **Delete**.

- **New task** opens a side sheet: a name, a description, a model or one of
  your saved agents, the prompt, and on or off. The sheet lists the parameters
  the prompt names as you type, and says which required field is missing when
  you save.
- **Run** starts a task at once when its prompt names no parameter. Otherwise a
  sheet asks for each value and shows the prompt as the task receives it.
- **Open** shows the task in a pane with three tabs: **Overview** (the prompt,
  the values it asks for and a `curl` call that runs it by name), **Runs** (its
  jobs) and **Settings** (on or off, its webhooks, delete).
- **Run history** lists every job by day. Filter it by state (in progress,
  done, failed, cancelled) and by task. Open a job to see the values it ran
  with, its answer or error (with **Copy**), its steps, the webhook outcome and
  a link to the chat that holds it. **Cancel** stops a job in progress; **Run
  again** and **Delete run** act on a finished one.

Set webhooks through the API. The Tasks page doesn't show or change them.
An administrator can switch on **All users** to see the tasks and jobs of
every user, read-only.


## Who can see them

Tasks and jobs belong to their owner; another user's id is `404`. An
administrator can read any job (`GET /api/agent/jobs/{id}`) and list every user's
tasks and jobs with `?all_users=true`, which adds a `user_groups` object keyed by
owner. The view is read-only. To switch off or delete what a user set up, see the
[administrator's view]({{% relref "/administration/chats-deployment#permissions" %}}) of agents, triggers and
connectors under `/api/chats/admin`.


## Clusters

Tasks and job records are in the database, shared by every frontend. A job runs on the frontend that received the request, unless the frontends send unattended turns to [chat workers]({{% relref "/administration/distributed-mode" %}}#chat-workers). If that process dies, the server records the end of the job and delivers its webhooks. It also removes jobs past their retention.
