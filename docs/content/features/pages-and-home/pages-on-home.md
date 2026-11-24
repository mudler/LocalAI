+++
url = "/features/pages-on-home/"
disableToc = false
title = "Pages on Home and in the dock"
description = "Put a page on Home as a tile, in a slot or in the page area, and keep pages open in the dock of a chat."
weight = 11
+++

Put a page on Home as a tile, in a slot or in the page area, and keep pages open next to a chat in the dock. This page says how a page asks for a place on Home, how big it may be and when it loads.

## A page on Home

A page with a `widget` block in its `page.json` can also be a tile on Home.
The tile runs the same page, in the same sandbox and with the same bridge, but
in its compact mode: `localai.page.mode` is `widget`, and `<html>` has
`data-mode="widget"` and `data-size="small|medium|large"`, so a style sheet can
switch layout without script. The page decides how its tile looks; LocalAI
does not shrink the full page.

| Field | Meaning |
|---|---|
| `widget.size` | The size of the tile when `home.json` gives the widget no `size`: `small` (one column, one row unit: a line or a number), `medium` (two columns and rows: a short list) or `large` (the full row, three row units: a table or a chart). Any other value is `medium`. |
| `widget.title` | The title of the tile, when it should be shorter than the page's. |
| `widget.compact` | `true`, or `{"width": 160}`: the page also has a layout for a slot (see [A page in a slot](#a-page-in-a-slot)). The width is in CSS pixels, 72 to 220 (120 by default); a value outside is clamped. |

A page without a `widget` block cannot be a tile. To put a page on Home, add
the block and a widget `{"id": "clock", "type": "page", "page": "clock"}` to
`LocalAI/home.json` (or pick the page in **Customise Home**, or ask the
assistant to "put it on Home"). A page of a shared folder also has
`"folder": <folder_id>`. Home shows at most three page tiles.

A small clock that is a full page in its own view and one line on Home:

```html
<!doctype html>
<style>
  body { font: 15px system-ui, sans-serif; margin: 0; padding: 24px; }
  #time { font-size: 64px; }
  :root[data-mode="widget"] body { padding: 12px 16px; }
  :root[data-mode="widget"] #time { font-size: 28px; }
  :root[data-mode="widget"] .more { display: none; }
</style>
<div id="time"></div>
<p class="more">The time where you are. On Home, only the time shows.</p>
<script>
  const el = document.getElementById('time')
  let timer = 0
  const tick = () => { el.textContent = new Date().toLocaleTimeString() }
  const start = () => { tick(); timer = setInterval(tick, 1000) }
  start()
  localai.on('visibility', ({ visible }) => {
    clearInterval(timer)
    if (visible) start()
  })
</script>
```

The tile has a bar with the page's icon and title, **Open page** and a refresh
button. Its frame is made only when the tile scrolls into view, and it is taken
down when the tile is far off screen; nearer, or in a hidden tab, the page gets
`visibility` `false` and should pause. A tile counts toward the eight live
frames of the app (see [Live frames and the budget of a tile](#live-frames-and-the-budget-of-a-tile)).
The height of each tile is fixed by its size, so nothing moves when a page
loads. A tile reads its data as the full page does, with `fetch` or
`localai.files.read`, and `files.changed` keeps it live.

In a tile, a link of the page asks before it opens, as in the full view.
`chat.send` is refused: open the page to send a message. When a page is gone,
cannot be read, or has no `widget` block, the tile says so with a link to
**Customise Home**. A shared page needs your consent first: the tile shows
**Allow this page**, which opens the trust sheet with **Allow on Home, in slots
and in the dock** (see [Shared pages in a slot and on Home](#shared-pages-in-a-slot-and-on-home)).

## A page in a slot

The slots of `LocalAI/ui.json` (the chat header, the sidebar footer and the
composer toolbar, see [Make LocalAI yours](/features/make-localai-yours/#slots))
show page tiles. A slot is a strip about 40 px high, so a page runs there only
when its widget block has `compact`: `<html>` then has `data-size="compact"`
and `localai.page.size` is `compact`. Draw one short line: a value and a word.

```json
{ "title": "Weather", "widget": { "size": "medium", "compact": { "width": 140 } } }
```

```css
:root[data-size="compact"] .full { display: none; }
:root[data-size="compact"] .strip { display: flex; height: 32px; align-items: center; }
```

A click on the tile opens it as a card under the tile (above it in the sidebar
and the message box), at the size of `widget.size`. The card is the same frame
in a bigger box: the bridge sends `mode` with the new size and the page keeps
its state; it is never reloaded. Escape, a click outside or the close button
puts it back in the strip. The card has the page's title, **Open**, and the line
"Sandboxed, no network". In the strip the page is shown, not used: a click
opens the card. A page without `compact` is a button that opens the page: in
the dock beside the chat while a chat is open (see [The dock](#the-dock)), in
the page view anywhere else. **Open** of a card does the same.

A tile in a slot reads its data as the full page does, and `files.changed` keeps
it live. `chat.send` is refused in a tile. A link asks before it opens.

### Live frames and the budget of a tile

The app keeps at most eight page frames running at once, for the page area and
the tiles on Home, the tiles in the slots and the page in front of the dock
together. A tile that gets none shows as a card with **Run**;
a click runs it and stops the frame that started first, which then shows as a
card. When a tile goes away, the next waiting tile runs.

A tile runs without a click, so it has a budget, and so does the page in
the dock. Its bridge sends a beat about
once a second with the time the page kept the browser busy since the last one
(its long tasks, and how late the beat's timer fired). LocalAI stops the frame
and shows **Reload** when:

| Reason | Limit |
|---|---|
| The page keeps the browser busy | more than 3 seconds of work within 10 seconds |
| The page stops answering while it is in sight | no beat for 5 seconds |
| The page does not start | no first beat within 10 seconds |

The page area of Home is watched the same way, but it must show its first
beat within 5 seconds of its start, and a failure shows the tile of
[the page area](#when-the-page-does-not-load) instead of **Reload** in a card.

A tile out of sight (a hidden tab, a tile scrolled away) is not watched,
because the browser slows its timers. Reload is manual: there is no retry loop.
The full page view has no budget. The browser decides how much a frame can do
before LocalAI can stop it; a frame that blocks the browser completely is
stopped only when it answers again.

### Shared pages in a slot and on Home

A shared page on Home, in a slot or in the dock starts as soon as the screen
shows, so allowing it to open is not enough: its tile shows **Allow** and the
trust sheet offers **Allow on Home, in slots and in the dock**. That consent is kept against the hash of
the page like **Allow**, so a change of its code asks again, and **Stop allowing
this page** removes both. **Allow once** runs the tile for this visit only.

## A page in the page area of Home

`LocalAI/home.json` can name one page that fills a full-width area on Home,
below the greeting, the composer and the shortcut chips and above the widgets:

```json
{ "pageArea": "today" }
```

`pageArea` is the name of one of your pages, `{"page": "today", "folder": 3}`
for a page of a shared folder (`folder` is the `folder_id` of the shared pages
list), or `null` (or no key) for no page area. Unlike a tile, the page needs no
`widget` block. Pick it in **Edit layout** on Home, under **Page area**, or ask
the assistant. The save is a normal change set, so it shows in the history and
can be reverted.

The page runs in the same sandbox, with the same bridge and the same budget as
a tile. It hears `localai.page.mode` `area` (and `<html data-mode="area">`) and
has no `size`: the area is the full width of Home. It is 240 px high until the
page says how high it is, and stays so for a page that never does; the page
scrolls inside it. To fit the area to its content, the page calls
`localai.page.resize()` when its content changes:

```js
new ResizeObserver(() => localai.page.resize()).observe(document.body)
```

The area follows, never lower than 160 px or higher than about 60% of the
window: a longer page scrolls inside. LocalAI applies at most four heights a
second (the last one asked), ignores a change of a few pixels and never reloads
the frame for it. A page whose `<html>` or `<body>` fills the frame
(`height: 100%`) measures the frame, not its content: pass the height of its
content, `localai.page.resize(el.offsetHeight)`. The frame is clipped to the area, so a
page cannot cover the strip, the composer or the widgets. `chat.send` is
refused, because the composer is on the same screen. The area counts toward the
eight live frames of the app. A shared page asks first, like a shared page on
Home: the area shows **Allow this page** until you allow it.

LocalAI draws a thin strip above the page: its name, who made it (**Page by
the assistant**, **Page by you** or **Page shared by** an author), **Runs as
you**, **Reload** and **Edit**, which opens a chat that asks the assistant to
change the page. The page cannot change the strip.

### When the page does not load

Only the area is replaced: the composer and the widgets keep working. LocalAI
shows **This page did not load.** with the reason when:

| Reason | What LocalAI saw |
|---|---|
| The page is missing | the page was moved, deleted, or you cannot open it any more |
| The value is not a page | `pageArea` in `home.json` is not a page name |
| A script error | the script threw before the page's first beat |
| No first paint | no first beat from the page within 5 seconds |
| It was stopped | the budget of a tile (busy, no answer) or an attempt to leave its frame |

LocalAI decides from what it sees (the read of the page, the load of the frame,
the beat of the bridge and a timer), never from what the page says about itself.
The tile offers **Revert**, **Open the file** and **Remove from Home**.
**Revert** undoes the last change of the page's folder, or the last change of
`home.json` when the value is not a page name; it is not offered for a shared
page, which is not your file. **Remove from Home** writes `"pageArea": null`.
Both are change sets, so the assistant sees them and you can undo them. There
is no retry loop: **Reload** in the strip starts the page again.

An administrator can switch the page area off for everyone (the
`home_page_area_enabled` [runtime setting]({{% relref "/administration/runtime-settings" %}})).
The area then shows a note that says so, and the server leaves `pageArea` out
of the `home.json` it serves (`GET /api/chats/wellknown/home` names it in
`withheld`). The file keeps its value: a write of the whole file that does not
name `pageArea` keeps it too, so the page comes back when the area is on again.

## A start page

The first entry of `pinnedPages` in `LocalAI/ui.json` is your start page. It has
its own screen, the page view at `/app/pages/<name>`, and a **Start page** label
in the sidebar. The Home route does not change. The strip of the page view has
a switch, **Make this my first pinned page**: turning it on pins the page if
needed and moves it to the top of the pinned list, and turning it off moves it
behind the other pins, so the next pin becomes the start page. The change keeps
the other pins, and every other key of the file.

## The dock

The dock is a panel beside a chat that shows one or more pages in tabs, so you
can talk about a page while you see it. There is no dock until something opens
one. A page gets into the dock in four ways:

- **Dock** in the chat header, or Ctrl+\ (Cmd+\ on a Mac), shows and hides
  the dock. An empty dock, and the **+** of the tabs, list your pages and the
  shared pages to open. The **+** always opens that list for a new tab and
  puts the focus on its first page; a page of the list opens as a tab and
  closes the list, and Escape or the tab in front goes back to the page.
- **Open in dock** on the Pages list starts a new chat with the page beside it.
- A page button in a slot, and **Open** of a compact tile's card, open the page
  in the dock of the open chat.
- The assistant opens a page of your home with its `open_page` tool, for
  example after it wrote the page you asked for. It does not ask first: the
  page runs as you anyway when you open it. It refuses a page of a shared
  folder, which needs your consent in the dock first.

The strip under the tabs says where the page comes from (**Page by the
assistant**, **Page by you** or the shared folder) and that it **Runs as you**.
**Ask the assistant to change this** puts a request that names the page in the
message box; nothing is sent until you send it. The bar has **Reload**, a link
to the full page and a button that hides the dock. The footer says **Live:
updates as files change** and **Sandboxed, no network**.

The dock and the Files pane of the chat share the right side, one at a time.
**Files** and **Dock** are two buttons next to each other in the chat header:
opening one hides the other, and a file the assistant writes never takes the
side from an open dock (the count on **Files** still grows). In a window under
1280 px, while the dock is open, both buttons show only their icon.

Each chat keeps its own dock: the open pages, the page in front, whether the
dock shows and its width are stored with the chat on the server, so a reload,
another browser and another LocalAI frontend of the same cluster show the
same dock. A new chat keeps its dock until its first message creates the chat.

Drag the left edge of the dock to change its width: at least 320 px and at most
60% of the window. A double click resets it to 452 px. With the edge focused,
the arrow keys move it (Shift for bigger steps), Home and End go to the bounds
and Enter resets it. The thread, the chat header and the message box narrow
for the dock; it covers none of them. On a phone the dock is a bottom sheet
that stops under the chat header: drag its handle up for full screen and down
to close, or tap it to switch between the two heights.

A docked page runs in the same sandbox as everywhere, with the same bridge
and the same budget as a tile (see [Live frames and the budget of a
tile](#live-frames-and-the-budget-of-a-tile)). The page in front counts toward
the eight live frames of the app. A page you open takes a frame at once and
stops the frame that started first; a page that comes back with a reload waits
for a free one and shows **Run it here** until then. `localai.page.mode` is
`page`, as in the full view, with no `size`. `chat.send` from a docked page shows the message in the dock; it goes to
this chat, as your message, only when you press **Send**. The message lies
over the top of the page, so the page does not move, and **Send** works half
a second after it shows. A page that asks more than 3 times at once waits, as
in [Limits]({{% relref "/features/pages-and-home/pages#limits" %}}). A shared page runs
in the dock only after you allowed it on Home, in the slots and in the dock
(**Allow on Home, in slots and in the dock**), because the dock starts it again without a
click after a reload.
