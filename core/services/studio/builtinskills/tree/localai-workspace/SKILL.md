---
name: localai-workspace
description: Use to personalise LocalAI for the user - persona, profile, shortcuts, Home widgets, theme.
---

# Make LocalAI theirs

A few JSON files in the user's home (`/workspace/home/`) change LocalAI for this user. LocalAI reads them; the settings pages edit the same files. Write them with your file tools. Each has a JSON Schema in `/workspace/shared-builtin-skills/localai-workspace/schemas/`: read it before you write, and keep to it. A file that breaks its schema is ignored and LocalAI uses its last good version, so check your JSON.

| File | What it does |
|---|---|
| `persona.json` | `name`, `system_prompt`: your name and instructions in every chat of this user. They reach you at the next message. |
| `user.json` | `name`, `bio`, `language`, `timezone`: a short "About the user" block in every chat. `seen` records hints (below). |
| `shortcuts.json` | `{"shortcuts": [{label, prompt, icon?, send?}]}`: the prompts above the composer. Without it the chat shows its default starters. |
| `home.json` | `{"widgets": [{id, type, title, source?}]}`: the Home page. `type` is `list`, `metric` or `chats`. |
| `ui.json` | `theme` (`dark`, `light`, `system`), `accent` (`#rrggbb`), `density` (`comfortable`, `compact`), `defaultModel`. |

Rules:

- Change only the keys the user asked for. Read the file first and keep every other key.
- Keep files small (at most 64 KiB). Use 2-space indentation.
- After a change, tell the user in one line what changed and where ("Saved to `shortcuts.json`. It shows above the composer.").
- Pages (your own HTML shown in LocalAI) come in a later release. Do not write `pages/` yet, and do not promise them.

## Widget data

A `list` or `metric` widget reads its `source`, a JSON file in the home that follows `schemas/widget-data.schema.json`:

```json
{"title": "Open tasks", "updatedAt": "2026-10-29T08:00:00Z",
 "items": [{"label": "Call the dentist", "meta": "Fri"}, {"label": "Send the invoice", "done": true}]}
```

A metric uses `value`, `note` and `series` (numbers for a small line). A `chats` widget needs no source: it lists the user's last chats. Put data files under `data/`.

## Recipes

**Add a shortcut.** Read `shortcuts.json` (or start from `{"shortcuts": []}`), append `{"label": "Plan my day", "prompt": "Look at my open tasks and plan today.", "icon": "calendar"}`, write it back.

**Show my open tasks on Home.** Write `data/tasks.json` (widget data, `items`), then add `{"id": "tasks", "type": "list", "title": "Open tasks", "source": "data/tasks.json"}` to `widgets` in `home.json`.

**Call me X and keep answers short.** Set `name` in `user.json` to X. Add "Keep answers short." to `system_prompt` in `persona.json`.

**Every morning update this.** Choose the data file a widget reads (create the widget as above if there is none). Then propose a schedule trigger with `propose_trigger`: the user confirms it on the card. Its prompt says which file to rewrite and how, for example "Rewrite data/inbox.json with a summary of today's mail as widget data." Say what a run costs (one chat turn each time) and suggest no more than one run an hour.

## Hints

When `user.json` has no `name` and no `bio`, you may offer once to remember them: only in a casual moment, never in the middle of a task, and never again after the user says no. Record the offer under `seen` in `user.json`, for example `"seen": {"profile": {"state": "declined", "at": "2026-10-29"}}` (`offered`, `declined` or `accepted`). Read `seen` before you offer anything.
