+++
url = "/features/chats-connectors/answers/"
disableToc = false
title = "How a bot answers"
description = "What a message from a messaging app gets, and how to set the working message, conversations, files and reply language."
weight = 30
devbuild = "addon"
+++

See what a message from a messaging app gets, and set how the bot answers: its working message, its conversations, the files it sends and takes, and its reply language. The first run needs none of this. Set it when the defaults don't fit.

## What a message gets

- **The agent's tools only.** A message gets exactly the tools the agent grants,
  intersected with what you may use now. It never gets the tool set of a chat, and
  a message cannot change the agent. A run that a trigger started never gets
  `propose_trigger`. A connector chat gets `propose_trigger` and `list_triggers`
  only while the binding holds `triggers.propose`, and `update_trigger` and
  `delete_trigger` only while it holds `triggers.change` (see
  [Triggers from a connector chat]({{% relref "/features/chat-apps/actions#triggers-from-a-connector-chat" %}})).
- **Your account.** The chat acts as you, so the feature gate, the model
  allowlist and your quota apply. If the model refuses the request, the sender
  gets one fixed sentence, for example that your quota is used up.
- **One chat for each sender, by default.** Each person who writes to the bot has a
  chat of their own with the agent. A Slack thread is one chat for everyone in it (see
  [Slack]({{% relref "/features/chat-apps#slack" %}})). They cannot see another person's chat. The conversation mode of
  the binding can change this (see [How the bot answers](#how-the-bot-answers)). If
  you change the agent of the binding, the next message of each sender starts a new
  chat with the new agent.
- **Text, files and voice messages.** The add-on hands the files of a message to the
  chat: on Telegram photos, voice messages and some other files (see
  [What a person can send]({{% relref "/features/chat-apps#what-a-person-can-send" %}})), on Slack any file (see
  [Slack]({{% relref "/features/chat-apps#slack" %}})). A file is attached to the chat as if you had attached it on the
  chat page: it is in **Files**, and an image goes to a model that can see. A voice
  message (any audio file) is transcribed, and the transcript is what the agent reads
  (see [Voice messages]({{% relref "/features/chat-apps#voice-messages" %}})). A file over the [file limit](#file-limits) gets a
  sentence that it is too large. With the files of the chats turned off, a message with a file gets a
  sentence that files are off, and its text still reaches the agent; a voice message
  is still transcribed. A sticker, a poll or a location gets a sentence that names
  what the bot reads. A message longer than 8,000 characters is refused. A sender may
  send 20 messages a minute; more are dropped, with one notice a minute.
- **Private chats, and mentions.** A message in a group that the bot was added to is
  ignored, unless the add-on marks it as addressed to the bot: a Slack mention or a
  reply in a thread the app follows, or on Telegram a message that names the bot
  (`@your_bot`, taken out of the text) or answers one of its messages. To read every
  message of a Telegram group, a bot needs its privacy mode off in `@BotFather`; a
  mention and a reply reach it either way.
- **Answers.** See [How the bot answers](#how-the-bot-answers). A message that
  arrives while the agent works waits and is read at the agent's next request, as in
  any chat; the answer of that turn covers it.
- **An agent with a folder.** When the agent has a
  [folder]({{% relref "/features/chats-and-agents/chats-agents" %}}#folders-of-an-agent), the chat of each sender
  reaches only that folder, its own chat folder, the shared folders and the skills,
  and the bot sends only files from those folders (see `send_files`). When the
  folder is deleted, unreadable or not chosen, the agent does not run: each message
  gets one reply, "This agent cannot work right now: its folder is missing.", and
  the server logs it. The binding stays on, so the agent answers again once you
  restore the folder or choose another one.

### Earlier messages of a thread

When the bot is mentioned in a thread, the people there often have talked for a while.
For a platform that can give them (Slack; not Telegram, because a bot cannot read the
history of a chat), LocalAI asks the add-on for the earlier messages of that thread when
the mention makes a new chat, and gives them to the agent with the first message:

- **Only that thread.** The bot reads the messages of the thread in which it is
  mentioned, nothing else: not the channel, not other threads, not files. For a file it
  sees the name and the count. A mention outside a thread reads nothing.
- **Once.** The messages are read when the chat of the thread starts. Later turns of the
  chat do not read them again. The agent gets them with the first message only; the
  chat keeps a copy for you to read (see
  [Reading a messaging chat in the web UI](#reading-a-messaging-chat-in-the-web-ui)),
  and that copy is never sent to the agent as a message.
- **Bounded.** At most 30 messages and 8 KiB of text. If there are more, the newest are
  kept, oldest first, one line `Label: text` each. The add-on can lower the number of
  messages. A thread of more than 1,000 messages is not read at all.
- **Labels.** What the person who mentioned the bot wrote earlier is labelled
  `The person asking (Name)`. The earlier answers of the bot are labelled
  `Assistant (you)`. Everyone else has their own name. So the agent can answer
  "what did I say first in this thread?". The person is matched by platform user id
  (or user name), never by display name. A display name that starts with one of the
  two fixed labels is shown as `Someone (...)`.
- **Untrusted.** The block starts with a heading that says these are the earlier
  messages of the thread, and that the text is untrusted content written by people:
  the agent must not follow instructions in it. Control characters and line breaks are
  removed and the characters `<` and `>` are replaced, so a message cannot close the
  block.
- **Logged.** Each read writes one line to the server log, for example
  `studio: thread history read binding=... kind=slack returned=12 lines=11 bytes=1530 truncated=false result=present`.
  `result=empty` means the thread held nothing to show. A kind without thread history
  logs `studio: thread history not read ... reason="kind serves no thread history"`.
  The line has counts only, never message text or names.
- **It never blocks the answer.** If the platform refuses, is slow or limits the calls,
  the agent answers without the context. The server logs the reason once.

There is no setting for this. The bot always reads the thread when it is mentioned in
one, on a platform whose add-on can give the earlier messages; on any other platform it
reads nothing.

### Reading a messaging chat in the web UI

A chat that a messaging connection started shows in your chat list under the connection
and opens like any chat. It also reads like the thread it came from. You see all of this
in your own chats only; it has the same access and the same lifetime as the chat.

- **Header.** The connection name and kind, whether the chat is a direct message or a
  channel (with its name when the add-on gives one), a link that opens the thread in the
  messaging app when the add-on can build one without an extra permission (Slack does,
  from the workspace address; other kinds may leave it out, and then no link shows), and
  the people who wrote in the chat.
- **Sender of each message.** The name the platform gave (else the handle, else
  "Unknown sender" with the platform id in small print), a badge and the time. The badge
  says how the server saw the sender when the message came in: *Owner* (paired with the
  bot), *Verified* (holds a role the owner gave rights to), *Listed* (on the allowlist
  themselves), *Channel member* (let in because their channel or group is listed) or
  *Anyone* (the connection accepts everyone). Messages of a chat made before this
  feature, and a message that joined words of several people while a turn ran, show no
  sender.
- **Earlier in the thread.** Where the bot was first addressed, a shut block "Earlier in
  the thread (N messages)" holds the lines the agent got with the first message, with the
  same wording and the same cut as in [Earlier messages of a thread](#earlier-messages-of-a-thread).
  It is stored once, when the chat starts, up to the 8 KiB limit. The server does not
  read the platform again to show it. A chat made before this feature has no block.
- **One answer.** An answer that the platform shows as a working message, an edit or
  several messages (a long answer is split) is one answer in the chat.
- **Notes of the server.** Small lines tell what the server did on its own: the tools it
  did not offer to the sender and why, a message it could not read, a message that was too
  long, files it did not accept, too many messages, a pairing that worked. Equal notes in a
  row show once with a count. A note is kept only in a chat that already exists.

The add-on may give two optional fields with an inbound message, `chatName` (the name of
the channel or group) and `link` (an https address of the message or its thread). The
server keeps the link only when it is https and short, and keeps nothing else of them.

## How the bot answers

Each binding has four settings, in the group **Where it posts and how it replies** on the
add-on page and in the API as `reply_mode`, `conversation` (with `window_minutes`),
`send_files` and `reply_language`. The group lists, in this order: the places the bot may post
(see [Where it posts]({{% relref "/features/chat-apps/access#where-it-posts" %}})), **Send my reminders and run results to**, **While the
agent works**, **Conversations** and **Send the files the agent makes**. The two settings that
people change least, the reply language and the file size limits, sit in a closed group named
**File sizes and reply language**. It opens by itself when one of them is set.

### While the agent works (`reply_mode`)

The bot shows "typing" as soon as it takes a message, and again every 4 seconds
until the turn ends. Slack has no typing for a bot: there a working message plays
that part.

| Mode | What the sender sees |
|---|---|
| `progress` (default) | One message, "Working…", that lists the tools as the agent starts them. The answer replaces it. |
| `stream` | A preview that shows the end of the answer as the model writes it, with a cursor (`▉`). Before the answer starts it shows "Working…" or the tool the agent uses; the reasoning of the model is not sent to the platform (use `reasoning` to show it). If the preview cannot be edited, it stays as it is until the answer comes, and the answer then replaces it; when it cannot replace it (the message is gone, or the platform refuses the edit), the answer is posted and the preview is deleted, so the conversation holds one message per answer. In a private Telegram chat the preview is a draft, and the answer follows it as a new message. Anywhere else it is one working message that the answer replaces. |
| `reasoning` | The `stream` preview, and while the model thinks, the end of its reasoning under the label "Thinking:", with a cursor. Once the answer starts, the preview shows the answer; when the agent calls a tool, it shows the tool, as in `stream`. The answer replaces the working message in the same way. It is never the default and you must turn it on for each binding. **Every sender of the binding can read the reasoning**, so use it only where that is acceptable. |
| `off` | Typing only, then the answer as a new message. On Slack the add-on posts its own "_Working…_" message, which the answer replaces. |

- The answer is converted from Markdown to the platform's markup: MarkdownV2 on
  Telegram (bold, italics, links, inline code and code blocks), Slack's markup on
  Slack. When Telegram cannot parse the markup of a message, that message goes as
  plain text, with each link's address after its label. Links show no preview card on
  Telegram. When the answer is longer than one message (4,096 characters on Telegram,
  4,000 on Slack), the working message holds the first part and the rest follows as
  new messages, without breaking a code block: a block cut in two opens again, with
  its language, in the next message.
- A preview (an edit or a draft) is plain text, since the answer is not whole yet.
- The previews of one binding are spaced out across all its conversations: at most
  one each 400 ms on Telegram, and one a second on Slack and on a platform whose
  add-on declares no interval. What changed in between is shown in the next preview.
  The answer itself is not held back.
- A Telegram draft that shows nothing new is sent again every 25 seconds, so Telegram
  keeps it. When Telegram refuses a draft, the turn goes on with a working message.
- When the platform asks to wait, the next previews wait that long. When an edit fails
  (the message was deleted, for example), the working message is left as it is and
  the answer goes as a new message.

### Conversations (`conversation`)

| Mode | Which chat a message belongs to |
|---|---|
| `sender` (default) | One long-lived chat for each person. A thread of the platform (a Slack thread, a Telegram forum topic) is one chat for everyone in it. |
| `chat` | One chat for each group, channel or private chat, shared by everyone who writes in it. A thread still has its own chat. |
| `window` | As `sender`, but a message that comes `window_minutes` (1 to 10,080) or more after the same person's previous message starts a new chat. A thread keeps its chat. |
| `thread` | Each message starts a new chat. A reply to an answer of the bot, or to the message that started a chat, goes on in that chat; on Slack, so does a thread under either one. |

#### Start a new chat with `/new`

A person can ask for a new chat at any time. The person sends `/new` as a message to the
bot. Slack takes a message that begins with a slash for its own commands, so there the
person sends `!new`. The host reads the command before any model turn: the agent never
sees it, and the logs record only that a new chat started, with the ids, never the text.

The command is the whole message. The case does not matter, and white space around it is
ignored. Telegram's `/new@BotName` works when it names the bot. A message such as
`/new please summarise` is plain text and goes to the agent. A message with a file is
never a command. Only a person who may talk to the bot can use it; any other person gets
nothing, as for any message.

| Mode | What `/new` does |
|---|---|
| `sender`, `window` | The next message of the person starts a new chat. The old chat stays in Studio, with all its messages. |
| `chat` | The next message of anyone in the group starts a new chat, for everyone. Only a sender that the owner paired with the bot may do this. Anyone else gets a short refusal. |
| `thread` | Nothing to reset: the bot says that every message already starts a new chat. |

In a thread of the platform (a Slack thread, a Telegram forum topic) the command resets
only the chat of that thread. The people in a thread share its chat, so in a group a
paired sender must send it. In a private chat the person can always send it.

If the bot is still answering in that chat, it asks the person to send the command again
when it is done, so a running turn never loses its chat. The old chat is not deleted, and
memory and files stay as they are. A trigger whose delivery target is the origin chat
still points at the old chat. The command needs no support from the add-on: it works for
every connector, and with several frontends, because the link from a person to a chat is
in the shared store.

Some add-ons add settings of their own to a binding (for example Slack's **Threads the
bot follows**). They appear under **Where it posts and how it replies**, and in the API as `options`, a
map from the option id to a value. The add-on declares the allowed values, and an
unknown option or value is a 400. An update without `options` keeps the stored values.

### File limits

A file that a person sends to the bot, and a file that the bot sends, has a size
limit. The limit is the smallest of four layers, and every sentence about it names the
number and the layer that set it, for example "That file is too large (3 MiB): I can
read files of up to 1 MiB here (limit of this add-on version)." The layers are the
limit of this add-on version (the cap of the inline path, 1 MiB), the limit the add-on
declares for its platform, the limit of the platform (the host ceiling that the
administrator sets) and the limit that you set for the bot. The agent reads the same
number in its instructions, so it does not promise a file that cannot go.

An add-on that streams files (Slack, Discord and Telegram do) has no inline cap: a file moves in
chunks, by a short-lived handle, so a 6 MB PDF goes both ways. An add-on that does not
stream keeps the inline cap of 1 MiB, whatever you set, and the sentence says "limit of
this add-on version".

What the platforms allow, from their own documentation:

| Platform | Bot reads a file | Bot sends a file |
|---|---|---|
| Slack | No separate bot limit is documented. The add-on declares the 1 GB per file that a member can add ([Add files to Slack](https://slack.com/help/articles/201330736-Add-files-to-Slack)); the [upload API](https://docs.slack.dev/reference/methods/files.getUploadURLExternal) states none either. | 1 GB per file, by the external upload flow. |
| Discord | 10 MB: the add-on declares a limit that fits every server level; Discord states the limit per server and per person, and a bot may be allowed less. | 10 MB, as one attachment of a message. |
| Telegram | 20 MB with `getFile` on the public Bot API ([Bots FAQ](https://core.telegram.org/bots/faq)). | 50 MB for a document, 10 MB for a photo; a larger picture goes as a document ([Bots FAQ](https://core.telegram.org/bots/faq)). |

A local Bot API server lifts the Telegram limits (no download limit, 2000 MB upload,
[Bot API](https://core.telegram.org/bots/api#using-a-local-bot-api-server)), but the
Telegram add-on has no setting for another server address, so it declares the limits of
the public one.

The server holds only one chunk of a file in memory, on both sides. Each transfer writes
one audit line (direction, binding, size, result, layer, time), with no name and no
content. The files of one message may add up to three times the limit.

- **Per bot.** The group **File sizes and reply language** of a binding has one field for the largest file the
  bot reads and one for the largest it sends, in MiB. Leave a field empty to use the
  limit of the add-on. A value can only lower that limit. The API takes
  `file_receive_max_bytes` and `file_send_max_bytes` (0 clears), and a value above
  `files.<direction>.allowed_bytes` is a `400`. A binding and a kind show the limit in
  effect as `files.receive` and `files.send` with `effective_bytes`, `layer`
  (`owner`, `addon`, `host` or `inline`), `declared_bytes` and `allowed_bytes`.
- **For the server.** The add-on owner sets the host ceiling with the policy key
  `connector_files_ceiling_bytes` (1 KiB to 1 TiB; 0 or unset means 100 MB). It is under
  **Settings** next to the add-on upgrade setting, as "Largest file a connector may
  move". No bot goes above it.

### Files the agent makes (`send_files`)

On by default. After the answer, the files that the turn wrote or changed go to the
sender: the files listed in the chat's **Files** with the turn's number, not the files
the sender attached.

- At most 5 files a turn, each within the [file limit](#file-limits). A larger file
  stays in the chat's files, and the sender gets a sentence that names it and the limit.
- Only documents, data, pictures, sound, video and `.zip` archives go: `.txt`, `.md`,
  `.log`, `.csv`, `.tsv`, `.json`, `.yaml`, `.yml`, `.xml`, `.pdf`, `.docx`, `.xlsx`,
  `.pptx`, `.odt`, `.ods`, `.odp`, `.png`, `.jpg`, `.jpeg`, `.gif`, `.webp`, `.mp3`,
  `.wav`, `.ogg`, `.m4a`, `.mp4` and `.webm`. Programs, scripts and web pages do not;
  the sender gets a sentence that names the file.
- The name loses any folder, control characters and quotes, and is cut to 128 bytes.
- On Telegram a PNG, JPEG or WebP picture goes as a photo and anything else as a
  document. On Slack the file is uploaded into the conversation (the app needs
  `files:write`).
- When the files of the chats are off (see [Files]({{% relref "/features/chats-and-agents/chats" %}})), nothing is sent.
- The agent can also give the sender a file that already exists, such as one from an
  earlier turn or from a folder it was given. It calls the `attach_file` tool with the
  path, and the file goes after the answer, with the files the turn made. The same limits
  apply, and the 5 files of a turn count both kinds. The tool is only offered in a chat
  that a connector started, when the binding sends files. It reads with the access of the
  chat, so an agent limited to a folder can attach only files inside it. When a file cannot
  be attached (too large, a type that is not sent, not found), the tool says why, so the
  agent can tell the sender. When the platform refuses a file, the sender gets "I could not
  send" with the name of the file and the reason that the add-on reports, if it gives one.
- Reading a binary file with `read_file` fails with a short hint: the skill for the file
  type if the chat has one, or a script run with the commands tool.

An update without `send_files` keeps the stored value.

### Reply language (`reply_language`)

The bot writes some sentences itself, without the model: the confirmation of a new chat,
the questions and answers about proposals and changes, the refusals (a file that is too
large, a message that is too long, too many messages, a sender who may not do this), the
notes about files, the pairing replies, and the "Working…" message of the progress mode.
These come in the language of the person. The answers of the model are not affected: the
model answers in the language of the conversation, as it does without this setting.

| Value | Language |
|---|---|
| `auto` (default) | The interface language of the owner when the server knows it, else the language of the sender, see below. |
| `en` | English |
| `it` | Italian |
| `de` | German |
| `es` | Spanish |
| `fr` | French |
| `pt-BR` | Portuguese (Brazil) |

With `auto`, the bot first uses the interface language of the owner, when the catalog has it.
The page of the add-on sends that language with each save (`ui_language`, a tag such as `it`
or `pt-BR`) and the binding keeps it, so every frontend of a cluster knows it. A save that
sends none keeps the stored one, and a value that is not a language tag is ignored. When
the owner's language is not known, or the six languages above lack it (Korean, for
example), LocalAI reads each message of the sender and scores it against the common
words of the six languages. It needs no model and sends the text nowhere. A message that
does not show a language clearly (`ok`, `yes`, `thanks`, a number, an emoji) does not
change anything. The last clear language of a sender is kept in memory for 24 hours, and
a later short reply keeps it; a full sentence in another language moves it. The reading
decides only when the owner's language is not known. A trigger
that writes to a chat on its own uses the language last seen in that chat. When nothing
is known, the host uses English. A fixed language ignores what the sender writes.

The words that answer a proposal are read in all six languages whatever the setting:
`yes`, `sì`, `ja`, `sí`, `oui`, `sim` confirm; `no`, `annulla`, `nein`, `cancelar`,
`annuler`, `não` drop; and each language has a phrase for undo (`undo`, `annulla
modifica`, `rückgängig`, `deshacer`, `annuler la modification`, `desfazer`). A reply is
always read by the host before any model turn, so it never reaches the model.

The value is stored with the binding. An update without `reply_language` keeps the stored
value; an unknown value is a 400. Instructions to the model (tool descriptions, the note
about held back tools, the thread block) stay in English.

### Limit the files a person can send (`inbound_media`)

Empty by default: a sender who may send files (`files.upload`) can send every kind that
the platform hands over. A list takes only those kinds:

| Kind | What it is |
|---|---|
| `photo` | A picture sent as a photo |
| `voice` | A voice message recorded in the app; it is transcribed |
| `audio` | An audio file; it is transcribed |
| `video_note` | A short round video message |
| `document` | Any file sent as a file: a PDF, an uncompressed picture, a text file |

```
curl -X PUT .../api/chats/connectors/<id> -H 'Content-Type: application/json' -d '{"name": "Support bot", "agent_id": "<id>", "enabled": true, "allow": ["12345678"], "caps": ["files.upload"], "inbound_media": ["voice"]}'
```


With authentication on, add `-H "Authorization: Bearer $API_KEY"` to each request.

- The check is made by the server, before the file is read: a file of a kind that
  the binding does not take is never downloaded, never stored and never reaches the
  model. The sender gets one sentence that names the kind, for example "This bot
  does not accept photos.", and the text or caption of the message still goes to the
  agent. A message with nothing else gets only the sentence and starts no turn.
- The add-on tells the server the kind of each file. Telegram names every kind. An
  add-on that names none (Slack and Discord, for now) hands over files of an unknown kind, which
  a binding with a list never reads.
- The list narrows `files.upload`, it does not replace it: a sender still needs the
  [capability]({{% relref "/features/chat-apps/access#capabilities-roles-and-pairing" %}}) `files.upload` to send any file, voice messages
  included; the example above enables both.
- An unknown or repeated kind is a `400`. An update without `inbound_media` keeps the
  stored list; `[]` takes every kind again. The binding returns the list (`[]` when
  it takes every kind).
