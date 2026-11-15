---
name: localai-workspace
description: Use to personalise LocalAI for the user - persona, profile, shortcuts, default models, Home widgets, theme, and updates on a schedule. To build an app or a tool as a page, use localai-apps.
---

# Make LocalAI theirs

Do the work with files, but talk to the user in plain words: say what changed ("I saved your shortcuts", "I added a Reading list page", "Home now shows your open tasks"), not file names, folders, formats or JSON. Name a file only if the user named it or asks where something is, or when you give them a file as a result (a document they asked for).

A few JSON files in the `LocalAI/` folder of the user's home (`/workspace/home/LocalAI/`) change LocalAI for this user. LocalAI reads them; the settings pages edit the same files. Use these paths: a settings file at the top of the home is in the old place, and LocalAI moves it into `LocalAI/`. Each has a JSON Schema in the `schemas/` folder of this skill, `/workspace/shared-builtin-skills/localai-workspace/schemas/`: read it with read_file before you write, and keep to it. A file that breaks its schema is ignored and LocalAI uses its last good version, so check your JSON.

| File | What it does |
|---|---|
| `LocalAI/persona.json` | `name`, `system_prompt`: your name and instructions in every chat of this user. They reach you at the next message. `avatar`, `greeting`, `emptyChat`: shown to the user, never sent to you. |
| `LocalAI/user.json` | `name`, `bio`, `language`, `timezone`: a short "About the user" block in every chat. `seen` records hints (below). |
| `LocalAI/shortcuts.json` | `{"shortcuts": [{label, prompt, icon?, send?}]}`: the prompts above the composer. Without it the chat shows its default starters. |
| `LocalAI/setups.json` | `{"default"?, "setups": [{id, name, model?, effort?, tools}]}`: named starting points for a new chat (`tools` lists names switched off or on); `default` is the `id` a new chat starts with. No prompt, no files. Limits and values: its schema. |
| `LocalAI/defaults.json` | The model for each kind of work (a model name, `"ask"` or `null`). See "Default models". |
| `LocalAI/home.json` | `{"headline"?, "pageArea"?, "widgets": [{id, type, title, size?, source?, trigger?, page?, folder?}]}`: the Home page, at most 12 widgets. `size`: `s`, `m` or `l`; a page defaults to its `widget.size`. `pageArea`: a page name (`{page, folder}` if shared) that fills the area under the composer, in mode `area`; `null` none. `type` is `list`, `metric`, `chats` or `page`. LocalAI sets `trigger`. A `page` widget names a page (`page`, its folder name; `folder` only for a shared folder), may leave out `title`; at most 3. |
| `LocalAI/ui.json` | `theme` (`dark`, `light`, `system`), `accent` (`#rrggbb`), `density` (`comfortable`, `compact`), `look`, `homeBackground`, `chatBackground` (see "Backgrounds"), `strings` (own text for a few labels), `sound`, `voice`, `slots` (see "Slots") and `pinnedPages` (sidebar pages; the first is the start page). |
| `LocalAI/nav.json` | the sidebar order, names, groups and links (see "Sidebar"). |
| `LocalAI/commands.json` | `{"commands": [{name, description, type, template?, args?, tool?}]}`: the user's slash commands (see "Commands, sound, voice"). |

Rules:

- Change only the keys the user asked for. Read the file first and keep every other key.
- Keep files small (at most 64 KiB). Use 2-space indentation.
- A script, draft or test data goes in the chat's work folder, `"$WORK"` in the shell (the file tools take the path the note names), not in `LocalAI/` or at the top of the home.
- After a change, tell the user in one line what changed and where they see it ("I saved your shortcut. It shows above the message box.").
- A page is `pages/<name>/index.html` with an optional `page.json` and its data files; it runs sandboxed, as the user, with no network. To build one, use the `localai-apps` skill.

## Sidebar

`LocalAI/nav.json` `items`, in order: a built-in entry `{"id": "files", "label"?, "hidden"?}` (ids in `schemas/nav.schema.json`; Home cannot be hidden), a `{"group": "Create"}` title, or a link to a `page`, a `chat` or a `url`. New chat, Chats, settings and search always show. An entry left out goes at the end; a gone page or chat is skipped; an address asks the person first. Keep the other items and keys.

## Commands, sound, voice

- `commands.json`: a `prompt` command (`template` with `{{arg}}`, each listed in `args` with a default) fills the message box; a `tool` command names a tool the user has and only asks you to use it. The user reads the text and sends it. A command has no system prompt. A name is lowercase, unique and not built in (`x-reserved-names`).
- `ui.json` `sound` (off unless `enabled`) and `voice` (`readAloud` off, button or auto): ranges in the schema. Replies read aloud go to the `tts` default of `defaults.json`; if it is not local, say so.

## Slots

`LocalAI/ui.json` `slots`: `chatHeader`, `sidebarFooter`, `composerToolbar`: lists of `page`, `tool` or `command` items (fields and caps: the schema). A page runs in the 40 px strip only if its widget block has `"compact": true` (`localai-apps`, "On Home"); else it is a button that opens it. A tool button asks the user first; a command item (a name in `commands.json`) puts its text in the message box.

## Backgrounds

`homeBackground` (behind Home) and `chatBackground` (behind the chat view) in `LocalAI/ui.json` are independent. Same shape, every key optional; layers stack picture over gradient over colour:

```json
{"homeBackground": {"image": "Pictures/sky.jpg", "fit": "cover", "opacity": 0.9, "blur": 4, "overlay": 0.4}}
{"chatBackground": {"gradient": {"from": "#0f172a", "to": "#3b82f6", "angle": 135}}}
{"homeBackground": {"color": "#1e293b"}}
```

- `color`, `gradient` (`from`, `to`, `angle`), `fit`, `opacity`, `blur`, `overlay`: values and ranges in `schemas/ui.schema.json`. Write no CSS.
- `image`: the path of a picture in the user's files, relative to the home, with no leading slash (`Pictures/sky.jpg`, not `/Pictures/sky.jpg`). Only `.png`, `.jpg`, `.jpeg`, `.webp`, `.gif` and `.avif`. No `..`, no address (`https://`, `data:`), no backslash. A path the user cannot read shows nothing; check the file exists first.
- Raise `overlay` (a veil, 0 to 1) for a busy or bright picture.
- To remove a background, delete its key (a save with `null` does the same).

## Default models

`LocalAI/defaults.json` sets the model per kind of work (its schema lists the keys). The media tools read it themselves.

- A result with `model_note` used a default. Tell the user ("Made with flux.1-schnell, your default for images").
- A tool that stops because the user wants to be asked, or because the default model is gone: ask which installed model to use (`list_models`), then call again with `model`, for that request only. Never switch models silently.
- Edit the file only when asked: read it, change that key, keep the rest, say what changed. The old `ui.json` `defaultModel` is only a fallback for `chat`.

## Identity, look and text

- `persona.json` `avatar` (a picture path, rule of the background `image`; no SVG), `greeting`, `emptyChat`; `home.json` `headline`. Their texts take only `{user.name}`, `{time_of_day}` ("Good morning"), `{date}`; another `{name}` or a lone brace breaks the file.
- `ui.json` `look`: values from the lists of the schema, never CSS. An `accent` under 4.5:1 contrast on the page of the theme is ignored: say so.
- `ui.json` `strings`: only `home.headline`, `composer.placeholder` (`{model}`), `chat.empty.title`, `nav.newChat`, `nav.home`, `nav.recentChats`, `nav.allChats`. No other text can change. A `nav.json` label wins over `nav.home`.

## Widget data

A `list` or `metric` widget reads its `source`, a JSON file in the home that follows `schemas/widget-data.schema.json`:

```json
{"title": "Open tasks", "updatedAt": "2026-10-29T08:00:00Z",
 "source": {"kind": "assistant", "note": "From our chat about the release"},
 "items": [{"label": "Call the dentist", "meta": "Fri"}, {"label": "Send the invoice", "done": true}]}
```

A metric uses `value`, `note` and `series` (numbers for a small line). A `chats` widget needs no source: it lists the user's last chats. Put data files under `data/`.

- Always set `updatedAt` (RFC 3339): the widget shows it.
- An item can link: `{"label": "Title", "meta": "source", "url": "https://..."}` opens the site in a new tab; `"url": "/app/pages/<name>"` opens a page of the app (also `/app/chats`, `/app/files`). Other links break the schema.
- `source` says who wrote the data: `{"kind": "trigger"}` in a trigger run, `{"kind": "page", "page": "<name>"}` for a page's data, `{"kind": "assistant"}` in a chat. `note` is one short line on where the data comes from. Both are fields inside the data file. Never print them as your answer.
- A file that breaks the schema is not shown: the widget keeps its last good version. Write the whole file at once.

## Recipes

**Add a shortcut.** Read `LocalAI/shortcuts.json` (or start from `{"shortcuts": []}`), append `{"label": "Plan my day", "prompt": "Look at my open tasks and plan today.", "icon": "calendar"}`, write it back.

**Show my open tasks on Home.** Write `data/tasks.json` (widget data, `items`), then add `{"id": "tasks", "type": "list", "title": "Open tasks", "source": "data/tasks.json"}` to `widgets` in `LocalAI/home.json`.

**Put this page on Home.** A page shows on Home only when its `page.json` has a `widget` block. Read `pages/<name>/page.json`, add `"widget": {"size": "medium"}` (`small` for one line or a number, `large` for a table or a chart) and keep its other keys; make sure the page has a compact layout for `localai.page.mode === 'widget'` (the `localai-apps` skill, section "On Home"). Then add `{"id": "<name>", "type": "page", "page": "<name>"}` to `widgets` in `LocalAI/home.json`. Home takes at most 3; with 3 already, ask which to replace.

## Updates on a schedule or when a file arrives

LocalAI runs a trigger as the user: a new chat that gets the trigger's prompt, on a schedule or when a file arrives in a folder. You never create a trigger yourself. You propose it with `propose_trigger`, a card shows the user what it does, and nothing runs until they press Create trigger. Never say a trigger is on before they do. A run that nobody watches only reads unless the proposal lists `acts` (`act.files`, `act.addons`, `act.send`, `act.spend`); ask for the fewest the task needs, only ones you hold, and tell the user which you asked for. Nothing changes them after the user confirms. To change a trigger the user has, call `list_triggers` for its `ref`, then `update_trigger` or `delete_trigger`: an edit or a delete waits for the user to press Apply or Delete on its card, so never say it is done before they do; a change that only pauses or resumes applies at once and the user can undo it.

Every proposal fills `reads` (a few words), `writes` (the data file), `widget` (the id of the widget in `LocalAI/home.json`) and `estimated_tokens` (one run: its prompt, what it reads, and the answer; a run that reads a day of mail is about 1,500 tokens). When the user confirms, LocalAI writes the trigger's id into the widget as `trigger`, and the widget shows when it runs, its last run and a Pause button.

Costs: every run is a chat turn on the user's model, so it uses tokens and, on a remote model, sends data out. A schedule runs at most once every 5 minutes. A trigger that runs more often than hourly is switched off after 3 runs in a row that hit its daily limit, and any schedule trigger after 3 failed runs in a row. Suggest the slowest schedule that does the job (once a day is usually enough), and say what one run costs.

The prompt of a run is all the run knows. Write it so it stands alone: what to read, which file to rewrite, and the widget-data shape (the run puts `source` and `note` inside that file). End it by telling the run to finish with one short plain sentence about what changed: the user reads it in the inbox and in the chat. Never ask the run for JSON as its answer.

**Every morning update this.**
1. Pick the data file: the `source` of the widget the user means. With no widget, write `data/<name>.json` with today's data and add the widget to `LocalAI/home.json` (`{"id": "inbox", "type": "list", "title": "Inbox this morning", "source": "data/inbox.json"}`).
2. Propose: `{"name": "Morning inbox", "when": "daily 08:00", "prompt": "Read the mail of the last 24 hours. Rewrite data/inbox.json as widget data: items with the mails that need a reply (label: sender and subject, meta: the time), updatedAt now, source {\"kind\": \"trigger\", \"note\": \"Mail from the last 24 hours\"}, all inside the file. Finish with one short sentence about what changed.", "reads": "Mail from the last 24 hours", "writes": "data/inbox.json", "widget": "inbox", "estimated_tokens": 1500}`.
3. Tell the user to check the card, in one line.

**When I drop a file in `inbox/`, summarise it.**
1. Make sure the folder exists (write `inbox/README.md` with one line if it does not).
2. Add a widget that reads `data/inbox-summaries.json`.
3. Propose with `"when": {"file_arrives_in": "/inbox"}` and a prompt that names the arrived file ("the file named in this message"), reads it, and rewrites `data/inbox-summaries.json` with one item per summary, newest first, at most 20. A file run starts 30 seconds after the file stops changing, at most once a minute and 24 times a day; its own writes start nothing.

**On Fridays show the week review first.** Add a widget `week` (`data/week.json`). Propose `"weekly fri 08:00"` that writes it and moves `week` to the top of `widgets` (keep the rest), and `"weekly mon 08:00"` that moves it back. Say these are two triggers.

**A page that starts a run.** Create `pages/<name>/requests/README.md` (the folder must exist); the page writes each request there (`localai.files.write('requests/' + Date.now() + '.json', ...)`). Propose `"when": {"file_arrives_in": "/pages/<name>/requests"}` with a prompt that reads the named request and writes `pages/<name>/answers.json`; the page reads it on `localai.on('files.changed', ...)`. Each request is one run and costs tokens.

## Offers

An offer is a suggestion the user did not ask for. LocalAI decides when one is allowed and tells you in a system note at the start of the turn ("Offer allowed this turn (profile, shortcut) ..."). Without that note, never offer anything. With it, offer at most one of the kinds it names, and only when the user's message is casual (thanks, small talk, a fact about themselves) and you used no tool. Skip it when the user is in the middle of something, when you refused or something failed in this chat, or when nothing in the conversation fits.

Put the offer as the last paragraph of a normal reply, in plain words: no buttons, no list, no pressure. Say what you would save and that it can be undone. Use these sentences (reword, keep the meaning):

| Kind | Sentence |
|---|---|
| `profile` | You mentioned {fact}. Do you want me to remember that in future chats? I would save it to your profile, and you can change or remove it in Account. |
| `shortcut` | Should I make this a shortcut? It would show in the Shortcuts menu of every chat. |
| `schedule` | Should this widget update every morning? I would propose a trigger for you to check first. |

Record the answer under `seen` in `LocalAI/user.json` (read the file first and keep every other key, in `seen` too), in `seen.offers.<kind>` with today's date:

- "Yes", "sure": do it (`LocalAI/user.json`, `LocalAI/shortcuts.json`, or `propose_trigger`), then set `{"state": "accepted", "at": "<date>"}`.
- "No", "no thanks": set `{"state": "declined", "at": "<date>"}` and answer in a few words. It is never offered again.
- "Not now", or the user ignores it: set `{"state": "later", "at": "<date>", "count": <previous count + 1>}`. LocalAI may allow it again after 14 days; a second "not now" counts as no.
- "Don't ask me things like that", "stop suggesting things", "never ask": set `seen.offers_off` to `true` and say "Understood. I will not offer to save things again." The user can turn offers back on in Account.

When the user asks you to turn offers back on, set `seen.offers_off` to `false`. Never write `last_offer`, `offer_chats`, `offer_turn` or `since`: LocalAI keeps them.
