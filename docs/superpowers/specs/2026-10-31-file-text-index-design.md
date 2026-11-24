# File text index (phase 1)

Tracking issue: https://github.com/mudler/LocalAI/issues/12990

## Problem

`search_files` and `GET /api/chats/files/search` read only UTF-8 text files
up to 1 MiB, and walk the tree on every call. A PDF, Word, Excel or
PowerPoint file is found by name only. A user who uploads a PDF with a
generated name cannot find it by a word inside it, even when the PDF has a
real text layer.

## Decisions

- Extract the text when a file is written (an upload, a chat tool, a sync,
  an undo), not when it is searched.
- Phase 1 types: PDF (text layer only), `.docx`, `.xlsx`/`.xlsm`, `.pptx`.
  No OCR, no audio, no model downloads.
- The text lives in LocalAI's own store, never as a file in the user's
  folders. One row per workspace node, keyed by node ID, with the content
  hash it was made from.
- In a cluster one frontend extracts (advisory lock), and every frontend
  searches the shared rows.

## Extraction

`core/services/studio/extract` stays the one entry: `Extract(ctx, name,
data)` for PDF and Office. PDF goes through the extractor it already uses
(PDFium in WebAssembly, no cgo). The add-ons' `pdfread` package is internal
to the add-ons and is not used. Changes:

- `Structure.Sections` lists where each page, sheet or slide starts in the
  text (a 1-based line), so a hit can say "page 3" or "sheet Budget".
- A PDF page starts with a `## Page N` line, as sheets and slides already
  start with `## Sheet` and `## Slide`. `read_document` gets the same text.
- `Limits.Pages` (default 200) stops a PDF after that many pages, and
  `Limits.Text` now also cuts a PDF. Both set `Truncated` and add a note.

## Store

Package `core/services/studio/textindex`. `Store` has two implementations
with one contract suite (`textindextest`):

- GORM (SQLite or PostgreSQL, the auth database): table
  `studio_file_texts`, one row per node: `node_id`, `path`, `owner`, `hash`,
  `extractor` (version), `status` (`pending`, `ready`, `failed`,
  `skipped`), `reason`, `kind`, `sections` (JSON), `text`, `folded` (the
  text in lower case, for matching), `truncated`, `bytes`, `updated`. A
  second table keeps the journal cursor.
  - SQLite: an FTS5 table with the trigram tokenizer over `folded`, kept in
    step by triggers, answers queries of three or more characters. If the
    driver has no FTS5, or the query is shorter, `instr(folded, ?)` is used.
  - PostgreSQL: a `pg_trgm` GIN index on `folded` when the extension can be
    created; the query is `folded LIKE '%q%'` either way.
- JSON (one process, no database): the metadata in one file, each text in
  its own file. Search reads the texts under the path one by one and
  matches them in memory. That is a scan, not an index, and it is
  acceptable for the single-process store.

The store answers candidates. The final match is always the Go matcher of
`search_files` run over the stored text line by line, so the semantics stay
the same: plain text is a case-insensitive substring, `regex: true` is RE2
per line. Regex search pages through the ready rows under the path.

## Access

A row is not a permission. At query time each candidate is checked with a
`Stat` of its path as the caller: the node must exist, be readable by the
caller, and have the same node ID and hash as the row. A file that the
caller cannot read, that moved, or that changed since it was indexed is not
shown. The text is never stored in a place that users read directly.

## Indexer

- The workspace journal is the queue. A pass reads `ChangesSince(admin,
  cursor)` in batches and applies each operation: a create or write of a
  supported file marks the row `pending` with the new hash (unchanged hash:
  only the path changes); a delete removes the row; a move changes the
  path, or the paths below a moved folder. The cursor is saved after each
  batch, so the pass resumes after a restart.
- The first pass starts at revision 0: replaying the journal is the
  backfill of existing files. It is rate limited by the batch budget of one
  pass and by the extraction concurrency.
- An upload (REST) and a write by a chat tool also mark the row `pending`
  at once and wake the indexer, so a search right after an upload says the
  file is being indexed instead of "nothing found".
- Extraction: at most 2 files at a time, 60 s each, a panic becomes
  `failed`, at most 50 MiB of input, text capped by
  `chats_index_max_text_bytes` (2 MiB by default) and 200 pages. A PDF with
  no text layer is `skipped` with reason `no text layer`. A hash that is
  ready on another row is copied, not extracted again.
- A purge (empty Trash, delete one entry for good) removes the rows of the
  nodes it erased, through a hook of the files service.
- Settings: `chats_index_files` (default on) and
  `chats_index_max_text_bytes`. Off: the indexer does no work and the
  search says nothing about pending files; rows that still match their file
  are used.

Distributed mode: single-runner. The pass runs under
`advisorylock.TryWithLockCtx` with a new key; the rows and the cursor are in
the shared database, so any frontend's search sees them. Without a database
(JSON store) the process is alone and runs the pass itself.

## Tools and REST

- `search_files`: name hits, then text-file lines (as before), then hits in
  indexed documents as `path (page 3, line 40): snippet`. When files under
  the path are still pending the answer ends with "2 files are still being
  indexed; search again in a moment." The description says it searches
  inside PDF, Word, Excel and PowerPoint files.
- `read_file` on a PDF or Office file returns the extracted text with the
  usual `offset`/`limit` (2000 lines). It uses the index when it matches the
  file, and extracts on the spot otherwise.
- `GET /api/chats/files/search`: hits gain `page`, `sheet` and `slide`, the
  result gains `indexing`. The Files page shows "page 3" instead of the line
  and a line that files are being indexed. No new route, so no new MCP tool:
  the route is the user's own files (see the existing skip in
  `api_instructions.go`).

## Tests

Store contract on JSON, SQLite and PostgreSQL (when
`LOCALAI_TEST_POSTGRES_URL` is set); extraction of synthetic fixtures built
in the test code; the indexer lifecycle (upload, overwrite, rename, folder
move, delete, purge, shared folder seen by two users, backfill, caps, panic,
two frontends on one database); the tools; a route test that uploads a
UUID-named PDF and finds it by a word inside with `search_files`.

## Not in phase 1

OCR, audio transcripts, model installs from a wizard, per-file status in the
Files page: see the issue.
