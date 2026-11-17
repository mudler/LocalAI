+++
url = "/features/localai-assistant/"
disableToc = false
title = "LocalAI Assistant"
weight = 60
description = "A chat for administrators that installs models, manages backends and checks the status of your LocalAI instance."
keywords = ["assistant","admin","mcp"]
devbuild = "feature"
+++

{{< agentic-routing current="localai-assistant" >}}

The LocalAI Assistant is a chat for administrators. It is a normal [chat]({{% relref "/features/chats-and-agents/chats" %}}) that starts with LocalAI's own admin tools and a system prompt that tells the model to ask you before it changes anything. You can install models, manage backends, edit model configs and check system status by chatting, with no REST calls or YAML edits.

The same tools are published as a Go package and can also be served over **stdio** to control a remote LocalAI instance from outside (e.g. from a desktop MCP host, Cursor, or `mcphost`). See [LocalAI MCP server]({{% relref "/features/chats-and-agents/localai-mcp-server" %}}). The tools that every user can have in a chat, such as image generation, are on [LocalAI client tools]({{% relref "/features/chats-and-agents/localai-client-tools" %}}).

## Start the assistant

The assistant starts from the **Home** page, not from the chats page. Sign in as an **admin**, pick a chat model with the model chip in the command bar, and use either entry:

- Type `/assistant` in the command bar.
- Click **Manage LocalAI by chatting** in the library row.

Home creates a new chat named "LocalAI Assistant" and opens it. It then appears in the chats list like any other chat, and you can come back to it later. Assistant mode is a shortcut: it switches the **LocalAI admin** tool group on for that chat and adds the assistant's system prompt. The admin tools are a built-in tool group like the others (see [Tool groups]({{% relref "/features/chats-and-agents/localai-client-tools#tool-groups" %}})), so any chat of an administrator can switch them on in its Tools menu. Sub-agents never get them.

Over the API, create the chat with `assistant: true`:

```bash
curl -X POST http://localhost:8080/api/chats \
  -H 'Content-Type: application/json' \
  -d '{"model": "my-chat-model", "title": "LocalAI Assistant", "assistant": true}'
```


With authentication on, add `-H "Authorization: Bearer $API_KEY"` to each request.

The server answers `403` to a user who is not an admin, `503` when the admin tools are switched off for the instance (`chats_localai_admin`), and `400` when the request also names a saved agent.

Once the chat is open, try:

> Install Qwen 3 chat

The assistant searches the gallery, lists candidates, asks you to pick, summarises the install, and waits for your confirmation before calling `install_model`. While the install runs, it polls progress and reports the outcome.

## Turning the assistant on and off

There is no separate switch for the assistant. The two [tool groups]({{% relref "/features/chats-and-agents/localai-client-tools#tool-groups" %}}) below decide what a chat has: switch off `chats_localai_admin` in **Settings → Tools** and Home hides `/assistant` and the library entry, and no chat gets the admin tools. The setting applies at the next turn. An assistant chat that already exists keeps its history. The setting also covers the OpenAI-compatible `metadata.localai_assistant` request and the realtime assistant session: both answer `503` ("the LocalAI assistant is switched off on this server") to an administrator while it is off, and `403` to anyone else. Creating an assistant chat in Studio is refused too. Earlier versions had a `localai_assistant_enabled` setting, a `LOCALAI_DISABLE_ASSISTANT` variable and a `--disable-local-ai-assistant` flag. They are deprecated and still read: when one says the assistant is disabled (variable or flag true, stored setting false), the server sets `chats_localai_admin` to off and logs one warning. The variable and the flag are an operator choice: they win over a `chats_localai_admin` that is on in the stored settings, at start and after a settings change or reload, so a disable never fails open. To turn the admin tools on again, remove the variable or flag. A stored `localai_assistant_enabled: false` with no variable or flag maps to `chats_localai_admin` off, and a `chats_localai_admin` that you set yourself in the stored settings wins over it. Use `chats_localai_admin` instead.

## Security model

- Only an admin can start an assistant chat or have the admin tools in any chat, and the server checks the role again on every turn (defense in depth). A user who loses the admin role also loses the tools in their existing chats.
- The Home entry is hidden for non-admin users and while the setting is off.
- The tool server is in-process - there is no localhost loopback, no synthetic API key, and no extra TCP socket.
- Mutating tools (`install_model`, `delete_model`, `edit_model_config`, `upgrade_backend`, …) are guarded by a system-prompt rule that requires the LLM to confirm the action with the user before calling them. There is no separate code-side preview/apply step.

## Tool catalog

**Read-only**: `gallery_search`, `search_models`, `inspect_remote_model`, `list_installed_models`, `list_galleries`, `get_job_status`, `get_model_config`, `list_backends`, `list_known_backends`, `list_backend_library_updates`, `backend_updates`, `system_info`, `list_nodes`, `list_scheduling`, `get_scheduling`, `vram_estimate`, `get_branding`, `get_usage_stats`, `get_pii_events`, `get_middleware_status`, `get_router_decisions`, `get_router_corpus_stats`, `list_shared_folders`, `list_shared_folder_events`, `list_chat_tools`, `list_host_exec_events`, `get_chat_egress`, `list_egress_events`, `list_aliases`, `list_failover_chains`.

For a remote instance, `list_installed_models` returns model names, backends, capabilities, and disabled and pinned states. Set its optional `capability` argument, such as `tts`, to filter the results. The `system_info` tool returns the instance version, installed backends, and loaded models.

**Mutating** (require user confirmation per the assistant's safety prompt): `install_model`, `import_model_uri`, `delete_model`, `edit_model_config`, `reload_models`, `cancel_model_load`, `load_model`, `install_backend`, `upgrade_backend`, `install_library_backend`, `toggle_model_state`, `toggle_model_pinned`, `set_branding`, `set_alias`, `seed_router_corpus`, `clear_router_corpus`, `create_shared_folder`, `rename_shared_folder`, `delete_shared_folder`, `restore_shared_folder`, `set_shared_folder_member`, `remove_shared_folder_member`, `set_node_vram_budget`, `set_scheduling`, `delete_scheduling`, `pin_failover_target`, `unpin_failover_target`, `set_chat_egress_ceiling`.

`backend_updates` lists the gallery and library backends that have a newer build and, with `action` `check`, checks the registries now (it installs nothing; taking an update stays `upgrade_backend` or `install_library_backend`, with confirmation). `list_backend_library_updates` and `install_library_backend` read and install the backends of the add-on libraries. `list_chat_tools` lists the built-in chat tools (`bash`, `web_search`, `web_fetch`), who serves each and the add-ons that replace them, and `list_host_exec_events` reads the audit of the commands the built-in `bash` ran on this machine. `get_chat_egress` and `list_egress_events` read the network settings of the Sandbox commands and their audit, and `set_chat_egress_ceiling` sets the widest network preset a chat may use.

Some administrator actions have no tool on purpose, and stay a person's act in the web interface: the oversight of other users' saved agents, triggers and connectors (list, runs, disable, delete), the **Test search** button of the web tools, and the settings `chats_host_exec`, `chats_tool_providers`, `chats_egress_allow_private`, the `chats_web_*` settings and the sandbox profile policy (`sandbox_profiles`, an owner setting of the Sandbox add-on, changed on its Settings tab). The list with the reasons is `adminRoutesWithoutTool` in `core/services/localaitools/coverage_test.go`.

The shared folder tools manage the [shared folders]({{% relref "/features/files-and-documents/files-reference#shared-folders" %}}). These tools do not carry a user into the shared folder service: a folder the assistant creates has no member until you add one, and the audit names its changes `@assistant`, not the administrator who asked. The other admin tools that depend on the caller, such as the usage tools, act as the administrator whose chat (or realtime session) runs them, so they see what that person may see. In a realtime session the switch `chats_localai_admin` and the administrator role are checked again before every tool call.

## The prompt and the recipes

The admin tools come with a system prompt: the role, the safety rules (confirm before changing anything) and, for the tasks that have steps, a set of recipes such as "install a chat model", "upgrade a backend" or "manage shared folders". The chats send this prompt on every turn, so it is short. It holds the role, the safety rules and a list of the recipe names with one line each. The text of each recipe is a built-in [skill]({{% relref "/features/chats-and-agents/chats-skills#skills" %}}) named `localai-admin-<recipe>` (for example `localai-admin-install-chat-model`) that the model loads by name when a task matches. The skills are listed only in a chat that has the admin tools: an administrator's chat with the **LocalAI admin** group on. A user who is not an administrator, a sub-agent, a messaging chat, a run that starts by itself and a chat with the group off never see their names or text. An add-on cannot replace them.

The safety rules never move into a skill. Measured on the shipped recipes, the prompt of an administrator's chat is about 4,300 bytes instead of 25,000 (about 1,070 tokens instead of 6,250), and the full recipe text costs tokens only on the turns that load it.

The standalone `local-ai mcp-server` has no skills layer, so it sends the full prompt: the role, the safety rules, the tool catalog and every recipe inline. Both forms come from the same files.

<script>
var m={"tool-groups":"../localai-client-tools/#tool-groups","tool-search":"../localai-client-tools/#tool-search","standalone-stdio-mcp-server":"../localai-mcp-server/#standalone-stdio-mcp-server","client-tools":"../localai-client-tools/#client-tools","adding-new-tools-or-skills":"../../contributing/assistant-tools/#adding-new-tools-or-skills"};var h=decodeURIComponent((location.hash||"").slice(1));if(h&&m[h]&&!document.getElementById(h)){location.replace(m[h]);}
</script>
