+++
disableToc = false
title = "Make LocalAI yours"
weight = 23
url = "/features/make-localai-yours/"
+++

You can change how LocalAI looks and behaves for you by asking for it in a
[chat](/features/chats/): the name and the instructions of the assistant, what
it knows about you, the shortcuts of the message box, the widgets on Home, the
look, and small tools of your own (pages). Every change is a file in your home
folder, so it has a history, it can be undone, and the settings pages edit the
same files. The settings files are in the `LocalAI/` folder of your home;
pages are in `pages/`.

The files need the chats and files of the server (see [Chats](/features/chats/)).

## What you can change

| What | Say, for example | File |
|---|---|---|
| The assistant's name and how it answers | "Call yourself Ada and keep answers short" | `LocalAI/persona.json` |
| What the assistant knows about you | "Call me Marta. I am a designer in Milan" | `LocalAI/user.json` |
| Shortcuts in the message box | "Make this a shortcut" | `LocalAI/shortcuts.json` |
| The widgets on Home | "Show my open tasks on Home" | `LocalAI/home.json` and a data file |
| The look | "Use the light theme and a compact layout" | `LocalAI/ui.json` |
| A small tool of your own | "Make me a task board" | `pages/<name>/` |
| Updates on a schedule | "Every morning, update this widget" | a trigger you confirm |

## Change it by talking

1. "Call me Marta and keep answers short": the assistant saves your name in
   `LocalAI/user.json` and the rule in `LocalAI/persona.json`. Every chat uses them from your
   next message.
2. "Make that a shortcut": it adds the request to `LocalAI/shortcuts.json`; the
   shortcut shows above the message box of every chat.
3. "Make me a task board": it writes a page in `pages/task-board/`, which shows
   under **Pages** in the sidebar.
4. "Show my open tasks on Home": it writes `data/tasks.json` and adds a list
   widget to `LocalAI/home.json`.
5. "Every morning, summarise my inbox here": it adds a widget and proposes a
   schedule trigger that you confirm on its card before anything runs.

### Examples on Home

A user with a chat model and none of these files sees **Make LocalAI yours**
under the Home message box: three example cards ("Call me <your first name>
and keep answers short", "Make me a task board", "Every morning, list
yesterday's chats here", or "summarise my inbox" when a mail connector is set
up), and links to the ready pages. A card puts its sentence into the message
box and sends nothing; you send it when you are ready. A card goes away once
its result exists (your persona or profile is saved, a page exists, Home has a
widget that reads a data file). **Hide these** hides the whole block, with
Undo, and is kept under `seen` in `LocalAI/user.json`. A user who already has
`LocalAI/shortcuts.json` or `LocalAI/ui.json` and another of these files never sees the
examples; the library line under Home then offers **Customise Home**.

### What a change shows

When the assistant changes one of these files or a page in a chat, the answer
ends with a short note: what it saved and where ("Saved to your profile in
`LocalAI/user.json`", "Created the page Task board in `pages/tasks/`"), with **Undo**
and a link to where you see it (Account, the page, Home). **Undo** undoes the
change sets of that turn in your file history. A toast appears only when the
effect is outside the chat: a new shortcut ("Added to your composer", and the
shortcuts row is outlined for a moment), the first page ("Task board is in
Pages, in the sidebar"), Home or the theme. Every toast action is also on the
note. A change made in another tab or by a trigger refreshes the screens
quietly, without a toast. The **Files** pane of the chat tags these files
("Your profile", "Assistant") and links here.

## You and the assistant

**Account > About you and the assistant** edits both files. Without accounts
(no authentication), the Account page shows its two tabs of these files only,
and the sidebar links to it as **Your settings**.

### LocalAI/persona.json

```json
{
  "name": "Ada",
  "system_prompt": "You are Ada, a calm and direct assistant. Keep answers short."
}
```

The persona is added to the prompt of every chat at the start of each turn,
after the chat's own system prompt and the instructions of its agent, which
take precedence, and before the server's default prompt text. A change applies
from the next message.

### LocalAI/user.json

```json
{
  "name": "Marta",
  "bio": "Product designer in Milan. I prefer plain, short answers.",
  "language": "it",
  "timezone": "Europe/Rome"
}
```

The model gets a short "About the user" block with these four fields, at most
1 KiB. The settings page fills in your name from your account and the language
and time zone from your browser; they are saved when you press **Save**.
`seen` is the record of what LocalAI showed you and what you answered (see
[Turning suggestions off](#turning-suggestions-off)); it is not sent to the model.

When neither file exists, nothing is added to the prompt. Each block costs
about 25 tokens of framing plus the text of its fields: with the examples on
this page, about 40 tokens for the persona and 50 for the user.

The **You** card says what reaches the model: your name, your bio, your
language and your time zone, at the start of every chat. Leave a field empty
to keep it out. With a remote model (a cloud proxy or a remote endpoint), this
text leaves the server with every chat.

## Shortcuts

`LocalAI/shortcuts.json`:

```json
{
  "shortcuts": [
    { "label": "Plan my day", "prompt": "Look at my open tasks and plan today.", "icon": "calendar" },
    { "label": "Summarise my inbox", "prompt": "Summarise my inbox", "icon": "mail", "send": true }
  ]
}
```

The shortcuts show above the message box of every chat. A click puts the
prompt in the box; with `"send": true` it sends it at once. Without the file
the chat shows its default shortcuts (the starters of an empty chat). An
empty list hides them.

Edit them with **Edit shortcuts** over the message box, in the Account page, or
by asking the assistant.

## Home

`LocalAI/home.json` and the data files of its widgets. **Edit layout** on Home adds,
removes and orders the widgets.


```json
{
  "widgets": [
    { "id": "tasks", "type": "list", "title": "Open tasks", "source": "data/tasks.json", "trigger": "6f1c0e2a9b" },
    { "id": "tokens", "type": "metric", "title": "Tokens this week", "source": "data/tokens.json" },
    { "id": "chats", "type": "chats", "title": "Last chats" }
  ]
}
```

A `list` or `metric` widget reads its `source`, a data file in your home:

```json
{
  "title": "Open tasks",
  "updatedAt": "2026-10-29T08:00:00Z",
  "source": { "kind": "trigger", "note": "From the mail of the last day" },
  "items": [
    { "label": "Call the dentist", "meta": "Fri" },
    { "label": "Send the invoice", "done": true }
  ]
}
```

A metric uses `value`, `note` and `series` (numbers for the small line). A
`chats` widget lists your last chats and needs no source; it replaces the list
of recent chats under it. A missing data file shows as empty. A data file that
breaks its schema shows its last valid data, marked as stale.

`updatedAt` is an RFC 3339 time. `source` says who wrote the data: `kind` is
`trigger`, `page` (with `page`, the page's name), `assistant` or `you`, and
`note` is one short line. They are fields inside the data file, never part of
a run's answer. Both are checked by the schema, and the editors keep
every key they do not show, so a save from Edit layout keeps a widget's
`trigger` and any key the assistant added.

To update a widget every morning, ask the assistant: it writes the data file,
adds the widget, and proposes a schedule trigger that you confirm on its card
(see [triggers](/features/chats-agents/#proposals)). Each run is one chat turn,
and a schedule proposed in a chat runs at most once an hour. A run ends with one
short sentence about what changed, which you read in the chat and the inbox.

A widget that a trigger feeds (named by `trigger`, or by the `widget` in the
trigger's plan) shows a footer:

| State | Footer |
|---|---|
| Updated | The schedule and the time of the last update, **Pause**; under it the next run and the tokens of a run |
| Running | "Updating now", **Open the run** |
| Never ran | "First run" and its time, **Run now** |
| Paused | "Paused" and the schedule it had, **Resume** |
| Last run failed | "Last update failed" and the day of the data it still shows, dimmed, **Details** (the trigger's runs) and **Retry** |
| Failed, no data | "The update failed and there is no data yet", **Details**, **Retry** |
| Waiting | A proposal names the widget: "Waiting for you to create its trigger in the chat", **Open the chat** |

A widget without a trigger says where its data comes from ("From the page
tasks", "From data/tasks.json"). A broken data file says so and shows its last
good version with **Revert**; a missing one offers to ask the assistant to fix
it. The header of the widget shows the cadence (Daily, Weekly, Paused), and a
line under the widgets counts the ones that update on a schedule, since their
runs use your models.

## Pages

A page is a small tool, such as a task board or a reading list: an
`index.html` in `pages/<name>/` with its data files next to it. It runs in a
sandbox, as you, cannot reach the network, and reads and writes only its own
folder. The **Pages** entry of the sidebar shows once you have a page, with a
one-time **New** label; pinned pages show under it. Two ready pages ship with
LocalAI (a task board and a reading list). See [Pages](/features/pages/) for
the folder, the bridge and the limits.

## Updates on a schedule

Ask "every morning, update this" and the assistant writes the data file of a
widget, adds the widget, and proposes a schedule trigger with
`propose_trigger`. Nothing runs until you confirm the trigger on its card. Each
run is one chat turn on your model, so it uses your models and counts toward
your usage; keep at least an hour between runs. Pause, resume or delete the
trigger on the Triggers page (see [triggers](/features/chats-agents/)). A
failed run keeps the last good data, marked as stale.

## Sharing pages

A page in a shared folder runs as the person who opens it, so it never runs
before they allow it, and a change by its author asks again. See
[Pages](/features/pages/).

## Look

`LocalAI/ui.json`, edited in **Account > Appearance**:

```json
{ "theme": "light", "accent": "#3b82f6", "density": "compact", "defaultModel": "qwen3-8b" }
```

`theme` is `dark`, `light` or `system`. The Appearance tab applies a change at
once and keeps it when you save. `defaultModel` is the model a new chat starts
with. `pinnedPages` lists the pages pinned under **Pages** in the sidebar.

## The LocalAI folder

The five settings files live in one visible folder, `LocalAI/`, at the top of
your home. Your own files stay at the top; **Files** lists them first and puts
`LocalAI/` and `Chats/` last, under **Managed by LocalAI**, with a short line
on what each holds. Moving a managed folder or a settings file to the Trash,
or renaming it, asks first and says what stops working. You can restore it
from the Trash.

Before this folder existed, the files were at the top of the home. LocalAI
moves them into `LocalAI/` the first time it reads your settings (or when you
open **Files**), one change set per file, so the history and the versions of
each file stay, and **Undo** of the move puts it back:

- Only the old file exists: it moves.
- Both exist: LocalAI keeps `LocalAI/` and leaves the old file alone.
- A file written at the old place after the move (by an old page or script):
  its content becomes a new version of the file in `LocalAI/`, and the old
  file goes to the Trash.

Until the next minor release, LocalAI also reads an old file in place when it
cannot move it; writes always go to `LocalAI/`. The move needs no setup and
works with several frontends: each step is one atomic change of the shared
workspace.

## Undo, versions and broken files

Each file has a JSON Schema (draft 2020-12). LocalAI validates every read and
every write against it, and refuses a file over 64 KiB.

- A write that breaks the schema is refused with the list of errors, and
  nothing is written.
- A file that is broken on disk (for example a bad edit by hand) is not used:
  LocalAI uses its last valid version from the file history and the settings
  page shows the errors, an **Invalid** badge and **Revert to the last good
  version**, which writes that version back as a new change.
- **Versions**, next to the file name of each settings card, lists the last
  versions of the file: when, and who wrote it ("By the assistant in
  "Getting started"", "By you"). **Revert** writes the one you pick back as a
  new change, so the newer version stays in the list and the revert can be
  reverted too.
- A save sends only the fields the editor shows. Keys it does not know, such
  as a field the assistant added, stay in the file.
- Two editors at once: when the file changed since you opened it, the save is
  refused and the page shows the newer values, with **Use the newer values**
  and **Keep my changes**.
- Each editor has a **Source** view of the raw JSON, with the same checks.

The schemas are served at `GET /api/chats/wellknown/{name}/schema` and ship in
the built-in skill folder (below), so the settings pages, the server and the
assistant read the same text. Properties can carry an `x-ui` hint for the
settings pages (`widget`, `hidden`).

## Turning suggestions off

### Offers from the assistant

The assistant may offer, as the last sentence of an ordinary answer, to
remember something you told it (your name or a short bio), to make a request
you repeat a shortcut, or to update a Home widget every morning. LocalAI, not
the model, decides when an offer is allowed:

- at most one a day (by the time zone of `LocalAI/user.json`) and one per chat;
- never in the first 10 minutes after LocalAI first met you, and never in the
  first two turns of a chat;
- never while a task runs in the chat (a sub-agent in the background, or a
  trigger proposal that waits for your answer), after a failed turn, or after
  the assistant turned a request down in that chat;
- never in a chat that a connector (Telegram, Slack), a trigger, a job, a page,
  a saved agent or the LocalAI Assistant started;
- never when you or the server turned offers off.

When an offer is allowed, the model gets one line at the start of the turn
(about 45 to 50 tokens); in every other turn nothing is added. LocalAI records
the day and the chat under `seen` in `LocalAI/user.json`, and the assistant records
your answer there: **no** is never offered again, **not now** may come back
after 14 days (a second "not now" counts as no), and "stop suggesting things"
turns all offers off. You can turn them back on, and forget your answers, in
**Account > About you and the assistant > Help and suggestions**.

The assistant also stops when you say "stop suggesting things" in any chat:
the note under its answer says "Suggestions from the assistant are off", with
Undo and a link to Account.

### Help and suggestions

**Account > About you and the assistant** has a card **Help and
suggestions**, stored under `seen` in `LocalAI/user.json`:

- **Offers from the assistant**: on or off for you. Until you choose, the
  server's default applies.
- **Notes about new things**: the one-time **New** label of a sidebar entry.
- **Examples on Home**: **Show the examples again** brings back the cards you
  hid.
- **What the assistant learned**: **Forget what the assistant learned about my
  hints** clears your answers to offers and the day and chat limits, so the
  assistant may offer again. The switches stay as they are.

## For administrators

- The whole feature follows the chats and their files: with the chats or the
  files off, none of these screens or files is used.
- Pages need the `pages` feature of a user (see [Pages](/features/pages/)).
- The runtime setting `chats_suggestions` (**Offers from the assistant** in the
  settings, on by default) is the default for users who did not choose. It
  applies at once. See [Runtime settings]({{%relref "features/runtime-settings" %}}).
- The hint record lives in each user's `LocalAI/user.json`, in the shared workspace,
  so the limits hold when several frontends serve the chats.

## File reference

Each file has a JSON Schema (draft 2020-12), served at
`GET /api/chats/wellknown/{name}/schema` and shipped in the built-in skill
folder, so the settings pages, the server and the assistant read the same
text.

### The assistant's skill

The built-in skill `localai-workspace` tells the assistant what each file does,
where the schemas are (`/workspace/shared-builtin-skills/localai-workspace/schemas/`
in the chat's files) and how to change a file without losing other keys. It has
recipes for "add a shortcut", "show my open tasks on Home", "call me X and
keep answers short" and "every morning update this". Its line in the index of
skills costs about 44 tokens a turn; like any skill, you can switch it off for
a chat in the **Skills** menu.

## API

| Method | Path | What it does |
|---|---|---|
| GET | `/api/chats/wellknown/{name}` | Reads a file: `persona`, `user`, `shortcuts`, `home`, `ui`, or `widget-data` with `?path=` |
| PUT | `/api/chats/wellknown/{name}` | Changes a file: `{"values": {...}, "replace": false}` |
| POST | `/api/chats/wellknown/{name}/revert` | Writes the previous valid version back, or the version `{"rev": N}` |
| GET | `/api/chats/wellknown/{name}/versions` | The versions of the file, newest first: `rev`, `at`, `kind` (`user` or `agent`), `chat` and `chat_title`, and `data` when valid |
| GET | `/api/chats/wellknown/{name}/schema` | The JSON Schema of the file |

A read answers `data`, `version` (also in the `ETag` header), `exists`,
`valid`, and for a broken file `errors`, `fallback` and `fallback_rev`. A write
merges the keys of `values` into the file; `null` removes a key, and
`"replace": true` writes `values` as the whole file. Send the `version` you
read in `If-Match` (`0` when the file did not exist): when the file changed
since, the answer is `409` with the file as it is now in `current`. A result
that breaks the schema is `400` with `errors`.

```bash
curl .../api/chats/wellknown/persona
curl -X PUT .../api/chats/wellknown/persona -H 'If-Match: "3"' \
  -d '{"values": {"system_prompt": "Keep answers short."}}'
curl .../api/chats/wellknown/widget-data?path=data/tasks.json
```

The routes are in the `chats` feature and work on your own home folder only.
