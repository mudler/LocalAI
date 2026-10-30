# One file toolset, one bash: built-in file tools and host commands

Status: approved design, 2026-10-30.

## Problem

A chat with the Sandbox add-on had two file toolsets: the built-in workspace
tools (`read_file`, `write_file`, `list_files`, `search_files`, `move_file`,
`delete_file`) and the add-on's `read`, `write`, `edit`, `glob` and `grep`.
They act on different copies, the model picks either, and only the built-in
ones show an Undo card. A chat without the Sandbox had no `edit`, no glob and
no regular-expression search at all. A chat without the Sandbox could also not
run anything, so many tasks looked broken.

## Decisions

1. The built-in workspace tools are the one file toolset of every chat.
2. The Sandbox add-on exposes execution only: `bash`, plus `status`,
   `set-backend` and `confirm-host` as before.
3. When no Sandbox can run commands, a chat can still get a built-in `bash`
   that runs commands directly on this machine, without isolation ("host
   commands"), behind an administrator setting and a consent card.
4. When the Sandbox add-on is installed, on for the chat and able to run, it
   always wins: the chat never gets two tools that run commands.

## Built-in file tools

| Old Sandbox tool | Built-in tool | Behaviour |
|---|---|---|
| `read` (offset, limit) | `read_file` | Unchanged: lines `offset`..`offset+limit-1`, at most 2000 lines, text up to 4 MiB. |
| `write` | `write_file` | Unchanged. Undo card. |
| `edit` | `edit_file` (new) | `old_string`, `new_string`, `replace_all`. `old_string` must match exactly once unless `replace_all` is true; no match and several matches are errors that say what to do. Text files up to 8 MiB. One change set, Undo card. |
| `glob` | `list_files` with `pattern` | Without `pattern`: one folder, as before. With `pattern`: every path below `path` that matches; `*` matches any characters including `/`, `?` one character, `[...]` a class. At most 500 paths, sorted. |
| `grep` | `search_files` with `regex`, `ignore_case` | Without `regex`: as before (names and text, plain text). With `regex: true`: `query` is a regular expression (RE2, which covers the extended syntax that `grep -E` takes) matched against each line of text files; at most 200 lines. `ignore_case` defaults to true. `path` may be a file or a folder. |
| `bash` | stays in the Sandbox | |

Why extend instead of adding `glob_files` and `grep_files`: both options only
add optional parameters with a default that keeps today's behaviour, and the
model already knows `list_files` and `search_files`. Two more names that
overlap by purpose would bring back the problem this design removes.

### Paths

- `/` and `/workspace/home/...` are the user's home, as before.
- `/workspace/shared-skills/...` and `/workspace/shared-builtin-skills/...` are
  the shared and built-in skills, read-only, as before.
- New: `/workspace/shared-<name>/...` is a shared folder of the user, with the
  same name the chat's view uses (`session.BuildView`). The mode is the one the
  user has: a folder shared with "Can read" refuses `write_file`, `edit_file`,
  `move_file` and `delete_file`. The workspace store checks the grant again.
- `..`, control characters and paths outside these are refused.

### Sub-agents

A sub-agent now gets the built-in file tools of its parent (all of them, or
the ones its parent names in `tools`). Before this change a sub-agent reached
files only through the Sandbox tools. Changes stay agent changes of the parent
chat, so the parent's Files pane refreshes and Undo works there.

## Sandbox add-on

- The manifest, `internal/tools.Tools` and `codetools.NewTools` list only
  `bash` among the model tools. `status`, `set-backend` and `confirm-host` stay.
- The workspace sync stays as it is: before a call the add-on rebuilds its view
  when someone else changed the user's files (`workspace.changes`), and after a
  call it commits what the code changed (`workspace.commit`). So a file written
  with `write_file` is visible to the next `bash`, and a file `bash` writes is
  in the user's files.
- Migration: chat settings store add-on switches by add-on ID (`addons_off`),
  never tool names, so nothing stored changes. A transcript that shows an old
  `addon__sandbox__read` call stays readable. The installed package keeps
  serving its old tools until the administrator reinstalls it.
- The in-session VM path (`session.NewTools`, `session/vm.go`) is not wired in
  production and is left as it is. Its tool names are reserved only when that
  path has a sandbox, so a host tool named `bash` is no longer dropped.
  Removing that path is a follow-up.

## Host commands

### States

What the model is told at each turn (the "Commands" block):

| State | When | Block |
|---|---|---|
| isolated | The Sandbox's `bash` is in the chat's tools. | `addon__sandbox__bash` runs commands in an isolated sandbox. |
| host | No Sandbox `bash`, and the setting gives this chat host commands. | `bash` runs commands directly on this machine with no isolation, in a scratch folder that holds the user's files; be careful, no destructive commands outside that folder unless asked; the first command may need the user's permission. |
| off | Neither. | Nothing runs code; write the result as a file or give the script (as before). |

The add-on's own Host backend keeps its own wording ("runs commands directly
on the server, with no isolation").

### The setting `chats_host_exec`

A runtime setting, administrators only, `off`, `ask` or `on`, empty for the
default: `off` with accounts (multi-user), `ask` without accounts. Env var
`LOCALAI_CHATS_HOST_EXEC`.

| Value | Who gets `bash` |
|---|---|
| off | Nobody. |
| ask | Administrators (everyone without accounts), in chats a person watches. Each command waits for a card in the chat. |
| on | Every user who may use chats, also in unattended chats and sub-agents. |

A chat does not get host commands when:

- the Sandbox add-on is on for the chat and can run (it wins);
- the chat switched the Sandbox add-on off (the person chose no commands
  there);
- the files are off (there is no workspace to run in);
- LocalAI runs as root (the backend refuses, as the add-on's Host backend
  does);
- in `ask` mode: the chat is unattended (trigger, connector, job) or a
  sub-agent, because nobody sees the card.

When the Sandbox add-on is installed but cannot run, its stand-in tool is
replaced by host `bash` when the setting gives it.

### Consent card (`ask`)

1. The model calls `bash`. The tool publishes a `host_exec_consent` event on
   the chat's stream with an ID and the command, and waits up to 5 minutes.
2. The chat shows a card at the tool line: "Run commands on this machine
   without isolation? Install the Sandbox for isolation", the command, and
   **Run once**, **Always on this instance** and **Don't run**. A person who is
   not an administrator sees "Ask an administrator" and no buttons.
3. **Run once** posts `POST /api/chats/{id}/host-exec/consent {id, decision:
   "once"}`. **Always** first saves `chats_host_exec: "on"` with
   `POST /api/settings` (the normal settings path), then posts `once`.
   **Don't run** posts `deny`. Only an administrator may post `once`.
4. The decision travels as a control message on the chat bus to the frontend
   that runs the turn. The tool runs the command or answers that it was not
   allowed. A tool line keeps a card `host_exec` with the outcome (`once`,
   `denied`, `timeout`, `admin`), so a reload shows what happened.

The card lives in the running frontend's memory: after a reload during the
wait the card is gone and the call ends after 5 minutes with "no answer".

### Guardrails

| | How |
|---|---|
| a. Unprivileged user, scratch folder, sync | The add-on's Host backend and its manager move to `core/services/studio/sandbox/host` and `.../sandbox/runner` and are shared by the add-on and by host commands. A chat's scratch folder is a materialised view of the home and the shared folders (`session.BuildView`); after each call the changes are committed as one change set; before a call the folder is rebuilt when someone else changed the files. Refuses root. |
| b. Clean environment | Only `PATH=/usr/local/bin:/usr/bin:/bin`, `HOME` (a folder of the scratch area), `LANG=C`, `TZ=UTC`. Nothing of the LocalAI process. |
| c. Limits | Timeout 120 s by default, at most 1800 s. Output cut at 64 KiB. The process group is killed on timeout, on cancel and when the output is full. One call at a time per chat; at most 4 scratch folders per user and 16 in all, as in the add-on. |
| d. Consent | Setting and card above. |
| e. Audit | Every call: user, chat, command (cut at 4 KiB), exit code, duration, time. Kept like the shared-folder audit: the auth database in a cluster, a file under the data path, or memory. Also one log line with the prefix `[host-exec]`. `GET /api/chats/admin/host-exec/events` (administrators) and the assistant tool `list_host_exec_events` read it. |
| f. Notice | Tools menu and chip: "Commands run on this machine, not isolated. Install the Sandbox for isolation", with the install link for administrators. |
| g. Prompt | The host block above. |
| h. One bash | The Sandbox's `bash` in the tools removes host `bash`. |

### Distributed mode

The scratch folders are per frontend, like the add-on's sandboxes: the
workspace is the shared truth, every call commits, and a chat whose next turn
runs on another frontend gets a new folder there. The setting is a runtime
setting like the others. The audit is in the auth database when there is one.
The consent decision rides the chat bus to the frontend that runs the turn.

### API surface

- `GET /api/chats/{id}/addons` adds `commands_isolation` (`isolated` or
  `host`), `commands_builtin` (true for host commands) and `commands_consent`
  (`ask` when each command waits for a card).
- `POST /api/chats/{id}/host-exec/consent` (chats feature, owner of the chat).
- `GET /api/chats/admin/host-exec/events?limit=` (administrator), with the
  assistant tool `list_host_exec_events`.
- Runtime setting `chats_host_exec`.

### Hook for the first-start wizard

The wizard step "Run code safely: install the Sandbox" is a later task. Its
link target is the one the notice uses, `SANDBOX_INSTALL_LINK`
(`/app/addons?tab=browse&q=sandbox`) in `utils/chatCommands.js`.

## Follow-ups

- Remove the in-session VM path of `core/services/studio/session`.
- Mount the shared folders in the Sandbox add-on too (it mounts the home only;
  the host operations take one mount today).
- A per-chat switch for host commands.
