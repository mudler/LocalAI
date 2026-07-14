# LocalAI Assistant — admin MCP server

This document is the contract for **anyone** (human or AI agent) touching LocalAI's admin REST surface, the in-process MCP server that wraps it, or the embedded skill prompts that teach the assistant how to use it. Read this before adding/removing/renaming admin endpoints, MCP tools, or skill recipes.

## What this feature is

`pkg/mcp/localaitools/` is a public Go package that exposes LocalAI's admin/management surface as an MCP server. It is used in two ways:

1. **In-process**: when an admin opens a chat with `metadata.localai_assistant=true`, the chat handler injects the in-memory MCP server (paired `net.Pipe()` transport, no HTTP loopback) so the LLM can install models, manage backends and edit configs by chatting.
2. **Standalone**: the `local-ai mcp-server --target=…` subcommand serves the same MCP server over stdio, talking HTTP to a remote LocalAI instance.

The two modes share **all** tool definitions and skill prompts. They differ only in their `LocalAIClient` implementation (`inproc/` calls services directly; `httpapi/` calls REST).

## The three things you must keep in sync

When you change LocalAI's admin surface, three layers must stay aligned:

1. **REST endpoint** in `core/http/endpoints/localai/*.go`.
2. **MCP tool registration** in `pkg/mcp/localaitools/tools_*.go`, plus a method on `LocalAIClient` (in `client.go`) and implementations in both `inproc/client.go` **and** `httpapi/client.go`.
3. **Skill prompt** under `pkg/mcp/localaitools/prompts/skills/*.md` — the markdown that teaches the LLM how to use the new tool. If the new tool fits an existing recipe, update that recipe; otherwise add a new file.

If you ship a REST endpoint without (2) and (3), conversational admins won't see the feature.

## Checklist for adding a new admin endpoint

- [ ] REST endpoint exists in `core/http/endpoints/localai/*.go` and is gated by `auth.RequireAdmin()` in `core/http/routes/localai.go`.
- [ ] `LocalAIClient` interface in `pkg/mcp/localaitools/client.go` has a method covering the new operation.
- [ ] DTOs added/updated in `pkg/mcp/localaitools/dto.go` (JSON-tagged; never expose raw service types).
- [ ] `inproc/client.go` implements the new method by calling the service directly (not via HTTP loopback).
- [ ] `httpapi/client.go` implements the new method by calling the REST endpoint.
- [ ] Tool registration added in the appropriate `pkg/mcp/localaitools/tools_*.go`. Mutating tools must reference safety rule 1 in the description.
- [ ] If the tool is mutating, ensure `Options{DisableMutating: true}` skips it (mirror the pattern in `tools_models.go`).
- [ ] Skill prompt added or updated under `pkg/mcp/localaitools/prompts/skills/`. The prompt must instruct the LLM when to call the tool, what to ask the user first, and what to do on error.
- [ ] Tests:
   - `pkg/mcp/localaitools/server_test.go` adds the tool name to `expectedFullCatalog` and `expectedReadOnlyCatalog` (if read-only).
   - Tool dispatch is added to `TestEachToolDispatchesToClient`.
   - `pkg/mcp/localaitools/httpapi/client_test.go` covers the new HTTP path.

## Adding a new skill recipe (no new tool)

Sometimes you want to teach the LLM a new pattern that uses existing tools. Drop a markdown file under `pkg/mcp/localaitools/prompts/skills/<verb>_<noun>.md`. The file is automatically embedded by `//go:embed` and assembled into the system prompt in lexicographic order. No Go changes needed.

Conventions:
- Filename: `<verb>_<noun>.md` (e.g. `install_chat_model.md`, `upgrade_backend.md`).
- First line: `# Skill: <Title Case description>`.
- Number the steps. Reference exact tool names in backticks.
- If the skill mutates state, remind the LLM to confirm with the user.

## Code conventions

These rules guard against the magic-literal drift that surfaced in the first audit. Do not re-introduce bare strings.

- **Tool names** always come from the `Tool*` constants in `pkg/mcp/localaitools/tools.go`. Tool registrations, the test catalog (`server_test.go`'s `expectedFullCatalog` / `expectedReadOnlyCatalog`), and dispatch tables reference the constants. The embedded skill prompts under `prompts/` keep bare strings — that's the one allowed exception, and `TestPromptsContainSafetyAnchors` enforces alignment.
- **Toggle/pin actions** use the `modeladmin.Action` type (`pkg/mcp/localaitools` and `core/services/modeladmin`). Use `ActionEnable`/`ActionDisable`/`ActionPin`/`ActionUnpin`; never bare `"enable"`/`"pin"` strings.
- **Capability tags** for `list_installed_models` use the `localaitools.Capability` type (`capability.go`). The `LocalAIClient.ListInstalledModels` interface takes a typed `Capability`, and the `inproc` switch only accepts canonical values (`"embed"`/`"embedding"` are not aliases — only `CapabilityEmbeddings`).
- **HTTP error checks** in `httpapi.Client` use `errors.Is(err, ErrHTTPNotFound)`, not substring matches on `err.Error()`. The typed `*HTTPError` carries `StatusCode` and `Body`; add new sentinel errors as needed rather than re-introducing string matching.
- **Channel sends** to `GalleryService.ModelGalleryChannel` / `BackendGalleryChannel` from inproc clients MUST select on `ctx.Done()` so a cancelled chat completion releases the goroutine. See `inproc.sendModelOp` / `sendBackendOp`.
- **Disk writes** of model config YAML go through `modeladmin.writeFileAtomic` (temp file + `os.Rename`). `os.WriteFile` truncates on crash and corrupts the model.
- **MCP server lifecycle**: every initialised holder MUST register `Close()` with `signals.RegisterGracefulTerminationHandler`. The standalone `mcp-server` CLI uses `signal.NotifyContext` to honour SIGINT/SIGTERM.

## File map (where to look)

```
pkg/mcp/localaitools/
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
  prompts.go             # //go:embed loader + SystemPrompt(opts)
  prompts/00_role.md
  prompts/10_safety.md   # SAFETY RULES — change with care
  prompts/20_tools.md    # curated tool catalog with one-liners
  prompts/skills/*.md
  inproc/client.go       # in-process LocalAIClient (services-direct)
  httpapi/client.go      # REST LocalAIClient (for standalone CLI / remote)
core/http/endpoints/mcp/
  localai_assistant.go   # process-wide holder + LocalToolExecutor
core/cli/mcp_server.go   # local-ai mcp-server subcommand
```

## Why two clients

The in-process MCP server runs inside the same LocalAI binary that serves chat. Going over HTTP loopback would (a) require minting a synthetic admin API key for the server to authenticate against itself, (b) double-marshal every tool dispatch, and (c) lose access to in-process channels (e.g. `GalleryService.ModelGalleryChannel` for streaming install progress). So in-process uses `inproc.Client`. The standalone stdio CLI talks to a *remote* LocalAI; HTTP is the only option, so it uses `httpapi.Client`. Both implement the same `LocalAIClient` interface, and the parity test in `pkg/mcp/localaitools/parity_test.go` (when present) keeps their output equivalent.

## Why prompt-enforced confirmation, not code gates

The user chose KISS. Every mutating tool has a safety rule (`prompts/10_safety.md` rule 1) that requires the LLM to summarise the action and wait for explicit user confirmation before calling it. There is no `plan_*`/`apply_*` two-step in code. If you add a mutating tool, do **not** add per-tool confirmation logic in Go — instead, list the new tool name in `prompts/10_safety.md` so the LLM knows it falls under the confirmation rule.

## Distributed mode

The in-memory MCP server runs only on the head node (where the chat handler runs). `inproc.Client` wraps services that are already distributed-aware (`GalleryService` coordinates with workers; `ListNodes` reads the NATS-populated registry). No NATS routing of MCP tools — the admin surface lives on the head, period.
