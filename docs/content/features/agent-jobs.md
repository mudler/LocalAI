+++
disableToc = false
title = "Agent tasks and jobs"
weight = 25
url = "/features/agent-jobs/"
+++

A **task** is a saved prompt template. A **job** is one run of a task: a
[chat](/features/chats/) that runs in the background, as the owner of the task,
with the same loop as every other chat. Use them to call LocalAI from a script,
a CI pipeline or another service: start a job with parameters, poll it, and read
the answer, or have LocalAI call a webhook when it ends.

The API keeps the paths and the JSON it had before the chats. A job is an
ordinary chat with origin `job`, so you can open it in the chats page and read
its transcript.

{{% notice note %}}
The API needs the `mcp_jobs` feature, which is off by default for a user. An
administrator grants it in **Settings → Users and keys** and has it always. See
[Chats permissions](/features/chats/#permissions). `LOCALAI_DISABLE_CHATS`
turns the jobs off with the rest of the chats.
{{% /notice %}}

## The Tasks page

**Agents > Tasks** (`/app/agent-tasks`) lists your tasks and their jobs. It
creates and edits a task (name, description, a model or one of your saved
agents, the prompt, on or off), runs one (it asks for each parameter the
prompt names), and shows each job's status, result, traces and webhook
outcome, with **Cancel**, **Run again** and **Delete**. Webhooks are set over
the API; the page keeps a task's webhooks as they are. An administrator can
tick **All users** to see the tasks and jobs of every user, read-only.

## A task

```
curl -X POST .../api/agent/tasks -d '{
  "name": "weekly-summary",
  "model": "my-model",
  "prompt": "Summarise {{.topic}} in five lines.",
  "webhooks": [{"url": "https://ci.example.com/hook", "headers": {"Authorization": "Bearer ..."}}]
}'
```

| Field | Meaning |
|---|---|
| `name` | Unique per user, ignoring case. |
| `model` | The model the job talks to. May be empty when `agent_id` names a [saved agent](/features/chats-agents/) that has a model. |
| `agent_id` | Optional. The job runs with the instructions and the grants of the saved agent (MCP servers, add-ons, collections, skills), as a chat with that agent does. Without it the job gets the tools a plain chat gets: the model's MCP servers when its `mcp.auto` is on, your MCP connectors and the add-ons you may use. To pin the MCP servers of a job, use an agent. |
| `prompt` | A Go template. `{{.name}}` is a parameter. A parameter the template names and the caller leaves out is a `400`. |
| `enabled` | `false` switches the task off without deleting it. |
| `webhooks` | Calls made when a job ends, see below. |

`GET`, `PUT` and `DELETE /api/agent/tasks/{id}` read, replace and delete one;
`GET /api/agent/tasks` lists yours. Deleting a task keeps its jobs until their
retention passes.

`cron`, `cron_parameters` and `multimedia_sources` are refused. A task does not
schedule itself: put the saved agent on a [trigger](/features/chats-agents/#triggers).

## A job

```
curl -X POST .../api/agent/jobs/execute -d '{"task_id": "<id>", "parameters": {"topic": "the week"}}'
curl -X POST .../api/agent/tasks/weekly-summary/execute -d '{"topic": "the week"}'
curl .../api/agent/jobs/<job_id>
```

Both calls answer `201` with `job_id`, `status` and the `url` to poll. The job
runs in the background.

| Job field | Meaning |
|---|---|
| `status` | `pending`, `running`, `completed`, `failed` or `cancelled`. Read from the chat. |
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
An address is never fetched: the server makes no request for a caller, and
`400` names the field. They are not kept for a rerun.

**Limits.** A user can have 10 jobs running at once (`429`) and 200 tasks. A
job that runs longer than `LOCALAI_MCP_CI_JOB_TIMEOUT` (default `10m`) is
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
The headers are stored as they are in the task store, which is protected like the
rest of the chats data. Do not put a secret you cannot rotate in a header.

## Who can see them

Tasks and jobs belong to their owner; another user's id is `404`. An
administrator can read any job (`GET /api/agent/jobs/{id}`) and list every user's
tasks and jobs with `?all_users=true`, which adds a `user_groups` object keyed by
owner. The view is read-only. To switch off or delete what a user set up, see the
[administrator's view](/features/chats/#permissions) of agents, triggers and
connectors under `/api/chats/admin`.

## Clusters

Tasks and job records are in the database, shared by every frontend. A job is
watched by the frontend that received the request. Its turn runs there too,
unless the frontends send unattended turns to
[chat workers](/features/distributed-mode/#chat-workers): then a chat worker runs
it, and the frontend still watches it and stops it at the run limit. If the
process that runs the turn dies, the chat is ended by the usual reaper, and the
leader's sweep (an advisory lock) records the end and delivers the webhooks of a
job whose frontend is gone. The same sweep removes jobs past their retention.
