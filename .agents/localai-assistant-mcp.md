# LocalAI Assistant — admin MCP server

This document is the contract for **anyone** (human or AI agent) touching LocalAI's admin REST surface, the in-process MCP server that wraps it, or the embedded skill prompts that teach the assistant how to use it. Read this before adding/removing/renaming admin endpoints, MCP tools, or skill recipes.

## What this feature is

`core/services/localaitools/` is a public Go package that exposes LocalAI's admin/management surface as an MCP server. It is used in two ways:

1. **In-process**: when an admin opens a chat with `metadata.localai_assistant=true`, the chat handler injects the in-memory MCP server (paired `net.Pipe()` transport, no HTTP loopback) so the LLM can install models, manage backends and edit configs by chatting.
2. **Standalone**: the `local-ai mcp-server --target=…` subcommand serves the same MCP server over stdio, talking HTTP to a remote LocalAI instance.

The two modes share **all** tool definitions and skill prompts. They differ only in their `LocalAIClient` implementation (`inproc/` calls services directly; `httpapi/` calls REST).

## The three things you must keep in sync

When you change LocalAI's admin surface, three layers must stay aligned:

1. **REST endpoint** in `core/http/endpoints/localai/*.go`.
2. **MCP tool registration** in `core/services/localaitools/tools_*.go`, plus a method on `LocalAIClient` (in `client.go`) and implementations in both `inproc/client.go` **and** `httpapi/client.go`.
3. **Skill prompt** under `core/services/localaitools/prompts/skills/*.md` — the markdown that teaches the LLM how to use the new tool. If the new tool fits an existing recipe, update that recipe; otherwise add a new file.

If you ship a REST endpoint without (2) and (3), conversational admins won't see the feature.

## Checklist for adding a new admin endpoint

- [ ] REST endpoint exists in `core/http/endpoints/localai/*.go` and is gated by `auth.RequireAdmin()` in `core/http/routes/localai.go`.
- [ ] `LocalAIClient` interface in `core/services/localaitools/client.go` has a method covering the new operation.
- [ ] DTOs added/updated in `core/services/localaitools/dto.go` (JSON-tagged; never expose raw service types).
- [ ] `inproc/client.go` implements the new method by calling the service directly (not via HTTP loopback).
- [ ] `httpapi/client.go` implements the new method by calling the REST endpoint.
- [ ] Tool registration added in the appropriate `core/services/localaitools/tools_*.go`. Mutating tools must reference safety rule 1 in the description.
- [ ] If the tool is mutating, ensure `Options{DisableMutating: true}` skips it (mirror the pattern in `tools_models.go`).
- [ ] Skill prompt added or updated under `core/services/localaitools/prompts/skills/`. The prompt must instruct the LLM when to call the tool, what to ask the user first, and what to do on error.
- [ ] Tests:
   - `core/services/localaitools/server_test.go` adds the tool name to `expectedFullCatalog` and `expectedReadOnlyCatalog` (if read-only).
   - Tool dispatch is added to `TestEachToolDispatchesToClient`.
   - `core/services/localaitools/httpapi/client_test.go` covers the new HTTP path.
- [ ] An admin route or setting that deliberately gets no tool goes in `adminRoutesWithoutTool` in `core/services/localaitools/coverage_test.go`, with the reason. The PR description repeats the reason.

## Adding a new skill recipe (no new tool)

Sometimes you want to teach the LLM a new pattern that uses existing tools. Drop a markdown file under `core/services/localaitools/prompts/skills/<verb>_<noun>.md`. The file is embedded by `//go:embed`. No Go changes needed. It is the **one source** of the recipe, and it reaches the model in two forms:

- **Inline** for the standalone `local-ai mcp-server` and any caller without a skills layer: `SystemPrompt(opts)` assembles the role, the safety rules, the tool catalog (`20_tools.md`) and every recipe in lexicographic order.
- **As a skill** in the studio chats: `Recipes()` returns `(Name, Description, Body)` for each file, `adminskills.With(builtinskills.FS())` (`core/services/studio/adminskills`, used when the server syncs the built-in layer) adds a `localai-admin-<file name, kebab case>/SKILL.md` for each, and `CompactSystemPrompt(opts)` is the prompt the chats send on every turn: the role (`00_role.md`), the safety rules (`10_safety.md`) and a list of the recipe names with their description. The model loads a recipe with `load_skill`. The chats list these skills only when they have the admin group (`groupSkillsOff` in `core/services/studio/localaigroups.go`), so a non-admin never sees them. Do not copy a recipe into `builtinskills/tree/`; a test fails when you do.

Conventions:
- Filename: `<verb>_<noun>.md` (e.g. `install_chat_model.md`, `upgrade_backend.md`). The skill name is `localai-admin-` plus the name with dashes.
- Front matter, three lines: `---`, `description: <one line, up to 120 characters, says when to use the recipe>`, `---`. The loader strips it from the inline prompt.
- Then `# Skill: <Title Case description>` on the first line of the text.
- Number the steps. Reference exact tool names in backticks.
- If the skill mutates state, remind the LLM to confirm with the user.
- The compact prompt is paid on every turn of every admin chat. `compactBudgetBytes` in `prompts_test.go` and `adminTurnPromptBudget` in `core/services/studio/localaiskills_test.go` cap it. Put new text in a recipe, not in `00_role.md` or `10_safety.md`, and raise a budget only with a reason in the commit. Safety rules stay always-on.

The skill `localai` (`core/services/studio/builtinskills/tree/localai/SKILL.md`) is the same idea for the client tools. `localaiskill_test.go` checks every tool and parameter in it against the schemas that `tools/list` serves, so update the skill when you change a client tool.

## Code conventions

These rules guard against the magic-literal drift that surfaced in the first audit. Do not re-introduce bare strings.

- **Tool names** always come from the `Tool*` constants in `core/services/localaitools/tools.go`. Tool registrations, the test catalog (`server_test.go`'s `expectedFullCatalog` / `expectedReadOnlyCatalog`), and dispatch tables reference the constants. The embedded skill prompts under `prompts/` keep bare strings — that's the one allowed exception, and `TestPromptsContainSafetyAnchors` enforces alignment.
- **Toggle/pin actions** use the `modeladmin.Action` type (`core/services/localaitools` and `core/services/modeladmin`). Use `ActionEnable`/`ActionDisable`/`ActionPin`/`ActionUnpin`; never bare `"enable"`/`"pin"` strings.
- **Capability tags** for `list_installed_models` use the `localaitools.Capability` type (`capability.go`). The `LocalAIClient.ListInstalledModels` interface takes a typed `Capability`, and the `inproc` switch only accepts canonical values (`"embed"`/`"embedding"` are not aliases — only `CapabilityEmbeddings`).
- **HTTP error checks** in `httpapi.Client` use `errors.Is(err, ErrHTTPNotFound)`, not substring matches on `err.Error()`. The typed `*HTTPError` carries `StatusCode` and `Body`; add new sentinel errors as needed rather than re-introducing string matching.
- **Channel sends** to `GalleryService.ModelGalleryChannel` / `BackendGalleryChannel` from inproc clients MUST select on `ctx.Done()` so a cancelled chat completion releases the goroutine. See `inproc.sendModelOp` / `sendBackendOp`.
- **Disk writes** of model config YAML go through `modeladmin.writeFileAtomic` (temp file + `os.Rename`). `os.WriteFile` truncates on crash and corrupts the model.
- **MCP server lifecycle**: every initialised holder MUST register `Close()` with `signals.RegisterGracefulTerminationHandler`. The standalone `mcp-server` CLI uses `signal.NotifyContext` to honour SIGINT/SIGTERM.

## File map (where to look)

```
core/services/localaitools/
  client.go              # LocalAIClient interface + DTO registry
  dto.go                 # JSON-tagged DTOs shared by both client impls
  server.go              # NewServer(client, opts) — registers tools
  tools.go               # Tool* name constants (single source of truth)
  capability.go          # Capability type + constants
  tools_models.go        # gallery_search, install_model, import_model_uri, ...
  tools_backends.go
  tools_config.go
  tools_system.go
  tools_state.go
  prompts.go             # //go:embed loader: SystemPrompt (full), CompactSystemPrompt, Recipes
  prompts/00_role.md
  prompts/10_safety.md   # SAFETY RULES — change with care
  prompts/20_tools.md    # curated tool catalog with one-liners (full prompt only)
  prompts/skills/*.md
  inproc/client.go       # in-process LocalAIClient (services-direct)
  httpapi/client.go      # REST LocalAIClient (for standalone CLI / remote)
core/http/endpoints/mcp/
  localai_assistant.go   # process-wide holder + LocalToolExecutor
core/cli/mcp_server.go   # local-ai mcp-server subcommand
```

## Why two clients

The in-process MCP server runs inside the same LocalAI binary that serves chat. Going over HTTP loopback would (a) require minting a synthetic admin API key for the server to authenticate against itself, (b) double-marshal every tool dispatch, and (c) lose access to in-process channels (e.g. `GalleryService.ModelGalleryChannel` for streaming install progress). So in-process uses `inproc.Client`. The standalone stdio CLI talks to a *remote* LocalAI; HTTP is the only option, so it uses `httpapi.Client`. Both implement the same `LocalAIClient` interface, and the parity test in `core/services/localaitools/parity_test.go` (when present) keeps their output equivalent.

## Why prompt-enforced confirmation, not code gates

The user chose KISS. Every mutating tool has a safety rule (`prompts/10_safety.md` rule 1) that requires the LLM to summarise the action and wait for explicit user confirmation before calling it. There is no `plan_*`/`apply_*` two-step in code. If you add a mutating tool, do **not** add per-tool confirmation logic in Go — instead, list the new tool name in `prompts/10_safety.md` so the LLM knows it falls under the confirmation rule.

## Scoped exception: add-on tools are not assistant tools

The prompt-only confirmation above applies to the admin catalog in
`core/services/localaitools/` and to nothing else. Tools of installed add-ons (for
example Tables) follow a different, code-enforced rule, documented in
[docs/content/addons/catalogue/work/tables.md](../docs/content/addons/catalogue/work/tables.md):

- They are never registered in this catalog and no `/api/addons` route appears
  in `toolToHTTPRoute`. `TestToolHTTPRouteMappingComplete` stays unchanged, and
  the consumer matrix test (`core/services/addons/tools`) fails if a tool name
  or route of the add-on surface appears here.
- A change proposed by an add-on tool is a stored proposal. Only the proposer's
  own browser session confirms it, with the digest of what the host card showed.
  A model's "yes" or an argument named `approved` is never authority.
- REST-to-MCP parity is intentionally partial for add-ons: the lifecycle
  mutations (install, enable, disable, upgrade, uninstall, library changes) are
  deliberately **not** exposed as assistant tools, because the assistant is one
  process-wide server with no per-call principal and would inherit prompt-only
  authorization. Do not add them without a per-call actor and a host approval
  card.
- The same holds for the sharing of the resource kinds that are not add-ons
  (`/api/shares`): no tool maps to those routes and no source file of
  `core/services/localaitools/` names them.
  `core/services/localaitools/shares_unmapped_test.go` and the consumer matrix
  test fail if one does. Sharing is never a tool.
- New code that discovers or executes tools must be classified in
  `tools.Consumers` (`core/services/addons/tools/consumers.go`) as route or
  reject. The matrix test scans the sources and fails for an unclassified file.

One backend route sits outside the add-on surface: `POST /backends/library/install`
installs a backend (not an add-on) from a library entry. It is exposed as the
`install_library_backend` tool and is a plain admin mutation under safety
rule 1. Listing library entries stays on `/api/addons` and has no assistant
tool, for the reason above.

Do not rewrite the unrelated assistant tools to match this rule.

## The client tools (second group)

`generate_image`, `speak`, `transcribe_audio` and the other inference tools are the **client** group:
any user, within their own permissions, in the same package. They differ from the manage group:

- One shared implementation. `inference.go` builds the requests and reads the answers; `inproc`
  and `httpapi` only supply how a request is sent (in memory as the acting user, or over HTTP) and
  how files are stored. Do not call a backend from a client tool: the call goes through the API
  route so the feature gate, the model allow-list, the quota and the usage log apply.
- The table `ClientTools` (`clienttools.go`) lists tool, route, feature permission and model flag.
  `clienttools_test.go` checks each row against `auth.RouteFeatureRegistry`. A new inference route
  needs a row and a tool. `ToolGroupOf` and `ToolGroups` tell the groups apart; the two groups are
  registered by separate functions (`registerClientTools` in `tools_client.go`).
- Files are references that a `FileStore` resolves (`inproc`: the home folder of the acting user;
  `httpapi`: `DirFileStore`). The chats do not use the home folder: `localaiTurn` gives each chat a
  fenced store (`clientFilesOf` in `core/services/studio/clientfiles.go`) through
  `mcpendpoint.WithFiles`, so an agent's folder bounds reads and writes of the client tools like it
  does for the chat's file tools. The store travels to the MCP server with the acting-user token.
  Never run client tools in a chat without it. Never put file bytes in a tool argument or result.
- Errors name the capability (`NoModelError`, `StatusError`), so the model can tell the person.
- `list_models` is the model discovery of the group. It is not an admin tool and not an alias of
  `list_installed_models`: that one reads the config for administrators (backend, pinned, disabled)
  and ignores the allow-list. `list_models` goes through `GET /v1/models/capabilities` as the acting
  user (`Inference.ListModels`, one implementation for both clients), so the model allow-list applies
  and no path or setting leaks. It has a row in `ClientTools` with `NoFeature` and `FLAG_ANY`; the
  chats offer it only together with another client tool. The `model` argument of every generation
  tool and the 404 hint point to it. Keep both pointers when you add a tool that takes a model.
- Client tools are not removed by `Options.DisableMutating`.
- The group is sent on every turn, so it is small. Related operations that share state in one
  domain are one tool with an `action` parameter: `voice` and `face` (`verify`, `identify`),
  `voice_profile` (`create`, `list`, `delete`), and
  `generate_image` (with `image` and `mask` it repaints). The schema is one flat object with an
  `action` enum, never `oneOf`/`anyOf`; the handler checks the fields each action needs and the
  `ClientTools` table has one row per route (`Action` tells them apart). Media tools stay
  single-purpose. `clientBudgetBytes` in `clienttools_merge_test.go` caps the size of the group's
  `tools/list` entries; raise it only with a reason in the commit.
- A tool that must not run on the word of the model alone (voice cloning needs the consent of the
  speaker) takes no consent argument. The lowest shared code (`Inference.CreateVoiceProfile`)
  refuses unless the context carries `WithVoiceConsent`, which only host code adds after a person
  confirmed (the chats turn `create` into a change proposal of kind `voice_profile`, see
  `core/services/studio/voiceprofilechange.go`). `ActingUserMeta` carries the grant across the MCP
  session like the acting user.
- Add a method to `LocalAIClient`, the DTOs to `dto_client.go`, and a dispatch case to
  `server_test.go`. The route goes in `toolToHTTPRoute` automatically from `ClientTools`.

### Keeping the client in step with the API

For media tools and arguments (file references, `params` allow-lists, budget raises) and for skills that need a sandbox profile, see [.agents/sandbox-profiles-and-media.md](sandbox-profiles-and-media.md).

The client must follow the API. `clientcoverage_test.go` reads `auth.RouteFeatureRegistry` and
fails when a route of an inference feature (chat, images, audio, video, 3d, sound, embeddings,
rerank, detection, face and voice recognition, stores and the others in `clientCoveredFeatures`)
is neither a row of `ClientTools` nor a key of `clientToolExemptions`. It also fails on a stale
row or exemption, and on a route that is both. Its failure message says which route and what to do.

To add a client tool for a new route:

1. Add a method to `LocalAIClient` (`client.go`) and the DTOs (`dto_client.go`).
2. Implement it in `inference.go` (shared code) and in both `inproc` and `httpapi`.
3. Register the tool and its schema in `tools_client.go`; add the name constant and a row (route,
   feature, model flag) to `ClientTools`. If the route belongs to an existing tool, add an `action`
   and a row instead of a new tool (see the size rule above).
4. Add a dispatch case to `server_test.go` and update the `localai` built-in skill
   (`core/services/studio/builtinskills/tree/localai/SKILL.md`, its drift test names what is
   missing) and the docs.
5. Remove the route from `clientToolExemptions` if it was listed there.

To change or remove a parameter or response of a route a tool wraps, change the DTOs, both
implementations, the schema and the skill in the same change.

To leave a route without a tool, add `"METHOD /pattern": "reason"` to `clientToolExemptions`. Start
the reason with "not built yet" when a tool is wanted but missing (this is the visible backlog);
otherwise say why an agent should not or need not call it.

## The two groups in the chats

The studio chats offer the two groups as built-in tool groups of the registry
(`core/services/studio/localaigroups.go`), so the chat's switches, the saved
agent's ceiling, `dropHidden` and the runtime settings apply to them like to any
group:

- Group `localai_client`, setting `chats_localai_client`: on by default in a chat.
  Each tool is offered when the user holds its `Feature` (`ClientTools`) and the
  `ModelChooser` finds an installed model. `describe_image` and `transcribe_audio`
  are left to the `media` group (`clientSkipped`).
- Group `localai_admin`, setting `chats_localai_admin`: opt-in (`OptIn`), so off
  until the chat or the saved agent (`builtins_on`) switches it on. Assistant mode
  adds it to the chat's switches (`switchesOf`). It needs `assistantAllowed` (the
  admin role), and never goes to a sub-agent, a trigger or job run, or a
  messaging chat.
- Each group is one registry entry (`External`), because the registry builds one
  tool per entry and the tools come from the MCP server. `Registry.BuildOne`
  decides whether the chat has the group; `localaiTurn` turns the MCP tools into
  chat tools and wraps the executor with `mcp.AsUser`, so the acting user token
  below works in both groups.
- The holder serves the groups apart: `Executor()` has the manage tools (the
  OpenAI-compatible path uses it and does not offer the client tools) and
  `ClientExecutor()` the client tools. A tool name in both groups is a bug.
- Do not mark the entries replaceable: an add-on cannot shadow a group.
- In a run nobody watches, a client tool goes through `act.spend`
  (`gateBuiltinActs`).

## Keep the tool surface small

Every tool of an always-sent group is paid for on every turn of every chat, by
every user, whether or not the turn needs it. On a small context window that
decides whether the chat fits at all. So:

- A new model tool must justify its place. Ask first whether an existing tool,
  a parameter or a skill recipe already covers it.
- Merge operations into one action-parameter tool only when they share state
  in one domain (`voice`: verify and identify; `generate_image` with `image` and
  `mask`). Use one flat object schema (many MCP clients and model APIs reject a
  top-level `oneOf`/`anyOf`), an `action` enum, and check the fields of each
  action in the handler with an error that names the action and the field. The
  `ClientTools` table keeps one row per route. Media tools and unrelated
  operations stay single-purpose.
- Keep descriptions to one short sentence per tool and short field descriptions.
- A group that is large must be deferrable behind the tool search (below). The
  admin group is the example: 58 tools, about 7,000 tokens.
- `clientBudgetBytes` in `clienttools_merge_test.go` caps the size of the client
  group's `tools/list` entries. Raise it only with a reason in the commit
  message.

## Tool search: the large groups behind three tools

The studio chats (`core/services/studio/toolsearch.go`) can send three small
tools, `tool_search`, `tool_describe` and `tool_call`, in place of the two LocalAI
groups. The setting `chats_tool_search` is `auto` (default), `on` or `off`. In
`auto`, a chat defers the groups when the estimated size of the schemas it would
send (bytes of the tool JSON over four) reaches a tenth of the chat's context
window: the chat's own context size, else the model's configured size; an
unknown window never defers. The decision is a pure function of the offered tools
and the window (`shouldDefer`), so it is the same on every turn and the prompt
cache keeps working. Nothing mutates the tool list during a turn.

Rules for this code:

- `localaiTurn` builds the tools of both groups as usual (the registry applies
  the setting, the chat's switches, the role and the agent's ceiling; the MCP
  executor is wrapped with `mcp.AsUser`). When the chat defers, the tools come
  back as `deferred` instead of being offered. The turn assembly then runs
  `gateBuiltinActs` on them (the `act.*` policy of a run that nobody watches) and
  hands them to `toolSearchTools`. The bridge holds only that list, so a name
  outside it is `no such tool`, whether the tool exists or not.
- The search index (BM25 over name, description and parameter names) is built
  from that same list. Never index a tool the chat could not call.
- The bridge tools never defer, and the core tools (files, web, shell, memory,
  media, triggers, add-on tools) are not part of this.
- The tools of the user's MCP connectors defer only when the setting is `on`
  (`connectorsDeferred`); `auto` and `off` send them in full. `scopedHostTools`
  returns them apart and already gated (`gateExternalAct`), and the turn hands
  them to `toolSearchTools` next to the LocalAI groups. A deferred connector
  tool still holds its name, so `localaiTurn` gets it in `have`.
- The tools panel marks a group with `deferred: true`.

## The acting user

A tool call can name the user it acts for. `localaitools.WithUser(ctx, user)` puts the user in a
context and `localaitools.UserFromContext(ctx)` reads it. `inproc.Client` uses the context user and
falls back to `FallbackUser` only when the context has none.

Context values do not cross the MCP session between the chat and the in-process server, so the
executor sends a one-use token in the `_meta` of the call (`localaitools.ActingUserMeta`) and the
server middleware turns it back into a context user. Only the in-process holder sets
`MCPToolInfo.ActAsUser`; a remote MCP server never gets a token. Wrap the holder's executor with
`mcp.AsUser(exec, user)` where a chat runs the tools: the studio chat does it in `assistantTurn`
with the chat owner, and the OpenAI-compatible chat endpoint does it with `auth.GetUser(c)`.

## Distributed mode

The in-memory MCP server runs only on the head node (where the chat handler runs). `inproc.Client` wraps services that are already distributed-aware (`GalleryService` coordinates with workers; `ListNodes` reads the NATS-populated registry). No NATS routing of MCP tools — the admin surface lives on the head, period.
