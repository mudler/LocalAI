# Typed addon contract

`ParseManifest` is the strict v1 parser used by staging and package verification.
Unknown fields, duplicate keys, invalid declarations, and unsupported compatibility ranges fail validation.
The typed declarations remain additive strict v1 fields, not a new protocol version.

- `capabilities.ui.homeCommands` names installed pages, never tools or install actions.
- `capabilities.executionProvider` names execution tools and one declared UI-only read status tool.
- `capabilities.hostOperations.required` and `.optional` request exact operation names, never grants.
- `capabilities.provides` says which provider slots of the host the add-on serves (`providerslots.go`, `provider-slots.json`). A slot is a job the host does itself and lets an add-on do in its place; the person picks one provider. The manifest names the slot, the version it was written for, tools for the roles of the slot and typed properties. It is an optional key under `api` 2.
- `capabilities.network` declares the egress policy of the commands an add-on runs (kind `egress-check`, a title, preset titles). It attaches the `network-policy` host control.

`ValidHostOp` validates syntax only. The host's registered vocabulary determines support.
Administrator policy and request admission remain authoritative, including for optional operations.
Status is advisory; an isolation report does not prove network enforcement or authorize execution.

See [the addon guide](../../../docs/content/addons/contract-reference.md#optional-typed-manifest-extensions)
for JSON examples, bounds, collision rules, lifecycle behavior, and policy migration.

Typed owner observations currently support local-owner JSON selections only.
Named-user and SQL selections remain unavailable and fail closed; they are not completed scope.

## Optional translations

The title and the description of an add-on are translated by the language maps of `ui.title` and `ui.description`.
The top-level `i18n` field translates the text of connector kinds, by language tag:

```json
{"i18n":{"it":{"kinds":{"slack":{"title":"Slack","description":"Parla con uno dei tuoi agenti da Slack."}}}}}
```

The set of fields is closed: only the title and the description of a declared kind, and only where the English kind has them.
Tool descriptions, schemas and skills are for the model and stay English. English is the manifest itself, so `en` is refused.
Tags follow the same rule as the language maps, at most 16, and all translated text has at most `MaxI18nChars` characters.
`NegotiateLanguage` picks the language of a request: the explicit one, else the `Accept-Language` header, else English.
See [the addon guide](../../../docs/content/addons/contract-reference.md#translated-text).

## Optional model actions

`capabilities.ui.modelActions` maps a model capability to a declared page:

```json
{"modelActions":[{"capability":"FLAG_IMAGE","page":"studio","path":"images"}]}
```

`capability` and `page` are required. `path` is an optional nonempty relative
suffix. There is one mapping per capability per addon, at most 27 mappings.
The SDK route bounds apply to `page/path`: 256 ASCII characters, eight segments,
and 1–64 characters per segment from `[A-Za-z0-9._~-]`, excluding `.` and `..`.
Absolute paths, URLs, queries, fragments, percent encoding and backslashes fail.
Unknown, duplicate and noncanonical keys fail strict parsing.

The exact vocabulary is `ModelActionCapabilities()`: all concrete flags in
`config.GetAllModelConfigUsecases()` except native `FLAG_CHAT` and
`FLAG_COMPLETION` and aggregate `FLAG_LLM`. `FLAG_ANY` is not a concrete flag.
A core/config parity test guards this list without importing core into contract.
Declarations carry no priorities, labels, permissions or model query templates.
Apps exposes a cloned typed list only through its existing installed UI view;
unavailable UI omission is unchanged. Protocol registration uses the same
`contract.Capabilities`, and verifies exact equality including these actions.

Studio owns image/video/TTS/sound destinations; Talk owns transcription at its
existing landing page; Face and Voice own face/speaker recognition respectively.
Talk does not currently consume a selected-model query value. Its pipeline and
landing semantics remain unchanged. Frontend dispatch and route cleanup are a
separate milestone; this contract alone does not change model Open behavior.

The allowed capability strings are:

```text
FLAG_EDIT FLAG_EMBEDDINGS FLAG_RERANK FLAG_IMAGE FLAG_TRANSCRIPT FLAG_TTS
FLAG_SOUND_GENERATION FLAG_TOKENIZE FLAG_VAD FLAG_VIDEO FLAG_DETECTION
FLAG_VISION FLAG_FACE_RECOGNITION FLAG_SPEAKER_RECOGNITION FLAG_AUDIO_TRANSFORM
FLAG_DIARIZATION FLAG_SOUND_CLASSIFICATION FLAG_REALTIME_AUDIO FLAG_SCORE
FLAG_DEPTH FLAG_TOKEN_CLASSIFY FLAG_3D FLAG_3D_ANIMATION FLAG_DECISIONS
FLAG_UPSCALE FLAG_MOTION FLAG_FORECAST
```

## Context files

`capabilities.contextFiles` names up to three small index files (one for each scope: user, agent, channel) that the host loads into the prompt of a chat.

```json
{"contextFiles":[{"scope":"user","path":"Memory/index.md"},{"scope":"agent","path":"Memory/agents/{agent}/index.md","cap":1024}]}
```

Every path lies under `Memory/`, ends in `.md` and holds no parent segment. An agent path holds `{agent}` once and a channel path holds `{channel}` once; the host fills them from the chat, never from the model. `cap` lowers the 2 KiB index cap; it cannot raise it. The add-on declares and the host reads: the add-on gains no access to the files.
