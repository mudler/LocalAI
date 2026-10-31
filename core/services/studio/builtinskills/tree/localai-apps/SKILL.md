---
name: localai-apps
description: Use when the user asks for an app, a tool, a tracker, a dashboard, a form, a game, a calculator, a planner or anything interactive to use inside LocalAI. Build it as a Page.
---

# Apps in LocalAI are Pages

LocalAI has its own app format: a Page. It is one HTML file in the user's home that LocalAI shows under **Pages** in the sidebar. The user can pin it and (with a shared folder) share it. Unless the user names a language or a runtime, "make me an app" means a Page. Do not start a Python, Node or desktop program for it.

## Decide

| The user wants | Do |
|---|---|
| An interactive thing to use here: tracker, to-do list, dashboard, form, game, calculator, planner, converter, timer | A Page (this skill) |
| The same, refreshed on a schedule or when a file arrives | A Page plus a trigger: read the `localai-workspace` skill, section "Updates on a schedule" |
| A Home widget (a list or a number) | The `localai-workspace` skill, not a Page |
| A page that should also show on Home | A Page with a `widget` block and a compact mode (section "On Home"), then the `localai-workspace` skill to add the tile |
| A real program: a script, a service, a CLI, something to install or deploy | Write that program as asked. First check the `Commands:` line of this chat: if nothing here runs commands, say so and give the files to run elsewhere |
| Data from the web | A Page cannot reach the network. Get the data with your web tools and write it to a file next to the page; the page reads it with `fetch('data.json')` |

If the user asked explicitly for Python, Node or another language, do that and skip this skill.

A script, test data or a draft that you make on the way goes in the chat's work folder that the prompt names (`/workspace/home/Chats/<chat>/work/`); the page folder holds only the page and its data.

## What a Page is

- Folder `/workspace/home/pages/<name>/`. The name is lowercase letters, digits and dashes, starting with a letter or digit, at most 64 characters (`reading-tracker`).
- `index.html` is required, at most 1 MiB. One file with the markup, style and script is simplest. Split into `app.js`, `style.css` or ES modules next to it only when it grows past what you can keep right; LocalAI puts those files into the page before it runs.
- `page.json` is optional: `{"title": "Reading tracker", "icon": "book", "description": "Books to read, with a rating"}`. The icon is one plain lowercase word. Always write it: the title is what the user sees in the list.
- Data files sit next to `index.html`, any name, any subfolder (`books.json`). Text only, at most 1 MiB each. Keep data in JSON, so you can read and change it in a chat later.

## The sandbox

The page runs in a frame with no network and no access to LocalAI or other files.

- Files next to the page work with relative URLs: `fetch('data.json')` and `XMLHttpRequest` (GET only, text and JSON; a missing file is a 404 response), `<script src="app.js">`, `<link rel="stylesheet" href="style.css">`, `<script type="module">` with `import './x.js'`, and small images (`<img src="logo.png">`, CSS `url()`; png, jpg, gif, webp, svg; 2 MiB of images in all).
- Nothing else is reachable: no `https://` URL, no CDN, no packages (`import 'react'`), no WebSocket, no file outside the page's folder (`../`), no fonts from files. A remote `fetch` fails with "network is off in pages". Write plain JavaScript with no build step.
- To save, use `localai.files.write`; `fetch` only reads.
- No `<form>` submit (it does nothing). Handle the button click and the Enter key.
- A link to an `https://` address works: LocalAI asks the user, then opens it in a new tab. Use plain `<a href="https://...">`; `window.open` returns `null`. A link to another file of the page opens nothing.
- No `localStorage` as the only copy of the data. Save to a file with `localai.files.write`; that is the copy the user and you can see.
- A page writes data files only (such as `.json` or `.txt`). `localai.files.write` refuses `.js`, `.mjs`, `.cjs`, `.css`, `.html`, `.htm`, `.svg`, `.xml`, `.xhtml`, `.wasm`, a file named `index` and `page.json` with the error code `forbidden`. You change the code of the page with the file tools, never the page itself.

## The bridge

`window.localai` is already there. Every call returns a promise. Paths are relative to the page's own folder.

- `localai.files.read(path)` gives `{path, content, version, size}`. A failed call rejects with an error that has `status` (404 for a missing file).
- `localai.files.write(path, content)` gives `{path, version, size}`. It creates folders. Writes are limited (a burst of 20, then one every 2 seconds), so save after a short pause, not on each key.
- `localai.files.list(dir)` gives `[{path, kind, size, version}]`.
- `localai.on('files.changed', fn)` calls `fn({paths})` when a file of the folder changes, and `fn({paths: [], resync: true})` when it cannot tell what: read again. It returns a function that removes the listener. Your own writes come back too.
- `localai.chat.send(text)` offers a message to the user, who must press Send. Use it for "Ask the assistant" buttons, never as a way to run the assistant on its own.
- `localai.on('visibility', fn)` calls `fn({visible})` when the page goes out of sight (hidden tab, or its tile on Home scrolled away) and back. Stop timers while `visible` is false.
- `localai.page` is `{name, title, mode, size}`: `mode` is `page`, or `widget` in a tile on Home, where `size` is `small`, `medium` or `large`.

The theme is the `data-theme` attribute (`light` or `dark`) on `<html>`. Define colours as CSS variables and override them under `:root[data-theme="dark"]`.

## Make it good

- Responsive: no fixed widths, `flex-wrap`, inputs with `min-width: 0`. Touch targets at least 36px high.
- Accessible: real `<button>` elements, a label or `aria-label` on every input, visible focus, text contrast in both themes.
- Build text with `textContent`, never `innerHTML`: items are data.
- On start, show an empty state and not an error when the data file does not exist yet.
- Show a short "Saved" or "Not saved: ..." status.
- Write the whole data file at once, never half of it.

## On Home

A page can also be a tile on Home. Add `"widget": {"size": "small"}` to `page.json` (`small`: one line or a number; `medium`: a short list; `large`: a table or a chart; an optional `"title"` for the tile). In a tile the same page runs with `localai.page.mode === 'widget'` and `<html data-mode="widget" data-size="...">`. Give it a compact layout: the few facts that matter, no inputs, no scroll. Do not shrink the full page. The tile has its own bar with the title and an "Open page" link, so leave out the page's header. `chat.send` does not work in a tile.

A news ticker that is a list in its own view and one rotating line on Home:

```html
<!doctype html>
<style>
  body { margin: 0; padding: 16px; font: 14px/1.45 system-ui, sans-serif; }
  :root[data-mode="widget"] body { padding: 10px 14px; }
  :root[data-mode="widget"] li:not(.now) { display: none; }
</style>
<ul id="news"></ul>
<script>
  const list = document.getElementById('news')
  const compact = localai.page.mode === 'widget'
  let items = [], i = 0, timer = 0
  const show = () => {
    list.textContent = ''
    items.forEach((it, k) => {
      const li = document.createElement('li')
      li.textContent = it.title
      if (!compact || k === i % items.length) li.className = 'now'
      list.appendChild(li)
    })
  }
  const load = () => fetch('news.json').then(r => r.json()).then(d => { items = d.items || []; show() }).catch(() => {})
  const rotate = () => { clearInterval(timer); if (compact) timer = setInterval(() => { i++; show() }, 5000) }
  localai.on('files.changed', ev => { if (ev.resync || ev.paths.includes('news.json')) load() })
  localai.on('visibility', ({ visible }) => { if (visible) rotate(); else clearInterval(timer) })
  load().then(rotate)
</script>
```

## Example: reading tracker

`/workspace/home/pages/reading-tracker/page.json`:

```json
{"title": "Reading tracker", "icon": "book", "description": "Books to read and finished, saved in books.json"}
```

`/workspace/home/pages/reading-tracker/index.html`:

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Reading tracker</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
  :root { --bg: #f6f7f9; --card: #fff; --line: #d9dde3; --text: #121417; --muted: #5d6673; --accent: #1f6feb; }
  :root[data-theme="dark"] { --bg: #0b0d10; --card: #171c22; --line: #29313b; --text: #e6e9ee; --muted: #8b95a3; --accent: #58a6ff; }
  * { box-sizing: border-box; }
  body { margin: 0; padding: 16px; background: var(--bg); color: var(--text); font: 14px/1.45 system-ui, sans-serif; }
  .add { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 12px; }
  input[type=text] { flex: 1 1 200px; min-width: 0; min-height: 36px; padding: 6px 10px; border: 1px solid var(--line); border-radius: 6px; background: var(--card); color: var(--text); font: inherit; }
  button { min-height: 36px; padding: 6px 12px; border: 1px solid var(--line); border-radius: 6px; background: var(--card); color: var(--text); font: inherit; cursor: pointer; }
  button.primary { background: var(--accent); border-color: var(--accent); color: #fff; }
  :focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  ul { list-style: none; margin: 0; padding: 0; display: grid; gap: 8px; }
  li { display: flex; align-items: center; gap: 10px; padding: 8px 12px; border: 1px solid var(--line); border-radius: 8px; background: var(--card); }
  li span { flex: 1; overflow-wrap: anywhere; }
  li.done span { color: var(--muted); text-decoration: line-through; }
  #status, #counts { color: var(--muted); font-size: 12px; min-height: 1em; }
</style>
</head>
<body>
<div class="add">
  <input id="title" type="text" maxlength="200" placeholder="A book to read" aria-label="A book to read">
  <button id="add" class="primary" type="button">Add</button>
</div>
<p id="counts"></p>
<ul id="list"></ul>
<p id="status" role="status"></p>
<script>
(function () {
  var FILE = 'books.json'
  var data = { books: [] }
  var version = 0
  var timer = null
  var list = document.getElementById('list')
  var statusEl = document.getElementById('status')

  function say(t) { statusEl.textContent = t }

  function render() {
    list.textContent = ''
    var left = data.books.filter(function (b) { return !b.done }).length
    document.getElementById('counts').textContent = data.books.length === 0
      ? 'Nothing here yet. Add your first book.'
      : left + ' to read, ' + (data.books.length - left) + ' finished'
    data.books.forEach(function (b) {
      var li = document.createElement('li')
      if (b.done) li.className = 'done'
      var box = document.createElement('input')
      box.type = 'checkbox'
      box.checked = !!b.done
      box.setAttribute('aria-label', 'Finished: ' + b.title)
      box.onchange = function () { b.done = box.checked; changed() }
      var name = document.createElement('span')
      name.textContent = b.title
      var del = document.createElement('button')
      del.type = 'button'
      del.textContent = 'Remove'
      del.onclick = function () { data.books = data.books.filter(function (x) { return x !== b }); changed() }
      li.appendChild(box); li.appendChild(name); li.appendChild(del)
      list.appendChild(li)
    })
  }

  function changed() {
    render()
    clearTimeout(timer)
    timer = setTimeout(function () {
      localai.files.write(FILE, JSON.stringify(data, null, 2)).then(
        function (f) { version = f.version; say('Saved') },
        function (e) { say('Not saved: ' + e.message) })
    }, 400)
  }

  function load() {
    return localai.files.read(FILE).then(function (f) {
      if (f.version === version) return
      var next = JSON.parse(f.content)
      if (!next || !Array.isArray(next.books)) throw new Error('books.json has no books list')
      data = next
      version = f.version
      render()
    }).catch(function (e) {
      // No file yet is normal: the page starts empty.
      if (version !== 0) say('Could not read ' + FILE + ': ' + e.message)
    })
  }

  function add() {
    var input = document.getElementById('title')
    if (!input.value.trim()) return
    data.books.unshift({ id: 'b' + Date.now(), title: input.value.trim(), done: false })
    input.value = ''
    changed()
  }

  document.getElementById('add').onclick = add
  document.getElementById('title').onkeydown = function (ev) { if (ev.key === 'Enter') add() }
  localai.on('files.changed', function (ev) { if (ev.resync || ev.paths.indexOf(FILE) >= 0) load() })

  render()
  load()
})()
</script>
</body>
</html>
```

Seed `books.json` only if the user gave you data: `{"books": [{"id": "b1", "title": "...", "done": false}]}`.

## After you write it

Tell the user in one or two lines: the Page is called "<title>" and is under **Pages** in the sidebar (files in `pages/<name>/`, data in `<file>.json`), that they can pin it there, and what it cannot do. To change it later, they can open it and use **Ask the assistant to change this**.

Say what a Page cannot do when it matters for the request: it has no server process, no packages and no network. For work that must happen by itself, offer a trigger (never say one is on until the user confirms its card). For outside data, offer to fetch it with your web tools into a data file. If the idea needs a real program and commands run in this chat, offer the Sandbox.

Pages need the `pages` feature. If writing under `pages/` is refused or the Pages screen is missing, say so and keep the files where they are.
