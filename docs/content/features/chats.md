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
| `LOCALAI_DISABLE_CHATS` | `false` | Turns off the chats: the `/api/chats` API, saved agents, triggers, connectors, notifications and the agent jobs (`/api/chats/*`, `/api/workspace/*` and `/api/agent/*` answer `404` with the type `chats_off`). The files workspace of the add-ons is not affected. |
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

The transcript lists messages, tool lines and one checkpoint line per turn.
Undo works per tool call, not per turn: each `write_file`, `edit_file` or
`move_file` call is its own change set in your files, and its tool line carries
a card `{"kind": "files_change", "id": "<rev>:<path>"}` (`delete_file` moves to
the Trash, where you restore it).
`POST /api/chats/files/undo {"rev": N}` undoes that change set; the chat page
uses this for the Undo of its change notes (see
[Make LocalAI yours](/features/make-localai-yours/#what-a-change-shows)). To
undo a whole turn, undo the changes of its tool lines, newest first. The
checkpoint line marks the end of the turn; its `rev` is `0` and its `text` is
empty, because the turn's changes are already kept call by call.

A chat is named after the first line of its first message, unless you gave it a
title. To rename it, see [Settings, rename and model change](#settings-rename-and-model-change).

### Start a chat with its switches

A chat can start with tools, add-ons and skills already switched off, and with
opt-in tools switched on. `switches` in `POST /api/chats` takes each list in the
form its own endpoint takes (see [Switch tools off for one
chat](#switch-tools-off-for-one-chat) and [Skills](#skills)). The chat is
stored with them, so its first turn already has them. A bad entry refuses the
whole request with `400`, and no chat is created.

```bash
curl -s localhost:8080/api/chats -H 'Content-Type: application/json' -d '{
  "model": "my-model",
  "switches": {
    "addons_off": ["office"],
    "mcp_off": ["crm", "connector:c1"],
    "builtins_off": ["web"],
    "builtins_on": ["saved_agents"],
    "skills_off": ["builtin:pdf"]
  }
}'
```

To show these switches before the chat exists, read what a chat on a model
(and a saved agent of yours) could use. Each switch is in its default state:

```bash
curl -s 'localhost:8080/api/chats/draft/tools?model=my-model'
curl -s 'localhost:8080/api/chats/draft/addons?model=my-model&agent_id=<agent>'
curl -s localhost:8080/api/chats/skills
```

The answers have the shapes of `GET /api/chats/{id}/tools` and `GET
/api/chats/{id}/addons`. A model you may not use is `403`, a model the server
does not have is `400`, and another user's agent is `404`.

## Keep typing while the model works

Send another message while a turn runs and it waits. The response says so:

```json
{"started": false, "pending_id": "p2"}
```

Waiting messages reach the model together, in the order you sent them, at its
next request. A waiting message shows the files you attached to it as the same
chips a sent message has. Until it is delivered you can change or remove it:

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

## The status line

The line under the composer shows what the chat is doing now, as one word:
**Ready**, **Loading model**, **Thinking**, a tool step (for example
**Reading** `q2.pdf`), **Answering**, **Waiting for you** (messages wait for
**Send now**), **Paused**, **Stopped**, **Failed** or **Interrupted**. A spinner
marks a running turn, and an icon marks a state that needs your attention.
While the composer has focus and is empty, the line also shows the keyboard
hint.

**Paused** means that the turn used all its steps: the model made as many
rounds of tool calls as one turn allows (`chats_max_iterations`, 40 by
default) and then wrote an answer from what it had, so the work may not be
finished. The line says how many steps the turn used (for example
`Paused · after 40 steps`) and shows a **Continue** button. Continue sends a
message that asks the model to go on from where it stopped; you can also type
your own. A turn that pauses is otherwise like one that ended by itself:
messages that wait start the next turn, and a sub-agent that pauses reports
back. The API reports it as `last_turn: "paused"` and sends a `paused` event
with `steps` before `turn_end`.

On the right, the context meter shows how full the model's context was at the
last request: a bar, the tokens used and the limit (for example
`12.3k / 32.8k tokens`), the tokens left (`20.4k left`) and the speed of that
request. Two ticks on the bar mark 80 % and 90 %. While an answer streams, the
speed is an estimate (shown with `≈`); the measured figure replaces it when the
request ends. From 80 % the meter turns amber, from 90 % red, both with a
warning icon. When the server does not know the limit, the meter shows only
the tokens used, with no bar. Where the line is narrow, the percentage replaces
the used and limit figures; narrower still, the speed moves to the details.
Select the meter to see:

- the context used, the limit and the tokens left, and what happens near the
  limit (the server compacts the older part of the chat),
- the model, its backend and, when the server runs with
  `--expose-node-header`, the node that served the request,
- the tokens in and out, the time to the first token, the prompt processing
  time and the speed, with the average of the last requests.

The figures come from the `usage` events of the page's event stream. The server
does not keep them, so they are empty after a reload until the next request.
On a phone the line shows the word, the bar, the percentage and the tokens
left, and the details open in a sheet.

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
  (`role`, `text`, `at`, and for a message of yours with files `attachments`,
  with the path of each file, and `note`, the text the model read from them).
  It holds no files and no model state. The Markdown export and a copied chat
  name the files of a message, not their paths. An import
  makes a new chat of yours with the model of the file, or the one named in
  `model`, which you must be allowed to use.

## Images, video and documents

Attach files with `multipart/form-data` on `POST /api/chats/{id}/messages`
(see above). In the chat page, one **Attach file** button takes files of any
type (images, audio, documents, archives such as `.zip`); you can also paste an
image or drop files on the chat. What the model gets depends on the file and
the model:

- **Images.** When the chat's model can look at images (a multimodal
  projector, `vision` in `known_usecases`, or `image` in
  `known_input_modalities`) the image goes to the model inside the message,
  as an `image_url` part, and the model answers about it directly. A model
  behind a cloud proxy cannot be asked what it accepts: declare `vision` in
  its configuration (see [Cloud passthrough proxy]({{% relref "operations/cloud-proxy#images" %}})).
  With a model that cannot look at images, the model gets the file path and
  the `describe_image` tool.
- **Video.** `mp4`, `webm`, `mov`, `mkv` and `avi` files go to a model that
  declares video input as a `video_url` part, with the next request after the
  message and for that turn only. Other models get the path only.
- **PDF, Word, Excel, PowerPoint and EPUB.** The text of the file is added to
  the message, the first 20,000 characters of each document, and the model
  reads the rest with the `read_document` tool (`offset` and `limit`). No
  helper model is needed. A workbook (`.xlsx`, `.xlsm`) comes sheet by sheet,
  each sheet as a heading and its rows as tab-separated lines, up to 2,000
  rows and 50 columns per sheet; a note says what a cut sheet leaves out. A
  Word document gives its headings (`#`), list items, paragraphs and tables
  (one tab-separated line per row), and a presentation its slides, each with
  its title, text and speaker notes. A PDF with no text layer is reported as
  a probable scan; an Office file with no text is reported as empty.
- **Audio.** A voice note is transcribed when a transcription model is set,
  as before.

The files are always kept in the chat's folder too. Images over 20 MB, videos
over 100 MB and more than 128 MB of media in one message stay files only.

### Images and vision

- **Size.** An image whose longest side is more than 2048 pixels is scaled
  down to 2048 before it goes in the message. An opaque image goes as a JPEG,
  an image with transparency as a PNG. The file in the chat's folder is not
  changed.
- **Only with its own message.** The images of a message go to the model with
  every request of that turn, tool calls included. The next messages do not
  send them again: in the history, each earlier image becomes a short note
  that says so. To look at an earlier image again, the model uses
  `describe_image` on its file. This keeps a few photos from filling the
  context, and a cloud model from billing them on every turn.
- **What the model is told.** A chat with files gets an `Images:` block in its
  instructions, after the `Commands:` block. It says that the model sees the
  attached images and answers about them directly, or that it cannot see
  images and uses `describe_image`, or that no model here can look at images:
  then the model tells the user so and suggests choosing a model that can.
- **`describe_image`.** The tool uses the first model of this list: the
  **Chat image model** setting, the chat's own model when it can look at
  images, the first installed model that can, and last the chat's own model
  when it is a passthrough cloud-proxy model with no vision declaration.
  LocalAI cannot ask such an upstream what it accepts, so the tool tries it.
  If the model refuses the image, the tool says that the model did not accept
  it and what to do: turn on "This model can look at images" in the model's
  settings, choose another model, or ask an administrator to set a Chat image
  model. A local model that cannot look at images is not tried. With no model
  to try, it fails with "No vision model: choose a model that can look at images for this chat, or
  an administrator can install one from the model gallery and choose it in
  Settings under Chat image model."
- **In the chat page.** When you attach an image and the chat's model cannot
  look at it, a notice over the message box says so and names the model that
  will describe the image, or says that no model can. `GET /api/chats` returns
  that model as `image_model`; the field is left out when there is none.

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
  chat's turns created or changed elsewhere in your home folder: with `bash`,
  or with a file tool such as `write_file`. The list is read from the
  history of your files, so a file that you or the model moved or deleted
  later is not listed at its old path.
- **Rename.** Renaming a chat renames its folder to match the new title, and
  the list gives the new paths at once. Earlier messages keep the old paths.
  The model is told at its next turn that the folder was renamed and which
  path is current.
- **Delete.** Deleting a chat removes its transcript and its sandbox. Its
  folder (with its work folder) and every file the model wrote stay in your
  files.
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
(a command of the model wrote it) or `tool` (a tool of the chat wrote it,
such as `write_file`). `changed` marks a file the chat changed but did not
create. `media` marks an attachment that the model received as an image or a
video inside the message; the others reach the model by path. `path` works
with the files API above. `stale` lists old folder paths that earlier
messages name. A `files` event on the chat's event stream says that the list
may have changed; read it again.

### Files that a command saved

A command of `bash` (the Sandbox's, or the built-in one on this machine) saves
what it writes in your home folder as one change of the chat, the same way
`write_file` does. The chat then shows those files where it shows a file
tool's:

- **Under the step.** The `bash` step lists the files the command created or
  changed ("Saved to your files"), one chip each; a chip opens the file in
  the preview pane. A file the command deleted is listed by name, struck out.
- **In the pane and under the answer.** The files are in the chat's file list
  above, with the turn that made them, so they get a chip under the answer
  and the preview pane opens by itself on the newest one (a Word, Excel,
  PowerPoint or PDF file, an image or a text file), as it does for a file of
  `write_file`.
- **In the transcript.** The tool line of the call has a `card` with `files`:
  `[{"path":"/report.docx","rev":42,"op":"created"}]`, where `path` is inside
  your home, `rev` is the change set (undo it with `POST
  /api/chats/files/undo`) and `op` is `created`, `changed` or `deleted`. At
  most 50 are listed; `files_more` counts the rest. The card keeps its own
  `kind` and `id` (who served the call: `tool_provider` for the Sandbox,
  `host_exec` for this machine).

Only what the command saves under `/workspace/home` (or a writable shared
folder) is kept. `bash` starts in `/workspace/home`, so a relative path such as
`report.docx` is in your home. A file written anywhere else, such as
`/workspace/report.docx` or `/tmp`, is not saved, and the result of the call
says so by name. When you or a file tool changed a file while the command ran,
your version stays and the command's version is saved as a copy; the result
names the copy's path.

The list of files comes from the change sets of the chat in the shared store,
read after each call, so every frontend of a cluster shows the same files.
Nothing about it is kept in memory.

The list that the model gets has the newest 50 files, one line each with the
path, size, type, origin and turn, and a count of the rest. A chat with no
files gets no list. The list is built from the stored history when a turn
starts, so every frontend of a cluster gives the same one.

### Working files and the work folder

A chat that makes a document often writes a script, some intermediate data or
a log on the way. These working files must not mix with your own files, so
every chat has a work folder: `/Chats/<date> <title>/work/` in your files
(`/workspace/home/Chats/<date> <title>/work/` for the model), next to the
chat's attachments. It is a normal, visible folder.

- **What the model is told.** At every turn the prompt names the chat's work
  folder and says: scripts, helper code, intermediate data, logs, drafts and
  anything else you did not ask for go there; the file you asked for (the
  document, the spreadsheet, the image) goes where you said, or at the top of
  your home, and nowhere else; no new top-level folders for scripts; a command
  that runs once needs no file (`python3 -c`, a heredoc). The descriptions of
  `bash` and `write_file` and the built-in `docx`, `xlsx`, `pptx`, `pdf`,
  `localai-apps` and `localai-workspace` skills say the same. Pages
  (`pages/<name>/`) and the `LocalAI/` settings folder are not working files.
- **When the folder is made.** The chat takes the name of its folder at its
  first turn, but makes the folder only when it is used: before a `bash`
  command runs, or when a file is saved there. A chat that saves nothing
  leaves no folder behind. `bash` still starts in `/workspace/home`.
- **A gentle nudge, no refusal.** When `write_file`, `move_file` or a command
  leaves a helper file (see below) outside the work folder, its result gets
  one line that suggests moving it there, at most once a turn. Nothing is
  refused or moved: you may have asked for that script.
- **Delete.** Deleting a chat keeps its folder, with the work folder and the
  attachments in it, like every other file the chat wrote.

Each file of the chat's list has a `kind`: `deliverable` or `working`. A file
is `working` when it is in the work folder of any chat
(`/Chats/<chat>/work/...`), or when it looks like a helper: the extension
`.py`, `.sh`, `.js`, `.ts`, `.mjs`, `.rb`, `.pl`, `.ps1`, `.bat`, `.log` or
`.tmp`, or a folder named `scripts`, `tmp` or `scratch` on its path. Files of
a page, of the `LocalAI/` folder or of a shared folder, and the files you
attached, are always `deliverable`. The server decides the kind in one place
and puts it on the file list (`kind`), on the files of a command's card
(`files[].kind`) and on the card of a file tool (`file_kind`).

```bash
curl .../api/chats/3f1c.../files
# {"files":[{"name":"summary.docx","path":"/summary.docx","kind":"deliverable",...},
#  {"name":"build_document.py","path":"/Chats/2026-10-31 Summary/work/build_document.py",
#   "kind":"working",...}], ...}
```

In the chat:

- **The Files pane** lists what the chat made for you first. The working
  files are in a group of their own under it, **Working files · N**, folded
  and quieter; open it to see and preview them. The files you attached are
  listed as before.
- **The preview** that opens by itself after a turn shows the newest file
  that is not a working file. A working file never opens it by itself.
- **The chips under the answer** name only what the chat made for you.
- **A step** that saved working files says so in one line ("Also saved 2
  working files"); click it to see their chips.

An administrator can turn the steering off with **Work folder for helper
files** (`chats_work_folder`, Settings > Tools). The prompt then names no
work folder and no nudge is added; the pane still shows working files apart.
The nudge is counted per process: a turn runs on one process, so nothing is
shared, and a restart only lets the line come once more.

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
- A sub-agent cannot run commands when its parent cannot (see [Running
  commands](#running-commands)). A task that asks to run something ("execute
  the script", "run make_deck.py") is then refused at once with "the sub-agent
  has no way to run commands either", and no sub-agent starts.
- A task with the same words as an earlier sub-agent of the chat is refused
  while no file of the chat was created or changed since that run: it would
  answer the same. The check reads the store, so it holds on every frontend of
  a cluster.

```bash
curl .../api/chats/3f1c.../agents                      # runs, their state, the limit
curl .../api/chats/3f1c.../agents/9ab2.../messages     # one run's transcript
curl -X POST .../api/chats/3f1c.../agents/9ab2.../stop # stop one run
```

### Saved agents as sub-agents

A chat can also start one of your [saved
agents]({{%relref "features/chats-agents#saved-agents" %}}) as a sub-agent. This
is off for every chat until you switch **Saved agents** on in the chat's
**Tools** menu. The chat then has two more things:

- `list_agents` lists your saved agents: `id`, `name`, `description` and
  `model`, at most 100. It lists only your own agents.
- `spawn_agent` takes an `agent` argument, the id or the name (ignoring case) of
  one of them. The sub-agent runs with the agent's instructions, its model (or
  the chat's model when the agent names none) and only what the agent grants,
  the same as a chat started with that agent. `tools` cannot be given together
  with `agent`. The run is listed with the other sub-agents, with the agent's
  name in its title.

The sub-agent cannot start sub-agents of its own and never gets `list_agents`.
Each call checks again that the chat has the switch on, that an administrator
left `chats_saved_agents` on, and that you have the `agents` feature, as the
agents API does. When one of them fails, or the agent does not exist or is
another user's, the call fails with a message that says why and no sub-agent
starts. Without the `agents` feature the switch is shown greyed out with the
reason.

```bash
curl -s -X PUT localhost:8080/api/chats/$ID/builtins -d '{"off":[],"on":["saved_agents"]}'
```

`on` lists the opt-in groups that are on and replaces the earlier list; omit it
to keep them. An opt-in group in `off` is ignored. The group in `GET
/api/chats/{id}/tools` has `opt_in: true`, and `usable` and `reason` say whether
it can serve the chat now.

## Running commands

A chat runs commands and code with one tool, `bash`. Who serves it depends on
the server:

- **The Sandbox.** With the [Sandbox
  add-on]({{%relref "features/addons#sandbox" %}}) installed, running and on
  for the chat, `bash` runs in an isolated sandbox. The add-on's `bash`
  replaces the built-in one (see [Tools that replace a built-in
  tool]({{%relref "features/addons#tools-that-replace-a-built-in-tool" %}})),
  so the chat never has two.
- **This machine.** Otherwise, when an administrator allows it, the built-in
  `bash` runs commands directly on the machine that runs LocalAI, **with no
  isolation**. This covers a server with no Sandbox, a chat that switched the
  Sandbox off, and a Sandbox that is stopped or cannot run.
- **Nothing.** With neither, nothing in the chat runs code.

At every turn the model is told in one short block which of the three holds.
With host commands the block says that `bash` runs on this machine with no
isolation and with its network, and tells the model to be careful: no
destructive or data-sending commands outside the task unless you asked. When
nothing runs, the block says that the model must not say or imply that it ran
something, nor send a sub-agent to run it; it writes the result as a file
(text, CSV, Markdown, HTML, JSON) or gives you the script, and tells you how to
get commands.

A skill can say that it needs commands with `requires: [exec]` in its
frontmatter (see [Skills](#skills)). In a chat that cannot run commands, such a
skill is listed to the model as "unavailable here: needs command execution",
without the path to its procedure, so the model explains instead of starting.

### Network of the Sandbox commands

The Sandbox has no network device: its commands reach HTTP and HTTPS only
through a proxy that asks LocalAI about each connection (see [Network
access]({{%relref "features/addons#network-access" %}})). Each chat has a
**Network** row in its Tools menu with three choices:

- **Off**: commands reach nothing.
- **Package registries**: commands reach the package registries only (PyPI,
  npm, Go modules, crates.io, RubyGems, Maven Central and GitHub releases), so
  `pip install`, `npm install`, `go get` and `cargo build` work.
- **All public sites**: commands reach every public host.

A choice above the limit an administrator set (`chats_egress_ceiling`) is
greyed out with *Limited by your administrator*. When the limit is `off` (the
default), the row says *Off: the administrator has not allowed network access*
and gives an administrator a link to **Settings > Tools > Sandbox network**;
anyone else is told to ask one. The model is told the same, so it reports that
the server's setting blocks the network instead of waiting for an approval. A new chat starts with the
administrator's default. A fork keeps the choice and the allow rules of the
chat it copies. The choice and the rules live with the chat on the server,
never in your files, so the code that runs in the Sandbox cannot change them.
**Rules and requests** under the row lists the hosts this chat allowed or
denied, with a button to remove each, and the requests that asked. When an
administrator keeps a list of always-allowed hosts (`chats_egress_allowed_hosts`),
the row says how many there are and the panel lists them, read only, under
*Always allowed by your administrator*. They are reachable whenever the network
of the chat is on (Package registries or All public sites), without a card; with
the network off they wait until you turn it on.

The model knows that `curl`, `pip`, `npm` and `git` work in the Sandbox within this setting. It prefers `web_fetch` and `web_search` to read or search web pages when the chat has them, and uses `curl` for APIs, downloads and installs.

When a command asks for a host the chat does not allow but the limit would,
the chat shows a card: *A command in this chat wants to reach* `host:port`,
with **Allow once** (for the rest of that command), **Allow for this chat**
and **Deny**. The command waits up to 60 seconds for your answer and goes on
by itself after it. The card stays with the chat, so a reload shows it; once
answered it becomes one line under the `bash` call that asked, such as
*Allowed for this chat by you, 14:02*. A host inside your network needs an
administrator to allow it. If the limit was lowered while the card waited, the
card has no buttons: it explains that network access is off (or limited) on this
server and who can change it, and it ends as *Refused: the server's network
limit does not allow it*. A trigger, a connector run or a sub-agent never
shows a card.

When commands run on this machine instead, the row is one line, *Commands run
on this machine and use its network*: these settings do not apply there.

The routes, for the chat's owner (another user's chat is 404):

| Route | What it does |
|---|---|
| `GET /api/chats/{id}/egress` | `{preset, ceiling, effective, applies, applies_reason, registries, allowed_hosts, rules, cards}`. `applies` is false when commands run on this machine (`applies_reason` `host`) or no Sandbox serves `bash` (`none`). A rule the ceiling no longer admits has `above_limit`. |
| `PUT /api/chats/{id}/egress {preset}` | Sets the preset: `off`, `registries` or `all`. Above the ceiling it is 400 `egress_above_ceiling`. |
| `DELETE /api/chats/{id}/egress/rules/{host}/{port}` | Removes an allow or deny rule. |
| `POST /api/chats/{id}/egress/consent {id, decision}` | Answers a card: `once`, `chat` or `deny`. Allowing a host inside your network needs an administrator (403). The same answer sent twice is one answer (204, nothing changes). Another answer to a card already answered is 409 `already_answered`. A host the ceiling does not admit refuses the card and gives 400 `egress_above_ceiling` on this answer and on every later one, so a stale card never ends in a conflict loop. In `GET /api/chats/{id}/egress` such a card has `above_ceiling`: no answer can make it work. |

The chat's event stream carries `egress_consent` (`ids: [card id]`, `text:
host:port`) when a card asks and `egress_card` when the chat's network changes,
so every open tab reads it again. Administrators read the settings in force
and the audit with `GET /api/chats/admin/egress` and `GET
/api/chats/admin/egress/events?limit=&user=&chat=&verdict=&host=`, and see
both in Settings > Tools > Sandbox network. Its `sandbox` field names the
add-on that runs the commands now: of the running add-ons that replace `bash`,
the one chosen in `chats_tool_providers` or the one installed first, and none
when the chosen one is stopped. It is read from the add-on host each time, so
it is right after a restart and the same on every frontend of a cluster.

### Commands on this machine

The built-in `bash` is for a server that cannot run the Sandbox. Read this
before you allow it:

**First start.** While no model is installed, the Home page of an
administrator lists the first steps. The optional last step, **Run code
safely**, offers to install the Sandbox add-on (it opens the add-on browser at
the Sandbox) and says what happens without it: the model can only use the
built-in host `bash` fallback, which has full network access and no isolation.
Press **Skip** to hide the step (the browser remembers it). The step is not
shown when the Sandbox is already installed, or when add-ons are off.

- **No isolation, full network.** A command runs as the user LocalAI runs as.
  It can read every file that LocalAI can read and reach the internet, your
  local network, the LocalAI API on the loopback and cloud metadata addresses.
  Nothing blocks the network without privileges, so there is no "no network"
  option, and the network settings of the Sandbox (`chats_egress_*`) do not
  apply. Install the Sandbox to isolate commands.
- **Never as root.** When LocalAI runs as root, the built-in `bash` is not
  offered.
- **Your files.** Each chat gets a scratch folder with your files: `home/`
  there is `/workspace/home` of the file tools, and `shared-<name>/` is a
  shared folder. A command starts in `home/`, so a relative path is a path in
  your home and a shared folder is `../shared-<name>`. The skills are there
  too, read-only, at the names the file tools use: `shared-skills/` (shared by
  an administrator), `shared-builtin-skills/` (built in) and
  `shared-addon-skills/` (from add-ons), so a skill's scripts run in place, for
  example
  `python3 ../shared-builtin-skills/pdf/scripts/verify_pdf.py report.pdf`. A
  shared folder named `skills` is at `shared-skills 2/`. After each call, what the command changed is saved to your
  files as one change of the chat, which you can undo in Files, History; a
  file you or the file tools changed meanwhile is read again before the next
  call.
- **A clean environment.** A command gets `PATH`, `HOME`, `LANG` and `TZ`
  only: no API key, database address or token of the server.
- **Limits.** A call stops after `timeoutSeconds` (120 by default, 1800 at
  most), and the whole process group is killed then, when the turn is stopped
  and when the output passes 64 KiB. One call runs at a time in a chat, and at
  most four chats of one user run commands at once.
- **Audit.** Every call is recorded: user, chat, command (cut at 4 KiB), exit
  code, duration and whether it timed out, in the auth database (in a cluster,
  shared by every frontend), in a file under the data path, or in memory. The
  server log has one line per command with the prefix `[host-exec]`.
  Administrators read the audit with `GET /api/chats/admin/host-exec/events`
  or the assistant tool `list_host_exec_events`. Lines older than
  `chats_egress_audit_days` (default 30, the retention of the Sandbox network
  audit) are removed daily; in a cluster one frontend does it, under an
  advisory lock. The file store also keeps at most 10000 lines.

The runtime setting `chats_host_exec` (Settings, **Tools**, the `bash` row;
`LOCALAI_CHATS_HOST_EXEC`) decides who gets it:

| Value | Who gets the built-in `bash` |
|---|---|
| `off` | Nobody. |
| `ask` | Administrators, in chats they watch. Each command waits for a card in the chat. |
| `on` | Every user who may use chats, also unattended runs that have an agent, and sub-agents. |

Empty is the default: `off` on a server with accounts, `ask` without. In `ask`
mode the chat shows a card under the running call: "Run this command on this
machine?", the command, and **Run once**, **Always on this instance** (saves
`chats_host_exec: on`) and **Don't run**. A person who is not an administrator
can only refuse it. The card lives as long as the call waits, at most 5
minutes. The server keeps it with the chat, so a reload, or another browser
or frontend, shows it again and it can still be answered; once the call ends
the card is gone and the call's line says how it ended. `GET /api/chats/{id}`
carries the waiting card as `host_consent` (`id` and `command`) while the turn
that asked runs. The card names that turn: a card of an earlier turn is not
shown, also while a later turn runs, and is cleared when the next turn starts.
An answer to a card with nobody waiting on it (its turn has ended, its
frontend stopped, or a newer card replaced it) is `410 consent_expired`, and
the chat shows the card as not answered: the command did not run. The answer
is sent with:

```bash
curl -X POST .../api/chats/3f1c.../host-exec/consent -d '{"id":"<id of the host_exec_consent event>","decision":"once"}'
```

Only an administrator may send `once`; anyone who owns the chat may send
`deny`. The first answer counts: the same answer again is `204`, the other one
`409 already_answered`. The call's line in the chat then says what happened: run on this
machine, allowed once, refused or not answered (card `host_exec`). A call that
the Sandbox served carries the card `tool_provider` with the add-on's ID.

**Distributed mode.** The scratch folders belong to the frontend that runs the
turn, like the Sandbox's sandboxes: your files are the record, and a chat whose
next turn runs on another frontend gets a new folder there. The waiting card is
stored in the chat record (state mode: shared), so any frontend can show it;
its answer is written to the card in the record first and then sent on the
chat bus to the frontend that runs the turn, which also reads the card again
every two seconds, so an answer given through any frontend reaches it within
two seconds even when the bus message is lost, as for the network cards. A card
left by a frontend that died is not shown and cannot be answered: its turn no
longer holds the chat, and the reaper, or the next turn, clears it. The
network cards of the Sandbox follow the same rule. The setting is a runtime
setting like the others.

### What the web interface is told

`GET /api/chats/{id}/addons` says the same for the web interface in
`commands`: `available`, `off` (the Sandbox is installed but off for this chat,
or its agent does not name it, and the built-in `bash` is not allowed),
`unavailable` (installed, but it cannot run commands on this server;
`commands_reason` says why) or `none` (no add-on that runs commands is
installed for you). When commands run, `commands_provider` says who serves
`bash` (`builtin`, or the add-on's ID) and `commands_isolation` says `isolated`
or `host`. `commands_consent` is `ask` when each command waits for the card,
and `commands_fallback` with `commands_fallback_reason` name the add-on that
would serve but does not (`off`, or its reason). The **Tools** menu and the
chip next to it say "Commands run on this machine" with the reason, and link an
administrator to the Sandbox.

```bash
curl -s localhost:8080/api/chats/$ID/addons
# {"addons":[],"commands":"available","commands_isolation":"host","commands_provider":"builtin","commands_consent":"ask"}
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

The **Tools** menu over the message box lists the built-in web tools, the add-ons you may use, the MCP servers of the
chat's model and your connectors, with a switch each. What cannot be used now is
shown off, with the reason (a connector that is switched off, a model server
that the model does not use by itself). A switch takes effect at the next turn.
Switches only take away: a chat with a [saved agent](/features/chats-agents/)
lists only what the agent grants and never gets more than that. A trigger run with no
agent copies the switches of the chat that proposed it.

The menu groups the rows under **Built-in tools**, **Add-ons**, **MCP servers
of the model** and **Your connectors**. Each row has the name, a line of text
when there is something to say (why it cannot be used, how the Sandbox runs
commands), a chip with the add-on that serves a built-in tool ("Served by
add-on searx"), the number of tools of an add-on, and the switch. A click on
the name also toggles the switch, and Space toggles a focused switch. The
header gives the count of what is on among what can be used ("4 of 7 on"),
with **Select all** and **Disable all**. A group with two or more switches has
**All on** and **All off** for that group only. These buttons never change a
row that cannot be used, and **Select all** leaves **Saved agents** off: you
switch it on by hand. They send one request for each kind of switch (built-in
tools, add-ons, MCP servers and connectors). A change that the server refuses
goes back, and a line at the bottom of the menu says so. On a phone the menu
opens as a sheet at the bottom of the screen.

```bash
curl -s localhost:8080/api/chats/$ID/tools
# {"addons":[...],"servers":[{"name":"crm","off":false,"usable":true}],
#  "connectors":[{"id":"c1","name":"Docs","off":false,"usable":true}],"off":[]}
curl -s -X PUT localhost:8080/api/chats/$ID/mcp -d '{"off":["crm","connector:c1"]}'
```

`off` holds the name of a server of the model, or `connector:<id>`. At most 200 switches.

The built-in web tools have their own switch, by group:

```bash
curl -s -X PUT localhost:8080/api/chats/$ID/builtins -d '{"off":["web"]}'
```

The trigger tools have a group too, `triggers`: `propose_trigger`,
`list_triggers`, `update_trigger` and `delete_trigger` switch together, and the
menu shows them as one **Triggers** row. It is on by default; `{"off":["triggers"]}`
takes the four tools away from that chat. A chat started by a trigger or a
connector and a sub-agent never have them, and the row then shows as not usable
with the reason. The row also shows as not usable when you do not have the
`triggers` feature, and it is not listed at all when the server runs no
triggers (`LOCALAI_DISABLE_TRIGGERS`). The group has no runtime setting: the
start-up switch governs it. See
[Proposals](/features/chats-agents/#proposals).

The saved agents group (`saved_agents`) is opt-in: it is off until `on` names
it. See [Saved agents as sub-agents](#saved-agents-as-sub-agents).

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

A skill is a folder with a `SKILL.md`: a `name`, a one-line `description` and a procedure the model reads when a task matches. An optional `requires` key lists what the procedure needs; today the only value is `exec`, a way to run commands (`requires: [exec]`). A chat that cannot run commands lists such a skill as unavailable (see [Running commands](#running-commands)). At each turn the chat's prompt lists the skills it has, by name and description only, and tells the model to call the `load_skill` tool with a skill's name before a task that matches it. `load_skill` returns the instructions of the skill, the text after its frontmatter. It offers exactly the skills of that list: for each name the nearest skill, not switched off, and the skills of the add-ons the chat can use now. Any other name is refused with the names of the chat's skills and the near ones first (for example `Did you mean pdf?` for `PDF`).

When the folder of a skill holds other files, such as references and scripts, the answer starts with `Base directory for this skill:` and the folder, and names up to 20 of the files, relative to it, so the model reads them with `read_file`. A sub-agent that has no `read_file` gets the instructions only. A skill with `requires: [exec]` in a chat that cannot run commands answers that it cannot be used there. Every chat with at least one skill has `load_skill`, and a sub-agent gets it with its parent's other tools. It has no switch and Settings > Tools does not list it; the Skills menu chooses what it offers. The Sandbox does not see the skill folders (the built-in `bash` does, see [Commands on this machine](#commands-on-this-machine)). When the model reads a `SKILL.md` with `read_file` at a wrong path, the error names the right path and `load_skill`. A `SKILL.md` over 64 KiB is refused.

Skills come from five places, nearest first, and the nearest wins when two have the same name:

| Layer | Where | Who writes it |
|---|---|---|
| Folder | any `.skills` folder in your files | you |
| Personal | `.skills` in your home folder | you |
| Shared | for every user | an administrator |
| Add-on | ships with an installed add-on | the add-on owner |
| Built-in | ships with the server | the server |

The add-on layer holds the skills of the add-ons that the chat can use now: installed, running, usable by you and on for the chat (see [Add-on tools](#add-on-tools)). A chat with a saved agent gets them only for the add-ons the agent grants. The model loads one by its name with `load_skill`; its files are in `/workspace/shared-addon-skills/<add-on>/<skill>/`, where the add-on ID says where it comes from. Switching the add-on off for a chat takes its skills out of that chat's index, and the skill list shows each one with `addon` and `addon_title`. Its switch id is `addon:<name>`.

An add-on can also replace a built-in skill of the same name, for example the Office add-on's own `xlsx`, written for its tools. The add-on's skill then serves and the built-in one shows `shadowed: "addon"` with `shadowed_by`, the add-on's ID. When the add-on is off for the chat or not running, or you switch its skill off (`addon:xlsx`), the built-in skill serves again: the model is told in one line, and the built-in skill in the skill list carries `fallback`, the add-on's title. When several add-ons replace one skill, the one installed first serves until an administrator chooses another in Settings > Tools, under **Skills that add-ons replace**, or with the switch in the add-on's details on the Add-ons page.

In the **Skills** menu over the message box each skill says where it comes from: **Built-in**, the add-on's title, **Personal**, **Shared** or **Workspace**. A built-in skill that an add-on replaces is listed under the others, dimmed, with "Replaced by <add-on>" and no switch. When a built-in skill serves because the add-on's own skill is off or not running, one line under the title says so. The **Skills** page lists add-on skills under the **Add-on** layer, with the add-on's title as the badge, and says which skill replaces which. See [Skills of an add-on]({{%relref "features/addons#skills" %}}) for how an add-on ships them.

The format is the same as the skills LocalAI already used, so an existing skill carries over by copying its folder into one of the places above. Git sources and zip import and export are not part of chats.

```bash
curl -X POST .../api/chats/skills -d '{"layer": "personal", "name": "review", "description": "Check a change before it ships", "body": "Read the diff.\nSay what you find."}'
curl .../api/chats/skills?chat=3f1c...            # what the chat has, with its switches
curl -X PUT .../api/chats/3f1c.../skills -d '{"off": ["review"]}'   # switch one off for this chat
```

A chat keeps its own switches. Turning a skill off takes it out of the index at the next turn. A switch with the plain name turns off the skill that wins, so a skill of the same name further away shows through, as the list says. A switch with the id `layer:name` (the `id` in the skill list, for example `personal:review`) turns off the skill of that layer only. A deleted skill goes to the Trash like any file. Only an administrator writes shared skills, and everyone reads them. Your personal skills are as private as your files.

The **Skills** page lists them by layer and edits personal skills, and the **Skills** menu over the message box switches them per chat. The menu has one switch for each skill, with its name and a line of its description (the full text shows as a tooltip), the count of skills that are on ("12 of 20 on"), and **Select all** and **Disable all**. From nine skills on it also has a search field; while a search is on, the two buttons read **Select shown** and **Disable shown** and change only the skills in the list. Every change is one request, and a change the server refuses goes back with a message under the list.

A chat stores the skills that are **off**, not the skills that are on. So **Select all** sends an empty list, **Disable all** sends the ids of every skill in the list, and a skill you add later is on in every chat, also in a chat where you chose **Disable all**. The menu says so under its title.

The server ships a few skills of its own, in the built-in layer: `docx`, `xlsx`, `pptx`, `pdf`, `writing-skills`, `localai-workspace` and `localai-apps`. `localai-workspace` tells the model how to change your persona, profile, shortcuts, Home widgets and theme; see [Make LocalAI yours](/features/make-localai-yours/). `localai-apps` tells it to build an app, a tracker or a tool as a [Page]({{%relref "features/pages" %}}) and not as a program. The office skills are adapted from the Feather project (MIT licence); the NOTICE file in the same folder keeps the source and the licence. They tell a model how to work with documents in a sandbox, so they have `requires: [exec]` and are unavailable in a chat that cannot run commands. When the Office add-on runs, its own skills replace these four: they use its tools, which read, edit and create Word, Excel and PowerPoint files, and read and create PDF files, in your files, with no command runner.

## Web

A chat can search the web with `web_search` and read a page with `web_fetch`.
LocalAI serves both itself, with no add-on and no key, and they are on by
default. They only read. Every result is labelled as untrusted content from the
internet, and `web_fetch` refuses addresses inside your network. An
administrator can switch each tool off, set a SearXNG instance for the
searches and allow more ports in **Settings > Tools**. An add-on can replace
either tool by name: the optional Web add-on replaces both, to search with Brave
Search, Tavily or Google with a key and to read PDF files. See
[Web search and web pages in chats](/features/web/).

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
curl -X POST ".../api/chats/files/folders" -d '{"path": "/Reports"}'  # make a folder
curl -X PUT  ".../api/chats/files/content?path=/Reports/q1.txt" --data-binary @q1.txt
curl         ".../api/chats/files?path=/Reports"                    # list a folder
curl         ".../api/chats/files/content?path=/Reports/q1.txt"     # download
curl -X POST ".../api/chats/files/move" -d '{"from": "/Reports/q1.txt", "to": "/Archive/q1.txt"}'
curl -X DELETE ".../api/chats/files?path=/Archive"                  # to the Trash
curl         ".../api/chats/files/trash"                            # what can be restored
curl -X POST ".../api/chats/files/undo" -d '{"rev": 12}'            # restore it
curl         ".../api/chats/files/history?path=/Reports&limit=50"   # the changes, newest first (at most 200)
curl -X DELETE ".../api/chats/files/trash/12"                       # delete that one for good
curl -X DELETE ".../api/chats/files/trash"                          # empty the Trash for good
curl         ".../api/chats/files/preview?path=/Reports/budget.xlsx" # what a workbook holds
```

- **Nothing is erased, until you say so.** A delete goes to the Trash, and undo restores it as a new change. A change you made after the deletion that conflicts with the restore makes it answer `409`; with `skip_conflicts` the rest is restored.
- **Emptying the Trash.** `DELETE /api/chats/files/trash` deletes everything in your Trash for good, and `DELETE /api/chats/files/trash/{rev}` deletes one entry, the `rev` that `GET /api/chats/files/trash` lists. Both answer `{"files": 2, "folders": 1, "bytes": 4096}`, what they removed. The list gives the same three numbers for each entry, and `totals` for the whole Trash, also what a long list leaves out. A permanent delete removes the files and folders, their older versions and their entries in the history, and frees the space the bytes held a couple of minutes later, unless another file shares the same bytes. It works the same on a server with a database, also with several frontends on one database, and on one with the JSON index. It cannot be undone: undo of that change answers `404`. It touches only your own Trash; a `rev` that is not in it is `404`. The file tools of a chat cannot do this, only you can.
- **Two uploads, one name.** An upload replaces a file only when it says which version it started from (`base`). Otherwise both are kept, and the new one is named `name (from you).ext`; the answer says so in `conflict_of`.
- **Downloads are safe.** Files are served as downloads. With `inline=1` an image, PDF, plain text, audio or video is shown by the browser; anything else, HTML and SVG included, never runs as a page.
- **Attachments.** Send a message as `multipart/form-data` with a `text` field and `files`. The files are saved in the chat's own folder under `Chats/` and the model gets their paths after your text. With a turn running the files are saved at once and the message waits like any other; the model reads them by path when the message reaches it. A sandbox that started before the files arrived sees them from the next turn.
- **Attachments in the transcript.** The transcript keeps your text and your files apart. In `GET /api/chats/{id}/messages` a user line has `text`, the words you typed, and `attachments`: `path` (inside your home, for the files API), `name`, `size`, `mime`, `version`, and `missing` when the file is no longer in your files. A file whose chat folder was renamed gets its new path. A waiting message has the same two fields. The list of paths and the text read from the files go to the model only: the chat shows an image as a thumbnail and any other file as a chip with its size and a download link, and a click or Enter opens the file in the chat's files pane. Messages sent before this change are shown the same way.
- **Previews of documents.** `GET /api/chats/files/preview?path=` reads what a `.xlsx`, `.xlsm`, `.csv`, `.tsv`, `.docx` or `.pptx` file holds, for the preview pane of a chat and the **Files** page. A spreadsheet gives `{"kind": "table", "sheets": [{"name", "rows", "total_rows", "total_cols"}]}`: every sheet, the first 200 rows and 30 columns of each as text, with dates, booleans and the stored result of formulas. A Word document gives `{"kind": "document", "blocks": [{"type": "heading" | "item" | "paragraph", "level", "text"}]}` and a presentation `{"kind": "slides", "slides": [{"n", "title", "lines", "notes"}]}`; both keep at most 200 000 characters and set `truncated` when they cut. The formatting is not kept. The path rules are those of the download. A file over 20 MiB answers `413`, another type `415`, and a file that is not what its name says `422`. Each frontend keeps the recent answers in memory, by the content of the file: a new version is a new entry, so frontends of a cluster never disagree, and nothing is shared between them.
- **Search.** `GET /api/chats/files/search?q=budget` finds files by name, lines inside text files, and lines of the text of PDF, Word, Excel and PowerPoint files, ignoring case. A hit inside a document has `document: true` and its `page`, `sheet` or `slide`; `indexing` counts the documents whose text is still being read. Text files are read on each search: one over 1 MiB, and any other binary file, is found by name only, and a very large workspace is scanned up to a limit. See [Search inside documents](#search-inside-documents).
- **The model can use your files.** A chat with files on gives the model one set of file tools: `list_files`, `read_file`, `write_file`, `edit_file`, `move_file`, `delete_file` and `search_files`. They are the same with or without the Sandbox. What they change is listed in the history as an agent change of that chat, and you can undo it from the note under the call; a delete goes to the Trash. A sub-agent gets the file tools of its parent chat (or the ones its parent names).
  - **Paths.** `/workspace/home/<path>`, `home/<path>` and `/<path>` all name `<path>` in your home folder. `/workspace/shared-<name>/...` is a shared folder, read-only when it is shared with you for reading, and `/workspace/shared-skills/...`, `/workspace/shared-builtin-skills/...` and `/workspace/shared-addon-skills/...` are the shared, built-in and add-on skills, read-only. Any other path under `/workspace` is refused with the right spelling, and nothing is written: an app the model builds goes to a Page, `/workspace/home/pages/<name>/index.html`.
  - **`read_file`** reads lines `offset` to `offset+limit-1` (at most 2000 lines, text files up to 4 MiB). On a PDF, Word, Excel or PowerPoint file it reads the text of the document, with a `## Page N`, `## Sheet` or `## Slide N` line where each one starts.
  - **`edit_file`** replaces `old_string` with `new_string`. `old_string` must match exactly once, unless `replace_all` is true; no match and several matches are errors that say what to do. Text files up to 8 MiB. When the file changed while it was edited, the edit is saved as a copy next to it.
  - **`list_files`** lists one folder, or with `pattern` every path below the folder that matches it: `*` matches any characters including `/`, `?` one character, `[...]` a class. At most 500 paths.
  - **`search_files`** finds plain text in names, inside text files and inside the text of PDF, Word, Excel and PowerPoint files (at most 30 hits). With `regex: true`, `query` is a regular expression (the extended syntax of `grep -E`) matched against each line (at most 200 lines). Case does not matter unless `ignore_case` is false. `path` may be a file or a folder. A hit in a document reads `path (page 3, line 40): text`, with `sheet "Name"` or `slide N` for a workbook or a deck; the line is the `offset` to give `read_file`. When documents below the path are still being indexed, the answer says so (`2 files are still being indexed`), so the model searches again instead of concluding that nothing matches.
- **Pictures and recordings.** `describe_image` lets the model look at an image in your files and `transcribe_audio` turns a recording into text. A voice note attached to a message is transcribed on the way in and its transcript goes with your message; the file is kept as it is. They use helper models, which you choose in the settings (Chat image model, Chat transcription model). Left empty, `describe_image` uses the chat's own model when it can see images, and otherwise the first installed model that can see images (or, for audio, transcribe). If there is none, a cloud-proxy chat model that does not declare vision is tried as a last resort. With none installed the tool says what to install. The helpers run as you, so your model access and quota apply.
- **A file is at most 256 MiB.** Uploads of the Files page do not use the general upload limit (15 MB by default). They are limited by this size, unless you set `--upload-limit` (`LOCALAI_UPLOAD_LIMIT`) yourself: then your value applies to them too, and an upload is at most the smaller of the two. A folder listing reports the limit in force in `max_file_size` (bytes), and each entry has `modified`, when it last changed. A message with attachments is limited to 1 GiB in all.

### Search inside documents

When a PDF, Word (`.docx`), Excel (`.xlsx`, `.xlsm`) or PowerPoint (`.pptx`) file is uploaded, written by a chat, synced from a Sandbox or restored, the server reads its text in the background, usually within a few seconds. The file search of the **Files** page and the chats' `search_files` then find the document by what it says, even when its name is a random string.

- **What is read.** The text layer of a PDF, page by page; the cells of each sheet; the headings, paragraphs and tables of a Word document; the slides and their notes. A scanned PDF has no text layer, so it is skipped (no OCR in this version). At most 50 MiB of file, 200 pages and `chats_index_max_text_bytes` of text (2 MiB by default) are kept for one file; a file over a limit is cut or skipped. A file whose content does not match its type (a `.pdf` that is not a PDF) is skipped.
- **Where the text is kept.** In the server's own store, next to the index of the files: the auth database (SQLite or PostgreSQL) when there is one, otherwise a `textindex` folder under the chats' data folder. The text never becomes a file in anybody's folders, and it is not sent anywhere.
- **Who finds it.** The text follows the permissions of its file: each hit is checked against the file at search time, so only users who can read the file find it, in their home or in a shared folder. A file that moved or changed since it was read is not shown until it is read again. Deleting a file removes its text; emptying the Trash removes what is left.
- **Search engine.** On SQLite the index uses FTS5 with the trigram tokenizer, on PostgreSQL a `pg_trgm` index when the extension can be created; otherwise, and with the JSON store, the server scans the stored texts. The answers are the same: case-insensitive text, or a regular expression matched line by line.
- **Settings.** **Search inside documents** (`chats_index_files`, on by default) and **Text kept per document** (`chats_index_max_text_bytes`) in Settings > Tools. With the switch off nothing new is read; text read before is still used for files that did not change.
- **Existing files.** At its first start the indexer reads the files that are already there, in steps, and goes on where it stopped after a restart.
- **Distributed mode.** Single-runner: one frontend at a time reads files, under an advisory lock, and the text is in the shared database, so every frontend searches the same text. An upload on any frontend is queued in the database at once, so its search says the file is being indexed. A chat worker searches the text but never reads files for the index.

### The Files page

The **Files** page in the sidebar is a file browser for all of this.

- **Browse.** A breadcrumb shows where you are; a deep path folds its middle into a menu. Folders come first. Click a column (Name, Modified, Type, Size) to sort by it, and again to reverse it. **List** shows a table; **Thumbnails** shows tiles with a picture of each image. The page remembers the view and the sort in your browser. Dates read as "5 minutes ago" or "yesterday"; hold the pointer on one for the full date. Back and Forward of the browser go between folders.
- **Filter and search.** Typing in the box filters the open folder by name. Press Enter, or choose **Search all files**, to search the names and the text of every file.
- **Upload.** Use **Upload** (its arrow also uploads a whole folder), drop files or folders anywhere on the page, or paste with Ctrl+V; a pasted screenshot gets the date and time as its name. A drop on a folder row, or on a folder of the breadcrumb, uploads into that folder. The upload panel at the bottom right shows each file with its progress, speed and time left, and the total for all of them. You can cancel a file, try a failed one again (the panel says why it failed), and keep working: an upload keeps going when you open another folder or another page of the app, and the browser asks before you close the tab while files are still on their way. A file over the server's limit is refused before it is sent. When an upload has the name of a file that is in the folder already, the page asks: **Replace** it, **Keep both** (the new one gets a new name) or **Skip** it.
- **Select.** Click a row to select it, Ctrl-click (Cmd on a Mac) to add one, Shift-click for a range, or use the boxes. The bar above the list then offers **Download**, **Move to…** and **Move to Trash** for the selection.
- **Act.** Each row has a menu (⋮, or a right click): **Preview** or **Open**, **Download**, **Rename**, **Move to…** and **Move to Trash**. Drag rows onto a folder to move them there. **New folder** and **New file** add one in place, with a name you can type over. A move, a rename and a delete show a message with **Undo**.
- **Keyboard.** Arrows move between rows (and tiles), Shift+arrows extend the selection, Space selects, Enter opens, F2 renames, Delete moves to the Trash, Ctrl+A selects all, Escape clears the selection, Backspace opens the parent folder, and typing the start of a name jumps to it.
- **Preview.** Opening a file shows it in a side pane, the same views as the preview pane of a chat, with **Download**, **Rename**, **Move to…** and **Move to Trash**. On a phone it is a sheet from the bottom. Escape closes it.
- **Trash and History.** The **Trash** tab restores what you deleted, deletes one item for good, or empties the Trash; each of those asks first and says how many files and how much space it removes, and **Empty Trash** is off while the Trash is empty. The **History** tab undoes a change of the open folder.

LocalAI keeps two folders of your home for itself: `LocalAI/`, the settings it
reads (persona, profile, shortcuts, Home, theme; see [Make LocalAI
yours](/features/make-localai-yours/#the-localai-folder)), and `Chats/`, one
folder per chat. **Files** lists your own files first and these two last,
under **Managed by LocalAI**; the list API marks them with `managed`
(`localai` or `chats`). Moving one to the Trash or renaming it asks first and
says what stops working.

### Your own rules: AGENTS.md

Put an `AGENTS.md` file in a folder to give the assistant rules for that
folder: how to name files, which language to write in, what not to touch.
LocalAI never sends the text of the file to the model. It sends one line that
points at it, and the model reads the file with its file tools:

```
Rules for this folder are in /workspace/home/Reports/AGENTS.md. Read that file before you start.
```

- At the start of each turn, the line names the `AGENTS.md` of the chat's own
  folder (`Chats/<chat>/`), of the folders above it, and of your home, nearest
  first, two at most. It is part of the note of the turn, not of the system
  prompt, so it costs about 25 tokens a file and only when such a file exists.
- The first time in a turn that a file tool (`list_files`, `read_file`,
  `write_file`, `move_file`) works in a folder whose `AGENTS.md`, or the one
  of a folder above it, was not named yet, the same line is added to the
  result of that call. Each file is named once a turn. Reading the
  `AGENTS.md` itself counts.
- A [trigger run](/features/chats-agents/) already gets the rules of its
  folder and the folders above it in its first message, so it gets no line for
  them.
- The checks run as you, on the shared workspace, and keep nothing between
  turns, so they work the same with several frontends.

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
everything else in the chats works. A chat then has no skills either, not even
the built-in ones, and no `load_skill`: skills are files, and every built-in
skill works on files or runs commands.

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

When chats live in memory, the chat page says so, and the list endpoint
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

**Sub-agents.** The runs are chats in the shared store, linked to their parent. The background limit, the list of runs and "stopping a chat stops its sub-agents" all read the store, so they hold when the turns of one chat run on different frontends. A stop reaches a run on another frontend over the bus. The tool subset of a running sub-agent is kept in the memory of the frontend that runs it, which is where it runs. A sub-agent started from a saved agent keeps nothing in memory (stateless): the agent is named on its chat record, and its tools are read from the store at each turn, as for any chat with an agent. The chat's saved agents switch is stored with the chat, and the permission is checked again at each call, so every frontend gives the same answer.

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

## Chats in the web UI

The web UI talks to this API, so a chat keeps running when you close the tab
and opens again from any browser.

- **Home starts a chat.** Home (`/app`) is the front door. Its message box is
  the one of the chat page, so everything a new chat offers works there before
  the chat exists:
  - pick the model (each row says whether it is loaded, what it can do and how
    it fits this machine) and a [saved agent](/features/chats-agents/);
  - switch tools, add-ons and skills in the **Tools** and **Skills** menus.
    They are a draft: the chat starts with them (`switches` in
    `POST /api/chats`);
  - attach images, audio and other files with the buttons, or paste an image.
    They go with the first message;
  - dictate, use the shortcuts row above the box, and type `/` for the Home
    actions;
  - `Enter` sends and `Shift+Enter` adds a line.

  The text you type on Home stays in the browser across a reload until you
  send it (staged files stay only until the page closes). **Send** creates the
  chat, sends the message with its files and opens the chat at
  `/app/chats/<id>`. If the message cannot be sent, the new chat is removed and
  the text and files stay on Home. Home also lists your newest chats under
  **Jump back in**.
- **The sidebar** lists your most recent chats under the main entries, with
  **New chat** (the `+` beside **Recent chats**, which opens Home) and **All
  chats**. When the sidebar is collapsed, only the **All chats** icon shows.
  There is no separate Chats entry: Home is where a chat starts.
- **All chats** (`/app/chats/all`) is your chat history: open it from the
  sidebar or from **All chats** next to **Jump back in** on Home. Type to find
  a chat by its name or by what was said in it (the server search); the line
  under a match shows where it matched. Open, rename (`F2`), copy, export as
  Markdown or delete one, clear them all, or import a JSON export. A deleted
  chat can be brought back for a few seconds with **Undo**; then the server
  deletes it.
- **Old addresses.** `/app/chats` with no chat id opens Home. A `?model=` in
  the address selects that model on Home, so `/app/chats?model=<id>` and the
  older `/app/chat/<model>` still start a chat on that model.

## The chat page

The chat page (`/app/chats/<id>`) shows one chat.

- **Conversations.** `Ctrl+K` (`Cmd+K` on a Mac), or **Chats** in the header,
  opens the same list as **All chats** over the chat, to move to another chat
  without leaving the page.
- **A new chat.** **New chat** in the **More** menu, in the `Ctrl+K` list and
  `/new` in the message box open Home. A chat with a [saved
  agent](/features/chats-agents/) starts from the agent's row on the Agents
  page: the chat page opens empty with the agent chosen, and its **Tools** and
  **Skills** menus are a draft for the new chat, as on Home. Picking another model
  in an open chat moves the chat to it from the next turn (not while a turn
  runs). `/assistant` starts a chat with the LocalAI management tools
  (administrators, when the assistant is on).
- **Chat settings** (the sliders icon in the header) holds the settings above:
  a system prompt, temperature, top P, top K, context size and reasoning
  effort. **Save settings** sends them all; they apply from the next turn.
- **While the model works** you can keep typing and attach files. A message you
  send waits over the message box and reaches the model at its next step;
  edit or cancel it until then. `Esc` or **Stop** ends the turn; waiting
  messages stay for **Send now**. `Up` in an empty box edits the last waiting
  message, or puts your last message back in the box.
- **Drafts stay with their chat.** What you type or attach in a chat stays
  there when you open another chat, and comes back when you return; a new chat
  starts empty. The browser keeps the text of each draft (not the files)
  across a reload, and drops it when you send it or delete the chat. A waiting
  message always goes to the chat you wrote it in, also when you open another
  chat before the server answers.
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
  skills for this chat (see above; a skill is switched by its `id`, and the Skills menu has Select all, Disable all and a search), and list
  the sub-agents of the chat with their transcript and a **Stop** for a
  running one. The Tools menu says whether the Sandbox add-on runs commands
  isolated, directly on this machine, or not at all. When the chat cannot run
  commands, the Tools menu opens with a notice, "Commands can't run here", and
  the reason: no add-on that runs code is installed, the Sandbox is off for
  this chat, or it cannot run on this server. An administrator gets a link to
  install the Sandbox or to its add-on page; anyone else is told to ask an
  administrator. A chip with the same words sits next to the Tools menu (on a
  phone, only its icon) and opens the notice. **Hide** removes the chip and
  leaves one line in the Tools menu; this browser remembers it until the
  reason changes. The Tools menu shows for this notice even when there is
  nothing to switch. Changes an add-on tool
  made show under the tool line with **Undo** when the add-on can undo them; a
  call the add-on refused (`not_permitted`, `resource_unreachable`,
  `not_running`, `invalid_request`) says why. MCP Apps show live under their
  tool call.
- **The preview pane** on the right shows what the assistant made. It opens
  by itself when a turn creates or changes a file, or when an answer holds a
  result: a whole HTML page, an SVG, a code block of 25 lines or more, or a
  block the answer presents as a file. A short snippet never opens it. It
  opens beside the thread from 1280 px and over it below that; on a phone it
  never opens by itself. It does not take the focus from the message box, and
  a screen reader hears "Preview opened: budget.xlsx". Close it and it stays
  closed in that chat (this browser remembers it) until you open it again.
- **Chips under an answer** name the files its turn made: "Created
  budget.xlsx · Open preview" or "Changed ...". A chip opens the pane on its
  file. **Files** over the message box opens and closes the pane.
- **What the pane shows.** The selected file or code block on top, with
  **Download**, **Copy** (text and code) and **Open in Files**, then the
  chat's files in two groups, **Created and changed** and **You gave it**,
  each with its size and turn. Spreadsheets, CSV and TSV files show as a table
  with a tab per sheet and the first 200 rows; Word documents and
  presentations as their text (the formatting is not shown); PDF files in the
  browser's viewer; HTML files run in the sandbox of
  [Pages]({{%relref "features/pages" %}}) with no network and no bridge;
  images, Markdown, JSON, code, audio and video with their own views; any
  other type as a card with **Download**. An add-on that previews a type (the
  `files.preview` slot) replaces the built-in view for it. Drop files on the
  pane to attach them to your next message. A file the model received as an
  image or video is marked **In context**.
- **Code blocks** in an answer have **Copy** and **Download**, and a block
  that is a result has **Open preview**.
- **A trigger the model proposes** shows as a card under its tool line with
  **Create trigger**, **Edit** (name and schedule or folder) and **Dismiss**.
- **A change of a trigger the model asks for** shows as a card under its tool
  line: an edit lists each field before and after with **Apply** and
  **Dismiss**, a delete shows the trigger with **Delete** and **Keep**, and a
  pause or a resume, which applies at once, is a receipt with **Undo**. See
  [Changes from a chat](/features/chats-agents/#changes-from-a-chat).
- **Shortcuts** over the message box are the prompts of your
  `LocalAI/shortcuts.json`, or the default starters without it. A click puts one in the
  box; **Edit shortcuts** changes the list. See [Make LocalAI
  yours](/features/make-localai-yours/).
- **Find** (`Ctrl+Shift+F`), focus mode, copying the chat as Markdown and
  the keyboard shortcuts work as before.

Administrators find every user's agents, triggers and connectors under
**Operate > Settings > Chats of all users**. The [agent tasks and
jobs](/features/agent-jobs/) have a page of their own, **Agents > Tasks**
(`/app/agent-tasks`).

## Troubleshooting a failed reply

When a turn fails, the thread shows one banner: "The reply failed", the reason in plain words, what to do about it, a **Retry** button and, under **Technical detail**, the error text with addresses, paths and keys removed. The full cause is in the server log. The banner names the cause when LocalAI can tell:

| Reason in the banner | What it means | What to do |
|---|---|---|
| The model "x" could not be started: its backend is not installed | The model needs a backend that is not installed. | Install the backend from the Backends page (the banner links to it for administrators), or pick another model. |
| The model "x" failed to start a moment ago and is resting | A load failed and LocalAI waits before it tries again. | Try again after the number of seconds the banner gives. |
| The model "x" could not be started | The backend started but the model did not load. | Check the model's configuration and the backend log. |
| The model "x" was not found | No model has that name. | Pick another model, or install it from the Models page. |
| The upstream refused the key | A cloud-proxy model's provider answered 401 or 403. | Check the API key in the model's settings. |
| The upstream does not accept this request at that address | The provider answered 404 or 405. | Set `upstream_url` to the full endpoint, for example `.../v1/chat/completions`. |
| The upstream address could not be reached | The host name does not resolve, or nothing answers there. | Check the address in the model's settings. |
| The provider behind the model returned an error | The provider or a proxy answered with a server error several times in a row. | Retry in a moment, then check the provider. |
| The model took too long to answer / The turn was cancelled | A timeout, or the turn was stopped. | Retry, or use a smaller or faster model. |

A failure that the page learns about only after a reload shows the banner without a reason, and only for a day after the turn. A new turn clears the banner.

## Moving from the old chat

The old chat page, the agents, the agent jobs and the old skills were replaced by the chats. At the first start the server converts what they left; see [Moving from the old chat and agents](/features/chats-migration/).

## API reference

The routes are listed under the `chats` tag at `/swagger/index.html`, and
`GET /api/instructions/chats` returns a machine-readable summary.
