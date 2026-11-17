+++
title = "Proposals, resources and file results"
description = "Proposals and confirmation, UI-only resource management and private file results."
weight = 90
+++

Look up proposals and confirmation, the management of resources from the UI and private file results. This page is part of the [Add-on contract reference]({{% relref "/addons/contract-reference" %}}).

## Proposals and confirmation

Every data or schema mutation that comes from an API client, an MCP session or the
OpenAI-compatible chat route is a stored proposal first. A proposal is applied only
by an explicit confirmation of that stored operation from the person's own browser
session. The chats on the server, their agents and their triggers are the exception
described under [Lifecycle](#lifecycle): there the add-on switch is the consent.
Sharing and database deletion are never proposals and never tools.

### Declared tools

The owner checks every request that names a tool against the manifest of the installed
current generation before anything reaches the add-on. The tool name is the top-level
`tool` key of the request `arguments`. The rule is the same for a `read`, a proposal
(`write` or `schema`), the confirmation of a stored proposal and the opening of an
output, on the owner's own frontend and on every forwarding frontend:

- No `tool` key: the request names no tool, and the rule has nothing to check.
- An exact `tool` key: the value must be a string that equals one entry of
  `capabilities.tools` byte for byte. A name of another add-on, a case, Unicode or
  white-space variant, an empty value and a value that is not a string are denied.
- A key that equals `tool` only after case folding or trimming white space (`Tool`,
  `TOOL`, ` tool`) is denied, because an add-on decoder may read it as the tool.
- A repeated key is denied at any depth of `arguments`. Keys are compared after JSON
  unescaping, white-space trimming and Unicode case folding, so `"tool"` and
  `"\u0074ool"` collide, and so do `"tool"` and `"TOOL"`. A runtime that keeps the
  first value would otherwise run another tool than the one that was checked.
- `arguments` must be exactly one JSON object. An array, a scalar, `null`, trailing
  data after the object, invalid JSON and nesting deeper than 64 levels are denied.
  This rule runs in the declared-tool check itself, so it holds for reads,
  proposals, confirmations and output opening whatever validation ran earlier.
- A denial is the standard 403, and the add-on is not contacted. A proposal is denied
  before the preview and nothing is stored. A stored proposal is checked again at
  confirmation, so a tool that an update withdrew is never applied.
- Only a real answer is a denial: the tool is not declared, or the generation is stale
  or not installed. When the declarations cannot be read, the request is unavailable
  (503) and nothing runs: the manager is closed or not the owner, the journal is
  uncertain or runtime recovery is required, the request context is done, or the
  source fails for any other reason. An unclassified failure is unavailable, never
  success and never a policy denial. The owner and every forwarding frontend give the
  same status. The owner reads declarations from its in-memory journal, cached for
  each generation.

### Lifecycle

Tools reach a model through the same proposals: a chat that sets
`metadata.addon_tools` can call the tools of enabled addons, and a change a
tool proposes waits as a pending proposal. The chats on the server (`/api/chats`),
their agents and their triggers are the one exception: turning an add-on on for
a chat or an agent is the consent, so a change tool is applied at once as the
chat's user. The chats adapter stores the proposal and confirms it in the same
call with the credential of a chat turn, so the same admission, revision
and receipt rules hold, a failed call leaves no pending proposal, and no API
key, MCP session or browser request can use that credential. The result says
what was applied and whether it can be undone: Tables, Fine-tuning and
Quantization keep a change set for the changes they can roll back, and the result
carries `undoable`, the `changeId` and a one-line `undo`. See
[Undoing a change a chat made](#undoing-a-change-a-chat-made). `GET /api/addons/proposals?state=pending`
lists the caller's own pending proposals (newest first, at most 32). See
[Tables]({{% relref "/addons/tables-reference#model-tools-and-approval-cards" %}}) for the tool names, the
approval card and the inventory of tool consumers.

`POST /api/addons/proposals` takes a typed `write` or `schema` request. When the
arguments name a tool, the owner derives the action from the kind the manifest gives
that tool (`write` tools are data writes, `schema` tools are schema changes) and
refuses a request whose `action` disagrees, a `read` or `create` tool, and a tool the
manifest does not define. The owner
resolves the resource, requires write authority (`schema` needs the owner role), asks the
addon for a preview and stores a proposal with:

- the canonical arguments, the addon generation, the resource and the proposing user;
- the schema and data revisions the preview saw;
- the exact number of effects and a bounded sample of them (the addon's preview summary,
  using `count` and `sample` when it has them);
- a `digest` of everything above (it also covers the operation ID, the proposing user and
  the action; the owner recomputes it from the stored record before it applies, so a record
  that was changed at rest is refused and never runs), and an expiry (the shorter of the addon's preview expiry
  and the proposal expiry limit, five minutes by default).

Nothing is applied by this call. The request's `operationId` becomes the proposal ID, so
every later step, including a lost reply, is resolved by the same ID. Repeating the same
request returns the same proposal. Reusing an ID with other arguments, another actor or
another resource is a denial.

A proposal is in exactly one state:

| State | Meaning |
|---|---|
| `pending` | Waiting for a human decision. |
| `cancelled` | Withdrawn by its actor, or invalidated when the user's authority on the resource was lowered or revoked. |
| `expired` | Not confirmed in time. The owner records it when the clock runs out, on the next proposal, on start and on recovery, whether or not anyone read it. |
| `executing` | Confirmed and handed to the addon. Durable before the addon sees the call. |
| `applied` | Committed. The receipt is stored with the proposal. |
| `rejected` | Refused by the addon (for example, the revision moved) or found stale (the addon generation changed). |
| `outcome-unknown` | The apply may or may not have run. A receipt lookup, or the resource owner's explicit acknowledgement, resolves it. |
| `abandoned-unknown` | The resource owner stopped waiting for an unresolved outcome. It no longer blocks writes and is never applied again; a later receipt lookup is still recorded. |

Bounds. A user holds at most 32 live proposals (pending ones that have not expired, and
unresolved ones) and at most 16 MiB of stored canonical arguments across them; the store
holds at most 1024 proposals and 64 MiB of arguments. The user's own bound is checked
first, so one user can exhaust only their own share. A limit reached is a typed quota
denial (HTTP 403) that names which bound it was. To make room for a new proposal the owner
drops settled proposals only (cancelled, expired, applied, rejected), the proposer's own
first and then the oldest; a live proposal of another user is never dropped. Settled
proposals are also dropped after a retention window of 24 hours by default
(`BrokerOptions.Retention`), so a repeated request inside the window still gets its stored
answer. Retention cannot be switched off.

Every transition is written atomically and fsynced by the owner before it is reported. The
store lives beside the policy store and follows the same rules: one process holds it, a
corrupt file is refused and never reset, and a restart recovers it.

### Creating a resource

A request with `action: "create"` and no `resourceId` proposes the creation of a new
resource, such as a Tables database. Nothing exists yet, so the owner works in this order:

1. It checks the creation authority of the caller and that the named tool is declared.
2. It reserves a pending resource for the proposer. A reservation is invisible to everyone,
   the proposer included, counts against the pending limit of the user and is bound to the
   proposer. The proposal records its ID as `resourceId`.
3. It asks the addon for a preview with the reserved ID. The preview only validates the
   request and creates nothing. What the human sees (`count` 1 and the requested `name`) is
   built by the owner and not taken from the addon.

Confirming runs the creation under the creation authority of the proposer, which is checked
again, and not under a role on a resource. The addon operation ID is `create-` and the
reserved ID, so a repeat is one operation. A committed receipt makes the proposer the
private owner of the resource. Cancelling, expiry and a rejected receipt drop the
reservation; the resource was never visible.

An uncertain apply keeps the reservation pending and the proposal `outcome-unknown`. A
receipt lookup settles both. The proposer can also stop waiting (`resolve`): the
reservation is dropped only when the addon's `resource-exists` tool confirms that the
database does not exist, and it stays pending when the addon says it exists or cannot
answer. At start the owner settles reservations of unknown or executing proposals by asking
the addon, and keeps those of proposals that are still pending. A reconciliation never drops
the reservation of a creation that is running in this process.

### Confirm

`POST /api/addons/proposals/{id}/confirm` with `{"digest": "..."}` submits exactly the
stored parameters under resource admission, once. The runtime validates the revision in
the same transaction as the write, so a data change after the preview is a `rejected`
proposal and not a silent overwrite. Confirming again returns the stored receipt and
applies nothing.

The route is reachable only from a host confirmation surface:

1. The credential must be a browser session cookie of the user who proposed. A named API
   key, a bearer token, a legacy key or an MCP session can propose and read, and can never
   confirm, whatever its role.
2. The request must be a same-origin fetch (`Sec-Fetch-Site: same-origin`,
   `Sec-Fetch-Dest: empty`, `Sec-Fetch-Mode` `cors` or `same-origin`).
3. The `X-LocalAI-Confirm` header must equal the proposal ID, and the body must carry the
   digest of what was displayed.
4. A request that carries a tool, agent or MCP execution marker (`Mcp-Session-Id`,
   `X-LocalAI-Tool-Call`, `X-LocalAI-Agent`, `X-LocalAI-Execution-Context`) is refused.

In local-owner mode there is no session; rules 2 to 4 apply, and any API credential on the
request is refused. The owner repeats the credential check, so a frontend cannot confirm
for a key credential either.

Limit of the confirmation boundary in local-owner mode. Without accounts there is no
identity to bind a confirmation to. The only boundary is the set of browser headers
above, and a script that sets them can confirm: any caller that can reach the HTTP port
shares the authority to propose and to confirm, and that includes the proposals
it made itself. This is inherent to local-owner mode, where unauthenticated callers
share one authority, and it is accepted and not changed. The owner logs a warning at
startup that says so. Use local-owner mode only on a trusted loopback or private
network, and use named-user mode wherever a confirmation must come from a particular
person. Plain text, a boolean in arguments, or a model saying "yes"
are only data and never approve. Clients without a confirmation UI can propose and read;
they never auto-apply. No MCP tool maps to confirm: the MCP exposure of proposals (list
and status only) is a later task and must keep that rule.

### Unknown outcomes and recovery

Every proposal call is idempotent by the proposal ID: a proposal is a single-use state
machine, and repeating a call with the same ID returns the stored state and never starts
a second operation. The owner therefore answers an outage by what it knows about the
proposal, and every frontend reports the same answer for the same owner state:

| Response | Meaning | What to do |
|---|---|---|
| `503` `proposal_unavailable` with `operationId` | The owner could not serve this call, and this call sent nothing to the addon. The proposal is in the state the owner recorded, which can be any state: pending, or already settled by an earlier call. | Read the proposal with `GET`, then repeat the same call with the same ID. |
| `503` `outcome_unknown` with `operationId` | The request to the addon may have reached it (`executing` or `outcome-unknown`). A request that never left the owner (no free slot, a withdrawn add-on, an invalid envelope, a connection that was never written to) returns the proposal to `pending` and is `proposal_unavailable`. An add-on's 409 conflict ends the proposal as `rejected`. | Do not confirm again as a new operation. Reconcile, wait for the automatic lookups, or resolve. |

Which type a route can emit does not depend on who proposed the operation. A proposal,
a confirmation and a cancellation by the proposer can be either type. A resolution is
always `proposal_unavailable`, because resolving never applies anything and the resolver
is the resource owner, who is not always the proposer. A confirmation that the addon
committed answers `200` with the applied proposal; the owner does not read the record
back through a second account check that could fail after the apply.

A reply lost between a forwarding frontend and the owner for a confirmation is reported
as `outcome_unknown`, because the frontend cannot know whether the owner started it.
The lifecycle dispatch rule is unchanged: any uncertain lifecycle mutation is an unknown
outcome with its original operation ID.

If the reply to a confirmation is lost, or the owner restarts while a proposal is
`executing`, the proposal is `outcome-unknown` and the response is `outcome_unknown` with
the original `operationId`. The mutation is never submitted again under any ID.

`POST /api/addons/proposals/{id}/reconcile` looks the operation's receipt up on the addon
and records a committed or rejected result. It is read-only toward the addon and can be
repeated. An addon that has no receipt for the operation stays `outcome-unknown`: a
missing receipt does not prove the call never ran. While an outcome is unresolved, every
other write or schema proposal on the same resource is denied; a pending proposal
shows `blocked: true`; reads continue. A write denied for this reason is `403`. For
the owner role of the resource only, the body is type `outcome_blocked` and carries
`blockedBy`, the operation ID to resolve. Any other user gets the plain denial and never
learns another user's operation ID, also not in a proposal view. On start the owner settles proposals left
`executing` by a crash the same way and never applies them.

Recovery has three steps and none of them submits the operation again:

1. **Automatic lookups.** The owner repeats the receipt lookup for each unresolved
   proposal with exponential backoff (from the `BackoffInitial` limit up to
   `BackoffMax`). They run for a finite window: the apply timeout plus five minutes,
   counted from the moment the apply was handed to the addon. A final receipt settles the
   proposal and lifts the block. After the window, only an explicit lookup remains.
2. **Explicit lookup.** `reconcile`, by the proposer, at any time.
3. **Owner resolution.** The owner role first finds what to resolve:
   `GET /api/addons/resources/{resourceId}/proposals?state=unresolved` lists every
   unresolved or abandoned proposal on the resource, whoever proposed it, with its ID,
   state, proposing user, action, exact effect count and bounded effect sample, expiry,
   digest and age in seconds, oldest first. It never returns the proposed arguments or a
   credential. Only the owner role of that resource may call it, from the same
   confirmation surface as resolve with `X-LocalAI-Confirm` equal to the resource ID;
   editors, installation administrators and API keys are denied, and no MCP tool maps
   to it. Then `POST /api/addons/proposals/{id}/resolve` with
   `{"digest": "...", "reason": "..."}` (the digest from the list, required and checked
   by the owner itself, not only by the HTTP route) lets the owner role of the resource
   stop waiting. It is accepted only from the same confirmation surface as confirm (the
   owner's own browser session, a same-origin fetch, `X-LocalAI-Confirm` equal to the
   proposal ID, no tool or agent marker). The proposer's role is not enough, an
   installation administrator without an owner role on the resource has no access, and
   no API key or MCP tool can call it. A final receipt found at that moment settles the
   proposal instead. Otherwise the proposal becomes `abandoned-unknown` and records who
   acknowledged it (`resolution.actorId`), when and the reason (up to 512 characters).
   Writes on the resource are accepted again.

An abandoned operation keeps its original operation ID. The owner keeps looking its
receipt up in the background for a day and on every `reconcile`; if the addon reports
that it ran, the proposal becomes `applied` with that receipt (or `rejected`), and the
`resolution` stays on the record, so a late success is recorded and never hidden. An
abandoned record is kept for 30 days and is not dropped to make room.

Data consistency. Resolving is the owner asserting that the outcome is not worth waiting
for; it does not prove the change did not run. If it did run, it stays in the data. Later
proposals preview the current revision, and the runtime refuses an apply whose revision
moved, so a change prepared against the state before the unknown one is rejected and not
applied on a stale view. Check the data before you resolve when that matters.

### Revocation races

Lowering or removing a user's grant cancels that user's pending proposals on the resource
before the change is acknowledged, and stops their admitted confirmations. A confirmation
that already reached the addon becomes `outcome-unknown` and is resolved by receipt.
Other frontends reach the proposal state only through the owner's gateway, signed and
actor-bound like every other forwarded call.

Run `sh scripts/addons/verify.sh proposals` for the proposal suites.

## UI-only resource management

A person manages their own resources, for example Tables databases, from the host's own
pages. The routes below are the whole surface. They are **never tools**: no model tool, no
MCP tool, no proposal and no typed dispatch reaches any of them, and sharing and deletion
have no tool definition anywhere. The routes live in the `addons` feature group with the
usual browser protections (CSRF, same-origin cookie requests), and they are registered in
the route-feature registry.

| Route | Who | What |
|---|---|---|
| `GET /api/addons/resources?addonId=` | any caller with the feature (keys too) | The resources of the add-on the caller owns or that were shared with the caller, with the caller's own role, the display name, the owner's display name for a shared one and, for the owner role only, how many people it is shared with and the revision. At most 100. The names are read at most half of the add-on's concurrency budget at a time, so a page that reads while it lists is not refused. |
| `POST /api/addons/resources` | the person's own session | Create a resource through the add-on's create tool. Caller becomes the owner; the resource is private. |
| `GET /api/addons/resources/{resourceId}/grants` | owner role, own session | Who it is shared with: immutable account ID, display name, role. |
| `PUT /api/addons/resources/{resourceId}/grants/{userId}` | owner role, own session | Give a named user `viewer` or `editor` on the whole resource. |
| `DELETE /api/addons/resources/{resourceId}/grants/{userId}` | owner role, own session | Remove the grant. |
| `GET /api/addons/resources/{resourceId}/users?q=&limit=` | owner role, own session | The user picker. |
| `POST /api/addons/resources/{resourceId}/rename` | owner role, own session | Rename through the add-on. |
| `POST /api/addons/resources/{resourceId}/delete` | owner role, own session | Delete the resource and its data. |
| `POST /api/addons/resources/{resourceId}/apply` | role of the tool, own session | One data or schema change through a declared write or schema tool, with no card. |
| `POST /api/addons/resources/{resourceId}/changes/{changeId}/undo` | the role of the change, own session | Undo one change a chat made at once, by the change set the add-on kept. |
| `GET /api/addons/resources/{resourceId}/operations/{operationId}?kind=` | owner role (rename, delete) or a role that may write (apply, undo), own session | Read-only receipt lookup of a rename, a deletion, a change or an undo. |
| `GET`/`PUT`/`DELETE /api/addons/conversations/{conversationId}/selection/{addonId}` | any caller with the feature | The resource a conversation works on. |

"Own session" means the same rules as a confirmation: a browser session cookie, a
same-origin fetch, `X-LocalAI-Confirm` equal to the resource ID (the operation ID for a
creation) and no tool, agent or MCP marker. An API key, a bearer token and a legacy key are
refused, whatever their role. In local-owner mode the rules are the same without a session.
The grants, the picker and the receipt lookup are owner-role reads of other people's data and
use the same surface.

Every call goes to the owner through the carrier, so a frontend that holds no addon state
gives the owner's own answer. The state is the owner's alone and is single-runner by design:
the policy store with its `deleting` records, the proposals and the selection store
(`selections/` below the addon root) are never read or written by a frontend, and a
frontend that cannot reach the owner reports a mutation as `outcome_unknown` with the
original operation ID. The owner checks the caller's role itself, with the same denial
for an unknown resource and for one that is not the caller's. Installation administration is
not data access: an administrator lists and changes what they own and nothing else.

### Creating, renaming and deleting

These are the person's own explicit actions, so they need no approval card, but they follow
every other rule of a mutation.

- **Create.** The owner reserves a pending resource, runs the add-on's create tool through
  the same preview, apply and receipt steps as a chat creation, and activates it. The
  resource ID is derived from the person and the `operationId`, so repeating the call
  returns the same resource and a repeat with other arguments is refused. The arguments are
  validated against the tool definition of the manifest exactly as a model's call is, and a
  crash is settled at the next start like any pending reservation. The per-person limits
  apply (`quota_reached`).
- **Rename.** The call runs under the resource's admission, behind an unresolved outcome
  (`outcome_blocked` for the owner role), against the generation and the revision the owner
  listed (`stale`, nothing changed). The add-on's receipt for the operation is the record:
  repeating an `operationId` replays it, and two people that chose the same ID never share one.
- **Delete.** The order is durable at every step:
  1. the resource becomes `deleting`: inaccessible to everyone, no new admission and no grant
     change, and admitted work is cancelled and waited for (bounded);
  2. with no work left, the owner checks the revision it listed and the confirmation: the
     request must carry `confirm`, equal to the display name exactly. A failure returns the
     resource to use;
  3. the deletion is marked confirmed, pending proposals of every user on the resource are
     cancelled, and the add-on is asked to delete under one operation identifier;
  4. only after the add-on confirmed, the record and every grant are removed.

  A crash leaves a `deleting` record. At the next start an unconfirmed one is returned to
  use, and a confirmed one is finished; a reconcile loop retries one that cannot be finished
  while the add-on is down. The resource stays inaccessible meanwhile. Deleting never
  uninstalls the add-on and never touches other resources. An outcome that cannot be
  established is `outcome_unknown` with the original `operationId`: repeat the same call, or
  look the receipt up; nothing is submitted under a new operation.

  Before it calls the delete tool, the owner asks the add-on's `resource-exists` tool whether
  the backing is still there. Only that explicit answer of the declared tool can say "gone",
  and then the record is removed without the delete tool. An add-on that is not installed,
  does not declare the tool or cannot answer never counts as "gone": the deletion stays
  `deleting`, is reported as `outcome_unknown` and is retried by the reconcile loop, so the
  data is never left behind a removed record. This applies to the request, to a repeat of it
  and to the recovery at start.

The delete tool is declared in `capabilities.adminTools`. The owner is its only caller, from
the delete endpoint and its own crash recovery, under the host action `delete`, which no
model tool, proposal or typed request can carry. The add-on refuses the tool under any other
action.

### Changing data and schema from the UI

`POST /api/addons/resources/{resourceId}/apply` is how a person's own page changes rows or
the schema, for example the Tables page. The body is `{operationId, generation, revision,
tool, arguments}`. It is not a proposal: nothing is stored for later and no card is shown,
because the request in the person's own tab is the intent. It keeps every rule of a
mutation:

- **Tool.** `tool` must be declared in `capabilities.tools` with a definition of kind `write`
  or `schema`. Reads, creations, the add-on's mapped rename, create and delete tools, admin
  tools and undeclared names are `400`. The arguments are the tool's own, without the
  resource (the path names it) and without `tool`; they are validated against the
  definition exactly as a model's call is, so an identity, role or approval field or an
  unknown field never reaches the add-on.
- **Role.** A `write` tool needs at least the editor role; a `schema` tool needs the owner
  role. A viewer, an editor changing the schema, a person with no grant and an installation
  administrator get the answer of an unknown resource.
- **Admission and revision.** The change is admitted per resource, waits behind an unresolved
  outcome, and is checked against the generation and the revision the caller read (`409
  stale`, nothing changed). The add-on's receipt is the record: repeating an `operationId`
  with the same request returns the same result and never applies twice; the same ID with
  another request is refused as `409 operation_conflict` before the add-on is called, with
  nothing applied (never `outcome_unknown`). The host binds each operation ID to a request
  hash (`requestHash`: SHA-256 of the resource, the tool and the canonical arguments, per
  user), returns it with the result, and keeps it for a bounded number of recent
  operations. An add-on that refuses the reuse with `409` on `/apply` gets the same
  answer.
- **Result.** The answer carries the new `revision` and the receipt's `result` (for Tables,
  the `count` and the changed row `ids`). An outcome that cannot be established is `503
  outcome_unknown` with the original `operationId`; look it up with `kind=apply` and never
  submit a new operation. The lookup answers `state` plus the `tool` and `requestHash` the
  ID was used for. Pass `requestHash=<hash of your request>` to ask about your own request:
  a hash that differs from the recorded one is `409 operation_conflict`, and a hash the
  host does not hold (for example after a restart) is `state: unknown`, which is safe to
  answer by repeating the same request. A client that compares `requestHash` before it
  believes `committed` never mistakes another change for its own.

### Undoing a change a chat made

A write, schema or create tool called from a chat, an agent or a trigger is applied at once
(there is no card), so undo is the safety net. An add-on that declares `capabilities.undo`
keeps a change set for the changes it can roll back, in its own storage, and says so in the
receipt of the change: the receipt payload has an `undo` object, `{"id", "available",
"summary", "reason"}`. The host copies it into the tool result as `undoable`, `changeId`,
`undo` (what an undo does, one line) and `reason` (why nothing was kept), and the chat shows an
Undo button on the entry. The model is told that the person can undo it and that it cannot.

`POST /api/addons/resources/{resourceId}/changes/{changeId}/undo` takes `{operationId,
generation}`. The change ID is the one the tool result named. It runs under these rules:

- **Humans only.** It accepts only the person's own browser session, with the rules of
  every UI-only mutation: a same-origin fetch, `X-LocalAI-Confirm` equal to the resource ID,
  and no API key, bearer token, tool, agent or MCP marker. This is deliberate. It is never a
  tool and no MCP tool maps to it, so a model cannot undo its own work or anyone else's
  without the person.
- **Role.** The role of the undo is the role of the change, which the add-on reports:
  `write` changes need the editor role, `schema` changes and creations the owner role. Other
  callers get the answer of an unknown resource.
- **Stale.** The undo is applied against the revision the add-on has just reported. If the
  resource moved on after the change, the answer is `409` with type `stale` and a message that
  says what changed; nothing is overwritten.
- **Typed refusals.** `not_found` (`404`: never kept, too large to keep, or dropped),
  `expired` (`410`: past the 7 days of retention), `already_undone` (`409`), `cannot_undo`
  (`409`: for example the job it started has finished) and `stale` (`409`). The type of the
  error is the code. `outcome_unknown` and `outcome_blocked` behave as for every mutation.
- **Idempotent.** The same `operationId` with the same change is one undo and returns the same
  result; after `outcome_unknown` look the receipt up with `kind=undo`.
- **Result.** `{resourceId, changeId, operationId, revision, summary, deleted}`. `deleted` is
  true when the change was the creation of a resource: the host deletes it with its own delete,
  and only while the add-on says nothing touched it since.

For an add-on author: declare `"undo": true` in `capabilities`, add the key `undo` to the
receipt payload of every change, and answer two calls that are never in `capabilities.tools`,
so no model can name them. The read tool `changes-get` takes `{change}` and answers
`{id, class, state, summary, detail, current, deletes}` with `class` `write` or `schema` and
`state` `available`, `stale`, `expired`, `undone`, `cannot` or `not_found`. The apply with the
action `undo` and the tool `changes-undo` takes `{change}` and the revision `current`, and
answers a committed receipt, or a rejected receipt whose payload is `{"error": code,
"message"}`. The add-on checks the action and the scope itself. Tables, Fine-tuning and
Quantization implement it; Fine-tuning and Quantization undo only the start of a job, by
stopping it while it runs.

In a cluster the undo travels the same carrier as every resource call: a frontend forwards it
to the owner, which calls the add-on, and the typed refusal crosses the carrier as the same
typed error. The change sets are state of the add-on, in its own storage on the owner (for
Tables inside each database file), so they are covered by the add-on's backups, restores and
purges and need no extra shared state on the frontends; the carrier and the owner keep none.

An SDK-based page does not call the route itself. `sdk.resources.apply` (see
[the SDK]({{% relref "/addons/contract-reference/apps-and-pages#the-resource-calls-of-the-sdk" %}})) sends it for the add-on and generation the SDK is
bound to.

### Sharing

Grants are whole-resource and `viewer` or `editor`; the owner role cannot be granted.
Named-user mode only: local-owner mode answers `409` with type `sharing_disabled`, and
anonymous and legacy principals are never grantees. The grantee is named by the immutable
account ID that the picker returned, and must be an active named account that holds the
`addons` feature. The picker finds such accounts by a prefix of the display name or of the
address (2 to 64 characters, at most 20 results) and returns only an ID and a display name,
never an address; the caller and the owner are left out, and the wildcard characters of a
query mean themselves. Lowering or removing a grant cancels the user's pending proposals and
stops their admitted work before the call is acknowledged; a drain that does not finish in
the bound is `503` and the change is already effective for new work.

### The resource of a conversation

The selection is a convenience for chat, not an authority. The owner stores an opaque
resource ID keyed by the authenticated user, the conversation ID and the add-on, in its own
bounded store: at most 256 entries per user (the least recently used is replaced), 8192 in
total, expiring after 90 days without use. It is not process-global, not shared between
people or conversations, not part of the chat history and not sent to the model.

- `PUT` requires at least the viewer role on the resource now.
- A tool call of a chat request that names no `resourceId` uses the conversation's selection.
  The conversation ID comes from the authenticated request (the `X-LocalAI-Conversation-Id`
  header the chat sends), never from the model's arguments, and the owner authorizes the
  reference for the caller on every call. A reference that was revoked, whose resource was
  deleted, that was edited in the store or that belongs to another person gets the denial of
  an unknown resource: the result of kind `error` with code `resource_unreachable`, from the
  owner and from every frontend. The reference stays and works again if the grant does.
- With no selection the tool call returns a result of kind `error` and code
  `select_resource`: no operation runs and the model is told to ask the person to choose.
- A refusal the model can act on is a result of kind `error`, never a raw error string.
  The codes are `invalid_request` (the add-on refused the call; a `message` carries its
  short reason when it gave one, such as a missing training engine), `not_permitted` (the
  person lacks a permission), `resource_unreachable` (no such resource, or no access; a read
  and a change are denied the same way, and the result does not tell a missing resource from
  one the person may not use) and
  `not_running` (the add-on is starting or stopped; nothing was sent). Each note says that
  nothing changed and not to repeat the call. A preview that finds a conflict, such as a
  table that exists, is an `invalid_request`, not an outage. The tools of the fine-tuning and
  quantization add-ons are not offered at all to a person without those permissions.
- An explicit `resourceId` in the arguments is the person's own word and is used as before.

### Errors

| Status | `error.type` | Meaning |
|---|---|---|
| 400 | `invalid_request` | A missing or wrong confirmation, an operation the add-on does not support, arguments or a name it refuses, a malformed identifier. |
| 403 | `addon_error` | Not the person's own session, or the feature or role is denied: one answer for all of them. |
| 403 | `not_permitted` | The add-on checked a host permission of the person, such as fine-tuning, and it is not granted. Nothing changed. |
| 403 | `resource_unreachable` | The resource does not exist or the person has no access to it: one answer for both, for a read and for a change. |
| 403 | `package_unverified` | An install failed because the package does not match its library entry or is not a valid add-on package. Nothing was installed. |
| 403 | `library_unavailable`, `library_conflict`, `release_not_found`, `package_fetch_failed`, `install_storage_failed` | An install or upgrade failed at the step the type names. Nothing was installed. The server log has the detail. |
| 403 | `quota_reached` | The per-person resource limits. |
| 403 | `outcome_blocked` | Another operation on the resource has an unresolved outcome (`blockedBy` is shown to the owner role only). |
| 404 | `not_selected` | No resource is selected, or the reference is no longer usable. |
| 409 | `stale` | The generation or the revision the caller saw is out of date; nothing changed. |
| 409 | `sharing_disabled` | Local-owner mode has no named users to share with. |
| 503 | `outcome_unknown` | A mutation may have run. `operationId` is the original; repeat the same call. |
| 503 | `addon_error` | The owner or the add-on cannot answer. |

A request that the add-on itself refuses as invalid (an unknown table, an unknown field, a
value its schema rejects) is `400` with `error.type` `invalid_request` on every route that
reaches the add-on: the typed apply, a read through dispatch or relay, a proposal and its
confirmation, an artifact open, and the chat tool result (kind `error`, code
`invalid_request`). The owner's own frontend and every forwarding frontend give the same
answer. The add-on answers HTTP 400 or 422 and the runtime client types that answer as a
refusal; any other non-success stays an outage (`503`). A refusal is deterministic and
nothing was applied: it is never `outcome_unknown`, the same call is refused again, and the
caller corrects the request instead of repeating it. An operation ID whose preview or
apply was refused is not consumed: the host forgets its binding and the add-on removes the
pending line it wrote for it, so the corrected request may reuse the same ID. A refused
proposal is not stored. A confirmation the add-on refused ends the proposal as `rejected`.

A selection call has no operation: a lost answer is plain unavailability and the call is
safe to repeat.

## Private file results

An addon read can return file outputs, such as a CSV export. A file result is a private,
temporary artifact of the user who asked for it. It is not a shared file: the model,
media, upload and agent file routes and the frontend code artifacts never see these
bytes, and the existing file APIs are unchanged.

### What the host does with an output

1. The owner never returns the addon's output handle to a client. When a read result has
   file outputs, the owner reserves one artifact for each (all of them, or none) and
   replaces the handle in the result with the **artifact ID**: an opaque, random,
   host-generated identifier. An addon handle, URL or path is never an identifier and
   never names a file. The handle must be a valid ID of the contract; a URL or path in its
   place is refused and nothing is read.
2. The artifact records the requesting principal, the source resources (the request's
   resource, as the policy recorded it), the addon and its generation, the operation, the
   sanitized name, the media type, the declared size and the expiry. The addon handle is
   kept only in memory while the copy runs; only a digest of it, with the principal and
   the operation, is stored so that a repeated request finds the same artifact.
3. A bounded background worker copies the bytes from the registered runtime endpoint of
   the addon (and from nowhere else) into a private staged area under `artifacts/` in the
   addon root: directories `0700`, files `0600`. The worker streams with one 32 KiB buffer
   and never holds the whole file. It stops at the declared size: a stream that is
   short, long by one byte, cancelled or failing is never published, its partial bytes are
   removed and the artifact is recorded as `failed` with a fixed reason. The bytes are
   published only by an atomic rename after the stream ended cleanly.
4. The copy runs under an output admission of **every** source resource, so a revocation
   cancels it and waits for it, like any other admitted work. At completion the owner
   checks again that the account is current and that the principal can read every source
   resource, that the addon is installed, enabled and at the same generation, and that the
   artifact is not expired. Only then is it recorded as `complete` (metadata is written
   atomically with fsync, validated on load and never reset).

Another user's artifact, an unknown ID, an expired one and a malformed one are the same
`403`, so identifiers cannot be probed. Read access to the source resource does not give
access to another user's artifact, and sharing a database never shares its exports.

### Routes

- `GET /api/addons/artifacts/{id}` returns the artifact view: state (`transferring`,
  `complete`, `failed`, `cancelled`), sanitized name, media type, size, sources, addon,
  generation and expiry. It is the status lookup of the background transfer. It is
  read-only, actor-bound and never shows a path or an addon handle.
- `GET /api/addons/artifacts/{id}/download` streams a complete artifact. The receiving
  frontend relays it from the owner through the `file` relay, so a download works from
  every frontend and no frontend holds the bytes. It needs the session or key of the
  requesting principal and the `addons` feature. Before the first byte and while streaming
  the owner checks the account and read access to all source resources again; a revocation
  cancels the stream. A failure after the first byte aborts the connection, so a truncated
  file never looks complete (the response carries the exact `Content-Length`).

No MCP tool maps to these routes in this release. A tool that returns a file result is
added with the Tables export; it will return the artifact reference, not a URL.

### Response safety

Every download is an attachment (RFC 6266, with an ASCII `filename` and a UTF-8
`filename*`) with a sanitized name, `X-Content-Type-Options: nosniff`,
`Cache-Control: private, no-store`, a `Content-Security-Policy` that sandboxes the
response and `Content-Length`. The name loses path elements, control characters,
bidirectional overrides and invisible characters, shell and file system metacharacters and
leading or trailing dots, is prefixed when it is a reserved device name and is at most 100
characters. The media type is one of `text/csv`, `text/plain`, `application/json`,
`application/x-ndjson`, `application/zip`, `application/pdf`, `image/png`, `image/jpeg` or
`application/octet-stream`; everything else (HTML, SVG, XML, JavaScript) is served as
`application/octet-stream`, and parameters are dropped. A browser request with a session
cookie must be a same-origin fetch or navigation like the other addon routes.
Spreadsheet formula handling of a CSV is the addon's concern; the host only delivers the
bytes as a download.

### Limits

All limits are finite and validated at startup; a partial or unbounded override is
refused.

| Limit | Default |
| --- | --- |
| One artifact | 64 MiB (at most the relay file bound; a file result larger than the configured relay file bound is refused whole at ingest, so nothing is staged that cannot be downloaded) |
| Declared bytes of one user / of all users | 1 GiB / 4 GiB |
| Artifact records of one user / of all users | 100 / 1000 |
| Concurrent transfers (all / one user) | 4 / 2 |
| Concurrent downloads | 16 |
| Lifetime from creation | 1 hour |
| One transfer | 5 minutes |

Quotas are enforced under one lock when the artifact is reserved, so concurrent requests
cannot exceed them; a refusal reserves nothing. A quota refusal is `403`, a busy store is
`503`. A repeated request (same principal, operation and output) returns the same
artifact and uses no more quota.

### Lifecycle

- **Expiry.** A sweep (every minute) removes artifacts at their expiry, transfers that no
  worker owns, artifacts of an uninstalled addon and files that no record names; opening
  the store also settles interrupted transfers and removes unreferenced files. An
  expired artifact is never served, even if the sweep did not run yet or the expiry falls
  while a download is being admitted or streamed. A download that meets a sweep which
  removes the artifact at that moment gets the same `403` as any expired or unknown
  identifier, never a corruption error. The sweep removes the bytes only of records it
  really changed: a transfer that completes while the sweep runs keeps its bytes.
- **Storage errors.** Only bytes that are definitely missing, not a regular file or of
  another size than recorded mark the artifact `failed` and remove the bytes (`503`). A
  transient storage error (for example too many open files or an I/O error) is `503`
  and leaves the bytes and the record as they are, so a later download can succeed.
- **Disable.** A disabled addon denies downloads and accepts no new file result. The
  complete bytes are kept until their normal expiry. Enabling it again serves an artifact
  only while it is unexpired and only to a principal that can still read every source
  resource.
- **Uninstall.** An uninstalled addon denies downloads at once and its artifacts are
  removed by the next sweep (they are not kept until expiry, because nothing can answer for
  them). An addon update to another generation does not serve the older build's files.
- **Restart.** Complete artifacts survive a restart. A transfer that was running is
  recorded as failed and its partial bytes are removed.
- **Close.** Closing the owner cancels running transfers and streams, waits for them and
  writes nothing afterwards.

Run `sh scripts/addons/verify.sh artifacts` for the artifact suites.
