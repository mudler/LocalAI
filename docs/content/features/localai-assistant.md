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

The server answers `403` to a user who is not an admin, `503` when the admin tools are switched off for the instance (`chats_localai_admin`), and `400` when the request also names a saved agent.

Once the chat is open, try:

> Install Qwen 3 chat

The assistant searches the gallery, lists candidates, asks you to pick, summarises the install, and waits for your confirmation before calling `install_model`. While the install runs, it polls progress and reports the outcome.

## Turning the assistant on and off

There is no separate switch for the assistant. The two [tool groups](#tool-groups) below decide what a chat has: switch off `chats_localai_admin` in **Settings → Tools** and Home hides `/assistant` and the library entry, and no chat gets the admin tools. The setting applies at the next turn. An assistant chat that already exists keeps its history. The setting also covers the OpenAI-compatible `metadata.localai_assistant` request and the realtime assistant session: both answer `503` ("the LocalAI assistant is switched off on this server") to an administrator while it is off, and `403` to anyone else. Creating an assistant chat in Studio is refused too. Earlier versions had a `localai_assistant_enabled` setting, a `LOCALAI_DISABLE_ASSISTANT` variable and a `--disable-local-ai-assistant` flag. They are deprecated and still read: when one says the assistant is disabled (variable or flag true, stored setting false), the server sets `chats_localai_admin` to off and logs one warning. The variable and the flag are an operator choice: they win over a `chats_localai_admin` that is on in the stored settings, at start and after a settings change or reload, so a disable never fails open. To turn the admin tools on again, remove the variable or flag. A stored `localai_assistant_enabled: false` with no variable or flag maps to `chats_localai_admin` off, and a `chats_localai_admin` that you set yourself in the stored settings wins over it. Use `chats_localai_admin` instead.

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
| `chats_tool_search` | `auto` (default), `on` or `off`: whether the two groups are [loaded on demand](#tool-search). |

Each chat has the usual switches (`PUT /api/chats/{id}/builtins`). A saved agent has the client group unless its `builtins_off` lists `localai_client`, and has the admin group only when its `builtins_on` lists `localai_admin` and its owner is an administrator. The tools the model sees are only the ones it can call:

- The admin group is for administrators. For any other user the group is listed as not usable, and the model has none of its tools.
- A client tool is offered when the account holds the feature permission in the table below and an installed model has the use case. A user with none of the features sees no client tool.
- A sub-agent never has the admin group. A run that starts by itself (a trigger or a job) does not have it either.
- **Messaging chats.** A chat that comes from a messaging app (Telegram, Slack and so on) has the client group like any other chat, so a person can ask the bot for a picture, a sound or speech. The tools act as the owner of the chat, within the owner's permissions and installed models, and an agent that should not spend the owner's compute there switches the group off with `builtins_off`. The admin group is never offered in a messaging chat, whatever the chat or the agent switches on: the person writing may not be the owner, and the admin tools change the server. The Tools menu and `GET /api/chats/{id}/tools` give that as the reason. Saving a voice with `voice_profile` is refused there too, because the confirmation of the speaker's consent is given in Studio.
- The client tools in a run that starts by itself need the `act.spend` grant, like other tools that spend compute.
- `describe_image` and `transcribe_audio` stay in the **Images and audio** group of the chat, so a chat never has the same tool name twice. There they are aliases of the client tools of the same name: they use the helper models of **Chat image model** and **Chat transcription model** and read the files the chat can see, then run the same code as the client tools, as the person who is chatting.

### Tool search

Every tool the model is offered is sent to it again on every turn, and the two groups are large: together about 9,000 tokens. On a model with a small context window that leaves little room for the conversation. So a chat can send three small tools in place of the two groups, and the model finds and runs the group tools through them:

| Tool | What it does |
|---|---|
| `tool_search` | Takes a few words (`query`, and `limit`, default 5, at most 20) and returns the best matching tools, each with a one-line description. |
| `tool_describe` | Takes a tool name and returns its full description and parameters. |
| `tool_call` | Takes a tool name and its `arguments` and runs the tool. |

The three together are under 400 tokens. The setting `chats_tool_search` decides when a chat uses them:

- `auto` (default): when the schemas of the tools of both groups that the chat would send reach a tenth of the context window of the chat's model. The window is the chat's own context size setting, or else the context size of the model. If the window is not known, the groups are sent in full.
- `on`: always, when the chat has at least one tool in the groups.
- `off`: never. Every tool is sent in full.

The decision uses only the tools the chat would send and the window, so a chat gets the same answer on every turn. It changes only when you change the switches of the chat or the model.

Permissions do not change. `tool_call` runs the same tool a direct call would, with the same checks: the role, the switches of the chat, the ceiling of a saved agent, the `act.spend` rule of a run that starts by itself, and the account of the person who is chatting. `tool_search` and `tool_describe` list and describe only tools the chat could call. Any other name, whether the tool exists or not, gets the same answer: `no such tool`. The tools of other groups, like the file and web tools, are always sent in full. The tools panel (`GET /api/chats/{id}/tools`) marks a group that loads on demand with `deferred: true`, and the Tools menu shows it as "Loaded on demand".

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
# read-only mode - skips registration of every mutating tool and of the client tools that write files or make content
local-ai mcp-server --target http://remote.localai:8080 --read-only
```

Useful for hooking LocalAI admin into Claude Desktop, Cursor, or any MCP host. The tool catalog is identical to the in-process variant, including the [client tools](#client-tools).

## Tool catalog

**Read-only**: `gallery_search`, `list_installed_models`, `list_galleries`, `get_job_status`, `get_model_config`, `list_backends`, `list_known_backends`, `list_backend_library_updates`, `backend_updates`, `system_info`, `list_nodes`, `list_scheduling`, `get_scheduling`, `vram_estimate`, `get_branding`, `get_usage_stats`, `get_pii_events`, `get_middleware_status`, `get_router_decisions`, `get_router_corpus_stats`, `list_shared_folders`, `list_shared_folder_events`, `list_chat_tools`, `list_host_exec_events`, `get_chat_egress`, `list_egress_events`, `list_aliases`, `list_failover_chains`.

For a remote instance, `list_installed_models` returns model names, backends, capabilities, and disabled and pinned states. Set its optional `capability` argument, such as `tts`, to filter the results. The `system_info` tool returns the instance version, installed backends, and loaded models.

**Mutating** (require user confirmation per the assistant's safety prompt): `install_model`, `import_model_uri`, `delete_model`, `edit_model_config`, `reload_models`, `cancel_model_load`, `load_model`, `install_backend`, `upgrade_backend`, `install_library_backend`, `toggle_model_state`, `toggle_model_pinned`, `set_branding`, `set_alias`, `seed_router_corpus`, `clear_router_corpus`, `create_shared_folder`, `rename_shared_folder`, `delete_shared_folder`, `restore_shared_folder`, `set_shared_folder_member`, `remove_shared_folder_member`, `set_node_vram_budget`, `set_scheduling`, `delete_scheduling`, `pin_failover_target`, `unpin_failover_target`, `set_chat_egress_ceiling`.

`backend_updates` lists the gallery and library backends that have a newer build and, with `action` `check`, checks the registries now (it installs nothing; taking an update stays `upgrade_backend` or `install_library_backend`, with confirmation). `list_backend_library_updates` and `install_library_backend` read and install the backends of the add-on libraries. `list_chat_tools` lists the built-in chat tools (`bash`, `web_search`, `web_fetch`), who serves each and the add-ons that replace them, and `list_host_exec_events` reads the audit of the commands the built-in `bash` ran on this machine. `get_chat_egress` and `list_egress_events` read the network settings of the Sandbox commands and their audit, and `set_chat_egress_ceiling` sets the widest network preset a chat may use.

Some administrator actions have no tool on purpose, and stay a person's act in the web interface: the oversight of other users' saved agents, triggers and connectors (list, runs, disable, delete), the **Test search** button of the web tools, and the settings `chats_host_exec`, `chats_tool_providers`, `chats_egress_allow_private` and the `chats_web_*` settings. The list with the reasons is `adminRoutesWithoutTool` in `core/services/localaitools/coverage_test.go`.

The shared folder tools manage the [shared folders]({{% relref "features/chats#shared-folders" %}}). These tools do not carry a user into the shared folder service: a folder the assistant creates has no member until you add one, and the audit names its changes `@assistant`, not the administrator who asked. The other admin tools that depend on the caller, such as the usage tools, act as the administrator whose chat (or realtime session) runs them, so they see what that person may see. In a realtime session the switch `chats_localai_admin` and the administrator role are checked again before every tool call.

## The prompt and the recipes

The admin tools come with a system prompt: the role, the safety rules (confirm before changing anything) and, for the tasks that have steps, a set of recipes such as "install a chat model", "upgrade a backend" or "manage shared folders". The chats send this prompt on every turn, so it is short. It holds the role, the safety rules and a list of the recipe names with one line each. The text of each recipe is a built-in [skill]({{% relref "features/chats#skills" %}}) named `localai-admin-<recipe>` (for example `localai-admin-install-chat-model`) that the model loads by name when a task matches. The skills are listed only in a chat that has the admin tools: an administrator's chat with the **LocalAI admin** group on. A user who is not an administrator, a sub-agent, a messaging chat, a run that starts by itself and a chat with the group off never see their names or text. An add-on cannot replace them.

The safety rules never move into a skill. Measured on the shipped recipes, the prompt of an administrator's chat is about 4,300 bytes instead of 25,000 (about 1,070 tokens instead of 6,250), and the full recipe text costs tokens only on the turns that load it.

The standalone `local-ai mcp-server` has no skills layer, so it sends the full prompt: the role, the safety rules, the tool catalog and every recipe inline. Both forms come from the same files.

## Client tools

Besides the management tools above, the server registers a second group, the client tools. They run inference, so a person can ask for a picture, a recording or a ranking in a chat. The management tools are for administrators. A client tool acts as the user who calls it, like any other API request: the route checks the account's permission for the feature, the model allow-list and the quota apply, and the usage is logged for that user. When the server refuses, the tool tells the person which capability the account may not use.

| Tool | API route | Permission | Model use case |
|---|---|---|---|
| `generate_image` | `POST /v1/images/generations`, or `POST /v1/images/inpainting` when it gets `image` and `mask` | `images` | image |
| `speak` | `POST /v1/audio/speech` | `audio_speech` | tts |
| `transcribe_audio` | `POST /v1/audio/transcriptions` | `audio_transcription` | transcript |
| `describe_image` | `POST /v1/chat/completions` with an image | `chat` | vision |
| `embed` | `POST /v1/embeddings` | `embeddings` | embeddings |
| `detect` | `POST /v1/detection` | `detection` | detection |
| `rerank` | `POST /v1/rerank` | `rerank` | rerank |
| `generate_video` | `POST /video` | `video` | video |
| `generate_sound` | `POST /v1/sound-generation` | `sound` | sound generation |
| `voice` | `POST /v1/voice/verify` or `/v1/voice/identify`, by `action` | `voice_recognition` | speaker recognition |
| `face` | `POST /v1/face/verify` or `/v1/face/identify`, by `action` | `face_recognition` | face recognition |
| `voice_profile` | `POST`, `GET` and `DELETE /api/voice-profiles`, by `action` (`create`, `list`, `delete`) | `audio_speech` | none |

`generate_image` repaints the white area of `mask` in `image` when you give both, and makes new images otherwise. `voice` and `face` take an `action`, `verify` (are these two the same?) or `identify` (who is this closest to?); `verify` needs a second file (`audio2` or `image2`). The tools stay few and short on purpose: every tool of the group is sent to the model on every turn, so related operations share one tool.

Every tool takes an optional `model`. When you leave it out, the tool uses the first installed model, by name, that has the use case. If there is none, the tool answers `no <capability> model installed`. `describe_image` uses the model that the **Chat image model** setting names, or an installed model that can look at images.

**Files.** A tool never carries file contents. A file argument (the image to edit, the recording to transcribe) is a reference, and a result (an image, audio, a video, the vectors of `embed`) is saved as a file and returned as a reference with its type, size and, for an image, its dimensions. In the assistant chat, a reference is a path in the user's files: `/workspace/home/...`, or a path relative to the home folder. Results go to the `generated` folder of the home folder. A chat of an agent that works in a folder reaches only that folder, exactly as the chat's own file tools do: a reference outside it is "file not found", and results go to the `generated` folder inside the agent's folder (or the chat's own folder when the agent's folder is read-only). The size limits are 20 MB for an image and 100 MB for a recording. A reference can also be a `data:` URL.

**Limits.** A tool refuses a request that is too large, with an error that states the limit: `steps` up to 150, `n` up to 4, video `seconds` up to 30 and `fps` up to 60, sound `duration_seconds` up to 600, `top_k` up to 50, up to 1000 documents for `rerank` and up to 256 texts for `embed`, and at most 1 MiB of text together for either. A user can have two image, video, sound or speech calls running at once; a third answers "busy" and the model can try again later.

`speak` does not take an uploaded reference recording, because the speech route does not. To speak in a cloned voice, save a voice profile with `voice_profile` and pass its `voice` value (a `localai://voice-profiles/...` reference) to `speak`.

**Voice profiles.** `voice_profile` keeps the voice-cloning profiles of the user who calls it, through the same routes as the [voice library]({{% relref "features/text-to-audio#voice-library" %}}), so the ownership rules apply: `list` shows the user's own profiles and the instance-wide ones (the result says that profiles created by an administrator are visible to all users, and it has the shape `{"profiles": [...], "note": "..."}`), `delete` removes one of the user's own (an administrator can delete any), and the profile of another user answers "no such profile". `create` takes `name`, `audio` (a reference to a WAV file in the user's files, 1 to 120 seconds), `transcript` (the exact words in the clip) and, optionally, `language`. It answers with the `id` and the `voice` value to give to `speak`. A user who is not an administrator may keep at most 100 MiB of reference audio in total. `speak` refuses a `voice` that is a file path for such a user, and says to use a voice of the model or one from `voice_profile`. A file that is not WAV is refused with a message that says so; the tool does not convert audio.

Cloning a voice without the consent of the speaker is abuse, so a model cannot create a profile alone. In a chat, `create` becomes a proposal: the chat shows a card with the name, the file, the transcript and the size, duration and first 8 hex characters of the SHA-256 of the recording, and the profile exists only after the person confirms that the speaker agreed. The server pins the recording at proposal time: when the person confirms, it reads the file again and refuses to create the profile if its SHA-256 or size changed. Then the model calls `voice_profile` with `list` to read the `voice` value. A run that starts by itself (a trigger or a job), a sub-agent and a messaging chat have nobody to confirm in Studio, so `create` is refused there. A run that starts by itself also refuses `delete`, whatever it holds; `list` works without `act.spend`, and `delete` works in a chat the person watches. Outside the chats, the standalone MCP server and the OpenAI-compatible chat endpoint cannot give the confirmation, so `create` answers that a person must confirm: make the profile in the Voice library instead. The tool needs no installed model.

**The `localai` skill.** The chats do not carry the usage notes of these tools in every prompt. The built-in skill `localai` has them: which tool to use for a task, the voice-cloning steps, how to chain tools (describe an image, then speak the description) and what to tell the person when a model is not installed (no model), a permission is refused (403) or a limit is hit (429). The model loads it by name when a task needs it. A chat lists the skill only while it has the client group.

`describe_image` and `transcribe_audio` also exist as built-in chat tools that read the user's files. A chat sends the model one tool of each name: in a chat, the **Images and audio** group serves these two, and the **LocalAI client** group serves the others.

`--read-only` also leaves out the client tools that write files or make new content: `generate_image`, `generate_video`, `generate_sound`, `speak`, `embed`, and the `create` and `delete` actions of `voice_profile`. The analysis tools (`transcribe_audio`, `describe_image`, `detect`, `rerank`, `voice`, `face`) and `voice_profile` `list` stay. In the standalone server, files are read from and written to the folder of `--files-dir`, and a file argument must lie inside it. There is no default folder: without `--files-dir` (or `LOCALAI_MCP_FILES_DIR`) the client tools that read or write files are not registered, and only `rerank` and `voice_profile` `list` and `delete` remain:

```bash
local-ai mcp-server --target http://remote.localai:8080 --api-key <key> --files-dir ./media
```

A user's API key is enough for the client tools. The management tools still need an administrator key.

## Adding new tools or skills

The MCP server lives at `core/services/localaitools/`. Tools are registered in `tools_*.go`; skill prompts (the markdown the LLM sees) are embedded from `prompts/`: one file per recipe under `prompts/skills/`, sent inline by the standalone server and offered as a skill by the chats. To add a new admin tool, add a method to the `LocalAIClient` interface plus implementations in `inproc/` and `httpapi/`, then register the tool and add or update a skill prompt. See `.agents/localai-assistant-mcp.md` for the full contributor checklist.
