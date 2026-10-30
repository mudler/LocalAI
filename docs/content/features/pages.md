+++
disableToc = false
title = "Pages"
weight = 22
url = "/features/pages/"
+++

A page is a small HTML page in your workspace that LocalAI shows as part of its
UI. The assistant writes one when you ask ("make me a task board"), or you write
it yourself. A page keeps its data in files next to it, so the assistant can
read and change the same data in a chat.

A page runs in a sandboxed frame. It cannot reach the network, the rest of
LocalAI, your other files or your other chats. It talks to LocalAI only through
a small bridge, `window.localai`.

{{% notice note %}}
Pages need the `chats` feature and the `pages` feature. An administrator always
has `pages`. For other users it is off until an administrator switches it on in
the user's permissions (**Users**, then **Pages (HTML pages in the
workspace)**). When authentication is off, the single user has it. The files of
the chats must be on (see [Chats]({{%relref "features/chats" %}})).
{{% /notice %}}

## Where a page lives

A page is a folder of your home:

```
pages/
  tasks/
    index.html     the page (required, at most 1 MiB)
    page.json      title, icon and description (optional)
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

## In the UI

- **Pages** in the sidebar opens the list of your pages. The entry shows only
  when you have at least one page, and the first time it appears it carries a
  small **New** label, until you open it or for a week (switch it off with
  **Notes about new things** in Account). Pinned pages show under it.
- The list groups your pages under **Yours**. Each row shows the title, the
  description, the folder and when the page changed last and who wrote it (you
  or the assistant), with **Open**, a pin toggle (**Pin to sidebar**) and a
  menu: **Ask the assistant to change this** (a new chat with the page's path
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
- Pins are kept in `ui.json` of your home, under `pinnedPages` (a list of page
  names). A pin or unpin keeps every other key of the file.

## How the assistant makes a page

Ask in a chat, for example "Make me a page with a reading list in
pages/reading/". The assistant writes `pages/reading/index.html` and a data
file with its file tools. The page shows up under **Pages** as soon as the
change is saved. To change a page, open it and use **Ask the assistant to
change this**.

Tell the assistant to use `window.localai` for data, never `fetch`, and to keep
the page in one `index.html` with inline script and style: the sandbox blocks
everything else. A form cannot be submitted in the sandbox, so a page handles
the click of a button and the Enter key itself.

## The bridge

Inside the frame, `window.localai` has these calls. Each returns a promise. A
path is relative to the page's own folder.

| Call | Result |
|---|---|
| `localai.files.read(path)` | `{path, content, version, size}`. Text files only (UTF-8), at most 1 MiB. |
| `localai.files.write(path, content, {base})` | `{path, version, size, conflict_of}`. Creates missing folders. Without `base` the file is replaced. With `base` (the `version` you read), a file that changed since is kept and your content goes to a conflict copy, named in `conflict_of`. |
| `localai.files.list(dir)` | `[{path, kind, size, version}]`. `dir` is optional; the default is the page's folder. |
| `localai.chat.send(text)` | `{sent: true, chat_id}` or `{sent: false}`. See below. |
| `localai.on('files.changed', fn)` | Calls `fn({paths})` when files of the page's folder change. `paths` are relative to the folder. When LocalAI cannot tell what changed (for example after the window was in the background), `fn` gets `{paths: [], resync: true}`: read again what you show. Returns a function that removes the listener. |
| `localai.page` | `{name, title}` of the page. |

Example: a counter that keeps its value in `count.json`.

```html
<!doctype html>
<button id="b">Count</button>
<script>
  let count = 0
  const b = document.getElementById('b')
  const show = () => { b.textContent = 'Count: ' + count }
  localai.files.read('count.json')
    .then(f => { count = JSON.parse(f.content).count })
    .catch(() => {})   // no file yet
    .finally(show)
  b.onclick = () => {
    count++
    show()
    localai.files.write('count.json', JSON.stringify({ count }))
  }
  localai.on('files.changed', ev => {
    if (ev.paths.includes('count.json')) {
      localai.files.read('count.json').then(f => { count = JSON.parse(f.content).count; show() })
    }
  })
</script>
```

Your own writes also come back as `files.changed`. Compare the `version` you
got from `write` with the one `read` gives to skip them.

### chat.send

`chat.send(text)` never starts the assistant by itself. LocalAI shows the
message above the page with a choice of chat: the page's own chat (created on
first use, titled "Page: <title>") or one of your recent chats. Nothing is sent
until you choose **Send**. The message is then added to the chat as your
message, starting with `From the page "<title>" (pages/<name>):`, so you can
see it in the chat before the assistant answers. **Dismiss** resolves the
promise with `{sent: false}`.

The page's own chat is marked as started by a page. Like a chat that a trigger
starts, it gets no tool that acts at once and cannot create triggers, because
its first message was written by a page and not by you.

Tool calls through the bridge are not available.

## A page that starts a run

A page cannot call the assistant on its own, but it can write files, and a
[file trigger](/features/chats-agents/#triggers) can watch a folder of the
page. Each file the page writes there starts a run, which answers by writing a
file the page reads back.

1. The assistant makes the folder `pages/ask/requests/` (a trigger needs a
   folder that exists) and proposes a trigger:
   `{"when": {"file_arrives_in": "/pages/ask/requests"}, "prompt": "Read the request file named in this message, do what it asks, and write the answer to pages/ask/answers.json.", "writes": "pages/ask/answers.json"}`.
   You confirm it on its card.
2. The page writes a request:
   `localai.files.write('requests/' + Date.now() + '.json', JSON.stringify({text}))`.
3. The page listens for the answer:
   `localai.on('files.changed', (ev) => { if (ev.paths.includes('answers.json')) show() })`.

Every request is one run, so it costs tokens. A page cannot start runs in a
loop: a file waits until it has not changed for 30 seconds, a file that changes
again after a run began waits for the trigger's minimum gap (one minute by
default), the run's own writes start nothing, and the trigger has a daily limit
(24 runs by default). The bridge also limits writes (below).

## Limits

| Limit | Value |
|---|---|
| `index.html` | 1 MiB |
| A file that the bridge reads or writes | 1 MiB, UTF-8 text |
| Path | 256 bytes, 8 folders deep |
| Writes | 20 at once, then one every 2 seconds, per page |
| `chat.send` | 3 at once, then one every 20 seconds, per page; 4000 characters |
| Pages in a listing | 200 |

A page cannot write its own `index.html` or `page.json`: only you and the
assistant change a page's code. Debounce writes in a page that saves while the
user types.

The rate limits are kept by each frontend. With several frontends, a page gets
one budget on each. They protect against a page that loops, not against a
person.

## The sandbox

- The frame is `<iframe sandbox="allow-scripts">` without `allow-same-origin`.
  The page has an opaque origin: it cannot read LocalAI's cookies, storage or
  DOM, cannot navigate the LocalAI window, open pop-ups or submit forms.
- The page is loaded with this content security policy in front of its own
  markup:

  ```
  default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline';
  img-src data:; font-src data:; connect-src 'none'; frame-src 'none';
  form-action 'none'; base-uri 'none'; object-src 'none'
  ```

  `fetch`, `XMLHttpRequest`, WebSocket, external scripts, styles, fonts and
  images, and nested frames are all blocked. Images work as `data:` URLs.
- A page can still navigate its own frame to another address. LocalAI sees
  the second load, closes the bridge and shows "The page tried to open another
  address" with **Reload**. The request of that navigation has left the
  browser by then, so do not run a page you do not trust with data you would
  not share.
- The bridge is a `MessageChannel` that LocalAI hands to the frame when it
  loads. Every call is checked by the server with your rights: a path that
  leaves the page's folder (`..`, an absolute path, a backslash, a control
  character) is refused. The workspace has no symbolic links, so a link cannot
  lead out of the folder either.
- A page runs as you: what it writes is saved as your change, and shows in the
  file history as "Page <name>: save <file>". Undo works as for any other
  change.

## Shared pages

A shared folder is a folder outside your home that an administrator gave you
access to, read-only or read-write (a workspace grant). Its pages are the
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
| **Cancel** | Nothing runs. The frame says "This page has not run", with **Review and allow** and **View source**. |

A consent is kept against the page (the node of its folder, so a rename does
not lose it and a page deleted and made again asks again) and the hash of its
code: `index.html` and `page.json`. When the author changes either, the next
open asks again and says what changed: the size of each file and the lines of
`index.html`, against the version you allowed. A page that is open when its
author changes it keeps running the version you allowed, and its strip says
that it changed, with **Review**. The data files of a page are not in the hash:
a page saves them all the time. A page whose code reads its data and runs it
as code does what its data says; you trust its author for that when you allow
it.

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
`shared_pages_enabled` in the [runtime settings](/features/runtime-settings/)
(or start with `LOCALAI_DISABLE_SHARED_PAGES=true`). Then the list still shows
the pages, dimmed, with "Shared pages are off on this server. An administrator
turned them off."; a page does not open; and the bridge refuses every call, so
an open shared page stops at its next call. Revoking a consent still works. The
files are kept.

## API

The UI uses these routes. They need the `chats` and `pages` features.

| Route | Use |
|---|---|
| `GET /api/chats/pages` | List your pages. |
| `GET /api/chats/pages/{name}` | A page with its `index.html` as `html`. |
| `GET /api/chats/pages/{name}/files?dir=` | `files.list` |
| `GET /api/chats/pages/{name}/file?path=` | `files.read` |
| `PUT /api/chats/pages/{name}/file` | `files.write`, body `{path, content, base}` |
| `POST /api/chats/pages/{name}/chat` | `chat.send` after you chose Send, body `{text, chat_id, model}` |
| `GET /api/chats/page-starters` | The starter pages. |
| `POST /api/chats/page-starters/{id}/copy` | Copy a starter into `pages/`. |
| `GET /api/chats/shared-pages` | The pages of your shared folders: `folder`, `folder_id`, `author_id`, `author_name`, `writable`, `hash`, `consent` (`none`, `allowed`, `changed`), and `enabled`. |
| `GET /api/chats/shared-pages/{folder_id}/{name}` | A shared page with `html`, `lines` and `consent` (`state`, and for the version you allowed `allowed_at`, `size`, `meta_size`, `lines`). |
| `PUT /api/chats/shared-pages/{folder_id}/{name}/consent` | Allow a version, body `{hash, once}`. `409` when the page changed since you read it. A visit (`once`) answers with a `token`. |
| `DELETE /api/chats/shared-pages/{folder_id}/{name}/consent` | Stop allowing it. |
| `GET .../files`, `GET` and `PUT .../file`, `POST .../chat` | The bridge of a shared page, as above. Each call sends `X-LocalAI-Page-Hash` (the version that runs) and, for a visit, `X-LocalAI-Page-Visit`. Without a consent: `403 page_not_allowed`. |

With shared pages off, every shared-pages route but the list and the revoke
answers `403 shared_pages_off`.

```bash
curl -s http://localhost:8080/api/chats/pages -H "Authorization: Bearer $KEY"
# {"pages":[{"name":"tasks","path":"/pages/tasks","title":"Tasks board",
#   "source":"own","author":"assistant","updated_at":"2026-10-29T09:00:00Z",
#   "updated_by":"you","rev":42,"size":5120}]}

curl -s -X PUT http://localhost:8080/api/chats/pages/tasks/file \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"path":"tasks.json","content":"[]"}'
```

The answers use the error types of the chats API: `400` for a bad name or path,
`403` when the feature is off or a page tries to change its own code, `404` for
a page or file that does not exist (also another user's), `413` for a file over
1 MiB, `415` for a file that is not text and `429` for too many writes or
messages.
