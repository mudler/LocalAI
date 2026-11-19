+++
url = "/features/chats-connectors/"
disableToc = false
title = "Chat apps and connections"
description = "Connect Telegram, Slack and Discord bots to a saved agent."
weight = 5
aliases = ["/features/connectors-and-agents/"]
keywords = ["connectors", "telegram", "slack", "discord", "bot", "chat apps", "mcp servers"]
devbuild = "addon"
+++

A connector binds a chat platform to a saved agent. People write to the bot, and the agent answers. Telegram, Slack and Discord are add-ons from the add-on library.

## What you need

- A saved agent. See [Build your first agent]({{% relref "/start/first-agent" %}}).
- The add-on of the platform, installed by an administrator.
- The `connectors` and `agents` features for your account. They are off by default for new users on a server with accounts. `LOCALAI_DISABLE_CONNECTORS` turns connectors off for everyone.

A binding names exactly one agent. To serve two agents, make two bindings. The bot answers only the people on its list of allowed senders, which is empty when you make a binding. See [Who may use the bot](#who-may-use-the-bot).


**Connections** is an item of its own in the sidebar, right after **Agents**. It has three tabs at the top of its pages:

- **Accounts** (`/app/chats/connectors`, the tab it opens on) lists the accounts and tokens that your agents use as tools, such as the Google Drive, Gmail, GitHub, Jellyfin and Mail connections. These are the kinds that need no agent.
- **Messaging** (`/app/chats/connectors?tab=messaging`) lists the bots that answer messages as one of your agents, such as Telegram, Slack and Discord. An older link with `?kind=<kind>` opens the tab that holds the kind.
- **MCP servers** (`/app/mcp-connectors`) is the page of your [MCP servers]({{% relref "/features/chat-apps/mcp-servers#mcp-servers" %}}).

The sidebar item is lit on all three tabs. The addresses did not change, so old links keep working.

- A **messaging connector** binds a bot of a platform to one of your agents, so a person can talk to it from that platform. The platform comes from a **connector add-on**: the [Telegram add-on](#telegram), the [Slack add-on](#slack) or the [Discord add-on](#discord) (the [GitHub add-on]({{% relref "/features/chat-apps/accounts#github" %}}), the [Google Drive add-on]({{% relref "/features/chat-apps/accounts#google-drive" %}}), the [Gmail add-on]({{% relref "/features/chat-apps/accounts#gmail" %}}), the [Jellyfin add-on]({{% relref "/features/chat-apps/accounts#jellyfin" %}}) and the [Mail add-on]({{% relref "/features/chat-apps/accounts#mail-imap-and-smtp" %}}) hold a token or a login for your agents instead of carrying messages). These are the first half of this page.
- An **MCP server** gives your chats and agents more tools. See [MCP servers]({{% relref "/features/chat-apps/mcp-servers#mcp-servers" %}}).

## Connector add-ons

Core keeps what is the same for every platform: the bindings, the sealed bot token,
the allowlist, the sender log, one chat for each sender (or for each thread) and the
turn itself. The
platform's API is spoken by an add-on that declares the `connector` capability (see
[Connector add-ons]({{% relref "/addons/contract-reference/connectors-and-chats#connector-add-ons" %}})). Install the add-on
from the add-on gallery, and its page adds and edits the bindings of its platform.

The Accounts and Messaging tabs group your bindings by add-on and links each group to the
add-on's own page. Each row has a state chip (**Running**, **Off** or **Needs attention**), the
agent and who may write to it, and a switch that applies at once. **New connection** lists the installed
platforms and opens the add form of the one you pick. With no connector add-on installed, the page says so and links
to the gallery. `GET /api/chats/connectors/kinds` lists the platforms that a running
add-on serves. A kind whose add-on declares a setup text also has `setup`, a short phrase
such as "a bot token from @BotFather" that says what the person needs before a connection
works (see [Setup text]({{% relref "/addons/contract-reference/manifest#setup-text" %}})).

A binding names the add-on that serves it. A binding made before connectors became
add-ons names none, and belongs to the add-on of its kind: a Telegram binding keeps
working as soon as the Telegram add-on is installed. Until then the binding is kept,
switched as it was, and shown as not running with the reason "install the Telegram
add-on"; nothing polls and nothing is lost.

### How messages arrive: pull and push

Each kind in the add-on manifest declares `delivery`:

- **`pull`** (Slack, Discord and Telegram). The host polls the add-on. One process in the
  cluster, the leader, does the polling. It stores the offset with the binding. When
  the leader stops, a new leader continues from that offset.
- **`push`**. The add-on calls the host operation `connector.inbound` for each
  message. The turn then runs in the process that hosts the add-on. A push add-on
  therefore needs a `combined` owner or a distributed owner (see
  [The owner in distributed mode]({{% relref "/addons/contract-reference/owner#the-owner-in-distributed-mode" %}})).

### The page of a connector add-on

The page of a connector add-on (Telegram, Slack, Discord, GitHub and the others) looks like the MCP connectors page:

- A **row** for each connection has a letter tile, the name, a state chip, one line of facts (the service, the agent and who may write to it), a **Test** button, a switch that turns it on or off at once, and a menu with **Edit**, **Share** (with accounts, see [Sharing a connection]({{% relref "/features/chat-apps/sharing#sharing-a-connection" %}})) and **Delete**. A row that **Needs attention** shows why (no token saved, the add-on is not running, the same bot as another connection, or a failed test) and, when the cause is the token, a **Fix token** button in place of **Test**.
- A click on a row opens a **pane** beside the list with the facts, the last test (kept in this browser, so it says "Not tested in this browser yet" until you test), the recent refusals and the senders of the bot, and **Remove**, **Share** (with accounts), **Edit** and **Test connection** at the foot.
- **New** and **Edit** open a **side sheet** with one form in three sections: **Connection** (the name, the token, the agent and the fields of the add-on), **Who can talk to it** and **What it may do**. **Save and test** saves the connection and tests it at once. Closing the sheet over a change asks first.
- **What it may do** has three presets. **Answer only** ticks no permission: the bot replies and nothing else. **Standard** ticks the usual set that the agent holds (what **Reset to the usual set** ticks) and never a sensitive permission. **Custom** shows the whole matrix of who can do what. A preset never turns on a sensitive permission. The replies, the files and the places the bot may post are behind **Replies, files and places**, and the check of what the form will do is behind **Check what it will do**.
- The address `?new=1&agent=<agent id>` opens the add form at once, with that agent chosen and shown as fixed text. `?binding=<id>` opens the settings of that connection with its permissions in view.

## Telegram

The Telegram add-on binds a bot to one of your [saved agents]({{% relref "/features/chats-and-agents/chats-agents" %}}).
A person who messages the bot talks to that agent, in a conversation of their
own, and the answer comes back in Telegram. The conversation is a
[chat]({{% relref "/features/chats-and-agents/chats" %}}) of yours: it uses your quota and your model allowlist,
and you can open it in LocalAI.

### LocalAI tools in a messaging chat

The [LocalAI client tools]({{% relref "/features/chats-and-agents/localai-client-tools#tool-groups" %}}) (images, speech, transcription and so on) work in a messaging chat by default, within the permissions and model allowlist of the agent's owner. An agent can switch the group off with `localai_client` in `builtins_off`. The admin group is never offered there, whatever the agent says: the person who writes to the bot may not be the owner. Three actions are refused in a messaging chat with a plain error, because they read or change data of the owner that the sender must not reach: `voice_profile` with `delete`, and `identify` of `voice` and `face` (they search the registry of known people). `voice_profile` with `create` is refused too, because the person who confirms that the speaker agreed does it in Studio, not in a messaging chat.

### Set up a binding

1. Install the **Telegram** add-on from the add-on gallery.
2. Make a bot with `@BotFather` in Telegram and copy its token.
3. Make an agent that names a model. A connector has no model of its own, so
   an agent without a model cannot be bound.
4. On the **Messaging** tab open **Telegram**, and make a binding: a name, the token,
   the agent, and who may use it.
5. Save. LocalAI asks Telegram, through the add-on, which bot the token belongs to.
   A token that Telegram refuses is not saved: the request is a `400` and the page
   shows the reason. **Test** asks again at any time.
6. Write to the bot from an account on the allowlist.

```
curl -X POST .../api/chats/connectors -H 'Content-Type: application/json' -d '{"kind": "telegram", "name": "Support bot", "token": "123:abc", "agent_id": "<id>", "enabled": true, "allow": ["12345678"], "reply_mode": "progress", "conversation": "sender", "send_files": true}'
curl -X POST .../api/chats/connectors/<id>/test
curl .../api/chats/connectors/<id>/senders
curl -X POST .../api/chats/connectors/<id>/senders/<sender>/allow   # add a sender of the log to the allowlist
```


With authentication on, add `-H "Authorization: Bearer $API_KEY"` to each request.

Each sender of the log may carry `chat` (the ID of the chat of the last message), `chat_name`
(its name, when the add-on knows it) and, for a message that was ignored, `reason`:
`not_listed` (the sender is not on the list) or `not_addressed` (a group message that does not
name the bot). All three are optional. Each recent decision of the access view may carry
`sender_name`, `chat` and `chat_name` next to the sender IDs. The decision log is kept in
memory only and is empty after the server restarts; the sender log is stored. The page shows
the name beside the ID and falls back to the ID when it has none.

### What a person can send

- **Text.** Up to 8,000 characters.
- **Photos.** The add-on downloads the largest size of the photo that is within the
  [file limit]({{% relref "/features/chat-apps/answers#file-limits" %}}) and attaches it to the chat. The caption is the text of the message. An album is
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
- **Size.** The [file limit]({{% relref "/features/chat-apps/answers#file-limits" %}}) of the binding. A larger file gets a sentence
  that names the limit and which limit it is, and the caption still reaches the agent.
- **Limit the kinds (`inbound_media`).** A binding can take only some kinds of file:
  `photo`, `voice`, `audio`, `video_note`, `document` (a file sent as a file). For a
  bot that should hear voice messages but never see receipts or other papers, set
  `"inbound_media": ["voice"]`. A file of another kind is never downloaded and never
  reaches the chat: the sender gets one sentence, for example "This bot does not
  accept photos.", and the caption still reaches the agent. Empty or absent takes
  every kind. See [Limit the files a person can send]({{% relref "/features/chat-apps/answers#limit-the-files-a-person-can-send-inbound_media" %}}).

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
binding. `POST .../enable` and `.../disable` switch it. You can have 50 bindings.
A binding says `active: false` and gives a `reason` while its add-on does not run.

## Slack

The Slack add-on binds a Slack app to one of your [saved agents]({{% relref "/features/chats-and-agents/chats-agents" %}}).
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
curl -X POST .../api/chats/connectors -H 'Content-Type: application/json' -d '{"kind": "slack", "name": "Team app", "token": "xoxb-... xapp-...", "agent_id": "<id>", "enabled": true, "allow": ["U012AB3CD", "C0123456"]}'
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
  [Earlier messages of a thread]({{% relref "/features/chat-apps/answers#earlier-messages-of-a-thread" %}})). This needs the scopes
  `channels:history`, `groups:history`, `im:history` and `mpim:history`, which the
  manifest of the add-on page asks for. An app made with fewer scopes answers without
  that context; add the scopes and reinstall the app.
- **Other channel messages** are not read.
- **Answers** are converted from Markdown to Slack's `mrkdwn` (bold, italics, links,
  lists, code blocks) and split at 4,000 characters, the limit Slack asks for. A turn
  shows a "Working…" message at once, and the answer replaces it in place (see
  [How the bot answers]({{% relref "/features/chat-apps/answers#how-the-bot-answers" %}})).
- **Files.** A file attached to a message (within the [file limit]({{% relref "/features/chat-apps/answers#file-limits" %}}), at most 8) is downloaded with
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
  mention the app there. A direct message still needs the person on the list. Each
  channel also has a trust level, see [Channels where the bot listens]({{% relref "/features/chat-apps/access#channels-where-the-bot-listens" %}}).
  An `@name` entry matches the Slack username (the handle) of the sender, not the
  display name, and only when the app has the scope `users:read`. When in doubt, use
  the user ID (`U...`): the access log names the sender ID of every refusal.

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

## Discord

The Discord add-on binds a Discord bot to one of your [saved agents]({{% relref "/features/chats-and-agents/chats-agents" %}}).
People talk to the agent in a direct message with the bot, or by mentioning the bot in a
channel or thread of a server. The add-on keeps a Gateway link (a websocket that the
add-on opens to Discord), so LocalAI needs no public address.

### Set up a binding

1. Install the **Discord** add-on from the add-on gallery.
2. At `discord.com/developers/applications` choose **New Application**. On the **Bot**
   page choose **Reset Token** and copy the token once.
3. On the same page, under **Privileged Gateway Intents**, turn on **Message Content
   Intent**. Without it Discord sends no text from servers. The add-on needs no other
   privileged intent. The page **Test** shows a warning when the intent is off.
4. Add the bot to a server with the link that the add-on page builds under **How to make
   the Discord bot**: type the Application ID and open the link. It uses the scope
   `bot` and the permission number `274878008320`: View Channels, Send Messages,
   Send Messages in Threads, Attach Files and Read Message History. Never give the bot
   Administrator, Manage Messages or Mention Everyone.
5. On the Discord add-on page make a binding: a name, the token, the agent, and who may use
   it. When you save, LocalAI checks the token and shows which bot it belongs to.
6. Mention the bot in a channel it can see, or write to it in a direct message.

```
curl -X POST .../api/chats/connectors -H 'Content-Type: application/json' -d '{"kind": "discord", "name": "Team bot", "token": "<bot token>", "agent_id": "<id>", "enabled": true, "allow": ["<user ID>", "<channel ID>"]}'
```

### How Discord messages map to chats

- **A direct message** with the bot is a conversation with its sender. The host answers it
  only for a listed person, or when the binding accepts anyone (a person can write to a bot
  only when they share a server with it).
- **A mention** of the bot in a server channel, or a reply to a message of the bot, addresses
  the bot. The answer goes in the same channel. Any other channel message is not read.
  `@everyone`, `@here` and role mentions never address the bot.
- **A thread** (also a forum post) is a chat of its own, and its chat is the parent channel,
  so listing a channel also covers its threads. The bot follows a thread once it was
  mentioned in it: later messages of the thread need no new mention. The setting **Threads
  the bot follows** (`followThreads`) is `mentioned` (the default) or `never`. A followed
  thread stays followed for 24 hours after its last message; the add-on keeps the list in
  its data directory (channel and thread IDs and times, never a token). The bot does not
  start threads.
- **Earlier messages of a thread** are read once when a thread chat starts (see
  [Earlier messages of a thread]({{% relref "/features/chat-apps/answers#earlier-messages-of-a-thread" %}})): at most 30, through
  `GET /channels/{thread}/messages`, which allows 100 per call. Messages of other bots and of
  Discord itself (joins, pins) are left out. This needs Read Message History.
- **Never answered**: every message that a bot or a webhook wrote, the messages of the bot
  itself, and system messages. Two bots cannot answer each other.
- **Answers** are plain Markdown (Discord reads it) and split at 2,000 characters, the limit
  of Discord, with code blocks closed and opened again at each cut. Every message carries
  `allowed_mentions: {"parse": []}`, and the add-on also breaks up `@everyone`, `@here` and
  `<@...>` mentions in the text, so the answer can never ping anyone. The bot sets no typing
  indicator (Discord discourages it for bots); a working message (see
  [How the bot answers]({{% relref "/features/chat-apps/answers#how-the-bot-answers" %}})) shows that the agent works. Edits are limited
  by Discord, so a live answer updates about once a second.
- **Files.** A file attached to a message (at most 8, 10 MB each) is downloaded from the
  Discord CDN, with a fresh link read at the moment of the download and without the token,
  and attached to the chat. A file that the agent makes is uploaded (10 MB at most).
- **Waits.** When Discord answers `429`, the add-on waits the time it asks for (the
  `Retry-After` header, else `retry_after` of the body) once and tries again. An edit is
  not repeated.
- **Start a new chat** with `!new`. Discord clients may take a message that starts with a
  slash for one of their own commands, so `!new` is the documented form (see
  [Start a new chat with `/new`]({{% relref "/features/chat-apps/answers#start-a-new-chat-with-new" %}})). The add-on has no slash
  commands.

### Who may use the bot

The binding has one list of Discord IDs (numbers; turn on Developer Mode in Discord, then
right-click and choose Copy ID):

- **A user ID**: the person may use the bot in a direct message and by mentioning it in any
  channel the bot reads. The person is a listed sender, so they hold every capability of the
  binding that is not sensitive.
- **A channel ID**: everyone in the channel (and its threads) may mention the bot there.
  They get no capability of their own (see
  [Capabilities, roles and pairing]({{% relref "/features/chat-apps/access#capabilities-roles-and-pairing" %}})).
- **No names and no server IDs.** The page accepts IDs only: names are not unique and
  change hands. A server (guild) ID matches nothing, because the chat of a message is a
  channel; use the channel IDs. The page names every entry that is not an ID. The add-on
  cannot see the allowlist, so a guild ID that is saved over the API is not refused: it is
  only inert.

**Anyone** (`accept_any`) is for public channels and read-only agents. Anyone who can message
the bot, or mention it in a channel it can read, is let in. The host gives these senders
**no capability** (only listed or paired senders hold one), so the agent cannot save memory,
propose triggers or take their files for them. The agent's own tools still work for them:
give the agent read-only tools for this connection. Text from a public channel is untrusted
and can try to steer the agent (prompt injection). The host limits each sender to 20 messages
a minute, and the add-on queues at most 30 messages a minute of one sender, so one person
cannot fill the queue of the link. Roles of a server are not mapped: the add-on declares no
role, because a server administrator is not a role ID and the permission check would need
each member's roles and each channel's overwrites.

### Notifications in Discord

Set a channel ID as the notification chat to post them there, or `user:` followed by your
user ID to receive them as direct messages from the bot.

### The token of a bot

The token is sealed like the token of a Telegram bot, and no API returns it. The add-on holds
it in memory while it keeps the Gateway link of the binding open, writes it nowhere and calls
only `discord.com`, the Gateway and the Discord CDN. The link is closed after two minutes
without a poll (the binding was switched off or deleted).

A binding is switched off, with a notification, when Discord refuses the token (HTTP 401, or
Gateway close code 4004) or the intents (4013, 4014: the Message Content Intent is off). The
link reconnects with a backoff after any other failure and resumes the session, so events
are not lost across a short break. Discord allows 1,000 identifies a day for a bot, so the
add-on never retries a refused token. Use one LocalAI instance for one bot: a second process
that uses the same token would receive the same events and answer twice.

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

## One bot connected twice

You can register the same bot twice with two token pairs, for example one Slack app
with two sets of tokens. Both bindings then see every message, and both would
answer it. The messaging add-ons (Slack, Telegram and Discord) tell the host which
upstream account a token belongs to: the workspace and bot user for Slack, the bot
for Telegram, the application for Discord. The host keeps it with the binding, and it
learns it when you save a token and, for a binding that has none yet, when its add-on
runs. An add-on that does not tell has no identity, and its bindings are never compared.

When two enabled bindings of one owner and one kind share an identity:

- Both connection pages and the list show "This bot is also connected as *name*. Both
  connections will answer the same message.", a link to the other connection and a
  "Turn this one off" button. The API shows it as `duplicate` on the binding, with the
  code `duplicate_connection`.
- Saving a binding that is enabled and a twin of another enabled binding asks first. The
  API answers `409` with the type `duplicate_connection`, and `param` holds the name of the
  other binding. Send `confirm_duplicate` set to `answer twice` to save it anyway. Nothing is
  saved without it. Switching a binding on with the switch does not ask; the warning shows at
  once.
- At run time only the oldest enabled binding answers a message. The younger one logs one
  line and stays silent, so you never get two replies, even if you ignore the warning. This
  applies to a message that has a platform message ID, because that ID shows that the two
  bindings were handed the same message. Switch the older one off and the younger one answers.

The choice of the binding that answers is a function of the stored bindings (age, then ID),
so every frontend makes the same one without a lock. See also [Multiple frontends and readiness]({{% relref "/administration/distributed-mode#multiple-frontends-and-readiness" %}}).

## For add-on authors

### For add-on authors: sending files

A connector add-on does not choose which files reach a person. The host does, and it
sends each one with the typed `send_file` op: the request carries a `transfer` handle for the bytes (`name`, `mime` and `size` of the file; see [File abilities and limits]({{% relref "/addons/contract-reference/connectors-and-chats#file-abilities-and-limits" %}})) with the chat and the thread of the message it answers. The
host chooses the files in two ways:

- The files that the turn made or changed go on their own, after the answer.
- The files the agent picks with its `attach_file` tool go with them. The host checks the
  path against the chat's access when the tool is called and again when it sends, drops
  repeated paths, and counts both kinds in the limit of 5 files a turn.

When an answer shows a file with a paperclip line (for example a paperclip and a file name
such as `report.pdf`) and no file went with it, the host adds one note after the answer:
"I could not attach the file. Ask me again and I will send it." The answer is not changed.
No note is added when a file went, when the binding sends no files, or for a paperclip with
no file name.

An add-on that implements `send_file` gets both, with no code of its own. It must not read
chat files or add tools for this. When the platform refuses a file, return a
`ConnectorError` with a short, plain `message` (for example the name of a missing
permission): the host puts it after "I could not send <name>" in the message to the person.
Keep the message free of tokens and internal addresses.

### For add-on authors: the account of a token

The op `me` may return `accountId`: a stable name of the upstream account, equal for every
token of that account and at most 128 bytes (Slack returns the workspace and bot user, Telegram
the bot ID, Discord the application ID). The host compares it within one kind. Leave it empty
when the platform cannot tell. The SDK's `connector.Bot` has an `AccountID` field for it.

### Other connectors

Telegram, Slack and Discord are messaging connectors, and MCP servers have their own
page. Mail and GitHub are account connections: see [Accounts]({{% relref "/features/chat-apps/accounts" %}}).
The IRC, Matrix and Twitter connectors of the old agents are not rebuilt.

## Next steps

- [Accounts]({{% relref "/features/chat-apps/accounts" %}}): GitHub, Google Drive, Gmail, Jellyfin and Mail.
- [MCP servers]({{% relref "/features/chat-apps/mcp-servers" %}}): more tools for chats and agents.
- [How a bot answers]({{% relref "/features/chat-apps/answers" %}}), [Who may use a bot]({{% relref "/features/chat-apps/access" %}}) and [Confirmations, triggers and messages]({{% relref "/features/chat-apps/actions" %}}).
- [Sharing a connection]({{% relref "/features/chat-apps/sharing" %}}).
- [Chat apps in a cluster]({{% relref "/administration/distributed-mode#chat-apps-mcp-servers-and-projects-in-a-cluster" %}}).

<script>
var m={"multiple-frontends-and-readiness":"../../administration/distributed-mode/#multiple-frontends-and-readiness","github":"accounts/#github","any-repository":"accounts/#any-repository","classic-tokens":"accounts/#classic-tokens","writing-to-github":"accounts/#writing-to-github","google-drive":"accounts/#google-drive","create-the-google-client-administrators":"accounts/#create-the-google-client-administrators","connect-an-account":"accounts/#connect-an-account","tools":"accounts/#tools","writing-to-drive":"accounts/#writing-to-drive","limits":"accounts/#limits","gmail":"accounts/#gmail","create-the-google-client-administrators-1":"accounts/#create-the-google-client-administrators-1","scopes":"accounts/#scopes","connect-an-account-1":"accounts/#connect-an-account-1","tools-1":"accounts/#tools-1","what-the-agent-reads":"accounts/#what-the-agent-reads","drafts-and-sending":"accounts/#drafts-and-sending","limits-1":"accounts/#limits-1","jellyfin":"accounts/#jellyfin","what-the-agent-sees":"accounts/#what-the-agent-sees","changing-things-on-jellyfin":"accounts/#changing-things-on-jellyfin","mail-imap-and-smtp":"accounts/#mail-imap-and-smtp","set-up-a-connection":"accounts/#set-up-a-connection","security":"accounts/#security","limits-2":"accounts/#limits-2","what-a-message-gets":"answers/#what-a-message-gets","earlier-messages-of-a-thread":"answers/#earlier-messages-of-a-thread","reading-a-messaging-chat-in-the-web-ui":"answers/#reading-a-messaging-chat-in-the-web-ui","how-the-bot-answers":"answers/#how-the-bot-answers","while-the-agent-works-reply_mode":"answers/#while-the-agent-works-reply_mode","conversations-conversation":"answers/#conversations-conversation","start-a-new-chat-with-new":"answers/#start-a-new-chat-with-new","file-limits":"answers/#file-limits","files-the-agent-makes-send_files":"answers/#files-the-agent-makes-send_files","reply-language-reply_language":"answers/#reply-language-reply_language","limit-the-files-a-person-can-send-inbound_media":"answers/#limit-the-files-a-person-can-send-inbound_media","capabilities-roles-and-pairing":"access/#capabilities-roles-and-pairing","channels-where-the-bot-listens":"access/#channels-where-the-bot-listens","why-a-capability-is-blocked":"access/#why-a-capability-is-blocked","text-in-the-language-of-the-page":"access/#text-in-the-language-of-the-page","where-it-posts":"access/#where-it-posts","preview-before-saving":"access/#preview-before-saving","on-the-connection-page":"access/#on-the-connection-page","confirming-a-change-in-the-chat":"actions/#confirming-a-change-in-the-chat","triggers-from-a-connector-chat":"actions/#triggers-from-a-connector-chat","approval-modes":"actions/#approval-modes","runs-may-act":"actions/#runs-may-act","writing-to-a-chat-on-its-own-send_message":"actions/#writing-to-a-chat-on-its-own-send_message","sharing-a-connection":"sharing/#sharing-a-connection","connections-that-log-in-with-oauth":"accounts/#connections-that-log-in-with-oauth","connect-an-account-2":"accounts/#connect-an-account-2","share-a-connection-that-logs-in":"sharing/#share-a-connection-that-logs-in","approve-the-login-of-an-add-on-administrators":"accounts/#approve-the-login-of-an-add-on-administrators","with-the-api":"accounts/#with-the-api","mcp-servers":"mcp-servers/#mcp-servers","add-one":"mcp-servers/#add-one","the-list-and-the-details":"mcp-servers/#the-list-and-the-details","secrets":"mcp-servers/#secrets","which-addresses-a-connector-may-reach":"mcp-servers/#which-addresses-a-connector-may-reach","how-chats-use-them":"mcp-servers/#how-chats-use-them","mcp-apps":"mcp-servers/#mcp-apps","import-from-this-browser":"mcp-servers/#import-from-this-browser","projects":"../projects-reference/#projects","in-the-ui":"../projects-reference/#in-the-ui","attached-tools":"../projects-reference/#attached-tools","the-api":"../projects-reference/#the-api","project-memory":"../projects-reference/#project-memory","tool-defaults":"../projects-reference/#tool-defaults","project-skills-and-attached-tools":"../projects-reference/#project-skills-and-attached-tools","the-lens-model":"../projects-reference/#the-lens-model","pull-in-other-projects":"../projects-reference/#pull-in-other-projects","agents-created-from-a-project":"../projects-reference/#agents-created-from-a-project","in-the-ui-1":"../projects-reference/#in-the-ui-1","export-and-import":"../projects-reference/#export-and-import","sharing-a-project":"../projects-reference/#sharing-a-project","projects-shared-with-you-fences-and-triggers":"../projects-reference/#projects-shared-with-you-fences-and-triggers","connections-triggers-and-loops-of-a-project":"../projects-reference/#connections-triggers-and-loops-of-a-project","in-the-ui-2":"../projects-reference/#in-the-ui-2","who-may-use-the-bot-1":"access/#who-may-use-the-bot"};var h=decodeURIComponent((location.hash||"").slice(1));if(h&&m[h]&&!document.getElementById(h)){location.replace(m[h]);}
</script>
