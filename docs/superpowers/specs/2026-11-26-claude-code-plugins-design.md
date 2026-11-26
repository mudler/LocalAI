# Plugins compatible with the Claude Code plugin format

Status: design note, 2026-11-26. The owner decisions below are final. Questions
that need an owner choice are in [Open questions](#22-open-questions), each with
a recommendation. No product code in this change.

Plan: `docs/superpowers/plans/2026-11-26-claude-code-plugins.md`.

## 1. Problem

Many skills, slash commands, sub-agents, MCP servers and hooks are published as
plugins in the Claude Code plugin format. A plugin is a folder with an optional
`.claude-plugin/plugin.json` and default component folders. A marketplace is a
git repository or a JSON file that lists plugins and where to get them.

LocalAI cannot use them in a chat today:

- `local-ai chat plugin ...` passes through to nib, the terminal agent, and
  manages nib's own plugins on the local machine (`core/cli/chat/run.go`,
  `isLocalOnlyArgs`). The server never sees them.
- Studio runs a nib `chat.Session` (`core/services/studio/nibharness/nib.go`)
  with nib skills off (`cfg.Skills = nil`) and no nib hooks or nib MCP servers.
  Studio serves its own skills, tools, MCP connectors and Sandbox.
- Studio already has every place a plugin component needs: skill layers, saved
  agents, slash commands, per-user MCP connectors, the Sandbox, the act policy
  wrapper (`actgate.go`), and the add-ons install queue and page.

This design adds a Plugins service to LocalAI. It installs plugins from
marketplaces, pins them to a commit SHA, stores their files by content digest,
and maps each component onto what Studio already has. nib parses the format; it
is fixed and extended upstream and imported.

## 2. Owner decisions (final)

1. A user can install a text-only plugin for themselves. A plugin with code
   needs an administrator.
2. Plugin code (hooks, stdio MCP servers, `bin/`) runs only in the Sandbox.
   There is no host execution, also not in standalone mode.
3. nib's adapter is fixed upstream and imported. LocalAI has no second parser.
4. The default marketplace is a curated LocalAI list. Administrators can add
   other marketplaces.
5. Plugins have a **Plugins** tab on the Add-ons page.
6. A plugin agent's model tier (`haiku`, `sonnet`, `opus`) maps to a model that
   an administrator sets. An unmapped tier uses the chat's model.

## 3. Goals and non-goals

### Goals

- Install, update and remove plugins from marketplaces, pinned to a full commit
  SHA, with the SHA shown to the person who installs.
- Map skills, commands, agents, MCP servers and hooks onto Studio, so that a
  plugin written for the Claude Code format works in a LocalAI chat with no
  change to the plugin.
- Run all plugin code in the chat's Sandbox, under the Claude Code hook
  contract.
- Per-user enable, and per-chat and per-agent switches, with the same rules as
  skills, add-ons and MCP connectors.
- Correct behaviour with several frontends.
- The same surface in the REST API, the CLI, the UI and, where the repo rules
  ask for it, the MCP server.

### Non-goals

- Host execution of plugin code, in any mode.
- npm, archive and command marketplace sources. nib parses them but does not
  fetch them. An entry with such a source shows as "not supported" in Browse.
- LSP servers, monitors, themes, workflows, channels, a plugin's root
  `CLAUDE.md`, JavaScript hook modules, `.mcpb` and `.dxt` bundles. nib reports
  them in `Plugin.Skipped`; the plugin page lists them as not supported.
- Hook handler types other than `command` (`http`, `mcp_tool`, `prompt`,
  `agent`). All 54 handlers in a survey of six public plugin repositories are
  `command`. The others are skipped and listed.
- Settings scopes of Claude Code (`project`, `local`, `managed`) and its
  settings files (`enabledPlugins`, `extraKnownMarketplaces`). LocalAI keeps its
  own state in its database.
- Signatures. Community plugins are not signed. The pin is the commit SHA and
  the content digest.
- Install from a local folder or zip file in P0 to P3. It is a candidate for a
  later phase.
- A plugin authoring UI.

### Wording

The product, the docs and the UI say "compatible with the Claude Code plugin
format". They never use Anthropic logos or marks, never say "official", and
never suggest an endorsement. The tab and the page are called **Plugins**.
Environment variable names (`CLAUDE_PLUGIN_ROOT` and the others) keep their
names, because plugins read them; the docs say that they are names of the
format. Plugin and marketplace names show as their authors wrote them.

## 4. Terms

| Term | Meaning |
|---|---|
| Plugin | A folder in the Claude Code plugin format. |
| Plugin ID | `<name>@<marketplace>`, as in the format. |
| Marketplace | A source of plugins: a git repository with `.claude-plugin/marketplace.json`, or a JSON URL. |
| Source SHA | The full git commit SHA (40 hex) that a plugin was fetched at. |
| Content digest | `sha256:<hex>` of the plugin's files, computed by LocalAI (section 7.2). It names the stored copy. |
| Package | The stored, immutable copy of one plugin at one content digest. |
| Install | A plugin made available, for everyone (`instance` scope) or for one user (`user` scope). It points at one package. |
| Text-only plugin | A plugin with no code reason (section 9.1). |
| Code plugin | A plugin with at least one code reason. |

## 5. Architecture

```
marketplaces (git, JSON URL)
        |  fetch at a SHA (nib claudeplugin.Fetch, hardened git)
        v
 Plugins service  core/services/plugins
   fetch -> check -> parse (nib claudeplugin.Load) -> classify -> store
        |                                   |
        | files by digest                   | rows
        v                                   v
 workspace store /plugins/<digest>/    database (plugin_* tables)
        |                                   |
        +---------------+-------------------+
                        v
 Studio, per turn (stateless):
   skill layer "plugin"   commands (/plugin:cmd)   plugin agents
   http MCP connectors    hooks and stdio MCP in the chat's Sandbox
```

New Go packages:

| Package | Role |
|---|---|
| `core/services/plugins` | Service: marketplaces, installs, updates, user state, the turn view. |
| `core/services/plugins/store` | Records. `GormStore` and `FileStore`, one shared suite. |
| `core/services/plugins/content` | Tree checks, digest, write and read in the workspace store, garbage collection. |
| `core/services/plugins/fetch` | Hardened git and HTTP fetch around nib. |
| `core/services/plugins/toolmap` | Tool names and arguments, both ways. |
| `core/services/studio/pluginhooks` | Hook engine (P2). |
| `core/http/endpoints/plugins` | REST handlers. |

Plugins are not add-ons. An add-on is a signed, supervised executable that one
owner process runs. A plugin is a folder of text and scripts that the frontends
read and the Sandbox runs. The Plugins service therefore does not use the add-on
owner, the add-on journal or `catalog.Sources`, whose sources carry a signature
policy that a plugin marketplace cannot meet. It reuses:

- the operation queue (`galleryop`) and the operations list for installs;
- the Add-ons page, as a new tab;
- the add-on concepts of a shipped default source with a tombstone, and the
  host-wide "official libraries" switch;
- the workspace store and its skill layers;
- `mcpconns` connectors and `connectors.Sealer`;
- the Sandbox add-on for code.

## 6. Data model

All tables use GORM `AutoMigrate` in the store constructor, under
`advisorylock.KeySchemaMigrate`, as `pins/store_gorm.go` does. A `FileStore`
with the same interface serves a server with no database, as for pins. Both
pass one shared store suite (`storetest`).

### 6.1 `plugin_marketplaces`

| Column | Type | Notes |
|---|---|---|
| `id` | string, PK | `^[a-z][a-z0-9-]{0,63}$`. Defaults to the `name` of `marketplace.json`. |
| `name` | string | From `marketplace.json`. |
| `kind` | string | `github`, `git` or `url`. |
| `location` | string | `owner/repo`, an `https://` git URL, or an `https://` JSON URL. |
| `ref` | string | Branch or tag to follow. Empty: the default branch. |
| `enabled` | bool | |
| `default` | bool | Shipped by LocalAI (the curated marketplace). |
| `removed` | bool | Tombstone of a removed default. Never listed, never enabled. |
| `auth_reference` | string | Reserved. A reference to a credential, never a secret. Empty in P0 (public marketplaces only). |
| `head_sha` | string | Source SHA of the last good refresh. Empty for `url`. |
| `refreshed_at`, `last_error` | time, string | `last_error` is a short code, never a body. |
| `created_by`, `created_at`, `updated_at` | | |

### 6.2 `plugin_marketplace_snapshots`

One row per marketplace: the last good `marketplace.json` as the server parsed
it.

| Column | Type | Notes |
|---|---|---|
| `marketplace_id` | string, PK | |
| `sha` | string | Source SHA of the snapshot (empty for `url`). |
| `body` | blob | The raw `marketplace.json`, at most 1 MiB. |
| `entries` | JSON | Normalized entries: name, source, description, version, category, tags, homepage, author, strict, and `supported` with a reason. |
| `skipped` | JSON | Entries nib skipped, with reasons. |
| `fetched_at` | time | |

Browse reads only this table. No frontend keeps a marketplace in memory.

### 6.3 `plugin_packages`

Immutable. One row per content digest.

| Column | Type | Notes |
|---|---|---|
| `digest` | string, PK | `sha256:<hex>`. |
| `name`, `version`, `description` | string | From the merged manifest. |
| `marketplace_id`, `entry` | string | Where it was first fetched from. |
| `source_sha` | string | |
| `size_bytes`, `file_count` | int | |
| `files` | JSON | Path, size, mode (`0644` or `0755`), SHA-256 of each file. |
| `model` | JSON | LocalAI's `plugins.Model` (section 6.8): the parsed components. |
| `code_reasons` | JSON | Subset of `hook-command`, `mcp-stdio`, `bin`, `lsp`, `monitor`, `shell-injection`. |
| `code_files` | JSON | Paths that count as code for review (section 9.3). |
| `skipped` | JSON | Components nib skipped, with reason codes. |
| `created_at` | time | |

### 6.4 `plugin_installs`

| Column | Type | Notes |
|---|---|---|
| `id` | string, PK | Random ID. |
| `plugin_id` | string | `<name>@<marketplace>`. |
| `scope` | string | `instance` or `user`. |
| `owner` | string | User ID for `user` scope; empty for `instance`. |
| `digest` | string | Current package. |
| `source_sha` | string | Pinned SHA. |
| `previous_digest` | string | For one-step rollback. |
| `runs_code` | bool | Copy of `code_reasons != []`, for queries. |
| `approved_by`, `approved_at`, `approved_digest` | | Who approved this digest. For a code plugin this is an administrator. |
| `update_digest`, `update_sha` | string | A newer version found by a refresh. Empty when current. |
| `state` | string | `installed`, `update_available`, `blocked` (section 9.4). |
| `installed_by`, `created_at`, `updated_at` | | |

Unique key: (`plugin_id`, `scope`, `owner`). A plugin ID installed for everyone
cannot also be installed for one user: the request answers 409 and names the
instance install.

### 6.5 `plugin_user_state`

| Column | Type | Notes |
|---|---|---|
| `principal`, `install_id` | PK | |
| `enabled` | bool | A `user` install is enabled at install. An `instance` install starts off for every user. |
| `needs_reenable` | bool | Set when an update adds a code reason (section 9.4). |
| `config` | JSON | Non-sensitive `userConfig` values. |
| `secrets` | blob | Sealed JSON of sensitive values (section 12.4). |
| `updated_at` | time | |

### 6.6 Chat and agent records

- `session.ChatRecord` gets `PluginsOff []string`: install IDs switched off for
  the chat. A chat stores what is off, as for `SkillsOff`, `AddonsOff` and
  `McpOff`, so a plugin enabled later is on in existing chats.
- `agents.Agent` gets `Plugins []string`: an exact grant list of install IDs, as
  `Addons` is. An empty list grants none.
- Skills of a plugin are also switched one by one through `SkillsOff`, with the
  ID `plugin:<plugin>:<skill>` (section 10.1).

### 6.7 Runtime settings

These are keys of the existing runtime settings, not a new endpoint:

| Key | Default | Meaning |
|---|---|---|
| `plugins_user_installs` | `true` | Users can install text-only plugins for themselves. |
| `plugins_model_tiers` | `{}` | Map of `haiku`, `sonnet`, `opus` to a LocalAI model name or alias. |
| `plugins_hook_timeout_seconds` | `60` | Cap for one hook handler. Maximum 600. |
| `plugins_max_bytes` | `20 MiB` | Cap for one plugin's files. |

### 6.8 `plugins.Model`

LocalAI stores its own JSON model of a parsed plugin, built from nib's
`claudeplugin.Plugin` at install. The turn path reads it from the
`plugin_packages` row and never calls nib's `Load` again. LocalAI keeps its own
DTO so that the stored rows do not depend on the JSON shape of nib's types.

```go
// core/services/plugins/model.go
type Model struct {
    Name, Version, Description string
    Skills     []SkillRef    // Name, Description, Dir (relative), Flags
    Commands   []CommandRef  // Name (with db:migrate segments), ArgumentHint, Arguments, Path
    Agents     []AgentRef    // Name, Description, Tools, DisallowedTools, Model, Skills, MaxTurns, Path
    MCPServers []MCPRef      // Name, Type, URL, Headers, Command, Args, Env (templates, unexpanded)
    Hooks      []HookRef     // Event, Matcher, Handlers (command, timeout)
    UserConfig []OptionRef   // Key, Type, Title, Description, Required, Default, Sensitive, Min, Max, Options
    Bin        []string
    Skipped    []SkippedRef  // Component, Name, ReasonCode
}
```

Bodies (skill, command and agent text) stay in the files. The turn path reads
them from the workspace store when it needs them.

## 7. Storage and distributed mode

### 7.1 Layout in the workspace store

Plugin files go into the workspace store, like the add-on skill layer
(`files.AddonSkillsPath`). Every frontend reads that store, and the blobs are on
the object store (S3) when one is set (`core/application/studio_files.go`).

```
/plugins/<digest-hex>/                  read only, immutable, one per package
    .claude-plugin/plugin.json
    skills/<name>/SKILL.md
    commands/...  agents/...  hooks/hooks.json  .mcp.json  bin/...
/users/<id>/LocalAI/plugin-data/<data-dir>/   CLAUDE_PLUGIN_DATA (P2)
```

`<data-dir>` is `claudeplugin.DataDirName(pluginID)`, for example
`fmt-tools` for `fmt@tools`. It survives updates, as the format asks.

`files.PluginsPath = "/plugins"` is a new constant in
`core/services/studio/files/layers.go`. `session.SkillLayers` gets a fourth
read-only mount: `/plugins` at `/workspace/shared-plugins`. So
`CLAUDE_PLUGIN_ROOT` in a chat is `/workspace/shared-plugins/<digest-hex>`.

The whole `/plugins` folder is mounted, as the add-on layer is. Reason: plugin
content comes only from marketplaces that an administrator enabled, it is not
user data, and per-chat mounts would need a new view feature. A folder name is a
digest, so a chat does not learn which user installed what.

### 7.2 Content digest

The digest is SHA-256 over the sorted list of lines
`<path>\0<mode>\0<sha256-of-content>\n`, where `mode` is `0644` or `0755`. Two
commits with the same plugin files give one package. The source SHA tells where
the files came from. The digest tells what they are.

### 7.3 Write once

An install writes `/plugins/<digest>/` only when it is absent. It writes into
`/plugins/.staging/<random>/`, checks every file's hash against the computed
list, then moves the folder to its final name. A second frontend that installs
the same digest at the same time finds the folder present and does nothing. The
package row is inserted with `ON CONFLICT DO NOTHING`.

### 7.4 Mode of each piece of state

The rule in `.agents/distributed-state.md` asks for one mode per piece of state.
The feature page under `docs/content/` states the same table.

| State | Mode | How |
|---|---|---|
| Marketplaces, snapshots, packages, installs, user state | Stateless per request | Rows in the database (or the file store in standalone). Every frontend reads them per request and per turn. |
| Package files | Shared, immutable | Workspace store, by digest. |
| Marketplace refresh | Single runner | `advisorylock.TryWithLockCtx` with a new key `KeyPluginsRefresh` (the next free number in `keys.go`) every 6 hours. Tick-shaped and idempotent, so leadership need not be sticky. |
| Install, update or remove of one plugin ID | Single runner per key | `HeldLock` on `KeyFromString("plugin-change:" + pluginID + ":" + scope + ":" + owner)` for the length of the change. |
| Package garbage collection | Single runner | Same lock key as the refresh, after it. Deletes a package that no install references and that is older than 24 hours. |
| Install jobs and their progress | Per instance, documented | A `galleryop` operation runs on the frontend that took the request, as add-on installs do. The row shows on every frontend; byte progress shows only on that frontend. A restart fails the job as `interrupted`; it is never resumed by itself. |
| Fetch scratch folders | Per instance | Under the data path, removed when the job ends and at startup. |
| Hook and stdio MCP processes | Sandbox rules | They run in the chat's Sandbox and follow its rules in distributed mode. |
| Model tiers and limits | Shared | Runtime settings, already shared. |

No `syncstate.SyncedMap` is needed: nothing lives in memory across requests. A
warm cache of `plugins.Model` per digest is allowed per instance because a
package never changes.

Tests: a two-frontend test on `testutil.NewFakeBus()` and one database shows
that an install through frontend A is in the turn view of frontend B, that two
concurrent installs of one plugin ID give one install, and that only one
frontend refreshes per tick.

## 8. Marketplaces

### 8.1 Sources of a marketplace

An administrator adds a marketplace with one string:

| Input | Kind | Fetch |
|---|---|---|
| `owner/repo` | `github` | `https://github.com/owner/repo.git`, shallow, at `ref`. |
| `https://host/path.git` or another `https://` git URL | `git` | Shallow, at `ref`. |
| `https://host/path/marketplace.json` | `url` | HTTP GET, at most 1 MiB. Relative plugin sources are not supported, as in the format. |

`ssh://`, `git@`, `file://`, local paths and `http://` are refused. A server
in a test may allow `file://` through a test option only.

A refresh resolves `ref` to a SHA, fetches `.claude-plugin/marketplace.json` at
that SHA, parses it with `claudeplugin.ParseMarketplace(bytes)`, and sets `Root`
and `SHA` itself. It does not call `LoadMarketplace`, because that runs
`git rev-parse` when it finds a `.git` folder. The snapshot replaces the old one
only when the parse succeeds; a failed refresh keeps the old snapshot and sets
`last_error`.

### 8.2 Plugin sources inside a marketplace

| Entry source | P0 | Pin |
|---|---|---|
| relative (`./plugins/x`) | yes | The marketplace snapshot's SHA. |
| `github` | yes | The entry's `sha` when present; otherwise `ref` (or the default branch) resolved to a SHA at install. |
| `url` (git) | yes | As `github`. |
| `git-subdir` | yes | As `github`. Sparse checkout of the subfolder. |
| `npm`, `archive`, `command` | no | Browse shows "This source type is not supported". |

These cover more than 99% of the entries in the surveyed repositories.

### 8.3 The curated marketplace

LocalAI ships one default marketplace: a curated list (owner decision 4).

- **Where:** a dedicated public git repository (recommended; see Open
  question 1). The default location is a constant in the server and can be
  replaced by the flag `--plugin-marketplaces` (environment
  `LOCALAI_PLUGIN_MARKETPLACES`, a comma-separated list) that seeds defaults at
  first start.
- **Seeding:** at first start the server adds it as a default marketplace. If an
  administrator removes it, a tombstone keeps it removed. The host-wide
  "official libraries" switch suspends it as it suspends the default add-on
  libraries.
- **Format:** a normal `.claude-plugin/marketplace.json`, so any client of the
  format can use it too. Each entry that points outside the repository has a
  full `sha`. LocalAI-specific metadata goes under `metadata.localai` of the
  entry, which other clients ignore:

```json
{
  "name": "localai",
  "owner": {"name": "LocalAI"},
  "plugins": [
    {
      "name": "superpowers",
      "source": {"source": "github", "repo": "obra/superpowers",
                 "sha": "<40 hex>"},
      "description": "Planning, TDD and review skills with a session bootstrap.",
      "category": "development",
      "metadata": {"localai": {"tested": "P2", "notes": "needs hooks for the bootstrap"}}
    }
  ]
}
```

- **Rules for an entry:** a license that allows use; a full SHA; it parses with
  `local-ai plugins validate`; a person read the code files at that SHA; and it
  was tried with a local model. The repository CI runs `local-ai plugins
  validate` on every entry and refuses an entry with no `sha`.
- **Update:** a pull request that moves the SHA. The server sees the new SHA at
  the next refresh and offers the update (section 9.4).

Initial content candidates, each subject to a license and code review:

| Candidate | Components | Phase where it works fully |
|---|---|---|
| `obra/superpowers` | skills, commands, agents, a SessionStart hook | Skills in P0; the bootstrap hook in P2. |
| Selected plugins of `wshobson/agents` | agents, commands, skills | Commands in P0; agents in P1. |
| Skill-only and command-only plugins from public plugin directories, such as commit, review and frontend-design helpers | skills, commands | P0. |
| A small `localai` plugin in the curated repository itself (relative source) | skills that teach LocalAI tools and the Sandbox | P0. It is also the end-to-end fixture. |

Plugins that need a heavy worker or the host (for example memory plugins that
run their own service) are not candidates.

## 9. Install, update and permissions

### 9.1 Text-only or code

A plugin is a code plugin when nib's `CodeReasons()` is not empty, or when a
skill or a command has a shell injection (`HasShellInjections()`):

| Code reason | Source |
|---|---|
| `hook-command` | A hook with a `command` handler. |
| `mcp-stdio` | An MCP server with `type: stdio`. |
| `bin` | Files in `bin/`. |
| `lsp` | An LSP server (not supported; it still counts). |
| `monitor` | A monitor (not supported; it still counts). |
| `shell-injection` | ``!`cmd` `` or a ` ```! ` block in a skill or command body. |

Not code:

- scripts inside a skill folder (`skills/x/scripts/run.sh`). The model runs them
  by choice with `bash`, as for imported skills ("Scripts are stored, not run").
- an `http`, `sse` or `ws` MCP server. It connects to a remote service, and
  users can already add HTTP MCP connectors themselves. The plugin card shows
  "Connects to `<host>`" for each one.

Reason for counting shell injections as code: the format runs them when the
command loads, before the model or the person sees the command.

### 9.2 Who can do what

| Action | User | Administrator |
|---|---|---|
| List marketplaces, browse, inspect | yes | yes |
| Add, edit, remove, refresh a marketplace | no | yes |
| Install a text-only plugin for themselves (`user` scope) | yes, when `plugins_user_installs` is on | yes |
| Install any plugin for everyone (`instance` scope) | no | yes |
| Install a code plugin | no | yes, `instance` scope only |
| Approve an update of a `user` install | the owner of the install | yes |
| Approve an update of an `instance` install | no | yes |
| Enable or disable an `instance` install for themselves | yes | yes |
| Remove a `user` install | its owner | yes |
| Remove an `instance` install | no | yes |

An administrator does not install a code plugin for one user only. Reason: the
review of code is the same work whatever the audience, and one scope keeps the
update and re-approval rules simple.

The install or the approval of a code plugin needs the administrator's own
browser session, as the add-on host grants do: the request carries
`X-LocalAI-Confirm`, and an API key or an MCP call gets `403 not_human_surface`.
The CLI prints the URL of the review page instead.

The feature permission is `FeaturePlugins` (`plugins`), in
`auth.GeneralFeatures`, on by default. It covers browsing, `user` installs and
per-user enable. Administrator rights cover the rest.

### 9.3 Install

1. **Resolve.** Find the entry in the marketplace snapshot. Resolve the source
   to a full SHA (section 8.2).
2. **Inspect** (`POST /api/plugins/inspect`). Fetch into a scratch folder with
   `claudeplugin.Fetch`, pinned to the SHA, through the hardened git of
   section 15.2. Check the tree (section 15.3). Compute the digest. Parse with
   `claudeplugin.Load`. Classify. The answer has the SHA, the digest, the
   components, the code reasons, the code files, the skipped components, the
   size and a preview token. The token is single use, valid for 15 minutes, and
   bound to the user, the plugin ID, the SHA and the digest.
3. **Review.** The UI shows the plugin, "Community plugin, not signed", the
   short SHA with a link to the commit when the host is known, the digest, what
   it contributes, and for a code plugin the full text of each code file.
4. **Install** (`POST /api/plugins/installs` with the token). The server checks
   the rights of section 9.2 again, then queues a `galleryop` operation
   (`EnqueuePluginOp`, key `plugin:<plugin-id>`). The job takes the per-plugin
   `HeldLock`, fetches again at the same SHA, and refuses when the digest
   differs from the token's digest. It writes the package (section 7.3), then
   the package row and the install row in one transaction.

Code files are:

- `hooks/hooks.json`, `.mcp.json` and `.claude-plugin/plugin.json` when they
  hold hooks, MCP servers or LSP servers;
- every file that a hook command, an MCP `command` or `args`, or an LSP command
  names through `${CLAUDE_PLUGIN_ROOT}/...` or a relative path;
- every file in `bin/`;
- every file with the executable bit or a `#!` first line, outside `skills/*/`;
- every skill or command file with a shell injection.

nib should compute this list (gap N6 in section 21). Until it does, LocalAI
computes it in `core/services/plugins/content`.

### 9.4 Update

A refresh of a marketplace compares each install's SHA with the snapshot. A
newer entry sets `update_sha` and `state = update_available`. A refresh never
installs anything. There is no automatic update in P0 to P3.

`POST /api/plugins/installs/{id}/update/preview` fetches the new SHA and
returns a diff against the installed package:

- the list of files added, removed and changed, with line counts;
- the full unified diff of each code file (old and new), up to 256 KiB in all;
  beyond that the list and a link to each file;
- the old and new code reasons;
- a preview token, as for an install.

Approval rules:

| Case | Who approves | What they confirm |
|---|---|---|
| `user` install, still text-only | its owner | the file list |
| `user` install, the new version has code | nobody | The update is refused for `user` scope. The install keeps the old version, with `state = blocked` and the reason "This version runs code. An administrator must install it." |
| `instance` install, no code file changed | an administrator | the file list |
| `instance` install, a code file or code reason changed | an administrator, browser session | each changed code file, ticked one by one |

When an update adds a code reason, every `plugin_user_state` row of that install
gets `needs_reenable = true` and stops contributing. The user sees "This plugin
now runs code in your Sandbox" and turns it on again. Reason: a person who
enabled a text-only plugin did not accept code in their chats.

The update job writes the new package, moves `digest` to `previous_digest`, and
sets the new one. `POST /api/plugins/installs/{id}/rollback` sets the previous
digest again with the same rights as an update, when its package still exists.

A turn that is running keeps the package it started with. The next turn uses the
new one.

### 9.5 Remove

Removing an install deletes its row and its user state rows, and the
plugin-owned MCP connectors (section 12). `CLAUDE_PLUGIN_DATA` folders stay in
the users' homes; the page says where. The package goes at the next garbage
collection when nothing else references it.

## 10. Component mapping

### 10.1 Skills: the Plugin layer

A new layer `skills.LayerPlugin` sits between Shared and Add-on:

folder, project, agent, personal, shared, **plugin**, add-on, built-in.

| Item | Value |
|---|---|
| Root | One `skills.Root` per enabled plugin: `Path: /plugins/<digest>/<skills dir>`, `Guest: /workspace/shared-plugins/<digest>/<skills dir>`, with new fields `Plugin` (plugin ID) and `PluginTitle`. |
| Name in the index | `<plugin>:<skill>`, as the format names it (`claudeplugin.Qualify`). |
| Name rule | `skills.ValidName` accepts `<name>:<name>` for this layer only. |
| Switch ID | `plugin:<plugin>:<skill>`. The whole plugin is switched through `PluginsOff`. |
| Reserved plugin names | A plugin whose name is a layer name (`folder`, `project`, `agent`, `personal`, `shared`, `plugin`, `addon`, `builtin`) is refused at install. Reason: `<layer>:<name>` is already the switch ID form. |
| Shadowing | None in practice: the names have a prefix. A plugin skill never replaces a built-in or add-on skill. |
| Variables | `load_skill` runs `claudeplugin.ExpandContent` on the body with guest paths for `CLAUDE_PLUGIN_ROOT`, `CLAUDE_SKILL_DIR`, `CLAUDE_PROJECT_DIR`, `CLAUDE_SESSION_ID` (the chat ID), and non-sensitive `user_config` values. Sensitive values show as `[sensitive option KEY]`. |
| `requires` | A skill whose `allowed-tools` names `Bash`, or whose body has a shell injection, gets `requires: [exec]`. |
| `disable-model-invocation: true` | Not in the model's index. Still a `/` command. |
| `user-invocable: false` | In the index, not in the `/` menu. |
| Files | Under `/workspace/shared-plugins/...`, read only. `load_skill` adds "Base directory for this skill:" with that path, as for add-on skills. |

Which plugins contribute in a turn: those that are installed (instance, or user
scope of the chat's principal), enabled for the principal, not in the chat's
`PluginsOff`, granted by the chat's agent when it has one (`Agent.Plugins`), and
not `needs_reenable`. This is read per turn from the database.

`RefreshKey` changes when the set of contributing digests changes, so the
Sandbox sees a newly enabled plugin.

### 10.2 Commands

A command is a user-invocable skill with argument rendering.

- **Name:** `<plugin>:<command>`, with subfolder segments (`<plugin>:db:migrate`).
  Typed as `/<plugin>:<command> <args>`.
- **Message:** the existing `studio.Command` gets `Args string`. The UI sends
  `{"text": "<args>", "skill": "<plugin>:<command>"}`; the server knows from the
  skill that it is a plugin command.
- **Rendering:** the server runs `claudeplugin.RenderArguments(body, args,
  names)`, which handles `$ARGUMENTS`, `$ARGUMENTS[N]`, `$N`, `$name` and `\$`,
  and appends `ARGUMENTS: <args>` when the body has no placeholder. Then
  `ExpandContent`. The result is the instruction of the turn, in the same
  system note form as `skillDirective`. The transcript keeps the typed text and
  shows a chip with `"invoked":{"kind":"command","name":"<plugin>:<command>"}`.
- **Bodies with `{{`:** they are plain text. Nothing passes them through a Go
  template.
- **`model`:** a tier maps as for agents (section 10.3), for this turn only.
  Other values are ignored.
- **`allowed-tools`:** ignored for permission. LocalAI never widens what a turn
  may do from plugin metadata.
- **Shell injections and `@file`** (P1): an injection runs in the chat's
  Sandbox, never on the host. The first run of a plugin digest for a user shows
  a consent card with the command text (the host-exec consent pattern of
  `hostbash.go`). Without a Sandbox the command answers that it needs one.
  `@path` reads a file of the chat's files, relative to `CLAUDE_PROJECT_DIR`,
  at most 64 KiB, and inlines it.
- **Collisions:** user commands in `LocalAI/commands.json` and built-in
  commands cannot contain `:`, so they never collide with a plugin command.

### 10.3 Agents

A plugin agent is a read-only saved-agent template (P1).

- **ID and name:** ID `plugin:<install-id>:<agent>`, name `<plugin>:<agent>`.
  They are not rows of `studio_agents`. `resolveAgent` (`savedagents.go`) also
  looks them up in the turn view.
- **Use:** `spawn_agent` with `agent`, `/agent <plugin>:<agent> <task>`, and
  `list_agents`. The chat needs **Saved agents** on, as for saved agents.
- **Instructions:** the body, after `ExpandContent`.
- **Tools:** `tools` and `disallowedTools` map through the tool map
  (section 11) into the child's `AllowedTools`. Unmapped names are dropped and
  listed on the plugin page. An empty `tools` means the parent's tools, as in
  the format.
- **Model:** `claudeplugin.ModelTier(model)`. `haiku`, `sonnet` and `opus` map
  through `plugins_model_tiers` when the entry names a model that exists.
  `inherit`, an unmapped tier, an unknown value and a missing model use the
  chat's model. The plugin page shows which model each agent gets.
- **Skills:** the agent's `skills` list qualifies to `<plugin>:<skill>` and
  limits the child's plugin skills.
- **`maxTurns`:** the child's step limit, capped by the server's own limit.
- **Ignored:** `hooks`, `mcpServers`, `permissionMode`, `initialPrompt`, as the
  format says for plugin agents. Also `color`, `background`, `memory`,
  `isolation`.
- **Copy:** **Copy to my agents** makes an editable saved agent from the
  template.

### 10.4 MCP servers

| Type | Phase | Mapping |
|---|---|---|
| `http`, `sse` | P0c | A per-user `mcpconns` connector owned by the plugin. |
| `ws` | none | Skipped: `mcpconns` has no WebSocket transport. |
| `stdio` | P3 | A process in the chat's Sandbox (section 13.3). |

Plugin-owned connectors:

- `studio_mcp_connectors` gets `plugin_install_id` and `plugin_server`. The
  service creates one connector per user and server when the user enables the
  plugin, and deletes it when the user disables it or the install goes. The
  Connectors page shows it read only, with the plugin's name.
- `MCPServer.Expand` fills `url` and `headers` from `${user_config.KEY}`,
  `${VAR}` and `${VAR:-default}`. `${VAR}` resolves only against the plugin's
  own variables (section 14), never the server's environment. Sensitive values
  go into the connector's sealed `Secrets.Headers`.
- In P0c a server whose template needs a `userConfig` value is skipped with the
  reason "needs settings (later version)". P1 adds the settings form.
- Tool names follow the existing connector rules (raw, prefixed on a clash).
  The tool map gives the format's name `mcp__plugin_<plugin>_<server>__<tool>`
  for hooks and agent tool lists.
- A chat with an agent gets a plugin's connectors only when the agent grants
  the plugin. The connector does not need its own entry in `Agent.MCPServers`.
- Unattended runs gate them with `gateExternalAct`, as other connectors.

### 10.5 Hooks

Hooks run in the chat's Sandbox under the Claude Code hook contract (P2,
section 13).

### 10.6 Other components

| Component | Decision |
|---|---|
| `bin/` | P3: added to `PATH` for `bash` in the Sandbox. |
| Output styles | P3 candidate: offered as a chat system prompt choice. |
| LSP, monitors, themes, workflows, channels, root `CLAUDE.md` | Not supported. Listed on the plugin page. |

## 11. Tool name map

`core/services/plugins/toolmap` holds the table. It maps names and argument
fields both ways. nib's `ToolMap` maps names only; LocalAI owns the argument
map because the argument shapes are LocalAI's.

### 11.1 Format to LocalAI

Used for agent `tools`, hook matchers and `updatedInput`.

| Format tool | LocalAI tool | Arguments |
|---|---|---|
| `Bash` | `bash` | `command` -> `command`; `timeout` (ms) -> `timeoutSeconds` (rounded up); `run_in_background: true` -> `job` with `action: start`. |
| `BashOutput` | `job` | `action: read`, `bash_id` -> `id`. |
| `KillShell`, `KillBash` | `job` | `action: kill`, `shell_id` -> `id`. |
| `Read` | `read_file` | `file_path` -> `path`; `offset`, `limit`. |
| `Write` | `write_file` | `file_path` -> `path`; `content`. |
| `Edit` | `edit_file` | `file_path` -> `path`; `old_string`, `new_string`, `replace_all`. |
| `MultiEdit` | `edit_file` | One call per edit, in order. A hook sees one `MultiEdit`. |
| `Glob` | `list_files` | `pattern`, `path`. |
| `LS` | `list_files` | `path`. |
| `Grep` | `search_files` | `pattern` -> `query` with `regex: true`; `path`; `-i` -> `ignore_case`. Other flags dropped. |
| `WebFetch` | `web_fetch` | `url`. `prompt` dropped. |
| `WebSearch` | `web_search` | `query`; the first of `allowed_domains` -> `site`. |
| `Task`, `Agent` | `spawn_agent` | `prompt` -> `task`; `subagent_type` -> `agent`. |
| `Skill` | `load_skill` | `skill` -> `name`. |
| `mcp__plugin_<p>_<s>__<t>` | the connector tool | unchanged. |
| `mcp__<s>__<t>` | a connector tool named `<s>` | unchanged. |
| `TodoWrite`, `NotebookEdit`, `NotebookRead`, `ExitPlanMode`, others | none | Dropped. Listed on the plugin page. |

### 11.2 LocalAI to the format

Used for hook payloads (`tool_name`, `tool_input`, `tool_response`). Each
LocalAI tool maps to one format name: `bash` -> `Bash`, `read_file` -> `Read`,
`write_file` -> `Write`, `edit_file` -> `Edit`, `list_files` -> `Glob`,
`search_files` -> `Grep`, `web_fetch` -> `WebFetch`, `web_search` ->
`WebSearch`, `spawn_agent` -> `Task`, `load_skill` -> `Skill`, `job` with
`start` -> `Bash` with `run_in_background`, connector tools -> `mcp__...`.
Other LocalAI tools pass with their own name, so a matcher `*` or a regex still
sees them.

Paths in `tool_input` are guest paths (`/workspace/home/...`), because the hook
runs in the same Sandbox.

`updatedInput` maps back through 11.1. A field that does not map makes the
update fail: the call does not run and the model gets "A plugin hook changed
this call in a way LocalAI cannot apply." Reason: running a call with half of a
change is worse than not running it.

## 12. Per-user state, userConfig and secrets

### 12.1 Enable

A user enables an `instance` install for themselves on the Installed list. A
`user` install is enabled at install. Disabling keeps the settings.

### 12.2 Per chat and per agent

- The chat's **Skills** menu groups plugin skills under the plugin's name, with
  one switch for the plugin and one for each skill.
- The chat's **Tools** menu lists plugins with connectors or hooks, with one
  switch each (`PluginsOff`).
- The agent editor has a **Plugins** list (`Agent.Plugins`), exact grant, as
  for add-ons.

### 12.3 userConfig (P1)

`plugin.json`'s `userConfig` becomes a form on the plugin's Installed page, per
user: string, number, boolean, and a choice from `options`. `directory` and
`file` take a path in the user's files. `claudeplugin.UserConfigOption.Validate`
checks each value, and `ResolveUserConfig` fills defaults and checks required
keys. A plugin with a missing required value contributes its text components,
and the components that use the value are skipped with "needs settings".

### 12.4 Sealed secrets

Sensitive values are sealed with `connectors.Sealer` (AES-256-GCM, the key that
`mcpconns` uses) with AAD `plugin|<install-id>|<principal>`. The API never
returns a sensitive value; it returns `set: true`. Sensitive values reach only:

- the headers and environment of the plugin's MCP servers;
- `CLAUDE_PLUGIN_OPTION_<KEY>` in the hook and stdio MCP environment.

They never reach a skill, command or agent body, the prompt or a log line.

## 13. Code in the Sandbox

### 13.1 Requirements

Plugin code runs only in the chat's Sandbox (owner decision 2). A chat with no
usable Sandbox runs no plugin code: its hooks and stdio servers do not run, and
the turn shows one notice per plugin, "Hooks of `<plugin>` need the Sandbox".
The text components still work. A saved agent with a folder already runs
commands only in the Sandbox; nothing changes for it.

### 13.2 Hooks (P2)

**Events.** P2 runs: `SessionStart`, `UserPromptSubmit`, `PreToolUse`,
`PostToolUse`, `PostToolUseFailure`, `Stop`, `SubagentStop`, `PreCompact`,
`SessionEnd`. Other events of `claudeplugin.HookEvents` are listed as skipped.

| Event | LocalAI point | `source` or matcher value |
|---|---|---|
| `SessionStart` | before the first turn of a chat; after `/clear`; after a compaction; on resume of a chat whose Sandbox was made again | `startup`, `clear`, `compact`, `resume` |
| `UserPromptSubmit` | before the turn's model call | none |
| `PreToolUse` | the tool seam, before `Run` | format tool name (section 11.2) |
| `PostToolUse`, `PostToolUseFailure` | the tool seam, after `Run` | format tool name |
| `Stop`, `SubagentStop` | the end of a turn of a chat or of a sub-agent | none |
| `PreCompact` | before a compaction | `manual`, `auto` |
| `SessionEnd` | chat deleted or Sandbox ended | `other` |

**Input.** JSON on stdin, as the format defines it: `session_id` (the chat ID),
`transcript_path` (empty in P2), `cwd` (`CLAUDE_PROJECT_DIR`),
`hook_event_name`, `permission_mode: "default"`, and per event `prompt`,
`tool_name`, `tool_input`, `tool_response`, `source`, `stop_hook_active`,
`trigger`.

**Output.**

| Exit | Effect |
|---|---|
| 0 | Stdout that starts with `{` is parsed as JSON. For `SessionStart` and `UserPromptSubmit`, plain stdout is added to the turn's context. |
| 2 | Block. `PreToolUse`: the call does not run, stderr goes to the model. `UserPromptSubmit`: the turn does not start, stderr shows to the person. `Stop`: the turn continues with stderr as the instruction. `PostToolUse`: stderr goes to the model. |
| other | Non-blocking error. The turn status shows "Hook of `<plugin>` failed"; stderr goes to the server log at debug level, cut to 200 bytes. |

JSON fields read: `continue`, `stopReason`, `suppressOutput`, `systemMessage`,
`decision: "block"` with `reason`, and `hookSpecificOutput` with
`hookEventName`, `permissionDecision` (`allow`, `deny`, `ask`),
`permissionDecisionReason`, `updatedInput` and `additionalContext`.

**Decisions.** All matching handlers run in parallel. Their results combine:
`deny` wins over `ask`, `ask` over `allow`. Context strings join in plugin
order.

- `allow` never widens. It cannot skip the act policy, a consent card or any
  other LocalAI check. Plugins can only narrow.
- `ask` shows a consent card in a chat that a person watches, with the hook's
  reason. In an unattended run (trigger, job, connector) it is `deny`.
- `deny` returns a sentence to the model as the tool result, not an error, as
  `gateAct` does, and records `OutcomeFrom(ctx).Deny("plugin_hook")`.

**`Stop` loops.** A `Stop` block continues the turn at most 3 times in a row.
The fourth block ends the turn with a notice. Reason: a buggy hook must not keep
a model running.

**Timeout.** Each handler gets `min(handler timeout, plugins_hook_timeout_seconds)`.
The format's default of 600 s is capped at 60 s by default because each hook adds
to the turn's latency. A timeout counts as a non-blocking error.

**Matchers.** `claudeplugin.MatchHook` with the format's tool names, so `Bash`,
`Edit|Write` and `mcp__.*` match as written.

**Execution.** Each handler is one Sandbox exec with the command, the
environment of section 14, `cwd = CLAUDE_PROJECT_DIR`, stdin, and separate
stdout and stderr capped at 64 KiB each. This needs a new execution call on the
Sandbox add-on (section 13.4).

**Seam.** The tool hooks wrap `session.Tool.Run` in a new
`core/services/studio/pluginhooks.go`, the same pattern as `gateAct` in
`actgate.go`. They are applied in `chatToolsAll` after the act policy wrappers
(`gateBuiltinActs`, `gateAddonAct`, `gateExternalAct`). Order of one call:

1. the act policy: a refused call runs no hook;
2. `PreToolUse` hooks;
3. the tool;
4. `PostToolUse` or `PostToolUseFailure` hooks.

The turn hooks sit in the Studio turn code, at the points in the table above.
Studio does not use nib's own hook dispatcher (`cfg.Hooks` stays empty): nib's
hooks run on the host, and the host is not allowed.

**Contract code.** The input builder, the output parser and the combine rule
are the same for nib and LocalAI. They belong in nib, with an executor that the
caller supplies (gap N3). Until nib has them, `pluginhooks` holds a local copy
behind the same interface, with a conformance table that later runs against
nib's code.

### 13.3 stdio MCP servers (P3)

A stdio server runs as a long-lived process in the chat's Sandbox, started at
the first turn that needs it and stopped with the Sandbox. The host talks MCP to
it over a stdio stream to the guest process. The Sandbox today has jobs with a
line input verb and a ring buffer, not a two-way stream, so P3 adds one
(section 13.4). `npx`, `uvx` and `bunx` commands need the matching runtime in
the Sandbox profile; the plugin page names the profile. Tools of a stdio server
are named and gated as for HTTP connectors.

### 13.4 Sandbox add-on changes

Two new execution operations of the Sandbox add-on, declared in the manifest's
`executionProvider` as page-only tools (as `job` is), so the model never sees
them:

| Operation | Phase | Input | Output |
|---|---|---|---|
| `exec` | P2 | `argv` or `command`, `env`, `dir`, `stdin` (at most 1 MiB), `timeoutSeconds` | `exitCode`, `stdout`, `stderr` (each at most 64 KiB), `timedOut` |
| `attach` | P3 | `argv`, `env`, `dir` | A two-way byte stream over the add-on's stdio multiplexer (`pkg/addons/stdiomux`), ended by either side |

They are additive manifest fields. An older host refuses a manifest with an
unknown field, so the Sandbox package that declares them ships with the host
release that knows them. The Sandbox profile table and `check-tools.sh` follow
the rules in `.agents/sandbox-profiles-and-media.md`.

## 14. Environment variables

| Variable | Value in the Sandbox | Where |
|---|---|---|
| `CLAUDE_PLUGIN_ROOT` | `/workspace/shared-plugins/<digest-hex>` | hooks, stdio MCP, body expansion |
| `CLAUDE_PLUGIN_DATA` | `/workspace/home/LocalAI/plugin-data/<data-dir>` | hooks, stdio MCP, body expansion |
| `CLAUDE_PROJECT_DIR` | the chat's working folder: `/workspace/home`, a project chat's project folder, or the agent's folder | hooks, stdio MCP, body expansion |
| `CLAUDE_PLUGIN_OPTION_<KEY>` | each `userConfig` value, sensitive ones included (`claudeplugin.OptionEnv`) | hooks, stdio MCP |
| `CLAUDE_SKILL_DIR` | the skill's guest folder | skill bodies |
| `CLAUDE_SESSION_ID` | the chat ID | body expansion, hooks |

`CLAUDE_PLUGIN_DATA` moves to P2 from P3 in the scope, because hooks of real
plugins write state there. In an incognito chat it points at a folder in the
Sandbox that is deleted when the Sandbox ends, and no file is written to the
user's home (`privacy.Allows`). Nothing else from the server's environment
reaches plugin code.

## 15. Security

### 15.1 Trust

- Every plugin shows "Community plugin, not signed", its source SHA (short,
  with the full value on hover) and its digest, at install, update and on the
  Installed list.
- The curated marketplace is reviewed by people, and still unsigned. The UI
  says "curated", not "verified".
- Plugin text is data from its author. Skills and commands go into the prompt
  like any other skill text. The docs say so, as they do for imported skills.

### 15.2 Fetch

`fetch` supplies its own `claudeplugin.GitFunc`:

- `https` only (`-c protocol.allow=never -c protocol.https.allow=always`);
- no hooks and no file system monitor (`-c core.hooksPath=/dev/null
  -c core.fsmonitor=false`), no submodules, no LFS smudge
  (`GIT_LFS_SKIP_SMUDGE=1`);
- `GIT_TERMINAL_PROMPT=0`, an empty `HOME` and no system config, so no
  credential helper or user config applies;
- shallow (`--depth 1`), sparse for `git-subdir`;
- a timeout of 120 s per fetch and a cap of 200 MiB on the scratch folder;
- a scratch folder under the data path, never `/tmp`, removed when the job ends.

HTTP fetches of `marketplace.json` use a 30 s timeout, a 1 MiB cap and no
redirect to a non-`https` URL.

### 15.3 Tree checks

Before the digest, every file of the plugin folder passes:

- `claudeplugin.CheckPath` for every path that the manifest names;
- no `..`, no leading `/`, no backslash, no NUL, no drive letter;
- no device, socket or pipe; symlinks are refused unless they point inside the
  plugin, and then the target file is stored as a regular file;
- no two paths that differ only in case;
- at most 2,000 files, 5 MiB per file, `plugins_max_bytes` (20 MiB) in all,
  depth at most 16, a path at most 255 bytes;
- `SKILL.md`, command and agent files at most 64 KiB (the skill limit);
  `plugin.json`, `hooks.json` and `.mcp.json` at most 256 KiB.

The mode stored is `0755` when the executable bit is set, else `0644`.

### 15.4 Limits per scope

- 50 `user` installs per user, 200 `instance` installs.
- 20 marketplaces; 1,000 entries per marketplace.

### 15.5 Runtime

- Code runs only in the Sandbox, with the chat's profile and network rules.
- Hooks get no credential of the server, only the plugin's own options.
- A hook cannot widen a decision (section 13.2).
- Plugin metadata (`allowed-tools`, `permissionMode`) never grants anything.
- Logs carry the plugin ID, the event and the outcome, never a prompt, a tool
  input or a secret.

## 16. REST API

All routes are under `/api/plugins`, with `auth.RequireFeature(db,
auth.FeaturePlugins)`, `auth.CSRFMiddleware()` and `BrowserProtection()`, as the
add-on group. Administrator routes add `auth.RequireAdmin()`. Errors use
`schema.ErrorResponse`. Swagger tag `Plugins`. Each route has a row in
`RouteFeatureRegistry`, and `/api/instructions` gets a `plugins` entry.

| Method and path | Who | Purpose |
|---|---|---|
| `GET /api/plugins/marketplaces` | user | List, with state and last error. |
| `POST /api/plugins/marketplaces` | admin | Add: `{"source": "owner/repo", "ref": ""}`. Refreshes at once. |
| `PUT /api/plugins/marketplaces/{id}` | admin | Change `ref`, `enabled`. |
| `DELETE /api/plugins/marketplaces/{id}` | admin | Remove (tombstone for a default). Installs stay. |
| `POST /api/plugins/marketplaces/{id}/refresh` | admin | Refresh now. |
| `GET /api/plugins/catalog` | user | Browse: `q`, `marketplace`, `category`, `runs_code`, paged. |
| `GET /api/plugins/catalog/{marketplace}/{name}` | user | One entry, with the last inspect result when known. |
| `POST /api/plugins/inspect` | user | `{"plugin": "name@mkt"}`: fetch, check and parse. Returns the preview and a token. |
| `GET /api/plugins/inspect/{token}/files/{path}` | user | One file of the preview, for review. |
| `POST /api/plugins/installs` | user or admin | `{"token": "...", "scope": "user"}`. 202 with the job ID. Code plugin: admin browser session. |
| `GET /api/plugins/installs` | user | Instance installs and the caller's own, with user state. Admins add `?all=true`. |
| `GET /api/plugins/installs/{id}` | user | Detail: components, skipped, code files, model per agent. |
| `GET /api/plugins/installs/{id}/files/{path}` | user | One file of the installed package. |
| `POST /api/plugins/installs/{id}/update/preview` | per 9.4 | Diff and token. |
| `POST /api/plugins/installs/{id}/update` | per 9.4 | `{"token": "...", "approved_files": [...]}`. 202 with the job ID. |
| `POST /api/plugins/installs/{id}/rollback` | per 9.4 | Back to `previous_digest`. |
| `DELETE /api/plugins/installs/{id}` | per 9.2 | Remove. |
| `GET /api/plugins/installs/{id}/me` | user | Own state: enabled, config, which secrets are set. |
| `PUT /api/plugins/installs/{id}/me` | user | `{"enabled": true, "config": {...}, "secrets": {...}}`. |
| `PUT /api/chats/{id}/plugins` | user | `{"off": ["<install-id>"]}`. |

Job progress uses the existing `GET /api/operations` rows (new flag
`isPlugin`) and `POST /api/operations/{jobID}/cancel`.

### MCP parity

The rule in `AGENTS.md` asks for an MCP tool for each administrator endpoint
that an administrator manages in a conversation. One tool in
`core/services/localaitools`, `plugins`, with an `action` parameter, because the
operations share one domain and one state: `list_marketplaces`,
`add_marketplace`, `refresh_marketplace`, `browse`, `list_installed`,
`install`, `check_updates`, `enable`, `disable`, `remove`. Each action checks its
rights in the handler.

`install` and `update` work only for text-only plugins through MCP. The
install, update and approval of a code plugin are in `adminRoutesWithoutTool`
with the reason "needs the administrator's browser session", as for add-on host
grants. The route map test (`Describe("Tool ↔ HTTP route coverage map")`) gets
the new rows. Plugin routes are not inference routes, so the LocalAI client
tools need no change.

## 17. CLI

`local-ai plugins` talks to a running server through `--target` (environment
`LOCALAI_TARGET`) and `LOCALAI_API_KEY`, as `local-ai addons install` does.

```
local-ai plugins marketplace list
local-ai plugins marketplace add <owner/repo | git URL | JSON URL> [--ref REF]
local-ai plugins marketplace remove <id>
local-ai plugins marketplace refresh [<id>]
local-ai plugins browse [QUERY] [--marketplace ID]
local-ai plugins info <name@marketplace>
local-ai plugins install <name@marketplace> [--scope user|instance] [--yes]
local-ai plugins list
local-ai plugins update [<name@marketplace>] [--check]
local-ai plugins remove <name@marketplace>
local-ai plugins enable|disable <name@marketplace>
local-ai plugins config <name@marketplace> KEY=VALUE...
local-ai plugins validate <dir>
```

- `install` prints the SHA, the digest, the components and the code reasons,
  then asks for confirmation unless `--yes`. For a code plugin it prints the URL
  of the review page and exits 0: the administrator finishes in the browser.
- `validate` works offline. It runs the same tree checks, digest and parse as
  the server and prints the report. It exits 1 on an error. The curated
  marketplace CI uses it, with `--marketplace` to check every entry of a
  `marketplace.json`.
- `local-ai chat plugin ...` stays: it manages the terminal agent's plugins on
  this machine. The help of both commands says which one is which.

## 18. UI

### 18.1 Plugins tab

A new tab `plugins` on the Add-ons page (`addonState.js` `TABS`, not in
`ADMIN_TABS`). It has three views, with the kit's `Tabs`:

- **Marketplaces.** One row per marketplace: name, location, short SHA, last
  refresh, state, **Refresh**. Administrators see **Add marketplace**, the
  enable switch and **Remove**. The curated marketplace shows "Curated by
  LocalAI".
- **Browse.** Cards with a search field and filters (marketplace, category,
  "Text only"). Each card shows the name, description, marketplace, chips for
  what it contributes ("4 skills", "2 commands", "1 agent", "Connects to
  host", "Runs code", "Needs Sandbox"), and one control: **Install**,
  **Installed** or **Update**. A user sees **Install** only on text-only
  plugins; a code plugin says "An administrator can install this".
- **Installed.** Groups "For everyone" and "Only for me". Each row: name,
  version, short SHA, state, the user's switch, **Update** when one is
  available, and a menu with **Details**, **Settings**, **Roll back** and
  **Remove**. Details list the components, the model each agent gets, the
  skipped components with reasons, and the code files.

Dialogs:

- **Install review**: "Community plugin, not signed", SHA, digest, what it
  contributes, the code files in full for a code plugin, and **Install**.
- **Update review**: the file list and the code diffs, with a tick per changed
  code file when 9.4 asks for it.
- **Settings**: the `userConfig` form (P1). A sensitive field shows "Set" and
  **Replace**, never the value.

Install progress uses the shared operations poller, as add-on installs do.

All controls come from the UI kit (`core/http/react-ui/src/components/ui/`).
Strings go through i18n in every locale.

### 18.2 Chat

- **Skills menu:** plugin skills under a group with the plugin's name and the
  source label "Plugin". One switch for the plugin, one per skill.
- **`/` completion:** a new group `plugins` in `slashCommands.js` lists
  `/<plugin>:<command>` and user-invocable plugin skills, with
  `argument-hint` as the hint.
- **Tools menu:** one switch per plugin that has connectors or hooks.
- **Agent pickers** (`AgentPicker.jsx`, `SubAgentsMenu.jsx`, `/agent `): plugin
  agents in a group "From plugins", with a "Read only" mark and the model they
  get.
- **Agent editor:** a **Plugins** grant list.
- **Hook activity (P2):** the turn status shows "Running hook of `<plugin>`"
  while a hook runs, and a hook's `systemMessage` as a notice.

### 18.3 Settings

The runtime settings page gets **Plugin agent models**: three model pickers for
`haiku`, `sonnet` and `opus` (`plugins_model_tiers`), with "Chat's model" as the
empty choice. The limits of section 6.7 sit next to it.

## 19. Docs

- New page `docs/content/features/plugins.md`: what is supported, the wording
  rule, install and update, permissions, the component mapping, the variables,
  the hook contract, the limits, and the distributed mode table of section 7.4.
- `docs/content/addons/manage-addons.md` and `user-guide.md`: the Plugins tab.
- `docs/content/features/chats-and-agents/chats-skills.md`: the Plugin layer,
  plugin commands and plugin agents.
- The CLI reference for `local-ai plugins`.

Each lane updates the pages of the behaviour it changes in the same commit.

## 20. Test strategy

| Level | What |
|---|---|
| Unit | Tree checks with crafted fixtures: traversal, symlink escape, case collision, size and count limits. Digest stability. Classification of each code reason. Tool map both ways, one row per table line. Argument rendering and expansion through nib. |
| Store | One shared suite for `GormStore` and `FileStore`: unique keys, the instance-or-user rule, tombstones, user state, sealed secrets round trip. |
| Service | Install, update, rollback, remove with a fake fetcher. Permission matrix of 9.2 and 9.4, row by row. Digest mismatch between inspect and install. `needs_reenable` on a new code reason. |
| Distributed | Two instances on `testutil.NewFakeBus()` and one database: install through A is in B's turn view; two concurrent installs give one; one refresh per tick; garbage collection never deletes a referenced package. |
| Fetch | A local git server fixture (test option allows it): pin by SHA, a moved ref, `git-subdir` sparse, a repository with `.gitattributes` filters and hooks that must not run, a size bomb. |
| Studio | Plugin layer precedence and switch IDs; `load_skill` expansion; `/plugin:cmd` rendering; plugin connectors per user and per agent grant; plugin agents through `spawn_agent` with tool subsets and tier models. |
| Hook contract | A conformance table: each event, exit 0, 2 and other, JSON output fields, plain stdout context, matchers with format names, `updatedInput` mapping and its failure, `ask` in watched and unattended chats, the `Stop` loop cap, timeouts. It runs against a fake executor, and later against nib's code. |
| Sandbox | Tagged tests with the real Sandbox add-on: `exec` (P2) and `attach` (P3). |
| Privacy | `tests/e2e/incognito_test.go` covers plugin data and hook logs: a sentinel in an incognito chat must not reach any store. |
| API | Each route with and without auth, admin and user, browser session and API key. `/api/instructions` count. MCP route map. |
| UI | Component tests of the three views and the dialogs; render-smoke specs so coverage does not drop. |
| End to end | The curated marketplace's own `localai` plugin, served from a fixture: add, browse, install, use a skill and a command in a chat, update, remove. |

Coverage baselines only go up.

## 21. Interface LocalAI needs from nib

LocalAI uses nib's `plugin/claudeplugin` package as the P0a branch has it:
`Load`, `LoadOptions`, `HasLayout`, `ValidateName`, `CheckPath`, `Plugin` and
its methods (`RunsCode`, `CodeReasons`, `HasShellInjections`,
`ResolveUserConfig`, `SensitiveKeys`), `Qualify`, `DataDirName`, `ModelTier`,
`ParseMarketplace`, `Marketplace`, `Entry`, `Source` (`Validate`, `Supported`,
`GitURL`), `ParsePluginID`, `Fetch`, `FetchOptions`, `FetchResult`, `GitFunc`,
`Vars`, `ExpandContent`, `ExpandConfig`, `RenderArguments`, `SplitArgs`,
`ParseInjections`, `HookEvents`, `HookGroup`, `HookHandler.TimeoutFor`,
`MatchHook`, `MCPServer`, `ParseMCPServer`, `MCPServer.Expand`, `OptionEnv`,
`UserConfigOption.Validate`.

Gaps to close in nib, by the phase that needs them:

| ID | Gap | Needed by |
|---|---|---|
| N1 | The package is not committed or released. LocalAI needs a tagged version (or a pseudo-version on the default branch) with `claudeplugin` and its tests passing. | P0b |
| N2 | `Fetch` needs a way to resolve a ref to a SHA without a checkout (`ResolveRef(ctx, src Source, opts FetchOptions) (string, error)`, `git ls-remote`), for update checks. | P0b |
| N3 | A hook runner for the new model, with a caller-supplied executor: `type Executor interface { Run(ctx context.Context, c HookCommand) (HookResult, error) }`, `BuildInput(event string, common CommonInput, specific any) ([]byte, error)`, `ParseOutput(event string, r HookResult) (Outcome, error)`, `Combine(event string, outs []Outcome) Outcome`. `Outcome` holds block, reason, permission decision, `updatedInput`, `additionalContext`, `systemMessage`, `continue`, `stopReason`, `suppressOutput`. Exit code 2 semantics and plain stdout context per event. | P2 |
| N4 | Stable reason codes in `Skipped` (an enum such as `unsupported-component`, `unsupported-hook-type`, `invalid-frontmatter`), so the UI can translate them. Today `Reason` is free text. | P0d |
| N5 | `FetchOptions` limits: a byte cap and a file cap checked during the fetch, and a context timeout honoured by the default git. LocalAI can enforce these in its own `GitFunc` and after the fetch, so this is a convenience. | P0b (soft) |
| N6 | `(*Plugin) CodeFiles() []string`: the files that hooks, MCP, LSP and `bin/` name, for the update review. LocalAI computes it until then. | P0b (soft) |
| N7 | File-path hooks inside a marketplace entry are not supported (`load.go` reports them). | P2 (soft) |
| N8 | `LoadMarketplace` runs `git rev-parse` when it finds `.git`. Document it, or add an option to skip it. LocalAI uses `ParseMarketplace` and is not blocked. | none |
| N9 | nib's own CLI (`cmd/plugin.go`) and `plugin.LoadManifest` still use the older adapter, and the older hook runner is off contract (any non-zero exit blocks, 30 s timeout, nib payloads). This affects `local-ai chat plugin`, not Studio. | nib only |

LocalAI does not need a nib `chat.Session` option for plugins: Studio maps the
components itself, and nib's skills, hooks and MCP stay off in Studio's
session.

## 22. Open questions

1. **Where the curated marketplace lives.**
   - (a) A new public repository next to LocalAI, for example
     `mudler/localai-plugins`.
   - (b) A repository in a new `localai-org` organisation.
   - (c) A file in this repository, `gallery/plugins/marketplace.json`.

   **Recommendation: (a).** A marketplace is a git repository in the format, so
   any client can add it. A separate repository keeps curation pull requests
   out of the LocalAI queue, has its own CI with `local-ai plugins validate`,
   and is small to fetch. Option (c) would make every server fetch this large
   repository at each refresh. Option (b) works too, but it needs a new
   organisation for one repository; a repository moves with a redirect if one
   is made later. Entries reference upstream plugins by `git-subdir` or
   `github` with a full SHA and do not copy them, so authors keep their
   history and licenses stay with the code.

2. **Initial content.** The candidates in section 8.3 need a license check and
   a code read. **Recommendation:** start with the small `localai` plugin and
   three to five skill-only or command-only plugins for P0; add
   `obra/superpowers` when P2 ships, because its value depends on its
   `SessionStart` hook.

3. **Default of `FeaturePlugins`.** **Recommendation:** on by default, with
   `plugins_user_installs` as the switch for personal installs. Owner
   decision 1 lets users install text-only plugins, and a default of off would
   hide that. Add-ons are off by default because they run code on the host;
   text-only plugins do not.
