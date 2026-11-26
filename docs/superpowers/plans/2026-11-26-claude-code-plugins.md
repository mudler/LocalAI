# Plugins compatible with the Claude Code plugin format: implementation plan

> **For agentic workers:** use superpowers:subagent-driven-development or
> superpowers:executing-plans. Each lane below is one agent task in its own
> worktree. Write the failing Ginkgo specs first, see them fail, then make
> them pass.

**Spec:** `docs/superpowers/specs/2026-11-26-claude-code-plugins-design.md`.
Read it before any lane. This plan gives files, interfaces, tests and
estimates, not function bodies. Section numbers (for example "spec 9.3") point
into the spec.

**Goal:** LocalAI installs plugins in the Claude Code plugin format from
marketplaces, pinned by SHA, and maps their skills, commands, agents, MCP
servers and hooks onto Studio. Plugin code runs only in the Sandbox.

## Global constraints

Every lane carries these:

- Read `AGENTS.md` and the `.agents/*.md` files it names for the area of the
  lane. The repo rules win over this plan.
- Tests are Ginkgo (`Describe`/`It`) in the package's existing suite. New
  packages get a suite file.
- Both stores (`GormStore`, `FileStore`) pass one shared suite.
- No new in-memory state across requests except the per-instance `Model`
  cache of spec 7.4. Each lane states the mode of any state it adds.
- User-facing changes update `docs/content/` in the same commit
  (docs-with-code rule).
- New API routes follow the checklist of `.agents/api-endpoints-and-auth.md`.
- New stores ask `privacy.Allows` (`core/privacy`).
- UI uses the kit only; run `npm run lint:ui-kit`.
- Wording: "compatible with the Claude Code plugin format". No Anthropic marks.
- No private data in code, comments, commits or PR text: no hostnames, IPs,
  local paths or internal names.
- Commit trailer `Assisted-by: AGENT_NAME:MODEL_VERSION`; no `Signed-off-by`
  and no `Co-Authored-By` from an AI.
- Coverage only goes up.
- Worktrees and scratch folders stay outside `/tmp`. Do not use `git stash` in
  shared worktrees; make work-in-progress commits instead.

Estimates are agent-days (d) for one agent, tests included.

## nib dependencies

The nib adapter (P0a) is on a branch and not released. These lanes cannot
merge before a nib version that has what they use. They can start against the
branch with a `replace` directive in a local worktree, but the merge needs the
released version in `go.mod`.

| nib item (spec 21) | Lanes that need it |
|---|---|
| N1: `plugin/claudeplugin` committed and tagged | B3, B5, C1, C2, D3, and every later lane |
| N2: `ResolveRef` | B4 (update checks); B5 can pin through `Fetch` alone |
| N3: hook runner with an executor | F2 to merge against nib; F2 can ship its local copy first |
| N4: stable `Skipped` reason codes | D4 (translated reasons); without it D4 shows the raw text |
| N6: `CodeFiles()` | none; B2 computes it locally |
| N10: partial clone filter in `checkout` | none; B3's `GitFunc` adds `--filter=blob:none` |

Lane N0 (in the nib repo, not this one) closes N1, N2 and N4 before P0b
merges, and N3 before P2 merges.

## Phase overview

| Phase | Outcome | Lanes | Estimate |
|---|---|---|---|
| P0a | nib adapter (in progress, nib repo) | N0 | 3 d |
| P0b | Plugins service: marketplaces, SHA-pinned installs, shared store, DB, distributed | B1 to B6 | 6 d |
| P0c | Studio: plugin skill layer, commands, http MCP per user, tool map, toggles | C1 to C5 | 4.5 d |
| P0d | REST, MCP tool, CLI, UI, docs, curated marketplace, end to end | D1 to D7 | 8.25 d |
| P1 | Plugin agents, model tiers, userConfig and sealed secrets, shell injections and `@file` | E1 to E5 | 6 d |
| P2 | Hooks in the Sandbox under the hook contract, `CLAUDE_PLUGIN_DATA` | F1 to F6 | 10 d |
| P3 | stdio MCP in the Sandbox, `bin/`, runtimes, output styles | G1 to G4 | 5.5 d |

The P0 total (6 + 4.5 + 8.25 = 18.75 d) is above the scope estimate of 11 d for
P0b to P0d. The difference is the API and UI surfaces that the repo rules ask
for (instructions, route registry, MCP parity, i18n, render-smoke specs) and
the two-frontend tests, and the curated marketplace with its CI in this repository.

---

## P0b: Plugins service

### Lane map

| Lane | Title | Size | Depends on | Parallel with | nib |
|---|---|---|---|---|---|
| B1 | Store and schema | 1 d | none | B2, B3, C5 | no |
| B2 | Content store, tree checks, digest, GC | 1.25 d | none | B1, B3, C5 | types only |
| B3 | Hardened fetcher | 1 d | none | B1, B2, C5 | N1 |
| B4 | Marketplaces and refresh leader | 1 d | B1, B3 | B2 | N1, N2 |
| B5 | Install, update, rollback, remove | 1.5 d | B1, B2, B3, B4 | C1 to C5 on stubs | N1 |
| B6 | Application wiring, flags, settings | 0.5 d | B5 | C1 to C5 | no |

Order: B1, B2, B3 and C5 in parallel; then B4; then B5; then B6. One merge
agent merges in that order and runs `go test ./core/services/plugins/...` after
each merge.

### B1: store and schema

**Files:** new `core/services/plugins/store/{types.go,store.go,gorm.go,file.go}`,
`core/services/plugins/store/storetest/suite.go`, suite files.

**Do:** the records of spec 6.1 to 6.5 and the `Store` interface:

```go
type Store interface {
    Marketplaces(ctx) ([]Marketplace, error)
    SaveMarketplace(ctx, Marketplace) error        // tombstone for a default
    Snapshot(ctx, id string) (Snapshot, error)
    SaveSnapshot(ctx, Snapshot) error
    Package(ctx, digest string) (Package, error)
    PutPackage(ctx, Package) error                 // insert once, no error on conflict
    UnreferencedPackages(ctx, olderThan time.Time) ([]string, error)
    Installs(ctx, f InstallFilter) ([]Install, error)
    InstallTx(ctx, fn func(Tx) error) error        // package + install in one transaction
    UserState(ctx, principal, installID string) (UserState, error)
    SaveUserState(ctx, UserState) error
    MarkNeedsReenable(ctx, installID string) error
}
```

`AutoMigrate` under `advisorylock.KeySchemaMigrate`. The file store writes one
JSON file atomically, as `catalog.Sources.persist` does.

**Tests (shared suite, both stores):** unique (`plugin_id`, `scope`, `owner`);
an instance install blocks a user install of the same plugin ID; a removed
default stays removed after `SaveMarketplace` of the same default;
`PutPackage` twice is fine; `UnreferencedPackages` skips referenced and young
packages; `MarkNeedsReenable` sets every user row of the install.

### B2: content store, tree checks, digest, GC

**Files:** new `core/services/plugins/content/{tree.go,digest.go,store.go,codefiles.go,gc.go}`;
`core/services/advisorylock/keys.go` (add `KeyPluginsRefresh`, the next free
number). B2 does not touch `files/layers.go`; it takes the root path as an
argument. C1 adds the constant.

**Do:**

- `Check(dir string, lim Limits) (Tree, error)`: spec 15.3. `Tree` lists path,
  size, mode and SHA-256.
- `Digest(Tree) string`: spec 7.2.
- `CodeFiles(t Tree, m *claudeplugin.Plugin) []string`: spec 9.3. Replace it
  with nib's `CodeFiles` when N6 lands.
- `Write(ctx, ws Store, root string, digest string, dir string, t Tree) error`:
  staging folder, hash check, move, spec 7.3.
- `Open(ctx, ws, root, digest, path)`: read one file.
- `GC(ctx, db, ws, store)`: under `KeyPluginsRefresh`, deletes unreferenced
  packages older than 24 hours.

**Tests:** fixtures for traversal, absolute path, backslash, NUL, device, a
symlink out of the tree (refused) and in it (stored as a file), case
collision, each size and count limit, depth. Digest stable across file order
and equal for equal trees. Two concurrent `Write` calls of one digest leave one
folder. GC keeps referenced and young packages. Each code file rule of spec
9.3.

### B3: hardened fetcher

**Files:** new `core/services/plugins/fetch/{git.go,fetch.go,http.go}`.

**Do:**

- `Git() claudeplugin.GitFunc`: the options of spec 15.2, including
  `--filter=blob:none` on every `fetch` until nib closes N10.
- `FetchPlugin(ctx, mkt *claudeplugin.Marketplace, e *claudeplugin.Entry, sha string, scratch string) (*claudeplugin.FetchResult, error)`.
- `FetchMarketplace(ctx, kind, location, ref string, scratch string) (body []byte, sha string, err error)`.
- `ResolveRef(ctx, gitURL, ref string) (string, error)`: through nib's
  `ResolveRef` when N2 lands; until then `git ls-remote` through `Git()`.
- Scratch folders under `<data path>/plugins/scratch/`, removed on return and
  at startup.

**Tests:** a local git fixture (a test option allows `file://`): pin by SHA,
a moved ref resolves to the new SHA, `git-subdir` checks out only the subfolder
and the object store holds no blob from outside it (partial clone),
a repository with a `post-checkout` hook and a `.gitattributes` filter that must
not run, a repository over the size cap fails and leaves no scratch folder,
`ssh://` and `http://` refused, the HTTP fetch of a JSON URL with a redirect to
`http://` refused.

### B4: marketplaces and refresh leader

**Files:** new `core/services/plugins/marketplaces.go`, `refresh.go`.

**Do:** add, change, remove (tombstone for a default), list; seed defaults at
first start, with the curated `url` marketplace from
`CuratedMarketplaceURL(internal.Version)` (spec 8.3: the `v<semver>` tag for a
release build, master otherwise, as for the official add-on library);
change detection of a `url` marketplace by `body_sha256`; the "official libraries" switch suspends defaults; refresh one
marketplace (spec 8.1: parse with `ParseMarketplace`, set `Root` and `SHA`,
keep the old snapshot on failure); the refresh loop every 6 hours under
`advisorylock.TryWithLockCtx(KeyPluginsRefresh)`, which also marks installs
with `update_available` (spec 9.4) and runs B2's GC.

**Tests:** `CuratedMarketplaceURL` gives the tag for `v1.2.3` and master for an
empty version and for `v1.2.3-14-gabc1234`; seeding only on a fresh store; tombstone survives a restart; the
official switch suspends and restores; a failed refresh keeps the old snapshot
and sets `last_error`; entries with npm, archive and command sources are
listed with `supported: false`; two instances on `testutil.NewFakeBus()` and one
database refresh once per tick.

### B5: install, update, rollback, remove

**Files:** new `core/services/plugins/{service.go,inspect.go,install.go,update.go,classify.go,model.go,tokens.go}`;
new `core/services/galleryop/plugins.go` (`PluginOp`, `EnqueuePluginOp`, key
`plugin:<plugin-id>`); one line in `galleryop` status types for `isPlugin`.

**Do:**

- `Inspect(ctx, actor, pluginID)`: spec 9.3 steps 1 and 2. Returns `Preview`
  and a token. Tokens are rows in the store with a 15 minute expiry (stateless
  per request; not in memory).
- `Install(ctx, actor, token, scope)`: spec 9.2 rights, then
  `EnqueuePluginOp`. The job takes `HeldLock` on
  `KeyFromString("plugin-change:...")`, fetches again, refuses a digest
  mismatch, writes the package, then the rows in one transaction.
- `UpdatePreview`, `Update`, `Rollback`, `Remove`: spec 9.4 and 9.5,
  including `needs_reenable` and the `blocked` state.
- `classify.go`: code reasons (spec 9.1), reserved plugin names (spec 10.1).
- `model.go`: `Model` from `claudeplugin.Plugin` (spec 6.8).
- `TurnView(ctx, principal, rec ChatRef) ([]ActivePlugin, error)`: the plugins
  that contribute to a turn (spec 10.1), read from the store. C1 to C4 consume
  this interface. Publish the interface on day one so C lanes can stub it.

**Tests:** the permission matrices of spec 9.2 and 9.4, one `It` per row; a
user install of a code plugin answers `ErrNeedsAdmin`; a code install through
an API key answers `ErrNotHumanSurface`; digest mismatch between inspect and
install fails the job and writes nothing; update of a user install that gains
code ends `blocked` and keeps the old digest; an instance update that gains code
sets `needs_reenable` on every user; rollback needs the old package; a
cancelled queued install writes nothing; two instances installing one plugin ID
at once give one install.

### B6: application wiring, flags, settings

**Files:** new `core/application/plugins.go`; `core/application/application.go`
(construct the service); `core/config/runtime_settings.go` and
`runtime_settings_registry.go` (keys of spec 6.7); `core/cli/run.go` (flag
`--plugin-marketplaces`, environment `LOCALAI_PLUGIN_MARKETPLACES`);
`docs/content/features/plugins.md` (first version: what exists, the
distributed mode table).

**Tests:** settings defaults and bounds (`plugins_hook_timeout_seconds` at most
600); the flag seeds defaults only on a fresh store; the service starts with
the file store when no database is set.

---

## P0c: Studio

### Lane map

| Lane | Title | Size | Depends on | Parallel with | nib |
|---|---|---|---|---|---|
| C5 | Tool map package | 0.5 d | none | B1 to B3 | types only |
| C1 | Plugin skill layer | 1 d | B5 `TurnView` interface | C2 stubbed, C3, C4 | N1 |
| C3 | Per-user enable, per-chat and per-agent switches | 1 d | B1 | C1, C4 | no |
| C2 | Plugin commands | 1 d | C1 | C3, C4 | N1 |
| C4 | HTTP MCP servers as plugin connectors | 1 d | B5 `TurnView` interface, C3 | C1, C2 | N1 |

C1, C3 and C4 can run at the same time: they touch different files (listed per
lane). C2 starts after C1 merges, because both touch the skill resolution of a
turn.

### C5: tool map

**Files:** new `core/services/plugins/toolmap/{toolmap.go,args.go}`.

**Do:** spec 11.1 and 11.2: `ToLocal(name string, input json.RawMessage) ([]Call, error)`
(a `MultiEdit` gives several calls), `ToFormat(name string, args json.RawMessage) (string, json.RawMessage)`,
`LocalNames(rules []string) (mapped, dropped []string)` for agent tool lists,
`FormatMCPName(plugin, server, tool string) string`.

**Tests:** one `It` per table row of spec 11.1 and 11.2; round trip of each
mapped tool; an `updatedInput` with an unmapped field fails; `mcp__` names pass.

### C1: plugin skill layer

**Files:** `core/services/studio/skills/{resolve.go,parse.go,index.go}`
(`LayerPlugin`, rank, `Root.Plugin`, `Root.PluginTitle`, name rule);
`core/services/studio/files/layers.go` (`PluginsPath`, guest path);
`core/services/studio/session/layout.go` (fourth `SkillLayers` mount);
`core/services/studio/loadskill.go` (expansion with `claudeplugin.ExpandContent`);
new `core/services/studio/pluginskills.go` (roots from `TurnView`, `RefreshKey`
input); `docs/content/features/chats-and-agents/chats-skills.md`.

**Do:** spec 10.1.

**Tests:** precedence (shared beats plugin, plugin beats add-on); a qualified
name `p:s` is valid only in the plugin layer; switch ID `plugin:p:s` turns off
one skill; `PluginsOff` removes every skill of the plugin; `load_skill`
expands `${CLAUDE_PLUGIN_ROOT}` and `${CLAUDE_SKILL_DIR}` to guest paths and
hides sensitive options; `allowed-tools: Bash` gives `requires: [exec]`;
`disable-model-invocation` keeps the skill out of the index; `RefreshKey`
changes when a plugin is enabled and not when a skill is switched off; a chat
with an agent sees only granted plugins.

### C3: per-user enable, per-chat and per-agent switches

**Files:** `core/services/studio/session/store.go` and its stores
(`PluginsOff`); `core/services/studio/agents/{types.go,gorm.go,json.go}`
(`Plugins`); new `core/services/studio/pluginsvc.go` (`SetPluginsOff`, agent
grant checks); `core/services/studio/agents/agentstest` suite;
`core/services/plugins/userstate.go` (enable, disable, `needs_reenable`).

**Do:** spec 6.5, 6.6, 12.1, 12.2. `SetPluginsOff` caps the list at 200 as
`SetSkillsOff` does. Agent export includes `Plugins`.

**Tests:** both chat stores and both agent stores keep the new fields; a chat
created before a plugin was enabled sees it; an agent with an empty `Plugins`
list sees none; a user cannot switch on an install that is `needs_reenable`
without the explicit re-enable call; export and import of an agent keep
`Plugins`.

### C2: plugin commands

**Files:** `core/services/studio/chatcommands.go` (`Command.Args`, plugin
command directive); `core/http/endpoints/studio/studio.go` (message field
mapping); `core/services/studio/session` (`Invocation` kind `command`);
`docs/content/features/chats-and-agents/chats-skills.md`.

**Do:** spec 10.2, without shell injections and `@file` (E4). A command body
with a shell injection answers "This command needs a later version of
LocalAI" until E4.

**Tests:** `$ARGUMENTS`, `$ARGUMENTS[1]`, `$0`, `$name`, `\$`, appended
`ARGUMENTS:` line; a body with `{{` stays literal; the transcript keeps the
typed text and the chip; an unknown command answers 400 with the names; a
command with `user-invocable: false` is refused from `/`; a tier `model`
applies to the turn only.

### C4: HTTP MCP servers as plugin connectors

**Files:** `core/services/studio/mcpconns/{types.go,gorm.go,json.go,service.go}`
(`plugin_install_id`, `plugin_server`, read-only rule);
`core/services/studio/mcpconnectors.go` (plugin connectors in a chat, agent
grant through `Agent.Plugins`); new `core/services/plugins/connectors.go`
(sync on enable, disable, remove, update);
`docs/content/features/chats-and-agents/mcp.md`.

**Do:** spec 10.4 for `http` and `sse`. Servers that need `userConfig` are
skipped with "needs settings" until E3. `ws` is skipped.

**Tests:** enable creates one connector per server; disable and remove delete
it; an update that changes the URL updates the connector; a user cannot edit a
plugin connector; `${VAR}` resolves only from the plugin's variables, never the
process environment; a chat with an agent gets the connectors only with the
plugin granted; unattended runs gate them with `act.spend`.

---

## P0d: surfaces

### Lane map

| Lane | Title | Size | Depends on | Parallel with | nib |
|---|---|---|---|---|---|
| D1 | REST API, auth, swagger, instructions | 1.5 d | B5, C3 | C1, C2, C4 | no |
| D2 | MCP tool `plugins` | 0.5 d | D1 | D3, D4, D5 | no |
| D3 | CLI `local-ai plugins` | 1 d | D1 (API), B2, B3 (`validate`) | D2, D4, D5 | N1 |
| D4 | UI: Plugins tab and dialogs | 2 d | D1 contract (can mock) | D2, D3, D5 | N4 (soft) |
| D5 | UI: chat surfaces | 0.75 d | C2, C3, D1 | D2, D3, D4 | no |
| D6 | Docs page, end-to-end and privacy tests | 1 d | all of P0 | D7 | no |
| D7 | Curated marketplace in `plugins/`, `localai` plugin, CI | 1.5 d | D3 `validate`, B3 | D6 | N1 |

D4 and D5 touch different UI files (`components/plugins/` and the Add-ons page
for D4; `components/chat/` and `utils/slashCommands.js` for D5). D4 can start
on the day D1 publishes its request and answer types.

### D1: REST API

**Files:** new `core/http/endpoints/plugins/*.go`; new
`core/http/routes/plugins.go`; `core/http/app.go` (register);
`core/http/auth/permissions.go` and `features.go` (`FeaturePlugins`, metadata,
`RouteFeatureRegistry` rows); `isAPIPath()` for `/api/plugins`;
`core/http/endpoints/localai/api_instructions.go` and its test (new `plugins`
entry, count); `core/http/endpoints/studio/` (`PUT /api/chats/{id}/plugins`);
swagger (`make swagger`); `docs/content/features/plugins.md` API section.

**Do:** spec 16 routes. Code plugin install and approval need the
administrator's browser session (`X-LocalAI-Confirm`), as add-on host grants
do. `GET /api/operations` rows get `isPlugin`.

**Tests:** each route with and without auth, user and admin; code install
through an API key answers `403 not_human_surface`; a user install of a code
plugin answers 403 with the reason; feature off answers 403; instructions count
updated.

### D2: MCP tool

**Files:** `core/services/localaitools/{tools.go,client.go}`, `inproc/`,
`httpapi/` and `httpapi/routes.go`; `coverage_test.go` (`toolToHTTPRoute`,
`adminRoutesWithoutTool` rows for the code-plugin routes with their reason);
`server_test.go` (`expectedFullCatalog`); the admin skill prompt per
`.agents/localai-assistant-mcp.md`.

**Do:** spec 16, "MCP parity": one `plugins` tool with an `action` parameter, a
flat schema, per-action checks.

**Tests:** the route map spec passes; `install` of a code plugin through MCP
answers the browser-session sentence; each action against the inproc client.

### D3: CLI

**Files:** new `core/cli/plugins.go`, `core/cli/plugins_validate.go`;
`core/cli/cli.go` (register); `core/cli/chat/chat_cmd.go` help text that names
`local-ai plugins`; docs CLI reference.

**Do:** spec 17. `validate` uses B2 and nib only, no server. `validate
--marketplace <file>` fetches and checks every entry.

**Tests:** each subcommand against an `httptest` server; `install` of a code
plugin prints the review URL and exits 0; `validate` exits 1 on each tree check
failure and 0 on the fixture plugin.

### D4: UI, Plugins tab

**Files:** `core/http/react-ui/src/components/addons/addonState.js` (`TABS`);
`src/pages/Addons.jsx` (the panel); new
`src/components/plugins/{PluginsView.jsx,MarketplacesView.jsx,BrowseView.jsx,InstalledView.jsx,InstallReview.jsx,UpdateReview.jsx,PluginDetails.jsx,pluginsClient.js}`;
`public/locales/*/addons.json` (every locale); render-smoke specs.

**Do:** spec 18.1. The settings form of `userConfig` is a placeholder until E5.

**Tests:** component tests of each view with a mocked client; a user sees
**Install** only on text-only cards; the update review asks a tick for each
changed code file when the preview says so; render-smoke specs keep coverage.

### D5: UI, chat surfaces

**Files:** `src/components/chat/SkillsMenu.jsx`, `src/utils/chatSkills.js`,
`src/utils/slashCommands.js`, `src/components/chat/ChatComposer.jsx`,
`src/components/home/HomeComposer.jsx`, the chat Tools menu component, the
agent editor's grant lists (`src/pages/AgentCreate.jsx`), locales.

**Do:** spec 18.2 without agent pickers (E5) and hook activity (F5).

**Tests:** plugin skills grouped by plugin with one switch each; `/p:cmd`
completes with its `argument-hint`; the **Plugins** grant list saves
`Agent.Plugins`.

### D6: docs page, end-to-end and privacy tests

**Files:** `docs/content/features/plugins.md` (full page, spec 19);
`docs/content/addons/manage-addons.md`, `user-guide.md`; new
`tests/e2e/plugins_test.go`; `tests/e2e/incognito_test.go` (plugin stores in
the scan).

**Tests:** spec 20 end to end with a fixture marketplace: add, browse, install,
use a skill and a command, update, remove. The incognito scan covers the plugin
tables.

### D7: curated marketplace in this repository

**Files:** new `plugins/.claude-plugin/marketplace.json`,
`plugins/marketplace.schema.json`, `plugins/README.md` (the entry rules of
spec 8.3), `plugins/localai/` (`.claude-plugin/plugin.json`,
`skills/{api,models,addons}/SKILL.md`, `commands/install-model.md`); new
`.github/workflows/plugins-marketplace.yml`; new bot workflow
`.github/workflows/plugins-bump-localai.yml`; `core/services/plugins/curated.go`
(`CuratedMarketplaceURL`, if B4 did not add it);
`docs/content/features/plugins.md` (the curated list and how to propose an
entry).

**Do:**

- The P0 list of spec 8.4 as `git-subdir` or `github` entries with full SHAs
  read on the day of the pull request, and `metadata.localai` (`license`,
  `runsCode`, `phase`, `reviewed`). No relative sources: a `url` marketplace
  cannot use them.
- The `localai` plugin of spec 8.4, each skill under 8 KB, linking to the docs
  pages instead of copying them.
- The CI workflow of spec 8.3 on pull requests with `paths: plugins/**`:
  schema, pins, resolve at each SHA with the partial, sparse clone, license
  file against `metadata.localai.license`, `local-ai plugins validate`,
  `runsCode` against the computed code reasons. It reuses the build cache of
  the existing Go workflows (`.agents/ci-caching.md`).
- The bot workflow on pushes to master that change `plugins/localai/`: it
  opens a pull request that sets the `localai` entry's `sha` to the merge
  commit, with the GitHub App token of `.agents/ci-bot-pull-requests.md`.
- Before each P0 entry goes in, a trial with a local model in a chat: load
  each skill, run each command. Record the result in the pull request.

**Tests:** the workflow fails on a fixture marketplace with a missing `sha`, a
relative source, a wrong SPDX ID, and a plugin with code marked
`runsCode: false`; it passes on the real file. The pinned-SHA check warns on a
pull request that changes `plugins/localai/` and fails on any other pull
request with a mismatch. The end-to-end test of D6 installs `localai` from a
fixture copy of the file.

---

## P1: agents, model tiers, userConfig, shell injections

### Lane map

| Lane | Title | Size | Depends on | Parallel with | nib |
|---|---|---|---|---|---|
| E2 | Model tiers | 0.5 d | B6 | E1, E3, E4 | N1 |
| E1 | Plugin agents as read-only templates | 1.5 d | C1, C3, C5, E2 interface | E3, E4 | N1 |
| E3 | userConfig and sealed secrets | 1.5 d | C4 | E1, E2, E4 | N1 |
| E4 | Shell injections and `@file` | 1.5 d | C2 | E1, E2, E3 | N1 |
| E5 | UI for P1 | 1 d | E1, E2, E3, E4 | none | no |

E1 to E4 touch different files. E1 calls `TierModel(ctx, tier, chatModel)` from
E2; agree the signature on day one.

### E2: model tiers

**Files:** new `core/services/plugins/tiers.go`; the runtime settings
registry entry for `plugins_model_tiers` (validation: known tier keys, model
names that exist or aliases).

**Tests:** each tier maps when set and the model exists; an unmapped tier, an
unknown value, `inherit` and a missing model give the chat's model.

### E1: plugin agents

**Files:** `core/services/studio/savedagents.go` (`resolveAgent` also looks in
the turn view); `core/services/studio/subagent.go` (tools, model, skills,
`maxTurns` for a plugin agent); `list_agents`; `core/http/endpoints/studio/`
agent list (`source: "plugin"`, read only); new copy endpoint
`POST /api/chats/agents/from-plugin`; docs `chats-sub-agents.md`.

**Do:** spec 10.3.

**Tests:** `spawn_agent` with a plugin agent by ID and by name; `tools` maps
to `AllowedTools` and drops unmapped names; empty `tools` inherits; the tier
model applies; agent `skills` limit the child's plugin skills; ignored fields
have no effect; a plugin agent cannot be edited; the copy is an ordinary saved
agent; saved agents off in the chat refuses it.

### E3: userConfig and sealed secrets

**Files:** `core/services/plugins/userconfig.go`; store `secrets` column use;
`core/services/plugins/connectors.go` (servers that need settings now
connect); the `/me` routes of D1 accept `config` and `secrets`; docs.

**Do:** spec 12.3 and 12.4.

**Tests:** validation per option type; required values missing skip the
components that use them; sensitive values never come back from the API, a
log line or a skill body; the sealed value opens only with its AAD; a header
template with a sensitive value reaches the connector's sealed headers.

### E4: shell injections and `@file`

**Files:** `core/services/studio/chatcommands.go`; new
`core/services/studio/plugininject.go` (Sandbox run, consent card, file
inline); `core/services/studio/loadskill.go` (injections in skill bodies);
docs.

**Do:** spec 10.2. Injections run through the Sandbox `bash` call of the
chat, never the host runner. Consent once per (user, plugin digest), stored as
a chat-independent row in `plugin_user_state` (add a `consented_digest`
column).

**Tests:** no Sandbox: the command answers that it needs one and runs nothing;
host bash allowed and no Sandbox: still nothing runs; first run asks consent,
second does not, a new digest asks again; `@file` reads under
`CLAUDE_PROJECT_DIR` only, refuses `..`, caps at 64 KiB; an incognito chat
stores no consent row.

### E5: UI for P1

**Files:** `src/components/chat/AgentPicker.jsx`, `SubAgentsMenu.jsx`,
`src/components/plugins/SettingsForm.jsx`, the runtime settings page (tier
pickers), the consent card component, locales.

**Tests:** plugin agents grouped with "Read only" and their model; the
settings form shows "Set" for sensitive values and never the value; tier
pickers save the setting.

---

## P2: hooks in the Sandbox

### Lane map

| Lane | Title | Size | Depends on | Parallel with | nib |
|---|---|---|---|---|---|
| F1 | Sandbox `exec` operation | 2 d | none | F2 | no |
| F2 | Hook engine and contract | 2 d | C5 | F1 | N3 (to switch to nib's code) |
| F3 | Tool seam: Pre and PostToolUse | 1.5 d | F2 interface | F4 | no |
| F4 | Turn seams and `CLAUDE_PLUGIN_DATA` | 1.5 d | F2 interface | F3 | no |
| F5 | UI, settings, docs | 1 d | F3, F4 | none | no |
| F6 | nib hook runner (nib repository) | 2 d | N1 | F1 to F4 | provides N3 |

F1 and F2 start together. F3 and F4 start on F2's `Engine` interface, agreed on
day one, and use a fake executor until F1 merges.

### F1: Sandbox `exec` operation

**Files:** `pkg/addons/contract` (the `exec` field of `executionProvider`),
the Sandbox add-on under `addons/sandbox/` (handler, limits), its manifest,
`check-tools.sh` if a tool is new, `.agents/sandbox-profiles-and-media.md` if
the rules change, `docs/content/addons/catalogue/work/sandbox-settings.md`.

**Do:** spec 13.4 `exec`. A page-only tool, never in the model's list.

**Tests:** exit code, separate stdout and stderr, caps, stdin, env, `dir`,
timeout with `timedOut: true`; an older host refuses the new manifest field
(contract test); the model's tool list does not contain it.

### F2: hook engine

**Files:** new `core/services/studio/pluginhooks/{engine.go,input.go,output.go,combine.go,executor.go}`
and `conformance_test.go`.

**Do:**

```go
type Executor interface {
    Run(ctx context.Context, c Command) (Result, error) // Command: argv or shell, env, dir, stdin, timeout
}
type Engine interface {
    Fire(ctx context.Context, ev Event, plugins []ActivePlugin, in Input) (Outcome, error)
}
```

Spec 13.2: events, input, output, exit codes, combine, timeouts, matchers with
`claudeplugin.MatchHook`, parallel handlers. When nib's N3 lands, `input.go`,
`output.go` and `combine.go` become thin calls to nib and the conformance table
runs against nib.

**Tests:** the conformance table of spec 20, one `It` per row.

### F3: tool seam

**Files:** new `core/services/studio/pluginhooks.go` (the `session.Tool.Run`
wrapper); `core/services/studio/subagent.go` (`chatToolsAll`: apply after the
act policy wrappers); the consent card for `ask` (reuse the host-exec consent
pattern); `docs/content/features/plugins.md` hooks section.

**Do:** spec 13.2 "Seam" and "Decisions", with C5's map for `tool_name`,
`tool_input`, `tool_response` and `updatedInput`.

**Tests:** a call refused by the act policy runs no hook; `deny` returns the
sentence and records the outcome; `allow` does not skip a consent card;
`ask` shows a card in a watched chat and denies in a trigger run;
`updatedInput` maps back, and an unmapped field fails the call;
`PostToolUseFailure` fires on a tool error; `MultiEdit`-shaped edits show as
one event.

### F4: turn seams and `CLAUDE_PLUGIN_DATA`

**Files:** the Studio turn code (`core/services/studio/session/chat.go` and the
service's turn start and end); compaction and `/clear` paths; chat delete and
Sandbox end; new `core/services/plugins/datadir.go`; `privacy.Allows` checks.

**Do:** spec 13.2 events table and `Stop` loop cap; spec 14 variables,
including `CLAUDE_PLUGIN_DATA` (moved from P3, spec 14).

**Tests:** `SessionStart` context reaches the first turn and after `/clear`
and compaction; `UserPromptSubmit` exit 2 stops the turn and shows the reason;
plain stdout of `UserPromptSubmit` is context; `Stop` block continues at most
3 times; `SessionEnd` fires on delete; no Sandbox gives one notice and no run;
the data folder survives an update; an incognito chat writes no data folder
(incognito scan).

### F5: UI, settings, docs

**Files:** the turn status component, notices for `systemMessage`, the
timeout setting control, `src/components/plugins/PluginDetails.jsx` (hooks
list), locales, `docs/content/features/plugins.md`.

**Tests:** hook activity renders; a hook failure shows the notice; the timeout
control enforces 600.

### F6: nib hook runner

Work in the nib repository. Provides N3 of spec 21 with the conformance table
from F2 as its test. Also fixes the older runner's contract (N9) if the nib
maintainers want one runner for both paths.

---

## P3: stdio MCP, `bin/`, runtimes, output styles

### Lane map

| Lane | Title | Size | Depends on | Parallel with | nib |
|---|---|---|---|---|---|
| G1 | Sandbox `attach` stream | 2 d | F1 | G3, G4 | no |
| G2 | stdio MCP servers in the Sandbox | 1.5 d | G1, E3 | G4 | N1 |
| G3 | `bin/` on `PATH`, plugin environment for `bash` | 0.5 d | F1 | G1, G4 | no |
| G4 | Runtimes and output styles | 1.5 d | B5 | G1, G3 | no |

### G1: Sandbox `attach` stream

**Files:** `pkg/addons/contract`, `pkg/addons/stdiomux` if it needs a new
frame, the Sandbox add-on, its manifest, docs.

**Do:** spec 13.4 `attach`. The stream ends when either side closes or the
Sandbox ends.

**Tests:** byte round trip, backpressure, close from each side, Sandbox end
closes the stream, an older host refuses the manifest field.

### G2: stdio MCP servers in the Sandbox

**Files:** `core/services/studio/mcpconns/client.go` (a transport over G1's
stream); `core/services/plugins/connectors.go` (stdio servers per chat, not per
user, because they live in the chat's Sandbox); `mcpconnectors.go`; docs.

**Do:** spec 13.3. Start at the first turn that needs the server, stop with
the Sandbox. Environment of spec 14. A profile without the runtime
(`npx`, `uvx`, `bunx`) gives a notice that names the profile.

**Tests:** a fixture stdio server lists and calls tools; the server stops with
the Sandbox; no Sandbox gives no server and one notice; sensitive options
reach the environment and no log.

### G3: `bin/` and plugin environment for `bash`

**Files:** `pkg/addons/contract` (`ExecutionCall` environment, if the field is
missing), the Sandbox add-on, `core/services/studio` (the chat's `bash` call
gets `PATH` with each enabled plugin's `bin/`).

**Tests:** a plugin's `bin/` tool runs by name in `bash`; a disabled plugin's
does not; mode `0755` is kept from the package.

### G4: runtimes and output styles

**Files:** `core/services/plugins/runtimes.go` (map `npx`, `uvx`, `bunx` to a
Sandbox profile hint shown on the plugin page);
`core/services/plugins/outputstyles.go` and the chat settings (an output style
as a system prompt choice); docs.

**Tests:** the hint matches the command; an output style applies to the chat
that chose it only.

---

## Merge order across phases

1. N0 (nib release with N1, N2, N4).
2. P0b: B1, B2, B3, C5 in parallel; B4; B5; B6.
3. P0c: C1, C3, C4 in parallel (on B5's interface); then C2.
4. P0d: D1; then D2, D3, D4, D5 in parallel; then D6 and D7.
5. P1: E2 first or together with E1, E3, E4; then E5.
6. P2: F1, F2 and F6 in parallel; F3 and F4 in parallel; F5.
7. P3: G1, G3, G4 in parallel; G2.

A review pass after each phase checks spec against code, the security rules of
spec 15, and the distributed mode table of spec 7.4.
