+++
title = "Connectors, change proposals and context files"
description = "Connector add-ons, change proposals from chats and context files."
weight = 70
+++

Look up the contract of connector add-ons, the change proposals that a chat makes and the context files. This page is part of the [Add-on contract reference]({{% relref "/addons/contract-reference" %}}).

## Connector add-ons

A connector add-on carries the messages of a messaging platform between the platform
and the [chats]({{% relref "/features/chat-apps" %}}). The Telegram, Slack and Discord add-ons are
first-party connector add-ons. The host keeps what is the same for every platform (the
bindings, the sealed bot token, the allowlist, the sender log, one chat for each sender
or thread, the turn and its notifications), so an add-on holds only the transport and
never stores a token. The first-party add-ons share their Go route
(`addons/internal/connector`) and their binding page (`addons/internal/connector-ui`).

The manifest declares the capability. It needs no tool and no `service` flag:

```json
"capabilities": {
  "connector": {
    "kinds": [{
      "id": "telegram",
      "title": "Telegram",
      "description": "Talk to one of your saved agents from a Telegram bot.",
      "senderHint": "A chat ID, a user ID or an @username",
      "delivery": "pull",
      "page": "bots",
      "drafts": true,
      "previewMs": 400
    }]
  },
  "ui": { "...": "the page that adds and edits the bindings" }
}
```

| Field | Meaning and bounds |
|---|---|
| `kinds` | 1 to 4 kinds. A kind is what a binding stores in `kind`, so it names the platform. |
| `kinds[].id` | A lower-case identifier, unique in the manifest. A binding names the add-on too, so another add-on cannot take over the bindings of a kind. |
| `kinds[].title`, `description`, `senderHint` | Plain text for people, with the bounds of the UI texts. |
| `kinds[].setup` | Optional, feature level 4. What the person needs before a binding of this kind works, as a short phrase that completes the sentence "Needs ...", such as `a bot token from @BotFather`. See [Setup text]({{% relref "/addons/contract-reference/manifest#setup-text" %}}). |
| `kinds[].delivery` | `pull`: the host asks for updates (long polling). `push`: the add-on calls the host operation `connector.inbound`. |
| `kinds[].page` | Optional. The add-on page that adds and edits bindings; it must be a declared page. The Connections page links to it. A kind that logs in with OAuth also makes its connections on this page, with the host's login (see [OAuth 2.0 credentials]({{% relref "/addons/contract-reference/manifest-extensions#oauth-20-credentials" %}})). |
| `kinds[].agentOptional` | Optional, manifest `api` 2. `true` lets a binding of the kind have no agent. Such a binding is credentials only: it holds the token, the resource list and the write mode, and the host never delivers a message to it, polls for it, answers from it or authorizes a sender on it. The host refuses an empty agent together with listening, accepting senders, an allowlist, a notification chat, talk-back channels, permissions, pairing or trigger approval. Without the key every binding of the kind needs an agent. |
| `kinds[].toolOnly` | Optional, feature level 5. `true` marks a kind whose bindings only serve tools: one binding is used by many agents, each through its own set of connections. It implies `agentOptional`. A binding of the kind never has an agent, never listens and never accepts senders: the host clears the agent on every save and read, and refuses `enabled` and `acceptAny`. The connector list returns `used_by`, the number of the person's saved agents that hold the binding, and the kinds list returns `tool_only`. A manifest that uses it must declare `featureLevel: 5`. |
| `kinds[].wildcardResources` | Optional. `true` lets a binding of the kind list `owner/*` (every resource of one owner) or `*` (every resource the credential reaches) in its resources. The host matches them before the add-on is called, without regard to case and on whole names; a star anywhere else is refused, `*` must be the only entry, and saving `*` needs the confirmation `confirm_any_resource` = `allow any resource`. Without the key every entry is an exact name. |
| `kinds[].tokenGuards` | Optional list of `{prefix, option, allow}`. The host refuses a credential that starts with `prefix` unless the binding set the kind option `option` to `allow`, and asks for `confirm_wider_token` = `accept wider token` when the option is turned on. It is checked when the token is saved and again each time it is used. `option` and `allow` must exist in the kind's `options`. At most 4. |
| `toolDefinitions[].enumerates` | Optional, on a read tool of an add-on with credentials. The tool lists what the credential reaches, names no resource, and runs only for a binding whose list is `*` (the kind needs `wildcardResources`). The request carries the resource key `*`. |
| `kinds[].drafts` | Optional. `true` when the add-on serves the op `draft`. The host then shows the live answer of a direct conversation as a draft instead of a working message. |
| `kinds[].previewMs` | Optional. The shortest time between two previews (edits or drafts) of one binding, 250 to 10000 ms. Without it the host waits one second. |
| `kinds[].history` | Optional, manifest `api` 2. `{"thread": true, "maxMessages": N}`: the add-on serves the op `history` for threads, and returns at most `maxMessages` (1 to 200; without it the host asks for 30). A kind without it is never asked, so it keeps the behaviour of a platform that cannot give old messages. |
| `kinds[].options` | Optional, manifest `api` 2, at most 4. A choice that each binding of the kind makes: `{"id", "title", "description", "values": [{"id", "title"}], "default"}` with 2 to 8 values and a default among them. The host shows the choice on the binding page, checks the owner's value against the declaration, stores it, and sends the value in effect to the add-on in `options` of every `poll`. What the option means is the add-on's own rule. |
| `kinds[].files` | Optional. The files the kind can receive and send, with a limit for each. Needs manifest `api` 2. See [File abilities and limits](#file-abilities-and-limits). |
| `kinds[].roles` | Optional, manifest `api` 2, at most 8. A role the kind can give to a sender: `{"id", "title", "description"}`, with an `id` in the same form as a kind `id`. The add-on owns the membership (for example, the admins of a workspace) and sends the role IDs of the sender in `roles` of each message, at most 8. The host drops a role the kind does not declare, and all roles for a kind that declares none. The owner of a binding maps each role to capabilities in the host. |
| `kinds[].permissions` | Optional, manifest `api` 2, at most 16. The capabilities the kind can grant to a role, from the host's closed list: `proposals.confirm`, `memory.write`, `files.upload`, `triggers.propose`, `triggers.run`, `talkback.channels`, `loops.wait` and the sensitive `triggers.change`, `talkback.new_channel`, `profile.write`, `skills.write`, `pages.write`, `dashboard.write`, `knowledge.write`, `loops.start`. An unknown name is refused. A sensitive name may be listed, but it is never granted to a role: only a sender that the owner paired can use it. `loops.wait` and `loops.start` may be listed too, but they belong to the binding alone and are never granted to a role. |

**Outbound: the connector route.** The host calls `POST /connector` on the add-on
with the same bearer credential and incarnation header as every other route and a body
`{"op", "kind", "token", ...}`. The ops are `poll` (`offset`, `timeoutSeconds` up to
25), `send` (`chat`, `thread`, `text`, and optionally `messageId`), `edit` (`chat`,
`messageId`, `text`), `typing` (`chat`, `thread`, and optionally `messageId`),
`send_file` (`chat`, `thread`, `transfer`: the handle of the file, see
[Moving a file by handle](#moving-a-file-by-handle)), `me` (who the bot
is, for the token check on save and the Test button), `media` (`media`: one
reference of a message's `media`, and a `transfer` handle; the answer is
`{"received": {"name", "mime"}}`), `delete` (`chat`, `messageId`: remove a message of
the bot) and, for a kind that declares `drafts`, `draft` (`chat`, `thread`, `draftId`,
`text`).

- **One message per answer.** A `send` with `messageId` replaces that message (the
  preview) with the first part of the text. The shared route tries the edit again after
  each wait the platform asks for (up to 3 tries, none over 30 s). If the edit still
  cannot be made, it sends the part as a new message and then deletes the preview with the
  same op as `delete`, so a preview with a cursor never stays beside the answer. A
  platform that cannot delete leaves it. The host never sends the reasoning of the model
  in a preview.

- **Options.** A `poll` carries `options`, a map from each declared option id to the
  value in effect (the owner's choice, or the default). The host sends it with every
  poll, so a change reaches the add-on within one poll. The shared route gives it to the
  platform with `connector.OptionsFrom(ctx)`. The Slack add-on uses it for `followThreads`
  (`own`, `any` or `never`): the add-on decides which messages are `mention`, so the host
  only forwards what the add-on marks as addressed to the bot.
- **The op `history`.** For a kind that declares `history`, the host calls
  `{"op": "history", "chat", "thread", "limit", "before"}` when a message in a thread
  makes a new chat, at most once for that chat. `limit` is the most messages to return
  (never more than `maxMessages`); `before` is the ID of the message the host handles
  now, which is not returned. The answer is `{"messages": [...], "truncated": bool}`, oldest
  first, the newest `limit` messages of the thread. A message is `{"messageId",
  "senderId", "senderName", "senderUsername", "text", "ts", "own", "media",
  "mediaNames"}`: `own` marks a message of the bot, `media` is the number of files and
  `mediaNames` at most 8 names; the bytes of a file are never part of a history.
  `truncated` says that older messages exist. The text is what people wrote: the host
  treats it as untrusted, flattens it, bounds it (30 messages and 8 KiB by default),
  and gives it to the first turn only. A failure or a `rate_limited` answer is
  logged and the turn goes on without the context; an add-on does not wait out a rate
  limit in this op. The add-on should read only the thread it is asked for. Core knows no
  platform: Slack's `conversations.replies` is the add-on's own business. The shared route
  serves the op for a platform that implements `connector.HistoryReader`.
- A `send` with a `messageId` replaces that message of the bot (the host's working
  message) with the first part of the text and sends the rest as new messages. When
  the message cannot be edited (it was deleted, or is too old), the first part goes
  as a new message too. The shared route (`addons/internal/connector`) does this for
  every platform.
- The `text` of a `send` is the answer, in Markdown. The add-on converts it to the
  markup of its platform and cuts it; the shared route lets a platform send each part
  in its markup and fall back to plain text by itself (Telegram does). The `text` of
  an `edit` or a `draft` is a preview: it goes as plain text.
- A `draft` shows `text` as a draft: a message that the bot is still writing, which
  the platform replaces when the answer is sent. `draftId` is not zero, and the host
  keeps one for the drafts of one answer. When the platform refuses it, the host
  sends a working message instead.
- An `edit` and a `draft` are not retried after a wait: a `rate_limited` answer comes
  back at once, and the host skips that preview. A `send` and a `send_file` wait
  once, as before.
- A `typing` with a `messageId` says that the host already shows a working message
  with that ID. A platform without a typing indicator (Slack) then posts nothing.
- `send_file` carries a `transfer` whose `name` is
  `contract.SafeFileName` of itself (no path, no control characters, at most 128
  bytes). The route refuses any other file, a call without a handle, and a platform
  that cannot send files.
  The answer names the message (Telegram) or the file (Slack) in `messageId`. The token is the
unsealed bot token of one binding. It is passed on each call and the add-on must not
write it anywhere. The answer is `{"updates", "nextOffset", "messageId", "botId", "botUsername", "received"}` or
`{"error": {"code", "retryAfter"}}` with a code of `unauthorized`, `conflict`,
`rate_limited`, `too_large` or `failed`. The host maps `unauthorized` to switching the binding off,
repeated `conflict` to the same, and `rate_limited` to a wait. `too_large` is the
answer of a `media` whose file is larger than the bound of the handle or than the platform lets a bot
download; the host tells the sender that the file is too large. A runtime that
implements `contract.ConnectorRuntime` has the route; others get a 404. The add-on
cuts a long `text` to the limit of its platform.

### File abilities and limits

The bytes of a file never travel inside the connector envelope: they move by a
handle (see [Moving a file by handle](#moving-a-file-by-handle)). A kind declares
what its platform can move, and the host decides how large a file may be, from the
number and the layer that sets it:

```json
"kinds": [{
  "id": "telegram",
  "files": {
    "receive": { "maxBytes": 20971520, "types": ["image/*", "application/pdf"] },
    "send":    { "maxBytes": 52428800 }
  }
}]
```

A kind that declares no `files` moves no files: the host does not ask it for
one and tells a sender who attaches a file that the platform takes none. Both
directions are optional, but `files` needs at least one. `maxBytes` is 1 to 1 TiB.
`types` is up to 32 distinct media types such as `application/pdf` or `image/*`;
it says what the kind moves. The host refuses an inbound file whose type the add-on
states and the list does not allow, and does not send an outbound file whose type
(from its extension) is not in the list. The
host also never sends a file whose first bytes are an HTML page. The host uses a declared size as an upper bound that the
owner can lower. It does not prove that a transfer works.

The limit in effect for one direction of one binding is the smallest of three layers:

| Layer | Set by | Named in the text to people as |
|---|---|---|
| Owner | The owner of the bot, in the **Files** section of the binding. It can only lower the limit of the kind. | "limit set for this bot" |
| Add-on | The kind, with `files.receive.maxBytes` or `files.send.maxBytes`. | "limit of" the kind's title |
| Host | The owner policy key `connector_files_ceiling_bytes`; zero means the default, 100 MB (100 MiB). | "limit of the platform" |

When a layer ties, the text names the one
a person can change first (owner, add-on, host). One function in core
(`EffectiveFileLimit`) decides this for every kind, so a connector never carries a size
of its own, and the sentences that the sender and the model read are built from its
result. An add-on that gets a file it cannot read answers `too_large`; the sender then
reads the limit in effect.

#### Moving a file by handle

The add-on moves file bytes by a handle, in both directions, with no more than one
chunk in memory. The add-on needs the host operation prefix `connector.` in its host policy, as it does
for `connector.inbound`.

The host adds `transfer` to the request of the op `media` or `send_file`, and no bytes:

```json
{"op": "media", "media": "<ref>", "transfer": {"handle": "…", "maxBytes": 20971520}}
{"op": "send_file", "chat": "…", "transfer": {"handle": "…", "name": "report.pdf", "mime": "application/pdf", "size": 6291456}}
```

- **Inbound (`media`).** The add-on downloads the file and streams it with
  `connector.file.put` `{"handle", "offset", "data", "final"}`, at most 256 KiB of data
  per call; it answers `{"received": {"name", "mime"}}`. An `offset` that is
  not the number of bytes the host has gets `{"received": n}` and nothing is written, so
  the add-on resumes there. The host stops at `maxBytes` (HTTP 413, code `too_large`).
  The host overwrites the size and the SHA-256 of `received` with its own record, so
  the add-on cannot choose them. It refuses an empty file.
- **Outbound (`send_file`).** The add-on reads the file with `connector.file.get`
  `{"handle", "offset", "length"}` (`length` at most 256 KiB) and gets `{"data", "eof"}`.
  It streams the chunks into the platform upload, so it needs no path and no token
  beyond the one the host passes with the call.

A handle is random. It is bound to the add-on of the call, so another add-on gets the
same answer as for an unknown handle. It lives as long as the connector call, which
may run up to 15 minutes, and ends with it: a file that is not finished is dropped with
its partial bytes. It also ends after two minutes with no call. Where the add-on runs
(the owner), the bytes go to the blob store of the workspace and are read from it, so
in a cluster the frontend and the add-on node share objects and need no shared disk.
Outbound, the frontend checks the access of the chat to the file before it makes the
handle and gives the owner the hash and size of that content only. Do not use a
`connector.file.*` call outside a connector call. An add-on that declares no `files`
is never sent a handle.

The SDK of the first-party add-ons (`addons/internal/connector`) has `Put` and `Get`;
a platform implements `StreamReader` and `StreamSender` and keeps only its own HTTP
calls. The limits come from the platforms' own documents: see the table in
[File limits]({{% relref "/features/chat-apps/answers#file-limits" %}}).

**Inbound.** A pull add-on answers `poll` with normalized messages (`cursor`, `chat`,
`thread`, `direct`, `mention`, `messageId`, `replyTo`, `senderId`, `senderName`,
`senderUsername`, `chatName`, `link`, `text`, `unsupported`, `media`, `mediaKinds`, `skip`) and
the next offset. `chatName` (the channel or group name) and `link` (an https address of the message or
its thread) are optional and are only shown to the owner in the header of the chat.
`mediaKinds` names the kind of each entry of `media` at the same index (`photo`, `voice`,
`audio`, `video_note`, `document`); the host reads it before it fetches a file, so a binding
that takes only some kinds (`inbound_media`) never fetches the others. An add-on that leaves
it out hands over files of an unknown kind, which only a binding that takes every kind reads.
`messageId` is the platform's ID of the message and `replyTo` the message it answers;
the thread conversation mode uses them. Core keeps the offset with the binding, passes it
to the next `poll` and stores the offset the answer names, so a new leader continues
where the old one stopped. A push add-on calls the host operation `connector.inbound`
with `{"bindingId", ...message}`; it is an [inbound operation]({{% relref "/addons/contract-reference/host-operations#inbound-operations" %}}).
Both paths end in one function of the host, so the allowlist, the sender log and the
rate limit are the same.

A message is answered when it is `direct` (a private chat) or a `mention` (it
addresses the bot in a group, such as a Slack mention or a reply in a thread the bot
follows); any other message is logged and ignored. Which chat a message belongs to
depends on the conversation mode of the binding (see
[How the bot answers]({{% relref "/features/chat-apps/answers#how-the-bot-answers" %}})); by default a
message with a `thread` belongs to a chat of that thread, shared by the people who
write in it, and any other message belongs to a chat of its sender. When a message has `media` (at most 8
references), the host reads each file with the op `media` before it makes the chat,
and attaches it to the chat, as an upload of the chat page would. The host makes the
name of each file safe again (`contract.SafeFileName`), and the chat saves it in its
own folder. An audio file (by its extension: `.ogg`, `.oga`, `.opus`, `.mp3`, `.m4a`,
`.wav`, `.flac`, ...) is a voice message: the host transcribes it with the
transcription model of the instance and the transcript, under
`[Voice message, transcribed]`, is the text of the turn; the recording is not kept.
With no transcription model, or when the transcription fails, the sender gets one
sentence and no turn starts. `unsupported` marks a message with no text and no file
that the add-on hands over.
The turn runs as the owner of the binding, with the tools of the binding's saved agent.

**Where it runs.** Core calls the route and registers the host operation on the process
that owns the add-ons. The binding loops run there, under the leader lock in a cluster.
A frontend that does not own the add-ons runs no loop.

**Existing bindings.** A binding of kind `telegram` that names no add-on (made before
connectors became add-ons) belongs to the add-on `telegram`. While that add-on does not
run, the binding is listed as not running with the reason "install the Telegram add-on".

## Change proposals from chats

A model can ask to change what lasts: a note in the person's memory, or the
profile of a saved agent. The host stores the request as a change proposal and
writes nothing until the owner confirms. The mechanism belongs to the host and
knows no chat platform, so every surface that runs a model uses it the same
way: a Studio chat, the chats of any connector add-on and a chat started from an
add-on page. An add-on needs no code and no manifest field for it.

How it works for an add-on author:

- The model calls the built-in tool `propose_change` (kind, scope, target,
  content, summary) in the tool group `changes`. A connector add-on carries the
  chat like any other: the tool result tells the model that nothing is saved
  yet, and the model says so in the platform.
- The proposal records its source as `{kind, id, chat, thread}`. `kind` is the
  origin of the chat (empty for a chat a person watches, `connector` for a
  messaging app), `id` is the binding, `chat` is the host chat. These names
  come from the host, never from the platform.
- The owner confirms in Studio. The host then writes the file as the owner in
  one workspace change set, so Files history and `revert_change` can undo it.
- `revert_change {id?}` undoes an applied change at once, because it only
  restores the earlier text. Without an id it undoes the last applied change of
  the same agent. A file edited since cannot be reverted.

The record is `contract.ChangeProposal` in `pkg/addons/contract` and is
versioned (`v`: 1). Readers ignore fields they do not know. The kinds are
`memory`, `profile`, `skill`, `page` and `dashboard`; this host offers all five.
A memory change writes under
`Memory/` (`Memory/<file>.md` for scope `user`, `Memory/agents/<agent>/` for
`agent`, `Memory/channels/<binding>-<hash>/` for `channel`). A profile writes
`LocalAI/agents/<agent>/profile.md` and is at most 4 KiB. Other contents are at
most 16 KiB. A target that is not a plain lower-case file name is refused, so a
proposal cannot leave `Memory/`.

A memory change with scope `project` writes `Projects/<folder>/memory/<file>.md`
for the project of the chat. A person who has write access to a project that
another person shares can also propose a note for it. This widens the record
with two optional fields, and a reader that does not know them ignores them:

- `proposer`: the person who made the proposal, when it is not the owner. The
  `owner` is still the person whose home the note is written to and the only
  one who can confirm, edit, dismiss or revert it. The host never sends an
  account here: the owner reads a display name. The field is empty for a note
  from the owner's own chat.
- `project_node`: the identity of the shared project folder, which the host
  checks again when the owner confirms.

The model names the shared project with the `project` argument of
`propose_change`: the reference of a shared project that the chat has pulled,
exactly as the tool description lists it. It cannot name a project the chat did
not pull, a project of its own, or an ID. A project that is shared read-only is
refused. The host checks the share again when the owner confirms. If the
project is no longer shared for editing, or it is archived, deleted or moved,
the proposal is settled as `stale`, the owner is told, and nothing is written.
The host keeps a read-only view of the proposals a person made for other
people's projects, with their status and never the owner. Today it is a service
call only: an endpoint and a screen for it come later. The owner sees a display
name for the proposer and nothing of the proposer's chat, agent or messaging
sender. The owner's "revert last" skips a grantee note that the owner confirmed: the
owner undoes it by its ID. A note for another person's project is never saved at once, whatever
the save setting of either person. The save setting and the memory choice of the
proposing chat decide whether it may propose; the settings of the owner do
not.

The other three kinds follow the same flow:

- `skill`: the content is a full `SKILL.md` (name and description in the
  frontmatter; the name must equal the target). Scope `user` writes
  `.skills/<name>/SKILL.md`, scope `agent` writes
  `LocalAI/agents/<agent>/skills/<name>/SKILL.md`; no other layer is offered.
  A skill with `locked: true` in its frontmatter cannot be changed by a
  proposal, and a locked personal skill cannot be replaced by an agent skill of
  the same name. The check runs again when the owner confirms.
- `page`: the target is the page name (lower-case letters, digits and dashes)
  and the files are an object of paths to text, with an `index.html`. The paths
  must stay inside the page folder, at most 32 files, all text, `page.json`
  checked like the Pages screen reads it, 256 KiB in total. The owner's
  confirmation writes `pages/<name>/` as one change set, so one undo removes it.
- `dashboard`: the content is the full `LocalAI/home.json`, validated against the
  same schema as the Home settings. A file that breaks the schema is refused
  when proposed and when edited at confirmation.

Limits and safety:

| Rule | Value |
|---|---|
| A proposal waits | 5 minutes, then it expires |
| Pending proposals of one person | 10 (for notes proposed for other people's projects, counted on the person who proposes) |
| Pending proposals of other people for one shared project | 10 |
| Proposals of one agent (or of plain chats) in an hour | 5 |
| Target changed after the proposal | the confirmation is refused (409) |
| Run started by a trigger, a job or a page, and sub-agents | never offered the tools |
| Chat that runs a saved agent | refused until the owner grants the kind (see below) |

A saved agent proposes a kind only when its `builtins` list holds the grant of
that kind: `memory.write`, `profile.write`, `skills.write`, `pages.write`,
`dashboard.write` or `knowledge.write`. The tool exists in a chat when the agent holds the grant, the
user may still use it and the chat did not switch the `changes` group off; its
schema lists only the kinds the chat holds. A plain chat holds what its user
may use. A run that started by itself never holds a grant. `GET
/api/chats/agents/builtin-grants` lists every grant with its title, description
and `sensitive` flag. A host can add a `proposals.Gate` on top: it is asked with
the action (`propose` or `revert`), the agent, the kind, the scope and the
source, after the grant check.

### How a memory note is saved

By default every memory note is a proposal that the person confirms. The person
can change this for memory notes only, in the Memory tab of the account settings,
where the server keeps the choice as its own setting. A person who chose on the page of an
add-on that declares a [user setting]({{% relref "/addons/contract-reference/host-operations#user-settings" %}}) with the role `memory.save-mode` (the
old Short memory add-on declared one, with the key `save-mode`) keeps that choice until they
choose in the account settings. The host finds the setting by its
role, so core code names no add-on:

| Value | What happens |
|---|---|
| `ask` (default) | The model proposes the note and the person confirms it, as above. |
| `auto` | A note from the person's own Studio chat is saved at once. The chat shows "Saved to memory" with an Undo, and no confirmation card. |
| `never` | The model cannot propose a memory note: `propose_change` does not list the `memory` kind, and the tool is not offered at all to a chat that holds no other kind. A call that still comes gets a plain refusal. |

An automatic note goes through the whole proposal path: the grant, the gate, the
target and size checks, the hourly limit and the audit. It is then confirmed as
the person, so it is one change set that `revert_change` and the Undo of the card
restore. The record has `auto: true`, and the audit lines of the write say it was
automatic. The safety rules are the host's, not the add-on's:

- Only a chat of the signed-in person can save without asking. A chat that a
  connector started always asks, whatever the setting says, because the sender of
  a message is not always the person. A trigger or a job never has the tool.
- `never` is the narrow side, so it applies in every chat, connectors included.
- A saved agent cannot widen the choice. Its grants decide whether it may propose a
  kind; the person's setting decides how a memory note is applied. An agent with its
  own `memory.write` grant still asks when the person said `ask`.
- A turn saves at most 5 notes without asking, a note over 4 KiB is not saved
  without asking, and a note that looks like it holds a secret (a private key
  block, a token, `password: ...`) is not either. Such a note becomes a normal
  proposal that waits for the person, and the model is told so.
- Only the `memory` kind reads the setting. A profile, skill, page or dashboard
  change always asks.
- If the setting cannot be read, the host asks. A failure never turns into `auto`. So
  does a missing declaration: without an add-on that declares the role, an old stored
  answer is ignored. An add-on that is not running does not count as `auto`. When
  several add-ons declare the role, the strictest answer wins (`never`, then `ask`,
  then `auto`), and a person who has not chosen on one of them counts as `ask`.

The hourly limit of 5 proposals per agent also counts automatic notes.

Every step is audited (propose, refuse, confirm, dismiss, expire, stale,
revert) with the source and a SHA-256 of the text, never the text.

REST, under the chats feature:

| Route | Does |
|---|---|
| `GET /api/chats/changes?status=` | list your proposals |
| `GET /api/chats/changes/{id}` | read one |
| `POST /api/chats/changes/{id}/confirm` | write it; a JSON body `{"content": "..."}` writes the owner's own text in place of the proposed one, with the same size limit |
| `POST /api/chats/changes/{id}/dismiss` | answer no |
| `POST /api/chats/changes/{id}/undo` | restore the file |
| `GET /api/chats/changes/audit` | read the audit |

Studio draws every proposal the same way, for any kind and any source. In a chat
the tool line of `propose_change` carries a card `{kind: "change_proposal", id}`
and the thread shows it under the message: the kind, the file and the scope, the
source (a chat, or a connector with its binding), the time left, and a line diff
against the file as it is now. The owner can Confirm, Edit then confirm (the text
the owner writes is saved in place of the model's) or Dismiss. After a
confirmation the card is a receipt with Undo. A proposal that waited a day shows
as expired, and one whose file changed since shows as stale; both offer to ask
the assistant again, and nothing was written. A write that fails leaves the
proposal waiting and says why. A kind the card has no wording for is shown by its
name. The changes that wait are also counted on the inbox button of the chat
header, and the Proposals page (`/app/chats/proposals`) lists them with the same
card.

In a cluster the proposals and the audit live in the shared database (the auth
database), so any frontend can confirm what another one stored. The limits are
checked in the same transaction as the insert. A race between two frontends can
let a limit be passed by the number of frontends that insert at the same moment,
never more. Without a database they live in a file under the data path, or in
memory when there is no data path.


## Context files

A context file is a short index in the user's home folder that the host puts in the
prompt of a chat, so the model knows what the user has saved without a tool call. The
details of each topic stay in files next to the index and the model reads them on demand
with the `context_read` tool. The mechanism belongs to the host, not to one app: a plain
chat in Studio, a chat that a Slack binding started and a chat that a Telegram binding
started all go through the same loader. An add-on does not add code to the host; it
declares a source.

### Declare a source

A source has four fields:

| Field | Meaning |
|---|---|
| `id` | A name, unique among the sources of the host. |
| `scope` | `user` (every chat of the user), `project` (the chats of one project), `agent` (the chats of one saved agent) or `channel` (the chats that one connector binding started). |
| `path` | The index file, relative to the user's home, ending in `.md`. An `agent` path holds `{agent}` once, a `channel` path holds `{channel}` once and a `project` path holds `{project}` once. The host fills them from the chat; the model never names them. |
| `cap` | The most bytes of the index that the prompt carries. Default and maximum: 2 KiB. |

For example, an add-on that keeps its own notes declares three sources: `Notes/index.md` for the user,
`Notes/agents/{agent}/index.md` for the agent and `Notes/channels/{channel}/index.md`
for the channel. A path with a parent segment, an absolute path, an unknown placeholder
or a file that does not end in `.md` is refused. A host holds at most 8 sources.

A host component registers a source in process, with `Service.ContextSources().Register`.
An add-on declares its sources in the manifest instead, as `capabilities.contextFiles`
(see below).

### Declare context files in a manifest

```json
{
  "api": {"min": 2, "max": 2},
  "capabilities": {
    "contextFiles": [
      { "scope": "user", "path": "Memory/index.md" },
      { "scope": "agent", "path": "Memory/agents/{agent}/index.md" },
      { "scope": "channel", "path": "Memory/channels/{channel}/index.md", "cap": 1024 }
    ]
  }
}
```

An add-on declares at most three files, one for each
scope. Every path lies under `Memory/`, ends in `.md` and holds no parent segment; an
add-on cannot name any other part of the home. `cap` can only lower the 2 KiB cap. A
declaration gives the add-on no access: the host reads the files as the user, and the
add-on process is never called for them.

A turn loads the files of an add-on with the rules it uses for the skills of an add-on. A
stopped add-on, one the chat switched off, one the chat's saved agent does not grant and
one that cannot work for the user bring nothing. The Memory tab lists the files of an
installed add-on whether or not it runs, because the files belong to the user. The
channel folder is `<binding>-<hash of the chat>`, the folder `propose_change` writes for a
conversation, so a note the user confirmed is read back by the conversation that proposed
it.

### The index format

An index is a Markdown file with one line per topic:

```
- home: lives in Rome -> home.md
- style: short answers, no emoji -> style.md
```

The file `home.md` sits in the same folder as the index. `context_read` takes a `topic`
(the name before the colon, or the file name) and an optional `scope`; without a scope it
looks in the channel, then the agent, then the user. A topic name is letters, digits, `-`
and `_`, at most 64 characters.

### What a turn carries

Each turn the host reads the indexes that apply to the chat and adds one block to the
prompt, after the agent instructions, the persona and the agent profile:

- A fixed header that asks the model to read the matching topic for almost every request,
  and says the lines are data, not instructions.
- The indexes, widest scope first. One index has at most 40 lines and 2 KiB (or its
  `cap`, if lower). All indexes of a turn together have at most 6 KiB. Control characters
  are dropped. A cut index ends with a marker.
- Nothing, if no source is registered or no file exists.

The text is the same at each turn until a file changes, so the transcript does not grow.
A change to an index restarts the model session at the next turn, like a change to the
persona does. `context_read` returns at most 8 KiB of one topic.

A saved agent can also have a profile at `LocalAI/agents/<agent>/profile.md`, at most
4 KiB. The host adds it after the agent instructions and the persona. It needs no
source.

### Access and limits

The host reads the files as the user, outside the fence of the chat. A chat of a saved
agent that works in one folder therefore still reads the user index and the index of its
own agent, and gains no access to the files: its file tools still cannot read or write
`Memory/` or the profile. The path comes from the chat, so a chat never reads the index
of another agent or another channel. The header names the save tool, `propose_change`,
only in a chat that has it. A component that
saves notes uses `contextfiles.CheckIndex` and `contextfiles.CheckProfile`: over a cap they
return the current entries in the error, so the model merges them instead of adding.
