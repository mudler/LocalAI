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
| `connectors` | off | Telegram and Slack bindings (`/api/chats/connectors`): a public door to one of your agents. |
| `mcp_jobs` | off | The agent tasks and jobs API (`/api/agent/...`). |
| `pages` | off | The pages of the workspace (`/api/chats/pages...`): HTML pages in `pages/` of the home, shown in a sandboxed frame. See [Pages]({{%relref "features/pages" %}}). |

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
| `/api/chats/admin/shared-folders...` | Create shared folders and choose who may use them (see [Shared folders](#shared-folders)) |

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

# Rename a chat. This answers 409 while a turn runs.
curl -s -X PATCH localhost:8080/api/chats/3f1c... \
  -H 'Content-Type: application/json' \
  -d '{"title": "Trip plans"}'
```

The reply is not in that response. Read it from the event stream or from the
transcript:

```bash
# Live server-sent events: text, reasoning, tool_start, tool_end, turn_end, files, ...
curl -N localhost:8080/api/chats/3f1c.../events

# The transcript, in order. Read it first, then open the event stream: the
# stream carries only what happens after you connect.
curl -s 'localhost:8080/api/chats/3f1c.../messages?after=0'
```

The transcript lists messages, tool lines and one checkpoint line per turn. A
checkpoint line has `rev`, the change set of the turn, and its `text` lists the
paths it changed inside your home, one a line. A tool line of `write_file` or
`move_file` carries a card `{"kind": "files_change", "id": "<rev>:<path>"}`.
`POST /api/chats/files/undo {"rev": N}` undoes either change set; the chat page
uses this for the Undo of its change notes (see
[Make LocalAI yours](/features/make-localai-yours/#what-a-change-shows)).

A chat is named after the first line of its first message, unless you gave it a
title. To rename it, see [Settings, rename and model change](#settings-rename-and-model-change).

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

## Settings, rename and model change

A chat keeps its own model settings. They apply from the next turn, and they
are part of the chat: a fork and an export carry them.

```bash
curl -X PATCH .../api/chats/3f1c... -H 'Content-Type: application/json' -d '{
  "title": "Trip plan",
  "model": "another-model",
  "settings": {"system_prompt": "Answer in short sentences.", "temperature": 0.3,
               "top_p": 0.9, "top_k": 40, "reasoning_effort": "low", "context_size": 16384}
}'
```

- `system_prompt` goes before the instructions of a saved agent.
- `temperature` (0 to 2), `top_p` (above 0, at most 1), `top_k` and
  `reasoning_effort` (`none`, `minimal`, `low`, `medium`, `high`) go to the
  model with every request of the chat. A missing field means the model's own
  default. `settings` replaces all of them, so send the whole object; `{}`
  clears it.
- `context_size` is the window, in tokens, that the loop plans with when it
  decides to compact the conversation. It does not reload the model.
- A new `title` (1 to 80 characters) or `model` needs a chat with no turn running (409 otherwise).
  The history stays when the model changes.

### Context meter and speed

Each model request sends a `usage` event with `used` (tokens in the context
the request sent), `limit` (the window the loop plans with, when known),
`completion` (tokens generated), `tokens_per_second` and `prompt_ms` (the
time the backend took to read the prompt; both only when the backend reports
timings), `model` (the model of the request) and `node` (the worker that
served it, only when the server runs with `--expose-node-header`).

## Regenerate, edit, fork, search, export

```bash
# Answer the last message again. Your messages of that turn stay.
curl -X POST .../api/chats/3f1c.../regenerate
# Replace one of your messages and run the chat again from it. The messages
# after it are removed. seq is the line number in the transcript.
curl -X POST .../api/chats/3f1c.../messages/12/edit -d '{"text":"A better question"}'
# Copy the chat up to and including line 14 into a new chat.
curl -X POST .../api/chats/3f1c.../fork -d '{"from_seq":14,"title":"Other idea"}'
# Find chats by title or by what was said (newest 500 chats).
curl '.../api/chats/search?q=invoice'
# Take a chat out as Markdown (default) or JSON, and bring a JSON file back in.
curl '.../api/chats/3f1c.../export?format=json' > chat.json
curl -X POST .../api/chats/import -d "{\"chat\": $(cat chat.json)}"
```

- Regenerate and edit remove the lines after the cut point from the
  transcript and from what the model sees. Files that the removed turns
  changed stay changed, and tool calls of the kept part are not replayed to
  the model, only the messages.
- A fork has the model, the settings and the tool switches of the original. It
  has no folder and no files of its own until you attach some.
- The JSON export has `version`, `title`, `model`, `settings` and `messages`
  (`role`, `text`, `at`). It holds no files and no model state. An import
  makes a new chat of yours with the model of the file, or the one named in
  `model`, which you must be allowed to use.

## Images, video and documents

Attach files with `multipart/form-data` on `POST /api/chats/{id}/messages`
(see above). What the model gets depends on the file and the model:

- **Images.** When the chat's model takes images (a multimodal projector, or
  `known_input_modalities` with `image` in its configuration) the image goes
  to the model inside the message, as an `image_url` part. With a model that
  does not, the model gets the file path and the `describe_image` tool, which
  needs a helper vision model.
- **Video.** `mp4`, `webm`, `mov`, `mkv` and `avi` files go to a model that
  declares video input as a `video_url` part, with the next request after the
  message and for that turn only. Other models get the path only.
- **PDF, Word, Excel, PowerPoint and EPUB.** The text of the file is added to
  the message, the first 20,000 characters of each document, and the model
  reads the rest with the `read_document` tool (`offset` and `limit`). No
  helper model is needed. A scanned PDF with no text layer is reported as
  such.
- **Audio.** A voice note is transcribed when a transcription model is set,
  as before.

The files are always kept in the chat's folder too. Images over 20 MB, videos
over 100 MB and more than 128 MB of media in one message stay files only.

## The files of a chat

Each chat lists the files that belong to it: the files you attached, the files
the model created, and the files it changed. The model gets the same list at
every turn, so it knows which files exist and where they are now.

- **Where attachments live.** In the chat's own folder in your files:
  `/Chats/<date> <title>/<name>`, for example `/Chats/2026-10-29 Budget/q1.csv`.
  The model sees the same file as
  `/workspace/home/Chats/2026-10-29 Budget/q1.csv`. The folder is made at the
  first attachment.
- **What is listed.** Every file in the chat folder, and every file that the
  chat's turns created or changed elsewhere in your home folder: in the
  sandbox, or with a file tool such as `write_file`. The list is read from the
  history of your files, so a file that you or the model moved or deleted
  later is not listed at its old path.
- **Rename.** Renaming a chat renames its folder to match the new title, and
  the list gives the new paths at once. Earlier messages keep the old paths.
  The model is told at its next turn that the folder was renamed and which
  path is current.
- **Delete.** Deleting a chat removes its transcript and its sandbox. Its
  folder and every file the model wrote stay in your files.
- **Fork.** A fork has no folder of its own until you attach a file. The files
  that the copied messages attached stay in the original chat's folder; the
  fork lists them while they are there.

To follow the changes to your files as they happen (from any tab, chat,
trigger or frontend), open the [change feed](/features/workspace-feed/)
(`GET /api/workspace/events`).

```bash
curl .../api/chats/3f1c.../files
# {"files":[{"name":"q1.csv","path":"/Chats/2026-10-29 Budget/q1.csv",
#   "guest_path":"/workspace/home/Chats/2026-10-29 Budget/q1.csv","size":2048,
#   "mime":"text/csv","origin":"user","turn":1,"at":"..."},
#  {"name":"report.md","path":"/report.md","guest_path":"/workspace/home/report.md",
#   "size":930,"mime":"text/markdown","origin":"model","turn":2,"at":"..."}],
#  "folder":"/workspace/home/Chats/2026-10-29 Budget/","folder_path":"/Chats/2026-10-29 Budget"}
```

`origin` is `user` (you attached it, or put it in the chat folder), `model`
(the model wrote it in the sandbox) or `tool` (a tool of the chat wrote it,
such as `write_file`). `changed` marks a file the chat changed but did not
create. `media` marks an attachment that the model received as an image or a
video inside the message; the others reach the model by path. `path` works
with the files API above. `stale` lists old folder paths that earlier
messages name. A `files` event on the chat's event stream says that the list
may have changed; read it again.

The list that the model gets has the newest 50 files, one line each with the
path, size, type, origin and turn, and a count of the rest. A chat with no
files gets no list. The list is built from the stored history when a turn
starts, so every frontend of a cluster gives the same one.

## Sub-agents

The model can start a sub-agent for a self-contained task. By default it waits
for the answer. With `wait` set to false it carries on, and the result arrives
later as a message in the conversation and as a `sub_agent_done` event.

- A sub-agent is its own conversation, linked to its parent. It appears in the
  list with `kind` set to `subagent` and a `parent_id`.
- A sub-agent gets the parent's tools, or a subset the parent names. It cannot
  start sub-agents of its own.
- A chat runs at most four sub-agents in the background at once. The count
  comes from the shared store, so it holds when turns of the same chat run on
  different frontends of a cluster.
- Stopping a chat, or a turn that fails, stops its running sub-agents, also
  when they run on another frontend and when the parent has no turn left.
- A `sub_agent_started` event (with the run's id and title) and a
  `sub_agent_done` event mark the run in the parent's event stream.

```bash
curl .../api/chats/3f1c.../agents                      # runs, their state, the limit
curl .../api/chats/3f1c.../agents/9ab2.../messages     # one run's transcript
curl -X POST .../api/chats/3f1c.../agents/9ab2.../stop # stop one run
```

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

### Prompts and resources as context

MCP servers can offer prompts (ready-made instructions with arguments) and
resources (documents the server holds). A chat can list them for the servers it
can use now, which means the servers of the model, less the ones you switched
off for the chat, or the ones the chat's saved agent grants, and your enabled
connectors. You can add any of them to a message as context for that turn:

```bash
curl .../api/chats/3f1c.../mcp/prompts      # {"prompts":[{"server":"connector:ab12","name":"review","arguments":[...]}]}
curl .../api/chats/3f1c.../mcp/resources    # {"resources":[{"server":"wiki","uri":"wiki://home"}]}
# Preview one:
curl -X POST .../api/chats/3f1c.../mcp/prompts -d '{"server":"connector:ab12","name":"review","arguments":{"lang":"Go"}}'
curl -X POST .../api/chats/3f1c.../mcp/resources/read -d '{"server":"wiki","uri":"wiki://home"}'
# Send them with a message. The server expands them as you and puts the text before yours.
curl -X POST .../api/chats/3f1c.../messages -d '{"text":"Go ahead","context":[
  {"server":"connector:ab12","name":"review","arguments":{"lang":"Go"}},
  {"server":"wiki","uri":"wiki://home"}]}'
```

A server the chat cannot use is `403 mcp_refused`, and the message is not sent.
A server that does not answer is `502 mcp_error`. Binary parts of a resource are
left out, and the text is cut at 256 KiB. The expanded text is part of the
message in the transcript.

### Switch tools off for one chat

The **Tools** menu over the message box lists the add-ons you may use, the MCP servers of the
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

A skill is a folder with a `SKILL.md`: a `name`, a one-line `description` and a procedure the model reads when a task matches. At each turn the chat's prompt lists the skills it has, with the path of each, and the model reads a skill with its file tools when it needs it. Other files in the skill's folder, such as references and scripts, are read the same way. A `SKILL.md` over 64 KiB is refused.

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

A chat keeps its own switches. Turning a skill off takes it out of the index at the next turn. A switch with the plain name turns off the skill that wins, so a skill of the same name further away shows through, as the list says. A switch with the id `layer:name` (the `id` in the skill list, for example `personal:review`) turns off the skill of that layer only. A deleted skill goes to the Trash like any file. Only an administrator writes shared skills, and everyone reads them. Your personal skills are as private as your files.

The **Skills** page lists them by layer and edits personal skills, and the **Skills** menu over the message box switches them per chat.

The server ships a few skills of its own, in the built-in layer: `docx`, `xlsx`, `pptx`, `pdf`, `writing-skills` and `localai-workspace`. The last one tells the model how to change your persona, profile, shortcuts, Home widgets and theme; see [Make LocalAI yours](/features/make-localai-yours/). The office skills are adapted from the Feather project (MIT licence); the NOTICE file in the same folder keeps the source and the licence. They tell a model how to work with documents in a sandbox. For most edits the Office add-on's tools are simpler: they read, edit and create Word, Excel and PowerPoint files, and read PDF files, in your files.

## Collections

When you may use collections, a chat has two more tools: `list_collections` and `search_collection` (`collection`, `query`, `limit`). They search your own collections, the ones of the Collections page, and nobody else's: a name that is not yours is answered "collection not found". An answer holds at most 20 passages and about 16 KiB. A sub-agent gets these tools only when its parent names them.

## Add-on tools

A chat can use the tools of the add-ons you may use. The **Tools** menu of the chat lists them with a switch for each add-on; a switch applies to that chat from its next turn. A tool runs as you, in your chat, and never as a user a model names.

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
- **Attachments.** Send a message as `multipart/form-data` with a `text` field and `files`. The files are saved in the chat's own folder under `Chats/` and the model gets their paths. With a turn running the files are saved at once and the message waits like any other, with the paths of the files in its text; the model reads them by path when the message reaches it. A sandbox that started before the files arrived sees them from the next turn.
- **Search.** `GET /api/chats/files/search?q=budget` finds files by name and lines inside text files, ignoring case. There is no index: large files (over 1 MiB) and binary files are found by name only, and a very large workspace is scanned up to a limit.
- **The model can use your files.** A chat with files on gives the model `list_files`, `read_file`, `write_file`, `move_file`, `delete_file` and `search_files`, for your home folder only. What it changes is listed in the history as an agent change of that chat, and you can undo it; a delete goes to the Trash. Sub-agents do not get these tools.
- **Pictures and recordings.** `describe_image` lets the model look at an image in your files and `transcribe_audio` turns a recording into text. A voice note attached to a message is transcribed on the way in and its transcript goes with your message; the file is kept as it is. They use helper models, which you choose in the settings (Chat image model, Chat transcription model). Left empty, the first installed model that can see images, or transcribe, is used. With none installed the tool says what to install. The helpers run as you, so your model access and quota apply.
- **A file is at most 256 MiB.** A message with attachments is limited to 1 GiB in all.

The **Files** page in the sidebar is a file browser for all of this: folders with a breadcrumb, upload by button or drag and drop, rename, delete to the Trash, a Trash tab that restores, a History tab that undoes a change, one search box over names and text, and a preview of text and images.

A folder `pages/<name>/` with an `index.html` is a page: the UI shows it in a
sandboxed frame under **Pages**. See [Pages]({{%relref "features/pages" %}}).

### Shared folders

A shared folder is a folder that several users use. An administrator creates
it and chooses its members: a user, or **Everyone** (every user, also the ones
who sign up later), each with **Can read** or **Can edit**. A member's chats see
the folder at `/workspace/shared-<name>` (read-only for a reader), and its pages
show on the Pages screen under **Shared with you** (see [Shared
pages]({{%relref "features/pages#shared-pages" %}})). A user who is not a member
sees nothing of the folder: every call about it answers `404`, as for a folder
that does not exist.

In the UI, the **Shared** tab of **Files** lists the folders. An administrator
creates, renames and removes them there, and opens **Members** to add a person
or Everyone, change their access or take it away. Any other user sees the
folders shared with them and their access.

- **Where it lives.** A shared folder is `/shared/<name>` in the workspace,
  outside every home folder. A name has 1 to 64 letters, digits, spaces, dots,
  dashes or underscores, starts with a letter or a digit, and is unique without
  regard to case. A server keeps at most 100 shared folders, with at most 200
  members each. A new folder has an empty `pages` folder, and the administrator
  who creates it is a member who can edit it, so they can fill it. Remove them
  when they do not need it.
- **Access is a grant.** A member is a workspace grant on the folder and
  everything in it, checked on every call. A change applies at once to the
  Files and Pages screens and to the shared pages' bridge; an open chat sees it
  the next time it opens.
- **Rename** keeps the members, the pages and the consents to them.
- **Remove** moves the folder and its files to the Trash as one change, and
  answers with `undo_rev`. **Undo** (or `POST .../restore` with that `rev`)
  brings it back with its members. Nothing is erased. Removing a folder also
  drops every user's consent to its pages, so a restored page asks again before
  it runs.
- **Audit.** Every change (create, rename, delete, restore, grant, revoke) is
  written with who made it and when: in the table
  `studio_shared_folder_events` of the auth database, or in
  `chats/shared-folders/audit.json` under the data path, and in the log. A
  change that the LocalAI Assistant made is written as `@assistant`.

| Route | Who | Use |
|---|---|---|
| `GET /api/chats/shared-folders` | any user | The folders shared with you: `id`, `name`, `path`, `pages`, `mode` (`read` or `write`). |
| `GET /api/chats/shared-folders/{id}` | any user | One of them; `404` when it is not shared with you. |
| `GET /api/chats/admin/shared-folders` | administrator | Every shared folder with its `members` (`principal`, `name`, `everyone`, `mode`). |
| `POST /api/chats/admin/shared-folders` | administrator | Create one, body `{name}`. `409` when the name is taken or the limit is reached. |
| `GET`, `PATCH`, `DELETE /api/chats/admin/shared-folders/{id}` | administrator | Read one, rename it (body `{name}`), remove it (answers `{folder, undo_rev}`). |
| `POST /api/chats/admin/shared-folders/restore` | administrator | Undo a removal, body `{rev}`. `409` when a folder with its name exists now. |
| `PUT /api/chats/admin/shared-folders/{id}/members/{member}` | administrator | Add a member or change its access, body `{mode}` (`read` or `write`). `member` is a user's ID, email or name, or `@everyone`. |
| `DELETE /api/chats/admin/shared-folders/{id}/members/{member}` | administrator | Take a member's access away. |
| `GET /api/chats/admin/shared-folders/events?limit=` | administrator | The audit, newest first. |

```bash
# Create a folder, share it with everyone (read) and with one person (edit).
curl -X POST ".../api/chats/admin/shared-folders" -d '{"name": "Team"}'
# {"id":42,"name":"Team","path":"/shared/Team","pages":0,
#  "members":[{"principal":"<your id>","name":"Ada","mode":"write"}]}
curl -X PUT ".../api/chats/admin/shared-folders/42/members/%40everyone" -d '{"mode": "read"}'
curl -X PUT ".../api/chats/admin/shared-folders/42/members/marta@example.com" -d '{"mode": "write"}'
# Remove it, then bring it back.
curl -X DELETE ".../api/chats/admin/shared-folders/42"          # {"folder":{...},"undo_rev":118}
curl -X POST ".../api/chats/admin/shared-folders/restore" -d '{"rev": 118}'
```

The LocalAI Assistant has the same operations as tools (see [LocalAI
Assistant]({{%relref "features/localai-assistant" %}})). In a cluster the
folders and the grants live in the shared workspace database and the audit in
the auth database, so every frontend sees the same state.

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
everything else in the chats works.

In a cluster the bytes go to the object store, so a file written through one
frontend is read through another. That needs S3 (the storage URL setting); `docker-compose.distributed.yaml` sets it up with Garage.
Without it the object store is a folder of each frontend, so files are off in
that cluster, with a reason that says so, instead of every frontend keeping its
own copy.

## Storage

| Setup | Where conversations live |
|---|---|
| Authentication on (SQLite or PostgreSQL) | The auth database. |
| No database, a data path set | JSON files under `<data path>/chats`. They survive a restart. |
| No database and no data path (`--data-path=-`) | Memory only. They are lost on restart, and files are off. |

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

**Sub-agents.** The runs are chats in the shared store, linked to their parent. The background limit, the list of runs and "stopping a chat stops its sub-agents" all read the store, so they hold when the turns of one chat run on different frontends. A stop reaches a run on another frontend over the bus. The tool subset of a running sub-agent is kept in the memory of the frontend that runs it, which is where it runs.

**Settings and edits.** The settings, the title and the model of a chat, and the cut that regenerate and edit make, are stored with the chat. A change of title or model, a regenerate and an edit take the same turn claim as a message, so they are refused with 409 while a turn runs on any frontend. A video that comes with a message is held in the memory of the frontend that runs the turn, for that turn's first request.

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

## The chat page

The chat page in the web UI (`/app/chats`) talks to this API, so a chat started
there keeps running when you close the tab and opens again from any browser.

- **Conversations.** `Ctrl+K` (`Cmd+K` on a Mac), or **Chats** in the header,
  opens the list of your chats, grouped by day. Type to find a chat by its name
  or by what was said in it (the server search); the line under a match shows
  where it matched. Open, rename (`F2`), export as Markdown or delete one. A
  deleted chat can be brought back for a few seconds with **Undo**; then the
  server deletes it.
- **A new chat.** Pick the model and, if you have [saved agents](/features/chats-agents/),
  an agent in the message box, then send. Picking another model in an open chat
  moves the chat to it from the next turn (not while a turn runs).
  `/assistant` starts a chat with the LocalAI management tools (administrators,
  when the assistant is on).
- **Chat settings** (the sliders icon in the header) holds the settings above:
  a system prompt, temperature, top P, top K, context size and reasoning
  effort. **Save settings** sends them all; they apply from the next turn.
- **While the model works** you can keep typing and attach files. A message you
  send waits over the message box and reaches the model at its next step;
  edit or cancel it until then. `Esc` or **Stop** ends the turn; waiting
  messages stay for **Send now**. `Up` in an empty box edits the last waiting
  message, or puts your last message back in the box.
- **On a message**: **Edit** (your message) replaces it and runs the chat
  again from it, **Regenerate** (the last answer) answers again, and
  **Branch** copies the chat up to that message into a new chat. The keys are
  `E`, `R` and `B` on a focused message.
- **More** exports the chat as Markdown or JSON and imports a JSON export as a
  new chat. Chats that the old chat page kept in this browser are offered for
  import once (`POST /api/chats/import/browser`); the browser's copy is
  removed when none failed.
- **Under the message box** the foot shows the context meter and the speed of
  the last model request (from the `usage` events). It turns amber above 80
  percent of the window and red above 90.
- **Tools, Skills and Sub-agents** menus over the message box switch tools and
  skills for this chat (see above; a skill is switched by its `id`), and list
  the sub-agents of the chat with their transcript and a **Stop** for a
  running one. The Tools menu says whether the Sandbox add-on runs commands
  isolated, directly on this machine, or not at all. Changes an add-on tool
  made show under the tool line with **Undo** when the add-on can undo them; a
  call the add-on refused (`not_permitted`, `resource_unreachable`,
  `not_running`, `invalid_request`) says why. MCP Apps show live under their
  tool call.
- **Files** over the message box opens the files pane on the right: the
  files you attached, the files the assistant created and the files it
  changed, each with its size and turn, a preview and a download. It updates
  while the model works and after a rename. Drop files on it to attach them to
  your next message. A file the model received as an image or video is marked
  **In context**. The pane and the canvas share the right side.
- **A trigger the model proposes** shows as a card under its tool line with
  **Create trigger**, **Edit** (name and schedule or folder) and **Dismiss**.
- **Shortcuts** over the message box are the prompts of your
  `shortcuts.json`, or the default starters without it. A click puts one in the
  box; **Edit shortcuts** changes the list. See [Make LocalAI
  yours](/features/make-localai-yours/).
- **Find** (`Ctrl+Shift+F`), **Canvas** (code blocks in a side panel), focus
  mode, copying the chat as Markdown and the keyboard shortcuts work as
  before.

Administrators find every user's agents, triggers and connectors under
**Operate > Settings > Chats of all users**. The [agent tasks and
jobs](/features/agent-jobs/) have a page of their own, **Agents > Tasks**
(`/app/agent-tasks`).

## Moving from the old chat

The old chat page, the agents, the agent jobs and the old skills were replaced by the chats. At the first start the server converts what they left; see [Moving from the old chat and agents](/features/chats-migration/).

## API reference

The routes are listed under the `chats` tag at `/swagger/index.html`, and
`GET /api/instructions/chats` returns a machine-readable summary.
