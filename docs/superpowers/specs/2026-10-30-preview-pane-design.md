# Chat preview pane (Canvas folded into the files pane)

Date: 2026-10-30. Branch: `feat/chat-preview-pane`.

## Problem

1. **Canvas** is a composer toggle that moves code blocks into a side panel.
   Nobody finds it, and what it does should happen on its own.
2. A file that the assistant creates (an `.xlsx`, a `.docx`, a PDF) shows only
   a Download in the chat's Files pane, and nothing opens when it appears. The
   person cannot find "what the assistant made" or "what I gave it".

## Decision

One right-hand pane. The chat's Files pane becomes the **preview pane**.
Canvas is removed: its composer chip, `/canvas`, its state, `CanvasPanel`,
the artifact cards in the transcript, the public `/api/canvas/preview` store
that only the canvas used, its CSS and its strings.

### Auto-open

`utils/previewPane.js` holds the pure decisions:

- `isPreviewableBlock({ lang, code, before })`: an HTML page (`<html`,
  `<!doctype` or `<body`), an SVG, Markdown of 25 lines or more, any block of
  25 lines or more, or a block that the line before it presents as a file
  (`index.html:`, `` save this as `app.py` ``). Short snippets never count.
- `autoOpenTarget({ files, messages, since })`: what a live turn produced
  after the watermark `since` (the turn and message seq seen when the chat
  opened). A created or changed file wins over a block; the newest wins.
- The per-chat closed memory: `localai_chat_preview_closed:<user>` in the
  browser storage, `{ [chatId]: at }`, at most 200 entries (oldest dropped).
  Closing the pane (button, scrim, Escape, the Files chip) writes it; opening
  it by hand (Files chip, a chip of the transcript, a code block's
  **Open preview**) removes it; deleting the chat removes it.

The page opens the pane on a target only when: the chat is not marked
closed, the window is wider than 639 px (a phone gets the chip only), and the
target is new since the watermark (a reload or an old chat never opens it).
Opening never moves focus; a `role="status"` region says "Preview opened:
budget.xlsx". A chip or button click is a request, so the pane takes focus
then.

### Chips in the transcript

`components/chat/FileChips.jsx` (`FileChip {path,name,size,kind,onOpen}` and
`FileChipRow`). Under the last answer of a turn: one chip per file that the
turn created or changed: "Created budget.xlsx · Open preview". Clicking opens
the pane on the file (the existing `focus={{path, n}}`). The row renders in
`Chat.jsx` next to the change note, so `ChatMessage.jsx` loses only its
canvas parts.

### Pane content

Master-detail in the 360-480 px pane. With nothing selected: the list in two
sections, **Created and changed** (assistant files, created then changed)
and **You gave it** (attachments), with the turn markers. With a file
selected: a picker (a native `select` with the two groups), the actions
**Download**, **Copy** (text and code) and **Open in Files**, then the
preview. **All files** goes back to the list. Drag and drop to attach stays.
A code block opened from the transcript is shown the same way (Copy,
Download), without a file behind it.

### Previews

`components/files/FilePreview.jsx` picks a view by extension. The add-on slot
`files.preview` keeps priority: when an installed add-on contributes to the
slot for the extension, the built-in view is not shown.

| Type | View |
|---|---|
| `.xlsx` `.xlsm` `.csv` `.tsv` | Table from the server: a tab per sheet, sticky header row, row numbers, 200 rows x 30 columns, "Showing 200 of N rows" |
| `.pdf` | The browser's viewer in an `iframe` of the inline content URL, with a fallback and Download |
| `.docx` `.pptx` | Text from the server: headings and paragraphs; slide titles, bullet text and a "Speaker notes" marker. Note "Text preview, formatting not shown" |
| `.html` `.htm` | A sandboxed `srcdoc` frame with the Pages sandbox (`allow-scripts`, no same origin) and the Pages CSP (no network). No bridge: a preview cannot read or write files or send messages |
| `.svg` images | `img` from the inline URL (an `img` never runs SVG script); click to zoom |
| `.md` | The chat's Markdown renderer |
| `.json` | Pretty-printed, with collapsible objects and arrays |
| code and text | The chat's highlighter |
| audio, video | Native players |
| other | A card with name, type, size and Download |

Every view: a skeleton while it loads, an error with Retry (never blank),
keyboard reachable, tokens only, light and dark.

### Server: `GET /api/chats/files/preview?path=`

Same checks as `GET /api/chats/files/content` (the files feature, the user's
home, the same path rules: a path that leaves the home is 400, another
user's file is 404). The parser lives in
`core/services/studio/files/preview` (standard library only: `archive/zip`,
`encoding/xml`, `encoding/csv`; `excelize` is not a dependency of the repo,
and the Office add-on's internals stay private to it).

- Cap: files over 20 MiB answer 413. Each zip part read is capped at 64 MiB
  uncompressed (a zip bomb stops there) and parsing has a 10 s deadline.
- `{kind:"table", sheets:[{name, rows, total_rows, total_cols}]}` for
  spreadsheets: shared strings and rich text, numbers (15 significant digits),
  dates from the cell style (1900 and 1904 systems), booleans, inline
  strings, formulas as their cached value.
- `{kind:"document", blocks:[{type:"heading"|"paragraph"|"item", level, text}]}`
  for `.docx`, `{kind:"slides", slides:[{n, title, lines:[{text, level}], notes}]}`
  for `.pptx`. Both cap the text at 200 000 characters and set `truncated`.
- 415 for any other type.
- Cache: an in-process LRU of 64 entries keyed by owner, path and the file's
  revision. A revision never changes its content, so frontends of a cluster
  can each keep their own copy: per-instance by design
  (`.agents/distributed-state.md`, mode 4), documented in `chats.md`.

PDF: the content endpoint sends `Content-Security-Policy: sandbox;
default-src 'none'`. Checked in Chrome: its PDF viewer renders such a
response inside an `iframe`, so the policy stays as it is. The pane's
`iframe` has no `sandbox` attribute of its own (that one would block the
viewer); the response's policy already sandboxes the document. A browser
that cannot show it gets the fallback text and Download.

## Not in scope

Undo in the pane; editing; pages.md.
