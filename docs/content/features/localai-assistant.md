+++
disableToc = false
title = "LocalAI Assistant"
weight = 27
url = '/features/localai-assistant'
+++

LocalAI Assistant is an admin-only chat modality. When enabled on a chat session, the conversation is wired to an in-process MCP server that exposes LocalAI's own admin/management surface as tools. You can install models, manage backends, edit model configs and check system status by chatting — no REST calls or YAML edits.

The same MCP server is published as a Go package and can also be served over **stdio** to control a remote LocalAI instance from outside (e.g. from a desktop MCP host, Cursor, or `mcphost`).

## Enabling the assistant in chat

Open the chat UI as an **admin** user and pick a chat-capable model in the model selector. The header shows a **Manage** toggle — flip it on, and a `Manage mode` badge appears next to the chat title. Starter chips ("What is installed?", "Install a chat model", "Show system status", "Update a backend") help you get going.

The home page also exposes a **Manage by chat** CTA that opens a fresh chat already in Manage mode.

Once on, try:

> Install Qwen 3 chat

The assistant searches the gallery, lists candidates, asks you to pick, summarises the install, and waits for your confirmation before calling `install_model`. While the install runs, it polls progress and reports the outcome.

## Disabling the feature

Either toggle it off in **Settings → LocalAI Assistant** (takes effect without restart), or hard-disable at startup:

```bash
LOCALAI_DISABLE_ASSISTANT=true local-ai run
```

When disabled, the chat handler refuses requests with `metadata.localai_assistant=true` and returns 503. The Manage toggle is hidden in the UI.

## Security model

- The chat toggle is hidden for non-admin users.
- The chat handler re-checks admin role at request time even when auth is configured to skip the assistant feature gate (defense in depth).
- The MCP server itself is in-process — there is no localhost loopback, no synthetic API key, and no extra TCP socket.
- Mutating tools (`install_model`, `delete_model`, `edit_model_config`, `upgrade_backend`, …) are guarded by a system-prompt rule that requires the LLM to confirm the action with the user before calling them. There is no separate code-side preview/apply step.

## Standalone stdio MCP server

You can run the same admin tool surface as a stdio MCP server pointed at any LocalAI HTTP API:

```bash
local-ai mcp-server --target http://remote.localai:8080 --api-key <admin-key>
# read-only mode — skips registration of every mutating tool
local-ai mcp-server --target http://remote.localai:8080 --read-only
```

Useful for hooking LocalAI admin into Claude Desktop, Cursor, or any MCP host. The tool catalog is identical to the in-process variant.

## Tool catalog

**Read-only**: `gallery_search`, `list_installed_models`, `list_galleries`, `list_backends`, `list_known_backends`, `get_job_status`, `get_model_config`, `vram_estimate`, `system_info`, `list_nodes`.

**Mutating** (require user confirmation per the assistant's safety prompt): `install_model`, `import_model_uri`, `delete_model`, `install_backend`, `upgrade_backend`, `edit_model_config`, `reload_models`, `toggle_model_state`, `toggle_model_pinned`.

## Adding new tools or skills

The MCP server lives at `pkg/mcp/localaitools/`. Tools are registered in `tools_*.go`; skill prompts (the markdown the LLM sees) are embedded from `prompts/`. To add a new admin tool, add a method to the `LocalAIClient` interface plus implementations in `inproc/` and `httpapi/`, then register the tool and add or update a skill prompt. See `.agents/localai-assistant-mcp.md` for the full contributor checklist.
