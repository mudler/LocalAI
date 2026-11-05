+++
disableToc = false
title = "Chats: connectors (messaging add-ons and MCP servers)"
weight = 23
url = "/features/chats-connectors/"
+++

The Connectors page of the [chats](/features/chats/) (`/app/chats/connectors`, under **Agents** in the sidebar) lists the messaging bindings you made, and the MCP servers have a page of their own (**MCP connectors** in the sidebar, `/app/mcp-connectors`):

- A **messaging connector** binds a bot of a platform to one of your agents, so a person can talk to it from that platform. The platform comes from a **connector add-on**: the [Telegram add-on](#telegram), the [Slack add-on](#slack) or the [Discord add-on](#discord) (the [GitHub add-on](#github) and the [Jellyfin add-on](#jellyfin) hold a token for your agents instead of carrying messages). This is the first half of this page.
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

- **`pull`** (Slack, Discord and Telegram). The host polls the add-on. One process in the
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
binding. `POST .../enable` and `.../disable` switch it. You can have 50 bindings.
A binding says `active: false` and gives a `reason` while its add-on does not run.

## GitHub

The GitHub add-on lets your [saved agents](/features/chats-agents/) read repositories: search code, read files and folders, and read issues, pull requests and their diffs. If you allow it, they can also write: comment on issues and pull requests, open issues, change a title or close an issue, add labels, and comment on a line of a pull request diff. It cannot merge, delete, push code, approve or request changes on a review.

1. Install the **GitHub** add-on from the add-on gallery.
2. On GitHub, make a **fine-grained personal access token** for only the repositories you want to share. The add-on page has a **Create a token on GitHub** button, and the steps and a table of the permissions are open there until you have a first connection. Give the token read access to Contents, Issues and Pull requests. To let the agent write, also give read and write access to Issues and Pull requests. Classic tokens (`ghp_`) are refused unless you allow them on the connection; see [Classic tokens](#classic-tokens).
3. On the add-on page, choose **New GitHub connection**. Paste the token and list the repositories, one `owner/name` per line or separated by commas (at most 50). `owner/*` and `*` are also accepted; see [Any repository](#any-repository). The name of the connection defaults to the first repository; a line that is not `owner/name` is named under the list. Press **Test**: the row shows, for each listed repository, whether the token reaches it and whether it can write.

This connection does not listen for `@mentions` on GitHub yet. An agent works on GitHub only when you ask it in a chat.

The agent field is optional. A connection without an agent shows **Credentials only** in the list: it holds the token, the repository list and the write mode for your chats, and it never answers a message. It cannot be switched on, take an allowlist, send notifications or hold permissions; the server refuses those with a message that asks for an agent. This is the manifest key `agentOptional` of the kind (see [Connector add-ons](/features/addons/)), which the GitHub kind declares; Telegram and Slack bindings always need an agent. Over the API, `agent_id` may be empty for such a kind and the connection reports `credentials_only: true`.

The list is checked by LocalAI before the add-on is called, so an agent is refused any repository that is not covered by it, even if the token could reach it. The token is kept sealed and is never shown again, and no tool result contains it. A call uses one of the connections that your chat enabled: with one connection it is enabled, with several you tick the ones the chat may use in the **Tools** menu of the chat (or the agent names a set), and until then the GitHub tools are not offered. With two or more enabled, the model names the connection in each call, and only an enabled one is accepted. LocalAI never picks one for you, and the list of the connection that was named is the only one that applies. See [The connections of an add-on](/features/chats/#the-connections-of-an-add-on).

### Any repository

A list entry can stand for many repositories. LocalAI matches the entry itself, before the add-on runs, as GitHub names compare: without regard to case, and only whole names.

| Entry | Covers |
|---|---|
| `owner/name` | that repository |
| `owner/*` | every repository of that owner or organization, one name deep. `other-owner/x` and `owner-evil/x` are not covered |
| `*` | any repository the token can reach, now and later |

A star works only as the whole entry `*` or as the whole name after an owner (`owner/*`). `acme/wid*` and `*/widgets` are refused, and `*` must be the only entry because it already covers the rest. The token still limits what GitHub shows: a fine-grained token reaches only the repositories you picked for it.

`*` needs a confirmation. In the page, tick the box under the list. Over the API, send `confirm_any_resource` set to `allow any resource` in the request that adds `*` (create, or an update that did not have it). The write mode is separate: `*` with **Read only** can change nothing. With `*`, the agent can also list the repositories the token reaches (the `list-repos` tool); with any other list it asks you which repository to use. The Test button reports how many repositories the token reaches instead of testing each one.

This is the manifest key `wildcardResources` of the connector kind (see [Connector add-ons](/features/addons/)). Only the GitHub kind declares it; for every other add-on, the Jellyfin and Home Assistant lists included, each entry stays an exact name. A tool that lists what a credential reaches declares `enumerates` and runs only for a connection whose list is `*`.

### Classic tokens

A classic token (`ghp_`) has scopes for the whole account, not for the repositories you list. By default LocalAI refuses one when you save it, and refuses to use a saved one. You can allow it per connection when a fine-grained token does not fit, for example for an organization that blocks them. Allowing it is a deliberate choice:

- In the page, tick **Allow a classic token** and then the box that says you understand it is wider than the list.
- Over the API, set the option `classicToken` to `allow` in `options`, and send `confirm_wider_token` set to `accept wider token` in the request that turns it on.

The repository list and the write mode apply as before, and LocalAI still enforces them before the add-on runs. But GitHub would let a leaked classic token do more than the list, so prefer a fine-grained token. Switching the option off stops the stored classic token from being used at once; the token is not deleted. Fine-grained tokens work as they did and need none of this. This is the manifest key `tokenGuards` of the connector kind, which holds back any token that starts with a prefix until the connection chooses an option.

What GitHub returns (issue text, comments, file content) is written by other people. Every result is marked as untrusted data for the model. Long files are cut at 96 KiB and long patches at 64 KiB; the result says what was cut and how to ask for the rest. When GitHub limits the rate of calls, the agent is told how long to wait.

### Writing to GitHub

On the connection you choose what the agent may change. The choice is stored with the connection and enforced by LocalAI before the add-on is called, so a model cannot raise it.

- **Read only** is the default. A write tool is refused and the agent is told to ask you to turn writes on.
- **Asks you first** stores each change as a proposal. The approval card says what will be done, in which repository and on which issue or pull request (with a link), as which connection, and the exact text that will be posted, and you confirm it in the approvals of the web interface. A chat or an agent cannot confirm it.
- **Acts without asking** lets a chat of yours make the change at once. A run without you present, such as an agent started by a trigger, still leaves a proposal for you to confirm.

The write tools are `comment-issue`, `comment-pr`, `create-issue`, `edit-issue` (title, close or reopen), `add-labels` and `review-comment-pr`. They work only on the repositories listed on the connection, the same list as for reading. The change is also refused when the target moved after the preview: for example the title changed, the issue is locked, or a pull request has a new commit.

Everything the agent writes is public to everyone who can see the repository. The tool descriptions tell the model so. Text is limited to 16 KiB, and a text that holds the token or something shaped like a secret is refused. Every comment and new issue carries a hidden marker (`<!-- localai-op:... -->`) that lets the add-on find an earlier attempt if GitHub took a change but the answer was lost. In that case the proposal shows an unknown outcome and nothing is sent twice.

Over the API the connection has a `write_mode` field: `off`, `propose` or `auto`. Absent means off for a new connection and unchanged for an update.

This version does not listen for mentions.

## Jellyfin

The Jellyfin add-on lets your [saved agents](/features/chats-agents/) and your chats look into a [Jellyfin](https://jellyfin.org) media server: search the library by title, browse a library, read the details of a film, a show or an album, see what was added lately, what a user can resume and which episode is next, see which players are active and what they play, and read the server version. If you allow it, they can also remote control a player (play an item, pause, resume, stop, seek, set the volume), mark an item watched or not watched, set a favourite and add items to a playlist. It cannot delete anything, scan or edit the library, or manage users. It needs Jellyfin 10.9 or later.

1. Install the **Jellyfin** add-on from the add-on gallery.
2. In the Jellyfin dashboard, as an administrator, open **Advanced**, then **API Keys**, choose the plus button, name the key (for example `LocalAI`) and copy it. A Jellyfin API key works for the whole server and not for one user; the write mode below is what limits the agent. Deleting the key in the dashboard cuts the access at once.
3. On the add-on page, choose **New Jellyfin connection**. Paste the key and write the **server address** as you open it in a browser, with the port, for example `http://192.168.1.10:8096` or `https://media.example/jellyfin`. Press **Test**: the row says whether the server answers, which version it runs and whether it accepts the key.

One connection holds one key and one server. To use a second server, add a second connection; when several are enabled in a chat the model names the connection in each call (see [The connections of an add-on](/features/chats/#the-connections-of-an-add-on)). The agent field is optional, as for GitHub: a connection without an agent holds credentials only.

**The address is the access list.** The server address is the one entry that the connection lists, and LocalAI checks it before the add-on is called, like a repository of a GitHub connection. The add-on talks to that address and to no other: only `http` and `https` are accepted, an address with a user name, a password or a query is refused, the key goes only in the `Authorization` header and never in an address, the paths are built by the add-on, and a redirect is not followed. A plain `http` address sends the key unencrypted; the page says so. Use it only on a network you trust.

**Self-signed certificates.** The add-on checks the certificate of an `https` server as a browser does. If your server uses a self-signed certificate, type its SHA-256 fingerprint in the **Certificate fingerprint** box (from the certificate view of your browser; colons and capitals are fine). The connection then accepts that one certificate and no other, and a different certificate is refused. Checks are never switched off. The fingerprint is stored with the address as `https://host:8920#sha256=<hex>`; this is what a model must copy as `server`, and the tool descriptions tell it to copy the address from the page.

### What the agent sees

Results are short and bounded: at most 50 items a page with a `next` index, overviews cut at 240 characters in a list and 1500 in the details of one item, 20 people per item. They hold names, years, ratings, runtimes, the watched state of the named user and the playback state of a session. They hold no file path, no stream address, no image address and never the key. Titles, overviews, names of people, user names and device names are written by other people, so every result is marked as untrusted data for the model and the add-on's skill says the same. The user of user-specific lists is chosen by name (`list-users` gives the names) and the add-on turns it into an ID; with one user on the server the name can be left out.

### Changing things on Jellyfin

The write mode of the connection works as for GitHub: **Read only** (the default), **Asks you first** and **Acts without asking**, stored with the connection and enforced by LocalAI before the add-on is called. With **Asks you first** each change is a proposal with a card that names the player, its user and what it plays now (for playback), or the item, the user or the playlist, and you confirm it in the approvals. A proposal expires after five minutes. The add-on reads the target again when you confirm: if another item plays on that player now, or the playlist changed, nothing is sent and the card says so. **Acts without asking** lets a chat of yours act at once, which includes pausing or stopping a screen other people are watching; a run without you present still leaves a proposal. The write tools are `control-playback`, `mark-played`, `set-favorite` and `add-to-playlist`.

The connection has a `write_mode` field over the API: `off`, `propose` or `auto`.

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
  mention the app there. A direct message still needs the person on the list. Each
  channel also has a trust level, see [Channels where the bot listens](#channels-where-the-bot-listens).
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

The Discord add-on binds a Discord bot to one of your [saved agents](/features/chats-agents/).
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
curl -X POST .../api/chats/connectors -d '{"kind": "discord", "name": "Team bot", "token": "<bot token>", "agent_id": "<id>", "enabled": true, "allow": ["<user ID>", "<channel ID>"]}'
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
  [Earlier messages of a thread](#earlier-messages-of-a-thread)): at most 30, through
  `GET /channels/{thread}/messages`, which allows 100 per call. Messages of other bots and of
  Discord itself (joins, pins) are left out. This needs Read Message History.
- **Never answered**: every message that a bot or a webhook wrote, the messages of the bot
  itself, and system messages. Two bots cannot answer each other.
- **Answers** are plain Markdown (Discord reads it) and split at 2,000 characters, the limit
  of Discord, with code blocks closed and opened again at each cut. Every message carries
  `allowed_mentions: {"parse": []}`, and the add-on also breaks up `@everyone`, `@here` and
  `<@...>` mentions in the text, so the answer can never ping anyone. The bot sets no typing
  indicator (Discord discourages it for bots); a working message (see
  [How the bot answers](#how-the-bot-answers)) shows that the agent works. Edits are limited
  by Discord, so a live answer updates about once a second.
- **Files.** A file attached to a message (at most 8, 10 MB each) is downloaded from the
  Discord CDN, with a fresh link read at the moment of the download and without the token,
  and attached to the chat. A file that the agent makes is uploaded (10 MB at most).
- **Waits.** When Discord answers `429`, the add-on waits the time it asks for (the
  `Retry-After` header, else `retry_after` of the body) once and tries again. An edit is
  not repeated.
- **Start a new chat** with `!new`. Discord clients may take a message that starts with a
  slash for one of their own commands, so `!new` is the documented form (see
  [Start a new chat with `/new`](#start-a-new-chat-with-new)). The add-on has no slash
  commands.

### Who may use the bot

The binding has one list of Discord IDs (numbers; turn on Developer Mode in Discord, then
right-click and choose Copy ID):

- **A user ID**: the person may use the bot in a direct message and by mentioning it in any
  channel the bot reads. The person is a listed sender, so they hold every capability of the
  binding that is not sensitive.
- **A channel ID**: everyone in the channel (and its threads) may mention the bot there.
  They get no capability of their own (see
  [Capabilities, roles and pairing](#capabilities-roles-and-pairing)).
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

Each binding has four settings, under **How it answers** on the add-on page and in
the API as `reply_mode`, `conversation` (with `window_minutes`), `send_files` and
`reply_language`.

### While the agent works (`reply_mode`)

The bot shows "typing" as soon as it takes a message, and again every 4 seconds
until the turn ends. Slack has no typing for a bot: there a working message plays
that part.

| Mode | What the sender sees |
|---|---|
| `progress` (default) | One message, "Working…", that lists the tools as the agent starts them. The answer replaces it. |
| `stream` | A preview that shows the end of the answer as the model writes it, with a cursor (`▉`). Before the answer starts it shows "Working…" or the tool the agent uses; the reasoning of the model is never sent to the platform. If the preview cannot be edited, it stays as it is until the answer comes, and the answer then replaces it; when it cannot replace it (the message is gone, or the platform refuses the edit), the answer is posted and the preview is deleted, so the conversation holds one message per answer. In a private Telegram chat the preview is a draft, and the answer follows it as a new message. Anywhere else it is one working message that the answer replaces. |
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

Four things sit on top of the allowlist. They work the same for every connector
add-on.

- **A listed sender.** A person who is on the allowlist themselves, by their ID or
  their `@name`, is trusted with every capability that is not sensitive, as far as
  `caps` and the switches of the agent and the server allow it. Nobody needs a
  pairing or a role for that. Someone who is let in only because their channel or group
  is listed, or because the binding accepts anyone, is not a listed sender and gets no
  capability this way.

- **Capabilities** (`caps`). The ceiling you enable for a binding, from a closed list
  (`proposals.confirm`, `memory.write`, `files.upload`, `triggers.propose`,
  `triggers.run`, `talkback.channels`, `tools.write`, and the sensitive `talkback.new_channel`,
  `profile.write`, `skills.write`, `pages.write`, `dashboard.write`, `knowledge.write`). The default is
  no: a binding with none enabled lets a sender talk to the agent, but the agent can
  not use any of these on the sender's behalf. A binding saved before permissions
  were enforced has none, so the first start after the upgrade gives it the usual set
  once, the same six that **Allow the usual** turns on (`proposals.confirm`,
  `memory.write`, `files.upload`, `triggers.propose`, `triggers.run`,
  `talkback.channels`; never a sensitive one). It only touches a binding with no
  capability whose kind lists permissions, and it logs one line for each binding it
  changes. It runs once for the whole store, also with several frontends, and never
  again, so a binding you clear later stays cleared. A binding you cleared before that
  start cannot be told from one never set and gets the set too. To keep the strict
  state, set `LOCALAI_SKIP_BINDING_CAPS_MIGRATION=true` before the first start.
- **Roles and grants** (`grants`). An add-on can declare roles, such as the admins of
  a workspace, and report the roles of each sender. You map a role to capabilities
  with `grants: [{"role", "caps"}]`. A grant is within `caps`, within what the kind
  lists as `permissions`, and never a sensitive capability. The kind list
  (`GET /api/chats/connectors/kinds`) shows each kind's `roles` and `permissions`.
- **Pairing.** Only a sender you pair may use the sensitive capabilities. Ask for a code
  with `POST /api/chats/connectors/<id>/pairing`; send it as a direct message to the
  bot from an account on the allowlist. The code looks like `LA-` and eight characters,
  works once and lasts ten minutes. The host checks it before any model turn, so
  the code never reaches the agent or the sender log. A binding holds at most three
  paired senders, and a pairing ends after 90 days. End one earlier with
  `DELETE /api/chats/connectors/<id>/paired/<sender>`. A sender who sends five wrong
  codes in an hour is ignored for an hour, and every attempt is logged without the
  code.

Pairing needs only a stable sender ID, so an add-on without roles supports it too.

The host checks these values for every message. A sender may use a capability when
the capability is in `caps`, no switch is off, and:

- for a capability that is not sensitive: the sender is on the allowlist by ID or
  `@name`, or is paired, or has a role that you granted it;
- for a sensitive capability: the sender is paired. The allowlist and a role never
  count.

The allowlist can hold people and channels together. The host matches the sender of
the message against the people entries (ID, and `@name` without regard to case). It
matches the chat to decide whether the bot answers, and to find the trust level of
that channel (below). So a listed channel always lets its members talk to the bot, but
each of them gets capabilities only through a role, a pairing, an entry of their own,
or a channel that is set to let everyone act. A binding with `accept_any` lists
nobody. A sender who is let in but is not trusted still gets a role or a pairing as
before.

#### Channels where the bot listens

Every channel or group on the allowlist (a Slack `C...` or `G...`, a Telegram group, a
Discord channel) has a trust level, `allow_trust` in the API, a map from the entry to
`answer` or `act`:

- `act` (the page says **Let everyone here ask for actions**): everyone who writes in
  the channel, in a thread of it too, is treated like a listed person for the
  capabilities that are not sensitive: sending files, proposing triggers, talk-back and
  the like. The ticked permissions of the connection and the grants of the agent still
  apply, so with an empty ceiling nothing is allowed. The add-on write tools still need
  `tools.write` ticked on the connection (the paired rule is unchanged). Sensitive
  capabilities, such as saving to the owner's notes or knowledge, still need a
  pairing, and the personal notes are still read for paired senders only.
- `answer` (**Answer only**): the bot answers there, but members who are not listed get
  no capability. This is the level of an entry stored before the setting existed, so an
  upgrade never widens trust. It is also the level of an entry that an API client adds
  without `allow_trust`.

A channel that you add on the settings page starts as `act`: allowing a channel is
what you expect to let its people use the bot, and the page shows one sentence on what
it means next to a switch back to **Answer only**. The level counts only for messages
written in that channel. A person who is not listed and writes to the bot in a direct
message is not trusted by the channel entry. `accept_any` is unchanged: it accepts
everyone and trusts nobody. The level applies to a group entry only; an `@name` or user
entry has none.

This list is not the list of channels the bot may post to. That list is **Where it may
post** (`channels`, capability `talkback.channels`) and says nothing about whose
messages the bot trusts.

When something is still refused in a channel, the bot says why in one plain sentence:
the channel is set to answer only, the person is not on the list, the action needs a
pairing, or the owner has not turned it on. The access log line carries the sender ID,
the code and the reason, for example that a sender was let in only through a channel set
to Answer only, and the settings page offers **Allow this person** next to such a
refusal.

The access view and the log name the way a sender got a capability: `allowlist`,
`paired`, `channel` (a member of a channel set to `act`), `agent-connection` (the write
tools of an add-on the agent is connected to) or `role:<id>`. If several people share one turn, the turn gets only what
all of them may do.

A tool of an add-on that holds your credential and changes something outside
LocalAI (a comment on an issue, for example) needs `tools.write`. So does a tool
that changes a resource of an add-on whose manifest scopes its resources per agent
(see [Resources of an agent](/features/chats-agents/#resources-of-an-agent)). The
host reads this from the add-on's manifest, so it applies to every add-on of that
shape. The
sender needs the capability as above; the write mode of the connection and the
confirmation of the change still apply on top of it.

The tick of `tools.write` is named "Let people who are not paired change things with
add-on tools", because that is who it is for. Connecting the agent to a connection
of an add-on already says that the agent may use it, so the write tools of that
add-on need no tick for you and for the people you paired with the bot. All of
these must hold:

- the sender is paired with the bot, and the bot lets that sender in today;
- the agent lists the add-on and has a connection of that add-on selected in the
  agent (a connection chosen in the chat, or the only one you have, does not
  count);
- that connection is not in write mode `off`;
- you did not take the tick away after setting it (see below).

The write mode (`propose` or `auto`) and the approval setting keep deciding whether
a call runs at once or waits for a confirmation: this changes only whether the tool
is offered. For every other sender the tick is still needed: a person on the
allowlist who is not paired, a person let in through a channel entry, anyone when the
bot accepts anyone (they never get write tools), a role, a trigger, a job and a
sub-agent. Write tools of an add-on without a connection, such as a table of the
agent, need the tick for paired people too.

If you tick the permission and later take the tick away, that is your choice and it
holds: paired people then have no write tools either. Ticking it again undoes this.
A save that never had the tick changes nothing.

The access view says it for each kind of sender. The paired sender shows the
permission as allowed "because this person is paired and the agent is connected to
the add-on" (the `agent-connection` way), and the other senders show the reason
`connection_permission_off`. The readiness list of the connection page has one row for
the add-on tools. It says that paired people can use the tools because the agent is
connected to the named connection (with its write mode) and that the others need the
tick. When the tools are held back, the model hears which tools and why, so the bot
says "the owner has not allowed this bot to change things in other services" and not
"I have no such tool", and the row says whether the cause is your removed tick, a
connection in write mode `off`, or a missing connection. The same note appears when
the agent names a connection of an add-on that no longer exists.

The agent's grants narrow the result: see Builtin grants in
[Agents](/features/chats-agents/). For `triggers.propose`, `triggers.run`,
`talkback.channels`, `talkback.new_channel` and `files.upload` the sender must hold
the capability and the agent must hold the grant.

The check decides which tools the model gets. A tool that the sender may not use is
not offered, so the model cannot be talked into it. If the model calls one anyway, the
call is refused with the reason, and the model tells the person. When a tool is held
back for a missing right, the turn also carries one short note for the model with the
reason in plain words (for example "the owner has not enabled reminders and scheduled
tasks for this bot", or "starting conversations in new channels needs a sender that the
owner paired with this bot"). The model is told to relay it and not to say that it has
no such tool. Today this gates the
agent's change proposals (`memory.write` for a memory note, `profile.write` for a
profile), files sent to the bot (`files.upload`), the trigger tools
(`triggers.propose`), and the confirm phrase (`proposals.confirm`), which answers trigger proposals and change proposals alike. Messages from a
sender who may not send files get a short refusal and no turn.

A binding that was stored before this check has no capabilities. Enable what you want
with `caps`, and pair the accounts that need the sensitive ones.

The settings page of every connector add-on edits all of this: a switch for each
capability, with a description and a mark on the sensitive ones; the role matrix; the
paired senders, with a button that shows a new code once; the channels it may post to
(talk-back, **Where it may post**); and the access view with the latest decisions. A binding with no capability
shows a notice that says why the features are off. The button "Allow the usual" turns
on the everyday capabilities that stay inside LocalAI and are not sensitive. In the
form, **Allow the usual set** ticks only the usual ones that the agent of the binding
holds, and says what it did. **Same as the agent** copies, once, every capability that
the agent holds and that has a binding capability, the sensitive ones too after a
confirmation that lists them; it does not follow later changes to the agent. A
capability the agent does not hold cannot be switched on. Both buttons only fill the form
and are off, with the reason, when the binding has no agent or the permissions of the
agent cannot be read. A link with `?binding=<id>` opens that binding at its permissions.
The form states the rule: people on the allowlist can use the ticked capabilities, and a
sensitive one also needs the person to be paired. It shows a notice when a sensitive
capability is ticked and nobody is paired, with a way to pair, and when the allowlist holds
only channels, whose members are not trusted one by one. A new binding still starts with no capability. The
kind list also returns `capabilities`, the closed list with a title, a description
and the `sensitive` and `usual` flags of each, so a page needs no list of its own.

To see the result, call `GET /api/chats/connectors/<id>/access`. It runs the same check
for a sender on the allowlist by ID or name (`listed`), a sender let in through a
listed channel (`anyone`), a paired sender and a sender of each role the kind
declares, and lists the latest decisions this server made (sender IDs, roles,
capability and result, never message text). An add-on settings page can show
it.

### Why a capability is blocked

Every capability in the access view says `allowed`. A refused one also says why, as data
a page can turn into a sentence and a link, in the first place the check failed:

```json
{
  "capability": "triggers.propose",
  "allowed": false,
  "reason": "the owner has not enabled triggers.propose for this bot",
  "code": "connection_permission_off",
  "blocked_by": "connection",
  "message": "This connection does not allow the bot to set reminders and scheduled tasks. Tick the matching permission on the connection.",
  "fix": {"kind": "open_connection", "target": "caps", "field": "triggers.propose"},
  "then_check": ["sender", "approval"]
}
```

Each kind of sender has one more entry, `notes.read`. It is not a permission that you can tick:
the owner's personal notes (the user and agent notes) are read into a
turn only for a paired sender, so a listed sender who is not paired, an `anyone` sender and a
role sender get `sender_needs_pairing`. The notes of the conversation itself and the agent profile are read for all.

An allowed capability has `allowed: true` and `via` (`paired`, `allowlist` or
`role:<id>`) and none of the other fields. `reason` stays the English line for logs.

The check goes through these links in order and reports the first that fails:

1. `server`: an administrator turned the feature off for the whole server.
2. `tools`: the Tools switch of the agent has the tool group off.
3. `agent`: the agent has not been given the permission.
4. `connection`: the permission is not ticked on this connection (or it has no agent).
5. `sender`: the person is not trusted for it (not on the list, needs a pairing, got in
   through a channel, or has a role without the grant).

`then_check` lists the links after the failed one that were not looked at, so more
problems may follow. For `triggers.propose` it ends with `approval`: the approval mode
shapes the result and never blocks.

| `code` | `blocked_by` | Meaning | `fix.kind` |
|---|---|---|---|
| `server_setting_off` | `server` | The feature is off for the server. `fix.target` is the setting key, `disable_triggers` or `disable_connector_talkback`. | `open_server_setting` |
| `tools_switch_off` | `tools` | The Tools switch of the agent is off. `fix.field` is `tools:<group>`. | `open_agent` |
| `agent_permission_off` | `agent` | The agent lacks the permission. `fix.field` is the permission. | `open_agent` |
| `agent_unavailable` | `agent` | The agent cannot be read. | `open_agent` |
| `no_agent` | `connection` | The connection has no agent. `fix.target` is `agent_id`. | `open_connection` |
| `connection_permission_off` | `connection` | The permission is not ticked. `fix.target` is `caps`. | `open_connection` |
| `sender_not_listed` | `sender` | The sender is not let in. | `add_person` |
| `sender_needs_pairing` | `sender` | A sensitive permission needs a pairing. | `pair_sender` |
| `channel_not_trusted` | `sender` | The sender got in through a channel set to answer only, or because the bot accepts anyone. Add the person, or set the channel to `act`. | `add_person` |
| `role_not_granted` | `sender` | The role of the sender has no grant. `fix.target` is `grants`. | `open_connection` |
| `kind_not_running` | `sender` | The app of the connection is not running, so roles are unknown. | none |
| `context_rule` | `connection` | Held, but not in this place or thread. | none |
| `knowledge_none` | `agent` | `knowledge.write` only: no collection is on the list of the agent. `fix.field` is `collections`. | `open_agent` |
| `knowledge_no_embedding` | `server` | `knowledge.write` only: the embedding model for Knowledge is not installed. | none |
| `unknown_capability` | `server` | Not a capability. | none |

The server sends no URL. A page maps `fix.kind` and `fix.target` to its own links. The
set of codes is closed, and the recent decisions carry `code` too.

The same codes feed the words a sender and the model get when a tool is held back. The
note for the model and the tool error use one whole sentence per code ("The owner has
not allowed this bot to set reminders and scheduled tasks."), with no setting names, host
names or error codes. The note tells the model to relay the reason plainly. The host
never posts it to a channel on its own.

Each capability has a verb phrase ("set reminders and scheduled tasks") that fills the
sentence of every code, so `message` always reads as a whole sentence, for example
"People in a channel are not trusted one by one, so the bot cannot save notes to memory
for them. Add the person to the allowed people."

### Text in the language of the page

The access view, the preview and the kinds call (which lists the capabilities) return
English text and, beside it, the same text in the language of the request. The language
is the `lang` query parameter (for example `it` or `pt-BR`), else the first language of
`Accept-Language` that the host translates, else English. The host translates Italian,
German, Spanish, French, Indonesian, Korean, Brazilian Portuguese and Simplified
Chinese.

- Each decision keeps `code` and `message` and gains `localized_message`, found by
  `code`. The view gains `language`.
- Each capability keeps `title` and `description` and gains `language`,
  `localized_title` and `localized_description`.

The `localized_*` fields are absent for English, for a language the host does not
translate, and for a refusal that a rule of the feature wrote itself (it has no code).
A page shows the localized field when it is there and the English one otherwise. The
note for the model and the words a sender reads in the chat do not use these fields:
the model reads English, and the sender reads the reply language of the connection.

### Preview before saving

`POST /api/chats/connectors/<id>/access/preview` answers like the access view for a form
that is not saved. The body has the fields that the check reads, with the names of the
update call: `agent_id`, `accept_any`, `allow`, `caps` and `grants`. A field that is
absent keeps the saved value; an empty list clears it. `sender` limits the answer to one
kind of sender: `listed`, `paired`, `channel_member` (or `anyone`), `channel_acts`, or `role:<id>`;
an unknown value is a 400. Use the id `new` with a `kind` for a connection that is not
saved yet. The reply has the shape of the access view, with `preview: true` and no recent
decisions.

```json
{"caps": ["memory.write", "triggers.propose"], "sender": "listed"}
```

The preview writes nothing and records no decision. It does not take a token and the
reply never has one. It uses the same sign-in, Connectors feature and routing as the
other connector routes, and the connection of another user is a 404.

### On the connection page

The page of a messaging connection (the Telegram and Slack add-ons) uses these calls in two places.

- **Readiness** sits at the top of the form: Token, Agent, Who may use it, Permissions, Triggers, Files and Notifications. Each row has a state (OK, needs attention, off, after you save, not working), one sentence and at most one button. Only the rows that need attention show at first; "Show all" opens the rest. A row that works only once the form is saved says so and offers "Keep these changes", which is the Save button.
- **What this bot can do** replaces the old list of what a sender may do. Choose the kind of sender (a listed person, a member of a listed channel, anyone when the bot accepts anyone, a paired person, or a role of the kind). Each permission shows as allowed or blocked. A blocked one shows the sentence of the host for the first blocker and one button. The page calls the preview with the values of the form as they are now, 400 ms after the last change, so it shows what a save will do, and it says so while the form has unsaved changes. A new connection is previewed under the id `new` with its kind.

The button follows `fix.kind`: `open_connection` jumps to that setting on the page, `open_agent` opens the agent in a new tab (so the form stays as it is), `open_server_setting` opens Settings for an administrator and says "Ask an administrator" for anyone else, `pair_sender` jumps to the pairing button, and `add_person` jumps to the allowed people. The latest decisions stay below the list.

## Confirming a change in the chat

When the agent proposes a change that lasts (a memory note, a change of its rules, a
skill, a page or the Home dashboard, with `propose_change`) in a chat from a messaging
app, LocalAI writes the question in that chat. The model does not write it, and it does
not send the person to Studio. A messaging chat asks first by default; the owner can let a
paired sender in a private chat save at once with the row **In messaging apps** (see
[Memory](/features/short-memory/#in-messaging-apps)). This section is the answer to the
question.

- **The language.** The question and the replies come in the reply language of the
  binding ([Reply language](#reply-language-reply_language)); the examples here are in
  English.
- **The question.** For example: `I can save a note in the memory (every chat of the
  owner): Standup time`, then the lines that will be added (at most 8 lines or 500
  characters), then `Reply "yes" to save it or "no" to drop it, here, within 24 hours. If
  more than one waits, reply "confirm CODE" or "cancel CODE".` The code has four
  characters and is the same on every frontend. Mentions that notify everyone are broken.
- **The answer.** A short word as the whole message, with the same words and languages as
  for triggers: `yes`, `ok`, `confirm` to save, `no`, `cancel` to drop. LocalAI reads it
  before any model turn and the model never sees it. The reply says what happened, for
  example `Saved: Standup time. Reply "undo" within 30 minutes to take it back.`
- **Undo.** The word `undo` (or the phrase of the language, see
  [Reply language](#reply-language-reply_language)), alone, within 30 minutes of a save
  that was made from the chat, restores the earlier text (`Removed: ...`). Any other
  word does not undo.
- **Several waiting.** A bare word answers only when exactly one proposal (a change or a
  trigger) waits in that chat and thread. Otherwise the bot lists the codes and the
  person writes `confirm CODE` or `cancel CODE`.
- **Who may answer.** A sender who holds `proposals.confirm` on the binding (a sender on
  the allowlist by ID or name, a paired sender, or a role with the grant). A change of
  the rules of the agent, a skill, a page or the dashboard needs a paired sender. In a
  shared chat only the sender whose message made the proposal can answer; in a private
  chat any trusted sender of that chat can. Another sender gets one sentence, `Only the
  owner can confirm this.` or `Only the person who asked can confirm this.`, and the
  proposal keeps waiting. When the sender who asked cannot confirm, the bot writes no
  question and the proposal waits in Studio for the owner; the model is told so.
- **Notes, scope and pairing.** A memory note from a messaging chat goes to the notes of
  that conversation (`scope` channel) unless the sender is paired and the agent's memory
  choice allows the agent's or the owner's notes. A note for those scopes is confirmed by a
  paired sender only. When the owner chose **Always confirm** (or the agent's rule is
  **Always confirm**), only the paired sender who asked can confirm, in a private chat too,
  and the model cannot undo or forget a note by itself.
- **One-to-one chats.** The host knows a chat is one-to-one from the `direct` field of the
  connector message (a Telegram private chat, a Slack direct message, a Discord direct
  message). The turn carries it, with whether the sender is paired, to the worker. A
  message that does not say counts as a group chat.
- **Time.** A proposal lapses after 24 hours. For 15 minutes after that, a bare word is
  answered with `That proposal expired. Ask again if you still want it.`; later it is
  an ordinary message.
- **Studio.** The same proposal shows as a card in Studio; whichever answer comes first
  wins and the other finds it closed. Every answer is in the audit of changes.

## Triggers from a connector chat

When the binding holds the capability `triggers.propose`, the agent can propose a
trigger from a chat in a messaging app. The agent gets `propose_trigger` and
`list_triggers` (the list shows only the triggers of that agent). Changing or deleting a
trigger stays in Studio. Nothing runs before the owner confirms.

- **The proposal is shown in the chat.** LocalAI writes the name, the schedule, what the
  run will do, where the results go and a four-character code in the chat and thread
  where the person wrote. The model does not write it.
- **Answer in plain words.** With the default approval mode (`ask`), the person answers in
  the same chat and thread with a short word as the whole message: `yes`, `ok`, `sure` to
  start it, `no`, `cancel`, `stop` to drop it (a few words of other languages work too).
  LocalAI handles it before any model turn. When several proposals wait in one thread, a
  bare word is not enough: the bot lists the codes, and the person writes `yes CODE`,
  `confirm CODE` or `cancel CODE`. Any longer message goes to the agent as usual. It
  needs the capability `proposals.confirm` for that sender (a sender on the allowlist by ID
  or name, a paired sender, or a role with the grant); a message from any other sender, or text the agent read, never answers
  a proposal. The same words confirm a change of the agent (see
  [Confirming a change in the chat](#confirming-a-change-in-the-chat)). A code is used once, and a proposal lapses after 24 hours. Studio always
  works as well.
- **Permissions are in the text.** The proposal says what a run may do with nobody
  present, in plain words: "Permissions: read only", or the `act.*` classes it carries,
  with the sensitive ones marked. A `yes` grants exactly the listed ones, and the
  confirmation repeats them. The model cannot change them after the confirmation.
  A read only proposal says what the person can do next: ask the agent to let the run
  act (when the binding allows it, see [Runs may act](#runs-may-act)), or change it later
  on the Triggers page.
- **Limits.** A binding can make 3 proposals an hour, and an owner can have 10 waiting.
- **One-off timers.** For "in 10 minutes" or "at 18:00", the agent proposes a trigger
  that runs once: `when` is `{"in": "10 minutes"}` or `{"at": "18:00"}` (this one needs a
  `time_zone`; a time that has passed today means the next day). It must be at least one
  minute and at most 365 days ahead. It reports to the chat where it was set, runs once,
  and is then removed, also when the run failed (a failed timer says so at once). A
  recurring trigger from a chat still runs at most once an hour. The proposal text states
  the cost: every run is one model run.
- **Where a run reports.** The proposal sets `delivery`: `inbox` (the default),
  `origin` (the chat and thread of the proposal) or `channel` (a channel of the binding that
  the owner approved, named in `delivery.channel`), and a mode: `always`, `on_change` or
  `on_problem`. LocalAI writes the last message of the run, cut to 4,000 characters,
  with mentions that notify everyone broken. A final message of exactly `[SILENT]`
  writes nothing. After three failed runs in a row the chat gets one alert, and not
  more than one every 6 hours. If the binding is off or the capability is off when a run
  ends, the Inbox gets the notice. A binding sends 20 of these messages a minute; more
  wait in a queue and go in order.
- **Pause.** While `triggers.run` is off for the binding, its triggers do not run. The
  records stay and are not replayed when the capability comes back.

### Approval modes

The owner chooses, per binding, what happens to a proposal. The choice is on the settings
page of the connector add-on, nested under the permission **Propose triggers** (greyed while
that permission is off, with its limits in a closed **Limits** list, and the current mode in
the line of the row), or in the `approval` field of the binding
(`PUT /api/chats/connectors/{id}`), and is the same for every connector.

| `mode` | Behavior |
|---|---|
| `ask` (default) | The bot posts the proposal and waits for a plain answer. |
| `auto` | The trigger is made at once. The bot tells the person what was set up and that `cancel` (within 30 minutes) or `cancel CODE` (any time) removes it. |
| `auto_readonly` | Like `auto` when the proposal says its runs only read and report (`access: read_only`, no file written); any other proposal asks. The runs of a trigger made this way can use only tools that read: every other tool is refused at the call, whatever the agent holds (see [Action policy of unattended runs](../chats-agents/#action-policy-of-unattended-runs)). |

The automatic modes are bounded by limits of the owner. Anything over a limit is not
refused but asked instead, and the proposal says why.

| Field | Default | Meaning |
|---|---|---|
| `max_pending` | 5 | Timers and triggers made without asking that may be live at once, per binding. |
| `min_gap_minutes` | 60 | Fewest minutes between runs of a recurring trigger. |
| `max_runs_per_day` | 10 | Runs a day of all of them together; a recurring trigger also gets this as its daily limit. |
| `max_lifetime_days` | 30 | How far ahead a timer may be, and how long a recurring trigger lives before it is removed. |

A proposal whose runs act follows the rules in [Runs may act](#runs-may-act).
A trigger on files, and one that reports to a channel, always asks. The read-only mode relies on what the proposal says and on
the grants of the agent; it does not stop a run from using a tool the agent has.

### Runs may act

By default the runs of a trigger that a chat proposes only read and report. The owner can
let them do more, per binding, with `approval.acts` in the settings of the connection
(`PUT /api/chats/connectors/{id}`, the same `approval` object as above). It is a list of
the `act.*` permissions that a trigger proposed from a chat of this binding may carry:
`act.files` (write files in the agent's folders), `act.addons` (use the actions of the
agent's add-ons, including running commands through an add-on), `act.send` (send
messages beyond where the run reports) and `act.spend` (use paid or external tools and
the network). The default is an empty list. Any other name is refused with `400`. A
present `approval` object replaces the whole object, so send `acts` again when you change
the mode or a limit.

On the settings page of the connector add-on, **Runs may act** is a switch under the approval mode of the permission **Propose triggers** (greyed while that permission is off). It is off by default. When it is on, four checkboxes name the acts in plain words: write files in the agent's folders, use the actions of the agent's add-ons, send messages beyond where it reports, and use paid or external tools and the network. All four are marked sensitive. A box is disabled with "The agent does not have this." when the agent lacks the matching grant; a box that is already ticked stays ticked and can be unticked. The line of the permission shows the choice, for example "Proposals: set up at once, runs may: write files, use add-ons". The page always sends the mode, the limits and `acts` together.

The list is a ceiling, not a grant. The acts of a proposal are what the model asks for in
`acts`, limited to this list, limited to what the agent holds:

- An act the model asked for that the binding does not allow, or that the agent does not
  hold, is dropped. The proposal text names it ("I did not allow: send messages (this
  connection does not allow it)"), so the person is not led to think the run can do it.
  The proposal is still made, with the acts that are left.
- The model sees, in the description of `propose_trigger` and in its result, which acts
  it may ask for here. It is told to ask the person "only report, or also act?" when the
  job clearly changes things.
- The agent is checked again at each run. If it loses a grant after the person said yes,
  the run holds the rest (see [Action policy of unattended runs](../chats-agents/#action-policy-of-unattended-runs)).
  A trigger made without asking also keeps to the acts of the binding at that moment.

What each approval mode does with a proposal that has acts (`act.files` also counts when
the plan names a file to write). Every act is sensitive:

| `mode` | Proposal with acts |
|---|---|
| `ask` | Asks, as always. |
| `auto_readonly` | Asks. This mode never starts a run that acts. |
| `auto` | Starts at once only when every act is in `approval.acts`, nothing was dropped, and every sender of the turn is paired with the binding (a role or the allowlist is not enough). Otherwise it asks and the proposal says why. |

The limits (`max_pending`, `min_gap_minutes`, `max_runs_per_day`, `max_lifetime_days`)
apply to these triggers as to any other. A proposal that does not ask for acts is not
changed by any of this.

## Writing to a chat on its own (`send_message`)

When the sender of a turn holds the capability `talkback.channels`, the agent gets the
tool `send_message`. The tool names no platform. The agent can use it to:

- **Write in the conversation.** With no `channel`, the message goes to the chat and
  thread of the conversation.
- **Write in a channel.** With `channel`, the message is a new top-level message in a
  channel that the owner listed for the binding (`channels` in the binding: `id`,
  `label` and `approved`, at most 10; an add-on settings page or the API sets them). A
  channel that is not listed is refused, and the error lists the ones that are. `thread`
  answers in a thread of that channel. The agent can name the channel by its label (case
  does not matter and a leading `#` is fine) or by its ID.
- **Write in a channel the owner has not enabled.** A listed channel with `approved`
  false needs the sensitive capability `talkback.new_channel` for each message, so only a
  paired sender can use it. A role never grants it. The owner approves a channel by
  setting `approved` to true.

The agent cannot add a channel or approve one: only the owner changes the list, so
text that the agent read in a web page or a file cannot widen where it writes.

A message has at most 4,000 characters, and mentions that notify everyone are broken.
The message goes through the same queue as other messages that LocalAI writes on its own:
a binding sends 20 a minute, and more wait in order. Each attempt adds a row to the audit
of the binding (time, binding, senders, channel, thread, kind, capability, result, length
and a SHA-256 hash of the text, never the text). `LOCALAI_DISABLE_CONNECTOR_TALKBACK=true`
turns the tool and the reports to a channel off for every binding; owner notices
continue.

A trigger can report to a channel under **Where it may post** with `delivery.target` `channel`. The
agent can name the channel by its label or its ID. The description of `send_message` and
of `propose_trigger` lists the approved channels (the first 10) as `label (id)`, so the
agent does not guess an ID. The report is a new message at the top of the channel, never
in the thread of the chat where the trigger was proposed; `delivery.target` `origin` writes
in that thread. The proposal text in the chat and the card in Studio say where the results
go, with the label of the channel. The proposal is refused if the channel is not listed
and approved, and the error lists the approved ones. When a run ends, LocalAI
checks again that the binding is on, the capability is on and the channel is still listed
and approved; if not, the Inbox gets the notice.

Not included yet: sending a file with `send_message`, and mapping a reply in the thread of
a message that the agent started to a conversation with the same agent.

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
so every frontend makes the same one without a lock. See also "Multiple frontends" above.

## Storage and clusters

Bindings, the read position in Telegram's updates, the sender log and the chat of
each sender are stored with your chats: in the database, in JSON files under the
data path, or in memory when there is no data path (then nothing survives a
restart). The JSON store is for one process.

| State | Mode | How |
|---|---|---|
| Bindings, offsets, sender log, sender chats, the key | Shared | Rows in the database. |
| Which frontend reads the bot | Single runner | The frontend that holds the advisory lock (`KeyChatsConnectors`). When it stops, another takes the lock and continues from the stored offset. Add-ons run on the process that owns the add-ons, and the loops run only where the add-on answers. |
| The upstream account of a binding, and which of two twin bindings answers | Shared | The account is stored with the binding. The oldest enabled binding of the same account answers; every frontend derives that from the stored bindings, so no claim is needed. A frontend remembers in memory only when it last asked an add-on for an account that the add-on did not tell, to ask again after 10 minutes. |
| Where the next message is read from | Shared | The offset is stored with the binding. Core passes it to the add-on's poll call and stores the offset that the answer names. |
| The reply being sent for a turn | Per instance | The frontend that took the message sends the answer, edits its working message or sends its draft, and sends its files. If it dies, the answer is lost; the message is not repeated. |
| The preview budget of a binding | Per instance | The time of the next edit or draft is kept in memory by the frontend that sends the replies. Replies of one binding run on the leader, so one budget covers them. |
| Draft IDs | Per instance | A counter in the process that sends the replies gives each turn a new draft ID. Two frontends could give the same ID, but a draft only lives in one chat for one turn. |
| The chats of the answers (thread mode) | Shared | Each answer's message IDs are stored with the chat of each sender, so a reply finds its chat on any frontend. |
| Slack Socket Mode links and their queues | Per instance | The Slack add-on process keeps one link per polled binding, acknowledges each event at once and queues it until the next poll. An event that is queued when the process stops is lost. The followed threads and the "Working…" messages are kept there too. |
| The language of each sender | Per instance | With `reply_language` set to `auto`, the frontend that takes a message keeps the last clear language of the sender and of the chat in memory for 24 hours. A trigger that runs on another frontend does not know it and uses English. The interface language of the owner is stored with the binding, so it is the same on every frontend. A fixed language is stored with the binding and works everywhere. |
| Files marked by `attach_file` | Per instance | The marks live in the process that runs the turn and are sent by the process that sends the replies. A turn that ends on another frontend than the one that sends the answer loses its marks; the agent then sees no error, and the person asks again. |

### For add-on authors: sending files

A connector add-on does not choose which files reach a person. The host does, and it
sends each one with the typed `send_file` op: the request carries a `transfer` handle for the bytes (`name`, `mime` and `size` of the file; see [File abilities and limits](/features/addons/#file-abilities-and-limits)) with the chat and the thread of the message it answers. The
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
