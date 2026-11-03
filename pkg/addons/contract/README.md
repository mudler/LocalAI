# Typed addon contract

`ParseManifest` is the strict v1 parser used by staging and package verification.
Unknown fields, duplicate keys, invalid declarations, and unsupported compatibility ranges fail validation.
The typed declarations remain additive strict v1 fields, not a new protocol version.

- `capabilities.ui.homeCommands` names installed pages, never tools or install actions.
- `capabilities.executionProvider` names execution tools and one declared UI-only read status tool.
- `capabilities.hostOperations.required` and `.optional` request exact operation names, never grants.

`ValidHostOp` validates syntax only. The host's registered vocabulary determines support.
Administrator policy and request admission remain authoritative, including for optional operations.
Status is advisory; an isolation report does not prove network enforcement or authorize execution.

See [the addon guide](../../../docs/content/features/addons.md#optional-typed-manifest-extensions)
for JSON examples, bounds, collision rules, lifecycle behavior, policy migration, and old-host staging refusal.
Old hosts must retain the old package when an extension-bearing upgrade is refused.
Release maintainers select the minimum supporting release and the duration of any legacy package support.

Typed owner observations currently support local-owner JSON selections only.
Named-user and SQL selections remain unavailable and fail closed; they are not completed scope.

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
