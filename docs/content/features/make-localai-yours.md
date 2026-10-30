+++
disableToc = false
title = "Make LocalAI yours"
weight = 23
url = "/features/make-localai-yours/"
+++

A few JSON files in your home folder change how LocalAI looks and behaves for
you: the name and instructions of the assistant, what it knows about you, the
prompts above the message box, the widgets on Home and the theme. LocalAI reads
these files. You can change them in the settings pages, or ask the assistant in
a [chat](/features/chats/) to change them for you. Both write the same file, so
every change has a history and can be undone, like any other file.

The files need the chats and files of the server (see [Chats](/features/chats/)).

## Examples on Home

A user with a chat model and none of these files sees **Make LocalAI yours**
under the Home message box: three example cards ("Call me <your first name>
and keep answers short", "Make me a task board", "Every morning, list
yesterday's chats here", or "summarise my inbox" when a mail connector is set
up), and links to the ready pages. A card puts its sentence into the message
box and sends nothing; you send it when you are ready. A card goes away once
its result exists (your persona or profile is saved, a page exists, Home has a
widget that reads a data file). **Hide these** hides the whole block and is
kept under `seen` in `user.json`. A user who already has `shortcuts.json` or
`ui.json` and another of these files never sees the examples.

## The files

| File | What it does | Where you edit it |
|---|---|---|
| `persona.json` | `name` and `system_prompt` of the assistant, in every chat of yours | **Account > About you and the assistant** |
| `user.json` | your `name`, `bio`, `language` and `timezone`; a short block of it reaches the model in every chat | **Account > About you and the assistant** |
| `shortcuts.json` | the prompts above the message box | **Edit shortcuts** over the message box, and the Account page |
| `home.json` | the widgets of the Home page | **Edit layout** on Home |
| `ui.json` | `theme`, `accent`, `density` and `defaultModel` | **Account > Appearance** |

Without accounts (no authentication), the Account page shows these two tabs
only, and the sidebar links to it as **Your settings**.

### persona.json

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

### user.json

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
`seen` is a record the assistant keeps of the hints it offered (see below); it
is not sent to the model.

When neither file exists, nothing is added to the prompt. Each block costs
about 25 tokens of framing plus the text of its fields: with the examples on
this page, about 40 tokens for the persona and 50 for the user.

### shortcuts.json

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

### home.json and widget data

```json
{
  "widgets": [
    { "id": "tasks", "type": "list", "title": "Open tasks", "source": "data/tasks.json" },
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

To update a widget every morning, ask the assistant: it writes the data file,
adds the widget, and proposes a schedule trigger that you confirm on its card
(see [triggers](/features/chats-agents/)). Each run is one chat turn; keep at
least an hour between runs.

### ui.json

```json
{ "theme": "light", "accent": "#3b82f6", "density": "compact", "defaultModel": "qwen3-8b" }
```

`theme` is `dark`, `light` or `system`. The Appearance tab applies a change at
once and keeps it when you save. `defaultModel` is the model a new chat starts
with. `pinnedPages` is reserved for a later release.

## Validation and history

Each file has a JSON Schema (draft 2020-12). LocalAI validates every read and
every write against it, and refuses a file over 64 KiB.

- A write that breaks the schema is refused with the list of errors, and
  nothing is written.
- A file that is broken on disk (for example a bad edit by hand) is not used:
  LocalAI uses its last valid version from the file history and the settings
  page shows the errors. **Revert to previous version** writes that version
  back as a new change, which you can undo too.
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

## The assistant's skill

The built-in skill `localai-workspace` tells the assistant what each file does,
where the schemas are (`/workspace/shared-builtin-skills/localai-workspace/schemas/`
in the chat's files) and how to change a file without losing other keys. It has
recipes for "add a shortcut", "show my open tasks on Home", "call me X and
keep answers short" and "every morning update this". Its line in the index of
skills costs about 44 tokens a turn; like any skill, you can switch it off for
a chat in the **Skills** menu.

The assistant may offer once to remember your name or a short bio, when
`user.json` has neither: only in a casual moment, never in the middle of a
task, and never again after you say no. It records the answer under `seen`.

Pages (your own HTML shown inside LocalAI) are planned for a later release.

## API

| Method | Path | What it does |
|---|---|---|
| GET | `/api/chats/wellknown/{name}` | Reads a file: `persona`, `user`, `shortcuts`, `home`, `ui`, or `widget-data` with `?path=` |
| PUT | `/api/chats/wellknown/{name}` | Changes a file: `{"values": {...}, "replace": false}` |
| POST | `/api/chats/wellknown/{name}/revert` | Writes the previous valid version back |
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
