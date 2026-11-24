+++
title = "Manifest extensions"
description = "The optional typed extensions of the add-on manifest."
weight = 20
+++

Look up the optional typed extensions that an add-on manifest can declare. This page is part of the [Add-on contract reference]({{% relref "/addons/contract-reference" %}}).

## Optional typed manifest extensions

These declarations extend the strict manifest contract. They keep
`schemaVersion: 1`, host/protocol/data-format level 1 and UI SDK 2. The manifest
`api` range is exactly 2 (`"api": {"min": 2, "max": 2}`). A manifest with another range, or with none, is refused at install.
The Go contract parser is authoritative; there is no separate JSON Schema.
The fragments below merge into an otherwise valid manifest, not a complete
installable package. The executable, identity, compatibility and UI
entry/SDK fields remain required.

### Home page commands

Inside `capabilities.ui`:

```json
{
  "pages": ["studio"],
  "navigation": ["studio"],
  "mainPage": "studio",
  "title": {"en": "Studio", "it": "Studio"},
  "icon": "ui/icon.png",
  "homeCommands": [{"id": "open", "alias": "studio"}]
}
```

`homeCommands` is opt-in and has at most 8 records. Each has a unique `id`
accepted by `contract.ValidName`. Optional `alias` uses the same lower-case,
slash-free identifier syntax; it requests convenience naming, not ownership.
Optional `page` must belong to `pages`, even if not in `navigation`. Omission
uses a nonempty, declared `mainPage`, never the first page or an external URL.

Optional `label` is a `Localized` string or language map (not a `default`
wrapper), with at most 60 Unicode characters per label and the existing
plain-text and language rules. Omission selects the installed localized title
and, for a non-main destination, the page identifier. Optional `icon` is an
allowlisted semantic host icon or exactly the packaged `ui.icon`; omission
selects the installed app icon, then `puzzle`. Omit unused overrides rather
than sending empty strings or null. No tool, URL, command text, install action
or component can be a command destination.

### Home commands from installed apps

Home reads only the authenticated installed Apps registry, never the catalog.
A contribution is actionable only while its app is enabled, running, compatible
with the host SDK, and has validated immutable assets and a declared destination.
Loading, list errors, forbidden access, absence, disabled installations and failed
installations remain distinct; a list error does not mean “not installed”.
Mounted consumers keep bounded polling alive after disable or failure so recovery
appears without reloading. Drawing commands does not load addon JavaScript.

Every eligible contribution has `/addon:<addonId>:<commandId>`. Optional aliases
cannot take any native command name, even a hidden native command, or any usable
skill name. If multiple contributions request the same alias, all lose that alias;
the canonical commands remain. Order is addon ID then contribution ID in the Apps
group. Home shows deterministic, actor-visible alias diagnostics to administrators
in its expandable command diagnostics section. Labels do not identify commands.

Clicking a command or submitting its exact text uses the same navigation guard.
Commands accept no arguments and do not execute prefixes. The guard re-reads the
authenticated installed registry and checks actor, permissions, generation,
immutable entry and declared page before navigating. Disabled, failed, upgraded,
removed or inaccessible apps cannot be opened by a previously captured menu entry.
Logout aborts pending reads; even a response that ignores cancellation cannot
publish into another actor's registry. Installed commands are Home-only; `/apps`
remains generic native navigation to installed apps. Uninstalled apps never get
install commands or promotional examples. Studio owns its `/studio` alias in its
manifest rather than in Home code. Historical URL redirects and pin migration
remain unchanged.

### Execution provider and status payload

Inside `capabilities`:

```json
{
  "executionProvider": {
    "tools": ["bash"],
    "statusTool": "execution-status"
  }
}
```

`tools` contains 1–8 unique declared action tools with executable definitions.
The current chat binding requires each bound tool to shadow `bash` through
`shadows`; existing shadow validation still applies, including its uniqueness
rule. This declaration neither grants shadow authority nor changes provider
arbitration or execution authorization.

`jobs` is optional, feature level 6: `{"jobs": {"tool": "job"}}` names the
tool that runs the background jobs of the host's `job` tool. It is a declared
`kind: "action"`, `uiOnly: true` tool that shadows nothing, is not an admin tool
and is neither a bound execution tool nor the status tool. It is the only
page-only tool that may be an action tool. No model is offered it and no page
can call it: the host calls it through the act path, with the chat as the
resource and the person as the actor, and with the arguments and answer below.
A provider without `jobs` cannot run background jobs. The host then answers
`job start` with "The add-on here cannot run background jobs: it is
older than this LocalAI. An administrator can update it." and does not run the
job anywhere else. A manifest with `jobs` and a lower `featureLevel` is
refused; an older host refuses the add-on with "needs a newer LocalAI".

The arguments of the jobs tool are `contract.JobArgs`: `verb` (`start`,
`list`, `read`, `input` or `kill`; the key is `verb` because `action` is a name
no tool schema may use), `id` (`job-` and six base32 characters), `command` and
`label` (start; the label is one line of at most 80 characters), `since` (an
absolute byte offset; omitted, the read goes on where the model's last read
ended), `maxBytes` (256 to 65536, default 16384), `observe` (a read that does
not move the model's cursor and carries the last 2000 bytes as `tail`: the
check of a wait), `input` (at most 2000 bytes) and `closeInput`, and for a kill
`signal` (`term`, the default, `int` or `kill`) and `reason`. `reason` is why
the host ends the job: `stopped` (the person's Stop), `chat_deleted`,
`agent_ended`, `shell_off`, `group_off` or `fenced`. The add-on records it as
the job's `end` (`stopped` with the state `killed`, the others `exited`);
without it the end is `killed`. The host's `execution` call options are
added as for `bash`. The answer is `contract.JobReply`: `ok`, `job` or `jobs`
(`id`, `label`, `state`, `end`, `exit`, `started`, `ended`, `lastOutput`,
`total`, `idleMs`; never the command), and for a read `output`, `from` and
`next`. A refusal is `ok: false` with `code` (`unknown`, `ended`, `limit`,
`busy`, `invalid`, `failed`) and an `error` the model reads. The host checks
every answer against its arguments (sizes, offsets, states, job IDs) and shows
nothing of one that fails, and a job of another person or chat is `unknown`.

While the add-on reports a started job as running, the owner keeps the
start's request live as a **job lease**, so the job's final checkpoint
(`workspace.commit`, `workspace.changes`) and its network checks (`egress.check`,
`egress.audit`) still find a live request after the call returned. The lease
has the actor, scope, resource and chat of the start. It ends when an answer of
the jobs tool reports the job finished, two hours and five minutes after the
start, or when the add-on process stops. A kill does not end the lease: the
job's final checkpoint runs after the kill. The host asks the jobs tool every
30 seconds while a chat it follows has a running job, so a finished job's lease
ends soon after the job. Under a lease the channel allows only those four
operations, and an add-on holds at most 64 leases. The owner takes the place
of a lease before it sends a start; with every place taken or held by a start
in flight, it does not send the start and answers it as a refusal with the code
`limit`. The in-flight
table, and so the lease, lives in the owner process, where every act of a
frontend is forwarded.

`statusTool` is a separately declared `kind: "read"`, `uiOnly: true` tool,
not an execution binding or admin tool. Its input schema is an object accepting
`{}`, with no required inputs, no resource, and `additionalProperties: false`.
It must answer without mutation. An owner-side typed read must invoke it;
the model-facing tool adapter intentionally excludes `uiOnly` tools.

The existing result envelope carries this JSON in `payload`:

```json
{
  "readiness": "available",
  "isolation": "isolated",
  "explanation": "Commands use the configured isolated backend.",
  "networkPolicyEnforced": true
}
```

`networkPolicyEnforced` is optional and false when absent. A provider sets it to
true only when every network connection of its commands goes through the host's
network policy (the Sandbox does, through its proxy). The host honors it only
for a status that is `available` and `isolated`; it then shows the chat's
Network row. Without the report the chat says that network-policy enforcement is
not verified for the provider.

`contract.DecodeExecutionStatus` accepts at most 2048 UTF-8 bytes. Both enums
are required: readiness is `available`, `unavailable` or `unknown`; isolation
is `isolated`, `host` or `unknown`. Optional explanation is nonempty plain text,
at most 200 Unicode characters, without markup, controls or direction overrides.
Unknown or duplicate fields, trailing JSON, `outputs`, private `ok` fields,
backend names and warning booleans are not accepted. Invalid payloads return
an error and unknown/unknown, never partial provider data. Non-available
readiness normalizes isolation to unknown. Status describes only the bound
tools of that addon incarnation; it is not a host isolation guarantee.

Studio consumes this declaration through the owner-selected conversation binding,
not an addon ID or a private settings payload. Prompt construction, command
status and nested egress reads share one observation within a turn. Priming and
standalone command-status or egress requests have separate, bounded scopes.
Consumer cancellation stops that wait; turn completion cancels the observation.
A failed read settles as unknown with a reason (`unknown_reason`: `owner_unreachable`, `not_reported`, `stale`, `invalid_status`, `not_selected`, `no_answer`, `reported_unknown` or `draft`, for a chat that is not created yet), which the Tools menu shows. It is not retried at once. After a failure that a restart of the add-on owner can cause (`owner_unreachable`, `no_answer`, `stale`), a scope that is still open (a long turn) binds and reads again, at most once every five seconds. A refusal of the binding or of the payload is not retried. Each
consumption revalidates the complete owner binding, including conversation,
current selection identity and runtime incarnation.

The application resolver reads the current principal-scoped chat and agent from
JSON or SQL/GORM stores and revalidates current account credentials in named-user
mode. Local-owner mode maps to the anonymous chat principal. Reads are advisory,
not transactional snapshots: revoke-and-restore changes between reads are not
remembered. Ordinary execution authority and consent remain unchanged. An undeclared
legacy provider does not acquire an isolation claim. Sandbox retains its existing
private Settings status response and separately declares the typed page-only
`execution-status` read. Unknown backends report unknown, never inferred isolation.

Provider-reported isolation and a declared network control do not establish
host network-policy enforcement. The egress API therefore does not claim policy
applicability for addon command runners until the host can prove enforcement.
Built-in or explicitly host execution continues to report the host-execution
reason. Controls can still identify the addon that draws its settings row.

The Apps tab binds each list request and registry ingestion to the authenticated
session epoch. A same-user refresh or account switch immediately hides the old
list and rejects its pending responses, even before effect cleanup. A new list
request can populate the refreshed session. These in-memory scopes contain no
credentials.

### Execution descriptor of a commands tool

On the definition of a tool that shadows `bash`:

```json
{
  "name": "bash",
  "kind": "action",
  "shadows": "bash",
  "description": "...",
  "inputSchema": { "...": "..." },
  "execution": {
    "class": "commands",
    "title": "Runbox",
    "note": "The environment has python3, jq and sqlite3.",
    "skills": { "providesRuntimeFor": ["documents"] }
  }
}
```

The descriptor says what the add-on is, so that the host can write about it
without knowing its ID. It grants no authority. Whether commands are ready and
isolated stays what the provider reports in its execution status (see above);
the host decides from that report.

| Field | Meaning |
|---|---|
| `class` | Required. `commands`: the tool runs shell commands and code. |
| `title` | Optional, at most 40 characters of plain text. The host calls the provider by this name in the prompt and in the Tools menu text ("Runbox runs commands in an isolated environment", "switch the Runbox add-on on"). Without it, the host uses the add-on's own title. |
| `note` | Optional, at most 600 characters of plain text. One sentence for the model, such as the programs the environment has. The host adds it to the Commands block only while the provider reports `isolated`. |
| `skills.providesRuntimeFor` | Optional list of skill kinds. `documents` means the add-on's document skills (`xlsx`, `docx`, `pptx`, `pdf`) run on this provider. |

The host offers the document skills of an add-on only while that add-on serves
the chat's commands and its status reports `isolated`. When the status is
`host` or unknown, or another provider serves the commands, the skills are left
out and the chat says why. A tool descriptor may leave `providesRuntimeFor`
out to opt out of that check, because its skills do not need its commands.

The field is optional and does not change the protocol. An add-on
whose commands tool declares no descriptor still works: the host treats it
as a provider of commands under the add-on's own title, with no note, and keeps
the isolation check for its document skills. The host never compares the add-on
ID with a fixed name for any of this, so a second add-on that runs commands gets
the same behavior with its own title.

The same holds for the rest of the interface. Settings, the Tools menu, the
network card of a chat and the model prompt describe the provider by its
declared title, or by generic words ("the add-on that runs commands") when none
is installed. Core text never names a shipped add-on, so the title you declare
is the only name your users see for it. A test reads `addons/*/manifest.json`
and fails when a locale string, a Settings label or a Go string of core names an
add-on that provides a role.

### Network capability

Needs manifest `api` 2. Inside `capabilities`:

```json
{
  "network": {
    "kind": "egress-check",
    "title": "Runbox",
    "presets": [
      { "id": "off", "title": "Closed" },
      { "id": "all", "title": "Open web" }
    ]
  },
  "hostOperations": { "optional": ["egress.check"] },
  "ui": { "controls": ["network-policy"] }
}
```

The declaration says that the add-on runs commands behind a proxy that asks the
host about each connection. The host attaches the `network-policy` control to
it, and writes about the network from it, without knowing the add-on ID. It
grants no authority: the host enforces the administrator's limit and the chat's
rules, and it answers `egress.check` only for an add-on whose host setting
grants that operation. The tool that runs commands does not have to shadow
`bash` for the control to attach.

| Field | Meaning |
|---|---|
| `kind` | Required. `egress-check`: the add-on's proxy calls the host operation `egress.check` for each connection, and the add-on blocks what the host refuses. The manifest must list `egress.check` in `hostOperations`. |
| `title` | Optional, at most 40 characters of plain text. The host names the network by it in the text the model reads ("The server's setting for the Runbox network") and in the `network` object of the API. Without it, the host uses the title of the commands tool, then the add-on's own title. |
| `presets` | Optional, at most three entries of `id` (`off`, `registries` or `all`, no repeats) and `title` (at most 40 characters). The chat Tools menu and the network cards show these titles for the host's presets (here "Closed" for `off` and "Open web" for `all`). A preset you leave out keeps the host's title. |

The apps list and the chat's network view carry the result in a `network`
object (`title` and `presets`). The settings keys do not change. The host keeps
the names `chats_egress_ceiling`, `chats_egress_default`, `chats_egress_ports`,
`chats_egress_extra_registries`, `chats_egress_allowed_hosts`,
`chats_egress_allow_private` and `chats_egress_audit_days`, with the values
and the API they have today, so a running instance needs no migration. They are
host settings, not settings of one add-on. The setting `chats_host_exec`, which
lets the host run commands when no add-on provides `bash`, is also a host
setting and names no add-on.

The host attaches the control to an add-on that asks for `network-policy` in
`ui.controls`, declares `network` and is granted `egress.check`. A manifest
that declares no `network` gets no network control, whatever tools it replaces.
The field does not change the protocol.

### Requested host operations are not grants

Inside `capabilities`:

```json
{
  "hostOperations": {
    "required": ["workspace.materialize", "workspace.commit"],
    "optional": ["egress.check", "egress.audit"]
  }
}
```

Each name must pass `contract.ValidHostOp` exactly: no wildcard or prefix grant.
There are at most 32 entries total, unique across both lists. An empty object
is invalid. Omission preserves legacy/unspecified requests, not an empty grant
and not a request for every operation. Required operations declare service
prerequisites; optional denials must support degraded operation. Unknown but
syntactically valid names are unsupported, never grants by inference. Requests
do not create host endpoints or change administrator-controlled grants.

### Official operation audit

The official manifests request the operations their production hostclient calls use.
Deployment prefixes in `deploy/addons/host-policy.json` remain separate administrator grants.
The generic binary selects no policy automatically.

| Addon | Required operations | Optional operations |
| --- | --- | --- |
| Fine-tuning | `finetune.checkpoints`, `finetune.dataset`, `finetune.export`, `finetune.get`, `finetune.list`, `finetune.start`, `finetune.stop` | None |
| Quantization | `quantize.get`, `quantize.import`, `quantize.list`, `quantize.start`, `quantize.stop` | None |
| Office | `workspace.export`, `workspace.import` | None |
| Sandbox | `workspace.changes`, `workspace.commit`, `workspace.materialize` | `egress.audit`, `egress.check`, `workspace.admin` |
| Web | None | `workspace.admin` |
| Home Assistant | None | `workspace.admin` |
| PiKVM | None | `workspace.admin` |
| Face, Slack, SSH, Studio, Tables, Talk, Telegram, Voice | None; omit `hostOperations` | None |

Fine-tuning and Quantization include operation names passed through mutation and undo wrappers.
They do not request the host's unused progress operations.

The `finetune.start` and `quantize.start` operations take an optional `backend`.
The host accepts only an installed backend whose gallery entry carries the tag of the operation (`fine-tuning` or `quantization`).
When the call names no backend, the host uses the first installed backend of that tag in gallery order.
A new engine needs only a gallery entry with the tag. With no engine installed, the call fails with a `400` that says so.
Telegram's historical `connector.` deployment grant does not imply a hostclient call or manufacture a manifest request.

Sandbox's proxy uses the optional egress operations when configured connections need policy decisions.
Missing grants do not authorize connections; the proxy cannot obtain an allow verdict.
Sandbox's settings, Web's provider settings, the limits of the Home Assistant add-on and the per-device switches of the PiKVM add-on use `workspace.admin` to check administrators.
Without that optional operation, these settings refuse changes; ordinary tools can remain available.
The shipped deployment policy covers every official required operation.
The package tests compare all twelve manifests with this source audit and the host's registered vocabulary.
Their package-builder fixtures use temporary executable and UI bytes, not official release binaries or frontend build artifacts.

### Release packaging

Every package declares manifest `api` 2, which is the only version the host
speaks. Catalog version ranges do not negotiate individual fields and cannot
guarantee eligibility before staging. These declarations alone do not install
packages, grant operations or activate runtime/UI integrations.

### Resource listing

An add-on that keeps resources can ask the host for a read tool that lists the
resources the caller may use, so a model finds the `resourceId` that its other
tools take. The add-on writes no code for it: the host builds the tool from its
own record of the grants.

```json
{
  "api": {"min": 2, "max": 2},
  "capabilities": {
    "resources": {
      "noun": "board",
      "inspect": "boards-inspect",
      "list": {"tool": "boards-list", "items": "boards"}
    }
  }
}
```

- `list.tool` is the tool name. The model sees it as `addon__<id>__<tool>`. It
  must not be the name of a tool, an admin tool or a tool definition of the
  manifest; the host refuses such a manifest.
- `list.items` is the key of the result array. It defaults to `resources`.
- `agentScoped: true` (feature level 2) lets a saved agent name the resources it may
  use. It needs a `list`, because the host builds the choices from it. See
  [Resource listing for agents]({{% relref "/addons/contract-reference/owner-forwarding#resource-listing-for-agents" %}}).
- The manifest `api` range must reach 2. A manifest that declares a listing with
  `api.max` 1 is refused.
- The tool is a read tool that takes exactly `{}`. The result lists at most 100
  entries, ordered by resource ID, with `resourceId`, an optional `name` and the
  caller's `role`. `truncated` is true when the list is incomplete. There is no
  pagination.
- The tool is offered only to actor-bound consumers, only while the add-on is
  enabled and running, and only for resources the caller owns or that were shared
  with the caller. Existing chat opt-ins, hidden add-ons and allowlists apply.
- An add-on that declares no `list` gets no listing tool, whatever else it
  declares.

### Credentials and a resource list

An add-on whose tools call a service with a secret of the person, such as an
access token, can ask the host to keep the secret and to check which resources
a call may reach. The model never sees or sets either.

```json
{
  "api": {"min": 2, "max": 2},
  "capabilities": {
    "credentials": [
      {"id": "token", "title": "Access token", "kind": "token", "resourceArgument": "repo", "resourceLabel": "repository"}
    ],
    "connector": {"kinds": [{"id": "example", "title": "Example", "delivery": "pull"}]}
  }
}
```

- The secret and the list live in the person's **binding** of the add-on's
  connector kind. The host seals the secret. The list is `Binding.Resources`: at
  most 50 entries, each an external name (`owner/name` for a repository) with a
  host ID made from its hash. The person edits it where they edit the binding.
- `resourceArgument` names a string argument that carries the external name. A
  tool that declares it must require it, unless the slot sets `resourceOptional`. Before the add-on is called, the host
  matches the argument against the list of the one binding that the call
  names, without regard to case. A name that is not on that list is refused with
  the code `resource_not_allowed` and the names of that list; the add-on is not
  called. The host never picks a binding: a chat uses the connections that the
  person or the saved agent enabled for it (or the person's only one). When two or
  more are enabled, the host adds a required `connection` argument to every tool
  of the add-on in the definitions it gives the model, an enum of the names of the
  enabled connections, checks the name against that set and removes the argument
  before the call. The add-on's manifest and tools do not change and must not use
  an argument of that name (a tool that does is not offered when a chat enables
  two or more connections). A call with no usable connection is refused with the
  code `select_connection`. The result of a call and the card of a proposal say
  which connection acted, for example "as Work, repository acme/widgets"; a
  proposal is applied with the binding it was previewed with. The word for the
  resource in these lines is `resourceLabel`, and the host takes nothing else
  about the add-on from its identity: any add-on that declares a credential
  behaves the same way.
- `resourceLabel` is optional (at most 24 characters of plain text): the word for
  the resource in that line. Without it the host says "resource".
- `resourceOptional` (default `false`) lets a call leave the resource argument out.
  The tools then declare the argument and do not require it. The host uses the one
  resource that the selected binding lists, so the connection is the only source
  of the target and the model never has to write it. A binding that lists several
  resources refuses the call with `resource_not_allowed` and names them. A call
  that names a resource is held to the list as before. An add-on that does not set
  it is not changed.
- A call that passes the check carries `Request.Credential` (the secret),
  `Request.ResourceKey` (the name as the person wrote it) and `Request.ResourceID`
  (the host ID). The host drops these three fields from any request a client
  sends. A person with no binding gets the code `no_credential`.
  `Request.GrantedScopes` is dropped the same way.
- A manifest with `credentials` must accept `api` 2, declare a connector kind and
  declare read tools only: a change under a credential needs a write mode, which
  this version does not have. Tools of such an add-on name no `resourceId`.
- A tool with `uiOnly: true` (see the `uiOnly` field of a tool definition) gets no credential and no resource, even in an add-on that declares a slot. It is for the add-on's own page, which has no chat to name a connection, so it works with no connection or with several. The Home Assistant add-on keeps the limits for all its connections this way.

- The secret is an opaque string of at most 4096 bytes, and the host never reads
  it. An add-on whose service needs more than a token (servers, a login and a
  password) can seal a small versioned JSON record as the secret and keep only
  the resource name in the list, as the Mail add-on does (see
  [Mail]({{% relref "/features/chat-apps/accounts#mail-imap-and-smtp" %}})). The host cannot show a
  sealed record back, so the add-on's page asks for all of it again when a person
  changes any part. A service that is not HTTP and is not reached through the
  sandbox network check must apply the address guard of `pkg/web/safehttp` itself.
- An add-on that is rate limited returns `contract.ErrRateLimited{RetryAfter}`.
  The runtime answers HTTP 429 with `Retry-After`; the model gets the code
  `rate_limited`, `retryAfter` in seconds and a note not to retry before then.

### OAuth 2.0 credentials

A credential slot of kind `oauth2` says that the host logs the person in to the
service with the OAuth 2.0 authorization code flow. The slot carries an `oauth`
descriptor, which is required for this kind and refused for kind `token`. The
add-on never sees the client secret, the authorization code or a refresh token;
it only gets a short-lived access token in `Request.Credential`, like any other
credential.

```json
{
  "id": "login",
  "title": "Example login",
  "kind": "oauth2",
  "oauth": {
    "authorizeUrl": "https://auth.example.test/authorize",
    "tokenUrl": "https://auth.example.test/token",
    "revokeUrl": "https://auth.example.test/revoke",
    "hosts": ["auth.example.test", "*.example.test"],
    "scopes": {"required": ["read"], "optional": ["write"]},
    "pkce": "S256",
    "authorizeParams": [{"name": "access_type", "value": "offline"}],
    "clientAuth": "basic"
  }
}
```

- `authorizeUrl` and `tokenUrl` are required; `revokeUrl` is optional. Each URL
  uses `https`, has no user info, query or fragment, uses no IP address and
  uses port 443 or none.
- `hosts` has one to four patterns. A pattern is an exact lower-case DNS name
  of two or more labels, or `*.` followed by such a name; a wildcard matches
  exactly one label. Every URL must match one pattern. A browser reads some
  names as an address of this machine, so the host refuses them: a label that
  is a number (`2130706433`, `127.1`, `0177.0.0.01`) or a hex number
  (`0x7f000001`, `0x7f.0x0.0x0.0x1`), a top-level label that is not letters or
  punycode (`xn--...`), `localhost` and any name under it, and a trailing dot.
- `scopes.required` and `scopes.optional` have at most 16 scopes each, no scope
  in both, and no repeat. A scope is an RFC 6749 scope token of at most 128
  bytes.
- `pkce` must be `S256`.
- `clientAuth` is how the host authenticates at the token endpoint: `basic`,
  `post`, or `none` for a public client that uses PKCE alone.
- `authorizeParams` has at most four `{name, value}` pairs of extra authorize
  parameters. The names come from a closed registry: `prompt`, `audience` and
  `resource` (a value of 1 to 128 characters from `A-Z a-z 0-9 . _ : / -`),
  `access_type` (only `offline`) and `include_granted_scopes` (only `true`).
  Names that the host sets itself, such as `client_id`, `redirect_uri`,
  `scope`, `state` and `code_challenge`, are always refused.
- The manifest parser is strict: an unknown field is refused.

The descriptor is data. It adds no route and makes no network call, and it
carries no authority. The host works out a SHA-256 digest of its canonical form,
which does not depend on field order, on the order of the host, scope and
parameter lists, or on whitespace. An administrator approves the add-on for
that digest, and a change of the descriptor, for example in an upgrade, voids
the approval.

An upgrade that changes the descriptor does not log people out. Their logins
keep their tokens but are held: the host neither uses nor refreshes them, and
the connections show `needs_approval`, until an administrator approves the new
descriptor. The approval then moves each held login to the new descriptor when
it sends the refresh token to the same `tokenUrl` and `revokeUrl` as before,
and the login works again without a reconnect. When either endpoint changed,
the host never sends the refresh token there: the login ends with the reason
`endpoint_changed` and the person connects again. A login that lacks a scope
the new descriptor requires ends with `scopes_missing`, and the scopes the new
descriptor no longer names are dropped.

A new login keeps only the scopes the descriptor names, required or optional,
even when the service grants more (incremental authorization). A login that
lacks a required scope fails with `scopes_missing`.

The apps list (`GET /api/addons/apps`) gives an add-on with an `oauth2` slot an
`oauth` object with `requiredScopes` and `optionalScopes`, so the Connections
page can offer the optional scopes when a person connects, and the add-on's
Settings tab shows the administrator the form that approves the login. For an
administrator, its `admin` object also has `sourceId` and `official` (the
official library, with the signature checked); the form shows the
untrusted-source warning above the approval when `official` is not true. The
addresses of the login are not in the list; an administrator reads them with
the OAuth client (see [the login routes](#the-login-routes)).

The list does not say whether an administrator approved the client. A
connection's `oauth.has_client` does, and a start with no approved client is a
`409 needs_approval`. An add-on page that logs in (the Gmail and Google Drive
pages) draws the host's login component, `sdk.ui.ConnectionLogin`: it adds the
connection, starts the login with the page as `return_to`, shows the state of each
connection, and offers an administrator a link to the add-on's Settings tab while
there is no approved client.

A request also carries `Request.GrantedScopes`, the scopes the host holds for
the person's login. The host sets it; it is not a secret, and the host drops it
from any request a client sends.

When the service refuses the access token, the add-on answers HTTP 401 with the
reason `credential_rejected`. It does so only when the service refused the token
and nothing changed. The host then decides whether to refresh the token or to
ask the person to reconnect; a 401 with another reason is an outage. An add-on
built on the public Go package `github.com/mudler/LocalAI/pkg/addons/sdk/protocol` returns
`contract.ErrCredentialRejected` (wrapped or not) from a handler, and the
package sends this answer. When the
connection has expired or was revoked, the model gets the code
`reconnect_needed` and a note to tell the person to reconnect on the Connections
page. The model is told not to repeat the call and not to report an outage.

#### Where the host keeps an OAuth login

A binding has an auth kind. An empty kind or `token` is a binding whose token
the person gave; it works as before. A binding of kind `oauth2` holds no token
of its own: its tokens are in its **grant**, and the binding is a usable
credential only while it has one. The auth kind must match the add-on: an
add-on whose credential slot is `oauth2` takes `oauth2` bindings only, and any
other connector add-on token bindings only. The connector kinds
(`GET /api/chats/connectors/kinds`) say `auth_kind: "oauth2"` for the first.

The host keeps three records, in the same store as the bindings (the
`studio_connector*` tables of the database, or `connectors.json` in the data
folder of a single process):

| Record | Table | Key | Sealed | Plain |
|---|---|---|---|---|
| Grant | `studio_connector_grants` | binding | access token, refresh token, token type | expiry, scopes, account label, state (`connected`, `needs_approval` or `reconnect_needed`), reason, descriptor digest, token and revocation endpoints, version, lease |
| OAuth client | `studio_oauth_clients` | add-on ID | client secret | descriptor digest, client ID, who approved it and when |
| Login state | `studio_oauth_states` | SHA-256 of the `state` parameter | PKCE verifier | binding, owner, add-on, descriptor digest, redirect URI, scopes, return path, nonce hash, expiry |

- Sealed values use AES-256-GCM with the sealing key of the bindings. Each
  record type has its own additional data (a grant's names its binding, owner
  and add-on), so a sealed value copied to another row does not open.
- The host never stores the `state` parameter itself. A login state lasts 10
  minutes and works once: the callback removes it in the same step that reads
  it, on whichever frontend serves the callback, even when the exchange that
  follows fails.
- The tables and the `auth_kind` column are added when the server starts.
  Existing bindings have an empty kind and need no change.

#### Login, refresh and disconnect

An administrator saves the client ID and secret of an add-on for the descriptor
digest they saw; the save is the approval, and the host refuses it when the
descriptor changed in the meantime. A status read says whether a secret is
stored and never returns it.

A login starts only when the approval holds for the current descriptor. The
host makes a 256-bit `state`, a PKCE verifier and a nonce for a cookie of the
browser, and asks for the required scopes and the optional ones the person
ticked. At the callback the state is used up first; then the person and the
nonce must match the start, and the approval must still hold. The host trades
the code at `tokenUrl`, checks that the required scopes were granted, and asks
the add-on's connector op `me`, with the new token, who the person is. An
`unauthorized` answer fails the login. The host removes control and format
characters (such as bidirectional overrides) from the account name, folds white
space and keeps at most 128 characters, so "Connected as" cannot show another
account than the one linked. The outcome is one of `ok`, `denied`,
`invalid_state`, `provider_error`, `exchange_failed`, `scopes_missing`,
`token_too_large` (the access token is larger than the 4096 bytes a request
carries) and `unavailable`; nothing the service wrote is copied into it.

Every credentialed call reads the grant:

- The access token is refreshed when less than two minutes of its life are
  left, or less than half of it for a token that lives under four minutes. A
  service that gives no `expires_in` is refreshed only after a
  rejection. The proposal broker reads the credential at preview and again at
  apply, so a confirmation hours later gets a fresh token.
- When the add-on answers `credential_rejected` to a read, a preview or an
  apply, the host refreshes once and retries the call once, unless the grant
  changed since the call began; then it retries with the newer token. A second
  rejection ends the grant, and the call is refused with `reconnect_needed`. A
  rejected apply changed nothing, so its proposal stays pending.
- The new access token, its expiry, the granted scopes and a rotated refresh
  token are written in one compare-and-set step before the token is used. When
  that write fails after the service rotated the refresh token, the host tries
  three times and then ends the grant.

| The service answers | Result | Grant |
|---|---|---|
| Network error, timeout or 5xx | the call fails as an outage and may be repeated | unchanged |
| 429 | `rate_limited` with the `Retry-After` of the service | unchanged |
| `invalid_grant`, `invalid_client`, another refusal | `reconnect_needed` | secrets wiped, state `reconnect_needed` |
| an access token over 4096 bytes | `reconnect_needed`, reason `token_too_large` | secrets wiped |
| a `scope` without a required scope | `reconnect_needed`, reason `scopes_missing` | secrets wiped |
| a `scope` with other scopes | the token is used | the granted scopes become those of the login that the service still grants; a scope the login never had is not added |
| (descriptor changed, not approved yet) | `reconnect_needed` to the call; the connection shows `needs_approval`, and no token is used or sent | secrets kept, state `needs_approval` |
| (new descriptor approved, same `tokenUrl` and `revokeUrl`) | the login works again, without a reconnect | moved to the new descriptor |
| (new descriptor approved, another `tokenUrl` or `revokeUrl`) | `reconnect_needed`, reason `endpoint_changed`; the refresh token is never sent to the new endpoint | secrets wiped |

Disconnect revokes the refresh token (or the access token when there is no
refresh token) at `revokeUrl` when the descriptor has one, with a 10 s limit,
and then deletes the grant even when the revocation failed. The answer says
whether the service was told. Deleting the connection revokes the same way
first; a person's delete asks the add-on owner, so it works on any frontend,
while an administrator's delete revokes only on a process that runs the
add-ons. A login whose callback finishes after its connection was deleted
stores nothing: the store writes a grant only while its binding exists, in the
same step.

The token client follows no redirect, reaches public addresses only, waits at
most 30 s and reads at most 64 KiB. It accepts bearer tokens only, an access
token must fit in 4096 bytes, and `expires_in` is clamped to one minute through
one day. A granted `scope` must be RFC 6749 scope tokens of at most 128 bytes,
the rule `Request.GrantedScopes` is checked with, so an answer with any other
scope fails the login or the refresh instead of every later call. Its errors
are fixed classes: no token, code, verifier, state, client secret or answer of
the service goes into a log, an error or a tool result.

Uninstalling an add-on does not touch its grants or its client yet: the
add-on manager has no hook for it. The bindings stay, as they do for a token
binding.

#### The login routes

| Route | Who | Body | Answer |
|---|---|---|---|
| `POST /api/chats/connectors/{id}/oauth/start` | the owner of the binding | `{"scopes": ["write"], "return_to": "/app/chats/connectors"}`, both optional | `{"authorize_url", "expires"}` and the nonce cookie |
| `GET /api/chats/connectors/oauth/callback` | the browser that started the login | the query the service sends | `303` to the return page |
| `POST /api/chats/connectors/{id}/oauth/disconnect` | the owner of the binding | none | `{"revoked": true}` or `false` |
| `GET /api/chats/admin/oauth-clients/{addonId}` | an administrator | none | the client, without the secret |
| `PUT /api/chats/admin/oauth-clients/{addonId}` | an administrator | `{"client_id", "client_secret", "descriptor_digest"}` | the client, without the secret |

The person's routes need the `connectors` feature and, because a login is an
add-on call, the `addons` feature. The client routes need the administrator
role. None of these routes has an MCP tool: a client secret must not pass
through a chat, and a login is a person's act in their own browser.

**Test a connection.** `POST /api/chats/connectors/{id}/test` on an `oauth2`
binding asks the add-on's `me` with the access token, renewed first when it
ends soon. It answers with the types of the login routes: `409 not_connected`
before the login, `409 reconnect_needed` when the login ended, `409
needs_approval`, `429 rate_limited` with `Retry-After`, and `503 unavailable`
when the login cannot be checked now. A frontend does not read the add-on's
login, so a test there answers `503 unavailable`; test on the add-on owner, or
in a combined process.

**Limits of the start.** A binding can have at most 5 logins that wait for
their callback, and a person at most 20; a login stops waiting when its
callback comes or after 10 minutes. A person can start at most 10 logins a
minute. Past a limit the start answers `429` with the type `too_many_logins`
and stores nothing. The count of starts per minute is kept by the add-on
owner, which serves every start; the waiting logins are counted in the shared
store.

**Register the callback with the service.** Every add-on uses one redirect
URI: the base URL of the server followed by
`/api/chats/connectors/oauth/callback`, for example
`https://localai.example.test/api/chats/connectors/oauth/callback`. The client
read returns it as `callback_url`. The host builds it, in this order, from
`LOCALAI_BASE_URL`, from the forwarding headers of a trusted proxy (see
[reverse proxy]({{% relref "/administration/reverse-proxy-tls" %}})), or from the
request when the host is `localhost` or a loopback address. When none of them
applies, the start is a `409` with the type `base_url_required`, because the
service would otherwise send the code to an address that anyone can choose.
The address must use `https`, so that the code, the state and the nonce cookie
do not cross the network in clear text: a trusted proxy that forwards plain
`http` for a host that is not loopback also gets `base_url_required`. An
`http` address works only for a loopback host, or when an administrator sets it
as `LOCALAI_BASE_URL`.
The address of a login is stored with its state and sent again at the
exchange.

**Approve an add-on.** Read the client: `current_digest` is the digest of the
login the add-on declares now, and `descriptor` shows it in plain fields
(addresses, hosts, required and optional scopes, client authentication and
authorize parameters). Register the callback address with the service, then
save the client ID and secret with `descriptor_digest` set to the digest you
read. The save is the approval. When the add-on's login changed in the
meantime, nothing is saved and the answer is `409` with the type
`approval_stale` and the new `current_digest`; read it again and review the
change. `client_secret` is write-only: an absent secret keeps the stored one,
and a public client (`clientAuth` `none`) has none. The read says
`has_secret`, `approved` and who approved when.

**Log in.** The start checks the binding, the approval and the optional
scopes, and sets a nonce cookie for this login (`HttpOnly`, `SameSite=Lax`,
`Secure` over HTTPS, ten minutes). Each login has its own cookie, named
`localai_oauth_nonce_` and the first eight hex digits of the SHA-256 of its
state, so two logins started in one browser do not break each other. The return page must be a
page of the web app: a path under `/app` with an optional query and no
fragment, as the add-on SDK accepts; anything else is a `400`. Open
`authorize_url` in the same browser. The service sends the browser back to the
callback, which:

- needs the session of the person who started the login. The session cookie is
  `SameSite=Lax`, so the browser sends it on that redirect. With
  authentication off there is no session, and the nonce cookie alone binds the
  login to the browser;
- finds the nonce cookie of its login by the state in the query and clears
  that cookie, whatever the outcome; the cookies of other logins stay;
- always answers `303` to the return page, with `oauth=ok`, `oauth=denied`
  (the person declined; a connection that worked keeps working) or
  `oauth=error&class=<class>`, where the class is `invalid_state`,
  `provider_error`, `exchange_failed`, `scopes_missing`, `token_too_large` or
  `unavailable`. The
  `error_description` of the service is never copied into the address, a page
  or a log.

**See the state.** The binding JSON (`GET /api/chats/connectors` and
`GET /api/chats/connectors/{id}`) has `auth_kind: "oauth2"` and an `oauth`
object: `state` (`connected`, `reconnect_needed`, `not_connected` or
`needs_approval`), the fixed `reason` of a reconnect, `account_label`,
`scopes`, `expires_at` and `has_client`. It never holds a token. The state is
read from the shared store, so every frontend shows the same; a login whose
descriptor changed in an upgrade shows `needs_approval` once a call or a
refresh found it, and the result of the approval as soon as an administrator
saves it. The add-on list of a chat (`GET /api/chats/{id}/addons`) gives each
connection a `connection_state`, and the add-on a `connection_state` of
`reconnect_needed` when a connection the chat may use must be connected again.

#### OAuth with several frontends

State mode: stateless per request, with a claim in the database. Every
frontend reads the OAuth records from the shared database, and the sealing key
is in the same database, so any process with the database can open them. No
process keeps a copy. Two things keep one refresh at a time:

- In one process, one refresh per grant and version runs, and the other callers
  wait for its result.
- Across processes, a refresher takes a lease in the grant row (a conditional
  update on the version, 90 s: three times the 30 s limit of the token request,
  so a slow service cannot outlive it). The other callers look at the row every
  200 ms for up to 10 s and use the token the winner wrote. A service that
  rotates its refresh tokens can revoke the whole login when one refresh token
  is used twice, so this matters.
- Each process compares the lease with its own clock. Keep the clocks of the
  hosts in sync (NTP): a host whose clock is ahead sees the lease end early by
  as much.

In a split deployment the refresh runs on the add-on owner: the frontends
forward credentialed tool calls to it, the owner adds the credential and calls
the add-on, and when the add-on rejects the token the owner refreshes and
retries before it answers. Only the final `reconnect_needed` refusal crosses
back to the frontend.

The OAuth descriptor comes from the manifest of the installed add-on, which
only the owner reads. So the start, the callback, the disconnect and the
client routes are owner dispatch, in a combined process and in a cluster
alike: the frontend that gets the request forwards it over the carrier with
the signed caller, and the owner serves it. The service can send the browser
back to any frontend; that frontend forwards the query and the nonce cookie as
fields of the call (never as headers), the owner trades the code, and the
frontend answers the redirect. A frontend never serves these routes from a
service of its own: when the owner cannot be reached, the person's routes
answer `503` and the callback ends with the class `unavailable`. An owner
that predates the calls answers `503` with the reason that it must be
upgraded.
