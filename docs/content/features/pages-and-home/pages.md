+++
url = "/features/pages/"
disableToc = false
title = "Pages"
weight = 10
description = "Pages are small HTML pages in your workspace, shown in a sandboxed frame and usable on Home."
keywords = ["pages","html","home"]
+++

A page is a small HTML page in your workspace that LocalAI shows as part of its
UI. The assistant writes one when you ask ("make me a task board"), or you write
it yourself. A page keeps its data in files next to it, so the assistant can
read and change the same data in a chat.

A page runs in a sandboxed frame. It can't make network requests, and it can't reach the rest of
LocalAI, your other files or your other chats. It talks to LocalAI only through
a small bridge, `window.localai`.

{{% callout style="warning" %}}
A page can still navigate its own frame to another address. That sends one request out of your browser before LocalAI closes the page. Don't run a page you don't trust with data you wouldn't share. See [The sandbox]({{% relref "/features/pages-and-home/pages-reference#the-sandbox" %}}).
{{% /callout %}}

{{% callout style="note" %}}
Pages need the `chats` feature and the `pages` feature. An administrator always
has `pages`. For other users it is off until an administrator switches it on in
the user's permissions (**Users**, then **Pages (HTML pages in the
workspace)**). When authentication is off, the single user has it. The files of
the chats must be on (see [Chats]({{%relref "/features/chats-and-agents/chats" %}})).
{{% /callout %}}

To put a page on Home, see [Pages on Home and in the dock]({{% relref "/features/pages-and-home/pages-on-home" %}}). To keep it current with a schedule, see [Home widgets and live pages]({{% relref "/features/pages-and-home/home-widgets" %}}). To write a page yourself, see [Pages reference]({{% relref "/features/pages-and-home/pages-reference" %}}).

## How the assistant makes a page

Ask in a chat, for example "Make me a page with a reading list in
pages/reading/". The assistant writes `pages/reading/index.html` and a data
file with its file tools. The page shows up under **Pages** as soon as the
change is saved. To change a page, open it and use **Ask the assistant to
change this**.

One `index.html` with inline script and style is the simplest page. A page
can also keep its script, style sheets, modules, small images and data in
files next to it (see [Files next to the page]({{% relref "/features/pages-and-home/pages-reference#files-next-to-the-page" %}})). A
form cannot be submitted in the sandbox, so a page handles the click of a
button and the Enter key itself.

## In the UI

- **Pages** in the sidebar opens the list of your pages. The entry shows only
  when you have at least one page, and the first time it appears it carries a
  small **New** label, until you open it or for a week (switch it off with
  **Notes about new things** in Account). Pinned pages show under it.
- The list groups your pages under **Yours**. Each row shows the title, the
  description, the folder and when the page changed last and who wrote it (you
  or the assistant), with **Open**, a pin toggle (**Pin to sidebar**) and a
  menu: **Open in dock** (a new chat with the page beside it, see
  [The dock]({{% relref "/features/pages-and-home/pages-on-home#the-dock" %}})), **Ask the assistant to change this** (a new chat with the page's path
  in the message box), **Show its files** (the folder in Files), **Rename**
  (moves the folder) and **Delete page** (moves the folder to the Trash, where
  Files can restore it). Above eight pages, a filter field shows over the list.
  **Ask for a new page** opens a chat with "Make me a page that " in the
  message box.
- With no pages, the screen offers **Ask for a page**, and **Start from a task
  board** or **Start from a reading list**, which copy a starter page into
  `pages/task-board/` or `pages/reading-list/`. The copy is yours; ask the assistant to change it.
  Two starter pages ship with LocalAI: a task board (`task-board`, data in
  `tasks.json`) and a reading list (`reading-list`, data in `reading.json`).
  Home offers both under **Or start from a ready page**.
- Open a page to see it at `/app/pages/<name>`. The strip above it says that
  the page runs as you, when and by whom it was last updated, and has
  **Reload** and **Ask the assistant to change this**. When the assistant
  changes `index.html`, the page reloads. When only a data file changes, the
  page gets a `files.changed` event and stays as it is. Switching between the
  light and dark theme reloads the page too: it gets the theme as `data-theme`
  on its `<html>` element.
- Pins are kept in `LocalAI/ui.json` of your home, under `pinnedPages` (a list of page
  names). A pin or unpin keeps every other key of the file.
- The first pinned page is your **start page**: the sidebar marks it with a
  **Start page** label (see [A start page]({{% relref "/features/pages-and-home/pages-on-home#a-start-page" %}})).

## Where a page lives

A page is a folder of your home:

```
pages/
  tasks/
    index.html     the page (required, at most 1 MiB)
    page.json      title, icon, description and widget (optional)
    tasks.json     data files of the page, any name, any subfolder
```

- The folder name is the page name: lowercase letters, digits and dashes, 1 to
  64 characters, starting with a letter or a digit (`^[a-z0-9][a-z0-9-]{0,63}$`).
  A folder with another name, or without `index.html`, is not a page.
- `page.json` is optional:

  ```json
  { "title": "Tasks board", "icon": "list-checks", "description": "What I do this week" }
  ```

  A missing or broken `page.json` does not stop the page; the folder name is
  then the title.
- A `widget` block in `page.json` lets the page be a tile on Home (see
  [A page on Home]({{% relref "/features/pages-and-home/pages-on-home#a-page-on-home" %}})):

  ```json
  { "title": "Clock", "widget": { "size": "small", "title": "Now" } }
  ```

## When a page does not work

A page that throws an error in its script, or leaves a promise unhandled, shows
nothing about it: the assistant cannot run JavaScript, so it could not see the
error. LocalAI keeps the error, so the assistant can read it.

- The frame of the page catches `window.onerror` and unhandled rejections and
  hands them to LocalAI over the bridge. Nothing shows in the page, no badge or
  button is added, and nothing goes into a chat.
- When you say a page does not work, the assistant calls its read-only tool
  `page_errors` with the page name. The tool gives the message, the line and
  column when known (the line counts from the top of your `index.html`), how
  often it happened and when it last happened. Without a name it lists the pages
  that have recent errors, newest first. The built-in `localai-apps` skill tells
  the assistant to read the error and fix it before it rewrites the page.
- A page that throws no error but looks wrong has nothing to read. The
  assistant then asks you what you see.

What is kept:

| Rule | Value |
|---|---|
| Where | On the server, per user and per page name, outside your files. A tile of the page on Home uses the same page name. With a database the errors are in the table `studio_page_errors`, which every frontend shares; without one, in `chats/pages/page-errors.json` under the data path. |
| Who records | Only you, when you view your own page or its tile. A viewer of a shared page records nothing. |
| Same error | Counted once: same message at the same file, line and column. |
| How many | The 10 newest different errors of a page. |
| Length | A message is cut at 500 bytes and a stack at 1500. |
| Noise | The browser's own errors, such as `ResizeObserver loop` and `Script error.`, are dropped. |
| Rate | The frame sends an error once and counts repeats, so a throw in a 60 frames per second loop costs a message every few seconds. The server also limits a page to a burst of 20 reports, then one every 2 seconds. |
| Cleared | When any file of the page changes (a data file the page saves counts too), so the assistant never reads the error of code that is gone. |

An error message can quote data of your page. The server never writes it to a
log. The assistant reads it only when it calls `page_errors`, and the tool
result says that the text is data and not instructions.

The frame has no network and no new right. A page
cannot read the errors back, and it cannot stop the capture.

## Shared pages

A shared folder is a folder outside your home that an administrator gave you
access to, read-only or read-write (a workspace grant). An administrator
creates the shared folders and chooses their members in the **Shared** tab of
**Files** or through the API (see [Shared
folders]({{% relref "/features/files-and-documents/files-reference#shared-folders" %}})). Its pages are the
folders `pages/<name>/` with an `index.html`, as in your home. They show on the
Pages screen under **Shared with you**, with their author (the account that
last wrote `index.html`), the folder, and a badge: **Not allowed yet**,
**Changed since you allowed it** or **Allowed**. A user without a grant on the
folder sees nothing of it.

A shared page runs as you, so it runs only after you allow it. Opening one the
first time shows a sheet that says who wrote it, where it lives, what it can do
(read and write files in its own folder, write a message into a chat that you
see before the assistant answers), what it cannot do (read your other files or
chats, reach the network, use tools or change your settings), and one limit of
the sandbox in plain words: a page can load another address in its own frame,
which sends one request out before the page stops. **Cancel** has the focus;
Escape is Cancel. **View source** shows `index.html` as text.

| Choice | Result |
|---|---|
| **Allow** | Kept for this version of this page. It runs now and each time you open it. |
| **Allow once** | It runs for this visit. You are asked again next time. A visit ends after 12 hours. |
| **Allow on Home, in slots and in the dock** | Shown by a tile on Home or in a slot, the page area and the dock. Kept for this version, and it also lets the page run there without a click. |
| **Cancel** | Nothing runs. The frame says "This page has not run", with **Review and allow** and **View source**. |

A consent is kept against the page (the node of its folder, so a rename does
not lose it and a page deleted and made again asks again) and the hash of its
code: `index.html`, `page.json` and the files of code that LocalAI puts into
the page: every `<script src>`, `<link rel="stylesheet">`, relative CSS
`@import` and ES module import of the folder, followed through the whole
module graph (the `code_files` of the page in the API). A file of code that
the page names and that is missing counts too, so adding it later also asks
again. When the author changes any of them, the next open asks again and says
what changed: the size of each file and the lines of `index.html`, or which
files of code there are when only one of them changed. A page that is open when its
author changes it keeps running the version you allowed, and its strip says
that it changed, with **Review**; a file of code it then asks for is refused
(`409`), so new code never runs under the old consent. The check ignores the
case of the name, as the files do: `Index.html` or `JS/App.js` is the same file
of code as `index.html` or `js/app.js`. The UI puts no text file
into a shared page that is not in its `code_files`.

The data files of a page are not in the hash: a page saves them all the time,
and images are only pictures. A page whose code reads its data and runs it as
code does what its data says; you trust its author for that when you allow it.
The files go into the page only after you allow it, with the same consent
check as the bridge. To find the files of code, the server reads the page's
markup and modules; each frontend keeps what it found per file content in a
small cache, which only saves reads (the same content gives the same files on
every frontend).

A consent for one page never lets another page run, even one with the same
code, and it is yours only. **Stop allowing this page** in the page's menu
revokes it. Every call of a shared page's bridge checks the consent on the
server, so a page that you stop allowing, in this tab or in another one, stops
at its next call.

A shared page uses the same bridge and limits as your own, with your own grant
on the folder: in a folder you can only read, its writes are refused ("you can
only read this shared folder"). It reads, writes and lists only its own folder.
A message it sends goes to your chat and says which shared page it comes from.

The consents are kept on the server, not in your files: the assistant, a page
and a trigger run can all write your files, so a consent there could be written
by the code it is meant to hold back. With a database (authentication on), they
are in the tables `studio_page_consents` and `studio_page_consent_events`, which
every frontend shares; without one, in `chats/pages/consents.json` under the
data path. Each **Allow**, **Allow once** and revoke is also written to the
consent audit (`studio_page_consent_events`: who, which page and folder, which
hash, when) and to the log.

An administrator can turn shared pages off at run time with
`shared_pages_enabled` in the [runtime settings]({{% relref "/administration/runtime-settings" %}})
(or start with `LOCALAI_DISABLE_SHARED_PAGES=true`). Then the list still shows
the pages, dimmed, with "Shared pages are off on this server. An administrator
turned them off."; a page does not open; and the bridge refuses every call, so
an open shared page stops at its next call. Revoking a consent still works. The
files are kept.

## Limits

| Limit | Value |
|---|---|
| `index.html` | 1 MiB |
| A file that the bridge reads or writes | 1 MiB, UTF-8 text |
| Files put into the page (scripts, styles, modules, images) | 4 MiB in all, images 2 MiB |
| Path | 256 bytes, 8 folders deep |
| Writes | 20 at once, then one every 2 seconds, per page |
| `chat.send` | 3 at once, then one every 20 seconds, per page; 4000 characters |
| Pages in a listing | 200 |

A page writes data files only. A write of a code or document file is refused
with the error code `forbidden` and the message "a page can write data files
only (such as .json or .txt); ask the assistant to change the code of the
page". That covers the extensions `.js`, `.mjs`, `.cjs`, `.css`, `.html`,
`.htm`, `.svg`, `.xml`, `.xhtml` and `.wasm` (in any case, in the page's folder
and its subfolders), a file named `index` and `page.json`. Because a page loads
its own scripts and styles, a page that could rewrite them would run new code on
the next load. The server enforces this on the page's bridge; you and the
assistant write the same files through the Files page and the file tools. A
shared page follows the same rule, and a consent is kept against the code you
allowed, which a page cannot change. Debounce writes in a page that saves while the
user types.

The rate limits are kept by each frontend. With several frontends, a page gets
one budget on each. They protect against a page that loops, not against a
person.
