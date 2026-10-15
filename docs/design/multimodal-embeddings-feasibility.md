# Multimodal embeddings feasibility inventory

This is Task 1 inventory, not native or runtime proof. **Native proof: PENDING.**
The approved all-modality specification remains unchanged. No downloads,
builds, services, model execution or GPU checks occurred.

## Machine-readable register

Unknown values are deliberately explicit, not guessed model configuration.

```json
{
  "schema_version": 1,
  "native_proof": "PENDING",
  "blockers": ["approval absent", "model/projector unknown", "fixture bytes absent", "native single-space proof absent"],
  "candidate": "EmbeddingGemma 2 (candidate only)",
  "model_identity": "unknown",
  "model_sha256": "unknown",
  "projector_identity": "unknown",
  "projector_sha256": "unknown",
  "license": "unknown; model and projector rights require review",
  "native_revision": "f2cc7282ce0042641df55e0e563b75e824e2b647",
  "localai_revision": "83418c8996e5e25db2c8c6a62253001155778572",
  "modality_evidence": {"text": "PENDING", "image": "PENDING", "audio": "PENDING", "silent-video": "PENDING", "audiovisual-video": "PENDING", "mixed": "PENDING"},
  "dimensions": "unknown",
  "pooling": "unknown",
  "decoder_versions": "host ffmpeg and ffprobe 6.1.1-3ubuntu5; packaged runtime unknown",
  "hardware": "Linux 6.8.0-139-generic x86_64; CPU model, RAM and usable accelerator unknown",
  "preprocessing_version": "mm-v1-f4-tsms-a16k-mono",
  "commands_results": ["df -B1 .: 3045408768 bytes available, filesystem 100% used", "command -v: python3, ffmpeg, ffprobe, protoc, go, cmake, c++ found; not build suitability proof", "ffmpeg -version; ffprobe -version: 6.1.1-3ubuntu5", "git rev-parse HEAD: 83418c8996e5e25db2c8c6a62253001155778572", "read-only git show/git grep f2cc7282: pinned source present; no candidate artifact documentation found in docs/models/tools/mtmd"],
  "peak_storage": "unknown until model/projector sizes and build strategy are selected; approval blocked",
  "approval": "absent; root must authorize downloads, native versus Docker build, isolated runtime and paths"
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

Commands and output are retained in `.sdd-work/task1/local-inventory.log` and
`.sdd-work/task1/source-inventory.log`. Tool presence is not codec, packaging,
compile, security, runtime or GPU proof. Sibling llama-decisions at a different
revision and LocalRecall MiniLM text-only results cannot establish this feature.

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

## Clarification register (root decision before native work)

These interpretations preserve numeric bounds and do not amend the spec:

1. Pixel limits count source pixels, not bytes: 64 megapixels of RGB alone
   occupy 192 MB. The 256 MiB temporary-storage limit is not a process RSS cap.
   Decode/process/release selected frames incrementally; do not retain a whole
   expanded video. Track actual allocation lifetimes and encoded source storage.
2. A finite model context can reject 120 selected frames plus full soundtrack
   with 413. Never reduce fps, truncate audio or silently discard parts to fit.
3. Proposed timestamp syntax is `Timestamp: <integer> ms\n`, elapsed from first
   PTS. Root must finalize rounding/nonmonotonic timestamp rules and version them
   before native implementation; 4 fps and no duplicate selected frames stand.
4. Resampling/downmix parameters depend on verified projector requirements and
   remain unknown. Root must freeze rates, sample format, channel policy and
   cumulative allocation accounting under a final preprocessing version.
5. Exact duration endpoints and preparation-clock accounting require root
   clarification: proposed inclusive duration limits, cumulative monotonic
   preparation time across probe/decode/fetch excluding inference; slot held
   through inference and batch completion. No counters may reset between items.

## Task 7 prerequisites and storage gate

Available space was 3,045,408,768 bytes (filesystem reports 100% used). This is
not adequate evidence to authorize a build. Compute peak additional disk as
model + projector + download/conversion staging + source/dependencies + object
and link outputs + packaged runtime/container layers + fixtures/reports +
concurrent temporary media + safety reserve, subtracting only verified reusable
read-only artifacts at the required revision. Every term is currently unknown;
a numeric peak cannot honestly be asserted. No shared-cache deletion is allowed.

Root must select native versus Docker, approve the peak estimate against fresh
free-space measurements, authorize exact paths/downloads/licenses, and isolate
runtime ports/configuration. Verify compiler/dependency versions and decoder
codec/protocol/process cleanup support before long work. Native proof must then
record artifact checksums, newly built binary identity, hardware, dimensions,
pooling, every modality and complete soundtrack consumption. Missing approval,
artifacts or platform runners are blockers, never skipped tests or PASS.

Inventory verification is intentionally narrower than runtime verification.
The Task 1 checker rejects every native PASS until a later task supplies and
validates actual runtime evidence. Inventory success cannot advertise native
all-modality support.

## Native proof protocol (fixed before any retrieval score)

Task 7 fixes these settings before any embedding score is computed. Version:
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
