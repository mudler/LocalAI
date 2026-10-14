---
title: Tables
---

## Status

Tables is a first-party addon for LocalAI. This page describes the database
lifecycle, the typed schema operations, and structured row queries and
confirmed row changes that exist today. CSV results and the Tables user
interface are not available yet.

Tables is a separate executable under `addons/tables`. It is not linked into
the LocalAI server, and nothing of it runs unless an administrator installs
and enables the addon. It reuses the root Go module and needs CGO for SQLite.

```
go build ./addons/tables/...
```

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

A database file without a record cannot be reached by anyone. A creation whose
outcome is uncertain keeps its reservation, and that reservation counts against
the per-user limit of pending resources until its receipt is found, the person
who asked stops waiting, or the next start. Stopping to wait drops the
reservation only when `resource-exists` says there is no database. A pending
chat proposal keeps its reservation, so a restart does not remove a database
you can still confirm.

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

## Limits

- One name per database, no uniqueness rule across databases.
- Rename needs the schema revision from the preview. A changed schema makes
  the rename rejected, and a new preview is required.
- The data directory holds the databases and a small operation ledger. Both
  are kept when the addon is disabled or uninstalled.

## How to try it

The addon is not published in a catalog yet. Build the executable, put it in a
package with `manifest.json` from `addons/tables`, and install the package from
a local source as described in [Addon contracts](../addons/). The suite
`sh scripts/addons/verify.sh tables-databases` runs the store and the real
executable under the real supervisor. The suite
`sh scripts/addons/verify.sh tables-schema` covers the schema operations,
revisions and receipts.
The suite `sh scripts/addons/verify.sh tables-rows` covers queries, filters,
aggregates and confirmed row changes with the real executable.
