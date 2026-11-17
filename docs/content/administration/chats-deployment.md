+++
disableToc = false
title = "Chats on a server: permissions, storage and clusters"
description = "Grant chat features to users, switch parts off, choose where chats and files are stored, and run chats in a cluster."
weight = 135
devbuild = "feature"
+++

Run chats on a server. Grant chat features to users, switch parts off, choose where chats and files are stored, and run chats in a cluster. The user guide is [Chats]({{% relref "/features/chats-and-agents/chats" %}}).

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
| `connectors` | off | Telegram and Slack bindings (`/api/chats/connectors`): a public door to one of your agents. |
| `mcp_jobs` | off | The agent tasks and jobs API (`/api/agent/...`). |
| `pages` | off | The pages of the workspace (`/api/chats/pages...`): HTML pages in `pages/` of the home, shown in a sandboxed frame. See [Pages]({{%relref "/features/pages-and-home/pages" %}}). |

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
| `/api/chats/admin/shared-folders...` | Create shared folders and choose who may use them (see [Shared folders]({{% relref "/features/files-and-documents/files-reference#shared-folders" %}})) |

The agent tasks and jobs have their own view: `?all_users=true` on
`GET /api/agent/tasks` and `GET /api/agent/jobs` (see [Agent tasks and
jobs]({{% relref "/features/chats-and-agents/agent-jobs" %}})).

## Switch the chats off

The operator can switch the whole surface or one part off. A part that is off
starts nothing: no loop, no trigger runner, no bot. `GET /api/features` reports
what runs (`chats`, `agents`, `triggers`, `connectors`).

| Setting | Default | Effect |
|---|---|---|
| `LOCALAI_DISABLE_CHATS` | `false` | Turns off the chats: the `/api/chats` API, saved agents, triggers, connectors, notifications and the agent jobs (`/api/chats/*`, `/api/workspace/*` and `/api/agent/*` answer `404` with the type `chats_off`). The files workspace of the add-ons is not affected. |
| `LOCALAI_DISABLE_TRIGGERS` | `false` | Turns off the trigger runner. Trigger routes answer `409 triggers_off`. |
| `LOCALAI_DISABLE_CONNECTORS` | `false` | Turns off the connector bots. Connector routes answer `409 connectors_off`. |
| `LOCALAI_DISABLE_CONNECTOR_TALKBACK` | `false` | Turns off `send_message` and the reports of triggers to a channel for every connector binding. |
| `LOCALAI_SKIP_BINDING_CAPS_MIGRATION` | `false` | Skips the one-time grant of the usual permissions to connector bindings that have none, and the one-time add of `loops.wait` to existing bindings. |
| `LOCALAI_DISABLE_AGENTS` | `false` | Deprecated. It kept the meaning it had in the old agent pool, which also held the knowledge base: it sets `LOCALAI_DISABLE_CHATS` and `LOCALAI_DISABLE_COLLECTIONS`, and logs a warning. |

`LOCALAI_DISABLE_COLLECTIONS` stays separate and turns off only the
[collections]({{% relref "/features/files-and-documents/collections" %}}).

## Storage

| Setup | Where conversations live |
|---|---|
| Authentication on (SQLite or PostgreSQL) | The auth database. |
| No database, a data path set | JSON files under `<data path>/chats`. They survive a restart. |
| No database and no data path (`--data-path=-`) | Memory only. They are lost on restart, and files are off. |

When chats live in memory, the chat page says so, and the list endpoint
returns `"persistent": false`.

If a server stops in the middle of a model request, the turn ends as
interrupted. A message you sent while that turn ran and that the request had
already taken is waiting again, and "Send now" starts it.

A sub-agent conversation is read-only: it runs with the tools its parent chose,
and a message to it answers `409`.

The JSON files are for one LocalAI process. A cluster needs PostgreSQL, as
[distributed mode]({{% relref "/administration/distributed-mode" %}}) already does.

## Clusters

In [distributed mode]({{% relref "/administration/distributed-mode" %}}) any frontend can serve any request about any chat.

**State mode: shared.** Chats, their messages, the waiting messages and the claim that lets one turn run at a time live in PostgreSQL. Live events travel over the message bus, so a client connected to one frontend sees a turn that another frontend runs. Events are delivered at most once. The ring of recent events that serves a reconnect is per instance, on purpose: it is a cache of the live tail on the frontend or chat worker that runs the turn, and the transcript is the record. The process that runs a turn records its instance id (`LOCALAI_INSTANCE_ID`) on the chat. A reconnect that reaches another frontend asks that process for the missed events over the message bus (request and reply on `chats.replay.<instance>`), in replies of at most 512 KiB. When that process is gone, does not answer in 2 seconds, or no longer has the events, the client gets a `resync` and reads the transcript. The cap of 8 event streams per chat and user is per frontend.

**Sub-agents.** The runs are chats in the shared store, linked to their parent. The background limit, the list of runs and "stopping a chat stops its sub-agents" all read the store, so they hold when the turns of one chat run on different frontends. A stop reaches a run on another frontend over the bus. The tool subset of a running sub-agent is kept in the memory of the frontend that runs it, which is where it runs. A sub-agent started from a saved agent keeps nothing in memory (stateless): the agent is named on its chat record, and its tools are read from the store at each turn, as for any chat with an agent. The chat's saved agents switch is stored with the chat, and the permission is checked again at each call, so every frontend gives the same answer.

**Settings and edits.** The settings, the title and the model of a chat, and the cut that regenerate and edit make, are stored with the chat. A change of title or model, a regenerate and an edit take the same turn claim as a message, so they are refused with 409 while a turn runs on any frontend. A video that comes with a message is held in the memory of the frontend that runs the turn, for that turn's first request.

**MCP connectors.** The records of your [MCP connectors]({{% relref "/features/chat-apps/mcp-servers#mcp-servers" %}}) are shared in PostgreSQL. The open MCP sessions are per instance: each frontend opens its own, and a local command (`stdio`) is refused in a cluster.

**Where a turn runs.** The frontend that wins the claim runs the whole turn. The model calls it makes are routed to inference nodes, and its MCP tool calls are sent to agent workers, exactly as for any other request. Agent workers do not run chats: they have no database.

**From any frontend** you can send a message, edit or cancel a waiting message, read the transcript, stop the turn and stream events. A stop travels over the bus to the frontend that runs the turn.

**If a frontend dies.** A running turn records a heartbeat every 15 seconds. One frontend, chosen by a PostgreSQL advisory lock, ends turns that have not beaten for a minute. Such a turn ends as `interrupted`, its waiting messages stay, and `send-now` starts them. A standalone process with a database does the same once at startup, for turns its previous run left behind. On a normal stop the server marks the turns it cuts short as interrupted itself, and it does not wait for them for good: closing the chats is bounded (15 seconds), and a turn that does not stop in time is marked interrupted in the database before the process exits, with its resume scheduled like a turn that died.

**Resuming by itself.** With `--chats-auto-resume` (`LOCALAI_CHATS_AUTO_RESUME`, off by default) the server goes on with an interrupted turn without anyone pressing Retry. The state lives in the chat's row, so it survives a restart of any process: a count of tries, the time of the interruption and the time of the next try. The same frontend that ends stale turns checks for due resumes at each tick (30 seconds), and a chat worker takes the start when workers are on. The claim on the chat decides when two frontends race, or when a person presses Retry at the same moment: one turn starts. A turn is resumed at most 3 times, after 15 seconds, 1 minute and 5 minutes, and only within 30 minutes of the interruption. A new message from you starts a normal turn and the count begins again. While a resume is scheduled the chat reads `will_resume: true` (with `resume_attempts`) and the page waits instead of resuming too. A run of a trigger, a job or a loop stays running until the resumed turn ends, a goal stays active, and a failed run is reported only when the last try is spent. A chat that a messaging connection started is not resumed by the server: its reply is sent by the process that died, so the sender is told the turn was interrupted. A sub-agent chat is not resumed either; its parent decides.

**Calls that a restart cut in two.** The transcript is the checkpoint: each finished message and tool result is a line of its own. A call in flight has no result line, and running it again could do its work twice. So a call of a tool that may change something leaves an internal line before it runs (`tool_intent`: the tool name and the start of its arguments; the API never returns it). If the turn is resumed and an intent has no result after it, the model is told that those calls started, that it is not known whether they took effect, and to check the state before it repeats one. Only tools that only read skip the line: the add-on tools of kind read, the built-in tools declared read only, and a fixed list (the file, search, collection and web tools). A tool that nobody classified, such as an MCP tool, counts as one that may change something. A result is matched to an intent by the tool name, so two parallel calls of one tool can hide one unknown outcome. The effects that the host sends itself (the webhooks of a job, the notification of a run, the report of a trigger) do not repeat: each is sent when the run ends, behind its own once-only record, and the run ends only after the last try.

**The answer that was being written.** A message is stored as a line when it ends, so a process that is killed in the middle of a long answer would lose it. While a message streams, the server saves its text so far into the chat record every 2 seconds, or when 4 KiB of new text have come (not more than four times a second), and clears it when the line is stored. A message that ends in under 2 seconds writes nothing. When the next turn starts (a resume, or a new message of yours) a text that is still there becomes an assistant line, kept as it was when last saved, up to 128 KiB. A resume also tells the model where its message stopped, so that it goes on from there. A graceful stop does not need this: the turn that Close cuts short stores its text as it ends.

In terms of the [distributed state rule]({{% relref "/administration/distributed-mode" %}}): the resume state is shared (it is in the chat row), and the sweep is single-runner (the advisory lock of the reaper).

The JSON files are not supported in a cluster, as the Storage table says.

## Where Files keep their data

Files need a place to live. The choice is made at start and is explicit: with a
database the tree goes there, and without one it goes in a JSON index.

| Setup | Tree | File contents |
|---|---|---|
| Authentication on, with a SQLite file | The auth database (pure-Go SQLite, in any build). | A folder under the data path. |
| Authentication on, with PostgreSQL | The auth database. | The data path, or the object store in a cluster. |
| No database, a data path set | A JSON index in `<data path>/chats/workspace`. | A folder under the data path. |
| No data path | none | none: files are off. |

The JSON index is a plain folder: `index.json` is a snapshot and `index.log` is
the changes since, one record per line. A change is written and synced before
the call returns, a start replays the log and drops a last record that a crash
cut short (the damaged log is kept next to it as `index.log.damaged-<time>`),
and the log is folded into a new snapshot when it passes 16 MiB. Files, uploads,
attachments, skills, the Trash, undo and the limits behave as they do on a
database, because both stores pass the same test suite. One LocalAI process
owns that folder, because its lock lives in the process: a second process on the
same folder would overwrite the first one's index, so use a database when more
than one frontend serves the files. There is no move between the two indexes:
files kept in a JSON index are not copied into a database when you turn
authentication on, and the other way round. Copy them with the files API.

When files are off, every files route and every attachment answers `409` with
the reason, the list endpoint returns `"files": false` and `files_reason`, and
everything else in the chats works. A chat then has no skills either, not even
the built-in ones, and no `load_skill`: skills are files, and every built-in
skill works on files or runs commands.

In a cluster, configured S3 holds workspace bytes; the distributed compose
example sets it up with Garage. Without S3, Files uses the existing
`DataPath/chats/blobs` directly. All Files participants must then mount the same
backing data directory and use the same PostgreSQL metadata database. See
[Shared filesystem for Files](#shared-filesystem-for-files) for the operator
contract. With PostgreSQL, startup checks that every node sees the same blob
folder and stops with an error when it does not.


### Shared filesystem for Files

Files selects its blob backend automatically, with no separate workspace path or
coordination flag:

| Existing configuration | Workspace bytes |
| --- | --- |
| S3 storage URL configured | S3, using the existing bucket, region and credentials, for every role |
| No S3 storage URL | Direct filesystem storage at `DataPath/chats/blobs` |

This applies to standalone servers, frontends, optional chat workers and the
add-on owner. An owner honors configured S3 without joining the distributed
control plane or changing its role. Global model/input staging is unchanged.
Standalone JSON/SQLite filesystem behavior and existing blob paths remain stable.
Without a data path and without S3, Files is disabled; temporary scratch never
becomes authoritative filesystem storage. With S3 and a database, local cache may
be temporary and is removed at shutdown. JSON metadata still needs a data path.
Configured S3 errors and PostgreSQL workspace initialization errors fail startup
rather than selecting another backend.

Distributed Files requires shared PostgreSQL metadata. PostgreSQL owns names,
authorization, revisions and blob references; immutable content-addressed bytes
live in the selected backend. Per-instance caches and scratch are disposable.
For filesystem storage, every Files consumer must mount the same existing data
directory/backing storage and use the same PostgreSQL database: frontends, the
add-on owner and optional chat workers. Inference-only nodes need no Files mount.
Equal path strings do not prove shared volume identity. With PostgreSQL, the
first node to start writes a random `.volume-id` file in `DataPath/chats/blobs`
and records the same id in the database; every node compares the two at startup
and refuses to start on a mismatch or when the file is missing. This catches a
volume that is not shared. The check covers the Files blob folder only. It doesn't check the models directory of distributed mode. It does not prove that the storage is coherent.
Other data-path contents do not thereby become cluster-safe.

Keep the canonical backing directory service-controlled, with compatible UID/GID
and permissions for every participant. LocalAI does not recursively chmod/chown
existing trees. Ordinary platform path aliases are resolved, while symlinks inside
the canonical blob tree are rejected; do not allow untrusted writers to replace
paths or redirect mounts. A same-node RWO/local-path volume needs enforced
co-placement and provides no cross-node failover guarantee. Multi-node use needs
coherent shared storage, normally an appropriate RWX volume: verify exclusive
creation, atomic same-filesystem rename, immediate cross-client visibility, file
sync and directory sync durability. An RWX label alone does not guarantee these
semantics; validate crash/power-loss behavior with the storage provider.

All PostgreSQL-backed direct-filesystem workspaces automatically hold the revision
lock through blob ingest and metadata publication, including non-distributed
owners. This serializes metadata writes during slow uploads. Blocked operations
must honor request context cancellation. Orphan cleanup uses a bounded 30-second
context; cancellation can defer cleanup to a later pass. S3 is an alternative to
this shared-filesystem deployment contract; its local cache is not treated as a
shared filesystem.

{{% callout style="warning" %}}
No paths or blobs migrate automatically. Existing local data stays in place and
existing S3-backed workspaces stay on S3. Switching populated metadata to another
backend requires a separately planned blob copy and verification procedure, with
all participants switching consistently. Mixed S3/local/shared backends on the
same metadata can produce missing content.
{{% /callout %}}
