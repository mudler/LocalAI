---
title: Tables
---

## Status

Tables is a first-party addon for LocalAI. This page describes the database
lifecycle, the typed schema operations, and structured row queries and
confirmed row changes that exist today, how a chat model uses them as tools,
and the host side of managing databases from the UI: creating, renaming,
sharing and deleting a database, and choosing the database of a conversation.
A table, with the filter and sort of a view, exports as a private CSV file
(see [CSV export](#csv-export)). Import and public file links are not
available. The Tables pages (the native UI) are described in
[The Tables pages](#the-tables-pages).

Tables is a separate executable under `addons/tables`. It is not linked into
the LocalAI server, and nothing of it runs unless an administrator installs
and enables the addon. It reuses the root Go module and needs CGO for SQLite.

```
go build ./addons/tables/...
```

## Deployment, trust and limits

- **One owner.** A database is a SQLite file on the disk of the one LocalAI instance
  that owns the add-on. Other frontends forward to it; while the owner is down every
  Tables call fails with `503` on every frontend, and nothing is applied later. See
  [Singleton owner and its limits](../addons/#singleton-owner-and-its-limits).
- **Trust.** The Tables executable is trusted code that runs as the owner's operating
  system user. Tables itself never learns who a caller is: the host gives it the
  database ID and the action, and the host decides the role and the approval.
- **Storage and backup.** The data is under the data directory of the add-on, and
  backups are made by the administrator through the recovery routes (see
  [Backup, upgrade and restore](#backup-upgrade-and-restore)). A copy of the data
  directory made while the add-on runs is not a consistent backup.
- **Ownership mode.** Named-user mode keeps databases private to their owners and
  shareable. Local-owner mode has one owner and no sharing. The mode is stored with the
  data and is changed only while the host is stopped, with an explicit mapping (see
  [Moving between the modes](../addons/#moving-between-the-modes)).
- **CSV.** An export is a private, temporary file with the formula policy described in
  [Spreadsheet formulas](#spreadsheet-formulas): it protects a spreadsheet, not a
  program that trusts CSV.
- **Updates.** The host never upgrades Tables by itself. An administrator checks for an
  update and applies one named candidate (see
  [Update detection and upgrade](../addons/#update-detection-and-upgrade)); the data is
  backed up first.
- **Optional contributions.** The package declares its UI pages and its tools. A host
  that has no UI still runs the tools, and a UI never gains a tool the manifest does not
  declare. The chat actions it declares are placed and drawn by the host; the page cannot
  add a button to the chat of its own.

## What a database is

Each database is one SQLite file with its own metadata. The file is named by an
opaque resource ID that LocalAI assigns. Tables does not choose the ID, does not
store who owns the database and does not decide who may open it. LocalAI keeps
owners and grants and checks them before every call.

| Property | Value |
|---|---|
| Location | `<addon data directory>/tables/databases/<resource-id>.db` |
| Modes | directory `0700`, files `0600` |
| Journal | WAL, foreign keys on |
| Name | 1 to 80 characters, no control characters, no leading or trailing space |
| Size | at most 1 GiB per file, enforced on every connection; a write past the cap fails with a capacity error |
| Locks | busy timeout 3 s, 10 s limit for one operation |

A resource ID must match `^[a-z][a-z0-9-]{0,63}$`. Any other value is refused
before it reaches the file system. Symbolic links are never followed, in the
data directory or as a database file.

Databases are private by default. A new database belongs to the person who
created it and nobody else can list or open it until the owner shares it with
a grant. Administrators who install the addon get no access to databases.

## Tools

The manifest declares these tools. LocalAI rejects any other tool name.

| Tool | Kind | Effect |
|---|---|---|
| `databases-create` | change (schema) | Create the database for a reserved resource ID. |
| `databases-rename` | change (schema) | Change the stored name. Owner role only. |
| `databases-inspect` | read | Name, revision, table names and the tables with their columns. |
| `tables-create` | change (schema) | Create a table. Argument `table`. |
| `tables-rename` | change (schema) | Rename a table. Arguments `table`, `newName`. |
| `tables-drop` | change (schema) | Drop a table and all its rows. Argument `table`. |
| `columns-add` | change (schema) | Add a column. Arguments `table`, `column`, `type`, optional `required`, `unique`, `default`. |
| `columns-rename` | change (schema) | Rename a column. Arguments `table`, `column`, `newName`. |
| `columns-drop` | change (schema) | Drop a column and its values. Arguments `table`, `column`. |
| `rows-query` | read | Select rows of a table with a filter, sort and page. See [Row queries](#row-queries). |
| `rows-aggregate` | read | Count, sum, avg, min and max with an optional group-by column. |
| `rows-export` | read | Export the rows of a table, with an optional filter and sort, as a private CSV file. See [CSV export](#csv-export). |
| `rows-insert` | change (write) | Insert up to 100 rows. Arguments `table`, `rows`. |
| `rows-update` | change (write) | Set columns on rows named by `ids` or a `filter`. Arguments `table`, `set`. |
| `rows-delete` | change (write) | Delete rows named by `ids` or a `filter`. |
| `resource-exists` | read | `{"exists": bool}`; used by LocalAI to settle interrupted creations. |

Tool names use hyphens because LocalAI manifests accept only
`[a-z][a-z0-9-]*` identifiers.

There is no tool that lists every database. The addon only knows the IDs that
LocalAI sends it. LocalAI lists the databases a person may see from its own
resource records, so a listing can never show a database the person has no
grant for.

Changes made from chat are proposals: the model asks, LocalAI previews the
change, and a person confirms it in the host confirmation card. A rename uses
the standard proposal path. Creating a database from chat is a proposal too
(`databases-create` with a `name`, no resource ID). LocalAI reserves a pending
resource for the person who asked, shows the requested name in the card, and
creates the database only when that person confirms it. Cancelling or letting
the proposal expire removes the reservation. Sharing and deleting a database
are not tools.

## Model tools and approval cards

A chat can offer the tools of Tables to its model. The person turns it on per
chat with the `/addons` command of the composer, which appears only when an
enabled addon offers tools. The chat then sends `metadata.addon_tools` with
each request, and the server decides what this person may call: the opt-in
grants nothing by itself.

```
POST /v1/chat/completions
{"model": "...", "messages": [...], "metadata": {"addon_tools": "true"}}
```

### Names and schemas

A tool is named `addon__<addonId>__<tool>`, for example
`addon__tables__rows-insert`. The name is valid for OpenAI function names and
fits in 64 characters. The namespace is reserved: an MCP server of a model
cannot publish a tool under it, and an MCP session never runs one.

The schema of each tool is in `capabilities.toolDefinitions` of the verified
manifest (`addons/tables/manifest.json`). The host serves it from the
manifest, not from the addon process, and rejects a manifest whose definitions
declare a keyword outside the closed set, an identity or approval property
(`actor`, `userId`, `role`, `approved`, `confirm`, `digest`, `proposalId`,
`operationId`, `tool` and similar), more than 32 properties, or more than 8 KiB
for one schema. A declared tool without a definition is not offered to a
model: `resource-exists` is one.

Every tool that works on a database takes `resourceId`. The host takes it out
of the arguments and binds it to the request. A model cannot name a database
it has no grant for: the same denial answers every tool.

### What a call does

| Tool kind | Host action | Result |
|---|---|---|
| Read (`databases-inspect`, `rows-query`, `rows-aggregate`, `rows-export`) | `read` | `{"kind":"result","result":...}`. A result over 128 KiB is refused, never cut. A file result is `{"kind":"artifact"}` with private artifact references and no bytes. |
| Row change (`rows-insert`, `rows-update`, `rows-delete`) | `write` proposal | `{"kind":"proposal","proposal":{id,digest,count,...}}` |
| Schema change (`tables-*`, `columns-*`, `databases-rename`) | `schema` proposal | the same proposal reference |
| Creation (`databases-create`) | `create` proposal | the same, with the reserved resource ID |

A tool call never applies a change. It stores a proposal that only the
proposer's own browser session can confirm. The tool adapter has no confirm,
cancel or resolve operation, and no tool of any consumer maps to those routes.
The model is told that nothing changed and that a person must confirm.

These names do not exist and are refused with a typed error: `confirm`,
`approve`, `apply_proposal`, `yes`, any sharing, grant, database deletion or
retained-data purge name, and any name that is not defined by the manifest of
an enabled addon. Arguments that carry identity, role, approval or operation
fields are refused before they reach the owner, so a model cannot forge the
actor, the role or an approval, and a repeated or oversized argument object is
refused as well.

### The approval card

When a tool answers with a proposal, the chat draws the host's card inline.
The card loads the stored proposal from the host by its ID. It shows the
add-on, the database (the requested name for a creation), the operation, the
exact count of effects, a bounded sample, the expiry and the state. The words
of the model are never the preview. Confirm and Cancel are the host's buttons:
Confirm sends the digest that was displayed with the `X-LocalAI-Confirm`
header from the person's own session.

The card is disabled for an expired, blocked, applied, cancelled, rejected or
unknown-outcome proposal and says why. It forgets what it loaded when the
person logs out, another person signs in, the addons feature is withdrawn or
the host stops answering for the proposal. The stored conversation keeps the
proposal ID only: no rows, no digest, no sample.

`GET /api/addons/proposals?state=pending` lists the caller's own pending
proposals, newest first, at most 32. It is where a change proposed by an agent
or a scheduled job waits: those callers can read and propose within their
permissions and can never confirm, because a key has no browser session.

### Consumers

The consumer inventory is executable. The tests scan the sources for code
that discovers or executes tools and fail for a file that has no decision
here, so a new consumer cannot ship without one. A routed consumer takes an
immutable actor from the authenticated request and goes through the broker.
A rejecting consumer never serves add-on tools: the namespace is refused or
filtered there.

| Consumer | Decision | What it is and why |
|---|---|---|
| `chat-completions` | route | `POST /v1/chat/completions` with `metadata.addon_tools`. The actor is the named user or key of the request. |
| `direct-api` | route | The `/api/addons` routes called by the UI, scripts and agents. The actor is the named user or key of the request, revalidated on every call. |
| `addon-tool-executor` | route | The executor that serves the namespace inside a chat request. It holds one actor value and has no setter. |
| `localai-assistant` | reject | The in-process admin MCP server. It has no per-call principal and its catalog holds no addon tool. |
| `model-mcp-sessions` | reject | MCP sessions built from model configuration and shared by every user of the model. The namespace is dropped from discovery and refused at execution. |
| `anthropic-messages` | reject | `POST /v1/messages`. A request with `addon_tools` gets `addon_tools_unsupported` (400). |
| `open-responses` | reject | `POST /v1/responses`. A request with `addon_tools` gets `addon_tools_unsupported` (400). |
| `realtime` | reject | The realtime WebSocket and WebRTC sessions. Long-lived sessions cannot be revalidated between utterances. |
| `legacy-mcp-chat` | reject | `POST /v1/mcp/chat/completions` passes no broker, so `addon_tools` gets the same typed answer. |
| `mcp-discovery-endpoints` | reject | Listing of servers, prompts and resources. They execute no tool. |
| `agent-jobs` | reject | Agent jobs and scheduled tasks. They run unattended and detached from a request principal. Use the API with a key to read and propose. |
| `agent-runs` | reject | Agent runs of the agent pool and their scheduler. Their tool filter drops the namespace. |
| `distributed-agent-worker` | reject | Workers that execute MCP tools for a frontend. The tool and discovery calls refuse the namespace, and the MCP job handler installs the session tool filter, so the agent library never offers or runs it. |

A shared model MCP session is a rejection on purpose. One session answers
every user of a model and keeps no current-user state, so it could not carry
an actor. A deployment that needs add-on tools for an agent uses the
`/api/addons` routes with that agent's own key.

### Several frontends

The tool adapter keeps no runtime state, so it is stateless in the sense of
the distributed-state rule. Each frontend builds the tool list from the apps
list its owner answers for the caller, and sends every call, proposal and
pending list through the same carrier as the other addon routes. A frontend
that is not the owner never holds a package, a proposal or a policy record.
The apps list carries the tool definitions of each enabled addon, at most 32
KiB per addon, so the model-facing schema is the one in the verified manifest
of the installed generation. If the owner cannot answer, discovery fails and
the chat request answers 503 rather than offering stale tools.

### Admin lifecycle tools

The LocalAI Assistant catalog has no add-on lifecycle tool (install, enable,
disable, upgrade, uninstall, library changes). This is deliberate. The
Assistant is one process-wide server with no per-call principal, and its
mutating tools rely on the model asking the person for confirmation in prose.
Add-on lifecycle changes are administration with persistent effects on data
and processes, so they are not exposed that way. Listing and status are read
with `GET /api/addons/apps` and the `addons-installed` management read, which
need a named administrator. A conversational lifecycle tool needs a per-call
principal and a host approval card first; it is not part of this release.
REST-to-MCP parity for `/api/addons` is intentionally partial for that reason,
and `TestToolHTTPRouteMappingComplete` stays unchanged. The UI-only resource
routes (create, rename, share, delete, selection) are likewise not tools:
sharing and deletion are never tools, and the consumer matrix test scans the
catalog for any `/api/addons` route.

## How creation survives a crash

Creation has three steps, in this order:

1. LocalAI records a pending resource for the creator. A pending resource is
   invisible to everyone, its creator included.
2. The addon creates the database file. It builds the file under a temporary
   name and links it into place only when it is complete.
3. LocalAI commits the resource, which makes it usable.

The addon operation ID is derived from the reserved resource ID, so repeating
a step is one operation. If LocalAI stops between steps, it settles every
pending resource at the next start by calling `resource-exists`:

- No database: the pending record is dropped.
- Database present: the resource is activated for the creator.
- The addon is installed but cannot answer: LocalAI stops its startup with a
  recovery-required error. It never guesses.

Deleting a database follows the same rule from the other side. LocalAI removes
the record of a deleting database without calling `databases-delete` only when
`resource-exists` answers that the database is gone. If the addon cannot
answer, the deletion stays pending and is retried.

A database file without a record cannot be reached by anyone. A creation whose
outcome is uncertain keeps its reservation, and that reservation counts against
the per-user limit of pending resources until its receipt is found, the person
who asked stops waiting, or the next start. Stopping to wait drops the
reservation only when `resource-exists` says there is no database. A pending
chat proposal keeps its reservation, so a restart does not remove a database
you can still confirm.

## Managing databases from the UI

Creating, renaming, sharing and deleting a database are the person's own
actions in the Tables pages, through the routes described in
[UI-only resource management](../addons/#ui-only-resource-management). They
are not tools: a model never sees them and sharing and deletion are never
proposals.

- `databases-create` and `databases-rename` are the same tools that chat uses,
  run by the person's explicit request with no approval card.
- `databases-delete` is an admin tool. The manifest lists it in
  `capabilities.adminTools` and not in `tools`, so it has no model definition and
  the declared-tool rule refuses it on every other path. The addon runs it only
  under the host action `delete` and only for the revision the owner confirmed.
  It removes the file and its journal files and records the operation in the
  ledger; repeating the operation, also after a crash between the removal and the
  receipt, gives the same receipt.
- The manifest `capabilities.resources` maps `create`, `rename`, `inspect` and
  `delete` to these tools, with the noun `database`.

A database is shared whole, as `viewer` or `editor`, with named users picked by
account. An owner and an editor see the same rows; only the owner changes the
schema, shares, renames and deletes. Deleting asks the owner to type the name,
removes the data and every grant, and never uninstalls the addon.

### The database of a chat

The owner keeps which database each conversation works on, per person. A tool
call that names no `resourceId` uses it, so a person can say "add a row" after
choosing a database once. The model never sets it, and the owner authorizes it
for the person on every call: a database whose grant was withdrawn or that was
deleted is refused like an unknown one. With none selected the tool answers a
`select_resource` result and runs nothing.

## The Tables pages

Tables ships its own UI as a standalone ES module in the package. It is loaded by
the host after the host was built, so installing or updating Tables changes the
pages without a rebuild of LocalAI. The Add-ons drawer shows a **Tables** tile.

| Where | What |
|---|---|
| `Databases` list | Your databases and the ones shared with you, each with your role, who shared it and, for the owner, how many people have access. Create, rename and delete (owner). |
| A database | The tables as tabs, the typed grid of the selected table, a structured filter, sort by column, cursor paging and the row actions of your role. |
| Schema (owner) | Create, rename and drop tables and columns. A drop says what is lost and asks for the name to be typed. |
| Share (owner) | The whole database is shared as `viewer` or `editor` with named accounts found by a picker. Local-owner mode says that sharing is not available and makes no sharing call. |
| `Select database` | A chat action in the composer menu: the databases you may use with your role, the one selected for this conversation, and a way to clear it. |

What each role sees:

| Role | Rows | Schema | Share, rename, delete |
|---|---|---|---|
| Owner | read, add, edit, delete | yes | yes |
| Editor | read, add, edit, delete | read only | no |
| Viewer | read | read only | no |

Hiding a control is never the control. The server checks the role of every call
again, and a crafted write of a viewer, a schema change of an editor or a share
of anybody who is not the owner is refused with the answer of an unknown
database.

### Editing rows

- **Typed editors.** Text, whole number, number, yes or no, and date and time
  (entered and shown in UTC). A value of the wrong type is refused in the form
  and nothing is sent. Integers beyond 2^53 are kept exact. An optional column
  has an *Empty* switch that sends `null`.
- **Filters.** A list of conditions over the table's own columns, joined by
  all or any, with the operators the column type supports. They become the
  `filter` of `rows-query`; there is no free text and no expression. The row
  ID is filterable as `_id`.
- **Paging.** The add-on's cursor goes forward and the page keeps the cursors it
  used to go back. A cursor that the add-on no longer accepts (the data
  changed) starts again at the first page and says so.
- **Batches.** *Add rows* takes up to 100 rows. A selection is changed or
  deleted in steps of at most 1000 row IDs, each step saved on its own with its
  own operation ID. If a later step fails, the page says how many rows were
  changed before it.
- **Revisions.** Every change sends the revision the page read. If another
  person changed the database, the save is refused as stale, the form keeps what
  you typed and offers *Reload and apply again*, which reads the revision and
  sends the same change once more under a new operation. An unknown outcome
  keeps the operation ID, so repeating is the same change and never applies
  twice.

A row editor sends only the fields you changed. A datetime keeps its stored
fraction of a second (up to nine digits) when you edit another field, and two
spellings of one instant, such as another UTC offset, are no change.

Row and schema changes made in these pages are the person's own action. They
need no confirmation card and are not proposals, and they still go through the
host's `POST /api/addons/resources/{resourceId}/apply`: the role of the action,
the declared tool definition, the admission of the resource, the revision and
the receipt (see [Changing data and schema from the UI](../addons/#changing-data-and-schema-from-the-ui)).

### The database of a chat

Choosing a database in the `Select database` action stores it for you, the
conversation and the add-on. A chat tool call that names no database uses it,
and the chat sends the conversation ID in the `X-LocalAI-Conversation-Id` header
only when the conversation has add-on tools enabled. Two people in the same
conversation ID, or one person in two conversations, never share a selection. A
database that is no longer shared with you is refused like an unknown one.

### Package and build

The UI source is `addons/tables/ui`. `node addons/tables/ui/build.mjs` writes
`dist/ui/tables.js`, `dist/ui/tables.css` and `dist/ui/icon.png`, which the
manifest names under `capabilities.ui` (`entry`, `css`, `icon`, `pages`,
`navigation`, `mainPage`, `chatActions`). The package declares no
dependencies, so it has no lockfile: the build uses the bundler that the host UI
already has installed (`core/http/react-ui/node_modules`) and needs nothing to
download. `node addons/tables/ui/test/run.mjs` runs its unit tests. The module imports
nothing: React, the components, the typed calls and the navigation arrive through
the host SDK, and every class of the style sheet starts with `tbl-`. The UI is
translated to the languages of the host; a missing key reads the English text.

## Schema operations

Only the owner of a database changes its schema. LocalAI refuses the schema
action for every other role before it calls the addon, and the addon refuses
a schema tool that does not arrive as a schema action with an actor and the
preview or apply scope, so a caller of the runtime transport cannot use a row
action to change a schema.

Tables and columns have a label and a stable internal identifier. The label is
what people see. It is 1 to 64 characters, with no control, format or
bidirectional characters, no leading or trailing space, and it is unique in its
scope ignoring case. The internal identifier (`t_...`, `c_...`) is random and
never changes. Labels are never part of an SQL statement, so a rename touches
only the catalog and no label can inject SQL.

| Logical type | Stored as | Default value |
|---|---|---|
| `text` | TEXT | a string up to 1024 bytes, no control characters |
| `integer` | INTEGER | a 64-bit integer |
| `real` | REAL | a finite number that SQLite reads back exactly |
| `boolean` | INTEGER, 0 or 1 | `true` or `false` |
| `datetime` | TEXT, UTC | an RFC 3339 time, stored in UTC |

A default is validated for its type and written through a strict literal
encoder, never copied from the request. There is no type change, trigger,
function, expression, computed column, view or raw SQL.

- A required column needs a default when the table has rows. On an empty table
  it needs none.
- A unique column is a unique index. A default that would repeat in more than
  one row of a unique column is refused. Several NULL values are allowed.
- Dropping a column rebuilds the table inside the same transaction: create the
  new table, copy the rows, drop the old one. A failure leaves the original.
- Limits: 50 tables per database, 64 columns per table.

## Revisions, receipts and previews

Each database has a revision pair, `schema` and `data`. Every schema change
raises `schema` by one. The preview returns the revision it was made against,
and apply must send that revision back. If the database moved, apply is
rejected with `{"error":"stale","revision":{...}}` and nothing changes. Both
numbers must match, because the data a drop destroys depends on the rows.

The change, the new revision and the operation receipt commit in one SQLite
transaction. After a crash the receipt is either in the database or the change
did not happen. The same operation ID with the same request returns the same
receipt and never applies twice. The same ID with another request is a
conflict. An operation that was refused leaves no receipt, so its ID can be
used again. If a reply is lost, LocalAI asks for the receipt, which the addon
finds in the database even when its own ledger was not updated.

A preview runs the change in a transaction that it rolls back, so it accepts
only what apply would accept. Its summary has this shape:

```json
{"count": 1,
 "sample": [{"action": "drop column", "table": "Orders", "column": "Total", "rowsLost": 42}],
 "dataLoss": {"rows": 42, "columns": 1, "tables": 0}}
```

`rowsLost` of a column counts the rows that hold a value in it. For a table it
counts all rows and `columnsLost` its columns. LocalAI shows `count` and
`sample` in the confirmation card.

## Row queries

`rows-query` and `rows-aggregate` run under the read action, so viewers can use
them. The addon builds each statement from internal identifiers and binds every
value as a parameter. A column or table label is only looked up in the catalog;
it never becomes SQL text. There are no joins, raw SQL, expressions, functions
or subqueries.

```json
{"tool": "rows-query", "table": "Orders", "columns": ["Item", "Qty"],
 "filter": {"and": [{"column": "Qty", "op": "gte", "value": 2},
                    {"or": [{"column": "Item", "op": "starts-with", "value": "pen"},
                            {"column": "Item", "op": "is-null"}]}]},
 "sort": [{"column": "Qty", "desc": true}], "limit": 50}
```

- **Rows** come back as `{"id": n, "values": [...]}` in the order of the
  `columns` of the reply. `id` is the stable row ID of the table. It never
  changes and is never reused after a delete. Filters and sorts may name it with
  the label `_id`; no column may use that label. Omit `columns` to select all.
- **Filter** is a tree of `and`, `or` and conditions: at most 8 levels and 64
  nodes. Operators are `eq`, `ne`, `lt`, `lte`, `gt`, `gte`, `in` (1 to 100
  `values`), `is-null`, `is-not-null`, and for text columns `contains`,
  `starts-with` and `ends-with`. A boolean column supports `eq`, `ne` and the
  null tests only. A node is exactly one of `and`, `or` or a condition; unknown
  fields in the arguments are refused.
- **Text matching.** `eq`, `ne` and the order operators on text are
  case-sensitive and compare bytes. `contains`, `starts-with` and `ends-with`
  are ASCII case-insensitive and treat `%`, `_` and `\` in the value as plain
  characters. The value is at most 1024 bytes.
- **NULL.** A comparison (`eq`, `ne`, `lt`, ..., `in`) never matches a NULL
  value, so `ne` does not return rows where the column is empty. Use
  `is-null` for those, for example `or` of `ne` and `is-null`. A comparison
  with a `null` value is refused. In a sort, NULL comes first ascending and last
  descending. Aggregates ignore NULL values; `count` without a column counts
  rows.
- **Sort** takes at most 4 columns. Rows with equal keys are ordered by row ID,
  so a page boundary is stable.
- **Pages.** `limit` is 1 to 500 and defaults to 50. When more rows match,
  `hasMore` is true and `cursor` is set; send it back with the same query to get
  the next page. A cursor is valid only for the same query and the same database
  revision; after any change the addon answers that the cursor is no longer
  valid and the caller starts again. There is no total count. Offsets beyond
  100000 rows are refused, so add a filter. A page is also cut at 512 KiB of
  JSON, and `hasMore` is set.
- **Values in JSON.** text is a string, integer a JSON integer, real a finite
  number, boolean `true` or `false`, datetime an RFC 3339 string. Datetimes are
  stored in UTC with nine fractional digits and returned in UTC. Integers can be
  sent as a number or a decimal string; integers beyond +/- 2^53 are returned
  as strings so no precision is lost.
- **Aggregates.** `aggregates` holds 1 to 8 of `{"fn": "count" | "sum" | "avg" |
  "min" | "max", "column": "..."}`. `sum` and `avg` need an integer or real
  column; `min` and `max` work on integer, real, text and datetime. `groupBy`
  names one column; groups are ordered by the group value and limited to 500,
  with `truncated` set when more exist. A sum that overflows 64 bits is an
  error, not a wrapped number.
- **Cancellation.** A query is bound to the request context and to a per
  operation deadline; a cancelled or slow query is interrupted and returns an
  error. Each read sees one snapshot of one revision.

## Row changes

`rows-insert`, `rows-update` and `rows-delete` are changes under the host
`write` action: Editors and Owners may propose them and the proposer confirms
them in the host card. The addon refuses a row tool that arrives under another
action and a schema tool that arrives under the `write` action.

- **Insert** takes 1 to 100 rows; each row maps column labels to values. A
  column that is left out gets its default, or NULL. A required column without a
  default must be given. An explicit `null` for a required column is refused
  even when it has a default. The whole batch commits or none of it.
- **Update and delete** name their rows by exactly one of `ids` (distinct
  positive row IDs, all of which must exist) or `filter`. There is no way to
  touch every row without a filter that selects them, and no truncate. One
  operation changes at most 1000 rows; a broader filter is refused with a
  request to narrow it. An operation that matches no row is refused.
- **Constraints.** Type, required and unique rules are enforced by the
  database. A violation rejects the whole operation, names the column by its
  label and leaves the data unchanged.
- **Preview.** The preview runs the same code in a transaction that it rolls
  back. It reports the exact `count`, a sample of the first 10 rows (for update,
  the values before and after of the changed columns; for delete, the full
  rows), an `affectedHash` of the exact row ID set, and `dataLoss.rows` for a
  delete. A change that would break a unique rule fails at preview, so the card
  never offers it.
- **Receipt.** The payload holds `count`, the affected `ids` and `affectedHash`.
  The receipt commits in the same transaction as the rows and the data revision
  step. LocalAI reports success only from the receipt.

### Revisions and staleness

The revision is conservative and per database. Every committed row change
raises `data` by one in the same transaction; a schema change raises `schema`.
Apply compares both numbers with the revision of the preview inside the
transaction. If either moved, the change is rejected with
`{"error":"stale", ...}`, and a new proposal and a new preview are required.
Because the revision is unchanged, the set of rows a filter selects is the set
that was previewed: a filter is never evaluated against rows that arrived after
the preview. A write to any table of the database makes a pending row proposal
stale, even when it touched unrelated rows. Two proposals made on the same
revision cannot both apply: exactly one wins and the other is stale.

Row changes use the receipt rules above: the same operation ID with the same
request replays one receipt, another request under the same ID is a conflict,
and a rejected operation does not use up its ID.

## CSV export

`rows-export` writes the rows a table, a filter and a sort select to a CSV
file. It is a read: it runs under the host `read` action, so an Owner, an
Editor and a Viewer may export what they can read, and it needs no approval
card. A chat tool call and the **Export CSV** button of a table page run the
same tool.

```json
{"tool": "rows-export", "table": "Orders", "columns": ["_id", "Item", "Qty"],
 "filter": {"column": "Qty", "op": "gte", "value": 2},
 "sort": [{"column": "Qty", "desc": true}], "maxRows": 5000}
```

`columns`, `filter` and `sort` follow the rules of [Row queries](#row-queries).
`columns` may name `_id` to write the row ID as a column; empty exports every
column in schema order. `maxRows` is optional. There is no `limit` and no
`cursor`: an export is the whole result, not a page.

### What the file holds

- One snapshot. The export reads the database in one read transaction, so every
  row comes from the same revision. A change that commits while the export
  runs is not in the file, and the export never blocks it. The file is written
  row by row through a small buffer; its memory use does not grow with the
  number of rows.
- RFC 4180 records with a header row of the column labels and CRLF line ends.
  A field is quoted when it holds a comma, a quote, a CR or LF, or starts or
  ends with a space or tab. A quote inside a field is doubled.
- UTF-8, with no byte order mark. Text is written as it is stored. A
  spreadsheet that guesses the encoding from the file may need to be told to
  read it as UTF-8.
- NULL is an empty field with no quotes. An empty text value is `""`. A reader
  that does not tell the two apart reads both as an empty value.
- Booleans are `true` or `false`. Integers are decimal and exact to 64 bits.
  Reals are the shortest decimal that reads back as the same number; very large
  or very small values use an exponent. Date-times are RFC 3339 in UTC.

### Spreadsheet formulas

A spreadsheet runs a cell that starts with `=`, `+`, `-`, `@`, a tab or a
carriage return as a formula. The export therefore writes a single quote
before every **text** cell, and every column label, that starts with one of
those characters, as the OWASP guidance recommends: `=1+1` is written as
`'=1+1`. A spreadsheet shows the quote as the text marker, not as part of the
value.

Limits of this policy:

- Cells of integer, real, boolean and date-time columns are never prefixed. A
  negative number stays a number (`-4`).
- The file now holds `'=1+1`, not the stored `=1+1`. A program that reads the
  CSV back must remove that quote itself; the export does not mark which cells
  it changed. A text value that really starts with `'` and a formula are
  therefore not told apart after the round trip.
- A text value such as `-5 apples` also gets the prefix.
- The policy protects the file against being run as a formula in a spreadsheet.
  It is not a defence for a program that treats CSV as trusted input.

### Backup, upgrade and restore

The databases are SQLite files under the data directory of the add-on, with one
ledger. The host backs them up with `VACUUM INTO` while the add-on is stopped, so the
copy is consistent and holds no write-ahead log (see "Data recovery" in the add-ons
reference). Backup, restore and purge are not tools and not part of the manifest:
the administrator's page uses the recovery routes, and no model can reach them.

Tables records the format of its data. `store.FormatVersion` is the format this
build writes; it is kept in the `format` key of each database and, for the whole store,
in the ledger. At start the executable calls `Upgrade`, which compares them, runs
`Migrate(ctx, fromVersion, toVersion)` when the data is older and writes the progress
to `migration.json`. Each database migrates in one transaction, so a step that fails
leaves that database exactly as it was, and the store version is recorded last, so a
run that stopped resumes. Data in a newer format than the executable reads stops the
start with exit status 9 and is left as it is: the host then keeps the add-on
unavailable instead of running it on data it cannot read. No step exists yet because
the first format is the only one.

## Limits

| Limit | Value |
|---|---|
| Rows in one export | 1,000,000 |
| Size of one file | 64 MiB, and never more than the host's file size bound |
| Time for one export, snapshot to last byte | 25 seconds |
| Exports staged at once in the addon | 16, at most 4 for one person |

A result past a bound makes the tool answer `{"exported": false, "error":
"too-large"}` and creates no file; add a filter to export less. A `maxRows`
above the row bound is refused the same way, not clipped. A staged export that
the owner did not fetch within ten minutes expires; the addon removes expired
files when it handles the next export or fetch, and removes every staged file
when it starts.

### How the file reaches the person

There is no separate asynchronous export job in the addon. The CSV is written
inside the read call, within the 25 second bound, because the owner needs the
exact size before it moves a byte. A result that does not fit those bounds
fails with `too-large` instead of running longer. The transfer from the addon
to the owner's private store is the background job: the read returns at once
with the artifact in state `transferring`, and the page and the chat ask the
host for its state until it is final. Cancelling the transfer, a revoked share
and a stopped addon end it and remove the partial bytes.

The addon writes the CSV into its private directory and answers with one
output description: a name (`<table>.csv`), the media type `text/csv`, the exact
size and an opaque handle bound to the operation, the person and the database.
LocalAI streams the bytes from the addon, checks the size and replaces the
handle by its own artifact identifier. The page, the chat and the model get
that identifier, the name, the size and the expiry. They never get the addon's
handle, a path or a URL. The transfer runs in the background; the artifact
shows `transferring`, then `complete`, `failed` or `cancelled`, and an export
that did not complete never becomes downloadable.

- `GET /api/addons/artifacts/{id}` is the transfer state, name, size and
  expiry. `GET /api/addons/artifacts/{id}/download` is the file, as an
  attachment with `X-Content-Type-Options: nosniff` and
  `Cache-Control: private, no-store`. Both need the person's own session or
  API key.
- The file is private to the person who exported it. Sharing the database does
  not share a file. A person who is not the principal, an unknown identifier
  and an expired file all get the same 403.
- Access to the database is checked again when the transfer completes and at
  every download, and a revocation stops a download in progress. After a
  viewer's share is withdrawn the viewer's earlier files stop downloading.
  A file already downloaded cannot be recalled.
- A file expires one hour after it was made, and the person's quota is
  100 files. Expiry removes the file only. The database and any backup are
  not touched.
- A disabled addon does not serve downloads; enabling it again serves the
  files that have not expired.
- In single-owner mode there is one local owner and no separation between
  people, as for every other operation.

### In the pages and in chat

A table page has an **Export CSV** button for every role. It exports the table
with the filter and sort of the view, optionally with the row ID, and then
shows the host's file card: name, size, expiry and the download button. In
chat, an export tool call is answered with the same card. The chat stores only
the artifact identifier; after expiry the card says the file expired.

## Limits

- One name per database, no uniqueness rule across databases.
- Rename needs the schema revision from the preview. A changed schema makes
  the rename rejected, and a new preview is required.
- The data directory holds the databases and a small operation ledger. Both
  are kept when the addon is disabled or uninstalled.

## How to try it

The addon is not published in a catalog yet. Acceptance builds the package locally and
installs it from a catalog on loopback; it never pushes, tags or signs. Build the executable, put it in a
package with `manifest.json` from `addons/tables`, and install the package from
a local source as described in [Addon contracts](../addons/). The suite
`sh scripts/addons/verify.sh tables-databases` runs the store and the real
executable under the real supervisor. The suite
`sh scripts/addons/verify.sh tables-schema` covers the schema operations,
revisions and receipts.
The suite `sh scripts/addons/verify.sh tables-rows` covers queries, filters,
aggregates and confirmed row changes with the real executable.
The suite `sh scripts/addons/verify.sh tables-tools` covers the model tools,
the consumer inventory, the pending proposal list and the approval card in a
real browser against the real owner.
The suite `sh scripts/addons/verify.sh tables-csv` covers the CSV export: the
snapshot and encoding rules, the output handles, the private artifacts of the
real owner and the download from the owner and two frontends.
The suite `sh scripts/addons/verify.sh tables-ui` covers the pages: the node
tests of the field codecs, the filter builder, the batch bounds and the build,
the UI-intent change route against the real owner, and a browser spec with
real sessions for the roles, private defaults, sharing and a concurrent revoke,
destructive deletion, a stale tab after an upgrade or a disable, the database of
a chat for two conversations and two people, keyboard, 400 pixels, both themes,
clearing on sign-out and local-owner mode.
The suite `sh scripts/addons/verify.sh tables-acceptance` is the end-to-end
acceptance (see [Acceptance](../../development/addon-testing/#tables-acceptance)).
The suite `sh scripts/addons/verify.sh tables-admin` covers the UI-only
management of databases with the real owner, real accounts and the real
executable: private defaults, the owner, editor and viewer matrix, sharing and
the user picker, deletion and its crash reconciliation, the database of a
conversation, and the answers of a forwarding frontend.
