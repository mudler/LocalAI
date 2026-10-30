---
name: localai-workspace
description: Use to personalise LocalAI for the user - persona, profile, shortcuts, Home widgets, pages, theme, and updates on a schedule.
---

# Make LocalAI theirs

A few JSON files in the user's home (`/workspace/home/`) change LocalAI for this user. LocalAI reads them; the settings pages edit the same files. Write them with your file tools. Each has a JSON Schema in `/workspace/shared-builtin-skills/localai-workspace/schemas/`: read it before you write, and keep to it. A file that breaks its schema is ignored and LocalAI uses its last good version, so check your JSON.

| File | What it does |
|---|---|
| `persona.json` | `name`, `system_prompt`: your name and instructions in every chat of this user. They reach you at the next message. |
| `user.json` | `name`, `bio`, `language`, `timezone`: a short "About the user" block in every chat. `seen` records hints (below). |
| `shortcuts.json` | `{"shortcuts": [{label, prompt, icon?, send?}]}`: the prompts above the composer. Without it the chat shows its default starters. |
| `home.json` | `{"widgets": [{id, type, title, source?, trigger?}]}`: the Home page. `type` is `list`, `metric` or `chats`. `trigger` is set by LocalAI when the user confirms the trigger that feeds the widget. |
| `ui.json` | `theme` (`dark`, `light`, `system`), `accent` (`#rrggbb`), `density` (`comfortable`, `compact`), `defaultModel`. |

Rules:

- Change only the keys the user asked for. Read the file first and keep every other key.
- Keep files small (at most 64 KiB). Use 2-space indentation.
- After a change, tell the user in one line what changed and where ("Saved to `shortcuts.json`. It shows above the composer.").
- Pages are small HTML tools in `pages/<name>/` (`index.html`, optional `page.json`, and their data files). LocalAI shows them in a sandbox; a page reads and writes only its own folder through `window.localai.files`.

## Widget data

A `list` or `metric` widget reads its `source`, a JSON file in the home that follows `schemas/widget-data.schema.json`:

```json
{"title": "Open tasks", "updatedAt": "2026-10-29T08:00:00Z",
 "source": {"kind": "assistant", "note": "From our chat about the release"},
 "items": [{"label": "Call the dentist", "meta": "Fri"}, {"label": "Send the invoice", "done": true}]}
```

A metric uses `value`, `note` and `series` (numbers for a small line). A `chats` widget needs no source: it lists the user's last chats. Put data files under `data/`.

- `updatedAt` is an RFC 3339 time. Always set it when you write the file: the widget shows it.
- `source` says who wrote the data: `{"kind": "trigger"}` in a trigger run, `{"kind": "page", "page": "<name>"}` for a page's data, `{"kind": "assistant"}` in a chat. `note` is one short line on where the data comes from.
- A file that breaks the schema is not shown: the widget keeps the last good version and says the file has an error. A failed run leaves the file as it was, so never write half a file; write the whole file once at the end.

## Recipes

**Add a shortcut.** Read `shortcuts.json` (or start from `{"shortcuts": []}`), append `{"label": "Plan my day", "prompt": "Look at my open tasks and plan today.", "icon": "calendar"}`, write it back.

**Show my open tasks on Home.** Write `data/tasks.json` (widget data, `items`), then add `{"id": "tasks", "type": "list", "title": "Open tasks", "source": "data/tasks.json"}` to `widgets` in `home.json`.

**Call me X and keep answers short.** Set `name` in `user.json` to X. Add "Keep answers short." to `system_prompt` in `persona.json`.

## Updates on a schedule or when a file arrives

LocalAI runs a trigger as the user: a new chat that gets the trigger's prompt, on a schedule or when a file arrives in a folder. You never create a trigger yourself. You propose it with `propose_trigger`, a card shows the user what it does, and nothing runs until they press Create trigger. Never say a trigger is on before they do.

Every proposal fills `reads` (a few words), `writes` (the data file), `widget` (the id of the widget in `home.json`) and `estimated_tokens` (one run: its prompt, what it reads, and the answer; a run that reads a day of mail is about 1,500 tokens). The card shows them, with the cost and how to stop it. When the user confirms, LocalAI writes the trigger's id into the widget as `trigger`, and the widget shows when it runs, its last run and a Pause button.

Costs: every run is a chat turn on the user's model, so it uses tokens and, on a remote model, sends data out. A schedule from a chat runs at most once an hour: a shorter one is refused. Suggest the slowest schedule that does the job (once a day is usually enough), and say what one run costs.

The prompt of a run is all the run knows. Write it so it stands alone: what to read, which file to rewrite, the widget-data shape, and `"source": {"kind": "trigger"}`.

**Every morning update this.**
1. Pick the data file: the `source` of the widget the user means. With no widget, write `data/<name>.json` with today's data and add the widget to `home.json` (`{"id": "inbox", "type": "list", "title": "Inbox this morning", "source": "data/inbox.json"}`).
2. Propose: `{"name": "Morning inbox", "when": "daily 08:00", "prompt": "Read the mail of the last 24 hours. Rewrite data/inbox.json as widget data: items with the mails that need a reply (label: sender and subject, meta: the time), updatedAt now, source {\"kind\": \"trigger\", \"note\": \"Mail from the last 24 hours\"}.", "reads": "Mail from the last 24 hours", "writes": "data/inbox.json", "widget": "inbox", "estimated_tokens": 1500}`.
3. Tell the user to check the card. One line.

**When I drop a file in `inbox/`, summarise it.**
1. Make sure the folder exists (write `inbox/README.md` with one line if it does not).
2. Add a widget that reads `data/inbox-summaries.json`.
3. Propose with `"when": {"file_arrives_in": "/inbox"}` and a prompt that names the arrived file ("the file named in this message"), reads it, and rewrites `data/inbox-summaries.json` with one item per summary, newest first, at most 20. A file run starts after the file stays unchanged for 30 seconds, at most once a minute, and at most 24 runs a day; the run's own writes start nothing.

**On Fridays show the week review first.**
1. Add a `metric` or `list` widget `week` that reads `data/week.json`.
2. Propose `"when": "weekly fri 08:00"` with a prompt that writes `data/week.json` and moves the widget `week` to the top of `widgets` in `home.json` (read the file, keep every other key and widget).
3. Propose a second trigger `"weekly mon 08:00"` that moves it back to the end. Say that these are two triggers and why.

**A page that starts a run.** A page can feed a file trigger: the page writes into its own folder, and a trigger watches a folder inside it.
1. Create `pages/<name>/requests/README.md` (a trigger needs a folder that exists). Have the page write requests into `requests/`: `localai.files.write('requests/' + Date.now() + '.json', JSON.stringify({text}))`.
2. Propose `{"when": {"file_arrives_in": "/pages/<name>/requests"}, "prompt": "Read the request file named in this message, do what it asks, and write the answer to pages/<name>/answers.json.", "writes": "pages/<name>/answers.json"}`.
3. The page listens with `localai.on('files.changed', ...)` and reads `answers.json` again. Tell the user that each request is one run, so it costs tokens; the minimum time between runs and the daily limit stop a page from starting runs in a loop.

## Hints

When `user.json` has no `name` and no `bio`, you may offer once to remember them: only in a casual moment, never in the middle of a task, and never again after the user says no. Record the offer under `seen` in `user.json`, for example `"seen": {"profile": {"state": "declined", "at": "2026-10-29"}}` (`offered`, `declined` or `accepted`). Read `seen` before you offer anything.
