+++
title = "Owner forwarding and HTTP relays"
description = "How frontends forward add-on requests to the owner and relay HTTP."
weight = 80
+++

Look up how a frontend forwards an add-on request to the owner and how HTTP is relayed. This page is part of the [Add-on contract reference]({{% relref "/addons/contract-reference" %}}).

## Owner forwarding and HTTP relays

The embedding host can configure `AddonConfig.Transport` at startup. The configuration
contains the owner HTTPS address, instance identities, CA roots, leaf certificate
SHA-256 pins, and Ed25519 signing keys. The owner also needs `ListenAddress` for its
separate mTLS listener. These settings are not runtime settings or browser inputs.
Without transport configuration, the public addon gateway remains unavailable.

All participating frontends must use the authoritative named-user database. The owner
queries current account, credential, and feature state before dispatch. Missing storage,
paused keys, revoked keys, expired sessions, and legacy synthetic identities deny access.
The embedding host's permissive installation callback cannot override these checks when
transport is enabled in the default named-user mode. Local-owner HTTP access exists only
when the deployment sets `OwnershipMode: "local-owner"`. A request never selects it; only the deployment does, and `local-ai run` does so by default when authentication is off.

The carrier requires TLS 1.3, CA verification, and explicit certificate pins. It obtains
a fresh random audience from the pinned owner before signing each request. Each owner
handler generates its own audience at construction. Restart therefore invalidates all
previous envelopes, including envelopes whose expiry has not passed. A bounded nonce
cache prevents replay within that handler. This cache is a documented per-owner exception;
it does not require shared storage because no other process can use that audience.

The signed payload binds the peer, actor, credential reference, operation ID, request
body, audience, nonce, and expiry. When a chat made the call, it also binds the chat
(`chat`: the user's principal and the chat ID), so the owner runs the add-on's
workspace operations with that chat's access (see
[Workspace operations]({{% relref "/addons/contract-reference/first-party-packages#workspace-operations" %}})). A changed, added or removed chat fails
the signature check. A request that no chat made has no `chat` field and signs as
before. An owner that does not know the field refuses a forwarded call that a chat made,
so upgrade the owner before the frontends. The carrier never forwards browser authorization,
cookies, or actor headers. It has no proxy discovery, redirects, affinity, fan-out,
owner election, or failover. An absent owner returns unavailable.

Two more optional capability fields describe the UI-only resource management (see
[UI-only resource management]({{% relref "/addons/contract-reference/proposals-and-resources#ui-only-resource-management" %}})).

| Field | Meaning and bounds |
|---|---|
| `capabilities.undo` | Boolean. The add-on keeps a change set for the writes a chat makes at once and answers the host's undo calls (see [Undoing a change a chat made]({{% relref "/addons/contract-reference/proposals-and-resources#undoing-a-change-a-chat-made" %}})). It changes no tool and offers nothing to a model. |
| `capabilities.adminTools` | At most 4 tool names that only the host's own UI-only endpoints may run. An admin tool is never in `tools`, never has a tool definition, is never offered to a model and is refused by every model, proposal and typed-read path: the declared-tool rule of those paths reads `tools` only. |
| `capabilities.connector` | Optional. 1 to 4 `kinds` of messaging binding the add-on serves, each with an `id`, a `title`, a `delivery` (`pull` or `push`) and optional texts and a `page`. See [Connector add-ons]({{% relref "/addons/contract-reference/connectors-and-chats#connector-add-ons" %}}). |
| `capabilities.client` | Optional, feature level 7. 1 to 32 `routes` that a paired client may call, each with an `id`, a `method`, a `path` and optional `stream`, `maxBodyBytes` and `timeoutSeconds`. The last segment of a path may be `{name...}` (feature level 8): it matches one or more segments and the add-on must validate the joined value itself. Needs `capabilities.service`. See [Paired clients: inference and add-ons]({{% relref "/addons/addon-clients" %}}). |
| `featureLevel` | Optional positive integer, default 1. The feature level the add-on needs from the host. See [Feature level]({{% relref "/addons/contract-reference/manifest#feature-level" %}}). |
| `setup` | Optional, feature level 4. What the person needs before the add-on works, as a short phrase such as `an access token`. A string or a map of language tag to string, like `ui.title`. For an add-on that has no connector kind; a kind has its own `kinds[].setup`. See [Setup text]({{% relref "/addons/contract-reference/manifest#setup-text" %}}). |
| `capabilities.resources` | Maps the management operations to tools: `create` (a declared tool of kind `create`), `rename` (a declared tool of kind `schema`), `inspect` (a declared tool of kind `read` whose result is an object with a `name` and a `revision`), `delete` (the one admin tool) and a plain-text singular `noun`. Every field is optional. An admin tool must be the `delete` of the mapping, and a mapping must name tools that exist with the kind it needs. A mapping with `create` or `delete` must also declare the read tool `resource-exists` in `tools`, because the host settles interrupted creations and deletions only on its explicit answer. `list` asks the host for a listing tool; see [Resource listing]({{% relref "/addons/contract-reference/manifest-extensions#resource-listing" %}}). |

A tool definition names the resource it works on with a required `resourceId`
argument, with two exceptions. A `create` tool has no `resourceId`: the host
reserves the resource. A `read` tool may leave `resourceId` out when it answers
about the person who calls it and not about a resource, for example a list of
that person's own items. The host runs such a read without a resource, binds the
envelope to the calling user, and the add-on must scope its answer to that user.
The host operation the add-on calls must scope by the actor of the envelope; the
add-on never takes a user from the arguments.
A `write` or `schema` tool, and a `read` tool that offers `resourceId`, must
require it.

A tool definition with `"uiOnly": true` is for the add-on's own page. It must be a
`read` tool without `resourceId`. The host runs it for the page like any such read,
but never offers it to a model, a chat, an agent or a trigger, and a model that
names it gets the unknown-tool answer. The Sandbox uses it for `set-backend` and
`confirm-host`: they change a setting, and the add-on asks the host whether the
caller is an administrator before it acts.

### Add-ons that keep the owner's own data

An add-on that keeps the data of one person and needs no credential and no resource list (the
[Calendar]({{% relref "/addons/catalogue/work/calendar" %}}) and [Contacts]({{% relref "/addons/catalogue/work/contacts" %}}) add-ons) sets `capabilities.ownerData` to `true` and
`featureLevel` to 3. Its tools name no resource: the add-on keys the data on the user in the
authority of the call, never on an argument. Reads run at once for every sender a messaging
bot lets in. A tool of another kind than `read` is held to `tools.write` in a messaging chat,
like the writes of a credentialed add-on: the owner and the senders paired with the bot hold
it without a tick on the bot, and every other sender (a listed person who is not paired, a
channel, a bot that accepts anyone) can read but not change. To let everyone the bot lets in
change the data, tick **Let people who are not verified change things with add-on tools**
(`tools.write`) in the permissions of that bot. The tick covers every sender, including listed
people and, for a bot that accepts anyone, any sender, and it applies to these add-ons only:
for other add-ons `tools.write` still stops short of a sender who is only let in. A trigger or a job runs the writes
only under the `act.addons` grant of the action policy, as for any action tool.

### Resource listing for agents

An add-on whose resources a person may want to hand out one by one can opt in to
per-agent resources:

```json
{
  "featureLevel": 2,
  "capabilities": {
    "resources": {
      "noun": "database",
      "nounPlural": "databases",
      "agentScoped": true,
      "list": {"tool": "databases-list", "items": "databases"}
    }
  },
  "i18n": {"it": {"resources": {"noun": "database", "nounPlural": "database"}}}
}
```

- `agentScoped`, `nounPlural` and `i18n.<language>.resources` need `featureLevel` 2
  (see [Feature level]({{% relref "/addons/contract-reference/manifest#feature-level" %}})). `agentScoped` also needs a `list` mapping,
  else the manifest is refused.
- The host names the resources in the agent form with `noun` and `nounPlural`, or
  their translation for the language of the page. `noun` is the singular fallback
  when there is no plural. Main-app texts name no add-on and no noun.
- The add-on is not told about the agent. The host checks the list of the agent
  before it forwards a call that names a resource, and filters the listing and the
  selection of a chat. See
  [Resources of an agent]({{% relref "/features/chats-and-agents/chats-agents" %}}#resources-of-an-agent).
- A tool that writes in such an add-on needs `tools.write` in a messaging chat.

### A confirmation the owner can relax

A `write` tool with `"approval": "always"` always waits for a person. This holds with or
without a credential: an add-on that declares none gets a stored proposal with the hold
`approval_always`, and the host applies it only when the owner confirms it in the browser.
A model, an API key, a trigger, a job or a sub-agent only proposes; it never runs the
write. The proposal expires like any other proposal, and the approval card shows it.
Only an add-on with a credential can relax the confirmation (see below). A tool can also
declare `"approvalOverridable": true`, so that the owner can choose not to be asked for
that one tool. It is the only way to relax `approval`, and it needs three things:

- The add-on declares a credential. The choice belongs to the person who owns the
  connection.
- The manifest declares the [user setting]({{% relref "/addons/contract-reference/host-operations#user-settings" %}}) `approval-<tool>` with the
  values `ask` and `skip`, the default `ask` and no `role`. A manifest with another
  default, other values or no setting is refused at install.
- The owner sets it to `skip` on the add-on's page. The write needs the owner's own
  browser session, like every user setting, so no model, API key or tool call can do it.

The host then runs the tool without a card only when the connection's write mode is
**propose** or **auto** (never **off**) and the call comes from one of two places:

- a chat that the owner has open themselves, or
- a messaging chat in which every sender of the turn is paired with the bot.

A sender who is only on the allowlist, a sender let in because the bot accepts anyone, a
role, a trigger, an agent job, a page, a sub-agent, and any call with no chat still get a
proposal that waits for a person. The setting is read again when the call is applied, so
switching it back stops a call that was stored a moment earlier. A tool that is not
marked `approvalOverridable` is never relaxed, whatever the setting holds. In a cluster
the decision for a messaging chat needs the grant of the turn, which lives on the
frontend that runs it; a call without it asks.

The write mode **auto**, which the add-on pages call **Allow all**, lifts the same ceiling
for these tools: with a connection on **auto**, a tool marked `approvalOverridable` runs
without a card whatever its `approval-<tool>` setting says, because the person chose to let
the agent act without asking for the whole connection. A tool with `"approval": "always"`
that is not `approvalOverridable` still waits for a person in every write mode. Use that
for an action that must never run unasked. The mail, Gmail, Google Drive and PiKVM add-ons declare none.

Every call that runs without a card is logged with the add-on, the tool, the connection,
the user and the outcome (`applied`, `rejected`, `unknown` or `not_run`), and never the
arguments. The same line is added to the connection's latest decisions. The connection's
access view and its access preview (`approvals` in the answer of
`GET /api/chats/connectors/{id}/access` and of the preview route) list each relaxable tool
with its setting and who it runs for without asking now.

### A write the connection's mode decides

A `write` tool that declares no `approval` follows the write mode of its connection:
**off** refuses it, **propose** holds it for a person to confirm in a card, and **auto**
applies it at once. The add-on pages name the three modes **Read only**, **Asks you first**
and **Allow all** (some older pages say **Runs at once** or **Acts without asking**). The
mail, Gmail, Google Drive and PiKVM add-ons offer all three. Declare `"approval": "always"`
only for a tool that must wait for a person even in **Allow all**; it also keeps the tool
waiting in **propose**, so an add-on that wants the connection to decide leaves it out.
Before the mail, Gmail and Google Drive add-ons offered **Allow all**, their write tools
declared `"approval": "always"`, so a connection that an API call had set to `auto` still
waited for a person. Those tools no longer declare it, and such a connection now applies
the change at once. The mode belongs to the person, who sets it on the add-on page with
their own session; a model cannot change it, and a new connection made on the page starts
on **propose**. The SSH add-on works this way: its `run` tool declares no `approval` and
no `approvalOverridable`, so the connection is the one place where the person chooses
between confirming each command and running it at once. The card of a held command shows the
host and the exact command (see [SSH]({{% relref "/addons/catalogue/services/ssh" %}})).

The host limits a write call to the apply timeout (60 seconds), so a write tool that runs a
command must stay below it.

### What happened to a tool call

Every call of an add-on tool from a chat writes one line to the host log when it ends,
whatever the ending:

```
studio: addon tool addon=<id> tool=<name> kind=<read|write|schema|create|action|-> binding=<id|-> resource=<name|-> chat=<origin> sender=<kind> outcome=<ok|denied|held|error> reason=<code|-> status=<http status|-> ms=<n>
```

- `chat` is the origin of the chat: `studio` for the web UI, or `connector`, `trigger`,
  `job` or `page`.
- `sender` says who the call is for: `owner` in the web UI; `paired`, `listed`, `role`,
  `channel`, `anyone` or `mixed` for a messaging turn (how the sender was let in);
  `trigger` or `job` for an unattended run.
- `outcome` is `ok`, `held` (a write that waits for a person), `denied` (a rule stopped it
  before the service was called) or `error`. A write that waited in a messaging chat
  later gets one more event in **Latest decisions**: `confirmed`, `cancelled` or
  `expired` (see [Confirming a held write in the chat](#confirming-a-held-write-in-the-chat)).
- `reason` is a stable code: a code of the permission chain (`sender_needs_pairing`,
  `connection_permission_off`), `write_mode_off`, `write_mode_propose` or
  `approval_always` for a write that waits (`waiting_in_chat` when the bot asked in the
  chat), `chat_confirmed`, `chat_cancelled` or `hold_expired` for its end, `resource_not_allowed` or
  `resource_not_in_agent_list`, `select_connection`, `act_read_only`, or
  `upstream_<status>` when the service behind the add-on refused the call.
- `status` is the HTTP status that service answered, when the add-on says so.

The line holds names and codes. It never holds the arguments, the result, a token or the
text of a note. An identical line is written once in 30 seconds; the next one says
`repeated=<n>` for the ones it held back.

The same event is kept in memory, at most 20 for each connection and each bot, and the
connection page shows it under **Latest decisions** with the tool, the outcome, the reason
in the language of the page and the time. `GET /api/chats/connectors/{id}/access` returns
them as `tool_outcomes`. They reset when the server restarts.

### Confirming a held write in the chat

A write tool waits for a person when the write mode of its connection is **ask first**.
In the web UI the owner confirms it on the Add-ons page. In a messaging chat the host
now asks in the chat itself, when the bot has `proposals.confirm` enabled and at least one
sender is paired with it.

- **What is written.** The host writes one message in the chat and thread of the request:
  the title of the tool in plain words, the resource, and the facts the tool definition
  lists, then how to answer: `yes` or `no`, or `confirm CODE` or `cancel CODE` when
  several wait. It never writes the words of the model, a token or a raw JSON dump, and
  every fact is cut to 120 characters. A tool definition lists its facts with two optional
  fields: `title` (at most 60 characters) and `summaryFields` (at most 6 keys of the
  sample that the preview returns). A key that is not listed is never repeated in a chat.
- **Who may answer.** Only a sender who is paired with the bot and holds
  `proposals.confirm`. A sender on the allowlist who is not paired, a member of a listed
  channel, and anyone on a bot that accepts anyone get one sentence and nothing runs. The
  person who asked and the person who answers can differ; the audit line names both. The
  owner can still confirm or cancel it in the web UI.
- **What runs.** The call stored with the proposal, bound to its digest, exactly as
  proposed. A code works once. The answer must come from the chat and thread where it was
  asked. The result goes back as one short message: a link or number the tool returned
  when it is safe to show (an https address without credentials), or the plain reason it
  failed, such as a refusal of the service behind the add-on.
- **Limits.** At most 5 writes wait in the chats of one bot, at most 10 are asked in an
  hour, and a proposal expires after 5 minutes. The records survive a restart. Above a
  limit the write waits for the owner in the web UI and the model is told so.
- **Not in the chat.** A tool that declares `approval: "always"` still waits for a person
  at the web UI, whatever a chat says.
- **The model.** The result of the tool says the write waits for confirmation in the chat,
  with the code, so the model does not say it was done. The web chat shows status lines
  for waiting, confirmed, cancelled, expired and failed.

When the service behind an add-on refuses a call (a token without the permission, a name
that does not exist, a value it does not accept), the add-on tells the host the status and
one fixed sentence that names the fix. The model gets `code: upstream_refused`, the
`upstream` status and that sentence as `message`, and tells the person plainly instead of
reporting an outage. An add-on does this by returning `protocol.Upstream(status, hint,
err)` from its runtime (the 400 answer carries the `X-Addon-Upstream-Status` and
`X-Addon-Upstream-Hint` headers; a host that does not read them behaves as before). The
sentence must hold no token and no text the service sent back.

A write that waits for a person (the write mode of the connection is **propose**, or the
tool always asks) says so in one sentence. In the web UI the user confirms or cancels it
on the Add-ons page. In a messaging chat the model tells the sender that the change waits
for the owner, who confirms it in the web UI: a chat turn never confirms a change.

### Tools that replace a built-in tool

A chat has a few built-in tools that an add-on may replace: `bash`,
`web_search` and `web_fetch` (`contract.ReplaceableTools`). The file tools are
not on the list: they are the one file toolset of every chat. A tool
definition replaces one with `"shadows": "<name>"`:

```json
{ "name": "bash", "kind": "action", "shadows": "bash", "description": "...", "inputSchema": { "...": "..." } }
```

- **Checked at install.** The name must be on the list, a page-only tool
  cannot shadow, and one manifest shadows a name once. The tool must also look
  like the built-in it replaces, so the model calls it the same way and the
  user keeps the consent split of the built-in name:

  | Name | Kind | Must declare (and may require only) |
  |------|------|-------------------------------------|
  | `bash` | `action` | `command` (string) |
  | `web_search` | `read` | `query` (string) |
  | `web_fetch` | `read` | `url` (string) |

  Extra optional properties are allowed, and a shadowing tool names no
  `resourceId`. The tool adapter checks all of this again when it lists the
  tools: a tool that fails keeps its add-on name and replaces nothing.
- **In a chat.** When the add-on is installed, running and on for the chat, the
  model sees one tool under the built-in's name, served by the add-on, and the
  built-in one is hidden. The call still goes to the add-on under its own name
  (`addon__<id>__<tool>`), with the same checks and audit. An add-on that is
  off for the chat, stopped, not granted by the chat's agent or unavailable
  offers nothing, so the built-in tool serves again. The chat's **Tools** menu
  says so in one line ("Sandbox replaces bash but cannot serve this chat now
  ...: the built-in bash serves", or that the chat has no such tool when the
  built-in is off too), from `fallbacks` of `GET /api/chats/{id}/tools`. A
  tool without `shadows` keeps its add-on name.
- **Qualified identities.** The shared tool adapter formats and parses
  `addon__<id>__<tool>`. Both components must match
  `[a-z][a-z0-9-]{0,63}`. Model-callable names cannot exceed 64 characters.
  Execution-status binding rejects plain names, malformed names, and names
  whose add-on ID differs from the selected provider. The real adapter already
  supplies canonical names; this check also guards the binding boundary.
  Studio's unavailable stand-ins use the same component grammar but do not
  apply the model-callable total length limit. A long stand-in identifies an
  unavailable add-on; it does not make that name callable through the adapter.
- **Who served it.** A call the add-on served carries the card `tool_provider`
  with the add-on's ID, so the chat shows the provider on the tool line.
- **Several add-ons.** Only one add-on serves a name. An administrator picks it
  in Settings, **Tools** (runtime setting `chats_tool_providers`, a map from the
  tool to the add-on ID; the same map holds the choices for the built-in skills
  an add-on replaces, see [Skills]({{% relref "/addons/contract-reference/manifest#skills" %}})); with no choice, the add-on
  installed first serves (ties go by add-on ID, and an add-on installed before
  the host kept the time sorts last). A chosen add-on that is installed but
  cannot serve the chat (stopped, off for the chat, not granted by its agent)
  gives the built-in tool, not another add-on; a choice of an add-on that is
  not installed is ignored. With no choice, the oldest add-on that is running
  serves. Settings > Tools lists every installed add-on that replaces a name,
  running or not, oldest install first, with the install time of each, marks
  a stopped one **(stopped)** and the default as **Default: installed first**,
  and says "The add-on that replaces this tool is not running: the built-in
  tool serves" when none serves (`stopped` and `note` in
  `GET /api/chats/admin/tools`). The
  library entry and the installed view list the names an add-on replaces in
  `shadows`, and the install dialog warns when an installed add-on already
  replaces one: "This add-on replaces the built-in bash. Another add-on
  (Sandbox) already does. Only one can be active; you choose which in
  Settings > Tools. Until you choose, the one installed first serves." The
  install is still allowed.
- **Install time.** The owner records when an add-on was first installed
  (`installedAt`, RFC 3339 in UTC) in its lifecycle journal, next to the other
  install facts. An upgrade keeps it; removing the add-on and installing it
  again sets a new one. `GET /api/addons/apps` and the installed view of an
  administrator carry it, and **More details** shows it. An add-on installed
  before the host kept the time has none. **Distributed state: shared.** The
  time lives in the owner's journal, the one durable record of the install, and
  the frontends read it from the owner, so every frontend picks the same
  default. A choice in `chats_tool_providers` always wins over it.
- **Other consumers.** The OpenAI-compatible API, MCP and agents keep the
  add-on name; only chats act on `shadows`.

### Public API

- `POST /api/addons/dispatch` accepts a contract request. Supported lifecycle actions
  are `install`, `enable`, `disable`, and `uninstall`. `operation-status` is read-only.
- `POST /api/addons/relay` accepts `{request, kind, handle}`. Kinds are `status`,
  `progress`, `json`, `file`, and `asset`. Handles are not filesystem paths. For `file`
  the handle is the identifier of a private file result (see
  [Private file results]({{% relref "/addons/contract-reference/proposals-and-resources#private-file-results" %}})), never an addon output handle.
- Both routes require named authentication and the `addons` feature. Lifecycle
  dispatch additionally requires current administration rights. Cookie requests require
  same-origin fetch metadata; header-authenticated clients use their validated keys.
- `POST /api/addons/proposals`, `GET /api/addons/proposals/{id}`,
  `POST /api/addons/proposals/{id}/confirm`, `.../cancel`, `.../reconcile` and
  `.../resolve` store, read, apply, withdraw and resolve data or schema proposals. See
  [Proposals and confirmation]({{% relref "/addons/contract-reference/proposals-and-resources#proposals-and-confirmation" %}}).
- `GET /api/addons/resources/{resourceId}/proposals?state=unresolved` lists, for the owner
  role of the resource only, the proposals on it whose outcome is unresolved, whoever
  proposed them. See [Unknown outcomes and recovery]({{% relref "/addons/contract-reference/proposals-and-resources#unknown-outcomes-and-recovery" %}}).
- `GET /api/addons/artifacts/{id}` and `GET /api/addons/artifacts/{id}/download` read
  the state of, and download, a private file result of the caller. See
  [Private file results]({{% relref "/addons/contract-reference/proposals-and-resources#private-file-results" %}}).
- `GET /api/addons/apps`, `GET /api/addons/apps/{id}/icon?generation=N`,
  `POST /api/addons/apps/{id}/start` and `.../stop` list and control the addons as
  apps. See [Apps]({{% relref "/addons/contract-reference/apps-and-pages#apps" %}}).
- `GET`/`POST /api/addons/resources`, `.../resources/{resourceId}/grants`,
  `.../grants/{userId}`, `.../users`, `.../rename`, `.../delete`, `.../changes/{changeId}/undo`, `.../operations/{operationId}`
  and `/api/addons/conversations/{conversationId}/selection/{addonId}` list, create,
  share, rename and delete a person's resources and keep the resource a conversation
  works on. They are UI-only. See [UI-only resource management]({{% relref "/addons/contract-reference/proposals-and-resources#ui-only-resource-management" %}}).

Every failure response has the body `{"error":{"type","message"}}`. A denial or
unavailability has `type` `addon_error` and a `code`. An `outcome_unknown` response is
status `503` and also carries the original `operationId`. Keep that `operationId` after
an `outcome_unknown` response. Query
`operation-status`; do not create another mutation. The carrier never retries mutations.
`Manager.OperationStatus` binds the lookup to the user who started the operation and to
the current administrator. A later browser session or another key of the same user reads
it. Another user, or an unknown ID, gets status `404` with `type` `operation_not_found`,
which is not a refusal of the role.
It does not submit, repair, start processes, or write the journal. `completed` describes
durable lifecycle publication, not runtime health. `runtimeState: not-observed` makes
that distinction explicit. Ambiguous legacy actor bindings deny lookup.

Progress is coarse lifecycle-state SSE, not byte counts or percentage completion.
The stream sends an initial snapshot, changed snapshots, and a heartbeat snapshot.
It polls no faster than 250 milliseconds and rechecks authority on each poll.
It closes on a completed or failed operation. A lifecycle writer can delay observation;
disconnecting still cancels the waiting lookup. Install-byte progress requires later integration.

`RelayLimits` has finite startup defaults: 60 seconds, 16 concurrent streams, 64 MiB
for files, 8 MiB for assets, and 1 MiB for progress. Heartbeats default to five seconds.
Overrides cannot remove these bounds. Streams cancel on disconnect and reject excess bytes.
The receiving frontend applies the budget of the relay kind: `file` uses the file
limit, `asset` the asset limit, `progress` and `status` the progress limit, and `json`
at most 2 MiB. An unknown kind is denied on the frontend before the owner is contacted.
Only content type, attachment disposition, cache policy, and nosniff headers cross the carrier.
Browsers never receive an addon process address.

A denial that the owner makes before it writes the first response byte (an invalid kind,
missing admission or resolver, a refused resource, or a revoked credential) is returned
as a typed owner status: `403` for a denial and `503` for unavailability. The receiving
frontend decodes it, so every frontend reports the same error for the same decision.
Only an authorization, policy or request rejection is `403`. A runtime that is not
installed, enabled or serving, an owner that is busy or in recovery, an output that ends
before its first byte, and any unclassified owner error are `503`, never a denial. A
`503` does not prove that nothing ran: the owner can fail after it recorded or applied a
mutation. A frontend therefore reports a mutation that gets a `503`, or a transport
failure, as `outcome_unknown` with the original `operationId`, and never as plain
unavailability. The owner's own frontend reports it the same way as every other
frontend. When an error carries both a denial and an unavailable cause, `503` wins.
After the first byte the status cannot change; the owner then aborts the stream and the
frontend reports the stream as failed, never as a clean end.

### Resource integration remains closed by default

`Admission.Admit` encloses the complete private-output stream lifetime. A host-owned
resolver maps resource references and opaque output handles to trusted records. A missing
admission provider or resolver denies access, including access by installation administrators.
The owner installs the policy described below as both providers at bootstrap; a frontend
that forwards to the owner, or a host without the policy, keeps denying. File results are
held by the private artifact store described in [Private file results]({{% relref "/addons/contract-reference/proposals-and-resources#private-file-results" %}});
without it a read that returns a file output is denied whole and a `file` relay is denied.

### Principals, roles and grants

`core/services/addons/policy` is the owner-local authority for resource access. Its store
is a private directory (`policy/` below the addon root, file mode 0600, one holder at a
time) that is separate from replaceable packages, so it can be backed up and restored on
its own. A resource is an opaque host-issued ID bound to the trusted addon identity and
an immutable owner user ID; the addon, the model and a request never supply either.

| Role | May |
|---|---|
| Viewer | read, and download private outputs |
| Editor | everything a Viewer may, and mutate rows |
| Owner | everything an Editor may, and change schema, share and delete |

Grants cover whole resources, never tables or rows. Installation administration confers
no access to user data: the policy has no administrator role. Only the owner grants or
revokes, the owner role cannot be granted, and an action that is not classified is a
mutation that needs the owner. A missing resource and a resource the caller may not touch
return the same denial.

Named-user mode uses the immutable user ID and rejects anonymous and legacy synthetic
principals (such as the legacy shared API key). Explicit local-owner mode disables
sharing and warns that every unauthenticated caller shares one authority: any local-owner
caller owns every resource. A populated store opened in the other mode is refused.
`policy.MigrateMode` changes it only while the host is stopped (the store lock must be
free), after it wrote a backup, and shared data can never return to local-owner mode.

Account and credential status are checked again at each admission. A storage outage
of the account database is `503`, while a missing, disabled or unauthorized account is
`403`, from every frontend. Only a definite denial (an error that wraps the shared
forbidden sentinel) is `403`. Any other error from the account or creation check, and a
cancelled check, is unclassified and fails closed as `503`; it is never reported as a
refusal of a valid user. The policy keeps no
current-user state: the actor is an argument of every call, so shared MCP sessions carry
none. Writes to one resource run one at a time; reads run concurrently. A revocation (or
lowering a role) is durable first, then invalidates the user's pending proposals through
the `ProposalInvalidator` seam, cancels the user's admitted work and returns only when
that work ended. If it does not end within the drain bound the call reports `503`
(`ErrDrainPending`): the revocation is already effective for new work but is not
acknowledged. The owed drain is stored with the change itself, together with the
resource's authority epoch at the lowering. Every admission carries the epoch it was
admitted under. Repeating the same `SetGrant` or `Revoke` after a failed attempt (or after
a restart) invalidates proposals again and waits only for the user's work admitted before
that lowering; it is acknowledged only after both finished, and the mark is then cleared.
Work admitted after the grant became durable again is neither cancelled nor waited on by
the retry. A later lowering starts a new epoch and covers it. Disabling or uninstalling an addon keeps its resources and grants; purge is
a separate, explicit operation that is not available to tools.

A new resource needs explicit creation authority. At bootstrap that authority requires a
current account check and an addon that is installed, enabled and not changing on the
owner; an absent addon is denied and a manager that cannot answer is `503`. `Register`
records the resource pending and inaccessible before the addon creates anything; `Commit`
activates it, and `Reconcile` resolves registrations left by a crash. The owner runs
`Reconcile` at startup before it serves. It asks the add-on whether the backing exists:
`yes` activates the record, `no` drops it. Reconcile is fail-soft per record. When the
answer cannot be established (the add-on is disabled, failed to start or does not
answer), that record stays pending and hidden, is never dropped or committed, keeps its
pending quota, and the pass goes on with the other records. Startup never aborts for this,
and the owner logs a warning with the settled and kept counts. A background loop re-runs
Reconcile every 30 seconds while any record is pending, so a record is settled once its
add-on is enabled and ready. The loop stops when the owner closes and waits for a running
pass. Pending reservations of a disabled add-on therefore stay pending until the add-on is
enabled again. A database with no record is never reachable.

Registrations are bounded. Per owner user at most 8 pending and 256 total resources, and
at most 4096 in the store (`Options.MaxPendingPerUser`, `MaxResourcesPerUser`,
`MaxResources`; zero selects these finite defaults). A request over a limit is denied
with `403` (`ErrQuota`); abandoning or reconciling away a registration frees its slot.

The store file is validated on open: an unknown role, a grant to the owner, a second JSON
document after the first, or an unknown field refuses the store, which is never reset.
Leftover `.policy-*` temporary files from a crash are removed on open. If a change was
renamed into place but the directory synchronization failed, the in-memory state follows
the file (a revocation is not undone) and the store accepts no further writes, reporting
`503`, until the host restarts.

### Explicit local-owner mode

Set `--addons-ownership-mode=local-owner` (or `AddonConfig.OwnershipMode` in an embedding
host) to select this mode. `local-ai run` also selects it by default when authentication
is off and you set no mode. Nothing else selects it: a missing credential, an absent
account database or an unknown value never does, and an unknown value fails startup.
If you turn authentication on later, set the mode to `named-user` and move the data
with `policy.MigrateMode` while stopped; the store refuses the other mode. The owner logs a warning at startup that unauthenticated
callers share one authority and that any caller that can reach the port can confirm
addon proposals.

- Every caller that passes the addon route chain acts as one synthetic principal,
  `local-owner`, with a fixed credential `local-owner`. Named-user mode rejects that ID,
  so it cannot collide with a real user.
- That principal owns every resource and sharing is disabled: `SetGrant` and `Revoke`
  are denied.
- With the authentication feature disabled (no account database) there is no caller
  identity at all: any client that can reach the HTTP port shares the authority, including
  lifecycle administration. Use this only on a trusted loopback or private network.
- With an account database, the route chain still authenticates callers and applies the
  addons feature gate, and lifecycle changes still need an administrator; only data
  ownership collapses to the single local owner.
- The store records the mode. Opening a populated store in the other mode is refused;
  use `policy.MigrateMode` while stopped. Shared data cannot return to this mode.

#### Moving between the modes

The mode is a property of the stored data, so it is changed with the host stopped and
never by changing the configuration alone:

1. Stop the owner. The migration needs the store lock, which a running owner holds.
2. Make a backup of the policy store. The migration writes one itself and refuses to
   continue when it cannot.
3. Name the mapping of identities. From `local-owner` to named users, every resource
   needs the account that takes it (`{"local-owner": "<account id>"}`; there is no
   wildcard and no default, and the target must be a current account). From named
   users to `local-owner`, a store that holds named-user data is refused, because
   that data would lose its separation.
4. Start the owner in the new mode. A store opened in the wrong mode is refused at
   startup, so a configuration mistake never silently changes who owns data.

A backup made in one mode restores into the other mode only with the same explicit
mapping (see [Data recovery]({{% relref "/addons/manage-addons#data-recovery" %}})).

Resource reads obtain the current ready runtime incarnation and check the exact generation.
Private output copying rechecks credentials and stays inside admission until completion
or cancellation. Immutable assets use installed package receipts, declared entry/CSS paths,
and a contained filesystem root; an account that holds the addons feature may read the
declared files of an enabled add-on it may see (see [The asset route]({{% relref "/addons/contract-reference/apps-and-pages#the-asset-route" %}})).

Mutation MCP tools remain deliberately unavailable. The later MCP integration defines
safe listing/status tools for proposals; prompt text cannot authorize mutation and no tool
confirms a proposal. For that reason `/api/addons/dispatch`, `/api/addons/relay` and the
proposal routes are deliberately not mapped in the LocalAI Assistant tool catalog
(`core/services/localaitools`) yet. The route map test only
requires an entry for each registered tool, so no test is weakened. The routes are described
in the generated Swagger document under the `Addons` tag.

Run `sh scripts/addons/verify.sh policy` for the policy suite.
