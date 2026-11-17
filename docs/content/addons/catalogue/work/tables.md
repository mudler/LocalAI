+++
title = "Tables"
description = "Tables is an add-on with databases that a chat can read and change, and that you can share."
keywords = ["tables","database","add-on"]
weight = 30
aliases = ["/features/tables/"]
+++

Keep structured data in a database of tables that you own, and ask a chat to read and change it. You can share a database with other people on your server and export a table as a CSV file. A new database is private to you until you share it.

For example, make a database called "Reading list" on the **Databases** page and add a table with the columns "Title" and "Status". Open a chat, switch on **Tables** in the add-ons control, pick the database with **Select database** and ask: "Add The Dispossessed with the status reading." The chat adds the row. Ask "How many books are done?" and it counts them. A change that a chat made can be undone.

## At a glance

| Topic | Detail |
|---|---|
| What it adds | Databases of tables that you own or share: typed rows, filters, a schema and sharing. A chat reads and changes them with tools. |
| Who installs it | An administrator, from **Add-ons**, **Find add-ons**, with **Install and turn on**. With authentication off, you are the administrator. |
| Who turns it on | A person needs the `addons` feature. It is off by default for new users on a server with accounts. The person switches the add-on on for a chat in the **Tools** menu. A saved agent uses it only when you name it in the agent. |

## Managing databases from the UI

Creating, renaming, sharing and deleting a database are the person's own
actions in the Tables pages, through the routes described in
[UI-only resource management]({{% relref "/addons/contract-reference/proposals-and-resources#ui-only-resource-management" %}}). They
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
pages without a rebuild of LocalAI. The My add-ons page shows a **Tables** card.

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
the receipt (see [Changing data and schema from the UI]({{% relref "/addons/contract-reference/proposals-and-resources#changing-data-and-schema-from-the-ui" %}})).

### The database of a chat

Choosing a database in the `Select database` action stores it for you, the
conversation and the add-on. A chat tool call that names no database uses it,
and the chat sends the conversation ID in the `X-LocalAI-Conversation-Id` header
only when the conversation has add-on tools enabled. Two people in the same
conversation ID, or one person in two conversations, never share a selection. A
database that is no longer shared with you is refused like an unknown one.

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

`columns`, `filter` and `sort` follow the rules of [Row queries]({{% relref "/addons/tables-reference#row-queries" %}}).
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

<script>
var m={"status":"../../../tables-reference/#status","deployment-trust-and-limits":"../../../tables-reference/#deployment-trust-and-limits","what-a-database-is":"../../../tables-reference/#what-a-database-is","tools":"../../../tables-reference/#tools","databases-of-a-saved-agent":"../../../tables-reference/#databases-of-a-saved-agent","database-discovery":"../../../tables-reference/#database-discovery","bundled-discovery-skill":"../../../tables-reference/#bundled-discovery-skill","model-tools-and-approval-cards":"../../../tables-reference/#model-tools-and-approval-cards","names-and-schemas":"../../../tables-reference/#names-and-schemas","what-a-call-does":"../../../tables-reference/#what-a-call-does","the-approval-card":"../../../tables-reference/#the-approval-card","consumers":"../../../tables-reference/#consumers","undo-of-a-chat-change":"../../../tables-reference/#undo-of-a-chat-change","several-frontends":"../../../tables-reference/#several-frontends","admin-lifecycle-tools":"../../../tables-reference/#admin-lifecycle-tools","how-creation-survives-a-crash":"../../../tables-reference/#how-creation-survives-a-crash","package-and-build":"../../../tables-reference/#package-and-build","schema-operations":"../../../tables-reference/#schema-operations","revisions-receipts-and-previews":"../../../tables-reference/#revisions-receipts-and-previews","row-queries":"../../../tables-reference/#row-queries","row-changes":"../../../tables-reference/#row-changes","revisions-and-staleness":"../../../tables-reference/#revisions-and-staleness","backup-upgrade-and-restore":"../../../tables-reference/#backup-upgrade-and-restore","how-the-file-reaches-the-person":"../../../tables-reference/#how-the-file-reaches-the-person","how-to-try-it":"../../../tables-reference/#how-to-try-it"};var h=decodeURIComponent((location.hash||"").slice(1));if(h&&m[h]&&!document.getElementById(h)){location.replace(m[h]);}
</script>
