+++
url = "/features/pages-reference/"
disableToc = false
title = "Pages reference"
description = "The bridge, files, links, sandbox rules and routes of a page."
weight = 12
+++

Write a page yourself: the `window.localai` bridge, files next to the page, links, runs that a page starts, the sandbox rules and the routes that the UI uses. If you ask the assistant to write pages, [Pages]({{% relref "/features/pages-and-home/pages" %}}) is enough.

## The bridge

Inside the frame, `window.localai` has these calls. Each returns a promise. A
path is relative to the page's own folder.

| Call | Result |
|---|---|
| `localai.files.read(path)` | `{path, content, version, size}`. Text files only (UTF-8), at most 1 MiB. A failed call rejects with an error that has `status` (`404` for a missing file, `413`, `415`). |
| `localai.files.write(path, content, {base})` | `{path, version, size, conflict_of}`. Creates missing folders. Without `base` the file is replaced. With `base` (the `version` you read), a file that changed since is kept and your content goes to a conflict copy, named in `conflict_of`. |
| `localai.files.list(dir)` | `[{path, kind, size, version}]`. `dir` is optional; the default is the page's folder. |
| `localai.chat.send(text)` | `{sent: true, chat_id}` or `{sent: false}`. See below. |
| `localai.on('files.changed', fn)` | Calls `fn({paths})` when files of the page's folder change. `paths` are relative to the folder. When LocalAI cannot tell what changed (for example after the window was in the background), `fn` gets `{paths: [], resync: true}`: read again what you show. Returns a function that removes the listener. |
| `localai.on('visibility', fn)` | Calls `fn({visible})` when the page goes out of sight (the tab is hidden, or its tile on Home scrolled away) and when it comes back. Pause timers and animations while `visible` is `false`. |
| `localai.on('theme', fn)` | Calls `fn({theme})` (`'dark'` or `'light'`) when the person switches the LocalAI theme. The page is not reloaded: `data-theme` and `color-scheme` on `<html>` change by themselves, so CSS that reads them needs no code. |
| `localai.on('mode', fn)` | Calls `fn({mode, size})` when a tile changes size without a reload: a tile in a slot that opens as a card, and back. `localai.page` and the `data-mode` and `data-size` attributes of `<html>` change first. |
| `localai.page.resize(height)` | Asks for a height in pixels. Only the page area of Home uses it (see [the page area]({{% relref "/features/pages-and-home/pages-on-home#a-page-in-the-page-area-of-home" %}})); elsewhere it resolves to `{height: null}`. Without `height`, the bridge measures the document (the box of `<html>`). Resolves to `{height}`, the height after the limits. |
| `localai.page` | `{name, title, mode, size}` of the page. `mode` is `page` in the full view and in the dock, `widget` in a tile, and `area` in the page area of Home; `size` is the tile's size (`compact`, `small`, `medium`, `large`), empty in the full view, the dock and the page area. The page cannot set them. |

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
first use, titled `Page: <title>`) or one of your recent chats. Nothing is sent
until you choose **Send**. The message is then added to the chat as your
message, starting with `From the page "<title>" (pages/<name>):`, so you can
see it in the chat before the assistant answers. **Dismiss** resolves the
promise with `{sent: false}`.

The page's own chat is marked as started by a page. Like a chat that a trigger
starts, it gets no tool that acts at once and cannot create triggers, because
its first message was written by a page and not by you.

Tool calls through the bridge are not available.

## Files next to the page

A page can name the files of its own folder with relative URLs, as a web page
does. Before the page runs, LocalAI reads them with your rights and puts them
into the page; while it runs, `fetch` and `XMLHttpRequest` read the folder.

| In the page | What happens |
|---|---|
| `fetch('data.json')`, `fetch('./data/x.json?v=2')` | Reads the file with `files.read`. The answer is a real `Response`: `200` with the text and a `content-type` from the extension, or `404` for a missing file (also `413` and `415`). `GET` and `HEAD` only. |
| `XMLHttpRequest` | The same, asynchronous only, `responseType` `''`, `text`, `json`, `arraybuffer` or `blob`. |
| `<script src="app.js">` | The script goes into the page at the same place. With `defer` or `async`, it goes to the end of the body. |
| `<link rel="stylesheet" href="style.css">` | Goes in as a `<style>`, with its relative `@import` and `url()`. |
| `<script type="module" src="main.js">`, `import './lib/x.js'`, `import('./x.js')` | The modules of the folder load from `blob:` URLs behind an import map. Cycles work. Only string specifiers that start with `./` or `../`. |
| `<img src="logo.png">`, `<source>`, `<video poster>`, CSS `url()` | Images (png, jpg, gif, webp, avif, bmp, ico, svg) go in as `data:` URLs. |

What does not work:

- The network: an `http:`, `https:` or `//host` URL. `fetch` rejects with
  `TypeError: network is off in pages; put the data in a file next to the page`;
  a script, style sheet or image with such a URL stays blocked by the policy.
- Files outside the page's folder: `../`, an absolute path, `%2e%2e/`, a
  backslash. They are never read.
- Packages (`import 'react'`), fonts from files, audio and video files, and
  binary files through `fetch` (the bridge reads UTF-8 text only).
- Writes through `fetch`: save with `localai.files.write`.

The files that go into the page are at most 4 MiB in all, and the images at
most 2 MiB. A script, style sheet or module that does not load (missing,
outside the folder, too large) shows a short notice at the bottom of the page
and a message in the browser console; an image that does not load gives only
the console message. When one of the files that went in changes, the page
reloads, as it does when `index.html` changes. Data read with `fetch` does
not reload the page: listen to `files.changed`.

## Links

A page cannot open a window or leave its frame. A click on a link to an
`http` or `https` address (also with Ctrl, Cmd or the middle button, or the
Enter key) and `window.open(url)` ask LocalAI instead: a bar above the page
shows the site, the whole address and **Open** and **Cancel**. **Open** opens
the address in a new tab with `noopener` and `noreferrer`; **Cancel** does
nothing. When the host is in punycode (letters of other alphabets, such as
`xn--pple-43d.com`), the bar also says how it reads (`аpple.com`), since it
may imitate another site.

- A link to `#id` scrolls the page to that element.
- A link to a file of the page (`other.html`) opens nothing; the console says
  why.
- `javascript:`, `data:`, `file:`, `blob:` and `//host` links and addresses
  with a user name or password are refused, with a message in the console.
- The bar shows one link: a new one replaces it. A page asks at most 5
  links at once, then one every 3 seconds.

`window.open` returns `null` in a page.

## A page that starts a run

A page cannot call the assistant on its own, but it can write files, and a
[file trigger]({{% relref "/features/chats-and-agents/chats-agents" %}}#triggers) can watch a folder of the
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

  The network is blocked for `fetch`, `XMLHttpRequest`, WebSocket, external
  scripts, styles, fonts and images, and nested frames. Images work as
  `data:` URLs. Links open only after you confirm them (see [Links](#links)).
  A page with ES modules of its folder also has `blob:` in
  `script-src`: its modules load from `blob:` URLs that the frame makes
  itself, which reach no network. `fetch` and `XMLHttpRequest` of the page
  are replaced by the bridge reads of [Files next to the
  page](#files-next-to-the-page).
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

## API

The UI uses these routes. They need the `chats` and `pages` features (the dock routes need only `chats`).

| Route | Use |
|---|---|
| `GET /api/chats/pages` | List your pages, each with its `widget` block when it has one. |
| `GET /api/chats/pages/{name}` | A page with its `index.html` as `html`. |
| `GET /api/chats/pages/{name}/files?dir=` | `files.list` |
| `GET /api/chats/pages/{name}/file?path=` | `files.read`. With `encoding=base64` the file must be an image (by extension) and `content` is its bytes in base64, with `media_type`. The UI uses it to put images into the page; the bridge reads text only. |
| `PUT /api/chats/pages/{name}/file` | `files.write`, body `{path, content, base}` |
| `POST /api/chats/pages/{name}/chat` | `chat.send` after you chose Send, body `{text, chat_id, model}` |
| `POST /api/chats/pages/{name}/errors` | An error that the script of your own page threw, body `{message, stack, file, line, column, count}`. The UI sends it; the assistant reads the errors with `page_errors`. `200` with `recorded` (false for browser noise), `429` when reported too fast. There is no route to read them back and none for shared pages. |
| `GET /api/chats/{id}/dock` | The dock of a chat: `{open, tabs: [{page, folder}], active, width}`, or `null`. The chat's detail (`GET /api/chats/{id}`) carries it as `dock`. |
| `PUT /api/chats/{id}/dock` | Replace the dock of a chat: at most 6 tabs, each page once (`folder` is the `folder_id` of a shared page, absent for your own), `active` the index of the tab in front, `width` 0 (the default) or 320 to 4096. A dock with no tab clears it. `400` out of bounds, `409` for a sub-agent run. The chat's event stream sends `dock` when it changes. |
| `GET /api/chats/page-starters` | The starter pages. |
| `POST /api/chats/page-starters/{id}/copy` | Copy a starter into `pages/`. |
| `PUT /api/chats/pages/{name}/consent` | Allow a version of your own page that others can edit (`others_can_edit`), body `{hash, once}`. `409` when the page changed since you read it. A visit (`once`) answers with a `token`. `embed` is refused with `400`: such a page never runs on Home, in a slot or in the dock by itself. A page that nobody else can edit needs no consent and answers `404`. Own browser session only (see below). |
| `DELETE /api/chats/pages/{name}/consent` | Stop allowing it. Own browser session only. |
| `GET /api/chats/pages/{name}/shares` | Who the page is shared with: `{name, enabled, hash, allowedCount, grants: [{userId, displayName, role, consent}]}`. `role` is `view`, `use` or `edit`. `userId` is an account ID, or `@everyone` (with an empty `displayName`). `consent` is `none`, `allowed` or `changed` for a named account, and absent for `@everyone`. `hash` is the current version, and `allowedCount` is how many named accounts allowed it. `enabled` is `false` when an administrator turned off the pages that people share; you can still list and take back. |
| `PUT /api/chats/pages/{name}/shares` | Share the page or change a role, body `{principal, role}`. `principal` is an account ID from the picker, or `@everyone`. `edit` for `@everyone` answers `400`. A lower role or a removed grant drops the consent of that account. Safe to repeat. The answer is the list of grants. |
| `DELETE /api/chats/pages/{name}/shares/{principal}` | Stop sharing with an account or with everyone. It works while sharing is off. Safe to repeat. |
| `GET /api/chats/pages/shares/search?q=` | The user picker: 3 to 64 characters of a display name, at most 20 results, each `{id, displayName}` and nothing else. 20 searches at once, then one every 2 seconds (`429 rate_limited`). |
| `GET /api/chats/shared-folders` | Your shared folders, with your `mode` and how many `pages` each has. `folder_id` of a shared page is the `id` of its folder. |
| `GET /api/chats/shared-pages` | The pages of your shared folders: `folder`, `folder_id`, `author_id`, `author_name`, `writable`, `hash`, `code_files`, `widget`, `consent` (`none`, `allowed`, `changed`), and `enabled`. |
| `GET /api/chats/shared-pages/{folder_id}/{name}` | A shared page with `html`, `lines` and `consent` (`state`, and for the version you allowed `allowed_at`, `size`, `meta_size`, `lines`, and `embed` when you allowed it on Home, in the slots and in the dock). |
| `PUT /api/chats/shared-pages/{folder_id}/{name}/consent` | Allow a version, body `{hash, once, embed}`. `409` when the page changed since you read it. A visit (`once`) answers with a `token`. `embed` also allows it on Home, in the slots and in the dock (a lasting consent, so not with `once`: `400`); a lasting consent to the same version keeps `embed`. |
| `DELETE /api/chats/shared-pages/{folder_id}/{name}/consent` | Stop allowing it. |
| `GET .../files`, `GET` and `PUT .../file`, `POST .../chat` | The bridge of a shared page, as above. Each call sends `X-LocalAI-Page-Hash` (the version that runs) and, for a visit, `X-LocalAI-Page-Visit`. Without a consent: `403 page_not_allowed`. A read of a file of code by a frame of an older version: `409`. |

With shared pages off, every shared-pages route but the list and the revoke
answers `403 shared_pages_off`.

### Sharing one of your pages

The share routes and the consent routes of an own page accept only your own browser session: a same-origin fetch with the header `X-LocalAI-Confirm` set to the page name (to the word `pages` for the picker), no API key, and no tool, agent or MCP marker. A call that does not fit answers `403` with the type `not_human_surface`. No MCP tool maps to these routes, because sharing and consent are never a tool. A page that is not yours and a page that does not exist get the same `403` (`authorization_error`). The routes need accounts: without them, they answer `409 sharing_disabled`.

These fields of a page in `GET /api/chats/pages` and `GET /api/chats/pages/{name}` are new:

| Field | Where | Meaning |
|---|---|---|
| `shared_with` | Your own page | How many accounts you shared it with (Everyone counts as one). |
| `shared_allowed` | `GET /api/chats/pages/{name}` | How many named accounts allowed the current version. |
| `others_can_edit` | Your own page | Another account has the Edit role. The page then has `hash`, `code_files`, `meta_size` and `consent` (`none`, `allowed`, `changed`), and it runs only after you allow its version. |
| `hash` | Your own page | Sent when the page is shared or others can edit it: the version that consents are kept against. |

The answer of `GET /api/chats/pages/{name}` for a page that others can edit also has `lines` and `consent` (`state`, `allowed_at`, `size`, `meta_size`, `lines`), as for a shared page. The consent never has `embed`.

For a page that others can edit, each bridge call of the page (`files`, `file`, `chat` under `/api/chats/pages/{name}/`) must send `X-LocalAI-Page-Hash` (the version that runs) and, for a visit allowed once, `X-LocalAI-Page-Visit`. Without a valid consent the call answers `403 page_not_allowed`, and a read of a file of code by a frame of an older version answers `409`. A page that nobody else can edit needs neither header.

For a page that a person shared with you, `GET /api/chats/shared-pages` and `GET /api/chats/shared-pages/{folder_id}/{name}` have these fields:

| Field | Meaning |
|---|---|
| `user_shared` | `true`: a person shared it, and no administrator checked it. |
| `role` | What you may do: `view`, `use` or `edit`. |
| `owner_name` | The name of the person whose home holds it. |
| `author_name` | Who last wrote its `index.html`. The account ID is not sent. |
| `turned_off` | `true` when an administrator turned off the pages that people share: the page is listed and does not open. |

Such a page has no `folder` and no `path`, because they would name the owner's account. `GET /api/chats/shared-pages` also has `user_shares_enabled`, which is `false` when the pages that people share are turned off. The bridge of such a page writes into the page folder of its owner: with `view`, a write answers `403`; with `use` or `edit`, a data file is saved and a code file is refused with `403`, so the page cannot change its own code.

The error types, besides those of the chats API:

| Status and type | When |
|---|---|
| `403 not_human_surface` | The call is not from your own browser session. |
| `403 authorization_error` | The page is not yours, or does not exist, or the call is not allowed. |
| `403 page_not_allowed` | The version that the frame runs is not allowed. |
| `403 user_page_shares_off` | An administrator turned off the pages that people share, and you open one. |
| `409 sharing_disabled` | Pages cannot be shared on this server, or an administrator turned sharing off (a new share or a search). |
| `409 conflict` | A limit (200 grants a page, 100 folders and pages a person), or a page that changed while you reviewed it. |
| `400 invalid_request_error` | A role that is not `view`, `use` or `edit`, `edit` for `@everyone`, or a person who is not an active account. |
| `429 rate_limited` | Too many searches for people. |

The setting that turns this off is `user_page_shares_enabled` (see [Runtime settings]({{% relref "/administration/runtime-settings" %}})).

There is no quota on what a person with the Use or Edit role writes into the page folder. The limits are those of the bridge: 1 MiB for each write, the write rate limit of [Limits]({{% relref "/features/pages-and-home/pages#limits" %}}), and 256 MiB for each file written through Files or a chat.

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
`403` when the feature is off or a page tries to write code (anything but a data file), `404` for
a page or file that does not exist (also another user's), `413` for a file over
1 MiB, `415` for a file that is not text and `429` for too many writes or
messages.
