# Workspace layout: one `LocalAI/` folder for the settings files

Date: 2026-10-30. Status: approved by the owner (final decision on the layout).

## Problem

The top of a user's home folder mixes the user's own files with the files that
LocalAI reads: `persona.json`, `user.json`, `shortcuts.json`, `home.json`,
`ui.json`, next to `pages/` and `Chats/`. A user who opens **Files** cannot
tell which files are theirs and which ones change how LocalAI works. Deleting
`ui.json` by mistake resets the theme without a word.

## Layout

```
/                      the user's own files first
  LocalAI/             managed: the settings LocalAI reads
    persona.json
    user.json
    shortcuts.json
    home.json
    ui.json
  Chats/               managed: one folder per chat (unchanged)
  pages/               unchanged: pages/<name>/
  data/                unchanged: the data files of Home widgets
```

- `LocalAI/` is visible, not hidden. The files are the interface: the user,
  the settings pages and the assistant edit the same files.
- `pages/`, `Chats/` and the widget data files (`data/*.json`) stay where they
  are. Only the five well-known files move.
- The page consent records (`pages/consents.json` of the JSON chat store) are
  not in the user's home and do not move.

## Server

- `wellknown.File(n)` is `/LocalAI/<n>.json`. `wellknown.Dir` is `/LocalAI`.
  Every reader and writer goes through `File`, so the routes
  `/api/chats/wellknown/:name` keep their URL and use the new place. A read
  answers `path: "/LocalAI/persona.json"`.
- The JSON Schemas keep their `$id`; their `title` names the new path.
- The change feed sends the path of the change, so a client that watches a
  settings file watches `LocalAI/ui.json`.
- The file listing (`GET /api/chats/files`) marks the managed folders and the
  files in them: `"managed": "localai"` for `LocalAI/` and its files,
  `"managed": "chats"` for `Chats/`. The UI uses this marker; it does not
  guess from names.

## Migration

The migration is lazy and per user. It runs before every access to a settings
file of that user (read, write, revert, versions) and before the listing of
the home folder in **Files**. It costs one `Stat` per file when there is
nothing to do.

For each of the five files:

| Top level (old) | `LocalAI/` (new) | What happens |
|---|---|---|
| missing | any | nothing |
| present | missing | the old file is **moved** to `LocalAI/` (one change set, "Move persona.json into LocalAI/"). A move keeps the node, so the file's history and Undo stay. |
| present, written after the migration | present | the old file was written after its move (an old page, a script, the assistant on an old prompt): its content is written to the new file as a new version, and the old file goes to the Trash, in one change set. The new file's earlier content stays in its versions. A log line says so; no toast. |
| present, not written after a migration | present | both stay as they are. The new file wins. A log line names the old file. The user decides. |

"Written after the migration" means: the history of the new file holds a move
from the old path, and the old file changed at a later revision. With no such
move (the user had both files before), nothing is overwritten.

Concurrency: several frontends and several requests may migrate the same user
at once. Each step is a single atomic store operation (a `Move`, or a
transaction with a version check), so the first one wins and the others see
`ErrNotFound`, `ErrExists` or a version conflict, which they ignore. No
advisory lock is needed: the state is the shared workspace itself (the
"stateless" mode of `.agents/distributed-state.md`). The migration acts as the
user, with the user's rights, inside the user's own home only. A principal
that cannot own a home (the admin, `@everyone`) is refused by `Abs`, as
before.

Versions: the versions list of a settings file follows the move. When the
history of the new path reaches the move from the old path, it goes on with
the history of the old path before the move, so the versions from before the
migration stay visible and can be reverted to.

## Compatibility window

Until the next minor release:

- A read uses the old top-level file when the new one is missing and the move
  failed (for example a store error). The answer names the old path.
- A write always goes to `LocalAI/`.
- A file written to the old path after the migration is picked up on the next
  access, as in the table above.

The whole window is one file, `core/services/studio/wellknown/legacy.go`, with
one entry point, `adoptTopLevelFiles`, and the legacy-path read. Removing the
window means deleting that file and its call sites (`grep adoptTopLevelFiles`).
The UI keeps the old paths in its change-note map so the notes of old chats
still read well; that map entry is marked the same way.

## Surfaces that change

- `core/services/studio/wellknown`: paths, migration, versions across the move.
- `core/schema/chats.go`: `ChatsFile.Managed`.
- `core/http/endpoints/studio/files.go`: the marker, the migration on the
  home listing.
- The built-in skill `localai-workspace`: every path is `LocalAI/<file>`.
- React UI: `wellKnown.js` (paths, feed patterns), `changeNotes.js` (tags for
  `/LocalAI/...`, old paths kept for old transcripts), the Home layout editor
  (list of data files), the "stored in" labels of the Account tabs, shortcuts
  row and Home, the Files page.
- Docs: `make-localai-yours.md`, `chats.md`, `workspace-feed.md`, `pages.md`.
- Tests and e2e specs that name the old paths.

## Files UI

- The home folder lists the user's own files and folders first, by name. The
  managed folders come last, under a small group heading **Managed by
  LocalAI**. A group, and not only the sort order, because the user should
  see at a glance that these folders are not theirs to tidy up.
- Each managed row has a small marker, "managed by LocalAI", and a one-line
  description: for `LocalAI/`, "Settings LocalAI reads: persona, profile,
  shortcuts, Home, theme."; for `Chats/`, "One folder per chat, with its
  files."
- Inside `LocalAI/` the folder header shows the same line.
- Deleting or renaming `LocalAI/`, `Chats/` or a file in `LocalAI/` asks
  first, and says what stops working ("LocalAI will forget your persona,
  profile, shortcuts, Home layout and theme. You can restore it from the
  Trash."). Cancel changes nothing.

## AGENTS.md nudge

A user can leave rules for the assistant in an `AGENTS.md` file. The model
gets a pointer, never the contents:

> Rules for this folder are in /workspace/home/<path>/AGENTS.md. Read that file before you start.

- At the start of a turn (the turn note, not the system prompt, so the harness
  does not restart): the chat's own folder (`Chats/<chat folder>`), the
  folders above it, and the home root. Nearest first, at most two paths named
  (about 25 tokens each), only when such a file exists.
- On a file tool call (`list_files`, `read_file`, `write_file`, `move_file`):
  the first time in a turn that the call touches a folder whose `AGENTS.md`
  (in the folder or above it, up to the home root) was not named yet this
  turn, the same line is added to the tool result. Once per folder per turn.
  Reading the `AGENTS.md` itself counts as named.
- The existence checks use the files service as the chat's actor, cached per
  turn.
- A trigger run already gets the rules of its folder and the folders above it
  in its first message (`triggers.RulesAs`): those folders count as named, so
  the run never gets the pointer for them.
- This is a per-turn note, not an offer: the offer budget of the hints
  service does not change.

## Tests

- Ginkgo, `wellknown`: old only (moved, history kept, Undo of the move brings
  the old file back), both present (new wins, old untouched), new only, a
  write to the old path after the migration (picked up, old in the Trash), two
  goroutines migrating at once, a user with no files, another user's files
  untouched, the compatibility read when the move fails.
- Ginkgo, `studio`: the nudge (present, absent, nearest first, cap of two,
  once per folder per turn, a trigger run does not get it twice).
- Ginkgo, routes: the listing marker.
- Node unit tests: the Files sort and grouping, the confirmation text, the
  change-note tags.
- Playwright with a stubbed API: the marker and the group, the extra
  confirmation; the existing specs that name the old paths are updated.
