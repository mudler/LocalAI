# Saved agent folder scope: implementation plan

> **For agentic workers:** use superpowers:subagent-driven-development or
> superpowers:executing-plans. Each lane below is one agent task in its own
> worktree. Write the failing Ginkgo specs first, see them fail, then make
> them pass.

**Spec:** `docs/superpowers/specs/2026-11-01-agent-folder-scope-design.md`.
Read it before any lane. This plan gives files, interfaces, test names and
assertions, not function bodies.

**Goal:** a saved agent can have one folder and a mode; every chat that runs
it, its sub-agents, its triggers and connector runs, its commands and its
add-on calls reach only that folder, the chat folder, the shared folders and
the skill layers (read only).

## Global constraints

Every lane carries these:

- Tests are Ginkgo (`Describe`/`It`), in the package's existing suite.
- Both workspace stores and both agent stores behave the same; new store
  cases go into the shared suites (`wstest`, `agentstest`, `storetest`).
- Nothing a fenced chat does may reach outside its fence. When in doubt, refuse
  with "not found".
- No new field in the add-on envelope (`pkg/addons/contract`): installed
  add-ons decode it with unknown fields refused.
- No new in-memory state across turns: the access is resolved per turn from
  the stores (stateless per request, `.agents/distributed-state.md`).
- User-facing changes update `docs/content/` in the same commit.
- Commit trailer `Assisted-by: AGENT_NAME:MODEL_VERSION`; no `Signed-off-by`
  and no `Co-Authored-By` from an AI.
- Coverage only goes up.

## Lane map

| Lane | Title | Size | Depends on | Parallel with |
|---|---|---|---|---|
| 1 | Store fence (`workspace`) | M | none | 2 |
| 2 | Agent record, schema, REST, export v2 | M | none | 1 |
| 3 | Chat access resolver and callers | L | 1, 2 | none |
| 4 | View, place and prompt (`session`, `files`, `media`, search) | L | 1, interface of 3 | 3 (against the `Access` interface stub) |
| 5 | Command runners (host bash, sandbox runner) | S | 3, 4 | 6, 7, 8, 9 |
| 6 | Add-on host operations and the chat binding | M | 3, 4 | 5, 7, 8, 9 |
| 7 | Triggers and connectors | M | 3 | 5, 6, 8, 9 |
| 8 | Sub-agents and `list_agents` | S | 3 | 5, 6, 7, 9 |
| 9 | Web interface | M | 2 | 3 to 8 |
| 10 | Docs, cross-lane security suite, release note | S | all | none |

Order: 1 and 2 in parallel, then 3 and 4 (4 can start on a stub of
`session.Access` agreed at the end of lane 1), then 5 to 9 in parallel, then
10. One merge agent merges the lanes in that order and runs the full
`core/services/studio/...`, `core/services/addons/...` and UI test suites
after each merge.

---

## Lane 1: store fence

**Files:** `core/services/studio/workspace/types.go`, `export.go`,
`jsonstore/access.go`, `jsonstore/grants.go`, `jsonstore/tree*.go` (Move,
Delete, parent creation), `gormstore/tree.go`, `gormstore/grants.go`,
`wstest/fence.go` (new), `wstest/wstest.go` (register it).

**Produces:**

```go
type FenceRoot struct { Node int64; Mode Mode }
func (a Actor) Fenced(roots ...FenceRoot) Actor          // intersects, never widens
func (a Actor) Fence() ([]FenceRoot, bool)
const MaxFenceRoots = 32
```

Store rules 1 to 7 of the spec ("Enforcement: the fence in the store").

**Failing specs first** (`wstest/fence.go`, `Describe("a fenced actor")`, run
by both stores):

- `It("cannot reach a sibling, the parent, the home root or another home")`:
  `Stat`, `Open`, `List` on each is `ErrNotFound`.
- `It("is confined even when its principal is the administrator")`.
- `It("cannot write in a read-only root")`: `Put`, `Mkdir`, `Move`, `Delete`
  are `ErrReadOnly`; reads succeed.
- `It("cannot delete or move its own root")`: `ErrReadOnly`.
- `It("cannot move a node out of or into the fence")`.
- `It("creates missing parents only inside the fence")`.
- `It("sees only its roots in ReadablePrefixes")`.
- `It("cannot grant or revoke")`: `ErrNotAdmin`.
- `It("filters History and ChangesSince to the fence")`: a change set outside
  is absent.
- `It("refuses a view mount outside the fence and caps a mount inside")`.
- `It("commits an upper entry outside every mount as an outside-view conflict")`.
- `It("follows a renamed and a moved root")`.
- `It("loses a deleted root and gets it back after Undo")`.
- `It("never widens when fenced twice")`: `Fenced(A).Fenced(B)` reaches only
  `A ∩ B`.
- `It("refuses more than MaxFenceRoots roots")`.
- `It("keeps the fence on a copy of the actor")`: assigning the actor and
  clearing `ChatID` keeps it.

**Done when:** `go test ./core/services/studio/workspace/...` passes, the
existing `wstest` cases are unchanged and green.

## Lane 2: agent record, schema, REST, export v2

**Files:** `core/services/studio/agents/types.go`, `gorm.go`, `json.go`,
`agentstest/suite.go`; `core/services/studio/store/{store,gorm,json}.go` and
`storetest/contract.go` (`ChatRecord.ScopedAgent`, `session/store.go`);
`core/schema/chats.go`; `core/http/endpoints/studio/agents.go`,
`agentsfile.go` and their tests; swagger (`make swagger`).

**Produces:**

```go
// agents.Agent
FolderID   int64
FolderMode string // "", "read", "write"

// session.ChatRecord
ScopedAgent bool

// schema
type ChatsAgentFolderWrite struct { Path, Mode string }
type ChatsAgentFolder struct { Path, Name, Mode, State string }
// ChatsAgentWrite.Folder *ChatsAgentFolderWrite; ChatsAgent.Folder *ChatsAgentFolder

// endpoints/studio: path <-> node resolution, behind an interface so lane 3
// can reuse it
type FolderResolver interface {
    NodeOf(ctx context.Context, principal, path string) (int64, error) // live, readable, not managed
    PathOf(ctx context.Context, principal string, node int64) (path string, ok bool)
}
```

**Failing specs first:**

- `agentstest`: `It("round-trips the folder and its mode")`,
  `It("reads an agent stored before the folder fields as one without a folder")`
  (GORM: insert a row through the old struct; JSON: load a file without the
  keys), `It("refuses a folder ID without a mode")`.
- `storetest`: `It("round-trips ScopedAgent and reads old chats as false")`.
- `endpoints/studio/agents_test.go`:
  `It("stores the folder by node and answers its current path after a rename")`,
  `It("keeps the folder when a write has no folder field")`,
  `It("clears the folder with an empty path")`,
  `It("refuses a file, a missing path, Chats and LocalAI as the folder")` (400),
  `It("answers state missing for a deleted folder")`.
- `agentsfile_test.go`:
  `It("exports an agent without a folder as version 1")`,
  `It("exports an agent with a folder as version 2 with the path, never the node")`,
  `It("imports a version 2 file and resolves the folder in the importer's home")`,
  `It("imports a version 2 file whose folder is missing as unset with a warning")`,
  `It("still imports version 1")`.
- `It("an importer that knows only version 1 refuses a version 2 file")`:
  call the version-1 check directly with a version 2 document.

**Done when:** both stores pass, swagger regenerated, the docs section on
the agent API in `docs/content/features/chats.md` shows `folder`.

## Lane 3: chat access resolver and callers

**Files:** `core/services/studio/session/access.go` (new `Access`),
`core/services/studio/agentaccess.go` (new, the resolver), `service.go`,
`capabilities.go`, `webtools.go`, `shadowers.go`, `mcpconnectors.go`,
`subagent.go` (turn tools take `Access`), `commandfiles.go`, `workfolder.go`,
`session/chat.go` (owner actor for uploads, folder creation, rename),
`session/manager.go` and `session/access.go` (`sameActor` compares fences;
resolve at turn start), `actorscan_test.go` (new).

**Consumes:** lane 1 `Fenced`; lane 2 `Agent.FolderID/FolderMode`,
`ChatRecord.ScopedAgent`, `FolderResolver`.

**Produces:**

```go
// session
type Access struct{ /* unexported */ }
func NewAccess(a workspace.Actor, v workspace.View) (Access, error)
func (x Access) Actor() workspace.Actor
func (x Access) View() workspace.View
func (x Access) Place() Place      // lane 4 fills Place; lane 3 uses today's mapping behind it
func (x Access) Owner() workspace.Actor

// studio
var ErrAgentFolderMissing = errors.New("studio: the folder of this agent is missing")
func (s *Service) chatAccess(ctx context.Context, rec session.ChatRecord) (session.Access, error)
// for hooks outside the package (lanes 5 and 6)
func (s *Service) ChatAccess(ctx context.Context, principal, chatID string) (session.Access, error)
```

`chatActor` is deleted. `CreateAs` refuses an agent whose folder is missing
or unset (`ErrAgentFolderMissing`, mapped to 409 `agent_folder_missing` in
the endpoint).

**Failing specs first** (`agentaccess_test.go`, `Describe("chat access")`):

- `It("gives a chat without an agent folder today's actor and view")`: equal
  to the old `chatActor` result and `BuildView`.
- `It("fences a chat whose agent has a folder")`: roots are the folder at its
  mode, the chat folder at write, each shared folder at read, each skill
  layer at read.
- `It("creates the chat folder before it fences")`.
- `It("refuses a chat whose folder is deleted and never returns the home")`.
- `It("refuses a chat whose scoped agent was deleted")` (ScopedAgent set).
- `It("keeps today's rule for a deleted agent that had no folder")`.
- `It("refuses CreateAs with an unset folder")`.
- `It("applies an agent edit at the next turn")`: change the mode between
  two sends; the second turn's actor has the new mode and the sandbox was
  rebuilt (`syncAccess`).
- `It("applies an edit made through another service instance")`: two
  `Service` values on the same stores.
- `It("renames the chat folder and keeps it in the fence")`.
- `actorscan_test.go`: `It("builds no workspace actor outside the allowlist")`
  parses `core/services/studio`, `core/services/addons/hostops`,
  `core/services/studio/hostexec` and `core/services/studio/sandbox/runner`
  with `go/parser`, and fails on a `workspace.Actor{` composite literal,
  `files.UserActor(` or `.Files.Actor(` outside the allowlist named in the
  spec ("Callers that must pass the scope").

**Done when:** the studio package tests pass and the scan is green with the
allowlist reviewed.

## Lane 4: view, place and prompt

**Files:** `core/services/studio/session/layout.go` (`BuildView`,
`LayoutPrompt`, reserved `chat` name), `session/place.go` and
`place_test.go` (new), `files/tools.go` (`locate` and friends call `Place`;
`HomePath` removed), `files/search.go` (root from the place),
`media/media.go` (read closure through the place),
`workspace/workfiles/workfiles.go` (chat mount), `chatfiles.go`
(`guest_path`), `rulesnudge.go` (start folder), `workfolder.go` prompt.

**Consumes:** lane 1; lane 3's `Access` (use the stub signature until lane 3
merges).

**Produces:**

```go
func BuildView(ctx context.Context, st workspace.Store, a workspace.Actor, primary string, chat string, shared []SharedRoot, log *slog.Logger) (workspace.View, error)
const GuestChat = "chat"
type Place struct{ /* mounts */ }
func NewPlace(v workspace.View) Place
func (p Place) Resolve(path string) (Target, error)
func (p Place) Guest(abs string) (string, bool)
func (p Place) Primary() string
func LayoutPrompt(v workspace.View, scoped ScopedPrompt) string
func (s *files.Service) Tools(x session.Access) []session.Tool
func (s *media.Service) Tools(f *files.Service, x session.Access, chatModel string) []session.Tool
```

**Failing specs first:**

- `place_test.go`, `Describe("Place")`:
  `It("maps every home spelling to the primary mount")`,
  `It("maps /workspace/chat to the chat folder")`,
  `It("refuses parents, NUL and control characters wherever they point")`
  (table of the spec's traversal list),
  `It("takes encoded and look-alike separators as literal names")`
  (`..%2fx`, `%2e%2e/x`, `..\x`, U+2024 twice, U+FF0F),
  `It("refuses a /workspace path that is no mount, case-sensitively")`,
  `It("never resolves outside its mounts")` (property test over random
  segment lists: the result is always inside one mount).
- `files/tools_test.go`: `It("cannot leave the agent's folder")` (the spec's
  path table across all six file tools), `It("finds nothing outside the folder
  and counts no pending file outside it")` (more than `pendingCheck` pending
  entries outside), `It("keeps the unscoped behaviour")` (the existing specs,
  unchanged).
- `media`: `It("read_document resolves /Other/x.pdf inside the folder")`.
- `layout_test.go`: `It("mounts the folder at home and the chat folder at chat")`,
  `It("mounts shared folders read only for a scoped view")`,
  `It("renames a shared folder called chat")`,
  `It("tells a scoped chat only its folders and no page hint")`.
- `workfiles`: `It("treats /workspace/chat/work as the work folder")`.

**Done when:** files, session, media and workfiles tests pass; the existing
tool parity test (`tools_parity_test.go`) is green.

## Lane 5: command runners

**Files:** `core/services/studio/hostexec/hostexec.go`,
`core/services/studio/sandbox/runner/runner.go`,
`core/application/studio_service.go` (hooks), `builtins` selection in
`core/services/studio` (no host bash for a scoped chat).

**Consumes:** `Service.ChatAccess` (lane 3), `BuildView` (lane 4).

**Produces:**

```go
// hostexec.Options and runner.Options
Actor func(ctx context.Context, user, chat string) (workspace.Actor, error)
Mounts func(ctx context.Context, a workspace.Actor) ([]workspace.MountRequest, error) // from the chat's view
```

**Failing specs first:**

- `hostexec_test.go`: `It("mounts only the fenced view of a scoped chat")`,
  `It("fails the call when the resolver fails")`.
- `runner_test.go`: `It("creates the sandbox with the chat's view")`,
  `It("cannot commit outside the fence")`.
- studio: `It("does not offer the built-in host bash to a scoped chat")`.

## Lane 6: add-on host operations and the chat binding

**Files:** `core/services/addons/hostops/inflight.go`, `broker.go`,
`engine_workspace.go` and tests; `core/services/addons/runtime/client.go`
(begin with the binding); `core/services/addons/gateway/forwarding.go`
(signed `Chat` field on `ForwardedRequest`); `core/services/addons/tools/adapter.go`
(`ChatCall`); `core/application/addon_gateway.go` (hook).

**Consumes:** `Service.ChatAccess` (lane 3), `Place` (lane 4).

**Produces:**

```go
// hostops
type Binding struct { Principal, ChatID string }
func (f *InFlight) BeginBound(env contract.Envelope, b Binding) (end func())
// Call
ChatID string
// WorkspaceHost
Access func(ctx context.Context, principal, chatID string) (session.Access, error)
// tools adapter
type ChatCall struct{ /* unexported: conversation, principal */ }
func NewChatCall(principal, conversation string) ChatCall
```

**Failing specs first** (`engine_workspace_test.go`,
`Describe("workspace operations of a scoped chat")`):

- `It("materializes / as the folder and adds the chat folder")`.
- `It("refuses export and import outside the folder")` (`/../x`, `/Other/x`,
  `/users/<p>/x`).
- `It("lists no change outside the folder")`.
- `It("reports a commit outside the view as a conflict")`.
- `It("binds a read call that has no resource ID")`.
- `It("stays user-wide for a call with no binding")`.
- `It("sends the add-on an envelope without a new field")`: marshal the
  envelope and decode it with `contract.Envelope.UnmarshalJSON`.
- `forwarding_test.go`: `It("carries the chat binding signed and rejects a changed one")`.

## Lane 7: triggers and connectors

**Files:** `core/services/studio/triggersvc.go`, `triggerscrud.go`,
`triggertools.go`, `triggers/rules.go` (actor parameter), `connectorfiles.go`,
`connectorsvc.go` (missing-folder reply).

**Consumes:** lane 3.

**Failing specs first:**

- `triggerscrud_test.go`: `It("refuses a file trigger outside the agent's folder")`
  at create and at update.
- `triggersvc_test.go`: `It("skips and switches off a run outside the folder")`,
  `It("skips and switches off a run whose agent folder is missing, without reading the home")`,
  `It("reads the rules from the agent's folder for a non-file trigger")`.
- `connectors_test.go`: `It("does not send a file outside the folder")`,
  `It("answers once that the agent cannot work when its folder is missing")`.

## Lane 8: sub-agents and list_agents

**Files:** `core/services/studio/subagent.go`, `savedagents.go`,
`agentaccess.go` (parent walk, if not done in lane 3).

**Consumes:** lane 3.

**Failing specs first** (`subagent_test.go`):

- `It("confines a generic sub-agent of a scoped chat, tools and sandbox view")`.
- `It("refuses a saved-agent child whose folder is outside the parent's")`.
- `It("gives a saved-agent child inside the parent's folder the narrower folder and the lower mode")`.
- `It("confines a child without a folder to the parent's folder")`.
- `savedagents_test.go`: `It("lists each agent's folder name and mode")`.

## Lane 9: web interface

**Files:** `core/http/react-ui/src/components/files/FolderPicker.jsx`
(new), `MoveDialog.jsx` (uses it), `pages/AgentCreate.jsx` (section
**Files**), `utils/chatAgents.js` (form to payload: `folder` only when
changed, `{path: ""}` to clear), `pages/Agents.jsx`, `pages/AgentPage.jsx`
(chips), `pages/Workspace.jsx` (marker), `pages/Chat.jsx` (header chip,
missing-folder error with **Edit agent**), the agents import result, the
locale files (every language), CSS with design tokens only; e2e
`e2e/agents-edit.spec.js`, `e2e/agents.spec.js`, a render-smoke spec for the
picker; `node:test` unit tests for `chatAgents.js`.

**Consumes:** lane 2 API.

**Tests first:**

- `chatAgents` unit: `folder omitted when unchanged`, `empty path clears`,
  `mode read maps to "read"`.
- e2e: `choose a folder and save shows it on the card`,
  `read only shows the lock chip`,
  `a missing folder shows the inline error and the warning chip`,
  `the Files page marks the agent's folder`,
  `starting a chat with a missing folder offers Edit agent`,
  `the picker cannot choose Chats or LocalAI`.
- No copy names a guest path, a node or a mount (review check plus a grep in
  the e2e spec over the rendered text for `/workspace/` and `node`).

## Lane 10: docs, security suite, release note

**Files:** `docs/content/features/chats.md`, `docs/content/getting-started/first-agent.md`,
`docs/content/features/addons.md`, release notes draft;
`core/services/studio/agentscope_security_test.go` (new).

**Specs:** one end-to-end Ginkgo suite on the studio test harness
(`studiotest`) that runs a scoped chat through every surface of the spec's
"Security tests" (file tools, media, search, a command through a fake
sandbox, an add-on read and action call, a generic and a saved sub-agent, a
file trigger run, a connector reply, a deleted folder) and asserts that no
result, card, prompt or count names a file outside the fence. Seed one file
outside the fence with a unique marker string and assert the marker appears
in no transcript, tool result or event.

**Done when:** docs state the distributed mode (stateless per request), the
host bash rule, the export version 2, and the downgrade caveat.
