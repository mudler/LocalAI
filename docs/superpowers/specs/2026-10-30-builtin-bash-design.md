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
   always wins: the chat never gets two tools that run commands. The rule is
   general and by tool name: an add-on tool may declare that it shadows a
   built-in tool (see "Shadowing").
5. When the Sandbox is switched off for a chat, stopped or cannot run, the
   built-in `bash` serves instead, under the host-commands rules.
6. Administrators manage the built-in tools in **Settings > Tools**: switch
   each one off, choose the host-commands mode, and pick which add-on serves a
   tool when several replace it.

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

- The manifest and `internal/tools.Tools` list only `bash` among the model
  tools, and its definition says `"shadows": "bash"`. `status`,
  `set-backend` and `confirm-host` stay, under their add-on names
  (`addon__sandbox__status`).
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

## Shadowing

An add-on tool can replace a built-in chat tool by name.

- **Manifest.** A tool definition may say `"shadows": "<name>"`. The name
  must be in `contract.ReplaceableTools` (`bash`, `web_search`,
  `web_fetch`); the file tools are not on the list. A page-only tool cannot
  shadow, and one manifest shadows a name at most once. The host checks this
  at install with the other manifest fields (`validateToolDefinitions`), and
  the tool adapter checks it again when it lists the tools.
- **Install warning.** When the add-on to install shadows a tool that an
  installed add-on already shadows, the install dialog warns before the
  install: "This add-on replaces the built-in <tool>. Another add-on
  (<name>) already does. Only one can be active; you choose which in
  Settings > Tools." The install is allowed.
- **Assembly** (`core/services/studio/shadow.go`). The broker lists the tool
  under its add-on name with `Shadows` set. The chat turns it into a tool of
  the built-in's name, with `Provider` set to the add-on's ID. A built-in
  tool is added only when no add-on tool has its name (`withBuiltins`); any
  other tool of that name (an MCP tool, say) is dropped. When several
  add-ons shadow one name, only the active one is offered (the pin, or with
  no pin the first by add-on ID: the host keeps no install time yet).
- **Routing.** The chat tool keeps the add-on name for the broker, so the
  call goes through the same permission checks and audit as before.
- **Fallback.** An add-on that is switched off for the chat, stopped or
  unavailable offers nothing, so the built-in serves; the Tools menu says so.
- **Labels.** A call the add-on served carries the card
  `tool_provider` (ID: the add-on); a call of the built-in `bash` carries
  `host_exec`. The tool line shows "Sandbox" or "Built-in, this machine",
  also after a reload. The Tools menu shows the provider of `bash`.

## Built-in tools registry and Settings > Tools

`core/services/studio/builtins` holds the built-in chat tools an add-on may
replace. Another feature registers its tools there (`web_search`,
`web_fetch`):

```go
type Tool struct {
	ID          string // a name in contract.ReplaceableTools
	Label       string // English; the UI translates by ID
	Description string
	// Setting is the runtime setting that switches the tool off; Enabled
	// reads it.
	Setting string
	Enabled func() bool
	// Build returns the tool for one chat, or ok false and a short reason
	// when this chat cannot have it now.
	Build func(ctx context.Context, c Chat) (tool session.Tool, ok bool, reason string)
}

type Chat struct {
	Actor    workspace.Actor
	Origin   string // "" for a chat a person watches
	SubAgent bool
	Admin    bool
}

func NewRegistry() *Registry
func (r *Registry) Register(t Tool) error // refuses an ID off the list or twice
func (r *Registry) Tools() []Tool          // in ID order
func (r *Registry) Lookup(id string) (Tool, bool)
```

The studio service takes the registry in `Deps.Builtins` and pins in
`Deps.ToolPins` (the runtime setting `chats_tool_providers`, a map from tool
ID to add-on ID).

`GET /api/chats/admin/tools` (administrators; assistant tool
`list_chat_tools`) lists each registered tool: `id`, `label`,
`description`, `setting`, `enabled`, `provider` (`builtin` or an add-on
ID), `candidates` (the add-ons that shadow it) and `pinned`. The **Settings >
Tools** tab shows one row per tool: an enabled switch, the provider, the
add-on choice when there are several candidates (with a notice "N add-ons
replace <tool>: choose which one to use" when none is pinned), and for
`bash` the host-commands mode with its explanation. Changes go through
`POST /api/settings`.

## Host commands

### States

What the model is told at each turn (the "Commands" block):

| State | When | Block |
|---|---|---|
| isolated | The Sandbox serves `bash` (it shadows the built-in). | `bash` runs commands in an isolated sandbox. |
| host | The built-in `bash` serves, and the setting gives this chat host commands. | `bash` runs commands directly on this machine with no isolation, in a scratch folder that holds the user's files. It can read what LocalAI can read and reach the internet, the local network and the LocalAI API. Be careful: no destructive or data-sending commands outside the task unless asked. A command may need the user's permission first. |
| off | Neither. | Nothing runs code; write the result as a file or give the script (as before). |

The add-on's own Host backend keeps its own wording ("runs commands directly
on the server, with no isolation").

### The setting `chats_host_exec`

A runtime setting, administrators only, `off`, `ask` or `on` (shown in the
`bash` row of Settings > Tools; `off` is the row's switch), empty for the
default: `off` with accounts (multi-user), `ask` without accounts. Env var
`LOCALAI_CHATS_HOST_EXEC`.

| Value | Who gets `bash` |
|---|---|
| off | Nobody. |
| ask | Administrators (everyone without accounts), in chats a person watches. Each command waits for a card in the chat. |
| on | Every user who may use chats, also in unattended chats and sub-agents. |

A chat does not get host commands when:

- the Sandbox add-on is on for the chat and can run (it wins);
- an administrator switched the built-in `bash` off (Settings > Tools);
- the files are off (there is no workspace to run in);
- LocalAI runs as root (the backend refuses, as the add-on's Host backend
  does);
- in `ask` mode: the chat is unattended (trigger, connector, job) or a
  sub-agent, because nobody sees the card.

When the Sandbox add-on is switched off for the chat, stopped, or cannot run,
the built-in `bash` serves (when the setting gives it), its stand-in tool is
left out, and the Tools menu says that the Sandbox is not serving and commands
run on this machine.

The host `bash` has the network of the LocalAI process: the internet, the
loopback (the LocalAI API too), the local network and cloud metadata
addresses. Nothing can block that without privileges, so there is no
"no network" switch. The consent card, the Tools notice, the model's block,
the docs and the audit (which logs every command) say so.

### Consent card (`ask`)

1. The model calls `bash`. The tool publishes a `host_exec_consent` event on
   the chat's stream with an ID and the command, and waits up to 5 minutes.
2. The chat shows a card at the tool line: "Run this command on this
   machine? Commands run with no isolation. They can read files that LocalAI
   can read and reach the internet and your network. Install the Sandbox for
   isolation.", the command, and
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
| f. Notice | Tools menu and chip: "Commands run on this machine, with its files and network, not isolated. Install the Sandbox for isolation", with the install link for administrators. |
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
  `host`), `commands_provider` (`builtin` or the add-on's ID),
  `commands_fallback` (the add-on that would serve but does not, with its
  reason) and `commands_consent` (`ask` when each command waits for a card).
- `GET /api/chats/admin/tools` and runtime settings `chats_tool_providers`.
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
