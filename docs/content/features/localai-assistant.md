+++
disableToc = false
title = "LocalAI Assistant"
weight = 23
url = '/features/localai-assistant'
+++

{{< agentic-routing current="localai-assistant" >}}

LocalAI Assistant is an admin-only chat that manages your instance. It is a normal [chat]({{% relref "features/chats" %}}) that starts with LocalAI's own admin tools and a system prompt that teaches the model to use them safely. You can install models, manage backends, edit model configs and check system status by chatting, with no REST calls or YAML edits.

The same tools are published as a Go package and can also be served over **stdio** to control a remote LocalAI instance from outside (e.g. from a desktop MCP host, Cursor, or `mcphost`).

## Start the assistant

The assistant starts from the **Home** page, not from the chats page. Sign in as an **admin**, pick a chat model with the model chip in the command bar, and use either entry:

- Type `/assistant` in the command bar.
- Click **Manage LocalAI by chatting** in the library row.

Home creates a new chat named "LocalAI Assistant" and opens it. It then appears in the chats list like any other chat, and you can come back to it later. Only the chat that was started this way has the admin tools; other chats never get them, and neither do the sub-agents it starts.

Over the API, create the chat with `assistant: true`:

```bash
curl -X POST http://localhost:8080/api/chats \
  -H 'Content-Type: application/json' \
  -d '{"model": "my-chat-model", "title": "LocalAI Assistant", "assistant": true}'
```

The server answers `403` to a user who is not an admin, `503` when the assistant is switched off, and `400` when the request also names a saved agent.

Once the chat is open, try:

> Install Qwen 3 chat

The assistant searches the gallery, lists candidates, asks you to pick, summarises the install, and waits for your confirmation before calling `install_model`. While the install runs, it polls progress and reports the outcome.

## Turning the assistant on and off

The assistant is on by default. Either toggle it off in **Settings → Chats and collections → LocalAI Assistant** (the `localai_assistant_enabled` runtime setting; it takes effect without a restart), or hard-disable it at startup:

```bash
LOCALAI_DISABLE_ASSISTANT=true local-ai run
```

When it is off, Home hides `/assistant` and the library entry, and `POST /api/chats` refuses `assistant: true` with `503`. The setting is read on every turn: an assistant chat that already exists keeps its history, but its next turn has no admin tools and the model is told they are unavailable.

## Security model

- Only an admin can start an assistant chat, and the server checks the role again on every turn (defense in depth). A user who loses the admin role also loses the tools in their existing assistant chats.
- The Home entry is hidden for non-admin users and while the setting is off.
- The tool server is in-process - there is no localhost loopback, no synthetic API key, and no extra TCP socket.
- Mutating tools (`install_model`, `delete_model`, `edit_model_config`, `upgrade_backend`, …) are guarded by a system-prompt rule that requires the LLM to confirm the action with the user before calling them. There is no separate code-side preview/apply step.

## Standalone stdio MCP server

You can run the same admin tool surface as a stdio MCP server pointed at any LocalAI HTTP API:

```bash
local-ai mcp-server --target http://remote.localai:8080 --api-key <admin-key>
# read-only mode - skips registration of every mutating tool
local-ai mcp-server --target http://remote.localai:8080 --read-only
```

Useful for hooking LocalAI admin into Claude Desktop, Cursor, or any MCP host. The tool catalog is identical to the in-process variant.

## Tool catalog

**Read-only**: `gallery_search`, `list_installed_models`, `list_galleries`, `get_job_status`, `get_model_config`, `list_backends`, `list_known_backends`, `list_backend_library_updates`, `system_info`, `list_nodes`, `list_scheduling`, `get_scheduling`, `vram_estimate`, `get_branding`, `get_usage_stats`, `get_pii_events`, `get_middleware_status`, `get_router_decisions`, `get_router_corpus_stats`, `list_voice_profiles`, `list_shared_folders`, `list_shared_folder_events`, `list_chat_tools`, `list_host_exec_events`, `get_chat_egress`, `list_egress_events`, `list_aliases`, `list_failover_chains`.

**Mutating** (require user confirmation per the assistant's safety prompt): `install_model`, `import_model_uri`, `delete_model`, `edit_model_config`, `reload_models`, `cancel_model_load`, `load_model`, `install_backend`, `upgrade_backend`, `install_library_backend`, `toggle_model_state`, `toggle_model_pinned`, `set_branding`, `set_alias`, `seed_router_corpus`, `clear_router_corpus`, `create_voice_profile`, `delete_voice_profile`, `create_shared_folder`, `rename_shared_folder`, `delete_shared_folder`, `restore_shared_folder`, `set_shared_folder_member`, `remove_shared_folder_member`, `set_node_vram_budget`, `set_scheduling`, `delete_scheduling`, `pin_failover_target`, `unpin_failover_target`, `set_chat_egress_ceiling`.

`list_backend_library_updates` and `install_library_backend` read and install the backends of the add-on libraries. `list_chat_tools` lists the built-in chat tools (`bash`, `web_search`, `web_fetch`), who serves each and the add-ons that replace them, and `list_host_exec_events` reads the audit of the commands the built-in `bash` ran on this machine. `get_chat_egress` and `list_egress_events` read the network settings of the Sandbox commands and their audit, and `set_chat_egress_ceiling` sets the widest network preset a chat may use.

Some administrator actions have no tool on purpose, and stay a person's act in the web interface: the oversight of other users' saved agents, triggers and connectors (list, runs, disable, delete), the **Test search** button of the web tools, and the settings `chats_host_exec`, `chats_tool_providers`, `chats_egress_allow_private` and the `chats_web_*` settings. The list with the reasons is `adminRoutesWithoutTool` in `pkg/mcp/localaitools/coverage_test.go`.

The shared folder tools manage the [shared folders]({{% relref "features/chats#shared-folders" %}}). The assistant has no user of its own, so a folder it creates has no member until you add one, and the audit names its changes `@assistant`.

## Adding new tools or skills

The MCP server lives at `pkg/mcp/localaitools/`. Tools are registered in `tools_*.go`; skill prompts (the markdown the LLM sees) are embedded from `prompts/`. To add a new admin tool, add a method to the `LocalAIClient` interface plus implementations in `inproc/` and `httpapi/`, then register the tool and add or update a skill prompt. See `.agents/localai-assistant-mcp.md` for the full contributor checklist.
