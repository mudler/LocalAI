+++
disableToc = false
title = "Chats: connectors (messaging add-ons and MCP servers)"
weight = 23
url = "/features/chats-connectors/"
+++

The Connectors page of the [chats](/features/chats/) (`/app/chats/connectors`, under **Agents** in the sidebar) lists the messaging bindings you made, and the MCP servers have a page of their own (**MCP connectors** in the sidebar, `/app/mcp-connectors`):

- A **messaging connector** binds a bot of a platform to one of your agents, so a person can talk to it from that platform. The platform comes from a **connector add-on**: the [Telegram add-on](#telegram) or the [Slack add-on](#slack) (the [GitHub add-on](#github) holds a token for your agents instead of carrying messages). This is the first half of this page.
- An **MCP server** gives your chats and agents more tools. See [MCP servers](#mcp-servers).

## Connector add-ons

Core keeps what is the same for every platform: the bindings, the sealed bot token,
the allowlist, the sender log, one chat for each sender (or for each thread) and the
turn itself. The
platform's API is spoken by an add-on that declares the `connector` capability (see
[Connector add-ons]({{% relref "features/addons" %}}#connector-add-ons)). Install the add-on
from the add-on gallery, and its page adds and edits the bindings of its platform.

The Connectors page groups your bindings by add-on and links each group to the
add-on's own page. With no connector add-on installed, the page says so and links
to the gallery. `GET /api/chats/connectors/kinds` lists the platforms that a running
add-on serves.

A binding names the add-on that serves it. A binding made before connectors became
add-ons names none, and belongs to the add-on of its kind: a Telegram binding keeps
working as soon as the Telegram add-on is installed. Until then the binding is kept,
switched as it was, and shown as not running with the reason "install the Telegram
add-on"; nothing polls and nothing is lost.

### How messages arrive: pull and push

Each kind in the add-on manifest declares `delivery`:

- **`pull`** (Slack and Telegram). The host polls the add-on. One process in the
  cluster, the leader, does the polling. It stores the offset with the binding. When
  the leader stops, a new leader continues from that offset.
- **`push`**. The add-on calls the host operation `connector.inbound` for each
  message. The turn then runs in the process that hosts the add-on. A push add-on
  therefore needs a `combined` owner or a distributed owner (see
  [The owner in distributed mode]({{% relref "features/addons" %}}#the-owner-in-distributed-mode)).

### Multiple frontends and readiness

In a multi-frontend deployment, connector discovery and calls go to the frontend
that owns the add-on runtime over the private, peer-authenticated add-on carrier.
You can create and test bindings from another frontend; it does not need its own
Slack or Telegram runtime. Poll leadership is unchanged: the polling frontend
and notification sender can reach the runtime owner without owning it themselves.
Connector access still requires **Connectors**, not the separate **Add-ons**
permission. Existing agent permissions and sender allowlists still apply.

A healthy owner with no running connector kinds returns an empty list. An
unreachable, unready or fenced owner returns an error instead: an empty list is
not an outage signal. Check that the connector add-on is running and that the
frontends can reach the configured private carrier before trying discovery again.

If discovery fails or the platform explicitly refuses a token, creating a binding
fails without saving it. Once discovery succeeds, a temporary outage during token
verification retains the existing permissive save behavior; use **Test** when the
platform is available again. An authorization denial or explicit token refusal
during replacement leaves the stored binding unchanged. Binding calls use the
owner's stored token. LocalAI does not replay a forwarded send automatically after
a transport failure, since the platform may already have received it.

Telegram's legacy binding resolution remains compatible with existing bindings.
It is not a fallback for discovering other connector kinds or for owner outages.

## Telegram

The Telegram add-on binds a bot to one of your [saved agents](/features/chats-agents/).
A person who messages the bot talks to that agent, in a conversation of their
own, and the answer comes back in Telegram. The conversation is a
[chat](/features/chats/) of yours: it uses your quota and your model allowlist,
and you can open it in LocalAI.

### Set up a binding

1. Install the **Telegram** add-on from the add-on gallery.
2. Make a bot with `@BotFather` in Telegram and copy its token.
3. Make an agent that names a model. A connector has no model of its own, so
   an agent without a model cannot be bound.
4. On the Connectors page open **Telegram**, and make a binding: a name, the token,
   the agent, and who may use it.
5. Save. LocalAI asks Telegram, through the add-on, which bot the token belongs to.
   A token that Telegram refuses is not saved: the request is a `400` and the page
   shows the reason. **Test** asks again at any time.
6. Write to the bot from an account on the allowlist.

```
curl -X POST .../api/chats/connectors -d '{"kind": "telegram", "name": "Support bot", "token": "123:abc", "agent_id": "<id>", "enabled": true, "allow": ["12345678"], "reply_mode": "progress", "conversation": "sender", "send_files": true}'
curl -X POST .../api/chats/connectors/<id>/test
curl .../api/chats/connectors/<id>/senders
curl -X POST .../api/chats/connectors/<id>/senders/<sender>/allow   # add a sender of the log to the allowlist
```

### What a person can send

- **Text.** Up to 8,000 characters.
- **Photos.** The add-on downloads the largest size of the photo that is within the
  [file limit](#file-limits) and attaches it to the chat. The caption is the text of the message. An album is
  one message, with all its photos (at most 8; more start a second message, which
  the agent reads after the first). The add-on waits about a second for the rest of
  an album; an album that takes longer to arrive can become two messages.
- **Voice messages, audio files and video notes.** A voice message or an audio file
  is transcribed (see [Voice messages](#voice-messages)). A video note is attached as
  a video.
- **Files.** A picture (JPEG, PNG, WebP), a PDF or a text file sent as a file is
  attached. The add-on checks the bytes of each file, not only its name, and names the
  file itself, for example `photo-AQADxyz.jpg`, so a name that the sender chose never
  reaches your files. Another type gets a sentence that the file could not be read.
- **Size.** The [file limit](#file-limits) of the binding. A larger file gets a sentence
  that names the limit and which limit it is, and the caption still reaches the agent.

### Voice messages

A voice message is transcribed with the **Chat transcription model** of the
instance settings, the same model that transcribes a voice note attached on the chat
page. Without one set, the first installed model that can transcribe is used. There
is no setting per binding: the model is the instance's, and the request runs as you,
so your model allowlist and quota apply.

The transcript is the text of the turn, under `[Voice message, transcribed]`, so the
agent knows that the sender spoke. A caption follows it. The recording itself is not
kept in the chat's files.

With no transcription model installed, or when the transcription fails, the sender
gets one sentence (for example "I cannot listen to voice messages here: no
speech-to-text model is set up") and no turn starts.

`GET`, `PUT` and `DELETE /api/chats/connectors/{id}` read, replace and delete a
binding. `POST .../enable` and `.../disable` switch it. You can have 10 bindings.
A binding says `active: false` and gives a `reason` while its add-on does not run.

## GitHub

The GitHub add-on lets your [saved agents](/features/chats-agents/) read repositories: search code, read files and folders, and read issues, pull requests and their diffs. It reads only. It cannot write, merge or delete anything on GitHub.

1. Install the **GitHub** add-on from the add-on gallery.
2. On GitHub, make a **fine-grained personal access token** for only the repositories you want to share. Give it read access to Contents, Issues and Pull requests. Classic tokens are not accepted.
3. On the add-on page, choose **New GitHub connection**. Pick an agent, paste the token and list the repositories, one `owner/name` per line (at most 50). Press **Test** to see whose token it is and which listed repositories it reaches.

The list is checked by LocalAI before the add-on is called, so an agent is refused any repository that is not on it, even if the token could reach it. The token is kept sealed and is never shown again, and no tool result contains it. With several connections, a call uses the first one that lists the repository.

What GitHub returns (issue text, comments, file content) is written by other people. Every result is marked as untrusted data for the model. Long files are cut at 96 KiB and long patches at 64 KiB; the result says what was cut and how to ask for the rest. When GitHub limits the rate of calls, the agent is told how long to wait.

This version does not listen for mentions and has no write tools.

## Slack

The Slack add-on binds a Slack app to one of your [saved agents](/features/chats-agents/).
People talk to the agent in a direct message with the app, or by mentioning the app in
a channel. The add-on uses Socket Mode, so LocalAI needs no public address.

### Set up a binding

1. Install the **Slack** add-on from the add-on gallery.
2. At `api.slack.com/apps` make an app **From an app manifest**. The add-on page shows
   the manifest to paste under **How to make the Slack app**: it turns on Socket Mode,
   subscribes to `app_mention` and the `message.*` events, and asks for the bot scopes
   `app_mentions:read`, `channels:history`, `groups:history`, `im:history`,
   `mpim:history`, `chat:write`, `files:read`, `files:write` and `users:read`. An app
   made before `files:write` was in the manifest cannot send files. Add the scope in
   the app settings, then reinstall the app to the workspace: Slack applies new scopes
   only after a reinstall.
3. Install the app to your workspace and copy the **bot token** (`xoxb-`).
4. Under **Basic Information**, **App-Level Tokens**, make a token with the scope
   `connections:write` and copy it (`xapp-`).
5. On the Slack add-on page make a binding: a name, both tokens, the agent, and who
   may use it. When you save, LocalAI checks both tokens with Slack and shows which
   app they belong to, or why Slack refused them.
6. Write to the app in a direct message, or invite it to a channel (`/invite @LocalAI`)
   and mention it.

Over the API the two tokens are one `token`, separated by a space:

```
curl -X POST .../api/chats/connectors -d '{"kind": "slack", "name": "Team app", "token": "xoxb-... xapp-...", "agent_id": "<id>", "enabled": true, "allow": ["U012AB3CD", "C0123456"]}'
```

### How Slack messages map to chats

- **A direct message** with the app is a conversation with its sender, like a Telegram
  chat. A reply in a thread of the direct message is a conversation of that thread.
- **A mention** of the app in a channel is answered in the thread of that message. The
  thread is one chat, shared by everyone who writes in it. Which threads the app then
  follows, so that a later reply reaches the agent without a new mention, is the
  setting **Threads the bot follows** (`followThreads` in `options`):
  - `own` (the default): the app follows the threads it started, that is, a mention
    outside a thread. A mention inside a thread that other people started is answered,
    and the thread is not followed, so the app does not answer what people write to
    each other.
  - `any`: the app also follows a thread that other people started, once it is
    mentioned in it.
  - `never`: every message needs a mention.

  A followed thread stays followed for 24 hours after its last message. The add-on keeps
  the list in its data directory, so a restart of LocalAI or a new Socket Mode link does
  not make the app forget its threads. The file holds channel IDs and message
  timestamps only, never a token. The app does not call Slack for a message to find
  out whether it follows the thread.
- **Earlier messages of the thread.** When a mention starts a new chat in a thread, the
  app reads the earlier messages of that thread once (see
  [Earlier messages of a thread](#earlier-messages-of-a-thread)). This needs the scopes
  `channels:history`, `groups:history`, `im:history` and `mpim:history`, which the
  manifest of the add-on page asks for. An app made with fewer scopes answers without
  that context; add the scopes and reinstall the app.
- **Other channel messages** are not read.
- **Answers** are converted from Markdown to Slack's `mrkdwn` (bold, italics, links,
  lists, code blocks) and split at 4,000 characters, the limit Slack asks for. A turn
  shows a "Working…" message at once, and the answer replaces it in place (see
  [How the bot answers](#how-the-bot-answers)).
- **Files.** A file attached to a message (within the [file limit](#file-limits), at most 8) is downloaded with
  the bot token and attached to the chat, as an upload on the chat page would be: a
  document is read, an image goes to a model that can see. A larger file gets a
  sentence that it is too large. An audio file is transcribed like a Telegram voice
  message (see [Voice messages](#voice-messages)). The add-on downloads only from
  Slack's file host.
- **Waits.** When Slack answers `429` with `Retry-After`, the add-on waits that long once
  and tries again.

### Who may use the app

The binding has two lists, kept as one allowlist:

- **Allowed people**: Slack user IDs (`U...`, `W...`) or `@names`. A listed person may
  use the app in a direct message and by mentioning it anywhere.
- **Allowed channels**: channel IDs (`C...`, `G...`). Everyone in a listed channel may
  mention the app there. A direct message still needs the person on the list.

Copy an ID in Slack from the profile of a person or the details of a channel, or read
it from the sender log after the person has written to the app. "Anyone in the
workspace" (`accept_any`) needs the same confirmation as for Telegram.

### Notifications in Slack

Set your Slack user ID (`U...`) as the notification chat to receive your run
notifications as direct messages from the app, or a channel ID to post them there.

### The tokens of a Slack app

Both tokens are sealed together, like the token of a Telegram bot, and no API returns
them. To replace them, type both on the page (or send both in `token`). The add-on
holds them in memory while it keeps the Socket Mode link of the binding open, writes
them nowhere and calls only `slack.com` and `files.slack.com`. A link that is not
polled for two minutes (the binding was switched off or deleted) is closed.

A binding is switched off, with a notification, when Slack refuses a token
(`invalid_auth`, `token_revoked` and similar) or turns Socket Mode off for the app.
Socket Mode spreads the events of an app over all its open links, so use one
connection per Slack app. If an old external agent and the add-on use the same Slack
app, each gets part of the events and some messages are lost or answered twice at
random. Stop the old bot or make a second app. Bind one app to
one LocalAI instance only. A cluster of LocalAI frontends counts as one instance: only
the leader opens links. Two separate installations that bind the same app each get
part of the events, and LocalAI cannot detect it (Telegram's `conflict` answer has no
Slack counterpart).

The Events API over HTTP is not supported: it needs a public address, and Socket Mode
gives the same events without one.

## What a message gets

- **The agent's tools only.** A message gets exactly the tools the agent grants,
  intersected with what you may use now. It never gets the tool set of a chat, and
  a message cannot change the agent. A run that a trigger started never gets
  `propose_trigger`. A connector chat gets `propose_trigger` and `list_triggers`
  only while the binding holds `triggers.propose` (see
  [Triggers from a connector chat](#triggers-from-a-connector-chat)).
- **Your account.** The chat acts as you, so the feature gate, the model
  allowlist and your quota apply. If the model refuses the request, the sender
  gets one fixed sentence, for example that your quota is used up.
- **One chat for each sender, by default.** Each person who writes to the bot has a
  chat of their own with the agent. A Slack thread is one chat for everyone in it (see
  [Slack](#slack)). They cannot see another person's chat. The conversation mode of
  the binding can change this (see [How the bot answers](#how-the-bot-answers)). If
  you change the agent of the binding, the next message of each sender starts a new
  chat with the new agent.
- **Text, files and voice messages.** The add-on hands the files of a message to the
  chat: on Telegram photos, voice messages and some other files (see
  [What a person can send](#what-a-person-can-send)), on Slack any file (see
  [Slack](#slack)). A file is attached to the chat as if you had attached it on the
  chat page: it is in **Files**, and an image goes to a model that can see. A voice
  message (any audio file) is transcribed, and the transcript is what the agent reads
  (see [Voice messages](#voice-messages)). A file over the [file limit](#file-limits) gets a
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
  [folder](/features/chats-agents/#folders-of-an-agent), the chat of each sender
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
  chat do not read them again, and they are not copied into the transcript.
- **Bounded.** At most 30 messages and 8 KiB of text. If there are more, the newest are
  kept, oldest first, one line `Name: text` each. The add-on can lower the number of
  messages. A thread of more than 1,000 messages is not read at all.
- **Untrusted.** The agent gets the lines in a block that says they are messages from
  other people and not instructions. Control characters and line breaks are removed and
  the characters `<` and `>` are replaced, so a message cannot close the block. Messages
  of the bot itself are included, so the agent sees its earlier answers.
- **It never blocks the answer.** If the platform refuses, is slow or limits the calls,
  the agent answers without the context. The server logs the reason once.

There is no setting for this. The bot always reads the thread when it is mentioned in
one, on a platform whose add-on can give the earlier messages; on any other platform it
reads nothing.

## How the bot answers

Each binding has three settings, under **How it answers** on the add-on page and in
the API as `reply_mode`, `conversation` (with `window_minutes`) and `send_files`.

### While the agent works (`reply_mode`)

The bot shows "typing" as soon as it takes a message, and again every 4 seconds
until the turn ends. Slack has no typing for a bot: there a working message plays
that part.

| Mode | What the sender sees |
|---|---|
| `progress` (default) | One message, "Working…", that lists the tools as the agent starts them. The answer replaces it. |
| `stream` | A preview that shows the end of the answer as the model writes it, with a cursor (`▉`). Before the answer starts it shows the reasoning as it grows, or the tool the agent uses. In a private Telegram chat the preview is a draft, and the answer follows it as a new message. Anywhere else it is one working message that the answer replaces. |
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

Some add-ons add settings of their own to a binding (for example Slack's **Threads the
bot follows**). They appear under **How it answers**, and in the API as `options`, a
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

The bytes of a file still travel inline for now. Until an add-on supports streaming, a
file larger than the inline cap does not move, whatever you set. The **Files** section
of the binding page says so: "Files up to 1 MiB are sent now; larger files need the
add-on to support streaming."

- **Per bot.** The **Files** section of a binding has one field for the largest file the
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
- When the files of the chats are off (see [Files](/features/chats/)), nothing is sent.
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

## Who may use the bot

The allowlist decides. It is empty when you make a binding, so the bot accepts
nobody. List chat IDs, user IDs or `@usernames`: numbers for Telegram, IDs such as
`U012AB3CD` and `C0123456` for Slack. A listed group or channel accepts everyone who
addresses the bot in it.

A message from anyone else is ignored: it is not answered, and the sender goes
in the sender log with a count. Open **Senders** on the binding to see who tried,
and use **Allow** to add a person to the allowlist.

"Anyone who can message the bot" (`accept_any`) lets everyone use the agent. The
server asks you to confirm it, because anyone who can find the bot can then use
the agent's tools as you: the request that turns it on, a create with
`accept_any` or an update that changes it from off to on, must also carry
`"confirm_accept_any": "accept any message"`, or it is a `400`. The page does
this with a confirmation. A binding that already has it keeps it on later edits,
and turning it off needs no confirmation. Use it for a public bot whose agent
grants nothing you would mind sharing.

### Capabilities, roles and pairing

Three things sit on top of the allowlist. They work the same for every connector
add-on.

- **Capabilities** (`caps`). The ceiling you enable for a binding, from a closed list
  (`proposals.confirm`, `memory.write`, `files.upload`, `triggers.propose`,
  `triggers.run`, `talkback.channels`, and the sensitive `talkback.new_channel`,
  `profile.write`, `skills.write`, `pages.write`, `dashboard.write`). A binding with
  none enabled behaves as before: the allowlist is the only gate.
- **Roles and grants** (`grants`). An add-on can declare roles, such as the admins of
  a workspace, and report the roles of each sender. You map a role to capabilities
  with `grants: [{"role", "caps"}]`. A grant is within `caps`, within what the kind
  lists as `permissions`, and never a sensitive capability. The kind list
  (`GET /api/chats/connectors/kinds`) shows each kind's `roles` and `permissions`.
- **Pairing.** A sender you pair may use the sensitive capabilities. Ask for a code
  with `POST /api/chats/connectors/<id>/pairing`; send it as a direct message to the
  bot from an account on the allowlist. The code looks like `LA-` and eight characters,
  works once and lasts ten minutes. The host checks it before any model turn, so
  the code never reaches the agent or the sender log. A binding holds at most three
  paired senders, and a pairing ends after 90 days. End one earlier with
  `DELETE /api/chats/connectors/<id>/paired/<sender>`. A sender who sends five wrong
  codes in an hour is ignored for an hour, and every attempt is logged without the
  code.

Pairing needs only a stable sender ID, so an add-on without roles supports it too.

The host stores and checks these values and shows them to the add-on pages. The
features that a capability switches on or off (triggers from chat, talk-back, and so
on) read them as they land.

## Triggers from a connector chat

When the binding holds the capability `triggers.propose`, the agent can propose a
trigger from a chat in a messaging app. The agent gets `propose_trigger` and
`list_triggers` (the list shows only the triggers of that agent). Changing or deleting a
trigger stays in Studio. Nothing runs before the owner confirms.

- **The proposal is shown in the chat.** LocalAI writes the name, the schedule, what the
  run will do, where the results go and a four-character code in the chat and thread
  where the person wrote. The model does not write it.
- **Confirm or cancel by text.** A sender writes `confirm CODE` or `cancel CODE`, as the
  whole message, in the same chat and thread. LocalAI handles it before any model turn.
  It needs the capability `proposals.confirm` for that sender (a paired sender, or a role
  with the grant). A code is used once, and a proposal lapses after 24 hours. Studio
  always works as well.
- **Limits.** A binding can make 3 proposals an hour, and an owner can have 10 waiting.
- **Where a run reports.** The proposal sets `delivery`: `inbox` (the default) or
  `origin`, the chat and thread of the proposal, and a mode: `always`, `on_change` or
  `on_problem`. LocalAI writes the last message of the run, cut to 4,000 characters,
  with mentions that notify everyone broken. A final message of exactly `[SILENT]`
  writes nothing. After three failed runs in a row the chat gets one alert, and not
  more than one every 6 hours. If the binding is off or the capability is off when a run
  ends, the Inbox gets the notice. A binding sends 20 of these messages a minute; more
  wait in a queue and go in order.
- **Pause.** While `triggers.run` is off for the binding, its triggers do not run. The
  records stay and are not replayed when the capability comes back.

## Notifications in Telegram

Set a notification chat ID on a binding to receive your run notifications there
too (a trigger that finished or failed, for example). The title and the text are
sent, not the link. Find your chat ID by writing to the bot and reading the sender
log.

## The bot token

The token is a secret. LocalAI seals it with AES-256-GCM and ties it to its
binding. No API returns it: a binding only says `has_token`. To change it, send a
new one with an update; an update without a token keeps the old one. The key is
`connectors.key` (mode `0600`) under the data path, or a row of the database that
every frontend shares. LocalAI refuses a key file that other users can read.

LocalAI passes the token to the add-on with each call and the add-on keeps none: it
holds no token on disk and in memory only for the call. The add-on only calls
`https://api.telegram.org`. LocalAI reads updates by long polling through the add-on,
so it needs no public address.

## When a binding switches itself off

A binding is switched off, and you get a notification, when Telegram refuses the
token, when another process keeps reading the updates of the same bot (Telegram
allows one reader; a new leader in a cluster waits and retries for about a minute
before it gives up), when the agent is deleted or has no model, or when the owner no longer
exists. Fix the cause and switch it on.

## Storage and clusters

Bindings, the read position in Telegram's updates, the sender log and the chat of
each sender are stored with your chats: in the database, in JSON files under the
data path, or in memory when there is no data path (then nothing survives a
restart). The JSON store is for one process.

| State | Mode | How |
|---|---|---|
| Bindings, offsets, sender log, sender chats, the key | Shared | Rows in the database. |
| Which frontend reads the bot | Single runner | The frontend that holds the advisory lock (`KeyChatsConnectors`). When it stops, another takes the lock and continues from the stored offset. Add-ons run on the process that owns the add-ons, and the loops run only where the add-on answers. |
| Where the next message is read from | Shared | The offset is stored with the binding. Core passes it to the add-on's poll call and stores the offset that the answer names. |
| The reply being sent for a turn | Per instance | The frontend that took the message sends the answer, edits its working message or sends its draft, and sends its files. If it dies, the answer is lost; the message is not repeated. |
| The preview budget of a binding | Per instance | The time of the next edit or draft is kept in memory by the frontend that sends the replies. Replies of one binding run on the leader, so one budget covers them. |
| Draft IDs | Per instance | A counter in the process that sends the replies gives each turn a new draft ID. Two frontends could give the same ID, but a draft only lives in one chat for one turn. |
| The chats of the answers (thread mode) | Shared | Each answer's message IDs are stored with the chat of each sender, so a reply finds its chat on any frontend. |
| Slack Socket Mode links and their queues | Per instance | The Slack add-on process keeps one link per polled binding, acknowledges each event at once and queues it until the next poll. An event that is queued when the process stops is lost. The followed threads and the "Working…" messages are kept there too. |
| Files marked by `attach_file` | Per instance | The marks live in the process that runs the turn and are sent by the process that sends the replies. A turn that ends on another frontend than the one that sends the answer loses its marks; the agent then sees no error, and the person asks again. |

### For add-on authors: sending files

A connector add-on does not choose which files reach a person. The host does, and it
sends each one with the typed `send_file` op: the file is in the request (`name`, `mime`
and at most 1 MiB of `data`) with the chat and the thread of the message it answers. The
host chooses the files in two ways:

- The files that the turn made or changed go on their own, after the answer.
- The files the agent picks with its `attach_file` tool go with them. The host checks the
  path against the chat's access when the tool is called and again when it sends, drops
  repeated paths, and counts both kinds in the limit of 5 files a turn.

An add-on that implements `send_file` gets both, with no code of its own. It must not read
chat files or add tools for this. When the platform refuses a file, return a
`ConnectorError` with a short, plain `message` (for example the name of a missing
permission): the host puts it after "I could not send <name>" in the message to the person.
Keep the message free of tokens and internal addresses.

### Other connectors

Telegram, Slack and MCP servers are built. Discord, IRC, Matrix, email, GitHub and Twitter
connectors of the old agents are not rebuilt; the release that removes the old
agents lists them as dropped.

## MCP servers

An MCP connector is an MCP server that you define. It is stored on the server,
as one of your records, so every browser and session of yours sees the same list.
(The old chat page kept such servers in the browser and reached them through a
proxy; see [Import from this browser](#import-from-this-browser).)

A connector is yours alone. Another user never sees, edits, tests or uses it.
You need the `mcp` feature, and `LOCALAI_DISABLE_MCP` turns it off for the
whole instance. Both are checked each time a chat uses a connector, not only
when you save it.

### Add one

On the **MCP connectors** page (`/app/mcp-connectors`) choose **Add connector**.
A side sheet opens:

1. Administrators choose the kind first: **Remote server** (an address over
   http or https) or **Local command** (see the table below). Other users add
   remote servers only.
2. Give it a name (unique among your connectors) and an address. The sheet
   checks the address as you type: it must start with `http://` or `https://`
   and must not hold a user name or password.
3. Under **Sign-in**, add a bearer token or other headers if the server asks for
   a key. Secret fields are masked; the eye button shows what you typed. They
   are stored encrypted on the server and never shown again.
4. **Save and test** saves the connector switched off, then connects with what
   was saved and lists the tools, each with the description its server gives.
   If the test works, **Turn on** switches it on; **Keep it off** closes the
   sheet. If it fails, the sheet gives the reason (a refused sign-in says to
   check the token or the headers) and offers **Test again**.

Escape, Cancel and the close button ask before they drop typed changes.

```
curl -X POST .../api/chats/mcp-connectors -d '{"name": "Docs", "transport": "http", "url": "https://mcp.example/mcp", "token": "...", "headers": {"X-Team": "blue"}, "enabled": false}'
curl -X POST .../api/chats/mcp-connectors/<id>/test
# {"ok": true, "tools": ["search", "fetch"], "tool_details": [{"name": "search", "description": "Search the docs."}, {"name": "fetch"}]}
curl -X POST .../api/chats/mcp-connectors/<id>/enable
```

### The list and the details

Each connector is a row with its state, where it points (the host only, never
the path or the query of the address), its tools, what is saved for it and when
it was last tested. The state is one of:

| State | Meaning |
|---|---|
| **Connected** | The last test connected. |
| **Needs sign-in** | The last test was refused for its credential (for example a `401`). **Replace token** opens the sheet on the token field. |
| **Can't connect** | The last test failed; the row gives the reason. |
| **Not tested** | It is on, but it was not tested in this browser since it last changed. |
| **Off** | It is switched off. Chats and agents do not use it. |

The last test of each connector is kept in this browser only (in
`localStorage`, without the address or any secret), so the page shows the last
known state with its time after a reload. A change of the address or of what is
saved makes it stale. **Test all** in the toolbar tests every connector that is
on.

The switch on a row applies at once. With four or more connectors, a search
(by name or address) and a filter (**All**, **On**, **Needs attention**,
**Off**) appear above the list. A click on a row opens its details beside the
list (over the page on a narrow screen): the switch, the last test with the
reason when it failed, the tools with their descriptions (with a search when
there are many), how it connects, and what is saved. **Remove** asks first; it
deletes the connector with its saved secrets.

`GET`, `PUT` and `DELETE /api/chats/mcp-connectors/{id}` read, replace and
delete one, and `POST .../enable` and `.../disable` switch it. You can have 20.
A test of a server that cannot be used is a `200` with `ok: false` and the
reason.

| Transport | What it is |
|---|---|
| `http` | A remote server. Streamable HTTP is tried first and the legacy SSE transport is the fall back. The address must be `http` or `https`, with no user name or password in it. |
| `stdio` | A command run on the host of the frontend, with `command`, `args` and `env`. Administrators only, and refused in distributed mode. |

OAuth sign-in for remote servers is not supported: use a token or a header.
A tool of a connector can have an interactive page; see [MCP Apps](#mcp-apps).

### Secrets

The token, the headers and the environment of a command are sealed in the
database or the JSON files, with the same key and the same method as the Telegram
tokens, and tied to the connector and its owner. They are write-only: the API
returns only `has_token`, `has_headers` and `has_env`. A `PUT` that leaves them
out keeps the stored values; an empty value clears them. They are never logged,
never put into a tool result or the model's context, and an error message never
repeats them or the query of the address.

### Which addresses a connector may reach

A connector makes the server connect to an address you choose. To stop it being
used to probe the internal network, a connector may reach only public hosts:

- The check is made **when the connection is opened**, on every address the name
  resolves to, and on every redirect. A name whose answer changes between the
  check and the connection (DNS rebinding) is refused too.
- Loopback, link-local, private (RFC 1918 and unique local), shared address space,
  cloud metadata and unspecified addresses are refused, for IPv4 and IPv6.
- An address that is plainly internal (an IP literal or `localhost`) is refused
  when you save or switch on a connector. A disabled connector may be saved with
  one, so that an import or a conversion can make it, but it cannot be switched on.

An administrator can allow private addresses with the runtime setting
`mcp_connectors_allow_private` (Settings, Access and security; default off; no
restart needed). Turn it on only when your users are trusted: with it, a connector
can reach services on the server's own network. Cloud metadata addresses stay
refused even then, and a request does not use a proxy from the environment. Turning it off closes the open
sessions that were made to such addresses: a tool handed out before is refused,
and the next turn connects again under the new rule. Likewise, a user who stops
being an administrator loses their running local commands at their next use.

A local command (`stdio`) runs without a shell, with a minimal environment
(`PATH`, `HOME`, `LANG`, `LC_ALL`, `TMPDIR`, `TZ`) plus the variables you sealed, and with a
time limit to start and to answer. It runs on the frontend, as the LocalAI process.

### How chats use them

- **A chat with no agent** gets the tools of all your enabled connectors, next to
  the MCP servers its model has.
- **A saved agent** names a connector as `connector:<id>` in its `mcp_servers` list;
  a name without the prefix is a server of the model. The agent form lists both.
  The agent holds the ID, so renaming a connector does not break it.
- **A connector that cannot be used** (switched off, deleted, not yours, the
  address refused or the server down) gives the model a note, "unavailable this
  turn: MCP connector Docs (cannot be reached)", and never fails the turn.
- **Triggers and sub-agents** follow the rules of agents: a trigger with an agent
  gets what the agent names; a sub-agent gets the tools its parent names.
- **Tool names.** A tool cannot take the name of a built-in tool or the add-on
  namespace; it is dropped. When two connectors (or a connector and the model)
  offer a tool with the same name, each gets the connector's name in front:
  `docs__search`, `mail__search` (the connector's name in lower case, two underscores, the tool). The names do not change while the connectors stay as they are.
- **Limits.** A call has the time limit of the model's MCP tools (360 seconds), so a research or document tool that takes minutes finishes, and its output is cut at 64 KiB like the output of the chat's own tools.
- **Per chat switches.** The tools panel of a chat lists the connectors and the
  model's MCP servers with a switch each. See [Chats](/features/chats/#mcp-tools).

### MCP Apps

A connector can offer MCP Apps: tools that declare an interactive HTML page in
`_meta.ui.resourceUri`. Chats show the page under the tool line as soon as the
call ends, while the turn goes on. The server
holds the MCP connection, so the page works for a connector like any other tool:
the page is read from the connector, and its tool calls go to the connector,
with the connector's own credential. The credential never reaches the browser.

- Only the owner's chat, with the connector enabled and not switched off in the
  tools panel of that chat, can read a page or call a tool for it. A saved agent
  must name the connector.
- Only a `ui://` resource that a tool of the connector declares is read.
- A tool whose `_meta.ui.visibility` does not include `"app"` cannot be called
  by a page. A tool whose visibility does not include `"model"` is not offered
  to the model.

See [Chats](/features/chats/#interactive-tool-pages-mcp-apps) for the routes and
the sandbox.

### Import from this browser

If the old chat page kept MCP servers in this browser (`localStorage`), the
MCP connectors page offers **Import N servers from this browser**. It shows each name and
address, never a header value, and makes a switched-off connector for each one.
A token or header leaves the browser only for a server the API accepted; the
ones that failed stay in the browser with the reason.

### Storage and clusters

| State | Mode | How |
|---|---|---|
| The connectors and their sealed secrets | Shared | Rows in the database (JSON files under the data path for one process). |
| The sealing key | Shared | The key of the Telegram connectors. |
| Open MCP sessions | Per instance | Each frontend opens its own sessions, lazily, for its own users' calls, and closes them after 10 idle minutes and when a connector is edited, switched off or deleted. A frontend notices an edit made on another by the record's update time at the next use. A session is never shared between users. |
| Local commands (`stdio`) | Refused | They would run on whichever frontend served the request. |

Remote connectors connect from the frontend that runs the turn, not from an
agent worker, so a server must be reachable from every frontend.
