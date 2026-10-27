# Multimodal embeddings feasibility inventory

This started as the inventory. The native run below was added later.
**Native proof: FAIL (recorded).** All seven input forms run natively and the
audio and soundtrack changes move the vectors, but 2 of the 12 frozen retrieval
queries miss the frozen margin. See the native run results section. The approved
specification is unchanged and nothing was retuned.

## Machine-readable register

Unknown values are deliberately explicit, not guessed model configuration.

```json
{
  "schema_version": 1,
  "native_proof": "FAIL",
  "blockers": [
    "text-to-audio-drum margin 0.005249 is below the frozen 0.01 minimum (audio-typing 0.6947 vs audio-drum 0.6999)",
    "text-to-audio-water margin 0.006343 is below the frozen 0.01 minimum (audio-typing 0.5816 vs audio-water 0.5879)",
    "no retuning is allowed: an alternative candidate or a contract revision needs a maintainer decision"
  ],
  "candidate": "EmbeddingGemma 2 (candidate; native run recorded)",
  "model_identity": "AtomicChat/embeddinggemma-2-GGUF@96a5415769c8a56d2557846c4ac23b9f724231be:embeddinggemma-2-Q8_0.gguf",
  "model_sha256": "60b2a9e5f0673aa25931d285a73ae712e447fcc3439330102a8eabea31da7c73",
  "projector_identity": "AtomicChat/embeddinggemma-2-GGUF@96a5415769c8a56d2557846c4ac23b9f724231be:mmproj-embeddinggemma-2-Q8_0.gguf",
  "projector_sha256": "bebe3951e09e0b232422fb58c9d6f9f271640808a6bfd698f9c65696b315308c",
  "license": "GGUF metadata and both model cards say apache-2.0; no LICENSE file in either repository; base card links the Gemma 4 license page and a Prohibited Use Policy; redistribution terms need owner review",
  "native_revision": "f2cc7282ce0042641df55e0e563b75e824e2b647",
  "localai_revision": "83418c8996e5e25db2c8c6a62253001155778572",
  "modality_evidence": {
    "text": "PASS",
    "image": "PASS",
    "audio": "PASS",
    "silent-video": "PASS",
    "audiovisual-video": "PASS",
    "mixed": "PASS"
  },
  "dimensions": 768,
  "pooling": "mean",
  "decoder_versions": "host ffmpeg and ffprobe 6.1.1-3ubuntu5 (probe-side decoder only; packaged runtime unknown)",
  "hardware": "AMD Ryzen 9 9950X3D 16-Core Processor, 20 logical cores, 90659504128 bytes RAM, Linux 6.8.0-139-generic x86_64, CPU only",
  "preprocessing_version": "mm-v1-f4-tsms-a16k-mono",
  "commands_results": [
    "approved native build: cmake -DGGML_NATIVE=ON -DLLAMA_CURL=OFF (Release, CPU) of the pinned source, done before the probe run",
    "bash test/multimodal/verify.sh native-proof (re-runs the probe when MM_MODEL, MM_PROJECTOR, MM_UPSTREAM_SOURCE, MM_BUILD_DIR, MM_DECODER_DIR and MM_APPROVAL_RECORD are set): MM_* set, python3 test/multimodal/native_probe/probe.py, exit status 3 = recorded FAIL, 21 of 23 cases passed, 677.8 s",
    "backend: llama-server version: 0.6.0-dev (build 1, commit f2cc7282c), sha256 a200455a44a460a82f7918bc9d40d6a1bc3ef09ca0fe3c6c9d6af21530446974",
    "server: --embeddings -c 16384 -b 16384 -ub 16384 -np 1 --no-warmup, /v1/embeddings, one input item per request",
    "report: test/multimodal/native-proof-report.json (raw float32 vectors, token counts, similarities, margins)"
  ],
  "peak_storage": "model 309855520 bytes + projector 554821120 bytes, source checkout 247 MB, CPU build 115 MB (all before the probe); the probe wrote about 1 MB of scratch and used no GPU; free space on the work volume stayed above 24 GB",
  "approval": "owner-recorded approvals: native builds and model downloads under an isolated scratch area; no remote execution"
}
```

## Local evidence and its limits

Read-only source inspection uses locally cached llama.cpp Git objects at the
full revision above, never the cache checkout's HEAD:

- `git show f2cc7282:tools/mtmd/README.md` describes separate model-specific
  projectors and unified image/audio input. It does not prove EmbeddingGemma 2
  compatibility or embedding quality. Candidate artifact/model card, immutable
  model/projector revisions, hashes and license are unavailable locally.
- `git grep -n -i embeddinggemma f2cc7282 -- docs models tools/mtmd` returned
  no matches. This is not a claim that the model is unsupported everywhere.
- `tools/server/server-context.cpp:6049` dispatches content arrays through
  `tokenize_oai_content_array`; its definition is in `server-common.cpp:1248`.
- `tools/mtmd/mtmd-helper.cpp` video support uses raw video frames and optional
  `MTMD_VIDEO` compilation. The upstream video path does not supply complete
  soundtrack extraction. LocalAI still needs bounded audiovisual preparation.
- Host ffmpeg configuration includes `--enable-gpl`; redistribution/license
  compatibility and minimal packaged codec closure remain unverified.

Command output was kept in local logs that are not committed. Tool presence is not codec, packaging,
compile, security, runtime or GPU proof. Results from other local models at a different
revision, and text-only results, cannot establish this feature.

## Frozen acceptance and planned fixtures

`test/multimodal/acceptance-contract.json` fixes 12 retrieval queries: three
per direction, three unrelated distractors each, image/audio/video queries
against text in one model/projector/pooling/preprocessing space. It fixes all
seven input forms, reordered batch, same-text media changes, identical-frame
soundtrack changes, mixed-part consumption and failures. The rank-one cosine
margin is at least 0.01; media/soundtrack distance is at least 0.001. These are
pre-inference correctness thresholds, not measured quality or general benchmarks.
Ties fail. Preserve raw vectors, similarities and margins. No prompt tuning,
captioning, OCR or transcription substitutes are permitted.

The manifest describes contributor-owned two-second recordings/renderings of
bell, drum, pouring water and typing. These bytes **do not exist yet**; expected
tracks/durations are design requirements, not probe observations. Planned hashes
are null, not hashes of filenames or empty data. Materialization requires actual
SHA-256 and rights confirmation before any native acceptance. The soundtrack
pair must reuse identical decoded video frames and replace the entire audio
track. Fixture production must not use acceptance scores to select examples.

## Clarification register (maintainer decision before native work)

These interpretations preserve numeric bounds and do not amend the spec:

1. Pixel limits count source pixels, not bytes: 64 megapixels of RGB alone
   occupy 192 MB. The 256 MiB temporary-storage limit is not a process RSS cap.
   Decode/process/release selected frames incrementally; do not retain a whole
   expanded video. Track actual allocation lifetimes and encoded source storage.
2. A finite model context can reject 120 selected frames plus full soundtrack
   with 413. Never reduce fps, truncate audio or silently discard parts to fit.
3. Proposed timestamp syntax is `Timestamp: <integer> ms\n`, elapsed from first
   PTS. Maintainers must finalize rounding/nonmonotonic timestamp rules and version them
   before native implementation; 4 fps and no duplicate selected frames stand.
4. Resampling/downmix parameters depend on verified projector requirements and
   remain unknown. Maintainers must freeze rates, sample format, channel policy and
   cumulative allocation accounting under a final preprocessing version.
5. Exact duration endpoints and preparation-clock accounting require maintainer
   clarification: proposed inclusive duration limits, cumulative monotonic
   preparation time across probe/decode/fetch excluding inference; slot held
   through inference and batch completion. No counters may reset between items.

## Prerequisites and storage gate

Available space was 3,045,408,768 bytes (filesystem reports 100% used). This is
not adequate evidence to authorize a build. Compute peak additional disk as
model + projector + download/conversion staging + source/dependencies + object
and link outputs + packaged runtime/container layers + fixtures/reports +
concurrent temporary media + safety reserve, subtracting only verified reusable
read-only artifacts at the required revision. Every term is currently unknown;
a numeric peak cannot honestly be asserted. No shared-cache deletion is allowed.

Maintainers must select native versus Docker, approve the peak estimate against fresh
free-space measurements, authorize exact paths/downloads/licenses, and isolate
runtime ports/configuration. Verify compiler/dependency versions and decoder
codec/protocol/process cleanup support before long work. Native proof must then
record artifact checksums, newly built binary identity, hardware, dimensions,
pooling, every modality and complete soundtrack consumption. Missing approval,
artifacts or platform runners are blockers, never skipped tests or PASS.

Inventory verification is intentionally narrower than runtime verification.
The inventory checker rejects every native PASS until later work supplies and
validates actual runtime evidence. Inventory success cannot advertise native
all-modality support.

## Native proof protocol (fixed before any retrieval score)

These settings are fixed before any embedding score is computed. Version:
`mm-v1-f4-tsms-a16k-mono`. The same values are in the `preprocessing` block of
the acceptance contract. Callers must include the version in their
embedding-space identity.

- Text: bare UTF-8, no retrieval prefix, no chat template, no separators.
- Image: the fixture PNG bytes, unchanged.
- Audio: complete first audio track, converted by `ffmpeg` with `-ac 1 -ar 16000
  -c:a pcm_s16le` (default swresample resampler) into a WAV file. The projector
  reports a 16 kHz audio sample rate. Standalone audio fixtures use the same step.
- Video expansion, in order: the text part `Video:\n`; for each selected frame
  the text part `Timestamp: <integer> ms\n` followed by the frame as an image
  part; when the first audio track exists the text part `Audio track:\n`
  followed by the audio part.
- Frame selection: sample times 0, 250, 500, ... ms while below the video stream
  duration (4 fps). Each sample takes the first decoded frame, in presentation
  order, whose elapsed time is at or after the sample time. A frame is never
  selected twice. The timestamp is the selected frame's elapsed milliseconds from
  the first presentation timestamp, rounded half up. Frames are decoded to RGB at
  their native resolution and encoded as PNG.
- Pooling and normalization are the model defaults of the pinned server (pooling
  type from the GGUF metadata, L2 normalization). Server arguments:
  `--embeddings -c 16384 -b 16384 -ub 16384 -np 1 --no-warmup`.
- One input item per request. The 12 queries, targets, distractors, the 0.01
  retrieval margin and the 0.001 distance thresholds are those of the frozen
  contract; nothing is tuned.
- Additional decisions: the mixed item is the contract order text, image, audio,
  then the audiovisual video expansion. The no-dropped-parts case requires the
  token count of the mixed item to match the sum of its parts minus the repeated
  special tokens within 2 tokens, and the leave-one-out vectors to differ from
  the full item by at least the media-change distance.

## Native adapter run (contract v2)

The probe below scores the upstream `llama-server`. The LocalAI backend adapter
was scored separately, through `/v1/embeddings` and the `llama-cpp` gRPC backend
built at llama.cpp `78a8ae14be559b45f5f13ddee82c188b683d5d8d`, with the same model
and projector hashes. Contract v1 was not retuned. Contract
`multimodal-acceptance-v2-adapter` was frozen, with new recordings, in the commit
that added it and before any v2 score existed. Result: **FAIL, 12 of 14 scored
cases**. The failures are `text-to-audio-drum` and `audio-to-text-water`. The v1
fixtures scored through the adapter (video not applicable) give 7 of 9 scored cases
and the same two text-to-audio failures, with margins 0.0062 and 0.0050 against the
upstream server's 0.0052 and 0.0063. The raw vectors are in
`native-proof-v2-vectors.json` and `native-proof-v1-adapter-vectors.json`, and
`check_adapter_proof.py` recomputes every number. The adapter publishes no video, so
the video rows of v1 stay as the upstream server recorded them.

## Native run results (exact pin)

Outcome: **FAIL, recorded.** Machine-readable evidence:
`test/multimodal/native-proof-report.json`, checked by
`bash test/multimodal/verify.sh native-proof` (exit status 3 for a recorded FAIL).
The probe is `test/multimodal/native_probe/probe.py`; the fixtures come from
`test/multimodal/native_probe/generate_fixtures.py`.

Run command (artifact paths come from environment variables; the approval record
is a JSON file with the maintainer's recorded approvals and the license review):

```
MM_MODEL=<model.gguf> MM_PROJECTOR=<mmproj.gguf> MM_UPSTREAM_SOURCE=<llama.cpp checkout at the pin> \
MM_BUILD_DIR=<cmake build dir> MM_DECODER_DIR=<dir with ffmpeg and ffprobe> \
MM_APPROVAL_RECORD=<approval.json> python3 test/multimodal/native_probe/probe.py
```

Identity and runtime:

- Model: AtomicChat/embeddinggemma-2-GGUF at 96a5415769c8a56d2557846c4ac23b9f724231be,
  `embeddinggemma-2-Q8_0.gguf`, sha256 `60b2a9e5f0673aa25931d285a73ae712e447fcc3439330102a8eabea31da7c73`, architecture
  `gemma-embedding2`. Projector `mmproj-embeddinggemma-2-Q8_0.gguf`, sha256
  `bebe3951e09e0b232422fb58c9d6f9f271640808a6bfd698f9c65696b315308c` (vision `gemma4v`, audio `gemma4a`). Upstream base model:
  google/embeddinggemma-2 (revision 914f7f89142e33e77833254d9c9b90c3cef7303b when fetched).
- Output: 768 dimensions, mean pooling (GGUF `pooling_type` 1), L2-normalized
  output (all vector norms are 1 within 1e-6). 16384-token server context. The
  server reports vision, audio and video capability.
- Source: llama.cpp `f2cc7282ce0042641df55e0e563b75e824e2b647`, CPU Release build,
  server binary sha256 `a200455a44a460a82f7918bc9d40d6a1bc3ef09ca0fe3c6c9d6af21530446974`.
- Hardware: AMD Ryzen 9 9950X3D, 20 logical cores, 90.7 GB RAM, no GPU.
  Decoder: ffmpeg and ffprobe 6.1.1-3ubuntu5 (probe side only).
- Special tokens added per item: 2 (BOS and EOS).

License. Model-card statements, quoted:

- Quantized repository front matter: `license: apache-2.0`, `base_model:
  google/embeddinggemma-2`, `base_model_relation: quantized`. Its body says
  "google/embeddinggemma-2, revision 914f7f8, Apache 2.0." Both GGUF files carry
  `general.license = apache-2.0`. No LICENSE file is in the repository.
- Base model card front matter: `license: apache-2.0`. Header line: "License:
  Apache 2.0" linking `https://ai.google.dev/gemma/docs/gemma_4_license`. Safety
  section: "Deployments must adhere to the Gemma Prohibited Use Policy". No LICENSE
  file is in the repository, and the repository is not gated.
- Open question for maintainer review: the cards say Apache 2.0, but they also link the
  Gemma license page and a Prohibited Use Policy. Confirm the redistribution
  terms before shipping the weights or documenting them as Apache 2.0.

Required items:

| Item | Verdict | Evidence |
| --- | --- | --- |
| Plain text, structured text | PASS | one 768-d vector each, 9 tokens, identical content |
| Image | PASS | 85 tokens |
| Audio (`input_audio` WAV) | PASS | 54 tokens for 2 s |
| Silent video (frames only) | PASS | 8 frames, 735 tokens |
| Audiovisual video | PASS | 8 frames plus 32785 samples of audio, 792 tokens |
| Mixed ordered item | PASS | 934 tokens; sum of parts minus repeated special tokens is 934 |
| No dropped parts | PASS | leave-one-out distances from the full item: image 0.002943, audio 0.001005, video 0.077505; floor 0.001. The audio ablation is only marginally above the floor |
| Media change (same text, bell vs drum image) | PASS | cosine distance 0.086461 (>= 0.001) |
| Soundtrack change (identical frames, bell vs drum track) | PASS | cosine distance 0.003933 (>= 0.001) |
| Single identity and space | PASS | one server instance, one model, projector, pooling, 768 dimensions |
| 12 retrieval queries | FAIL | 10 of 12 pass; 2 miss the 0.01 margin |

Retrieval (cosine of query to target, to the best distractor, and the margin):

| Query | Target | Best distractor | Margin | Verdict |
| --- | --- | --- | --- | --- |
| text-to-image-bell | image-bell | 0.7426 | 0.6342 | 0.1084 | PASS |
| text-to-image-drum | image-drum | 0.7507 | 0.6714 | 0.0793 | PASS |
| text-to-image-water | image-water | 0.7574 | 0.5417 | 0.2157 | PASS |
| text-to-audio-bell | audio-bell | 0.6682 | 0.6364 | 0.0318 | PASS |
| text-to-audio-drum | audio-drum | 0.6999 | 0.6947 | 0.0052 | FAIL |
| text-to-audio-water | audio-water | 0.5879 | 0.5816 | 0.0063 | FAIL |
| text-to-video-bell | video-bell | 0.7036 | 0.6138 | 0.0898 | PASS |
| text-to-video-drum | video-drum | 0.7366 | 0.6607 | 0.0759 | PASS |
| text-to-video-water | video-water | 0.7048 | 0.5214 | 0.1834 | PASS |
| media-to-text-bell | text-bell | 0.7426 | 0.6714 | 0.0712 | PASS |
| media-to-text-drum | text-drum | 0.6999 | 0.6355 | 0.0644 | PASS |
| media-to-text-water | text-water | 0.7048 | 0.6249 | 0.0799 | PASS |

The two failures are both text-to-audio and both rank the target first with a
margin under 0.01: `text-to-audio-drum` (0.6999 against audio-typing 0.6947) and
`text-to-audio-water` (0.5879 against audio-typing 0.5816). Nothing was retuned.
Whether to accept this candidate, change the candidate, or revise the contract
(with a new contract version) is a maintainer decision.

Supplementary, not gating: a 7-item batch in the contract permutation returned
indexes 0 to 6 in request order and each vector matched the single-item vector
(cosine at least 0.9999999). Repeated requests with identical content returned
bit-identical vectors.

Weaknesses of this evidence:

- The fixtures are synthetic clip-art and procedural sounds, 2 s each, four
  concepts. Margins are small-sample measurements, not a quality benchmark.
- The video path is a probe-side construction (frames plus audio in one content
  array). The model has no learned temporal alignment between frames and audio.
- The soundtrack-change distance of 0.003933 is above the frozen floor of 0.001
  but small. Audio tokens are few compared with 735 frame tokens.
- The server-side vectors come from the upstream HTTP server, not the future
  LocalAI backend, so tokenization of the same content array is assumed equal.
- The report is machine-validated but a self-produced file; the checker cannot
  prove that it came from a real run. The server log hash and binary hash are
  recorded for audit.
- The run used a Q8_0 quantization from a third party, not the base weights.
