+++
url = "/features/files-reference/"
disableToc = false
title = "Files API and reference"
description = "Keep files and folders, search inside documents, unpack archives, set folder rules and share folders, with the routes."
weight = 11
devbuild = "feature"
+++

Use the Files page and the Files API to keep files and folders, search inside documents, unpack archives, give the assistant rules for a folder and share folders with other people. For the short version, see [Files and shared folders]({{% relref "/features/files-and-documents/files" %}}).

## Your files

Each user has a personal workspace: folders at any depth, uploads, a Trash and
a history with undo. Paths in the API are inside your own home folder, which is
`/`. You cannot name another user's files, and a path with `..` is refused.

```bash
curl -X POST ".../api/chats/files/folders" -d '{"path": "/Reports"}'  # make a folder
curl -X PUT  ".../api/chats/files/content?path=/Reports/q1.txt" --data-binary @q1.txt
curl         ".../api/chats/files?path=/Reports"                    # list a folder
curl         ".../api/chats/files/content?path=/Reports/q1.txt"     # download
curl -X POST ".../api/chats/files/move" -d '{"from": "/Reports/q1.txt", "to": "/Archive/q1.txt"}'
curl -X DELETE ".../api/chats/files?path=/Archive"                  # to the Trash
curl         ".../api/chats/files/trash"                            # what can be restored
curl -X POST ".../api/chats/files/undo" -d '{"rev": 12}'            # restore it
curl         ".../api/chats/files/history?path=/Reports&limit=50"   # the changes, newest first (at most 200)
curl -X DELETE ".../api/chats/files/trash/12"                       # delete that one for good
curl -X DELETE ".../api/chats/files/trash"                          # empty the Trash for good
curl         ".../api/chats/files/preview?path=/Reports/budget.xlsx" # what a workbook holds
```

- **Nothing is erased, until you say so.** A delete goes to the Trash, and undo restores it as a new change. A change you made after the deletion that conflicts with the restore makes it answer `409`; with `skip_conflicts` the rest is restored.
- **Emptying the Trash.** `DELETE /api/chats/files/trash` deletes everything in your Trash for good, and `DELETE /api/chats/files/trash/{rev}` deletes one entry, the `rev` that `GET /api/chats/files/trash` lists. Both answer `{"files": 2, "folders": 1, "bytes": 4096}`, what they removed. The list gives the same three numbers for each entry, and `totals` for the whole Trash, also what a long list leaves out. A permanent delete removes the files and folders, their older versions and their entries in the history, and frees the space the bytes held a couple of minutes later, unless another file shares the same bytes. It works the same on a server with a database, also with several frontends on one database, and on one with the JSON index. It cannot be undone: undo of that change answers `404`. It touches only your own Trash; a `rev` that is not in it is `404`. The file tools of a chat cannot do this, only you can.
- **Two uploads, one name.** An upload replaces a file only when it says which version it started from (`base`). Otherwise both are kept, and the new one is named `name (from you).ext`; the answer says so in `conflict_of`.
- **Downloads are safe.** Files are served as downloads. With `inline=1` an image, PDF, plain text, audio or video is shown by the browser; anything else, HTML and SVG included, never runs as a page.
- **Attachments.** Send a message as `multipart/form-data` with a `text` field and `files`. The files are saved in the chat's own folder under `Chats/` and the model gets their paths after your text. With a turn running the files are saved at once and the message waits like any other; the model reads them by path when the message reaches it. A sandbox that started before the files arrived sees them from the next turn.
- **Attachments in the transcript.** The transcript keeps your text and your files apart. In `GET /api/chats/{id}/messages` a user line has `text`, the words you typed, and `attachments`: `path` (inside your home, for the files API), `name`, `size`, `mime`, `version`, and `missing` when the file is no longer in your files. A file whose chat folder was renamed gets its new path. A waiting message has the same two fields. The list of paths and the text read from the files go to the model only: the chat shows an image as a thumbnail and any other file as a chip with its size and a download link, and a click or Enter opens the file in the chat's files pane. Messages sent before this change are shown the same way.
- **Previews of documents.** `GET /api/chats/files/preview?path=` reads what a `.xlsx`, `.xlsm`, `.csv`, `.tsv`, `.docx` or `.pptx` file holds, for the preview pane of a chat and the **Files** page. A spreadsheet gives `{"kind": "table", "sheets": [{"name", "rows", "total_rows", "total_cols"}]}`: every sheet, the first 200 rows and 30 columns of each as text, with dates, booleans and the stored result of formulas. A Word document gives `{"kind": "document", "blocks": [{"type": "heading" | "item" | "paragraph", "level", "text"}]}` and a presentation `{"kind": "slides", "slides": [{"n", "title", "lines", "notes"}]}`; both keep at most 200 000 characters and set `truncated` when they cut. The formatting is not kept. The path rules are those of the download. A file over 20 MiB answers `413`, another type `415`, and a file that is not what its name says `422`. Each frontend keeps the recent answers in memory, by the content of the file: a new version is a new entry, so frontends of a cluster never disagree, and nothing is shared between them.
- **Search.** `GET /api/chats/files/search?q=budget` finds files by name, lines inside text files, and lines of the text of PDF, Word, Excel and PowerPoint files, ignoring case. A hit inside a document has `document: true` and its `page`, `sheet` or `slide`; `indexing` counts the documents whose text is still being read. Text files are read on each search: one over 1 MiB, and any other binary file, is found by name only, and a very large workspace is scanned up to a limit. See [Search inside documents](#search-inside-documents).
- **The model can use your files.** A chat with files on gives the model one set of file tools: `list_files`, `read_file`, `write_file`, `edit_file`, `move_file`, `delete_file` and `search_files`. They are the same with or without the Sandbox. What they change is listed in the history as an agent change of that chat, and you can undo it from the note under the call; a delete goes to the Trash. A sub-agent gets the file tools of its parent chat (or the ones its parent names).
  - **Paths.** `/workspace/home/<path>`, `home/<path>` and `/<path>` all name `<path>` in your home folder. `/workspace/shared-<name>/...` is a shared folder, read-only when it is shared with you for reading, and `/workspace/shared-skills/...`, `/workspace/shared-builtin-skills/...` and `/workspace/shared-addon-skills/...` are the shared, built-in and add-on skills, read-only. Any other path under `/workspace` is refused with the right spelling, and nothing is written: an app the model builds goes to a Page, `/workspace/home/pages/<name>/index.html`.
  - **`read_file`** reads lines `offset` to `offset+limit-1` (at most 2000 lines, text files up to 4 MiB). On a PDF, Word, Excel or PowerPoint file it reads the text of the document, with a `## Page N`, `## Sheet` or `## Slide N` line where each one starts.
  - **`edit_file`** replaces `old_string` with `new_string`. `old_string` must match exactly once, unless `replace_all` is true; no match and several matches are errors that say what to do. Text files up to 8 MiB. When the file changed while it was edited, the edit is saved as a copy next to it.
  - **`list_files`** lists one folder, or with `pattern` every path below the folder that matches it: `*` matches any characters including `/`, `?` one character, `[...]` a class. At most 500 paths.
  - **`search_files`** finds plain text in names, inside text files and inside the text of PDF, Word, Excel and PowerPoint files (at most 30 hits). With `regex: true`, `query` is a regular expression (the extended syntax of `grep -E`) matched against each line (at most 200 lines). Case does not matter unless `ignore_case` is false. `path` may be a file or a folder. A hit in a document reads `path (page 3, line 40): text`, with `sheet "Name"` or `slide N` for a workbook or a deck; the line is the `offset` to give `read_file`. When documents below the path are still being indexed, the answer says so with the full count (`2 files are still being indexed`), so the model searches again instead of concluding that nothing matches. A search looks at 20 000 files and folders and reads 64 MiB of text at most; when it stops at one of these limits before the end, the answer ends with `[results limited: ...]`, the REST search sets `limited`, and the **Files** page says "Results limited", so a missing match is not taken as proof.
- **Pictures and recordings.** `describe_image` lets the model look at an image in your files and `transcribe_audio` turns a recording into text. A voice note attached to a message is transcribed on the way in and its transcript goes with your message; the file is kept as it is. They use helper models, which you choose in the settings (Chat image model, Chat transcription model). Left empty, `describe_image` uses the chat's own model when it can see images, and otherwise the first installed model that can see images (or, for audio, transcribe). If there is none, a cloud-proxy chat model that does not declare vision is tried as a last resort. With none installed the tool says what to install. The helpers run as you, so your model access and quota apply.
- **A file is at most 256 MiB.** Uploads of the Files page do not use the general upload limit (15 MB by default). They are limited by this size, unless you set `--upload-limit` (`LOCALAI_UPLOAD_LIMIT`) yourself: then your value applies to them too, and an upload is at most the smaller of the two. A folder listing reports the limit in force in `max_file_size` (bytes), and each entry has `modified`, when it last changed. A message with attachments is limited to 1 GiB in all, and each attached file to 256 MiB. A message with attachments does not use the general upload limit either, unless you set `--upload-limit`: then a message is at most the smaller of your value and 1 GiB. A message without attachments (JSON) always uses the general upload limit.

### Search inside documents

When a PDF, Word (`.docx`), Excel (`.xlsx`, `.xlsm`) or PowerPoint (`.pptx`) file is uploaded, written by a chat, synced from a Sandbox or restored, the server reads its text in the background, usually within a few seconds. The file search of the **Files** page and the chats' `search_files` then find the document by what it says, even when its name is a random string.

- **What is read.** The text layer of a PDF, page by page; the cells of each sheet; the headings, paragraphs and tables of a Word document; the slides and their notes. A scanned PDF has no text layer, so it is skipped (no OCR in this version). At most 50 MiB of file, 200 pages and `chats_index_max_text_bytes` of text (2 MiB by default) are kept for one file; a file over a limit is cut or skipped. A file whose content does not match its type (a `.pdf` that is not a PDF) is skipped.
- **Where the text is kept.** In the server's own store, next to the index of the files: the auth database (SQLite or PostgreSQL) when there is one, otherwise a `textindex` folder under the chats' data folder. The text never becomes a file in anybody's folders, and it is not sent anywhere.
- **Who finds it.** The text follows the permissions of its file: each hit is checked against the file at search time, so only users who can read the file find it, in their home or in a shared folder. A file that moved or changed since it was read is not shown until it is read again. Deleting a file removes its text; emptying the Trash removes what is left. The text of what a purge erased is dropped right after it, and when that cannot finish (the server stops, the database fails), the indexer finds the entries whose file is gone on its own: while **Search inside documents** is on, every 30 seconds it checks 200 entries, round all of them.
- **Search engine.** On SQLite the index uses FTS5 with the trigram tokenizer, on PostgreSQL a `pg_trgm` index when the extension can be created; otherwise, and with the JSON store, the server scans the stored texts. The answers are the same: case-insensitive text, or a regular expression matched line by line.
- **Settings.** **Search inside documents** (`chats_index_files`, on by default) and **Text kept per document** (`chats_index_max_text_bytes`) in Settings > Tools. With the switch off nothing new is read; text read before is still used for files that did not change.
- **Reading a long document.** The index keeps the first `chats_index_max_text_bytes` of a document. When `read_file` needs more, it reads the whole file once and keeps its whole text (up to 16 MiB of characters) in the same store, by the content of the file, so the next pages come from there and not from the file again. A new version of the file is new content and is read again. The kept text goes with the file's index entry: deleting the file or emptying the Trash removes it too.
- **Existing files.** At its first start the indexer reads the files that are already there, in steps, and goes on where it stopped after a restart.
- **Distributed mode.** Single-runner: one frontend at a time reads files, under an advisory lock, and the text is in the shared database, so every frontend searches the same text. An upload on any frontend is queued in the database at once, so its search says the file is being indexed. A chat worker searches the text but never reads files for the index. The whole texts that `read_file` keeps are in the shared database too, so a long document is read once for every frontend. Where the check for entries of erased files stands is in the memory of the frontend that runs the pass: another frontend that wins the lock checks from its own place, which only repeats work.

### The Files page

The **Files** page in the sidebar is a file browser for all of this.

- **Browse.** A breadcrumb shows where you are; a deep path folds its middle into a menu. Folders come first. Click a column (Name, Modified, Type, Size) to sort by it, and again to reverse it. **List** shows a table; **Thumbnails** shows tiles with a picture of each image. The page remembers the view and the sort in your browser. Dates read as "5 minutes ago" or "yesterday"; hold the pointer on one for the full date. Back and Forward of the browser go between folders.
- **Filter and search.** Typing in the box filters the open folder by name. Press Enter, or choose **Search all files**, to search the names and the text of every file.
- **Upload.** Use **Upload** (its arrow also uploads a whole folder), drop files or folders anywhere on the page, or paste with Ctrl+V; a pasted screenshot gets the date and time as its name. A drop on a folder row, or on a folder of the breadcrumb, uploads into that folder. The upload panel at the bottom right shows each file with its progress, speed and time left, and the total for all of them. You can cancel a file, try a failed one again (the panel says why it failed), and keep working: an upload keeps going when you open another folder or another page of the app, and the browser asks before you close the tab while files are still on their way. A file over the server's limit is refused before it is sent. When an upload has the name of a file that is in the folder already, the page asks: **Replace** it, **Keep both** (the new one gets a new name) or **Skip** it.
- **Select.** Click a row to select it, Ctrl-click (Cmd on a Mac) to add one, Shift-click for a range, or use the boxes. The bar above the list then offers **Download**, **Move to…** and **Move to Trash** for the selection.
- **Act.** Each row has a menu (⋮, or a right click): **Preview** or **Open**, **Download**, **Rename**, **Extract here** (on an archive; see [Extract an archive](#extract-an-archive)), **Move to…** and **Move to Trash**. A folder of your own also has **Share…** when the server has accounts (see [Share a folder of your home](#share-a-folder-of-your-home)). Drag rows onto a folder to move them there. **New folder** and **New file** add one in place, with a name you can type over. A move, a rename and a delete show a message with **Undo**.
- **Keyboard.** Arrows move between rows (and tiles), Shift+arrows extend the selection, Space selects, Enter opens, F2 renames, Delete moves to the Trash, Ctrl+A selects all, Escape clears the selection, Backspace opens the parent folder, and typing the start of a name jumps to it.
- **Preview.** Opening a file shows it in a side pane, the same views as the preview pane of a chat, with **Download**, **Rename**, **Move to…** and **Move to Trash**. On a phone it is a sheet from the bottom. Escape closes it.
- **Trash and History.** The **Trash** tab restores what you deleted, deletes one item for good, or empties the Trash; each of those asks first and says how many files and how much space it removes, and **Empty Trash** is off while the Trash is empty. The **History** tab undoes a change of the open folder.

LocalAI keeps two folders of your home for itself: `LocalAI/`, the settings it
reads (persona, profile, shortcuts, Home, theme; see [Make LocalAI
yours]({{% relref "/features/chats-and-agents/make-localai-yours" %}}#the-localai-folder)), and `Chats/`, one
folder per chat. **Files** lists your own files first and these two last,
under **Managed by LocalAI**; the list API marks them with `managed`
(`localai` or `chats`). Moving one to the Trash or renaming it asks first and
says what stops working.

### Extract an archive

Choose **Extract here** in the menu of an archive on the Files page to unpack it. For a large archive this is the best way: the server does the work and no chat is needed. The same call is `POST /api/chats/files/extract {"path": "/report.zip"}`. Add `"dest": "/some/folder"` to choose the folder.

- **Formats.** ZIP, TAR, and TAR with gzip, bzip2, xz, zstd, brotli, lz4 or snappy (`.tar.gz`, `.tgz`, `.tar.bz2`, `.tar.xz`, `.tar.zst` and so on). 7z and RAR are off by default; see [7z and RAR](#7z-and-rar-risk). A single compressed file, such as `notes.txt.gz`, is not an archive and is refused.
- **Where the files go.** By default into a new folder next to the archive, named like it without the extension: `report.zip` and `report.tar.gz` both give `report/`. When that name is taken, the folder is `report 2`, `report 3` and so on. With `dest`, the files go into that folder, which is created when it is missing. A folder that LocalAI manages (`LocalAI`, `Chats`, `Memory`, `pages`, `.skills`) is refused.
- **One change.** All files are written as one change, so one **Undo** removes the whole extraction. The toast that shows the result has the Undo button. The change is also in the History tab.
- **A name that is taken.** An existing file is never replaced. The new file is saved next to it as a copy named `name (from <user>).ext`, and the answer lists it under `conflicts`. A file with the same content as the existing one is not written again.
- **What is skipped.** Symbolic links, hard links, devices, sockets and pipes are not extracted. A name that is not valid UTF-8, a name that the Files page does not allow, a path deeper than 64 levels, a repeated name, an encrypted ZIP entry and a file over 256 MiB are skipped too. The answer lists them under `skipped` with a reason. A nested archive is written as a plain file; it is not unpacked.
- **Paths that leave the folder.** An entry with an absolute path, a `..` part or a NUL byte makes the whole archive refused with a 400 error, and nothing is written. A backslash in a name counts as a folder separator.
- **Limits.** The limits are counted on the bytes that are really read, not on the sizes that the archive says. They are: most bytes in all files (1 GiB by default), most entries (20,000 by default), 256 MiB for one file, a ratio of 1,000 between the expanded size and the size read (checked after 64 MiB), and 10 minutes. An archive over a limit writes nothing and the answer says which limit it was (413, or 408 for the time). An administrator sets the first two in **Settings > Tools** (`chats_extract_max_bytes` and `chats_extract_max_entries`, see [Runtime settings]({{% relref "/administration/runtime-settings" %}})).
- **Names with other encodings.** A ZIP made by an old tool may store names in a local code page. Those names are not valid UTF-8, so they are skipped and listed. Extract such an archive in a chat sandbox with a tool that knows the code page.

The model has no tool for this on purpose: a tool would cost tokens in every chat. A chat can unpack a small archive in its sandbox. The `localai` skill tells the model to point you to the Files page for a large one.

#### 7z and RAR (risk) {#7z-and-rar-risk}

The readers for 7z and RAR run inside the LocalAI server. They take sizes from the file and allocate memory for them before they read any data, and they have no memory limit. A crafted 7z or RAR file can make the server run out of memory and stop. For this reason they are off by default. An administrator can turn them on with **Extract: allow 7z and RAR** (`chats_extract_7z_rar`). Do this only when you trust every user who can upload files. The byte, entry, time and ratio limits still apply, but they cannot stop an allocation that happens before the first byte is read. ZIP and TAR are not affected: the ZIP entry count is read from the end of the file and checked before the reader loads the directory, and the zstd and xz readers have a fixed memory cap.

### Your own rules: AGENTS.md

Put an `AGENTS.md` file in a folder to give the assistant rules for that
folder: how to name files, which language to write in, what not to touch.
LocalAI never sends the text of the file to the model. It sends one line that
points at it, and the model reads the file with its file tools:

```
Rules for this folder are in /workspace/home/Reports/AGENTS.md. Read that file before you start.
```

- At the start of each turn, the line names the `AGENTS.md` of the chat's own
  folder (`Chats/<chat>/`), of the folders above it, and of your home, nearest
  first, two at most. It is part of the note of the turn, not of the system
  prompt, so it costs about 25 tokens a file and only when such a file exists.
- The first time in a turn that a file tool (`list_files`, `read_file`,
  `write_file`, `move_file`) works in a folder whose `AGENTS.md`, or the one
  of a folder above it, was not named yet, the same line is added to the
  result of that call. Each file is named once a turn. Reading the
  `AGENTS.md` itself counts.
- A [trigger run]({{% relref "/features/chats-and-agents/chats-agents" %}}) already gets the rules of its
  folder and the folders above it in its first message, so it gets no line for
  them.
- The checks run as you, on the shared workspace, and keep nothing between
  turns, so they work the same with several frontends.

A folder `pages/<name>/` with an `index.html` is a page: the UI shows it in a
sandboxed frame under **Pages**. See [Pages]({{%relref "/features/pages-and-home/pages" %}}).

### Shared folders

A shared folder is a folder that several users use. An administrator creates
it and chooses its members: a user, or **Everyone** (every user, also the ones
who sign up later), each with **Can read** or **Can edit**. A member's chats see
the folder at `/workspace/shared-<name>` (read-only for a reader), and its pages
show on the Pages screen under **Shared with you** (see [Shared
pages]({{%relref "/features/pages-and-home/pages#shared-pages" %}})). A user who is not a member
sees nothing of the folder: every call about it answers `404`, as for a folder
that does not exist.

In the UI, the **Shared** tab of **Files** lists the folders. An administrator
creates, renames and removes them there, and opens **Members** to add a person
or Everyone, change their access or take it away. Any other user sees the
folders shared with them and their access.

- **Where it lives.** A shared folder is `/shared/<name>` in the workspace,
  outside every home folder. A name has 1 to 64 letters, digits, spaces, dots,
  dashes or underscores, starts with a letter or a digit, and is unique without
  regard to case. A server keeps at most 100 shared folders, with at most 200
  members each. A new folder has an empty `pages` folder, and the administrator
  who creates it is a member who can edit it, so they can fill it. Remove them
  when they do not need it.
- **Access is a grant.** A member is a workspace grant on the folder and
  everything in it, checked on every call. A change applies at once to the
  Files and Pages screens and to the shared pages' bridge; an open chat sees it
  the next time it opens.
- **Rename** keeps the members, the pages and the consents to them.
- **Remove** moves the folder and its files to the Trash as one change, and
  answers with `undo_rev`. **Undo** (or `POST .../restore` with that `rev`)
  brings it back with its members. Nothing is erased. Removing a folder also
  drops every user's consent to its pages, so a restored page asks again before
  it runs.
- **Audit.** Every change (create, rename, delete, restore, grant, revoke) is
  written with who made it and when: in the table
  `studio_shared_folder_events` of the auth database, or in
  `chats/shared-folders/audit.json` under the data path, and in the log. A
  change that the LocalAI Assistant made is written as `@assistant`.

| Route | Who | Use |
|---|---|---|
| `GET /api/chats/shared-folders` | any user | The folders shared with you: `id`, `name`, `path`, `pages`, `mode` (`read` or `write`). A folder a person shared from their home has `owner` (their name), `everyone` when you have it only through **Everyone**, and an empty `path`. |
| `GET /api/chats/shared-folders/{id}` | any user | One of them; `404` when it is not shared with you. |
| `GET /api/chats/admin/shared-folders` | administrator | Every shared folder with its `members` (`principal`, `name`, `everyone`, `mode`). |
| `POST /api/chats/admin/shared-folders` | administrator | Create one, body `{name}`. `409` when the name is taken or the limit is reached. |
| `GET`, `PATCH`, `DELETE /api/chats/admin/shared-folders/{id}` | administrator | Read one, rename it (body `{name}`), remove it (answers `{folder, undo_rev}`). |
| `POST /api/chats/admin/shared-folders/restore` | administrator | Undo a removal, body `{rev}`. `409` when a folder with its name exists now. |
| `PUT /api/chats/admin/shared-folders/{id}/members/{member}` | administrator | Add a member or change its access, body `{mode}` (`read` or `write`). `member` is a user's ID, email or name, or `@everyone`. |
| `DELETE /api/chats/admin/shared-folders/{id}/members/{member}` | administrator | Take a member's access away. |
| `GET /api/chats/admin/shared-folders/events?limit=` | administrator | The audit, newest first. |

```bash
# Create a folder, share it with everyone (read) and with one person (edit).
curl -X POST ".../api/chats/admin/shared-folders" -d '{"name": "Team"}'
# {"id":42,"name":"Team","path":"/shared/Team","pages":0,
#  "members":[{"principal":"<your id>","name":"Ada","mode":"write"}]}
curl -X PUT ".../api/chats/admin/shared-folders/42/members/%40everyone" -d '{"mode": "read"}'
curl -X PUT ".../api/chats/admin/shared-folders/42/members/marta@example.com" -d '{"mode": "write"}'
# Remove it, then bring it back.
curl -X DELETE ".../api/chats/admin/shared-folders/42"          # {"folder":{...},"undo_rev":118}
curl -X POST ".../api/chats/admin/shared-folders/restore" -d '{"rev": 118}'
```

The LocalAI Assistant has the same operations as tools (see [LocalAI
Assistant]({{%relref "/features/chats-and-agents/localai-assistant" %}})). In a cluster the
folders and the grants live in the shared workspace database and the audit in
the auth database, so every frontend sees the same state.

### Share a folder of your home

You can share a folder of your own home with other people, without an
administrator. Open **Files**, choose **Share…** in the menu of a folder, and
add a person or **Everyone**, with **Can read** or **Can edit**. This is the
same Share dialog as for agents and the other things you can share (see
[Share with your team]({{%relref "/features/sharing" %}})).

- **What you can share.** A folder inside your home: not the home itself, and
  not a folder that LocalAI keeps or reads as your own words: `LocalAI/`,
  `Chats/`, `Memory/`, `pages/`, a `.skills` folder anywhere, the `skills` folder of a project (`Projects/<project>/skills`), or a folder
  inside one of them. You cannot share a folder that someone shared with you or
  an administrator's shared folder. A person shares at most 100 folders, each
  with at most 200 grants.
- **What your chats use.** Your chats load your skills, your notes, your agent
  profiles and your persona, and **Pages** runs your pages, with your own
  rights. LocalAI leaves out anything in a folder on which another person, or
  **Everyone**, can edit: a `.skills` folder inside a folder you share with
  **Can edit** gives your chats no skill, a project you share with **Can edit** loses its own skills while the share lasts, and a page or a note there is not
  yours. What you share with **Can read** stays in use. The others still see and
  change those files as files.
- **Who.** An active account that the search finds, or **Everyone** (every
  account, also the ones created later). The search is the user picker of the
  other kinds of sharing (see
  [Share with your team]({{%relref "/features/sharing#api" %}})): it starts at three
  characters of the display name, a part of an address finds nobody, and an
  account without a name is found by its whole address only. It shows a name and
  nothing else.
- **What they see.** The folder is in their **Shared** tab with your name and
  their access, and their chats see it at `/workspace/shared-<name>` like an
  administrator's shared folder (`shared-<name> 2` when two have the same name).
  Its path in your home is not shown to them. Its `pages/` are not shared pages,
  and the `pages` folder of your home cannot be shared this way at all, nor a
  folder inside it. A page runs in the browser of the person who opens it, so a
  page is shared on its own, from the **Pages** screen (see [Share a
  page]({{%relref "/features/pages-and-home/pages#share-a-page" %}})). The other way
  to share a page is an administrator's shared folder.
- **Taking it back.** A lower access or a removed grant holds from their next
  file call, on every frontend. Their chats that run with the folder on the
  frontend that serves your change are stopped before the change answers; the
  other frontends get the request on the chat bus and stop theirs within
  seconds. A grant follows the folder: when you rename or move it inside your
  home, it stays shared; when you move it to the Trash, nobody else can open
  it, and restoring it from the Trash shares it again.
- **Moves that keep a share where it does not belong.** A folder that is
  shared, or that has a shared folder inside it, cannot move into another
  person's home (also not by someone who can edit there), out of a home, or
  into `LocalAI/`, `Chats/`, `Memory/`, `pages/` or a `.skills` folder. The
  move is refused with `409` and type `folder_shared`, and nothing moves: stop
  sharing it first. A folder of your home that still has a grant from before,
  wherever it is, is in your list of shared folders and you can remove its
  grants.
- **Only from your browser.** Changing who has a folder, listing it and the
  search accept only your own signed-in browser tab. An API key, an agent, a
  tool and an MCP client cannot share anything. In local-owner mode (no
  accounts) folders cannot be shared.
- **Audit.** Each grant and revoke is a line of the shared folders audit, with
  you as the one who made it.

| Route | Who | Use |
|---|---|---|
| `GET /api/chats/files/shares` | you | The folders of your home that are shared: `id`, `name`, `path` (in your home), `sharedWith`, `everyone`; `sharing` is `false` when folders cannot be shared here. |
| `POST /api/chats/files/shares` | you | Body `{path}`: the folder's `id` for the routes below. `403` for a folder you cannot share, and for one that does not exist. |
| `GET /api/chats/files/shares/{id}/grants` | you, browser only | `{grants: [{userId, displayName, role}]}`; `role` is `read` or `write`. |
| `PUT /api/chats/files/shares/{id}/grants/{principal}` | you, browser only | Body `{role}`. `principal` is an account ID from the search, or `@everyone`. |
| `DELETE /api/chats/files/shares/{id}/grants/{principal}` | you, browser only | Remove a grant. |
| `GET /api/chats/files/shares/{id}/users?q=` | you, browser only | Search accounts by a display-name prefix of 3 to 64 characters, or an account without a name by its whole address, at most 20, never you. `429` with type `rate_limited` after 20 searches at once, then one every 2 seconds. |

"Browser only" means a same-origin `fetch` with the header `X-LocalAI-Confirm`
equal to the folder ID, no API key and no tool, agent or MCP marker; anything
else gets `403`. A folder you cannot share, one that does not exist and one of
someone else get the same `403`, so a call cannot tell them apart.

The grants are the same workspace grants as an administrator's, in the
workspace database, and nothing else keeps a copy: every frontend reads them on
every call, so a change made through one frontend holds at once on all of
them. A frontend stops its own running chats, and sends the request to stop on
the chat bus to the other frontends and chat workers, which stop theirs. The
search answers on the frontend that serves the call and keeps the count of
searches of each person in the memory of that frontend (per instance): with
several frontends a person may search at that rate on each of them, and a
restart resets the count. The count only slows down a person who tries many
names; it decides no access.
