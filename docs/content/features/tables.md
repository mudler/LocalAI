---
title: Tables
---

## Status

Tables is a first-party addon for LocalAI. This page describes the database
lifecycle that exists today: creating, renaming and inspecting databases. Rows,
columns, CSV results and the Tables user interface are not available yet.

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
| Size | at most 1 GiB per file |
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
| `databases-inspect` | read | Name, revision and table names of one database. |
| `resource-exists` | read | `{"exists": bool}`; used by LocalAI to settle interrupted creations. |

Tool names use hyphens because LocalAI manifests accept only
`[a-z][a-z0-9-]*` identifiers.

There is no tool that lists every database. The addon only knows the IDs that
LocalAI sends it. LocalAI lists the databases a person may see from its own
resource records, so a listing can never show a database the person has no
grant for.

Changes made from chat are proposals: the model asks, LocalAI previews the
change, and a person confirms it in the host confirmation card. A rename uses
the standard proposal path. Sharing and deleting a database are not tools.

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
the per-user limit of pending resources until the next start.

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
executable under the real supervisor.
