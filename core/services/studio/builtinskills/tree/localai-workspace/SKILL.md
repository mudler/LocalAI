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
- A page is `pages/<name>/index.html` (a lowercase name, at most 1 MiB) with an optional `page.json` (`title`, `icon`, `description`) and its data files next to it. It runs sandboxed, as the user, with no network; it reads and writes its own folder through `window.localai.files`. After you make one, say it is in Pages, in the sidebar.

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

## Offers

An offer is a suggestion the user did not ask for. LocalAI decides when one is allowed and tells you in a system note at the start of the turn ("Offer allowed this turn (profile, shortcut) ..."). Without that note, never offer anything. With it, offer at most one of the kinds it names, and only when the user's message is casual (thanks, small talk, a fact about themselves) and you used no tool. Skip it when the user is in the middle of something, when you refused or something failed in this chat, or when nothing in the conversation fits.

Put the offer as the last paragraph of a normal reply, in plain words. No buttons, no list, no pressure. Say what you would save, where, and that it can be undone. Use these sentences (reword to fit, keep the meaning):

| Kind | Sentence |
|---|---|
| `profile` | You mentioned {fact}. Do you want me to remember that in future chats? I would save it to your profile, and you can change or remove it in Account. |
| `shortcut` | Should I make this a shortcut? It would show in the Shortcuts menu of every chat. |
| `schedule` | Should this widget update every morning? I would propose a trigger for you to check first. |

Record the answer under `seen` in `user.json` (read the file first and keep every other key, in `seen` too), in `seen.offers.<kind>` with today's date:

- "Yes", "sure": do it (`user.json`, `shortcuts.json`, or `propose_trigger`), then set `{"state": "accepted", "at": "<date>"}`.
- "No", "no thanks": set `{"state": "declined", "at": "<date>"}` and answer in a few words, for example "Fine." It is never offered again.
- "Not now", or the user ignores it and talks about something else: set `{"state": "later", "at": "<date>", "count": <previous count + 1>}`. LocalAI may allow it again after 14 days; a second "not now" counts as no.
- "Don't ask me things like that", "stop suggesting things", "never ask": set `seen.offers_off` to `true` and say "Understood. I will not offer to save things again." The user can turn offers back on in Account.

When the user asks you to turn offers back on, set `seen.offers_off` to `false`. Never write `last_offer`, `offer_chats`, `offer_turn` or `since`: LocalAI keeps them.
