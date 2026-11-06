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

Home creates a new chat named "LocalAI Assistant" and opens it. It then appears in the chats list like any other chat, and you can come back to it later. Assistant mode is a shortcut: it switches the **LocalAI admin** tool group on for that chat and adds the assistant's system prompt. The admin tools are a built-in tool group like the others (see [Tool groups](#tool-groups)), so any chat of an administrator can switch them on in its Tools menu. Sub-agents never get them.

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

## Tool groups

The assistant's tools are two built-in tool groups of the chats, listed in the Tools menu of a chat and in **Settings → Tools**:

| Group | Group name | Tools | Who | In a chat |
|---|---|---|---|---|
| **LocalAI client** | `localai_client` | The [client tools](#client-tools) | Every user, within the permissions of the account | On |
| **LocalAI admin** | `localai_admin` | The management tools | Administrators only | Off until the chat switches it on (assistant mode does) |

Each group has an instance setting that an administrator changes in **Settings → Tools**, or with `POST /api/settings`. Both are on by default and apply at the next turn:

| Setting | Effect |
|---|---|
| `chats_localai_client` | Offer the client tools to the chats. |
| `chats_localai_admin` | Offer the admin tools to the chats of administrators. |

Each chat has the usual switches (`PUT /api/chats/{id}/builtins`). A saved agent has the client group unless its `builtins_off` lists `localai_client`, and has the admin group only when its `builtins_on` lists `localai_admin` and its owner is an administrator. The tools the model sees are only the ones it can call:

- The admin group is for administrators. For any other user the group is listed as not usable, and the model has none of its tools.
- A client tool is offered when the account holds the feature permission in the table below and an installed model has the use case. A user with none of the features sees no client tool.
- A sub-agent never has the admin group. A run that starts by itself (a trigger or a job) and a messaging chat do not have it either. The client tools in a run that starts by itself need the `act.spend` grant, like other tools that spend compute.
- `describe_image` and `transcribe_audio` stay in the **Images and audio** group of the chat, so a chat never has the same tool name twice.

The OpenAI-compatible API is not changed: `metadata.localai_assistant` still gives an administrator the admin tools there, and the client tools are not offered.

## Security model

- Only an admin can start an assistant chat or have the admin tools in any chat, and the server checks the role again on every turn (defense in depth). A user who loses the admin role also loses the tools in their existing chats.
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

Useful for hooking LocalAI admin into Claude Desktop, Cursor, or any MCP host. The tool catalog is identical to the in-process variant, including the [client tools](#client-tools).

## Tool catalog

**Read-only**: `gallery_search`, `list_installed_models`, `list_galleries`, `get_job_status`, `get_model_config`, `list_backends`, `list_known_backends`, `list_backend_library_updates`, `system_info`, `list_nodes`, `list_scheduling`, `get_scheduling`, `vram_estimate`, `get_branding`, `get_usage_stats`, `get_pii_events`, `get_middleware_status`, `get_router_decisions`, `get_router_corpus_stats`, `list_voice_profiles`, `list_shared_folders`, `list_shared_folder_events`, `list_chat_tools`, `list_host_exec_events`, `get_chat_egress`, `list_egress_events`, `list_aliases`, `list_failover_chains`.

For a remote instance, `list_installed_models` returns model names, backends, capabilities, and disabled and pinned states. Set its optional `capability` argument, such as `tts`, to filter the results. The `system_info` tool returns the instance version, installed backends, and loaded models.

**Mutating** (require user confirmation per the assistant's safety prompt): `install_model`, `import_model_uri`, `delete_model`, `edit_model_config`, `reload_models`, `cancel_model_load`, `load_model`, `install_backend`, `upgrade_backend`, `install_library_backend`, `toggle_model_state`, `toggle_model_pinned`, `set_branding`, `set_alias`, `seed_router_corpus`, `clear_router_corpus`, `create_voice_profile`, `delete_voice_profile`, `create_shared_folder`, `rename_shared_folder`, `delete_shared_folder`, `restore_shared_folder`, `set_shared_folder_member`, `remove_shared_folder_member`, `set_node_vram_budget`, `set_scheduling`, `delete_scheduling`, `pin_failover_target`, `unpin_failover_target`, `set_chat_egress_ceiling`.

`list_backend_library_updates` and `install_library_backend` read and install the backends of the add-on libraries. `list_chat_tools` lists the built-in chat tools (`bash`, `web_search`, `web_fetch`), who serves each and the add-ons that replace them, and `list_host_exec_events` reads the audit of the commands the built-in `bash` ran on this machine. `get_chat_egress` and `list_egress_events` read the network settings of the Sandbox commands and their audit, and `set_chat_egress_ceiling` sets the widest network preset a chat may use.

Some administrator actions have no tool on purpose, and stay a person's act in the web interface: the oversight of other users' saved agents, triggers and connectors (list, runs, disable, delete), the **Test search** button of the web tools, and the settings `chats_host_exec`, `chats_tool_providers`, `chats_egress_allow_private` and the `chats_web_*` settings. The list with the reasons is `adminRoutesWithoutTool` in `core/services/localaitools/coverage_test.go`.

The shared folder tools manage the [shared folders]({{% relref "features/chats#shared-folders" %}}). The assistant has no user of its own, so a folder it creates has no member until you add one, and the audit names its changes `@assistant`.

## Client tools

Besides the management tools above, the server registers a second group, the client tools. They run inference, so a person can ask for a picture, a recording or a ranking in a chat. The management tools are for administrators. A client tool acts as the user who calls it, like any other API request: the route checks the account's permission for the feature, the model allow-list and the quota apply, and the usage is logged for that user. When the server refuses, the tool tells the person which capability the account may not use.

| Tool | API route | Permission | Model use case |
|---|---|---|---|
| `generate_image` | `POST /v1/images/generations` | `images` | image |
| `edit_image` | `POST /v1/images/inpainting` | `images` | image |
| `speak` | `POST /v1/audio/speech` | `audio_speech` | tts |
| `transcribe_audio` | `POST /v1/audio/transcriptions` | `audio_transcription` | transcript |
| `describe_image` | `POST /v1/chat/completions` with an image | `chat` | vision |
| `embed` | `POST /v1/embeddings` | `embeddings` | embeddings |
| `detect` | `POST /v1/detection` | `detection` | detection |
| `rerank` | `POST /v1/rerank` | `rerank` | rerank |
| `generate_video` | `POST /video` | `video` | video |
| `generate_sound` | `POST /v1/sound-generation` | `sound` | sound generation |
| `verify_voice`, `identify_voice` | `POST /v1/voice/verify`, `/v1/voice/identify` | `voice_recognition` | speaker recognition |
| `verify_face`, `identify_face` | `POST /v1/face/verify`, `/v1/face/identify` | `face_recognition` | face recognition |

Every tool takes an optional `model`. When you leave it out, the tool uses the first installed model, by name, that has the use case. If there is none, the tool answers `no <capability> model installed`. `describe_image` uses the model that the **Chat image model** setting names, or an installed model that can look at images.

**Files.** A tool never carries file contents. A file argument (the image to edit, the recording to transcribe) is a reference, and a result (an image, audio, a video, the vectors of `embed`) is saved as a file and returned as a reference with its type, size and, for an image, its dimensions. In the assistant chat, a reference is a path in the user's files: `/workspace/home/...`, or a path relative to the home folder. Results go to the `generated` folder of the home folder. The size limits are 20 MB for an image and 100 MB for a recording. A reference can also be a `data:` URL.

`speak` does not take an uploaded reference recording, because the speech route does not. To speak in a cloned voice, pass a saved voice profile URI from `list_voice_profiles` as `voice`.

`describe_image` and `transcribe_audio` also exist as built-in chat tools that read the user's files. A chat sends the model one tool of each name: in a chat, the **Images and audio** group serves these two, and the **LocalAI client** group serves the others.

The client tools are not turned off by `--read-only`, which is about changing the instance. In the standalone server, files are read from and written to the folder of `--files-dir` (default: the current folder), and a file argument must lie inside it:

```bash
local-ai mcp-server --target http://remote.localai:8080 --api-key <key> --files-dir ./media
```

A user's API key is enough for the client tools. The management tools still need an administrator key.

## Adding new tools or skills

The MCP server lives at `core/services/localaitools/`. Tools are registered in `tools_*.go`; skill prompts (the markdown the LLM sees) are embedded from `prompts/`. To add a new admin tool, add a method to the `LocalAIClient` interface plus implementations in `inproc/` and `httpapi/`, then register the tool and add or update a skill prompt. See `.agents/localai-assistant-mcp.md` for the full contributor checklist.
