# 🎉 LocalAI 4.11.0 Release! 🚀

<h1 align="center">
  <br>
  <img height="300" src="https://raw.githubusercontent.com/mudler/LocalAI/refs/heads/master/core/http/static/logo.png">
  <br>
  <br>
</h1>

LocalAI 4.11.0 is out!

Fifteen days and 243 pull requests. This release turns audio into a scene rather than a transcript, gives operators a failover layer that works across local and remote models, and adds first-class structured decision models. A Parakeet pipeline can now report what was said, who said it, and which sounds happened. Registered voices can replace anonymous speaker IDs with names, and Studio can create a reusable speaker profile from clean intervals in an ordinary multi-speaker recording.

Model serving gained ordered failover chains with probes, trip and recovery policy, warm fallbacks, distributed state, pinning, metrics, and a `localai-proxy` backend. SystemOne routes can use native decision models or GLiNER zero-shot extraction. Galleries can be shipped as signed OCI artifacts with digest verification and safe extraction. Kimodo adds text-to-humanoid motion through `/3d/animate`, while single-node installs get a useful operations dashboard again.

**Highlights:**

- 🎙️ **Audio scenes and remembered speakers** - Parakeet combines ASR, Nemotron diarization, CED sound classification, realtime speaker events, and named voice matching. `/v1/audio/diarization` can export portable speaker profiles, and Studio lets an operator preview clean intervals, name a speaker, and register the profile explicitly.
- 🔀 **Model failover chains** - one model name can route across ordered local, aliased, or remote targets. LocalAI probes health, trips failing targets, recovers with dwell rules, keeps warm fallbacks loaded, emits routing headers and realtime switch events, and exposes pinning through REST, MCP, metrics, and the Web UI.
- 🧭 **Structured decisions** - `/v1/systemone`, `/permute`, and `/separate` now route by explicit model capability. Native `vllm_decide` models use the generic `Score` RPC; GLiNER models provide zero-shot extraction through `TokenClassify`.
- 📦 **Signed OCI galleries** - gallery sources can use `oci://`, with manifest validation, safe extraction, digest checks, staged caches, signed official fallbacks, and `source_repository` policy checks for reusable signing workflows.
- 🕺 **Kimodo text-to-motion** - `POST /3d/animate` creates animated skeleton GLBs from text. Studio detects animation-capable models, renders the skeleton, stores it in 3D history, and records backend-provided usage and traces.
- 🖥️ **This machine dashboard** - single-node deployments can inspect CPU, RAM, disk, GPU state and running model processes, open logs, and stop a model from **Operate → This machine**.
- 🧠 **92 more gallery entries** - the gallery grew from 1,568 to 1,660 entries, including NeMo Speech ASR and diarization, Qwen-Image 2.1, Kev and CLM decision models, and a large batch of text, vision and quantization variants.

Plus SGLang thinking budgets, runtime video LoRAs, explicit image `negative_prompt`, system backend aliases, worker version reporting, DRM VRAM accounting, 42 bug-fix PRs, and 118 engine and dependency updates.

---

## 📊 This release in numbers

| | |
|---|---|
| Pull requests merged | **243** |
| Commits | 353 |
| Files changed | 623 (**+51,035** / -2,625) |
| Development window | 15 days (2026-09-17 to 2026-10-02) |
| Git authors | 18 |
| Gallery entries | 1,568 to **1,660** (+92) |

Where the work landed:

| Area | Change |
|---|---|
| `core/` | +23,883 / -807 across 350 files |
| `backend/` | +12,437 / -186 across 119 files |
| `gallery/` | +4,998 / -110 |
| `pkg/` | +3,310 / -170 across 61 files |
| `swagger/` | +2,254 / -100 |
| `docs/` | +2,142 / -1,115 across 50 files |

---

## 📌 TL;DR

| Area | Summary |
|------|---------|
| 🎙️ **Audio scenes** | `parakeet-cpp` can pair ASR with Nemotron-3 diarization and CED sound classification. Transcription segments and words can carry speakers; realtime sessions emit speaker and sound events. Voice registry matches can add names. `include_speaker_profiles=true` exports clean intervals and portable embeddings for explicit profile enrollment. |
| 🔀 **Failover** | A model config can define ordered `failover.targets`, probes, trip thresholds and recovery rules. Requests retry only before bytes are committed. `localai-proxy` forwards supported APIs to another LocalAI. Operators get REST, SSE, MCP, metrics, realtime events, and UI controls for health and pinning. |
| 🧭 **Decisions** | `known_usecases: [decisions]` is a first-class capability. Native decision models use `Score` and `vllm_decide`; GLiNER models use dynamic `TokenClassify` labels. SystemOne validates bodies, question counts and question-specific constraints before dispatch. |
| 📦 **OCI galleries** | `oci://` sources support relative entries, immutable digest pulls, optional Cosign policies, staged caches and strict path confinement. Official HTTP and GitHub sources now have signed OCI fallbacks. `source_repository` pins reusable-workflow signatures to the expected caller repository. |
| 🕺 **Animation** | `kimodocpp` and `POST /3d/animate` turn text into skeleton `.glb` output. Studio renders animation results. Generic backend metadata carries usage under `metadata.usage`; traces report load, queue and inference timing. |
| 🖥️ **Operations** | **Operate → This machine** restores single-node visibility with resources, running process metrics, logs and model shutdown. Distributed node details now show worker version and live loaded models. `/system` can report Linux DRM VRAM per local model. |
| 🧠 **Models** | The gallery grew by 92 entries. Highlights include NeMo Speech diarized ASR, Qwen-Image 2.1, Kev and CLM decision models, plus consolidated model and quantization batches. |
| 🛠️ **Reliability** | 42 bug-fix PRs cover backend concurrency, watchdog eviction, SGLang compatibility, realtime API keys, reasoning streams, tool grammar, symlink resolution, Anthropic refusals, pre-stream error handling, and Python 3.12 FunASR dependencies. |

---

## 🚀 New Features & Major Enhancements

### 🎙️ Audio scenes: speech, speakers and sounds

`parakeet-cpp` now handles three parts of an audio scene in one backend: transcription, speaker diarization, and sound-event classification.

An ASR model can attach `diarization_model:<path>` and `sound_model:<path>`. A diarization model can attach `asr_model:<path>`. `diarization_latency` selects `model`, `low`, `very_low`, or `ultra_low` latency. `/v1/audio/transcriptions` splits segments at speaker changes and labels both segments and words. Set `diarize=false` to disable attribution for one request.

`/v1/audio/diarization?include_text=true` returns transcript text when an ASR companion is loaded. `/v1/audio/classification` scores ten-second CED windows and averages each class; `threshold` filters classes and `top_k=0` returns all of them.

Realtime sessions emit `conversation.item.input_audio_transcription.segment` and `conversation.item.sound_detection`. `pipeline.diarization` enables speaker events for committed `server_vad` turns. Live timestamps and speaker indices are turn-local, not session-global.

> 🔗 PRs: #12335

### 🗣️ Name and remember speakers

The voice registry now participates in diarization. Configure `speaker_model:<path>`, with optional `speaker_threshold` and `speaker_margin`, to match diarized speaker embeddings against registered voices. Matched JSON segments keep their `SPEAKER_NN` label and add `name` and `name_score`; realtime events add `speaker_name`.

`include_speaker_profiles=true` on `/v1/audio/diarization` exports a versioned profile object for each usable speaker: encoder identity, dimension, numeric speaker slot, clean speech duration, clean intervals, and the embedding. It requires a JSON response format. Unsupported backends return 501 rather than silently dropping the profile request.

`POST /v1/voice/register` accepts an exported profile with an explicit `speaker_slot`. Profile enrollment and audio enrollment are mutually exclusive. Studio adds a diarization workflow that lets an operator preview clean intervals, assign a name, and save the speaker deliberately.

Profiles are unsigned biometric data, not proof of identity or consent. The registry remains global, process-local, ephemeral, and unsynchronized across frontends. Browser storage does not retain vectors or recordings, and profile bodies are excluded from API traces.

![Speaker diarization profiles in Studio](https://raw.githubusercontent.com/mudler/LocalAI/release/v4.11-notes/release-screenshots-v4.11.0/ui-diarization-speakers.png)

> 🔗 PRs: #12382, #12414, #12420

### 🔀 Model failover chains and `localai-proxy`

A model name can now represent an ordered list of local models, aliases, or remote targets:

```yaml
name: assistant-llm
failover:
  targets:
    - model: primary-local
    - model: remote-backup
    - model: warm-local-backup
      warm: true
  probe:
    interval: 15s
    timeout: 5s
  trip:
    errors: 1
    window: 30s
  recovery:
    probes: 3
    min_dwell: 60s
```

Transport failures, HTTP 5xx, rate limits and resource exhaustion can trip a target. Normal 4xx validation errors do not. LocalAI retries only before it commits response bytes; bodies above 32 MiB are not replayed. Warm local fallbacks stay loaded and are exempt from idle and LRU eviction.

Responses identify the actual target with `X-LocalAI-Served-Model`; fallback responses can add `X-LocalAI-Failover`. Management endpoints under `/api/failover` provide snapshots, per-chain state, an SSE event stream and admin pinning. MCP tools, OpenTelemetry metrics, distributed PostgreSQL/NATS state, a Web UI, and `localai.model.failover` realtime events use the same chain state.

`localai-proxy` forwards supported LocalAI APIs to another LocalAI instance. Configure `proxy.upstream_url`, `upstream_model`, credentials and request timeout. Set `known_usecases` explicitly because remote capabilities cannot be inferred.

![Failover chain health and active targets](https://raw.githubusercontent.com/mudler/LocalAI/release/v4.11-notes/release-screenshots-v4.11.0/ui-failover.png)

> 🔗 PRs: #12285

### 🧭 Structured decisions and zero-shot extraction

SystemOne is now capability-routed instead of assuming that every scoring backend is a decision model.

GLiNER2.5 adds zero-shot extraction through `TokenClassify` with per-request labels. The endpoints are `POST /v1/systemone`, `/v1/systemone/permute`, and `/v1/systemone/separate`. The native decision path uses the generic `Score` RPC and vllm.cpp's `vllm_decide`.

Decision models declare:

```yaml
backend: vllm-cpp
known_usecases:
  - decisions
```

`/v1/systemone` supports choice, `noul`, and score questions. NER models serve the extraction path and the permutation/separate variants. Request bodies are limited to 64 KiB and 64 questions, with validation for state, IDs, choices, score levels, and boolean criteria.

The gallery adds Kev 0.8B, GLiNER models, Laya and CLM decision models. Decisions are also discoverable in `/api/instructions` and the installed-model UI.

> 🔗 PRs: #12140, #12247, #12373, #12391, #12397, #12428

### 📦 Signed OCI galleries

Gallery sources and mirrors can use `oci://registry/repository:tag`. LocalAI validates the manifest before downloading layers, confines every `org.opencontainers.image.title` beneath the destination, rejects duplicates and traversal, limits gallery artifacts to 512 layers and 64 MiB, verifies each digest, and promotes the cache only after `index.yaml` parses.

Signed pulls resolve a mutable tag to a digest, verify that digest, and pull the same digest. Relative gallery-entry URLs resolve from the unpacked OCI root. Existing HTTP, GitHub, Hugging Face and file galleries also gain directory-relative entries with traversal protection.

Official model and backend source order now ends with signed OCI fallbacks on Quay. Publication creates an immutable commit tag, signs the digest with keyless Cosign/OIDC, and only then moves the public tag. `artifact_verification` separates gallery trust from backend-image verification. `source_repository` additionally checks the Fulcio caller-repository extension for shared signing workflows.

> 🔗 PRs: #12167, #12182, #12235

### 🕺 Kimodo text-to-motion

The new `kimodocpp` backend turns a text prompt into a humanoid skeleton animation in binary glTF format. Linux amd64 and arm64 support CPU and Vulkan; Apple Silicon supports CPU.

`POST /3d/animate` accepts a text input and parameters such as `frames`, `steps`, `text_guidance`, and `seed`. Results use `url` or `b64_json`. Studio's 3D workflow detects animation-capable models, switches to capability-driven fields, renders the skeleton in a dedicated viewer, and keeps it in 3D history.

Backends can attach opaque metadata to the generic result. Kimodo reports text tokens as input units and effective `frames × steps` as output units under `metadata.usage`, using accounting rule `frame_steps_v1`. Animation traces include model loading, queueing, inference, output bytes, and metadata.

> 🔗 PRs: #12095, #12162, #12184

### 🖥️ Operate this machine

Single-node installations once again get a useful runtime dashboard instead of an empty worker page. **Operate → This machine** shows VRAM, RAM, CPU and model-disk gauges, a CPU-only state, host memory split by model, and a sortable running-model table with backend, resident memory, CPU share, uptime and PID.

Operators can open model logs and stop a model through the existing shutdown API. **Operate → Running now** shows the five largest local model processes. `/system` adds optional process data, while `/api/resources` adds logical cores, CPU usage, load and model-disk information.

Linux DRM backends can also report `loaded_models[].size_vram`. The value is a point-in-time resident-device-memory reading, not a scheduler reservation and not safe to sum as exclusive physical VRAM usage.

![Single-machine resource and running model dashboard](https://raw.githubusercontent.com/mudler/LocalAI/release/v4.11-notes/release-screenshots-v4.11.0/ui-this-machine.png)

> 🔗 PRs: #12189, #12026

### 🖧 Better distributed node inspection

Workers and agent workers now register their LocalAI version and build commit. The node detail view shows the version and fetches a live list of loaded models with state and in-flight request count. Older workers remain compatible and display an unknown version.

> 🔗 PRs: #12328

### 🧰 Smaller features worth knowing about

- **SGLang thinking budgets.** `thinking_budget:<tokens>` is forwarded through strict-thinking sampling. `reasoning_default:on|off` sets the model default, and request metadata can override it. Use `engine_args.enable_strict_thinking: true`.
- **Runtime video LoRAs.** `video_lora_dir` exposes prompt-selected adapters with `<lora:name:strength>` without fusing them into base weights.
- **Explicit image negative prompts.** `/v1/images/generations` accepts `negative_prompt` while retaining `positive|negative` compatibility.
- **System backend aliases.** System packages can publish hardware-specific binaries through `metadata.json` and one hardware-neutral backend alias.
- **Deployment-level audio.cpp default.** `AUDIOCPP_DEFAULT_BACKEND` selects the packaged backend when the model does not contain an explicit `backend:` option.

> 🔗 PRs: #12193, #12119, #12031, #12141, #12133

---

## 🐛 Bug Fixes (recap)

**Backend lifecycle and concurrency:**
- Serialize llama.cpp RPC CGo calls to prevent rare concurrent crashes - #12316
- Propagate model identity for concurrent backend calls - #12244
- Clean watchdog state and ignore stale backend evictions - #12299, #12333
- Lower default watcher concurrency to reduce memory pressure - #12315
- Clean idle models using effective LRU policy - #12233

**SGLang and Python backends:**
- Fix ROCm and Blackwell SGLang compatibility paths - #12338, #12362
- Keep FunASR on Python 3.12-compatible Transformers and tokenizers - #12402
- Respect llama-box GPU split settings - #12228
- Repair Qwen MTP tokenizer classification - #12246

**Streaming and API behavior:**
- Add realtime WebSocket API-key support - #12271
- Fix streamed reasoning extraction and blank thinking output - #12269, #12180
- Generate stable JSON-schema tool grammar for llama.cpp - #12153
- Surface Anthropic refusals instead of returning an empty reply - #12424
- Do not stream pre-response errors as assistant content - #12425
- Let startup fail when forced backend initialization fails - #12113

**Models, galleries and files:**
- Resolve gallery models through symlinks - #12132
- Make gallery conflict consolidation deterministic - #12124, #12221
- Correct Qwen-Image 2.1 RPC op-count synchronization - #12190
- Fix dead documentation and example links - #12320, #12392, #11546

---

## 🧠 Models

The gallery grew from 1,568 to 1,660 entries, a net increase of 92.

Audio: NeMo Speech Sortformer diarization, Nemotron 3.5 streaming ASR, Parakeet TDT 0.6B v3, combined diarized ASR configurations, CED Tiny/Base, and Parakeet realtime/offline scene variants.

Decisions and extraction: Kev 0.8B, GLiNER 2.5, Laya, Nimble and CLM decision entries for the SystemOne APIs.

Image and vision: Qwen-Image 2.1 Q4_K and Q8_0 with reference-image editing, plus additional Qwen3.8 vision and projector variants.

Text and reasoning: Ternary Bonsai 2 27B, NeoHorse variants, Qwen3.8 distills and Flash Next low-bit variants, Occamy, Hy-MT2, Maple Preview, Qwen3.8 Cyber and many additional consolidated entries.

> 🔗 PRs: #12124, #12221, #12265, #12190, #12391, #12397

---

## 👒 Dependencies

Submodule and engine pin bumps this cycle:

| Project | Bumps |
|---|---|
| ikawrakow/ik_llama.cpp | 14 |
| 0xShug0/audio.cpp | 14 |
| CrispStrobe/CrispASR | 13 |
| ggml-org/llama.cpp | 11 |
| ServeurpersoCom/omnivoice.cpp | 7 |
| leejet/stable-diffusion.cpp | 7 |
| PrismML-Eng/llama.cpp | 6 |
| mudler/vllm.cpp | 6 |
| ggml-org/whisper.cpp | 6 |
| NVIDIA/NeMo-Speech.cpp | 5 |
| TheTom/llama-cpp-turboquant | 3 |
| mudler/parakeet.cpp | 4 |
| localai-org/ced.cpp | 2 |
| PABannier/sam3.cpp, localai-org/voice-detect.cpp, antirez/ds4 | 1 each |

There are 118 dependency-labelled pull requests in the release range, including Python packages, GitHub Actions, UI dependencies, backend wheels and model-engine pins.

---

## 📖 Documentation

The documentation now covers decision models and SystemOne, mixed CPU/GPU inference, optional proxy API keys, and the current GPU environment-variable behavior. Eleven dead documentation links and several Discord, Slack, Telegram and chatbot example links were repaired. Gallery documentation was simplified by removing per-model sections that had become hard to keep current.

> 🔗 PRs: #12428, #12392, #12320, #12286, #12222, #12143, #12111, #12109, #11546

---

## 🙌 New Contributors

- @H-XX-D made their first contribution in #12245
- @lqp made their first contribution in #12239
- @PINYOPATTANAWASANPORN made their first contribution in #12392
- @pratikgx made their first contribution in #12140
- @yzxcj797 made their first contribution in #12103

Thanks also to @Anai-Guo, @blackd, @leilei3167, @pos-ei-don, @richiejp, @SuperMarioYL and @walcz-de for their contributions this cycle.

<!-- Release notes generated using configuration in .github/release.yml at 58830f7ac508845a6f4efa32cfca06af422d4d82 -->
<!-- Release notes generated using configuration in .github/release.yml at 58830f7ac508845a6f4efa32cfca06af422d4d82 -->

## What's Changed
### Bug fixes :bug:
* fix(models): hide .tar.bz2 and .sha256 files from the model list by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12122
* fix(xsysinfo): include AMD GTT in APU VRAM detection by @leilei3167 in https://github.com/mudler/LocalAI/pull/12094
* fix(ui): make text selection visibly contrast the background by @blackd in https://github.com/mudler/LocalAI/pull/12123
* fix(gallery): strip oci:// before upgrade-check digest lookup by @mudler-agent in https://github.com/mudler/LocalAI/pull/12138
* fix: preserve vllm-omni imports after backend relocation by @mudler-agent in https://github.com/mudler/LocalAI/pull/12137
* fix: correct stale symbol names in doc comments by @mudler-agent in https://github.com/mudler/LocalAI/pull/12139
* fix(cosignverify): find a bundle the index entry describes badly by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12165
* fix(api): report an alias's target in /v1/models/capabilities by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12183
* fix(make): fail the protoc download on an HTTP error by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12187
* fix(auth): require validated header credentials for CSRF exemption by @richiejp in https://github.com/mudler/LocalAI/pull/12185
* fix(openai): return HTTP errors for pre-stream failures and report per-slot context by @mudler-agent in https://github.com/mudler/LocalAI/pull/12204
* fix(xsysinfo): avoid startup hang on intel_gpu_top by @leilei3167 in https://github.com/mudler/LocalAI/pull/12206
* fix(sglang): support msgspec-based ServerArgs (sglang >= 0.5.20) by @pos-ei-don in https://github.com/mudler/LocalAI/pull/12155
* fix(vllm-omni): remove invalid syntax in test.py by @H-XX-D in https://github.com/mudler/LocalAI/pull/12135
* fix(turboquant): extend D512 flash-attn patch to all turbo V types by @mudler-agent in https://github.com/mudler/LocalAI/pull/12234
* fix(gallery): strip the oci:// scheme before every registry lookup by @mudler-agent in https://github.com/mudler/LocalAI/pull/12238
* fix(gallery): tie oci:// gallery caches to the verification policy by @mudler-agent in https://github.com/mudler/LocalAI/pull/12239
* fix(gallery): verification follow-ups for oci:// galleries by @mudler-agent in https://github.com/mudler/LocalAI/pull/12243
* fix(backend): re-probe MediaMarker after cold vision model load by @leilei3167 in https://github.com/mudler/LocalAI/pull/12254
* fix(gallery): read the models dir once per gallery listing by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12283
* fix(swagger): describe backend metadata as an object by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12178
* fix(compose): request NVIDIA compute capability by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/11990
* fix(modelartifacts): reuse committed sibling files for narrowed allow_patterns by @SuperMarioYL in https://github.com/mudler/LocalAI/pull/11484
* fix(responses): wait for complete JSON tool calls by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12001
* fix(responses): preserve streamed output items by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12048
* fix(kokoros): add missing animate3_d stub to Backend trait impl by @mudler-agent in https://github.com/mudler/LocalAI/pull/12301
* fix(ollama): report on-disk size for /api/tags and /api/ps by @leilei3167 in https://github.com/mudler/LocalAI/pull/11989
* fix(quantization): pin the producing backend on imported quantized models (#11875) by @Anai-Guo in https://github.com/mudler/LocalAI/pull/11879
* fix: return correct HTTP status codes for saturation and no-nodes-available by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12113
* [router] fix: re-seed the knn corpus index when the vector store comes back empty by @walcz-de in https://github.com/mudler/LocalAI/pull/12267
* fix(functions): honor function_arguments_key when building the tool grammar by @Anai-Guo in https://github.com/mudler/LocalAI/pull/11677
* fix(models): fallback to application config default context size in /v1/models/capabilities (#12202) by @PINYOPATTANAWASANPORN in https://github.com/mudler/LocalAI/pull/12216
* fix: point docker-compose default at gallery phi-2-chat by @leilei3167 in https://github.com/mudler/LocalAI/pull/11987
* fix(distributed): stage the files a model install declares by @mudler-agent in https://github.com/mudler/LocalAI/pull/12309
* fix(gallery): keep model deletion inside the models directory by @mudler-agent in https://github.com/mudler/LocalAI/pull/12324
* fix: make the remaining VerifyPath checks effective by @mudler-agent in https://github.com/mudler/LocalAI/pull/12326
* fix(llama-cpp): keep llama.cpp's default cache_ram instead of no limit by @walcz-de in https://github.com/mudler/LocalAI/pull/12297
* fix(huggingface): list repos nested more than one directory deep by @mudler-agent in https://github.com/mudler/LocalAI/pull/12355
* fix(react-ui): extract text from PDF attachments in chat and home by @mudler-agent in https://github.com/mudler/LocalAI/pull/12374
* fix(vllm-cpp): annotate the hf_overrides config.json read for gosec by @mudler-agent in https://github.com/mudler/LocalAI/pull/12380
* fix(watchdog): ignore stale backend evictions by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12333
* fix(funasr): select Python 3.12 tokenizers by @mudler-agent in https://github.com/mudler/LocalAI/pull/12402
### Exciting New Features 🎉
* feat: Add kimodo.cpp and 3D animation API/UI by @richiejp in https://github.com/mudler/LocalAI/pull/12095
* feat(vllm-cpp): add video_lora_dir option for runtime prompt-activated LoRA by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12119
* feat(openai): add negative_prompt field to image generation request by @lqp in https://github.com/mudler/LocalAI/pull/12031
* feat(swagger): update swagger by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12000
* feat(swagger): update swagger by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12148
* feat(kimodocpp): track usage through generic backend metadata by @richiejp in https://github.com/mudler/LocalAI/pull/12162
* feat(ui): show running models and host gauges on single-node installs by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12189
* feat(stablediffusion-ggml): Qwen-Image 2.1 support + gallery GGUF by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12190
* feat(kimodo): Add observability hooks by @richiejp in https://github.com/mudler/LocalAI/pull/12184
* feat(audio-cpp): AUDIOCPP_DEFAULT_BACKEND fallback for models without a backend option by @blackd in https://github.com/mudler/LocalAI/pull/12133
* feat(vllm-cpp): add GLiNER2.5 NER via TokenClassify by @mudler-agent in https://github.com/mudler/LocalAI/pull/12140
* feat(vllm-cpp): unify decision pipeline through Score RPC with vllm_decide ABI v29 by @mudler-agent in https://github.com/mudler/LocalAI/pull/12247
* feat(system): report per-model DRM VRAM by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12026
* sglang backend: pass through thinking_budget + require_reasoning by @pos-ei-don in https://github.com/mudler/LocalAI/pull/12193
* feat(swagger): update swagger by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12308
* feat(distributed): report worker version and show models in node inspector by @mudler-agent in https://github.com/mudler/LocalAI/pull/12328
* feat(failover): serve a model name from a chain of local and remote targets by @mudler-agent in https://github.com/mudler/LocalAI/pull/12285
* feat(parakeet-cpp): speaker diarization, sound detection and live scene events by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12335
* feat: decisions usecase for decision models, with gallery tagging by @mudler-agent in https://github.com/mudler/LocalAI/pull/12373
* feat(parakeet-cpp): name speakers from the shared voice registry by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12382
### 🧠 Models
* feat(gallery): add four Italian community Piper voices by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12121
* feat(gallery): consolidate 25 pending gallery PRs by @mudler-agent in https://github.com/mudler/LocalAI/pull/12124
* feat(gallery): galleries published as OCI artifacts by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12167
* batch(gallery): merge 14 gallery model-addition PRs by @mudler-agent in https://github.com/mudler/LocalAI/pull/12221
* feat(gallery): optionally pin the signing certificate's source repository by @mudler-agent in https://github.com/mudler/LocalAI/pull/12235
* feat(gallery): add vllm-cpp entries for laya, cua-s1-forms, and gliner2.5 by @mudler-agent in https://github.com/mudler/LocalAI/pull/12240
* feat(gallery): add nemo-speech-cpp diarization and ASR models by @mudler-agent in https://github.com/mudler/LocalAI/pull/12265
* feat(gallery): read metadata for system-path backends, enabling variant aliases by @blackd in https://github.com/mudler/LocalAI/pull/12141
* chore(gallery): add Hemmingway and remove invalid chat entry by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12278
* chore(gallery): add MiMo distill Qwen 9B variants by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12282
* feat(gallery): publish signed OCI fallbacks by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12182
* chore(gallery): add Sharp-Spark 4B variants by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12287
* chore(gallery): add Swift 1.5 GSQ-RCO variants by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12293
* chore(gallery): add ThinkingCap Qwen3.8 variants by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12295
* chore(gallery): add Agention Qwen3.8 variants by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12296
* chore(gallery): add Qwopus Flash V2 variants by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12298
* chore(gallery): add Cyber-Tiel-Coder variants by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12300
* feat(gallery): add kev-0.8b on vllm-cpp as a decisions model by @mudler-agent in https://github.com/mudler/LocalAI/pull/12391
* chore(gallery): add Cyber-Ornith 1.5 variants by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12383
### 📖 Documentation and examples
* docs: :arrow_up: update docs version mudler/LocalAI by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12111
* docs: explain mixed CPU/GPU inference by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12143
* docs(gpu): drop false auto-set claim for gfx1151 env vars by @leilei3167 in https://github.com/mudler/LocalAI/pull/12109
* docs(gallery): remove per-model documentation sections by @mudler-agent in https://github.com/mudler/LocalAI/pull/12222
* docs(proxy): clarify optional upstream API keys by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12286
* docs: replace dead chatbot-ui example link with repo root by @yzxcj797 in https://github.com/mudler/LocalAI/pull/11546
* docs: fix 11 dead links in the documentation by @pratikgx in https://github.com/mudler/LocalAI/pull/12320
* docs: fix Discord, Slack and Telegram example links by @pratikgx in https://github.com/mudler/LocalAI/pull/12392
### 👒 Dependencies
* chore: :arrow_up: Update ServeurpersoCom/omnivoice.cpp to `c2257c833333f222d64dc9d437afdcece33ceb0b` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12116
* chore: :arrow_up: Update NVIDIA/NeMo-Speech.cpp to `07003daa7eefea542076310722ccaa89709ee3c3` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12115
* chore: :arrow_up: Update ggml-org/llama.cpp to `972d2313bc0bf0a45f634f77d95c9fb03aeab12c` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12090
* chore: :arrow_up: Update PABannier/sam3.cpp to `416186c501d060df7ca02989d49b38080f5f81f3` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12091
* chore: :arrow_up: Update mudler/vllm.cpp to `e27e6d1c8f9ccd2803d37030f8a677507fe6e314` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12089
* chore: :arrow_up: Update leejet/stable-diffusion.cpp to `cc515a01f9d0e3f6b975234cc934b807f55bcd35` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12087
* chore: :arrow_up: Update ikawrakow/ik_llama.cpp to `dc31024448b8f18eac0cd5c2e200b6c7e015ef7a` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12088
* chore: :arrow_up: Update 0xShug0/audio.cpp to `f2b4937306daa25f5c78520f3c626ed31495a37a` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12086
* chore(deps): bump docs/themes/hugo-theme-relearn from `8bb66fa` to `aa16cb1` by @dependabot[bot] in https://github.com/mudler/LocalAI/pull/12096
* chore(deps): bump actions/checkout from 6 to 7 by @dependabot[bot] in https://github.com/mudler/LocalAI/pull/12108
* chore(deps): bump protobuf from 7.35.0 to 7.36.1 in /backend/python/transformers by @dependabot[bot] in https://github.com/mudler/LocalAI/pull/12098
* chore(deps): bump grpcio from 1.83.1 to 1.84.0 in /backend/python/vllm by @dependabot[bot] in https://github.com/mudler/LocalAI/pull/12099
* chore(deps): bump grpcio from 1.83.1 to 1.84.0 in /backend/python/common/template by @dependabot[bot] in https://github.com/mudler/LocalAI/pull/12101
* chore(deps): bump grpcio from 1.83.1 to 1.84.0 in /backend/python/rerankers by @dependabot[bot] in https://github.com/mudler/LocalAI/pull/12104
* chore: :arrow_up: Update ServeurpersoCom/omnivoice.cpp to `cd6922ac3cb465f1c0a22465e77db21d367204fe` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12126
* chore: :arrow_up: Update mudler/vllm.cpp to `f3cd97e379fbeca4e50415edbdd52d2517b98ef8` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12132
* chore: :arrow_up: Update ikawrakow/ik_llama.cpp to `2ae132fa601ea06818ed3584f50f7eb4f72d4967` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12131
* chore: :arrow_up: Update ggml-org/whisper.cpp to `5670d5c0bbcb148feabef84400a07cfca9aa3b30` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12130
* chore: :arrow_up: Update ggml-org/llama.cpp to `50631b3d2c569ad8e5c112090cd28570b1268ee0` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12129
* chore: :arrow_up: Update leejet/stable-diffusion.cpp to `2ea8aff7ef603977dc2ece7856bf9736dba96652` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12127
* chore: :arrow_up: Update 0xShug0/audio.cpp to `a074d6b8cdb16b89cd028876e83629a538d49b9a` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12125
* chore: :arrow_up: Update CrispStrobe/CrispASR to `647db2c7abed1fc82a69767f6e8b3993b94b8417` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12112
* chore: :arrow_up: Update CrispStrobe/CrispASR to `7bd1d6062eb3d96dfb68fb240f8736399ba490c3` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12153
* chore: :arrow_up: Update leejet/stable-diffusion.cpp to `1330cebae8f2ba99249df846cc0c9444fcbd4308` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12152
* chore: :arrow_up: Update ikawrakow/ik_llama.cpp to `401a09d2f534d2eeabb0a37919ebc5a2cbc56ac6` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12151
* chore: :arrow_up: Update mudler/vllm.cpp to `ea8c83d75f461a520e41328c44bde6c949453fa6` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12149
* chore: :arrow_up: Update 0xShug0/audio.cpp to `a7b58a6d3d6ae4143c485266b1c6c09898ad8c72` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12150
* chore: :arrow_up: Update PrismML-Eng/llama.cpp to `9a9394a895b96003ca842a6041cb28ac49a108f7` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12114
* chore: :arrow_up: Update ggml-org/llama.cpp to `e613ef2c81bae98d59850d061ac29e6e3e88cb00` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12157
* chore: :arrow_up: Update antirez/ds4 to `0aaea5a238fb41a35106a551e73c8409dfb751ac` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12168
* chore: :arrow_up: Update ikawrakow/ik_llama.cpp to `9cba2e3874df6f598fd339c4c6c7d5fc2b44645b` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12174
* chore: :arrow_up: Update CrispStrobe/CrispASR to `46612927d8ed7a98e88fb9f411768a2ccbbe1170` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12171
* chore: :arrow_up: Update ServeurpersoCom/omnivoice.cpp to `ae9dd24b6a5ff72bf50519490092ad14c64627a8` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12169
* chore: :arrow_up: Update 0xShug0/audio.cpp to `e3de8e3f3cbfac55ffa58df71426c41550a8598b` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12176
* chore: :arrow_up: Update ggml-org/llama.cpp to `ce8caa6e60a03093351d6016a818720e0d46f0fb` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12177
* chore: :arrow_up: Update 0xShug0/audio.cpp to `17cc8980e9c8f8073796aead8a91c809511cbab1` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12199
* chore: :arrow_up: Update CrispStrobe/CrispASR to `5cfdc754c04d7bb3f0ab637f0e09502eda22d1ae` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12198
* chore: :arrow_up: Update NVIDIA/NeMo-Speech.cpp to `302ebc93f096d03395e5d86c643897b8bf0fd9a8` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12196
* chore: :arrow_up: Update PrismML-Eng/llama.cpp to `01ae597e3f7d4742909e1e831abb12fe3d24b2cf` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12195
* chore: :arrow_up: Update ikawrakow/ik_llama.cpp to `baac291dc9d531927760b48451d8dfcb63b6adec` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12192
* chore: :arrow_up: Update ggml-org/llama.cpp to `58367713a6935c0810103378144008df32e3d5db` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12197
* chore: :arrow_up: Update ggml-org/whisper.cpp to `307869af285d7f6f689ba100b3515e2d1b3feb05` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12200
* chore: :arrow_up: Update ggml-org/llama.cpp to `709fe755dfa810d77e2ac386292b29648b536864` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12208
* chore: :arrow_up: Update ikawrakow/ik_llama.cpp to `c5b5773bed338c5f3b985d277764a4d780b83d42` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12210
* chore: :arrow_up: Update PrismML-Eng/llama.cpp to `bdc23b56b4458b9f1655aec5287f3ab56ee8daaa` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12207
* chore: :arrow_up: Update ggml-org/whisper.cpp to `a44e07845931421bb6f3447ce0010ed9dc76a118` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12209
* chore: :arrow_up: Update 0xShug0/audio.cpp to `1ee4ce8275997a7dcf0e2a5dc3410e509b898d6d` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12211
* chore: :arrow_up: Update leejet/stable-diffusion.cpp to `c92d73c408515c94beef32161bb5960764fde7a0` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12212
* chore: :arrow_up: Update CrispStrobe/CrispASR to `18d74132d22fa7c967181720310d4ba1df9b7bf0` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12213
* chore: :arrow_up: Update mudler/vllm.cpp to `d4738d241271b4d10134a6499f97337f20fcf8ce` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12175
* chore: :arrow_up: Update ServeurpersoCom/omnivoice.cpp to `3ac485d0688fc684f5dcf2c95283b745220f9dcc` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12191
* chore: :arrow_up: Update TheTom/llama-cpp-turboquant to `4deec5587b2963af00bdf80884f3337e02eb7d64` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12154
* chore: :arrow_up: Update ikawrakow/ik_llama.cpp to `f3d6e6e3020ddfebad60113845bf521620766da5` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12233
* chore: :arrow_up: Update CrispStrobe/CrispASR to `97a35a6e519fda1835f3c8353516384aa8710b8c` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12230
* chore: :arrow_up: Update mudler/vllm.cpp to `b24f8094cba9b4f02df71bcff8d41ddc7e88b4ef` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12229
* chore: :arrow_up: Update 0xShug0/audio.cpp to `9bdd1d908bbd128e9eb405f5a8e38d0defb84c72` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12224
* chore: :arrow_up: Update ggml-org/llama.cpp to `d2e54583c7452353eb35d40431281f6ee984332f` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12228
* chore: :arrow_up: Update ggml-org/whisper.cpp to `a664346ea5c6dddff3e61a2b7b32dd4514613f50` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12227
* chore: :arrow_up: Update PrismML-Eng/llama.cpp to `0324c66521960d67aa7da8687fb1453a79a6565c` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12226
* chore: :arrow_up: Update vllm-metal (darwin) to `v0.30.0` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12225
* chore: :arrow_up: Update vllm-project/vllm cu130 wheel to `0.30.0` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12214
* chore: :arrow_up: Update CrispStrobe/CrispASR to `acc08e3bd3e5c17a3852115f3efa0e1ab30bc47a` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12259
* chore: :arrow_up: Update NVIDIA/NeMo-Speech.cpp to `97a15afa5caa9bce5baaa86c1184103877af4101` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12257
* chore: :arrow_up: Update leejet/stable-diffusion.cpp to `b167b942f77ecb17e7f78e163a8c32ff7ac95c10` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12255
* chore: :arrow_up: Update ggml-org/whisper.cpp to `d09f61a708f3487afa956ff578e60eae5e7a233c` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12253
* chore: :arrow_up: Update ikawrakow/ik_llama.cpp to `20f7a72edd7049fe5a87eef2b5e9a50ae109ca4b` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12251
* chore: :arrow_up: Update 0xShug0/audio.cpp to `857de2366ed74bdb2c37f85259089e3a0a6b8cb0` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12248
* chore: :arrow_up: Update ServeurpersoCom/omnivoice.cpp to `8ab42195a05a9d48a3942b17568c1f3a876e133a` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12249
* chore: :arrow_up: Update PrismML-Eng/llama.cpp to `842b1880415d6f508f03b789e5ce70194def7bfd` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12250
* chore: :arrow_up: Update ggml-org/llama.cpp to `84e76d8a23162eca70490da131945ebec1f09bf4` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12258
* chore: :arrow_up: Update 0xShug0/audio.cpp to `e79205f3e0083d04e812e1a4a376f71be97e9a22` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12269
* chore: :arrow_up: Update ikawrakow/ik_llama.cpp to `1aaf7105be6e55a97fa4a9fd6f5bd362b08436dc` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12270
* chore: :arrow_up: Update PrismML-Eng/llama.cpp to `adfffbe41b2cabcd51fff326ab045662265062bb` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12271
* chore: :arrow_up: Update CrispStrobe/CrispASR to `6b78932d09765406ba0e0154d95bc6289246ceee` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12273
* chore: :arrow_up: Update leejet/stable-diffusion.cpp to `2f886889e6e8b78738d6b87f7191f6018557c551` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12274
* ci: bump Hugo from 0.146.3 to 0.166.0 by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12281
* chore: :arrow_up: Update ikawrakow/ik_llama.cpp to `cdf232cc17e410e60c1bc3b85516c4a41199b662` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12288
* chore: :arrow_up: Update CrispStrobe/CrispASR to `013ae1624dc40ecf059065d577180722439f804e` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12292
* chore: :arrow_up: Update mudler/parakeet.cpp to `2bf88954dc628b32835734e2e9159550a75a1dc6` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12291
* chore: :arrow_up: Update 0xShug0/audio.cpp to `94bd4656399180befc141b17bd6696bf84df0a9f` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12289
* chore(deps): bump LocalAGI to 8253de9 (re-dial dropped MCP sessions) by @walcz-de in https://github.com/mudler/LocalAI/pull/12299
* chore: :arrow_up: Update ggml-org/llama.cpp to `95887577ab5fead779581a7030a83c7752ff3234` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12272
* chore: :arrow_up: Update mudler/vllm.cpp to `c3bebc357385990f721af66a3a6c69328dd4fc6c` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12252
* chore: :arrow_up: Update TheTom/llama-cpp-turboquant to `a3d5603d110bda29222d2011596cdc84d7fa532d` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12232
* chore(deps): bump sentence-transformers from 5.7.0 to 6.1.0 in /backend/python/transformers by @dependabot[bot] in https://github.com/mudler/LocalAI/pull/12245
* chore(deps): update numpy requirement from >=2.5.2 to >=2.5.3 in /backend/python/transformers by @dependabot[bot] in https://github.com/mudler/LocalAI/pull/12244
* chore(deps): update transformers requirement from >=5.15.1 to >=5.17.0 in /backend/python/transformers by @dependabot[bot] in https://github.com/mudler/LocalAI/pull/12105
* chore(deps): bump grpcio from 1.83.0 to 1.84.0 in /backend/python/transformers by @dependabot[bot] in https://github.com/mudler/LocalAI/pull/12102
* chore(deps): bump grpcio from 1.83.1 to 1.84.0 in /backend/python/coqui by @dependabot[bot] in https://github.com/mudler/LocalAI/pull/12106
* chore(deps): bump LocalAGI to 7e0947d (no-RAG-DB crash fix, tool filters, per-collection models) by @mudler-agent in https://github.com/mudler/LocalAI/pull/12302
* chore: :arrow_up: Update ikawrakow/ik_llama.cpp to `ed27bf7ed25e637692e89cd341d802522a2cee8a` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12313
* chore: :arrow_up: Update CrispStrobe/CrispASR to `ec98831d0776ec8a16ccaf93955693eb7ecfbec3` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12314
* chore: :arrow_up: Update 0xShug0/audio.cpp to `77491a33c589c53ff18add050095cf35647c8213` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12315
* chore: :arrow_up: Update ServeurpersoCom/omnivoice.cpp to `ead199a2bc4c53a57cac90095ae049a111d9e98d` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12316
* chore: :arrow_up: Update leejet/stable-diffusion.cpp to `3f8527a46c54ecf4cb4ed6003da8e8982283c73c` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12317
* chore: :arrow_up: Update ggml-org/llama.cpp to `4da6337767f973e2b4d0797e5b323d77d8565e4a` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12318
* chore: :arrow_up: Update 0xShug0/audio.cpp to `f825d1d1b92af309585aeb656b2a59c44fc603eb` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12343
* chore: :arrow_up: Update ikawrakow/ik_llama.cpp to `d741de5074cd424dd3ba7cfc4d9b7649f1eb0463` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12351
* chore: :arrow_up: Update ServeurpersoCom/omnivoice.cpp to `53e6c2066150802ad3cd4b655b31c696e78e0019` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12350
* chore: :arrow_up: Update ggml-org/whisper.cpp to `6e4ab854f67f743900934a703d5603419384c961` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12349
* chore: :arrow_up: Update CrispStrobe/CrispASR to `2cd383a926e3c37334e75eb5d8b8a85bd82ed22d` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12348
* chore: :arrow_up: Update localai-org/ced.cpp to `b10237678d1c3b30c77d19f2e63f6c198c7f8d09` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12346
* chore: :arrow_up: Update NVIDIA/NeMo-Speech.cpp to `0f706e43cf1fbc031bad1423e05460d3acaeaa1c` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12361
* chore(deps): bump nib to v0.12.1 by @mudler-agent in https://github.com/mudler/LocalAI/pull/12372
* chore: :arrow_up: Update localai-org/ced.cpp to `61dec2ab0106f2047ee40062a7075dbf08c523d0` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12365
* chore: :arrow_up: Update ikawrakow/ik_llama.cpp to `0821d62a8b356bd1db3c6765551a30bfcc44a6de` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12364
* chore: :arrow_up: Update CrispStrobe/CrispASR to `be202c472503a5c7f1d3e568c420865cad02f1c3` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12363
* chore: :arrow_up: Update 0xShug0/audio.cpp to `ed96b7307c8daba2ebcf7912af928825f6b14cb9` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12362
* chore: :arrow_up: Update mudler/parakeet.cpp to `623a968bccbd2214588df398fcce687cd4218dea` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12347
* chore: :arrow_up: Update TheTom/llama-cpp-turboquant to `bcb85fc3ae85efa0f5f392c6c880dfc524923860` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12344
* chore(vllm-cpp): bump to 967883486 (ABI v30), add hf_overrides and Tev1 entries, fix vllm-cpp gallery installs by @mudler-agent in https://github.com/mudler/LocalAI/pull/12379
* chore: :arrow_up: Update 0xShug0/audio.cpp to `9a02e61326aaaf9d462b584ca5e0daba22c0abfc` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12389
* chore: :arrow_up: Update NVIDIA/NeMo-Speech.cpp to `4c101bc7113f49101a3e11d2c994c519f41939f6` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12388
* chore: :arrow_up: Update mudler/parakeet.cpp to `8c8cec0c4564610a0a4b30a8a6f2ead15d1a76fb` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12387
* chore: :arrow_up: Update CrispStrobe/CrispASR to `ba8c1ea667f30b1c0e32ef8574cee68d9f30bcf3` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12386
* chore: :arrow_up: Update localai-org/voice-detect.cpp to `b74a896f47c6d04fcca0a962ff317528fd0b0019` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12384
* chore: :arrow_up: Update ikawrakow/ik_llama.cpp to `32cddbfcefed93896a39c64e7c38c119de8682e6` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12385
* chore: :arrow_up: Update ggml-org/llama.cpp to `a4d880fd5c7f88713ded6db9f0111893bd78afa6` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12345
### Other Changes
* chore(model-gallery): :arrow_up: update checksum by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12015
* chore(model gallery): :robot: add 1 new models via gallery agent by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12080
* chore(model-gallery): :arrow_up: update checksum by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12128
* chore(model-gallery): :arrow_up: update checksum by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12156
* chore(model-gallery): :arrow_up: update checksum by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12172
* fix(ci): sign backends in the format we verify by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12166
* chore(model-gallery): :arrow_up: update checksum by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12194
* chore(model-gallery): :arrow_up: update checksum by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12215
* chore(model-gallery): :arrow_up: update checksum by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12231
* chore(model-gallery): :arrow_up: update checksum by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12256
* chore(model-gallery): :arrow_up: update checksum by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12275
* chore(model-gallery): :arrow_up: update checksum by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12290
* fix(ci): use Go 1.27 for Darwin backends by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12284
* fix(ci): retain backend digests for release retries by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12160
* chore(website): refresh the counters by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12039
* chore(model gallery): :robot: add 1 new models via gallery agent by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12223
* chore(model-gallery): propose variant groupings for review by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12180
* chore(model gallery): :robot: add 1 new models via gallery agent by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12237
* chore(model-gallery): :arrow_up: update checksum by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12310
* chore(website): refresh the counters by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12329
* chore(model-gallery): :arrow_up: update checksum by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12342
* chore(model-gallery): :arrow_up: update checksum by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12366
* test(agentpool): pin the standalone agent contract by @mudler-agent in https://github.com/mudler/LocalAI/pull/12378
* chore(model-gallery): :arrow_up: update checksum by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12390
* feat(audio): remember speakers from diarization by @mudler-agent in https://github.com/mudler/LocalAI/pull/12414
* chore: :arrow_up: Update CrispStrobe/CrispASR to `fdc3a0007d68f8d3905f20e9cd4d18b5f193e096` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12413
* fix(llama-cpp): let parallel:1 in the model options win over LLAMACPP_PARALLEL by @walcz-de in https://github.com/mudler/LocalAI/pull/12426
* fix(whisperx): keep the transcript when diarization fails, report real errors by @walcz-de in https://github.com/mudler/LocalAI/pull/12427
* fix(cloud-proxy): surface Anthropic refusals instead of empty replies by @walcz-de in https://github.com/mudler/LocalAI/pull/12424
* fix(llama-cpp): do not stream the error text as content on pre-stream failures by @walcz-de in https://github.com/mudler/LocalAI/pull/12425
* chore: :arrow_up: Update mudler/parakeet.cpp to `bee7c14dfcc23613df58176c59a40459e7b47095` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12420
* chore: :arrow_up: Update ikawrakow/ik_llama.cpp to `d9e286846d6f8232db48ec5c111a4ea3aea675ef` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12418
* chore: :arrow_up: Update ggml-org/llama.cpp to `a868c3e3c56657f7e8a6231190dbbe90e7dd86c0` by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12419
* chore(model-gallery): :arrow_up: update checksum by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12415
* feat(gallery): add Nimble 9B and CLM decision models, bump vllm.cpp to a19294a9 by @mudler-agent in https://github.com/mudler/LocalAI/pull/12397
* docs: introduce decision models on the blog by @localai-org-maint-bot in https://github.com/mudler/LocalAI/pull/12428

## New Contributors
* @mudler-agent made their first contribution in https://github.com/mudler/LocalAI/pull/12124
* @lqp made their first contribution in https://github.com/mudler/LocalAI/pull/12031
* @H-XX-D made their first contribution in https://github.com/mudler/LocalAI/pull/12135
* @yzxcj797 made their first contribution in https://github.com/mudler/LocalAI/pull/11546
* @PINYOPATTANAWASANPORN made their first contribution in https://github.com/mudler/LocalAI/pull/12216
* @pratikgx made their first contribution in https://github.com/mudler/LocalAI/pull/12320

**Full Changelog**: https://github.com/mudler/LocalAI/compare/v4.10.0...v4.11.0
