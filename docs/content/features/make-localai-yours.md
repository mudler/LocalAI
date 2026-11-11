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
| The assistant's picture, greeting and empty chat | "Use Pictures/ada.png as your picture and greet me by name" | `LocalAI/persona.json` |
| The headline of Home | "Say 'Rise and shine' at the top of Home" | `LocalAI/home.json` |
| Standing rules of one saved agent | "Always answer this agent's users in French" | `LocalAI/agents/<agent>/profile.md` |
| What the assistant knows about you | "Call me Marta. I am a designer in Milan" | `LocalAI/user.json` |
| Shortcuts in the message box | "Make this a shortcut" | `LocalAI/shortcuts.json` |
| Saved setups of a new chat | "Save this as a setup called Coding" | `LocalAI/setups.json` |
| The model for pictures, speech and other work | "Use kokoro for speech from now on" | `LocalAI/defaults.json` |
| The widgets on Home | "Show my open tasks on Home" | `LocalAI/home.json` and a data file |
| The look | "Use the light theme, a serif font and round corners" | `LocalAI/ui.json` |
| A few labels of the app | "Call the New chat button New thread" | `LocalAI/ui.json` |
| The sidebar | "Hide Agents and rename Skills to Recipes" | `LocalAI/nav.json` |
| Small tiles and buttons in the chat header, the sidebar or the message box | "Put the weather page in the chat header" | `LocalAI/ui.json` (`slots`) |
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

The assistant explains a change in plain words ("I saved your shortcuts", "I added a Reading list page"). It does not name files, folders or formats unless you do, or you ask where something is.

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

#### Picture, greeting and empty chat

The same file sets how the assistant shows itself. None of these keys reaches
the model; they change only what you see.

```json
{
  "name": "Juniper",
  "avatar": "Pictures/avatars/juniper.png",
  "greeting": "Hi {user.name}, what are we working on?",
  "emptyChat": { "title": "Ask Juniper anything", "subtitle": "I remember your projects.", "showShortcuts": true }
}
```

| Key | Meaning |
|---|---|
| `avatar` | A picture in your files, such as `Pictures/avatars/juniper.png`, written without a leading slash. Allowed types: png, jpg, jpeg, webp, gif, avif (no SVG). It shows on Home, above each reply and in an empty chat. Without it, the first letter of the name shows. |
| `greeting` | The first line of the assistant in an empty chat. At most 200 characters. |
| `emptyChat.title` | The headline of an empty chat. At most 80 characters. |
| `emptyChat.subtitle` | One line under the headline. At most 200 characters. |
| `emptyChat.showShortcuts` | `false` hides your shortcuts in an empty chat. Default `true`. |

The texts can use the placeholders `{user.name}` (the name in `user.json`, or
your account name), `{time_of_day}` (the greeting of the hour, such as "Good
morning") and `{date}` (the short date of Home). Any other `{name}`, or a brace
without its pair, is refused when you save. The identity shows in your own
chats; a chat with an agent and the LocalAI assistant of **Manage** keep their
own name.

### LocalAI/agents/\<agent\>/profile.md

A plain Markdown file of at most 4 KiB with the persona and the standing rules of one
saved agent. The host adds it to the prompt of every chat of that agent, right after the
agent's instructions and the persona; longer text is cut. You can edit it in the Files
page. Add-ons can also register index files that the host loads in the same way (see
[Context files](/features/addons/#context-files)).

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

## Setups

`LocalAI/setups.json` is a per-user file. It keeps the named setups you save
from a new chat:

```json
{
  "default": "coding",
  "setups": [
    {
      "id": "coding",
      "name": "Coding",
      "model": "my-coder",
      "effort": "high",
      "tools": { "builtins_off": ["web_search"], "mcp_off": ["calendar"] }
    }
  ]
}
```

A setup stores the model, the tool switches and the reasoning effort. The
tool switches are lists of names that are switched off or on:
`builtins_off`, `builtins_on`, `addons_off`, `mcp_off` and `skills_off`.
`effort` is empty, `none`, `minimal`, `low`, `medium` or `high`. `default` is
the `id` of the setup a new chat starts with.

A setup does not store a prompt, a persona, files or the text of a chat. Use
[shortcuts](#shortcuts) for prompts and the persona for standing
instructions. You can keep at most 50 setups; an `id` has lowercase letters,
digits and dashes, and a name has at most 60 characters. Without the file a new
chat starts from the defaults.

### Starting a chat from a setup

On Home, the Setup chip is the first chip of the message box. Its menu lists
your setups with the model and a short summary of each one. Pick one to load
its model, tool switches and effort into the new chat. A line under the box
shows what is loaded. When you change the model, a switch or the effort, the
chip says "edited" and the line offers `Update "name"`, `Save as new…` and
`Reset`. "Save current setup as…" and "Manage setups" are at the bottom of the
menu. Manage lets you rename, reorder and delete setups and choose the default.

A new chat starts from the default setup. The default is the saved setup that
was in use when you sent your last message. Sending with no setup in use
changes nothing. Sending an edited setup makes the saved, unedited version the
default; use Update to keep your edits. The first time you send a message with
no setups, LocalAI saves the box as "My default" and offers a Rename action. If
the model of a setup is not installed, the chat keeps the model it has and the line says so;
the setup is not changed. A saved agent decides the model and tools of its
chat, so the chip is off while you have one chosen. The line under the box
then says why and keeps "Manage setups" at hand.

## Default models

`LocalAI/defaults.json`, edited in **Account > Defaults**, holds one model for
each kind of work:

```json
{
  "chat": "qwen3-8b-instruct",
  "image": "flux.1-schnell",
  "tts": "kokoro-82m",
  "stt": "whisper-large-v3-turbo",
  "embeddings": "bge-m3",
  "rerank": "bge-reranker-v2-m3",
  "video": null,
  "vision": "ask"
}
```

The keys are `chat` (the model a new chat starts with), `image`, `tts` (text
to speech), `stt` (speech to text), `embeddings`, `rerank`, `video` and
`vision` (image understanding). A value is one of three things:

- the name of an installed model;
- `"ask"`: the assistant asks you which model to use, every time;
- `null`, or no key: you made no choice, and LocalAI picks the first installed
  model that can do the work, as it did before this file existed.

The assistant reads the file before it makes a picture, reads text aloud,
transcribes, embeds, reranks, makes a video or looks at an image. It says which
default it used ("Made with flux.1-schnell, your default for images"). Say
"use sd-3.5-medium this time" to use another model for one request; that
never changes your default. To change the default itself, use the Defaults tab
or ask ("use kokoro for speech from now on"); the assistant tells you what it
changed and never edits the file on its own.

If the model of a default is not installed any more, or is switched off, the
assistant does not use another one. It tells you, asks which model to use for
this request and offers to set a new default. The Defaults tab marks such a row
"Not installed". A row with no installed model shows **Install one**, which
opens the model gallery.

Each row of the tab lists only the installed models that can do that kind of
work. A pick is saved at once. The line under the tab names the file, as on
the other tabs: **Source** shows it, and **Revert to previous version** writes
the earlier version back.

The `defaultModel` key of `ui.json` is the older way to set the chat model.
It is still read, but only when `defaults.json` has no `chat`. The Defaults
tab shows it in the Chat row and never writes it; choose a model in the row
to keep it in `defaults.json`.

## Home

`LocalAI/home.json` and the data files of its widgets. **Edit layout** on Home adds,
removes and orders the widgets.

`headline` replaces the greeting at the top of Home. It takes the placeholders
of the greeting (`{user.name}`, `{time_of_day}`, `{date}`), and an empty value
keeps the greeting of the time of day. Edit it in **Account > About you and the
assistant**. A `home.json` with a headline and no widgets is valid.

```json
{ "headline": "{time_of_day}, {user.name}" }
```


```json
{
  "widgets": [
    { "id": "tasks", "type": "list", "title": "Open tasks", "source": "data/tasks.json", "trigger": "6f1c0e2a9b" },
    { "id": "tokens", "type": "metric", "title": "Tokens this week", "source": "data/tokens.json" },
    { "id": "chats", "type": "chats", "title": "Last chats" },
    { "id": "board", "type": "page", "page": "task-board" }
  ]
}
```

Home has at most 12 widgets.

A `list` or `metric` widget reads its `source`, a data file in your home:

```json
{
  "title": "Open tasks",
  "updatedAt": "2026-10-29T08:00:00Z",
  "source": { "kind": "trigger", "note": "From the mail of the last day" },
  "items": [
    { "label": "Call the dentist", "meta": "Fri" },
    { "label": "Send the invoice", "done": true },
    { "label": "Release notes", "meta": "news", "url": "https://example.com/release" }
  ]
}
```

An item with `url` shows its label as a link. An `http` or `https` address
(without a user name or password) asks first, with the same bar as a link of a
page: it shows the site, the full address and a warning for a host in
punycode, and the site opens in a new tab only after you choose **Open**. The
data file is often written by a trigger run or the assistant from web text,
so a planted link does not open with one click. The tooltip of the link names
the site. A path of the app (`/app/pages/<name>`, `/app/chats`, `/app/files`, and
the screens `chat`, `agents`, `skills`, `triggers`, `collections`, `models`,
`account` and `addons` under `/app/`) opens inside LocalAI. Any other `url`
breaks the schema, and the error says which item and why, so a trigger run
that writes a bad link gets told.

A metric uses `value`, `note` and `series` (numbers for the small line). A
`chats` widget lists your last chats and needs no source; it replaces the list
of recent chats under it. A missing data file shows as empty. A data file that
breaks its schema shows its last valid data, marked as stale.

A `page` widget shows one of your [pages](/features/pages/#a-page-on-home) as
a tile, in the page's compact mode. It names the page (`page`, the folder name
under `pages/`) and, for a page of a shared folder, `folder` (the folder id).
Its `title` is optional: without it, the tile shows the title of the page's
`widget` block, or the page's own title. Only a page whose `page.json` has a
`widget` block can be a tile, and Home shows at most three page widgets; the
server refuses a layout with more, and says so. **Customise Home** lists the
pages that can be a tile, with their size. A tile whose page is gone, or has no
`widget` block, says so calmly and links to **Customise Home**; a shared page
you have not allowed shows **Allow this page**.

`updatedAt` is an RFC 3339 time. `source` says who wrote the data: `kind` is
`trigger`, `page` (with `page`, the page's name), `assistant` or `you`, and
`note` is one short line. They are fields inside the data file, never part of
a run's answer. Both are checked by the schema, and the editors keep
every key they do not show, so a save from Edit layout keeps a widget's
`trigger` and any key the assistant added.

To update a widget every morning, ask the assistant: it writes the data file,
adds the widget, and proposes a schedule trigger that you confirm on its card
(see [triggers](/features/chats-agents/#proposals)). Each run is one chat turn,
and a schedule proposed in a chat runs at most once every 5 minutes. A run ends with one
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
{ "theme": "light", "accent": "#3b82f6", "density": "compact" }
```

`theme` is `dark`, `light` or `system`. The Appearance tab applies a change at
once and keeps it when you save. `pinnedPages` lists the pages pinned under **Pages** in the sidebar.

`accent` must keep a contrast of at least 4.5:1 against the page in the theme
you use. The Appearance tab shows the ratio beside the colour and does not save
a colour that fails. If the file holds an accent that fails in the current
theme (for example a dark teal in the dark theme), LocalAI does not use it and
shows the default accent; the Appearance tab says why. The check runs again
when you change the theme. Text on a filled accent (a button) takes the text
or the page colour of the theme, whichever reads better.

### Fonts, corners, messages and code

`look` changes the font, the corners and the look of your messages and of code.
Each key takes one value of a fixed list; there is no CSS. Every colour comes
from the theme, so each value works in the light and the dark theme. Leave a
key out for the default. The Appearance tab shows a live preview.

```json
{ "look": { "font": "serif", "radius": "round", "bubble": "tinted", "messageWidth": "wide", "codeTheme": "ink" } }
```

| Key | Values (default first) |
|---|---|
| `font` | `default` (Geist), `system`, `serif`, `mono`. For messages and the message box; code stays monospace. |
| `radius` | `soft`, `sharp`, `round`. The corners of cards, buttons and messages. |
| `bubble` | `card`, `filled`, `tinted`, `outline`, `plain`. How your own messages look. |
| `messageWidth` | `medium` (760 px), `narrow` (640 px), `wide` (920 px), `full`. |
| `codeTheme` | `auto`, `mono` (no colours), `vivid` (more colours), `ink` (a block in the inverse colours of the theme). |

A value outside its list is refused when you save. Keys you add inside `look`
stay in the file.

### Your own text for a few labels

`strings` replaces a few labels of the app with your own words. Edit them in
**Account > Text**, or ask the assistant. Only these keys can change:

| Key | Where | Placeholders |
|---|---|---|
| `home.headline` | The top of Home, when `home.json` has no `headline` | `{user.name}`, `{time_of_day}`, `{date}` |
| `composer.placeholder` | The hint in an empty message box, when a model is chosen | `{model}` |
| `chat.empty.title` | The headline of an empty chat, when `persona.json` has no `emptyChat.title` | none |
| `nav.newChat` | The New chat button | none |
| `nav.home` | Home in the sidebar | none |
| `nav.recentChats` | The title of the recent chats in the sidebar | none |
| `nav.allChats` | All chats in the sidebar | none |

```json
{ "strings": { "nav.newChat": "New thread", "composer.placeholder": "Ask {model}..." } }
```

The text of security, permission, consent and trust prompts cannot change: the
server refuses any key that is not in the list. A text with a placeholder its
key does not take, or a brace without its pair, is refused too. An empty box in
the Text tab removes the key, and the default text shows again.

Home can be renamed in two places: `nav.home` here, and a `label` on the
`home` item of `nav.json` (see "Navigation"). The more specific one wins: a
`nav.json` label beats `nav.home`, which beats the built-in label.

### Home and chat backgrounds

`homeBackground` puts a colour, a gradient or a picture behind the Home page.
`chatBackground` does the same behind the chat view: the messages and the
message box. Each changes only its own page; every other page keeps its look,
and you can set one, both or none. Pick them in **Account > Appearance**, or
ask the assistant ("put the picture Pictures/sky.jpg behind my chats"). Both
write the same file, and a change shows at once.

```json
{
  "theme": "dark",
  "homeBackground": {
    "image": "Pictures/sky.jpg",
    "fit": "cover",
    "opacity": 0.9,
    "blur": 4,
    "overlay": 0.4
  },
  "chatBackground": {
    "gradient": { "from": "#0f172a", "to": "#3b82f6", "angle": 135 }
  }
}
```

Both keys take the same values:

| Key | Meaning |
|---|---|
| `color` | A solid colour, `#rrggbb`. |
| `gradient` | `{ "from": "#rrggbb", "to": "#rrggbb", "angle": 135 }`. The angle is 0 to 360 degrees; 180 runs from top to bottom. |
| `image` | The path of a picture in your files, such as `Pictures/sky.jpg`. Write it without a leading slash. Allowed types: png, jpg, jpeg, webp, gif, avif. |
| `fit` | `cover` fills the page (default). `contain` shows the whole picture. |
| `opacity` | 0 to 1. How strong the background is. Default 1. |
| `blur` | 0 to 40 pixels. Default 0. |
| `overlay` | 0 to 1. A veil in the colour of the theme, so text stays readable in the light and the dark theme. Default for Home: 0.55 for a picture, 0.35 for a gradient, 0 for a plain colour. Default for the chat: 0.8, 0.6 and 0, because a chat is long text. |

The picture is read from your own files with your own rights. A path that
starts with `/`, contains `..`, is a web address or does not end in a picture
type is refused when you save. If the file is edited into an invalid state,
the page keeps the last good version and the Appearance tab says the file has
an error. In the chat, the messages also sit on a panel in the colour of the
theme, so they stay readable over any picture. Keys you add yourself stay in the file when you save from the
Appearance tab. To remove a background, press **Remove the background** or
delete its key.

## Navigation

`LocalAI/nav.json`, edited in **Account > Navigation**, sets the order of the
sidebar: which built-in entries show and in what order, their names, groups with
a title, and links of your own to one of your pages, to a chat or to a web
address. Ask the assistant ("move Models under a group called Tools and hide
Agents") or use the tab: drag a row or use the arrows, rename it in place, hide
it with the eye, press **Add group** or **Add link**. A live copy of the sidebar
on the right shows the result as you edit. **Reset to default** is always there.
Without the file the sidebar is the default one.

```json
{
  "items": [
    { "id": "home" },
    { "id": "files" },
    { "type": "chat", "chat": "c_8f2a", "label": "Gate repair" },
    { "group": "Create" },
    { "id": "agents", "hidden": true },
    { "id": "skills", "label": "Recipes" },
    { "group": "Tools" },
    { "id": "models" },
    { "type": "url", "url": "https://localai.io/docs", "label": "Docs" }
  ]
}
```

An item is exactly one of three kinds:

| Item | Meaning |
|---|---|
| `{ "id": "files" }` | A built-in entry. `label` renames it, `hidden: true` takes it out. The ids are `home`, `files`, `pages`, `agents`, `connections`, `memory`, `skills`, `models` and `addons`. LocalAI fixes this list: an id it does not know is ignored. |
| `{ "group": "Create" }` | A section title. The items after it, up to the next group, are listed under it. A group with nothing to show is not drawn. |
| `{ "type": "page", "page": "task-board" }` | A link of your own. `type` is `page` (a page in `pages/`), `chat` (the id of a chat) or `url` (an `http` or `https` address, with no user name or password). `label` and `icon` are optional. |

Some things never change. New chat, Chats (the recent chats and All chats), the
settings entries and the search are not items, so the file cannot move, rename
or hide them. Home can be moved and renamed but not hidden; its `nav.json` label wins over
the `nav.home` text of `ui.json`. A built-in entry
that the list does not name is added after the last item, in the default order,
so a short list never loses an entry. An entry that your account or this server
does not offer (for example Models for a user who is not an administrator, or
Pages while you have no page) stays out of the sidebar whatever the file says.
The pinned pages stay under **Pages**, and the pinned apps under **Add-ons**; if
you hide the entry, they go with it.

A link to a page or a chat that no longer exists shows dimmed with "missing" in
the editor and is skipped in the sidebar; it comes back if the page or chat does.
A link to a web address asks before it leaves LocalAI, with the same question as
the links of a page, and opens in a new tab. The list holds at most 60 items, 12
groups and 30 links. The legacy navigation (`features.addon_navigation` off)
ignores this file.

When the file has an error, the sidebar keeps the last good version and the
editor says what is wrong, with **Revert**. A save from the tab merges your edit
with the file as it is then, item by item, so a change that the assistant made
while the tab was open is not lost. Keys you add yourself stay in the file.

## Slots

`slots` in `LocalAI/ui.json`, edited in **Account > Slots**, puts small items
in three places of the app, left to right:

| Slot | Where | Items |
|---|---|---|
| `chatHeader` | The bar over a chat, before find and settings | at most 4 |
| `sidebarFooter` | The foot of the sidebar, on every screen (hidden while the sidebar is collapsed) | at most 3 |
| `composerToolbar` | Beside the attach button of the message box of a chat | at most 6 |

```json
{
  "slots": {
    "chatHeader": [
      { "type": "page", "page": "weather" },
      { "type": "page", "page": "standup", "folder": 4 }
    ],
    "sidebarFooter": [{ "type": "page", "page": "focus-timer" }],
    "composerToolbar": [
      { "type": "tool", "tool": "summarize_chat", "label": "Summarize chat", "icon": "sparkles", "arguments": { "words": 120 } },
      { "type": "command", "command": "translate" }
    ]
  }
}
```

An item is one of three kinds. Each kind takes only its own fields; `label`
and `icon` are optional on all of them except that a tool button needs a
`label`.

| Item | What it does |
|---|---|
| `{ "type": "page", "page": "weather" }` | A page tile. A page whose `page.json` widget block has `compact` runs in the slot, in a strip about 40 px high, and a click opens it as a card under the tile, at its widget size. The card is the same frame: the page is told its new size and is not reloaded. A page without `compact` is a button that opens the page. `folder` names the shared folder of a shared page. |
| `{ "type": "tool", "tool": "summarize_chat", "label": "Summarize chat" }` | A tool button with fixed `arguments` (at most 16 plain values, 2 KiB). A click first asks you, and shows the tool and its arguments. In a chat it then sends a visible message that asks the assistant to run that tool; in the sidebar it opens a new chat with that message as a draft for you to send. The tool's own approval in the chat still applies. |
| `{ "type": "command", "command": "translate" }` | One of your commands. Commands come in a later release: until then the item is kept in the file and shows nothing. |

A slot never hides the shell: the chat's title, the model, find and settings
stay. The same item can be in two slots, but only once in one slot. The server
refuses a file with an unknown slot, a slot over its cap, an item without the
field of its kind or with the field of another kind, or an item listed twice.
A save from the tab sends only `slots`, merged item by item with the file as it
is then, so a change that the assistant made while the tab was open is not lost.
Without `slots` the app looks as before.

Pages in slots and on Home run in the same sandbox as everywhere, and the app
keeps at most eight page frames running at once (the Home tiles and the slots
together); the others show as a card that runs on a click, which stops the
frame that started first. A page frame that keeps the computer busy or stops
answering is stopped with **Reload**; see
[Pages](/features/pages/#a-page-in-a-slot). A page of a shared folder runs in a
slot only after you allow it there, apart from opening it.

## The LocalAI folder

The settings files live in one visible folder, `LocalAI/`, at the top of
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
| GET | `/api/chats/wellknown/{name}` | Reads a file: `persona`, `user`, `shortcuts`, `setups`, `defaults`, `home`, `ui`, `nav`, or `widget-data` with `?path=` |
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
