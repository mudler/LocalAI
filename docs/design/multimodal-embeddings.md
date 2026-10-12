# Multimodal embeddings in LocalAI

Date: 12 October 2026
Status: proposed written specification; architecture and all-modality scope approved, implementation awaits written-spec approval.
Baseline: LocalAI `a551c83465ee538367358f239de5a364ebea9329`.
Pinned llama.cpp: `f2cc7282ce0042641df55e0e563b75e824e2b647`.

## Goal and ownership

Extend LocalAI embeddings to accept text, images, audio, video, and ordered mixtures through its existing HTTP API. Preserve existing text consumers.

LocalAI owns input validation, bounded media decoding, model loading, inference, and response encoding. Dante owns retrieval prompts, source segmentation, source retention, embedding-space identity, and reindexing. LocalRecall owns supplied-vector storage and retrieval.

This change does not add Dante ingestion, query UI, indexing profiles, or LocalRecall proxy routes. It does not replace media with generated captions, OCR, or transcripts.

## Design choice

Use the existing embedding endpoint and an additive typed backend contract. Process one input item per backend invocation initially. This preserves result boundaries without changing existing pooling semantics.

A separate llama-server deployment was considered and rejected: it duplicates inference lifecycle and deployment responsibilities. Reusing unstructured string fields was rejected: old backends could silently embed JSON as text.

## HTTP contract

Both existing embedding endpoint aliases retain their authentication, authorization, and response envelope.

Existing string and token-array inputs retain their behavior. A new input can be a content object or an array containing strings and content objects. Do not reinterpret strings as JSON.

```json
{
  "model": "multimodal-embedding-model",
  "input": [
    {
      "content": [
        {"type": "text", "text": "Find related evidence"},
        {"type": "image_url", "image_url": {"url": "data:image/png;base64,..."}},
        {"type": "input_audio", "input_audio": {"data": "...", "format": "wav"}},
        {"type": "input_video", "input_video": {"url": "https://media.example/clip.mp4"}}
      ]
    },
    "a separate text input"
  ],
  "encoding_format": "float"
}
```

Each input item produces exactly one vector. All parts in a content object contribute to that vector, in order. Batch indexes retain request order. Return no partial success envelope when an item fails.

Supported part types:

- `text`: a string, passed without implicit separators, retrieval prefixes, or chat templates.
- `image_url`: a URL containing HTTP(S) or a base64 data URI. Version 1 supports PNG and JPEG.
- `input_audio`: exactly one of `data` or `url`. `data` contains raw base64; `url` contains HTTP(S) or a data URI. Supported formats are WAV, MP3, and FLAC.
- `input_video`: exactly one of `data` or `url`, with the same source rules. `video_url` is an alias with the same nested fields.

Version 1 supports MP4 with H.264 and optional AAC, plus WebM with VP8/VP9 and optional Opus. Other containers and codecs return an unsupported-media error.

For a supplied audio `format`, require consistency with the detected bytes. Reject both `data` and `url` rather than choosing one silently. This is intentionally stricter than upstream llama.cpp.

Reject unknown fields in new content objects, duplicate keys, empty content arrays, malformed media, unsupported part types, and conflicting `messages` plus structured `input`. Legacy parsing is not tightened incidentally. Structured batches cannot contain nested token arrays; callers use the existing token-only form separately.

Preserve existing `dimensions` and float/base64 response handling. Validate actual vector lengths and finite values. Reject zero vectors for new structured input. Do not assume 768 dimensions or normalize twice.

## Video semantics

The pinned upstream video helper embeds visual frames only. It does not include the soundtrack. LocalAI must not reuse that behavior while claiming audiovisual support.

Expand a video at its original content position into:

1. A `Video:` text marker.
2. Frames sampled on the media timeline at 4 frames per second, starting at zero. Use the first decoded frame at or after each sample time; do not repeat a frame when the source rate is lower.
3. A timestamp marker before each selected frame, expressed as elapsed milliseconds from the first presentation timestamp.
4. If present, the complete first audio track, preceded by an `Audio track:` text marker.

The resulting sequence is joint audiovisual input, not synchronized frame/audio attention. The API does not promise temporal alignment learned by the model.

Reject videos with multiple video streams or multiple audio tracks instead of silently choosing content. Silent videos require vision capability. Videos with sound require vision and audio capabilities. Undecodable or unsupported soundtracks fail the item. Documented frame sampling is allowed; accidental truncation of duration, parts, or audio is not.

Use deterministic decoding and record preprocessing-version details in the documentation. Callers incorporate that version into their embedding-space identity.

## Resource and security contract

The following initial bounds apply only to requests using structured input. Existing global limits can impose smaller bounds.

| Resource | Maximum |
| --- | --- |
| Encoded HTTP body | 48 MiB |
| Input items per request | 16 |
| Source parts across a request | 128 |
| Text across a request | 1 MiB UTF-8 |
| Media bytes after base64 decoding or HTTP transfer | 32 MiB across a request |
| Duration per audio/video part | 30 seconds |
| Aggregate audio/video source duration | 60 seconds; a video's soundtrack does not count twice |
| Sampled video frames across a request | 120 |
| Pixels per source image or video frame | 16 megapixels |
| Pixels across decoded images and selected video frames | 64 megapixels |
| Channels in source audio | 2 |
| Source audio sample rate | 192 kHz |
| Aggregate decoded/resampled audio allocation | 64 MiB |
| Fetch duration per source | 30 seconds |
| Media preparation, including probe/decode | 60 seconds across the request |
| Temporary media storage | 256 MiB per request |
| Concurrent media preparations per frontend | 2; excess requests receive 429 |

These are hard bounds, not reasons to truncate. Validate arithmetic before allocation. Bound decoder stdout, stderr, and temporary files. Stop decoding when runtime counters exceed limits, even if probe metadata lies. Reject model context overflow instead of dropping parts.

Resolve HTTP(S) media in Go. Use the hardened HTTP client and validated-IP dialing. Reject private, loopback, link-local, multicast, unspecified, and reserved destinations, including IPv4-mapped IPv6 forms. Disable redirects, proxies from the environment, URL credentials, and forwarded client authentication. Require a successful HTTP status. Recheck every new dial against approved addresses while preserving TLS hostname validation.

Do not expose file paths, `file://`, caller-controlled decoder arguments, or backend URL fetching. Decode media from bounded private files or buffers. Subprocesses receive explicit argv, restricted protocol access, request cancellation, and process-group cleanup. Reject external playlist references. Never invoke a shell with user content.

Go fetches and validates encoded media. The backend probes and decodes media; it never opens a user-supplied URL. Each invocation receives the remaining request budgets. A typed result reports consumed frames, pixels, audio allocation, and source duration. Go deducts consumption before dispatching the next item. The backend enforces budgets during decoding, not only after completion. The frontend holds its preparation slot until the batch finishes, including inference.

Compressed-media bytes travel across RPC; expanded frame buffers do not. Keep serialized RPC messages below the existing 50 MiB transport ceiling. Validate `proto.Size` before dispatch. A 32 MiB request-wide media limit leaves headroom for metadata and text.

Media limits are versioned constants initially, not a new configuration subsystem. The preparation limiter is deliberately per frontend. Existing backend scheduling controls inference concurrency. Documentation explains multiplied capacity in multi-frontend deployments.

Do not log media bytes, URLs with credentials or query strings, local paths, or model input text. Return sanitized error messages.

## Backend and capability contract

Add a dedicated `MultimodalEmbedding` RPC with a typed request. Its response wraps the existing embedding result and typed resource consumption. The request carries ordered parts, binary media bytes, remaining resource budgets, model identity, and existing embedding inference options. Do not place structured input into the legacy `Embeddings` string.

A dedicated RPC makes old backends return `UNIMPLEMENTED`, rather than ignore unknown fields and return text-only vectors. Keep the legacy `Embedding` RPC unchanged.

Extend `ModelMetadataResponse` with a protocol version and supported embedding modalities. Distinguish silent-video and audiovisual-video capability. Publish these fields before any chat-template-dependent early return.

Check metadata for the selected loaded model/backend before fetching media. The backend probes video tracks before the second, content-specific capability check and before inference. A silent-video-capable model may accept a silent clip; a clip with sound also requires audiovisual capability.

Missing capability fields, unavailable metadata, and old backend RPCs fail closed for structured input. Text requests continue through the legacy path. Backend implementations also validate modalities and limits; frontend checks are not the only guard.

Capabilities belong to a backend/model instance, not a process-wide model-name cache. Invalidate cached metadata on unload, reload, backend restart, and model identity changes. Local and distributed transports carry the new RPC and enforce the same rejection behavior.

Only llama.cpp implements the new RPC in this change. Other backends retain text support and return an explicit unsupported error for structured requests.

Use native mtmd media tokenization and the embedding task path. Factor focused helpers out of the large C++ adapter rather than adding another monolithic request handler. Preserve ordered parts, pooling, usage accounting, and cancellation. No captioning or transcription model participates.

## Errors and lifecycle

Use the existing API error envelope, with these classifications:

- 400: malformed structure, contradictory fields, empty input, or invalid option combination.
- 413: byte, decoded-resource, duration, part-count, or context-budget overflow.
- 415: unsupported or mismatched media format.
- 422: the selected model cannot consume a requested modality.
- 429: media preparation capacity exhausted.
- 501: backend protocol or required media implementation unavailable.
- 502: source fetch failure, corrupt backend result, or inference transport failure.
- 504: preparation or configured inference deadline exceeded.

Propagate client cancellation through fetch, decode, RPC, and model work. Release slots, files, child processes, and backend tasks on every exit. No partial response escapes after cancellation or a failed batch item.

## Model and runtime acceptance

EmbeddingGemma 2 is the initial candidate, not an assumed proof of all-modality support. Verify matching model/projector artifacts, checksums, license, output dimensions, pooling, and native modality behavior before declaring completion.

Require a single compatible embedding space for the full acceptance matrix. Using unrelated models for each modality does not establish cross-modal retrieval. If no candidate satisfies this requirement, report a blocker; do not substitute captions or reduce the approved scope.

Record model and projector identities, LocalAI/backend revisions, decoder versions, preprocessing settings, and hardware. Provision downloads and native builds only after checking available storage and build prerequisites. Obtain approval before long builds, as required by the repository guide.

Mandatory real-runtime matrix through HTTP, Go, RPC, and the newly built backend:

- Plain text, structured text, image, audio, silent video, audiovisual video, and a mixed item.
- A reordered mixed batch returns matching response indexes and stable dimensions.
- Paired media with identical surrounding text produces meaningfully different vectors; media is not ignored.
- Identical video frames with different soundtracks produce distinguishable embeddings.
- Text-to-image, text-to-audio, text-to-video, and media-to-text retrieval fixtures rank their intended match first among at least three unrelated distractors.
- At least three fixed queries per retrieval direction pass without tuning prompts against the acceptance fixture.
- Image-query and audio-query fixtures use the same model space as their indexed targets.
- A mixed audiovisual item runs without dropping any part. Frame sampling and soundtrack inclusion match the declared policy.
- Missing projector, missing decoder, unsupported model, old backend, malformed media, overflow, cancellation, and model reload behave as specified.

Cross-modal fixtures are bounded correctness checks, not a general quality benchmark. Record actual similarities and margins, not only PASS labels.

## Automated regression coverage

Use TDD for request parsing, resource accounting, typed transport, capability checks, and errors. Mocks may test routing and failures; they do not replace real-model acceptance.

Cover nil/empty fields, unknown and duplicate keys, mixed batches, float/base64 encoding, dimensions, token-only requests, and existing text/messages behavior. Reject media-only objects instead of returning an empty successful response.

Security fixtures cover redirects, DNS rebinding, IPv6, misleading content lengths, decompression bombs, malformed timestamps, forged probe metadata, decoder output overflow, and cancellation cleanup. Use owned fixtures only.

Transport tests include old servers, missing metadata fields, local and distributed routes, backend reload, and concurrent requests. Verify the new RPC does not bypass model identity or routing controls.

Build and test the affected Go packages and llama.cpp adapter. Run normal repository CI targets without lowering coverage gates. Exercise Linux runtime acceptance. Compile supported affected platform variants, or state a concrete platform blocker before release.

Update `docs/content/features/embeddings.md`, request schemas, and existing capability documentation in the implementation commits. Document source schemes, supported formats, strict field rules, video soundtrack semantics, resource limits, examples, model setup, and error behavior.

## Implementation sequence

1. Validate the model/projector and decoder feasibility against the pinned backend. Establish fixed media fixtures and a provenance record.
2. Add typed parsing and bounded media acquisition, with negative and legacy regression tests.
3. Add the dedicated RPC, capability metadata, model lifecycle rules, and local/distributed routing tests.
4. Implement bounded native media preparation and llama.cpp inference for all modalities, including video soundtracks.
5. Wire the HTTP response path and publish documentation and verified model configuration.
6. Run the complete real-model matrix, security/lifecycle tests, normal CI, and independent Go/C++ reviews. Fix findings before opening the implementation PR.

Each step retains previous text behavior. No partial all-modality claim is published while the runtime matrix remains incomplete.

## Source evidence

At the baseline, `core/http/middleware/request.go` handles legacy string/token inputs, not content objects. `core/backend/embeddings.go` passes text/token input to the legacy RPC. `backend/cpp/llama-cpp/grpc-server.cpp` uses `tokenize_input_prompts` for embeddings.

At the pinned llama.cpp revision, `tools/server/server-context.cpp` dispatches content objects through `tokenize_oai_content_array`. `tools/server/server-common.cpp` defines media part shapes. `tools/mtmd/mtmd-helper.cpp` decodes video frames without audio extraction.

Existing LocalAI validated-IP dialing in `core/http/endpoints/localai/cors_proxy.go` is a security reference. Existing unbounded base64 fetch helpers are not sufficient for this feature.
