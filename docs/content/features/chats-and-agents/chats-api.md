+++
url = "/features/chats-api/"
disableToc = false
title = "Chats API"
description = "Start a chat, send messages, read events, change settings, search and export, with the HTTP routes."
weight = 19
devbuild = "feature"
+++

Use the chats API to start a chat, send messages, read the reply as events, change settings, search and export. The examples use `localhost:8080`. When the server has API keys, add `-H "Authorization: Bearer $API_KEY"`. For what you can do in the web UI, see [Chats]({{% relref "/features/chats-and-agents/chats" %}}).

## API reference

The routes are listed under the `chats` tag at `/swagger/index.html`, and
`GET /api/instructions/chats` returns a machine-readable summary.

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

# After a dropped connection, send the id of the last event you got.
curl -N 'localhost:8080/api/chats/3f1c.../events?after=9d2e41.57'
```

The events of a running turn have an id, `<run>.<n>`, sent as the SSE `id` field.
A client that lost its connection sends the last id back in the `after` query
parameter, or in the `Last-Event-ID` header (a browser sends that header when it
reconnects by itself; the header wins). The server first sends the events it
missed, then the live ones. Deltas of text and reasoning that came one after the
other are not sent again, and the ones the client had are cut off. The server
keeps the last 2048 events or 4 MiB of each running chat, and the ring stays
for 30 seconds after the turn ends, so a client that reconnects at once also
gets `turn_end`. Events that do not come from the turn itself (`files`, `goal`,
`dock`, `loops`, `cleared` and the cards) have no id and are not replayed: they tell
the client to read the record again.

When the server cannot give what was missed, the stream starts with a `resync`
event. This happens when the id is of another turn, is older than the ring
holds, or is not an id of this server, and in a cluster when the server that
runs the turn is gone or does not answer in 2 seconds. Read the transcript, then
go on: the `resync` event carries the id of the newest event known, when there
is one, and the live events follow it. A gap in the numbers of the live events
is filled from the ring in the same way.

A user can have 8 event streams open on one chat on one server; one more gets
HTTP 429 until one closes. The counter `localai_chat_event_resyncs_total` counts
the reconnects that got a `resync`, by `reason`: `local` (the ring of this server
did not have the events), `owner` (the ring of the server that runs the turn did
not have them), `unreachable` (that server did not answer) and `no_owner` (no
other server is known to run the turn). A steady rate of `local` or `owner`
says that the ring is too small for your clients' gaps.

The transcript lists messages, tool lines and one checkpoint line per turn.
Undo works per tool call, not per turn: each `write_file`, `edit_file` or
`move_file` call is its own change set in your files, and its tool line carries
a card `{"kind": "files_change", "id": "<rev>:<path>"}` (`delete_file` moves to
the Trash, where you restore it).
`POST /api/chats/files/undo {"rev": N}` undoes that change set; the chat page
uses this for the Undo of its change notes (see
[Make LocalAI yours]({{% relref "/features/chats-and-agents/make-localai-yours" %}}#what-a-change-shows)). To
undo a whole turn, undo the changes of its tool lines, newest first. The
checkpoint line marks the end of the turn; its `rev` is `0` and its `text` is
empty, because the turn's changes are already kept call by call.

A chat is named after the first line of its first message, unless you gave it a
title. To rename it, see [Settings, rename and model change](#settings-rename-and-model-change).

### Start a chat with its switches

A chat can start with tools, add-ons and skills already switched off, and with
opt-in tools switched on. `switches` in `POST /api/chats` takes each list in the
form its own endpoint takes (see [Switch tools off for one
chat]({{% relref "/features/chats-and-agents/chats-tools#switch-tools-off-for-one-chat" %}}) and [Skills]({{% relref "/features/chats-and-agents/chats-skills#skills" %}})). The chat is
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
    "skills_off": ["addon:pdf"]
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

The draft add-ons list also takes the switches the composer has so far, as
comma-separated query parameters `addons_off`, `builtins_off` and `builtins_on`
(the lists that `POST /api/chats` takes in `switches`). The `commands` state of
the answer is then the one the new chat would have: with `builtins_off=shell`
it is `switched_off`. A name that is not an add-on switch or a built-in group
is `400`. The Home composer sends them each time you change a switch.

### List the chats of one origin

`GET /api/chats` returns every chat unless you send `origin`. With
`origin=person` it returns only the chats a person started, with
`origin=automated` only the chats that something else started, and with an
origin kind (`trigger`, `connector`, `job`, `page`) only the chats of that kind.
Each chat carries `origin` (empty for a person) and `origin_id` (the trigger,
binding, job or page that started it). With `origin` set, `limit` counts the
chats kept and sub-agent runs are left out, so a burst of chats from a bot
cannot push your own chats out of the first page.

```bash
curl -s 'localhost:8080/api/chats?kind=chat&origin=person&limit=8'
curl -s 'localhost:8080/api/chats?kind=chat&origin=automated&limit=200'
```

## Keep typing while the model works

Send another message while a turn runs and it waits. The response says so:

```json
{"started": false, "pending_id": "p2"}
```

Waiting messages reach the model together, in the order you sent them, at its
next request. A waiting message shows the files you attached to it as the same
chips a sent message has. Until it is delivered you can change or remove it:

```bash
curl -X PATCH  .../api/chats/3f1c.../pending/p2 -H 'Content-Type: application/json' -d '{"text": "Use euros."}'
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

## Talk to a chat (Live)

Live lets you talk to an open chat with your voice. You speak, the chat
answers, and a voice reads the answer aloud. Then Live listens again. You do
not press a key.

What you need:

- A microphone, and a page opened over HTTPS or from `localhost`. A browser
  gives microphone access only to those pages. On any other address the
  **Live** button is off and says why.
- One model for speech to text (for example `whisper-base`) and one model for
  the voice (for example `kokoro-multi-lang-v1.0-sherpa`).

Start Live with the **Live** button next to the message box of a chat. If a
model is missing, the button opens a sheet that shows the missing parts. An
administrator can install a small model for each part with one click. A user
who is not an administrator sees the names of the models and must ask an
administrator to install them. When both models are installed, press **Start
Live**.

You can also press **Live** on the message box of Home. LocalAI creates a chat
with the model, agent and settings you picked on Home, opens it, and starts
Live there. If a model is missing, the chat opens with the setup sheet. The text
you typed on Home stays on Home. If you end Live before you say anything, the
empty chat stays in your chat list.

Your audio is sent to your LocalAI server. The server turns your speech into
text and makes the voice. Your audio stays on the LocalAI instance, and the
browser does not use a cloud speech service. The text of the chat goes to the
chat model as in a typed chat, so it goes where that model runs.

The dock shows a **Standard** chip. In this mode Live does not listen while the
voice speaks. You cannot interrupt the voice, and the voice cannot hear itself.
To cut the voice short, press **Stop**. Live then listens again. Live is
half-duplex per browser tab, so use Live in one tab per chat.

Live uses the same chat, not a separate one. Your words appear in the thread as
normal messages. Tools, history, files and the settings of the chat work as in
a typed chat. A turn that fails is not read aloud, and Live listens again.
**Esc** and the **Stop** button of the message bar stop a running turn and keep
Live on. When the turn is one you started by voice, the part of the reply that
was already written is not read aloud, and Live listens again.

Other controls: **Mute** and **Unmute** the microphone, **Type instead**
(ends Live and puts the cursor in the message box) and **End Live**. Live
pauses while the browser tab is hidden. If the browser blocks the audio (some
browsers allow sound only after a click), the dock shows a **Play** button.
Press it to hear the answer.

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
  decides to compact the conversation. It does not reload the model and does
  not change the window the backend loads, so it cannot make a small model
  large enough; only the model's own `context_size` does that.
- A new `title` (1 to 80 characters) or `model` needs a chat with no turn running (409 otherwise).
  The history stays when the model changes.

In the web UI, the **Effort** chip in the message box (next to Tools and
Skills, on the chat page and on the Home page) and the chat settings sheet (`/settings`, or the sliders icon in the
chat header) show and change the same `reasoning_effort`. The chip and the
effort buttons in the sheet apply at once. In the sheet, temperature, top P and
top K are sliders with a number box and a reset button; an empty value means
the model's own default. The sliders cover 0 to 2 (temperature), above 0 to 1
(top P) and 0 to 200 (top K). You can type a larger top K in the box. These
values and the system prompt and context size are sent when you press **Save
settings**. `reasoning_effort` `none` asks the model not to think.

A level picked on the Home page is saved to the new chat when it is created,
before the first message is sent, and the chat page opens with it.

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
curl -X POST .../api/chats/3f1c.../messages/12/edit -H 'Content-Type: application/json' -d '{"text":"A better question"}'
# Copy the chat up to and including line 14 into a new chat.
curl -X POST .../api/chats/3f1c.../fork -H 'Content-Type: application/json' -d '{"from_seq":14,"title":"Other idea"}'
# Find chats by title or by what was said, in all your chats.
curl '.../api/chats/search?q=invoice'
# Take a chat out as Markdown (default) or JSON, and bring a JSON file back in.
curl '.../api/chats/3f1c.../export?format=json' > chat.json
curl -X POST .../api/chats/import -H 'Content-Type: application/json' -d "{\"chat\": $(cat chat.json)}"
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

### Search your chats

`GET /api/chats/search?q=...` searches the titles and the messages (yours and the model's) of all your chats. It does not search the runs of sub-agents, and it never returns a chat of another user.

- **Words and phrases.** A message matches when it holds every word of `q`, in any order, as part of a word or whole. A phrase in double quotes counts as one word: `q="port of qdrant"`. The search ignores case.
- **Ranking.** The best match comes first: a message with the whole phrase or with whole words ranks above one with word parts, and newer messages rank above older ones. A search ranks the newest 600 matching messages.
- **One hit for each chat.** A hit carries `chat` (with the agent and the origin), `seq` (the line of the message), `role`, `at`, a `snippet` and `highlights`: the parts of the snippet that match, as `start` and `end` offsets in UTF-16 units, so a client can mark them without parsing HTML. Add `group=message` to get every matching line.
- **Filters.** `agent` (a saved agent), `origin` (`person`, `automated`, `trigger`, `connector`, `job` or `page`), `role` (`user` or `assistant`), `since` and `until` (a date `YYYY-MM-DD` or an RFC 3339 time; a date in `until` includes that whole day) and `limit` (default 50, at most 200).
- **In the web UI.** The search box of the Chats page and of the Ctrl or Cmd+K dialog uses this search. From two characters it lists the hits best first, each with the chat, who wrote the line, its date and a snippet with the matched words marked. Three filters (agent, source, date) narrow it, and **Reset filters** takes them off. Arrow keys move through the hits, Enter opens one, and Escape clears the search first and closes the dialog next. A hit opens its chat at the message: the address `/app/chats/<id>?seq=14` scrolls to line 14 and marks it for a moment. The list says when it is still searching, when nothing matches, when the search failed (with **Try again**), and when older chats are still being indexed.
- **After an upgrade.** The server keeps a search index. When it starts with chats from before the index, it adds them in the background and logs the progress; the answer says `indexing: true` until that ends, and older chats can be missing meanwhile. The index uses SQLite FTS5 (trigram), or `pg_trgm` on PostgreSQL; without them the server scans. Deleting a chat, or regenerating or editing from a message, removes the old text from the index in the same step.
- **Chats in files.** A server that keeps chats in files (no database) has no index. It scans the newest chats first for up to two seconds and sets `partial: true` with `searched_chats` and `total_chats` when it stopped early.

## Images, video and documents

Attach files with `multipart/form-data` on `POST /api/chats/{id}/messages`
(see [Start a chat and send messages](#start-a-chat-and-send-messages)). In the chat page, one **Attach file** button takes files of any
type (images, audio, documents, archives such as `.zip`); you can also paste an
image, or drag files over the page and drop them. The same works on Home. A
drag of text is ignored, and with files off nothing is staged. What the model gets depends on the file and
the model:

- **Images.** When the chat's model can look at images (a multimodal
  projector, `vision` in `known_usecases`, or `image` in
  `known_input_modalities`) the image goes to the model inside the message,
  as an `image_url` part, and the model answers about it directly. A model
  behind a cloud proxy cannot be asked what it accepts: declare `vision` in
  its configuration (see [Cloud passthrough proxy]({{% relref "/administration/cloud-proxy#images" %}})).
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
- **Audio.** A voice note is transcribed when a transcription model is set.

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

## Metrics

With metrics on, `/metrics` counts every turn of the loop (a chat in the page, a
saved agent, a trigger run, a connector message and an agent job):

| Metric | Type | Labels |
|---|---|---|
| `localai_agent_runs_total` | counter | `agent` (the saved agent's name, empty for a plain chat), `origin` (`chat`, `trigger`, `connector` or `job`), `outcome` (`completed`, `error` or `cancelled`) |
| `localai_agent_run_seconds` | histogram | the same labels |

They keep the names the old agent pool had, so an existing dashboard works. The
`origin` label is new.
