+++
url = "/features/localai-client-tools/"
disableToc = false
title = "LocalAI client tools"
description = "Ask a chat for a picture, a recording or a ranking: the inference tools that every user can have in a chat, and the tool groups that carry them."
weight = 61
devbuild = "feature"
+++

Ask a chat to make a picture, a recording or a ranking, and it runs the inference tools of LocalAI for you. Every user can have these tools, within the permissions of the account. This page lists the tools, the tool groups that carry them and the tool search that loads them on demand. The tools that manage the server are for administrators: see [LocalAI Assistant]({{% relref "/features/chats-and-agents/localai-assistant" %}}).

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
- A client tool is offered when the account holds the feature permission in the table below and an installed model has the use case. A user with none of the features sees no client tool. `list_models` comes with them and needs no feature of its own.
- A sub-agent never has the admin group. A run that starts by itself (a trigger or a job) does not have it either.
- **Messaging chats.** A chat that comes from a messaging app (Telegram, Slack and so on) has the client group like any other chat, so a person can ask the bot for a picture, a sound or speech. The tools act as the owner of the chat, within the owner's permissions and installed models, and an agent that should not spend the owner's compute there switches the group off with `builtins_off`. The admin group is never offered in a messaging chat, whatever the chat or the agent switches on: the person writing may not be the owner, and the admin tools change the server. The Tools menu and `GET /api/chats/{id}/tools` give that as the reason. Saving a voice with `voice_profile` is refused there too, because the confirmation of the speaker's consent is given in Studio.
- The client tools in a run that starts by itself need the `act.spend` grant, like other tools that spend compute.
- `describe_image` and `transcribe_audio` stay in the **Images and audio** group of the chat, so a chat never has the same tool name twice. There they are aliases of the client tools of the same name: they use the helper models of **Chat image model** and **Chat transcription model** and read the files the chat can see, then run the same code as the client tools, as the person who is chatting.

### Tool search

Every tool the model is offered is sent to it again on every turn, and the two groups are large. On a model with a small context window that leaves little room for the conversation. So a chat can send three small tools in place of the two groups, and the model finds and runs the group tools through them:

| Tool | What it does |
|---|---|
| `tool_search` | Takes a few words (`query`, and `limit`, default 5, at most 20) and returns the best matching tools, each with a one-line description. |
| `tool_describe` | Takes a tool name and returns its full description and parameters. |
| `tool_call` | Takes a tool name and its `arguments` and runs the tool. |

The three tools are small. The setting `chats_tool_search` decides when a chat uses them:

- `auto` (default): when the schemas of the tools of both groups that the chat would send reach a tenth of the context window of the chat's model. The window is the chat's own context size setting, or else the context size of the model. If the window is not known, the groups are sent in full.
- `on`: always, when the chat has at least one tool in the groups. The tools of the user's MCP connectors (the Connectors page) go behind the three tools too.
- `off`: never. Every tool is sent in full.

The tools of MCP connectors load on demand only with `on`. With `auto` and `off` they are sent in full. With `on`, a chat with many connectors sends the three tools instead of every connector schema on every turn: `tool_call` runs the connector tool the chat would have offered, so the switches of the chat, the grants of a saved agent and the `act.spend` rule apply in the same way. The MCP servers of the model's own configuration are not affected.

The decision uses only the tools the chat would send and the window, so a chat gets the same answer on every turn. It changes only when you change the switches of the chat or the model.

Permissions do not change. `tool_call` runs the same tool a direct call would, with the same checks: the role, the switches of the chat, the ceiling of a saved agent, the `act.spend` rule of a run that starts by itself, and the account of the person who is chatting. `tool_search` and `tool_describe` list and describe only tools the chat could call. Any other name, whether the tool exists or not, gets the same answer: `no such tool`. The tools of other groups, like the file and web tools, are always sent in full; so are the tools of MCP connectors unless the setting is `on`. The tools panel (`GET /api/chats/{id}/tools`) marks a group that loads on demand with `deferred: true`, and the Tools menu shows it as "Loaded on demand".

The OpenAI-compatible API is not changed: `metadata.localai_assistant` still gives an administrator the admin tools there, and the client tools are not offered.

## Client tools

Besides the management tools above, the server registers a second group, the client tools. They run inference, so a person can ask for a picture, a recording or a ranking in a chat. The management tools are for administrators. A client tool acts as the user who calls it, like any other API request: the route checks the account's permission for the feature, the model allow-list and the quota apply, and the usage is logged for that user. When the server refuses, the tool tells the person which capability the account may not use.

| Tool | API route | Permission | Model use case |
|---|---|---|---|
| `generate_image` | `POST /v1/images/generations`, or `POST /v1/images/inpainting` when it gets `image` and `mask` | `images` | image |
| `upscale_image` | `POST /v1/images/upscale` | `images` | image upscaling |
| `transform_audio` | `POST /audio/transformations` | `audio_transform` | audio transformation |
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
| `projects` | the project routes of the chats API (`/api/chats/projects` and `/api/chats/{id}/project`, `/api/chats/{id}/projects`), by `action` (`list`, `get`, `create`, `update`, `archive`, `restore`, `delete`, `set_chat_project`, `pull`, `unpull`, `agents`, `connections`, `triggers`, `loops`, `chats`, `memory`, `write_memory`, `shared`, `shared_get`) | `chats`; `agents`, `connections` and `triggers` also need the feature of what they list | none |
| `list_models` | `GET /v1/models/capabilities` | none | none |

`generate_video` converts `size` to the `width` and `height` of the route, and `seconds` times `fps` (24 when unset) to `num_frames`, because the route does not read `size` or `seconds`. It also takes `start_image`, `end_image`, `audio`, `ref_images`, `ref_audios`, `ref_videos` (each one a list of frame image files with an optional `fps` and `audio`), `seed`, `cfg_scale` and `steps`. The files come from the chat's file store and travel as data URLs; the tool never sends a path, and it does not expose the free-form backend `params` of the route, because some backends read file paths from them. The route accepts at most 16 references of each kind, 512 frames per reference video and 128 MiB per file; the tool stops a call whose media weigh more than 256 MiB together.

`detect` takes `points` (flat x, y, label triples, label 1 or 0) and `boxes` (flat x1, y1, x2, y2 quads), up to 64 of each, as prompts for segmentation models. `voice` and `face` with `verify` take `anti_spoofing`; the face route then answers a liveness verdict and score for each image, and the tool returns them. The identify routes take a `store` field that their handlers do not read, so the tools leave it out.

`voice_profile` `create` takes a `description` and, next to the first `audio` and `transcript`, `extra_clips` (more clips of the same voice, each with its own transcript; at most 10 clips in all). The confirmation card lists every clip, and the confirmation pins all of them together: the size and duration are the totals, and the hash covers every clip in order, so a clip that changes after the person confirmed stops the creation.

**Which model a tool uses.** When the call names no `model`, the tools `generate_image`, `speak`, `transcribe_audio`, `embed`, `rerank`, `generate_video` and `describe_image` use the user's default for that kind of work, from `defaults.json` (see [Make LocalAI yours]({{% relref "/features/chats-and-agents/make-localai-yours#default-models" %}})), through one function (`Inference.choose` in `core/services/localaitools/defaults.go`), in this order: a `model` in the call; the default model, when it is installed; otherwise, with no default, the first installed model with the use case. A default of `"ask"` or a default that is not installed or is switched off stops the call with a message that tells the model to ask the user and call again with `model`; it never uses another model by itself. When a default decided, the result has a `model_note` ("Using flux.1-schnell, the user's default for images.") that the model repeats to the user. The other tools (`detect`, `generate_sound`, `upscale_image`, `transform_audio`, `voice`, `face`) have no default and keep the first-installed rule.

`speak` also takes `sample_rate` (8,000 to 192,000 Hz), which the TTS route passes to the model.

`generate_sound` takes the fields of the route: `lyrics`, `instrumental`, `caption`, `bpm`, `keyscale`, `timesignature`, `language`, `vocal_language`, `think`, `do_sample`, `prompt_influence` (the sampling temperature) and `expansion_model`, a model that expands the text first. Which of them a model reads depends on the model. The API does not accept a source audio on this route, so the tool has none. The model allow-list of the account applies to `expansion_model` too.

`generate_image` repaints the white area of `mask` in `image` when you give both. Otherwise it makes new images: `image` alone is the starting picture (img2img), `ref_images` are reference pictures for models that take them, and `seed` repeats a result. The tool sends these as the `file`, `ref_images` and `seed` fields of the generations route. `voice` and `face` take an `action`, `verify` (are these two the same?) or `identify` (who is this closest to?); `verify` needs a second file (`audio2` or `image2`). The tools stay few and short on purpose: every tool of the group is sent to the model on every turn, so related operations share one tool.

`upscale_image` takes `image`, `scale` (1 to 16, and the factor the upscaler model was built for) and an optional `model` with the upscale use case, and saves the enlarged image as a file.

`transform_audio` takes `audio`, an optional `reference`, `model`, `response_format` (`wav`, `mp3`, `ogg`, `flac`), `sample_rate` (8000 to 192000) and `params`. The `reference` is the far-end loopback for echo cancellation, or the target speaker for voice conversion. `params` is forwarded to the backend by the route, so the tool accepts only a short list of keys and checks each value: `noise_gate` (`true` or `false`), `noise_gate_threshold_dbfs` (a number from -120 to 0), `stem` and `language` (a short name, never a path) and `text` and `target_text` (plain text of up to 2000 bytes). Any other key is refused with the list of allowed ones. A model that separates sources (for example `htdemucs`) returns one file per stem in `stems`; the stem that the response body carried shares its file with `audio`, and the tool fetches at most 16 stems, only from the generated-audio folder of the server. See [Audio Transform]({{% relref "/models/capabilities/audio/audio-transform" %}}) for the route and the models.

`list_models` is how the model finds the name to give as `model`. It lists the models the account may use, by name, with their capabilities (`image`, `tts`, `transcript`, `sound_generation`, `embeddings`, `detection`, `video`, `rerank`, `upscale`, `audio_transform`, `chat` and others) and a short description when the model configuration has one. The optional `capability` argument narrows it, and `query` keeps the models whose name or description contains the text (any case); a few synonyms work (`speech`, `transcription`, `music`, `embedding`, `upscaling`, `denoise`, `separation`). It works for every user, not only administrators, because it reads `GET /v1/models/capabilities` as the account: the model allow-list of the account applies, and the answer has no file paths or settings. It sorts by name without regard to case and returns at most 60 models per call. `offset` and `limit` page through the list like a file read. The result gives `total`, `offset` and `returned`, and when more models remain a `hint` names the next call, for example `offset=60`. An offset past the end gives an empty list and a hint, not an error. It is offered together with the other client tools, and needs no feature permission and no installed model of its own. It differs from the administrator tool `list_installed_models`, which shows the instance's configuration (backend, pinned and disabled state) for all models.

Every tool takes an optional `model`. When you leave it out, the tool uses the first installed model, by name, that has the use case. If there is none, the tool answers `no <capability> model installed`. `describe_image` uses the model that the **Chat image model** setting names, or an installed model that can look at images.

**Files.** A tool never carries file contents. A file argument (the image to edit, the recording to transcribe) is a reference, and a result (an image, audio, a video, the vectors of `embed`) is saved as a file and returned as a reference with its type, size and, for an image, its dimensions. In the assistant chat, a reference is a path in the user's files: `/workspace/home/...`, or a path relative to the home folder. Results go to the `generated` folder of the home folder. A chat of an agent that works in a folder reaches only that folder, exactly as the chat's own file tools do: a reference outside it is "file not found", and results go to the `generated` folder inside the agent's folder (or the chat's own folder when the agent's folder is read-only). The size limits are 20 MB for an image and 100 MB for a recording. A reference can also be a `data:` URL.

**Limits.** A tool refuses a request that is too large, with an error that states the limit: `steps` up to 150, `n` up to 4, video `seconds` up to 30 and `fps` up to 60, sound `duration_seconds` up to 600, `top_k` up to 50, up to 1000 documents for `rerank` and up to 256 texts for `embed`, and at most 1 MiB of text together for either. A user can have two image, upscale, audio-transform, video, sound or speech calls running at once; a third answers "busy" and the model can try again later.

`speak` does not take an uploaded reference recording, because the speech route does not. To speak in a cloned voice, save a voice profile with `voice_profile` and pass its `voice` value (a `localai://voice-profiles/...` reference) to `speak`.

**Voice profiles.** `voice_profile` keeps the voice-cloning profiles of the user who calls it, through the same routes as the [voice library]({{% relref "/models/capabilities/audio/text-to-audio#voice-library" %}}), so the ownership rules apply: `list` shows the user's own profiles and the instance-wide ones (the result says that profiles created by an administrator are visible to all users, and it has the shape `{"profiles": [...], "note": "..."}`), `delete` removes one of the user's own (an administrator can delete any), and the profile of another user answers "no such profile". `create` takes `name`, `audio` (a reference to a WAV file in the user's files, 1 to 120 seconds), `transcript` (the exact words in the clip) and, optionally, `language`. It answers with the `id` and the `voice` value to give to `speak`. A user who is not an administrator may keep at most 100 MiB of reference audio in total. `speak` refuses a `voice` that is a file path for such a user, and says to use a voice of the model or one from `voice_profile`. A file that is not WAV is refused with a message that says so; the tool does not convert audio.

Cloning a voice without the consent of the speaker is abuse, so a model cannot create a profile alone. In a chat, `create` becomes a proposal: the chat shows a card with the name, the file, the transcript and the size, duration and first 8 hex characters of the SHA-256 of the recording, and the profile exists only after the person confirms that the speaker agreed. The server pins the recording at proposal time: when the person confirms, it reads the file again and refuses to create the profile if its SHA-256 or size changed. Then the model calls `voice_profile` with `list` to read the `voice` value. A run that starts by itself (a trigger or a job), a sub-agent and a messaging chat have nobody to confirm in Studio, so `create` is refused there. A run that starts by itself also refuses `delete`, whatever it holds; `list` works without `act.spend`, and `delete` works in a chat the person watches. Outside the chats, the standalone MCP server and the OpenAI-compatible chat endpoint cannot give the confirmation, so `create` answers that a person must confirm: make the profile in the Voice library instead. The tool needs no installed model.

**Projects.** `projects` manages the projects of the user who calls it, through the same routes as the Projects page, so the ownership rules apply: a user reaches their own projects and chats only. It needs no model. `list` and `get` return a short summary (id, name, slug, description, status, chat count, agent count) and the tools the project attaches (`attached`, five lists), and never the defaults of a project (its limit). `create` and `update` take `attached` and replace the whole block. In a chat the lists are added to the block and nothing is removed (detach on the project page), and the model may attach skills and collections, but it may not attach a connector, an add-on or an MCP server, because they can hold credentials: it is told to ask the person to do it on the project page. `agents` lists the agents made from a project (`project` is the id): id, name, description, model, files mode, whether the agent may write the project memory, and whether it is unavailable because the project is archived, deleted or its folders changed. `connections`, `triggers` and `loops` list what is bound to those agents: the messaging connections (id, kind, name, agent, enabled; never a token or a sender list), the triggers (with the next run of a schedule) and the loops of the chats that use them (the prompt is cut to 200 characters). `set_chat_project` puts the chat `chat_id` in `project` (an empty `project` takes it out); `pull` and `unpull` add or remove a project that the chat reads from, by reading the chat's list and writing it back, because the API replaces the whole list. A chat that is running cannot change its project (the API answers 409). `update` clears a description when it is given an empty string. `chats` lists the chats of a project (id, title, agent, time; never a message). `memory` reads the index and the names of the topic files of a project. `write_memory` (`project`, `file`, `content` as the whole new text, at most 16 KiB) writes one memory file as the user, the same as the Projects page does, for API and MCP callers. `shared_get` (`project` is the `ref` from `shared`) reads a shared project with its memory. In a chat the model may not run `pull`, `unpull`, `set_chat_project` or `delete`: they are for API and MCP callers and for the Projects page, and the model is told to ask the person. In a chat `write_memory` is refused too, and the model is told to use `propose_change` with scope `project`, so that the person confirms the note. Pulling a project into a chat is the person's choice. The tool is refused in a messaging chat and in a sub-agent, and a run that starts by itself may only `list`, `get`, `agents`, `connections`, `triggers`, `loops`, `chats` and `memory`. `shared` lists the projects that other people share with the user (`ref`, name, mode, status, owner display name; see [Sharing a project]({{% relref "/features/chats-and-agents/projects-reference#sharing-a-project" %}})). It is read only and works in a chat without a fence and for API and MCP callers; a chat inside a fence and a run that starts by itself refuse it, and so they do `shared_get`. A chat inside a fence (a project agent, or an agent with a folder) and a run that starts by itself see their own project only: `list` shows no other project, and `get`, `agents`, `connections`, `triggers`, `loops`, `chats` and `memory` refuse another project's id, so the names, descriptions and contents of other projects stay outside the fence. No action shares a project: sharing is the folder share of a signed-in person. The model writes to a project's memory with `propose_change` and scope `project`, not with `write_memory`.

**The `localai` skill.** The chats do not carry the usage notes of these tools in every prompt. The built-in skill `localai` has them: which tool to use for a task, the voice-cloning steps, how to chain tools (describe an image, then speak the description) and what to tell the person when a model is not installed (no model), a permission is refused (403) or a limit is hit (429). The model loads it by name when a task needs it. A chat lists the skill only while it has the client group.

`describe_image` and `transcribe_audio` also exist as built-in chat tools that read the user's files. A chat sends the model one tool of each name: in a chat, the **Images and audio** group serves these two, and the **LocalAI client** group serves the others.

`--read-only` also leaves out the client tools that write files or make new content: `generate_image`, `generate_video`, `generate_sound`, `upscale_image`, `transform_audio`, `speak`, `embed`, the `create` and `delete` actions of `voice_profile`, and every action of `projects` except `list`, `get`, `agents`, `connections`, `triggers`, `loops`, `chats`, `memory`, `shared` and `shared_get`. The analysis tools (`transcribe_audio`, `describe_image`, `detect`, `rerank`, `voice`, `face`), `voice_profile` `list` and `projects` `list`, `get`, `agents`, `connections`, `triggers`, `loops`, `chats`, `memory`, `shared` and `shared_get` stay. In the standalone server, files are read from and written to the folder of `--files-dir`, and a file argument must lie inside it. There is no default folder: without `--files-dir` (or `LOCALAI_MCP_FILES_DIR`) the client tools that read or write files are not registered, and only `rerank` and `voice_profile` `list` and `delete` remain:

```bash
local-ai mcp-server --target http://remote.localai:8080 --api-key <key> --files-dir ./media
```

A user's API key is enough for the client tools. The management tools still need an administrator key.
