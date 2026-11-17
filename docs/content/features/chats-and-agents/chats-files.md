+++
url = "/features/chats-files/"
disableToc = false
title = "The files of a chat"
description = "Which files a chat holds, where they live, how the work folder behaves and how to list and download them."
weight = 11
devbuild = "feature"
+++

See the files a chat holds: the ones you attached, the ones the model created and the ones it changed. This page says where they live, how the work folder behaves, and how to list and download them. Your own file store, with folders, Trash and sharing, is in [Files and shared folders]({{% relref "/features/files-and-documents/files" %}}).

## The files of a chat

Each chat lists the files that belong to it: the files you attached, the files
the model created, and the files it changed. The model gets the list in front
of your message whenever it changed since the model last read it, so it knows
which files exist and where they are now. The list is not in the system
prompt: it changes at most turns that write a file, and a changed system
prompt makes a backend with a prompt cache read the whole conversation again.

- **Where attachments live.** In the chat's own folder in your files:
  `/Chats/<date>-<title slug>/<name>`, for example
  `/Chats/2026-10-29-budget/q1.csv`. The name is the creation date and the
  title in lowercase ASCII letters and digits joined by dashes (accents are
  dropped; a character with no ASCII form, such as a middle dot, becomes a
  dash), so it never holds a space or a character that a shell would split or
  mangle. A title with no ASCII letter or digit gives `new-chat`. When two
  chats get the same name, the second gets `-2`, then `-3`. The model sees the
  same file as `/workspace/home/Chats/2026-10-29-budget/q1.csv`. A chat made
  before this form keeps its folder, such as `/Chats/2026-10-29 Budget/`, and
  that folder is not renamed when the title changes. The folder is made at the
  first attachment. In a chat whose agent works in [one
  folder]({{%relref "/features/chats-and-agents/chats-agents#folders-of-an-agent" %}}), the
  model sees the chat folder at `/workspace/chat` instead (the file above is
  `/workspace/chat/q1.csv`), and the folder is made before the first turn. The
  list on this page keeps the paths in your home.
- **What is listed.** Every file in the chat folder, and every file that the
  chat's turns created or changed elsewhere in your home folder: with `bash`,
  or with a file tool such as `write_file`. The list is read from the
  history of your files, so a file that you or the model moved or deleted
  later is not listed at its old path.
- **Rename.** Renaming a chat renames its folder to match the new title (the
  new slug), and the list gives the new paths at once. A folder in the old
  form (`<date> <title>`, with a space) is left where it is. Earlier messages keep the old paths.
  The model is told at its next turn that the folder was renamed and which
  path is current.
- **Delete.** Deleting a chat removes its transcript and its sandbox. Its
  folder (with its work folder) and every file the model wrote stay in your
  files.
- **Fork.** A fork has no folder of its own until you attach a file. The files
  that the copied messages attached stay in the original chat's folder; the
  fork lists them while they are there.

To follow the changes to your files as they happen (from any tab, chat,
trigger or frontend), open the [change feed]({{% relref "/features/files-and-documents/workspace-feed" %}})
(`GET /api/workspace/events`).

```bash
curl .../api/chats/3f1c.../files
# {"files":[{"name":"q1.csv","path":"/Chats/2026-10-29-budget/q1.csv",
#   "guest_path":"/workspace/home/Chats/2026-10-29-budget/q1.csv","size":2048,
#   "mime":"text/csv","origin":"user","turn":1,"at":"..."},
#  {"name":"report.md","path":"/report.md","guest_path":"/workspace/home/report.md",
#   "size":930,"mime":"text/markdown","origin":"model","turn":2,"at":"..."}],
#  "folder":"/workspace/home/Chats/2026-10-29-budget/","folder_path":"/Chats/2026-10-29-budget"}
```


With authentication on, add `-H "Authorization: Bearer $API_KEY"` to each request.

`origin` is `user` (you attached it, or put it in the chat folder), `model`
(a command of the model wrote it) or `tool` (a tool of the chat wrote it,
such as `write_file`). `changed` marks a file the chat changed but did not
create. `media` marks an attachment that the model received as an image or a
video inside the message; the others reach the model by path. `path` works
with the [Files API]({{% relref "/features/files-and-documents/files-reference" %}}). `stale` lists old folder paths that earlier
messages name. A `files` event on the chat's event stream says that the list
may have changed; read it again.

### Files that a command saved

A command of `bash` (the Sandbox's, or the built-in one on this machine) saves
what it writes in your home folder as one change of the chat, the same way
`write_file` does. The chat then shows those files where it shows a file
tool's:

- **Under the step.** The `bash` step lists the files the command created or
  changed ("Saved to your files"), one chip each; a chip opens the file in
  the preview pane. A file the command deleted is listed by name, struck out.
- **In the pane and under the answer.** The files are in the chat's file list
  above, with the turn that made them, so they get a chip under the answer
  and the preview pane opens by itself on the newest one (a Word, Excel,
  PowerPoint or PDF file, an image or a text file), as it does for a file of
  `write_file`.
- **In the transcript.** The tool line of the call has a `card` with `files`:
  `[{"path":"/report.docx","rev":42,"op":"created"}]`, where `path` is inside
  your home, `rev` is the change set (undo it with `POST
  /api/chats/files/undo`) and `op` is `created`, `changed` or `deleted`. At
  most 50 are listed; `files_more` counts the rest. The card keeps its own
  `kind` and `id` (who served the call: `tool_provider` for the Sandbox,
  `host_exec` for this machine).

Only what the command saves under `/workspace/home` (or a writable shared
folder) is kept. `bash` starts in the chat's [work
folder](#working-files-and-the-work-folder) and has its path in `$WORK`, so a
relative path such as `report.docx` is in that folder (a chat with no work
folder yet starts in `/workspace/home`, with no `$WORK`). A file written anywhere else, such as
`/workspace/report.docx` or `/tmp`, is not saved, and the result of the call
says so by name. When you or a file tool changed a file while the command ran,
your version stays and the command's version is saved as a copy; the result
names the copy's path.

The list of files comes from the change sets of the chat in the shared store,
read after each call, so every frontend of a cluster shows the same files.
Nothing about it is kept in memory.

The list that the model gets has the newest 50 files, one line each with the
path, size, type, origin and turn, and a count of the rest. A chat with no
files gets no list. The list is built from the stored history when a turn
starts, so every frontend of a cluster gives the same one.

### Working files and the work folder

A chat that makes a document often writes a script, some intermediate data or
a log on the way. These working files must not mix with your own files, so
every chat has a work folder: `/Chats/<date>-<title slug>/work/` in your files
(`/workspace/home/Chats/<date>-<title slug>/work/` for the model), next to the
chat's attachments. It is a normal, visible folder.

- **Where a command starts.** Every shell of the chat starts in the work
  folder, and `$WORK` holds its path: the `bash` of the Sandbox, the `bash` on
  this machine and the [background jobs]({{% relref "/features/chats-and-agents/chats-commands#background-jobs" %}}) of both. The
  folder is made before the shell starts. A chat with no work folder yet (or
  with the steering off) starts in `/workspace/home` and has no
  `$WORK`. The model can still write anywhere in your files: nothing
  restricts where a command writes. On this machine `$WORK` is a path of the
  scratch folder, not of `/workspace`.
- **What the model is told.** At every turn the note in front of your message
  names the chat's work folder (the file tools take that path), says that the
  shell starts there and that `$WORK` holds it, and gives the quoting rule:
  use `$WORK` or relative paths instead of typing absolute paths, quote every
  path with double quotes, and never type out a name with spaces or non-ASCII
  characters but take it from `ls` or a glob. The prompt says: scripts, helper code, intermediate data, logs, drafts and
  anything else you did not ask for go there; the file you asked for (the
  document, the spreadsheet, the image) goes where you said, or at the top of
  your home, and nowhere else; no new top-level folders for scripts; a command
  that runs once needs no file (`python3 -c`, a heredoc). The descriptions of
  `bash` and `write_file` and the Sandbox document skills say the same. The built-in
  `localai-apps` and `localai-workspace` skills also give this guidance. Pages
  (`pages/<name>/`) and the `LocalAI/` settings folder are not working files.
  The folder is named in the note and not in the prompt because its name
  carries the chat's title: the prompt stays the same for every chat, so a
  backend with a prompt cache (llama.cpp) reuses it, with the tool
  definitions after it, instead of reading it again for each new chat.
- **When the folder is made.** The chat takes the name of its folder at its
  first turn, but makes the folder only when it is used: before a `bash`
  command runs, or when a file is saved there. A chat that saves nothing
  leaves no folder behind.
- **A gentle nudge, no refusal.** When `write_file`, `move_file` or a command
  leaves a helper file (see below) outside the work folder, its result gets
  one line that suggests moving it there, at most once a turn. Nothing is
  refused or moved: you may have asked for that script.
- **Delete.** Deleting a chat keeps its folder, with the work folder and the
  attachments in it, like every other file the chat wrote.

Each file of the chat's list has a `kind`: `deliverable` or `working`. A file
is `working` when it is in the work folder of any chat
(`/Chats/<chat>/work/...`), or when it looks like a helper: the extension
`.py`, `.sh`, `.js`, `.ts`, `.mjs`, `.rb`, `.pl`, `.ps1`, `.bat`, `.log` or
`.tmp`, or a folder named `scripts`, `tmp` or `scratch` on its path. Files of
a page, of the `LocalAI/` folder or of a shared folder, and the files you
attached, are always `deliverable`. The server decides the kind in one place
and puts it on the file list (`kind`), on the files of a command's card
(`files[].kind`) and on the card of a file tool (`file_kind`).

```bash
curl .../api/chats/3f1c.../files
# {"files":[{"name":"summary.docx","path":"/summary.docx","kind":"deliverable",...},
#  {"name":"build_document.py","path":"/Chats/2026-10-31-summary/work/build_document.py",
#   "kind":"working",...}], ...}
```

In the chat:

- **The Files pane** opens from the **Files** button in the chat header, next
  to **Dock**, which shows how many files the chat holds. It shares the right
  side with the dock, so opening one hides the other. It lists what the chat
  made for you first. The working
  files are in a group of their own under it, **Working files · N**, folded
  and quieter; open it to see and preview them. **Download all files as ZIP**, at the top of the pane,
  saves every file of the list, working files included, in one ZIP with the
  folder layout kept. It is off when the list is empty.
- **The preview** that opens by itself after a turn shows the newest file
  that is not a working file. A working file never opens it by itself.
- **The chips under the answer** name only what the chat made for you. An
  image the answer does not show itself also appears above the chips as a
  thumbnail that opens the pane.
- **Images and links in an answer** that name a file of the chat, by its path in
  the sandbox, by its path in your files (a folder name with spaces is fine) or
  by its name, show the file in the message. A link to a file opens it in the
  pane. An image that does not load shows as a chip with its name, never as a
  broken image.
- **A step** that saved working files says so in one line ("Also saved 2
  working files"); click it to see their chips.

An administrator can turn the steering off with **Work folder for helper
files** (`chats_work_folder`, Settings > Tools). The prompt then names no
work folder and no nudge is added; the pane still shows working files apart.
The nudge is counted per process: a turn runs on one process, so nothing is
shared, and a restart only lets the line come once more.
