+++
title = "Host operations, settings and providers"
description = "Action tools, host operations, user settings, provider slots and model search sources."
weight = 60
+++

Look up the action tools, the host operations an add-on can call, the user settings, the provider slots and the model search sources. This page is part of the [Add-on contract reference]({{% relref "/addons/contract-reference" %}}).

## Action tools

A tool of kind `action` does what a person asked a chat to do, at once, as that
person, and in that person's own space. It is not confirmed in the browser and
it is not a proposal. The sandbox add-on, which runs a command, and the office
add-on, which edits a file the user owns, use it. An `action` tool is not for
data of a shared resource: a change that others see is a `write`, `schema` or
`create` tool, which the OpenAI-compatible route leaves as a proposal for a
person to confirm.

- **Declaration.** The tool is in `capabilities.tools` with a definition of kind
  `action`, and the manifest sets `capabilities.actions` to `true`. The host
  refuses a manifest that declares an action tool without it. An action tool
  names no resource: if its schema has a `resourceId`, the host ignores the
  value the model gives and sets it to the ID of the conversation.
- **Who may call it.** Only the chats consumer (`studio-chats-addons` in the
  consumer table of [Tables]({{% relref "/addons/catalogue/work/tables" %}})). Every other consumer neither lists the
  tool nor runs it: the old chat route, the direct API, agents and scripts.
- **The call.** `POST /act` with an envelope whose action and scope are `act`.
  The actor is the user of the chat. The answer is a `Result`. The call may run
  up to `ActTimeout` (30 minutes), is never retried by the host, and an add-on
  that dies during it gives the caller `outcome_unknown`. The request carries the
  chat's conversation ID in `resourceId`. The host sets it from the chat, the
  model cannot name it, and an add-on uses it only to keep state for that chat,
  never as authority.
- **Undo.** What a tool does to the user's files is a change in the history of
  their workspace, and it can be undone there.
- **Runs with nobody present.** A trigger or an agent job calls the tools of an
  add-on under the action policy of unattended runs. The `kind` of the tool
  decides the class: `read` always runs, and every other kind is refused unless
  the run holds `act.addons` (or `act.addons:<add-on id>`). A tool that the host
  gates with a permission, because the add-on holds a credential, also needs
  `act.spend`. Declare `read` only for a tool that changes nothing. See
  [Action policy of unattended runs]({{% relref "/features/chats-and-agents/chats-agents" %}}#action-policy-of-unattended-runs).

## Host operations

### Required and optional operation admission

A verified manifest can request exact operation names with
`capabilities.hostOperations.required` and `.optional`. These are requests,
**not grants**. The owner compares them with the host operation vocabulary
and the immutable host policy selected at startup. A manifest without
requests keeps its configured behavior.

Coverage uses the broker's literal, nonempty prefix rule. For example,
`workspace.` covers the requested name `workspace.materialize`;
`workspace.materialize.narrow.` does not. Registry membership is an exact-name
check: a grant cannot make `workspace.future` a supported operation. Requests
are exact names, not prefixes, patterns, or authority for selecting providers.

The owner verifies and stages a new install or upgrade before admission.
Missing required requests refuse publication, before installed records,
migrations, runtime withdrawal, or process creation. A refused upgrade retains
the old receipt, digest, generation, and running incarnation; it needs no
rollback. Automatic upgrades suppress further attempts for the same failed
candidate until a different candidate appears or the owner restarts. They never
acquire grants or prompt for approval.

After an operator removes required grants and restarts, the package remains
installed with its enabled intent. Its effective runtime is unavailable
(`failed` in Apps), and the owner admits no launch, tool, resource, connector,
or page access. It does not offer installation as a repair. Restore the host
policy to make it available again. The policy file and flags are read at startup,
and a change of them needs a restart. Grants and deny rules that an administrator
sets in the UI apply live: a revoke or a deny stops an add-on that loses a
required operation, and a grant or a lifted deny starts one that was refused,
without a restart. Subsequent manager `SetHostOps` calls do not replace the
startup decision.

Missing optional requests permit a degraded start. Existing admin Apps details
and lifecycle operation results include bounded required, optional, granted,
and missing name lists with counts. Here “granted” counts only supported
**requested** names covered by host policy, not every grant the addon holds.
Ordinary Apps readers receive the generic failed observation without policy
paths, grant details, or missing operation names. Split deployments carry the
owner's decision; a frontend cannot supply grants.

Admission does not authorize an individual call. The broker still requires a
registered handler and rechecks the live incarnation, operation scope, actor
features/admin status, workspace/chat access, and connector binding. Handlers
are wired as host services start; admission uses the host's startup operation
vocabulary rather than treating not-yet-wired handlers as unknown operations.


A host operation is how an add-on process asks the host to do something the
add-on cannot do itself, such as starting a job of the engine. It is contract
surface that third-party add-ons cannot use unless the host setting names them.
An add-on that serves paired clients can also ask `client.list` for the clients
that may reach it (see [Paired clients]({{% relref "/addons/addon-clients" %}})). When a client call serves a client with a folder grant, `client.workspace.list`, `.read` and `.write` give it text files of the owner (same page).

The rules:

- The host lists every operation in its code. No add-on gets an operation that
  is not listed, and an operation that is not registered is refused. The
  default is refuse.
- The host setting `--addon-host-ops` (`LOCALAI_ADDON_HOST_OPS`) names the
  add-ons that get the channel and the operation prefixes each may call, as a
  comma list of `addon-id=op-prefix`. The generic binary grants nothing when
  unconfigured. An explicitly empty value names nobody, including in Docker.
  An administrator can instead select a file with `--addon-host-policy`; see
  [Administrator host operation policy]({{% relref "/addons/contract-reference/host-policy#administrator-host-operation-policy" %}})
  for precedence and explicit-empty behavior.
  This is a host setting, not a manifest field and not a model argument.
- An add-on that the setting names finds two optional fields in its startup
  object: `hostEndpoint`, a loopback address of the form
  `http://127.0.0.1:<port>`, and `hostCredential`, a secret of its own that is
  never the credential of the host-to-add-on channel. Both belong to one
  incarnation and end with it. An add-on that is not named gets neither field.
  The protocol version stays 1, and an add-on that ignores the fields works as
  before.
- A call is `POST` with the header `X-Addon-Host-Credential` and a JSON object
  `{"operationId", "op", "arguments"}`. `operationId` is the ID of a request
  the host is serving for that add-on at that moment. The host looks up the
  envelope it issued for that request. A call with no live request, with an
  expired authority or with a scope below the scope of the operation is
  refused. An `apply` operation needs an `apply` request; a `read` operation
  runs under a `read`, `preview` or `apply` request.
- The operation runs as the actor of that envelope, never as a user named in
  the arguments. The host refuses an argument with a name of identity or
  authority (for example `userId`, `scope` or `approved`).
- The host checks the feature of the operation for that actor, as the route
  middleware does for the same request. In local-owner mode the actor is the
  local owner and holds every feature.
- A body larger than the payload limit is refused. An operation that is marked
  idempotent runs once for one operation ID; a repeat gets the first result.
- A process that does not run the add-on manager offers no endpoint, so the
  operation fails closed.

The answer is `{"result": ...}` or `{"error": {"code", "message"}}`. The error
codes are `no_live_operation`, `scope`, `feature`, `unknown_op`, `bad_request`,
`too_large`, `not_found`, `conflict` and `unavailable`.

The channel itself ships with no operation. Each operation is added by its own
change and documented with it.

### Inbound operations

One class of operation has no user behind it: a message that comes from outside,
such as a Telegram message pushed by an add-on. Its scope is `inbound`. The rules
above change in these ways, and only for operations the host registers with that
scope (today `connector.inbound`):

- No live request is needed. The authority is the credential of the add-on's live
  incarnation, which only that process holds and which ends with it. A missing,
  wrong or old credential is refused with `no_live_operation` (401), and so is the
  credential of another add-on, because each add-on has its own listener.
- The call has no actor and no feature check. The operation must find its
  authority in the host's own records. For `connector.inbound` that is the stored
  binding: it must name the calling add-on and be switched on, or the call is
  refused with `scope` (403), also when the binding does not exist. The owner and
  the agent come from the binding. An argument that names a user, an actor or an
  owner is refused with `bad_request` before the operation runs.
- The add-on ID is the one the host built the listener for, never a value of the
  call. The host setting must still name the operation prefix for the add-on
  (`telegram=connector.` in the stock Docker policy).

Every other operation keeps all its checks. An operation cannot become inbound by
a field of the request: the scope is part of its registration.

### How the pieces fit

The owner process holds everything about host grants. A frontend stores nothing and enforces nothing, and it forwards the host access routes to the owner.

- **Files.** The owner writes `host-grants.json` and the audit file `host-grants-audit.jsonl` in its add-on state directory, next to `host-settings.json`. A missing document is the first start: it is created at the first write. A document that cannot be read does not stop the owner, but then no add-on can call any host operation, because the deny rules inside it are unknown. The startup log says so. The audit file records who changed what, never a policy path or a secret.
- **The check on each call.** The broker asks one Authority for every operation. It combines the floor (policy file and flags, read once at startup), the deny rules of the file, the flags and the UI, and the grants of the UI document. A deny always wins. A UI grant counts only for the package it was made for, and only after the install or upgrade that created it completed. A revoke or a deny applies to the next call, with no restart. A call that already runs finishes.
- **At install and upgrade.** The routes under `/api/addons/host-grants` stage the package, and the owner grants only what the administrator approved, the verified manifest declares and the host registers. An upgrade adds its operations next to the old ones. If the upgrade does not complete, only the added operations are removed, and the running version keeps what it had.
- **Startup log.** The owner logs where the floor came from (flag, environment or file), how many add-ons, prefixes and deny rules it holds, how many add-ons and operations the UI document holds, the blanket mode and whether UI grants are locked.

## User settings

An add-on can declare choices that each person makes for it, such as how it behaves
for them. The add-on names the key and the values it understands in its manifest.
The host keeps the person's answer, checks every write against the declaration and
reads the answer for its own rules. The add-on does not store it and is not called.

```json
{
  "capabilities": {
    "userSettings": [
      { "key": "save-mode", "values": ["ask", "auto", "never"], "default": "ask" }
    ]
  }
}
```

A key is lowercase letters, digits and dashes, at most 40, and starts with a letter.
A setting has 2 to 8 values of the same form, and a default that is one of them. An
add-on declares at most 8 settings. A person who has not chosen has the default. The
manifest fixes the allowed values and nothing else: the words on the page and the
help text are the add-on's own.

A setting can also carry a `role`, when the host reads the answer for one of its own
rules. The host defines the values and the default of each role, and the manifest is
refused when it differs, so an add-on cannot make a host rule more permissive than the
host's own default. The only role today is `memory.save-mode`, with the values `ask`,
`auto` and `never` and the default `ask`. One add-on declares a role at most once.

| Route | Does |
|---|---|
| `GET /api/addons/{id}/user-settings` | the settings of the add-on for the caller, each with its values, default and the value in force |
| `PUT /api/addons/{id}/user-settings` | set one: `{"key": "...", "value": "..."}` |

Rules:

- A write needs the person's own browser session: a same-origin fetch with the
  `X-LocalAI-Confirm` header set to the add-on ID. An API key, a bearer token, an MCP
  session or a tool call is refused with `403 not_human_surface`. No MCP tool maps to
  these routes, so a model cannot change a setting that limits what it may do.
- An unknown add-on, or one the caller cannot see, is `404`. A key that the manifest
  does not declare, or a value it does not allow, is `400` and nothing is stored. A
  stored value that a newer manifest no longer allows reads as the default.
- Authority: per person. The answers live in the account database that every frontend
  shares, so a save through one frontend is read through another. Without accounts
  they are one file under the data path for the one local owner. The store never falls
  back to a local copy when the database is down: the routes answer `503`, and a host
  rule that reads a setting uses the default. A write replaces one key and has the same
  result when repeated, so a retry after an uncertain write is safe; the last write
  wins.
- A page reaches its settings only through `sdk.userSettings`, which is bound to the
  add-on. `sdk.core.fetch` still refuses `/api/addons`.
- A setting is a person's choice. It never widens what a connector chat or an
  unattended run may do; a host rule that reads one says so (see
  [How a memory note is saved]({{% relref "/addons/contract-reference/connectors-and-chats#how-a-memory-note-is-saved" %}})).

## Provider slots

A provider slot is a job the host does itself and lets an add-on do in its place. The
host owns the slot, its gates and a built-in provider. An add-on says in its manifest
that it can serve the slot, and each person picks one provider: the built-in one, an
add-on that declares the slot, or Off. Memory (the notes the assistant reads and saves)
is the first slot.

The list of slots is data in the host (`pkg/addons/contract/provider-slots.json`) and it
is closed. An add-on cannot invent a slot: a new slot comes with a host release, with
one descriptor entry and its built-in provider. A descriptor gives the slot an ID, a
version, a title and a description, the choice a person has (the default, whether Off
is allowed and what Off means), the built-in provider, the **roles** a provider can
fill, which declarations the slot activates, the facts a provider may declare about
itself, and the ids of the host rules that apply to every provider.

### Declare a slot in a manifest

An add-on declares at most four slots under `capabilities.provides`. The manifest stays
at `api` 2: `provides` is an optional key.

```json
{
  "capabilities": {
    "tools": ["recall", "remember"],
    "toolDefinitions": [
      {"name": "recall", "kind": "read", "description": "...", "inputSchema": {"type": "object", "properties": {"query": {"type": "string"}}, "additionalProperties": false}},
      {"name": "remember", "kind": "write", "description": "...", "inputSchema": {"type": "object", "properties": {"resourceId": {"type": "string"}, "text": {"type": "string"}}, "required": ["resourceId"], "additionalProperties": false}}
    ],
    "provides": [
      {
        "slot": "memory",
        "slotVersion": 1,
        "roles": {"read": "recall", "write": ["remember"]},
        "properties": {"remote": true, "undo": "none"}
      }
    ]
  }
}
```

- `slot` is a slot of the host. An unknown slot refuses the manifest.
- `slotVersion` is the version of the slot's contract that the add-on was written for.
  The host accepts the range from the slot's `minVersion` to its current version.
- `roles` map a role of the slot to tools of `capabilities.tools`, as one name or a
  list. The host checks each tool against the role: it must be declared with a
  definition, have an allowed kind, not be page-only, not shadow a built-in tool, and
  serve one role only. A role takes at most the number of tools its descriptor allows.
  A role the descriptor marks required must be present. A provider never brings an
  `action` tool through a role.
- `properties` are typed facts the descriptor asks for, such as `remote` (a boolean)
  and `undo` (`host`, `provider` or `none`). `undo: "provider"` needs
  `capabilities.undo`. The interface shows them to the person who picks the provider.
- The title and the description in the list of providers are the add-on's `ui.title`
  and `ui.description`.

An add-on with no `roles` is valid. A provider that only ships index files and skills
declares the slot and its `contextFiles` and `skillDefinitions`, and needs no tool.

### What a pick changes

A declaration does nothing until a person picks the add-on. While another provider
serves, the add-on's context files, its skills and the tools of its roles are not
offered in the chats of that person. Its other tools are not touched. When the person
picks it, those contributions count, and the built-in provider stops serving: one
provider serves a slot at a time. The host keeps every gate for any provider, so an
add-on cannot loosen a save rule, a grant or Off. The `contextFiles` of a provider must
lie in the scopes that the slot allows.

### When the provider fails

If the add-on a person picked stops, is disabled, is removed or cannot be listed, the
slot is **off** for that person, with the reason `provider_unavailable`, and the person
sees which add-on it is. It does not fall back to the built-in provider: a person who
chose an external provider must not have their data written somewhere else without
being asked. The choice stays stored, so the add-on serves again when it runs, and the
person can pick the built-in provider at any time. A store that cannot be read, or an
add-on list that cannot be read, also turns the slot off for that call.

### What the host does itself with a provider

The model reaches the tools of the `read` and `write` roles as ordinary tools. The host
calls the other roles itself, and it keeps every gate while it does.

| Role | When the host calls it | What the host does with the answer |
|---|---|---|
| `context` | At the start of each turn of a chat that reads the notes | Puts the text in front of the message, wrapped as data, for this turn only |
| `export` | When the person downloads their notes | Writes the answer as a zip file |
| `forget` | When the person asks the provider to forget notes | Reports the result, or the change that waits for a confirmation |
| `write` | When the model calls the tool | Applies the save level of the person first (see below) |

**Context.** The host calls the tool with the facts its schema declares, and no others:
`query` (the message of the person, cut at 1 KiB), `scopes` (the scopes the chat may
read), `budgetBytes`, `origin` (`studio` or `connector`) and the opaque references
`userRef`, `agentRef` and `conversationRef`. A provider never gets a name, an instruction
or a platform ID. The tool answers `{"text": "..."}` or a JSON string. The call has a
time limit of 3 seconds, and the text is cut at 6 KiB. The host puts the text in front of
the message inside the markers `<<<provider-data` and `provider-data>>>` with a header that
says it is data and not instructions. A marker or a line that looks like a system note is
defused inside the text. The text is not part of the system prompt, so a text that
changes with each message does not restart the chat engine. A chat that has memory off,
a private chat and an agent with no notes make no call at all.

**Export and forget.** The export tool takes no argument and answers
`{"files": [{"path": "a/notes.md", "content": "..."}]}`; any other JSON is kept as one
file, `export.json`. A path must be relative and is made safe by the host. The forget
tool is a `write` tool, so it names a resource like every write tool. The host offers the
facts `group` (`user`, `agent` or `channel`), `key` and `resourceId` to the tools that
declare them. A provider with no `export` or no `forget` role has none: the page says so,
and the person uses the service itself. The built-in provider has no forget; its notes
are files that the person deletes in Files. `GET /api/chats/memory/export` and
`DELETE /api/chats/memory` route to the provider that serves.

**Save path.** A write through a provider follows the same rules as a note of the
built-in provider. The level of the person is the strictest of their setting, the rule of
the saved agent and the messaging row. *Never* takes the write tools away. A saved agent
needs the `memory.write` grant, and a sender in a messaging chat needs the capability.
Only *Save it and tell me* applies a write at once, and only for a note of up to 4 KiB
that does not look like a secret, within 5 writes in a turn, and from a provider that is
local or declares `undo` as `host` or `provider`. Every other write is stored as a
proposal and waits for the person; the model is told so and does not say it was saved.

**When the provider fails.** The chat never fails because a provider does. A context call
that times out or fails leaves the turn without notes, and the model hears one line
saying so. After 3 failures in a row the host stops calling the provider for one minute (a
circuit breaker), then tries one call. The slot shows a plain notice in `health` while it
waits. The breaker is per server process: each frontend learns on its own.

**Audit.** Every call of the host to a provider writes one log line,
`studio: provider call`, with the slot, the role, the add-on, the tool, the person, the
chat, the outcome (`ok`, `truncated`, `failed`, `timeout`, `bad_result` or `cool_down`),
the time and the size. Every write adds `studio: provider write` with the level and
whether it was held. No line has a note or any text of the provider.

**A provider that sends notes away.** A provider that declares `remote: true` must also
declare `capabilities.network`, so the person and the host see the network access before
anything is sent. A manifest that does not is refused.

### The retired Memory add-on

The notes are built in, and the add-on `memory` that used to read them is an alias of the
built-in provider. Its package is retired: it is no longer in the library or built by the
package workflow, so it is not offered for a new install. A copy that is still installed keeps working: the host does not list it
as a provider, a choice that names it means the built-in provider, and its index files
and skills are ignored so the notes do not load twice. Nothing is deleted. A new
provider must use another ID.

### Write a provider

1. Start from `pkg/addons/providerconformance/sample`: a manifest that declares every
   role, and a small service behind it. The sample keeps the notes in a service outside
   this machine, so it declares `remote: true` and the network.
2. Give the tools the shapes above. A tool of the `context` role needs no resource. Do not
   declare a property that the contract reserves, such as `scope`.
3. Run the conformance test from a Go test of your add-on:

```go
func TestMyProviderConforms(t *testing.T) {
    providerconformance.Run(t, providerconformance.Subject{Manifest: manifestJSON, Call: myHandler})
}
```

It checks the manifest against the descriptor, then calls the role tools the way the host
does: the context within its byte budget and with an empty message, the export with safe
paths, the read and forget tools, and a tool that does not exist. A provider that passes
it works with the time limit, the size cap and the wrapping of the host.

### Who can see a source

The sources are registered once for the whole server, and each person sees the ones their add-on visibility allows. One person who cannot see an add-on never removes its source for another person: a source is unregistered only when nobody sees it any more. The panel and the search use the list of the person who asks.

The network access of a source is the add-on's own. An add-on that makes HTTP calls from its own process, as the Web add-on does, declares no network capability: the `network` capability and the `egress.check` operation are for add-ons that run commands behind the host's proxy, such as Sandbox.

### Routes

| Route | Does |
|---|---|
| `GET /api/chats/providers` | every slot with its providers and the choice in force |
| `GET /api/chats/providers/{slot}` | one slot; 404 for an unknown slot |
| `PUT /api/chats/providers/{slot}` | pick a provider: `{"provider": "builtin" \| "off" \| "<add-on id>"}`; 400 for a provider the slot does not list, 409 for an add-on that cannot serve now |
| `GET /api/chats/memory/export` | the notes as a zip file; the export of the provider when an add-on serves |
| `DELETE /api/chats/memory` | ask the provider that serves to forget notes (`scope`, `key`, `resource`); 404 when it has none |

`resolution.active` says what serves: `builtin`, an add-on ID or `off`. `health` is present
while the host leaves a failing provider alone, with a plain `notice`. The choice is
kept per person with the account, so every frontend of a cluster reads the same answer.
It is a person's choice and needs no add-on call. The settings page draws the list from
this answer, and **Settings** then **Memory** is the first place that uses it.

### Test a provider add-on

Test a provider with a fake add-on: declare the slot, serve a context file, list the
add-on through the broker interface of the tests, then check that the add-on's files
reach the prompt only after the person picks it, that the built-in notes do not load
at the same time, and that a stopped or removed add-on turns the slot off instead of
falling back. `core/services/studio/providerslots_internal_test.go` is the reference, and
`core/services/studio/providercalls_internal_test.go` covers the host's own calls.
`pkg/addons/contract/providerslots_test.go` covers the manifest rules.

## Model search sources

The slot `model-search` lets an add-on add a **source** to the model search panel (the
Explore tab of the Models page) and to the search tool of the assistant. The add-on gives
data and typed filters only. It ships no UI, markup, script or style: the host draws the
rows and the controls, so a source from an add-on looks like the built-in one. A result
must install through a scheme that the host already handles. An add-on cannot add a
download scheme.

This slot has a **set** selection (`selection.mode: "set"` in `provider-slots.json`; the
other slots are pick-one). A person can have any number of sources on. The built-in source is
Hugging Face (`huggingface`). Every source the person can see is on by default, built in or
from an add-on, running or not: the host stores only the sources the person turned off, so a
source from a newly installed add-on is searched with no action. All enabled sources are
searched at the same time and the answers are merged. An empty set means no source is
searched: the slot has no Off value.

### Declare a source

An add-on declares the slot in `capabilities.provides` (role `search`, one tool of kind
`read`) and describes its source in `capabilities.modelSearch`. A manifest that uses
`modelSearch` must declare `featureLevel: 9`.

```json
{
  "featureLevel": 9,
  "capabilities": {
    "tools": ["search"],
    "toolDefinitions": [{"name": "search", "kind": "read", "description": "...", "inputSchema": {"type": "object"}}],
    "provides": [{"slot": "model-search", "slotVersion": 1, "roles": {"search": "search"}}],
    "modelSearch": {
      "source": {"id": "example", "title": "Example", "icon": "cloud", "listsLatest": false},
      "sorts": ["downloads", "likes"],
      "filters": [
        {"id": "kind", "type": "enum", "label": "Kind", "values": ["gguf", "any"], "default": "gguf"}
      ]
    }
  }
}
```

| Field | Meaning |
|---|---|
| `source.id` | The provider ID. Results, the stored choice and the `providers` parameter use it. It never changes once published. `builtin` and `off` are not allowed. Two add-ons that declare the same ID: the second is refused when it registers, and the host writes an audit line. |
| `source.title` | The name shown on the chip and on each row. |
| `source.icon` | An icon name of the host. An unknown name draws none. |
| `source.listsLatest` | The source answers with no search text when the sort is `latest`. |
| `source.hasVariants` | Opening a result of this source is likely to list several quantizations. The host sets `has_variants` on each row from this property, never from the row. |
| `source.p2p` | The models come by torrent. The panel puts the small p2p tag on the chip. |
| `sorts` | At most 5 of `downloads`, `likes`, `updated`, `trending`, `latest`. |
| `filters` | At most 8. Each has `id`, `type`, `label`, optional `help`. Types: `enum` (`values`, at most 32, and a `default` that is one of them), `bool`, `range` (`min`, `max`, `step`, `unit`) and `text` (`maxLength`, at most 200). The IDs `q`, `limit` and `sort` are reserved. |
| `i18n.<language>.modelSearch` | Translates the source `title` and, by filter ID, the `label`, `help` and the labels of enum `values`. English stays the fallback. |

### The call

The host calls the `search` tool with:

```json
{"q": "qwen", "limit": 20, "sort": "downloads", "format": "gguf", "filters": {"kind": "gguf"}}
```

`filters` holds only the filters of this source, typed. A filter that declares a `default` and that the request leaves out is sent with that default, so the value the panel shows and the value the add-on gets are the same. The values are: a string for an enum or a text,
a boolean, a number for a range. The tool answers with `{"results": [...]}`. A row has the
fields `id`, `title`, `uri`, `magnet`, `url`, `size_bytes`, `downloads`, `likes`,
`seeders`, `tags`, `pipeline_tag`, `license`, `gated` and `last_modified`. The host checks
every row (`search-result-validation`):

- `uri` must start with a scheme that the importer accepts: `huggingface://`,
  `pirateface://`, `magnet:` or `https://`. A row that fails is dropped.
- `id` is at most 200 bytes, `title` 300, `url` and `magnet` 2048, and a row has at most
  32 tags of at most 64 bytes. A row over a cap is dropped.
- At most `MaxLimit` (50) rows are read.
- The host sets `provider` to the source ID, whatever the row says. It computes
  `installed`, `installed_name`, `backend` and `has_variants` itself and discards the
  values of the add-on.

Dropped rows are counted in the `dropped` field of the status of the source. An error, a
timeout (15 seconds) or an answer that is not valid JSON gives a failed status for that
source only. Text from a source is untrusted when it reaches the assistant: the `search_models` tool puts the rows and the failure texts of add-on sources in one field, `untrusted_sources`, fenced as untrusted text, and keeps them out of the plain fields.

### Routes

| Route | Does |
|---|---|
| `GET /api/models/search` | Searches the enabled set of the caller. `providers=a,b` limits the sources. `filters.<source>.<id>=value` sets a filter of one source; an unknown filter or a value that does not fit its type gives a 400 that names the filter. The answer has `results`, `providers` (each with `title`, `ok`, `count`, `dropped`) and `sources` (title, icon, sorts and the filter schema of each source). |
| `GET /api/models/search/sources` | The sources that are installed, and the enabled set of the caller: every visible source minus the ones the caller turned off. `missing` is always empty and stays for older clients. |
| `PUT /api/models/search/sources` | `{"enabled": ["huggingface", "<source id>"]}`. The host stores the visible sources that are not in the list as the ones turned off. An ID that is off and has no installed add-on stays off, so installing the add-on again keeps the choice. |

The `enabled` list has at most as many IDs as the visible sources plus the stored off IDs plus the built-in source, and never more than 64; a longer list gives a 400. The list of sources turned off is kept per person with the other per-person add-on choices (key `provider-model-search-off`), so every
frontend of a cluster reads the same answer. Changing it needs no restart. The first version of the feature stored the enabled set under `provider-model-search`; that key is no longer read.

### Test a source

`pkg/modelsearch` has a fake broker that stands in for the add-ons. Use it to test the
fan-out, the row checks and the filter checks. `pkg/addons/contract/modelsearch_test.go`
covers the manifest rules.
