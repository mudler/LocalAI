# Saved agents: a folder instead of the whole workspace

Date: 2026-11-01. Status: draft for review. The decisions marked "owner" are
binding; the ones marked "default" were chosen for this draft and each has a
recommendation under [Open questions](#open-questions).

## Intent

A saved agent today works on the whole home folder of its user. A user who
keeps client work, personal papers and project notes side by side cannot give
an agent one project and be sure it sees nothing else.

This spec lets the user assign a saved agent one folder of their files and an
access mode. A chat that runs the agent starts in that folder and cannot read,
list, search, change or run commands against anything outside it, through any
tool, add-on, trigger, connector or sub-agent.

## Goals

- An agent has an optional folder and a mode, read only or read and write.
  Without a folder it behaves exactly as today.
- Every way a chat reaches the files goes through one enforcement point in the
  workspace store, so a caller that forgets the folder gets less access, not
  more.
- The folder is stored by its identity in the store. A rename or a move is
  followed. A deleted folder stops the agent with a clear reason.
- The chat's own folder (attachments and working files) stays in the user's
  home and is a second, separate folder of the chat.
- The same rules apply to chats a person starts, sub-agents, triggers and
  connector runs, on every frontend of a cluster.
- The REST API, the export file, the web interface and the docs show the
  folder.

## Non-goals

- Several folders per agent. One folder, one mode. The data model leaves room
  for a list later.
- A folder outside the user's home (a shared folder) as the agent's folder.
- Limiting what the user sees. The Files page, the Trash, History and Undo of
  the Files page stay user-wide.
- Limiting tools that do not use the workspace: MCP servers, web tools and
  collections that the agent is granted. They are separate grants of the agent
  and keep their own rules. The editor says so in one line.
- A new permission or feature flag. A user who may edit an agent may set its
  folder.

## Anti-goals

The feature fails if any of these is possible for a chat whose agent has a
folder:

- A tool call, a command, an add-on call or a sub-agent reads, lists, finds,
  counts or changes a file outside the folder, the chat's own folder, the
  shared folders and the skill layers.
- A write succeeds in read-only mode, inside the folder.
- A missing, deleted or unreadable folder falls back to the whole home, or to
  anything wider than the folder.
- The prompt, a tool result, a search count, a file card or an error message
  tells the model the names of files outside its folders.
- An agent file exported with a folder is imported somewhere as an agent
  without one.

Each anti-goal has a test in [Security tests](#security-tests).

## Constraints

- Two workspace stores (JSON file and SQL through GORM) and two agent stores
  (JSON file and GORM) must behave the same. Their shared suites
  (`wstest.RunStoreSuite`, `agentstest.Run`) carry the new cases.
- Several frontends may serve the same user (`.agents/distributed-state.md`).
  The feature must not add in-memory state that two frontends could disagree
  on.
- An add-on built against the current SDK must keep working. The add-on
  envelope is decoded with unknown fields refused
  (`pkg/addons/contract/manifest.go` `strictJSON`), so a new envelope field
  would break every installed add-on.
- Tests are Ginkgo. The UI is the React app. User-facing changes update
  `docs/content/` in the same change.
- UI copy names folders as the user sees them (`/Projects/Acme`). It never
  shows guest paths, store identifiers or internal terms.

## Decisions

| # | Decision | Source |
|---|---|---|
| D1 | One access mode per agent: read only, or read and write. | owner |
| D2 | The chat's own folder (`Chats/<chat>/`, with `work/` and the attachments) stays in the home and is a second folder of the chat, next to the agent's folder. | owner |
| D3 | The Files page stays user-wide. A folder that an agent uses is only marked there. | owner |
| D4 | The folder is stored by its identity. A rename or a move is followed. A deleted folder makes the agent refuse to start with a clear reason; it never falls back to the whole workspace. | owner |
| D5 | The folder is chosen in a folder picker in the agent editor. There is no typed path in the UI. | default |
| D6 | Sub-agents of a scoped agent inherit the folder, also those started by the generic `spawn_agent`. | default |
| D7 | Shared folders and the skill layers stay visible to a scoped agent, read only. | default |
| D8 | Triggers and connector runs enforce the folder. | default |
| D9 | An older server that imports a scoped agent file refuses it: the file has version 2. | default |
| D10 | A scoped agent never gets the built-in bash that runs on the host without isolation. Commands run only in the Sandbox add-on. | default, required by the anti-goals |

D10 follows from the docs of the built-in bash: it "can read every file that
LocalAI can read". No workspace rule can confine it, so a scoped agent cannot
have it.

## Design

### Overview

```
agent record ── folder node ID + mode
      │
      ▼
studio.Service.chatAccess(ctx, rec)      the one resolver, per turn
      │   reads the agent (and the parent chat for a sub-agent),
      │   checks the folder, makes sure the chat folder exists
      ▼
session.Access { Actor (fenced), View, Place }
      │
      ├── workspace.Store: every call checks Actor's fence   (layer A, enforcement)
      ├── session.Place: model paths -> workspace paths        (layer B, mapping)
      └── session.View: what a sandbox mounts                  (layer C, mounting)
```

Layer A is the enforcement point: the store refuses any node outside the fence,
whoever calls it. Layers B and C only decide how paths are spelled and what is
mounted. A mistake in B or C gives a "not found" from A, not a leak.

### Data model

`agents.Agent` (`core/services/studio/agents/types.go`) gets two fields:

```go
// FolderID is the workspace node of the agent's folder. FolderMode is
// "read" or "write" for an agent with a folder and "" for an agent that
// works on the whole home. FolderMode set with FolderID 0 is an agent whose
// folder is not chosen yet (an import whose folder was not found): it does
// not start.
FolderID   int64
FolderMode string
```

- `Validate` accepts `FolderMode` "", "read" or "write", and refuses a
  `FolderID` without a mode.
- GORM store: two columns on `studio_agents`, `folder_id` (integer, default 0)
  and `folder_mode` (size 8, default ""). `AutoMigrate` adds them. Existing
  rows read as agents without a folder, which is what they are today.
- JSON store: the two fields are written with the agent. A file without them
  loads as agents without a folder.
- `clone` needs no change (no slices).
- `session.ChatRecord` gets `ScopedAgent bool` (see the "Agent deleted" row of
  [Failure modes](#failure-modes)); both chat stores add it, and old chats read
  as false.

The node ID is the store's own ID (`workspace.Node.ID`), stable across a
rename and a move, kept on a tombstone, and reused when Undo restores the
folder. Both workspace stores already keep IDs this way (`wstest/access.go`
checks that a grant follows a moved node).

### Wire schema and REST

No new route. The agent routes in `core/http/routes/studio.go` carry the field.

```go
// ChatsAgentFolderWrite chooses the agent's folder.
type ChatsAgentFolderWrite struct {
    Path string `json:"path"` // a folder in the user's home, "/" based; "" clears
    Mode string `json:"mode"` // "read" or "write"
}

// in ChatsAgentWrite
Folder *ChatsAgentFolderWrite `json:"folder,omitempty"`

// ChatsAgentFolder is the agent's folder as it is now.
type ChatsAgentFolder struct {
    Path  string `json:"path,omitempty"` // the folder's path in the home now
    Name  string `json:"name,omitempty"`
    Mode  string `json:"mode"`
    State string `json:"state"` // "ok", "missing" or "unset"
}

// in ChatsAgent
Folder *ChatsAgentFolder `json:"folder,omitempty"`
```

- The server turns `path` into the node ID at write time: the path goes
  through `files.Service.Abs`, the node must be a live folder that the user can
  read, and it must not be `Chats/`, `LocalAI/` or a folder inside them
  (`files.Managed`). `/` (the whole home) is allowed; with mode "read" it gives
  a read-only agent on all the files.
- A write without `folder` keeps the stored folder. A client that does not know
  the field (an older page, a script) therefore cannot widen an agent by
  editing it. `"folder": {"path": ""}` clears it.
- The response resolves the node ID to its path at read time, so a renamed
  folder shows its new path. `state` is "missing" when the node is deleted or
  no longer readable, "unset" for an agent whose folder is not chosen.
- Errors use the existing error shape: 400 for a path that is not a folder or
  a managed folder, 409 `agent_folder_missing` when a chat starts with an
  agent whose folder is missing or unset.
- Swagger: the schema blocks change; `make swagger` regenerates. No new tag,
  so `api_instructions.go` is unchanged.

### Export and import

`core/http/endpoints/studio/agentsfile.go`:

- An agent without a folder exports as today, version 1. Older servers keep
  importing it.
- An agent with a folder exports as version 2 with
  `agent.folder: {"path": "/Projects/Acme", "mode": "read"}`. The path is the
  folder's path in the home at export time; the node ID never leaves the
  server.
- Import accepts versions 1 and 2. For version 2 it resolves `folder.path` in
  the importing user's home with the same checks as a write. When there is no
  such folder, the agent is saved with the mode and no folder ("unset") and the
  response says so: `"warnings": ["The folder /Projects/Acme was not found.
  Choose a folder before you use this agent."]`.
- A server from before this change refuses a version 2 file, because it
  accepts only version 1 (`agentsfile.go:129`). This is D9.

### Enforcement: the fence in the store

`workspace.Actor` gets an unexported fence:

```go
// FenceRoot is a folder a fenced actor may reach, by node ID, and the most
// it may do there.
type FenceRoot struct {
    Node int64
    Mode Mode // ModeRead or ModeWrite
}

// Fenced returns a copy of a that reaches only the given folders. Fencing a
// fenced actor keeps the intersection: a fence never grows.
func (a Actor) Fenced(roots ...FenceRoot) Actor

// Fence returns a copy of the roots, and false for an actor with no fence.
func (a Actor) Fence() ([]FenceRoot, bool)
```

Rules, the same in `jsonstore` and `gormstore`:

1. The mode of a fenced actor on a node is the lower of its grant and its
   fence mode. The fence mode is the highest `Mode` of a root that is the node
   or one of its ancestors, and `ModeNone` when there is none. Both stores
   already walk the ancestors in `access` (`jsonstore/access.go`,
   `gormstore/tree.go:221`), so the fence is one more filter on that walk.
2. A fenced actor never takes the administrator shortcut, even when its
   principal is the workspace administrator.
3. `ReadablePrefixes` of a fenced actor lists only the fence roots it may read.
4. `Grant` and `Revoke` refuse a fenced actor (`ErrNotAdmin`).
5. `Delete` and `Move` of a node that is a fence root are refused for that
   actor (`ErrReadOnly`). The agent cannot take its own folder away.
6. `Put` and `Mkdir` that create missing parents are checked at the nearest
   live ancestor, which must be inside the fence.
7. The fence has at most 32 roots; more is `ErrInvalid`.

Everything else follows from `access`: `Open`, `Stat`, `List`, `History`,
`ChangesSince`, `Undo`, `View` (a mount is capped by the actor's mode, and a
mount outside the fence is `ErrNotFound`), `Materialize` and `CommitUpper`
(both check each mount's top node). A deleted root makes everything under it
not found; a restored root works again.

The field is unexported so that only `Fenced` can set it. Code that copies an
actor keeps the fence. Code that builds a new `workspace.Actor{...}` literal
gets an actor with no fence; see
[Callers](#callers-that-must-pass-the-scope) for how that is caught.

### The resolver

One function in `core/services/studio` replaces `chatActor(p, conv)`
(`service.go:534`):

```go
// chatAccess is the access of a chat for this turn: its actor, fenced when
// the chat's agent (or, for a sub-agent, the parent's agent) has a folder,
// the view of its folders and the place that maps the model's paths.
// ErrAgentFolderMissing when that folder is missing or not chosen.
func (s *Service) chatAccess(ctx context.Context, rec session.ChatRecord) (session.Access, error)
```

It:

1. Finds the agent whose folder applies: the chat's own agent; for a
   sub-agent, the parent chat's agent (see [Sub-agents](#sub-agents)).
2. With no folder: returns the plain user actor with the chat ID and today's
   view. Nothing changes for an agent without a folder.
3. With a folder: checks that the node is live and readable by the user, else
   `ErrAgentFolderMissing`. Makes sure the chat folder `Chats/<folder>/`
   exists, as the user, before fencing (a fenced actor cannot create it,
   because `Chats/` is outside its fence). Builds the fence: the agent's
   folder at its mode, the chat folder at `ModeWrite`, each shared folder of
   the user at `ModeRead` (D7), and each skill layer at `ModeRead`.
4. Builds the view and the place from the fence (next sections).

`session.Access` is a small value with unexported fields and one constructor
in `session`, so a tool cannot be built from a principal string:

```go
type Access struct{ /* actor, view, place */ }
func NewAccess(a workspace.Actor, v workspace.View) (Access, error)
func (x Access) Actor() workspace.Actor
func (x Access) View() workspace.View
func (x Access) Place() Place
// Owner is the user's own actor, without the fence and the chat ID, for the
// host's bookkeeping (chat folder rename, upload of a person's attachment).
func (x Access) Owner() workspace.Actor
```

The resolver runs at every turn start, not only at `Open`. `syncAccess`
(`session/access.go:97`) already rebuilds the sandbox and restarts the harness
when the actor or the view changes; `sameActor` must compare fences too. An
edit of the agent (new folder, new mode) therefore applies from the next turn
on every frontend. A turn that is running keeps the access it started with.

### The view and the guest layout

`session.BuildView` takes the place's primary folder instead of computing
`HomePath(a.Principal)`:

| Guest path | Unscoped chat (today) | Scoped chat |
|---|---|---|
| `/workspace/home` | the home, read and write | the agent's folder, at the agent's mode |
| `/workspace/chat` | not mounted | the chat folder `Chats/<folder>/`, read and write |
| `/workspace/shared-<name>` | shared folders, at their grant | shared folders, read only |
| `/workspace/shared-skills` and the other skill layers | read only | read only |

The agent's folder keeps the name `home` so that every spelling the model and
the tools already use (`/a.txt`, `/workspace/home/a.txt`, `home/a.txt`)
keeps meaning "the place I work in". The name `chat` is reserved like the
skill layer names, so a shared folder named `chat` gets `shared-chat`.

The host bash runner (`hostexec`) and the sandbox runner build their mounts
from this view, so the Sandbox add-on sees the same layout.

### The place: mapping the model's paths

`session.Place` replaces the home-only mapping in `files/tools.go`
(`toolPath`, `HomePath`, `strayGuestPath`, `locate`, `locateShared`) and the
calls of `files.Service.Abs` and `Rel` that use a chat's principal:

```go
// Place maps the paths a model writes to workspace paths, from the mounts
// of the chat's view. It refuses "..", NUL, control characters and a path
// under /workspace that is no mount.
type Place struct{ /* mounts */ }
func (p Place) Resolve(path string) (Target, error) // Target: abs, root, write, guest spelling
func (p Place) Guest(abs string) (string, bool)     // workspace path -> /workspace/... ; false outside every mount
```

- `/x`, `x`, `home/x` and `/workspace/home/x` resolve in the primary mount;
  `/workspace/chat/x` in the chat folder; `/workspace/shared-<name>/x` in that
  shared folder; the skill layer names as today.
- The file tools, the media tools (`media/media.go:368`), `search_files`, the
  chat's file list (`chatfiles.go`, `guest_path`), the work folder prompt and
  the rules nudge take their paths from the place.
- For an unscoped chat the place has the home at `home` and the shared folders,
  which is today's mapping. The existing tests of `files/tools_test.go` stay
  green unchanged.
- Cards and change notes still show the path in the home (`/Projects/Acme/a.md`),
  because the user's Files page is user-wide (D3).

### The work folder as the second folder

- The chat folder exists before the chat is fenced (resolver, step 3).
- In a scoped chat `workfiles.GuestFolder` is `/workspace/chat/work`, and the
  attachments are in `/workspace/chat/`. The prompt line that names the work
  folder and the line in `LayoutPrompt` follow.
- `workfiles.Kind` and `Stray` (deliverable, working, stray helper files)
  learn the chat mount: a helper file under `/workspace/chat/work` is working,
  one in the agent's folder is a deliverable.
- A rename of the chat moves `Chats/<old>` to `Chats/<new>` as the owner
  (`Access.Owner`). The fence holds the node ID, so the chat keeps its folder.
- Uploads by the person go through the owner actor into the chat folder, which
  is where they go today.

### Search and the pending count

`files.Service.Search` takes a root from the place. For a scoped chat the root
is the agent's folder (and the other mounts, searched one by one), never the
home. Hits are filtered by `mayReadEntry` with the fenced actor. The pending
count past `pendingCheck` (`files/search.go:247`) counts by path prefix; with
the root at the agent's folder it can only count files inside it. A test puts
more than `pendingCheck` pending files outside the folder and checks that the
count is 0.

### History, undo and trash

- The model has no undo or history tool. `commandMark` and `commandFiles`
  (`commandfiles.go`) read the chat's own change sets through `userOf(a)`,
  which builds an unfenced literal. They change to the fenced actor: the
  chat's change sets can only touch nodes inside the fence anyway.
- The Files page's History, Undo and Trash stay user-wide (D3).
- An agent whose folder went to the Trash is "missing". Undo of that delete
  restores the node with the same ID, so the agent works again with no edit.

### System prompt

`LayoutPrompt` (`session/layout.go:243`) of a scoped chat:

```
The files you can use are in these folders; use these paths with the file tools:
- /workspace/home: the folder "Acme" of the user (read only). You cannot see the user's other files.
- /workspace/chat: this chat's own folder (read and write). Files the user attaches are here; save working files in /workspace/chat/work/.
- /workspace/shared-<name>: a folder shared with the user (read only).
There is nothing else under /workspace.
```

- The "build a Page" hint is left out: pages live in `pages/` of the home,
  outside the folder (see open question Q4).
- The persona and user blocks (`wellknown.TurnPrompt`, called from
  `subagent.go:102`) are read by the host from `LocalAI/` and stay: they are
  the user's own words about themselves, not files the model reads.
- The rules nudge (`rulesnudge.go`) walks from the agent's folder up to the
  folder itself, not to the home root. A trigger run's rules
  (`triggersvc.go:447` `runRulesFolder`) start at the agent's folder for a
  non-file trigger.
- `list_agents` (`savedagents.go`) adds the folder name and mode of each agent
  so a parent model can choose a sub-agent knowingly.

### Sub-agents

- The resolver gives a sub-agent the folder of its parent's agent. This covers
  the generic `spawn_agent` (`subagent.go:302`, `AgentID` empty), whose chat
  today opens with a plain user actor and therefore a full sandbox view.
- A sub-agent started from a saved agent that has its own folder: the child's
  folder must be the parent's folder or inside it, and its mode is the lower of
  the two. Otherwise `spawn_agent` fails with "This agent works in another
  folder than this chat." The parent's folder is never widened by a child.
- A scoped parent cannot start a saved agent without a folder: the child
  inherits the parent's folder.
- The child uses the parent's chat folder as its second folder, as today for
  the work folder (`subWorkBlock`).

### Triggers and connector runs

These chats are created by `CreateAs` with an origin, and their turns go
through the same resolver, so the fence applies. In addition:

- A file trigger of a scoped agent must watch the agent's folder or a folder
  inside it. `triggerscrud.go` refuses it at create and update ("The trigger's
  folder must be inside the agent's folder."); `checkFolder`
  (`triggersvc.go:381`) checks it again at each run, and a run outside skips
  and switches the trigger off with that reason.
- A run whose agent folder is missing is skipped with reason "the agent's
  folder is missing" and the trigger is switched off, like a trigger whose
  folder is gone. It is never run on the home.
- `runMessage` reads the folder rules with the fenced actor.
- Connector replies: `readChatFile` (`connectorfiles.go:50`) reads the files to
  send with the chat's access instead of `Files.Actor(owner)`.
- A connector message to an agent whose folder is missing gets one plain reply,
  "This agent cannot work right now: its folder is missing.", and is logged.

### Add-on host operations

The add-on workspace operations (`hostops/engine_workspace.go`) work today as
the user: `Files.Actor(p)` and `Files.Abs(p, path)`, with the principal from
the add-on actor. The chat is looked up only for the change set attribution,
and only for action tools, because only an action carries the chat in
`ResourceID` (`addons/tools/adapter.go:595`). A read tool such as the Office
add-on's `read_docx` carries no chat at all. Both are full-home bypasses.

Design:

- The host binds every add-on call made from a chat turn to that chat, for
  every tool kind. The binding is host-side only: it is stored with the
  envelope in `hostops.InFlight` (a new field of the in-flight entry), and it
  travels between frontends inside the signed `gateway.ForwardedRequest`. It is
  never added to the add-on envelope, which installed add-ons decode strictly.
- `hostops.Call` gets `ChatID string`, filled from the in-flight entry.
- `WorkspaceHost` gets a resolver hook instead of `Files.Actor` and `Abs`:
  `Access func(ctx context.Context, principal, chatID string) (session.Access, error)`.
  With a chat it returns the chat's access; without one it returns the user's
  access (user-wide, as today, for add-on pages and UI calls that no chat
  made).
- `materialize`, `commit`, `export`, `import` and `changes` resolve every path
  through the place and act as the access's actor. The add-on's `/` is the
  agent's folder. When the add-on mounts `/` at `home` and the chat is scoped,
  the host adds the chat folder at `chat`, so the Sandbox sees the same layout
  as the host bash without an add-on change.
- The tool adapter takes a `ChatCall` value built only by the chat turn, so a
  call from a chat cannot be made without its binding.

### Pages and widgets

- Pages and their bridge are a user surface: the viewer's browser calls them
  with the viewer's rights, confined to the page's own folder
  (`pages/pages.go` `loc`). They stay user-wide, like the Files page.
- A scoped agent cannot write `pages/` of the home (it is outside the fence)
  and is not told to build pages. A page it writes inside its folder is an
  ordinary file.
- Widgets live in `LocalAI/home.json`, outside every agent folder. A widget
  linked to a trigger of a scoped agent shows that trigger's runs as today.

### Callers that must pass the scope

Every chat-originated workspace call must use the chat's access. The list, from
the code:

| Caller | Today | Change |
|---|---|---|
| `service.go` `chat` (Open) | `chatActor(p, conv)` | `chatAccess(ctx, rec)` |
| `capabilities.go:219`, `webtools.go:170`, `shadowers.go:122`, `mcpconnectors.go:130` | `chatActor(p, conv)` | `chatAccess` |
| `subagent.go` `chatTools`, file tools, media tools | the session's actor | the session's `Access` |
| `commandfiles.go` `commandMark`, `commandFiles` | `userOf(a)` literal | fenced actor |
| `workfolder.go:86` `ensureWorkFolder` | `userOf(a)` | owner actor, on the chat folder only |
| `chatfiles.go:128` chat file list | `workspace.Actor{Principal: p}` | user surface; `guest_path` from the place |
| `session/chat.go:1015` uploads, `:1045`, `:1095`, `:1235` | `c.user` (the chat actor without the chat ID) | `Access.Owner`, chat folder only |
| `rulesnudge.go:41` | home path | the place's primary folder |
| `connectorfiles.go:51` | `Files.Actor(owner)` | chat access |
| `triggersvc.go:387,447,477`, `triggerscrud.go:166,409` | `Files.Actor(owner)`, home | chat access, agent folder |
| `hostexec` `Options.Actor` (`application/studio_service.go:147`) | `files.UserActor(user)` | resolver hook (context, error) |
| `sandbox/runner` `Options.Actor` (`runner.go:144`) | literal | resolver hook |
| `hostops/engine_workspace.go:97,322,379,388,429,446` | `Files.Actor(p)`, `Files.Abs` | `Access` hook |
| `service.go:1420` Delete | `chatActor` | owner actor (a person's action) |

Host reads that stay unfenced on purpose, because they are the host reading
the user's settings or explicit grants, not the model reading files:
`wellknown` (persona, user, home), `skills.Resolve` in `skillsvc.go` and
`chatcommands.go`, and `loadskill.go:147` (reads only skills the agent
grants, by name).

How a missed caller is caught:

1. `chatActor` is removed. Its replacement returns an error and needs a chat
   record, so every old call site stops compiling until it is moved.
2. Tools are built from `session.Access`, which has no exported fields and one
   constructor.
3. The runner and host-exec hooks change signature to
   `func(ctx context.Context, user, chat string) (workspace.Actor, error)`.
4. A Ginkgo spec in `core/services/studio` parses the package sources (and
   `hostops`, `hostexec`, `sandbox/runner`) and fails on a
   `workspace.Actor{...}` literal, `files.UserActor(` or `Files.Actor(`
   outside an allowlist that names the host reads above. A new caller must
   either use the access or be added to the allowlist in review.
5. Layer A refuses any node outside the fence, so a caller that still slips
   through with the fenced actor and a wrong path gets "not found".

### Failure modes

| Situation | Behaviour |
|---|---|
| Folder deleted (in Trash) or unreadable | New chat: 409, "The folder of this agent is missing. Choose a folder in the agent's settings." Open chat: the turn does not start; the chat shows the same message. Trigger: skipped, switched off. Connector: one plain reply. Sub-agent: tool error. |
| Folder restored from Trash | Works again, same node ID. |
| Folder renamed or moved | Followed. The agent shows the new path; the prompt shows the new name at the next turn. |
| Folder moved into a shared folder | Followed while the user can read it; the mode is capped by the user's grant there. |
| Mode or folder changed while a chat is open | Applies from the next turn, on every frontend (resolver per turn, `syncAccess`). A running turn finishes with the access it started with. |
| Agent deleted | Today's rule: the chat runs with `deleted: true` and no grants. A deleted agent leaves no folder to read, so the chat record keeps a flag, `ScopedAgent bool` on `session.ChatRecord`, that the resolver sets whenever it sees a folder on the chat's agent and clears when it sees none. A chat with the flag whose agent is gone is treated as missing and refused, never the whole home. A chat without it keeps today's rule. |
| Import with a folder that does not exist here | Saved as "unset" with a warning; refuses to start until a folder is chosen. |
| Older server imports a version 2 file | Refused (D9). |
| Server downgraded after scoped agents exist | The older binary ignores the new columns and the agents work on the whole home. This cannot be prevented from the new code. The release notes say it; see Q6. |

### Distributed behaviour

The feature is stateless per request (mode 3 of
`.agents/distributed-state.md`):

- The folder and mode live in the agent store, which is the GORM store in a
  cluster.
- The resolver reads the agent and the folder node at every turn start, on the
  frontend or chat worker that runs the turn. Nothing is cached between turns.
- Add-on calls resolve the chat from the binding on the frontend that owns the
  add-on, from the same stores.
- The per-process `egressChats` cache maps a one-way key to a chat ID and never
  changes, so it cannot disagree between frontends.

The docs page states the mode. A test runs two service instances on the same
stores: an agent edit through the first applies to the next turn served by the
second.

## Security tests

All Ginkgo. Each line names the anti-goal it covers.

Store (in `wstest`, so both stores run it):

- a fenced actor cannot `Stat`, `Open`, `List`, `History` or `ChangesSince` a
  sibling of its root, the root's parent, the home root or another user's home;
- the administrator principal, fenced, is confined too;
- read mode refuses `Put`, `Mkdir`, `Move` and `Delete` inside the root;
- `Delete` and `Move` of the root itself are refused;
- `Move` from inside to outside and from outside to inside are refused;
- `Put` of `root/a/b/c.txt` creates the parents; a path whose nearest live
  ancestor is outside is refused;
- `View` of a folder outside is not found; `CommitUpper` of an upper layer with
  an entry outside every mount is an `outside-view` conflict;
- `ReadablePrefixes` returns only the roots; `Grant` is refused;
- a renamed and a moved root are still reachable; a deleted root is not; a
  restored root is again;
- `Fenced` on a fenced actor never widens it.

Paths (in `files/tools_test.go` and a new `session/place_test.go`), for
`read_file`, `write_file`, `list_files`, `move_file`, `delete_file`,
`search_files` and `read_document`:

- `../x`, `/../x`, `/workspace/home/../x`, `/workspace/chat/../../x`,
  `/workspace/shared-x/../home/x`;
- `/users/<p>/x`, `//users/<p>/x`, `/workspace/home//../x`;
- encoding tricks taken as literal names, never as separators or parents:
  `..%2fx`, `%2e%2e/x`, `..\x`, `\..\x`, U+2024 ONE DOT LEADER twice,
  U+FF0F FULLWIDTH SOLIDUS, a NUL byte, control characters;
- case: `/Workspace/Home/x` is not the guest home;
- a name outside the folder that exists in the home (`/Other/x.pdf`) resolves
  inside the folder and is not found;
- `search_files` finds nothing outside, and the indexing count is 0 with more
  than `pendingCheck` pending files outside.

Symlinks: an upper layer with a symbolic link that points outside commits as
`unsupported`; a materialized tree is built through `os.Root` (existing tests
extended with a fenced view).

Add-on operations (in `hostops/engine_workspace_test.go`):

- with a chat binding to a scoped chat: `materialize` of `/` mounts only the
  folder and the chat folder; `export` and `import` of `/../x`, `/Other/x` and
  `/users/<p>/x` fail; `changes` lists nothing outside; `commit` of an entry
  outside is a conflict;
- a read call (no `ResourceID`) from a scoped chat is bound and confined;
- a call with no binding is user-wide (documented behaviour for add-on pages);
- the add-on envelope sent to the add-on has no new field.

Chats (in `core/services/studio`):

- a scoped chat's `list_files /` lists the folder only; the prompt names only
  its folders;
- the built-in host bash is not offered to a scoped chat (D10);
- a generic `spawn_agent` child of a scoped chat is confined, also its
  sandbox view;
- a saved-agent child with a folder outside the parent's is refused; one
  inside gets the narrower folder and the lower mode;
- a file trigger outside the folder is refused at create and skipped at run; a
  run with a missing folder is skipped and never reads the home;
- a connector reply does not send a file outside the folder;
- a chat whose agent folder is deleted refuses the turn and never resolves to
  the home; so does a chat whose scoped agent was deleted;
- the source scan spec (callers) passes.

## UI

All in `core/http/react-ui/`, design tokens only, copy in every locale file.

- **Agent editor** (`pages/AgentCreate.jsx`): a new section **Files** (icon
  `folder`) after **Prompts and goals**. Two choices: **All my files**
  (default) and **One folder**. With one folder: a **Choose folder** button
  opens a folder picker; the chosen folder shows as its path with a folder
  icon and a **Change** button. Below it, a two-way switch: **Can read** /
  **Can read and change**. Help line: "Chats with this agent see only this
  folder and their own chat folder. Shared folders and skills stay readable.
  MCP servers and web tools you grant are not limited to this folder." The
  closed section summarises "All my files" or "/Projects/Acme, can read".
- **Folder picker**: `components/files/FolderPicker.jsx`, extracted from the
  tree walk of `MoveDialog.jsx` (which then uses it). It lists only folders of
  the user's home, hides `Chats` and `LocalAI` from choice (shown disabled
  with a hint "LocalAI manages this folder"), and has a breadcrumb, a loading
  row, an empty state ("No folders here") and an error state with **Try
  again**.
- **Missing folder**: the section shows an inline error, "This folder is gone.
  Choose another folder to use this agent.", and the save keeps working.
- **Agents page** (`pages/Agents.jsx`, `pages/AgentPage.jsx`): a chip with the
  folder name and a lock icon for read only; a warning chip "Folder missing"
  that links to the editor.
- **Files page** (`pages/Workspace.jsx`): a folder that an agent uses gets a
  small marker with a tooltip "Used by Research (can read)". The marker comes
  from the agent list the page already can fetch; there is no new Files API
  field. The page stays user-wide.
- **Chat**: the header of a scoped chat shows a chip with the folder name and
  the mode. Starting a chat with a missing folder shows the server message
  with an **Edit agent** action. The chat's files pane is unchanged.
- **Import**: the import result shows the warning when the folder was not
  found.
- Playwright specs in `e2e/agents-edit.spec.js` and `e2e/agents.spec.js`
  (picker, mode, missing state), render-smoke for the picker, so coverage does
  not drop.

## Docs to update

- `docs/content/features/chats.md`: a section "Give an agent one folder"
  under "Saved agents" (what the agent sees, the chat folder, modes, missing
  folder, sub-agents, triggers and connectors, the host bash rule, the
  distributed mode: stateless per request), and the API example with
  `folder`.
- `docs/content/features/chats.md` "Running commands": a scoped agent runs
  commands only in the Sandbox.
- `docs/content/getting-started/first-agent.md`: one step about the folder.
- `docs/content/features/addons.md`: workspace operations of a chat run
  inside the chat's folders; `/` is the agent's folder.
- The agent export format (version 2) where the import is documented.
- Release notes: the downgrade caveat (Q6).

## Open questions

Each has a recommendation; the owner decides.

- **Q1. Mid-turn narrowing.** An edit applies from the next turn. Should an
  edit that narrows (another folder, write to read) also stop the running
  turns of that agent's chats? Recommendation: no for this change; the turn
  limit bounds the window, and the editor says "Applies from the next
  message". Revisit if agents get long unattended goals.
- **Q2. The host bash (D10).** Recommendation: keep D10. A scoped agent on a
  server without the Sandbox can still read and write files with the file
  tools; it only loses commands, and the editor says so.
- **Q3. Shared folders read only (D7).** Recommendation: read only, as the
  default says; a write would let a scoped agent change files that other
  people see, outside the folder the user chose.
- **Q4. Pages from a scoped agent.** Recommendation: no pages in this change.
  Mounting `pages/` would expose the code of every other page. A later change
  can list pages found under the agent's folder.
- **Q5. A shared folder as the agent's folder.** Recommendation: not in this
  change; the picker shows the home only. The fence already supports it.
- **Q6. Downgrade.** An older binary ignores the new columns and widens the
  agents. Recommendation: say it in the release notes and accept it; the
  alternative (encode scoped agents so older servers fail to load them) breaks
  every agent on downgrade.
- **Q7. Write without `folder` keeps it.** Recommendation: keep, so an older
  client cannot widen an agent; clear with `"folder": {"path": ""}`.
- **Q8. `/` as the folder.** Recommendation: allow it, mainly for a read-only
  agent on all the files.
- **Q9. Add-on mounts.** Recommendation: the host adds the chat folder when an
  add-on mounts `/` at `home`, so no add-on needs a change. An add-on that asks
  for other mounts gets only what the place resolves.
