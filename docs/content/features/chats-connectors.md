+++
disableToc = false
title = "Chats: connectors (messaging add-ons and MCP servers)"
weight = 23
url = "/features/chats-connectors/"
+++

The Connectors page of the [chats](/features/chats/) (`/app/chats/connectors`, under **Agents** in the sidebar) lists the messaging bindings you made, and the MCP servers have a page of their own (**MCP connectors** in the sidebar, `/app/mcp-connectors`):

- A **messaging connector** binds a bot of a platform to one of your agents, so a person can talk to it from that platform. The platform comes from a **connector add-on**: the [Telegram add-on](#telegram) or the [Slack add-on](#slack). This is the first half of this page.
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
5. Use **Test**. LocalAI asks Telegram which bot the token belongs to.
6. Write to the bot from an account on the allowlist.

```
curl -X POST .../api/chats/connectors -d '{"kind": "telegram", "name": "Support bot", "token": "123:abc", "agent_id": "<id>", "enabled": true, "allow": ["12345678"]}'
curl -X POST .../api/chats/connectors/<id>/test
curl .../api/chats/connectors/<id>/senders
```

`GET`, `PUT` and `DELETE /api/chats/connectors/{id}` read, replace and delete a
binding. `POST .../enable` and `.../disable` switch it. You can have 10 bindings.
A binding says `active: false` and gives a `reason` while its add-on does not run.

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
   `mpim:history`, `chat:write`, `files:read` and `users:read`.
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
  thread is one chat, shared by everyone who writes in it, and the app follows it:
  a later reply in that thread reaches the agent without a new mention (for 24 hours).
- **Other channel messages** are not read.
- **Answers** are converted from Markdown to Slack's `mrkdwn` (bold, italics, links,
  lists, code blocks) and split at 4,000 characters, the limit Slack asks for. A turn
  that takes more than a few seconds shows a "Working…" message, and the answer
  replaces it in place.
- **Files.** A file attached to a message (up to 1 MiB, at most 8) is downloaded with
  the bot token and attached to the chat, as an upload on the chat page would be: a
  document is read, an image goes to a model that can see. A larger file gets a
  sentence that it could not be read. The add-on downloads only from Slack's file host.
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
Socket Mode spreads the events of an app over all its open links, so bind one app to
one LocalAI instance only.

The Events API over HTTP is not supported: it needs a public address, and Socket Mode
gives the same events without one.

## What a message gets

- **The agent's tools only.** A message gets exactly the tools the agent grants,
  intersected with what you may use now. It never gets the tool set of a chat, and
  a message cannot change the agent. A run started by a connector never gets
  `propose_trigger`.
- **Your account.** The chat acts as you, so the feature gate, the model
  allowlist and your quota apply. If the model refuses the request, the sender
  gets one fixed sentence, for example that your quota is used up.
- **One chat for each sender.** Each person who writes to the bot has a chat of
  their own with the agent. A Slack thread is one chat for everyone in it (see
  [Slack](#slack)). They cannot see another person's chat. If you change
  the agent of the binding, the next message of each sender starts a new chat
  with the new agent.
- **Text, and files where the add-on reads them.** On Telegram a photo, voice note or
  file gets a sentence that says only text is read for now. The Slack add-on hands
  files to the chat (see [Slack](#slack)); with the files of the chats turned off, a
  message with a file gets a sentence that files are off, and its text still reaches
  the agent. A message longer than 8,000 characters is refused. A sender may
  send 20 messages a minute; more are dropped, with one notice a minute.
- **Private chats, and mentions.** A message in a group that the bot was added to is
  ignored, unless the add-on marks it as addressed to the bot (a Slack mention, or a
  reply in a thread the app follows).
- **Answers.** The bot sends the final answer of each turn, split at 4,096
  characters (the limit of Telegram), and shows "typing" while it works. A message
  that arrives while the agent works waits and is read at the agent's next request,
  as in any chat; the answer of that turn covers it.

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
| The reply being sent for a turn | Per instance | The frontend that took the message sends the answer. If it dies, the answer is lost; the message is not repeated. |
| Slack Socket Mode links and their queues | Per instance | The Slack add-on process keeps one link per polled binding, acknowledges each event at once and queues it until the next poll. An event that is queued when the process stops is lost. The followed threads and the "Working…" messages are kept there too. |

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

On the **MCP connectors** page (`/app/mcp-connectors`) choose **New MCP server**. Give it a
name (unique among your connectors) and an address, and optionally a bearer
token or other headers. Use **Test connection** to connect and list the tool
names before you switch it on.

```
curl -X POST .../api/chats/mcp-connectors -d '{"name": "Docs", "transport": "http", "url": "https://mcp.example/mcp", "token": "...", "headers": {"X-Team": "blue"}, "enabled": false}'
curl -X POST .../api/chats/mcp-connectors/<id>/test
# {"ok": true, "tools": ["search", "fetch"]}
curl -X POST .../api/chats/mcp-connectors/<id>/enable
```

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
can reach services on the server's own network. Turning it off closes the open
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
- **Limits.** A call has a time limit of 60 seconds, and its output is cut at 64 KiB like the output of the chat's own tools.
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
MCP page offers **Import N servers from this browser**. It shows each name and
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
