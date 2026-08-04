---
title: "What landed in LocalAI 4.8"
date: 2026-08-04
author: "Ettore Di Giacinto"
category: "Release"
tags: ["release", "vllm.cpp", "audio.cpp", "3d", "gallery", "distributed", "performance"]
summary: "A new inference engine, 3D generation, one backend that serves six audio endpoints, and a web interface 3.48x lighter. 374 pull requests in twenty-one days."
extracss: ["blog.css"]
---

LocalAI 4.8.0 is out, after twenty-one days and 374 merged pull requests. There are three new things LocalAI can do, and a lot of repair work on things it already did.

The full notes list everything. This post covers the parts that change what you do day to day, with the pull request numbers so you can read the diffs.

## The web interface got 3.48x lighter

Open the UI over a slow link and you now wait about a third as long. Three separate HTTP problems were fixed together in [#11056](https://github.com/mudler/LocalAI/pull/11056), all measured on a live deployment.

The server was sending no `Content-Encoding` at all, whatever the client asked for. There is now gzip middleware, on by default, with `--disable-http-compression` and `--http-compression-min-length` (default 1024) if you want to change it. Streaming responses are skipped explicitly, because buffering an SSE stream behind a gzip writer defeats incremental flushing and looks to the client like a hung request. Completion, realtime, speech, transcription, agent-job and log-tail paths are all on that skip list, as are already-compressed formats, which gzip made marginally larger.

Vite content-hashes the bundle filenames, so an `/assets/` URL can never change content, yet the assets shipped with no `Cache-Control`, `ETag` or `Last-Modified`. They now carry `public, max-age=31536000, immutable`, and `index.html` is explicitly `no-cache` so a deploy is still picked up.

The third one was `/api/traces` returning a 21 MB unpaginated blob that the UI polled every five seconds. Both trace endpoints now take `limit` (default 50, max 1000, `0` for all), `offset` and `full`, and summarize by default, dropping bodies and headers but keeping the byte counters so the UI can still say what it dropped. Every trace carries a process-lifetime `id`, and `GET /api/traces/{id}` serves the full record when you expand a row.

<div class="tw">
<table>
<thead><tr><th>Measurement</th><th>Before</th><th>After</th><th>Change</th></tr></thead>
<tbody>
<tr><td>React JS + CSS over the wire</td><td>2,815,513 B</td><td>807,918 B</td><td><b>3.48x smaller</b></td></tr>
<tr><td>All embedded assets, fonts included</td><td>3,953,917 B</td><td>1,559,787 B</td><td>2.53x smaller</td></tr>
<tr><td>Repeat navigation asset transfer</td><td>full re-download</td><td>0 bytes</td><td>eliminated</td></tr>
<tr><td><code>/api/backend-traces</code> poll payload</td><td>21,131,097 B</td><td>7,201 B</td><td><b>~2900x smaller</b></td></tr>
</tbody>
</table>
</div>

## One gallery entry, several builds

Installing a model no longer means reading a list of quantizations and guessing which one your card will hold. A gallery entry can now declare `variants:`, a list of references to other entries that are alternative builds of the same weights:

```yaml
- name: nanbeige4.1-3b-q4
  url: github:mudler/LocalAI/gallery/nanbeige4.1.yaml@master
  overrides: {parameters: {model: nanbeige4.1-3b-q4_k_m.gguf}}
  files: [...]
  variants:
    - model: nanbeige4.1-3b-q8
```

At install time LocalAI drops the variants this host cannot run, which it derives from the backend name rather than from hardware conditions an author would have to write by hand, so MLX disappears on Linux and CUDA disappears on a Mac. It then drops the ones that will not fit, using VRAM on GPU hosts and cgroup-aware system RAM on CPU hosts so a container sees its own limit rather than the machine's. Of what is left it picks the largest, on the assumption that a bigger footprint is a better build of the same weights. The entry's own build always competes and is never filtered out, so selection ends with something installable.

Sizes come from the existing `pkg/vram` estimator, which reads the remote GGUF header, falls back to an HTTP `HEAD`, then the declared `size:`, then the Hugging Face repo listing. Nothing is downloaded to make the decision, and a failed probe never fails an install.

Every surface can override the choice: `variant` on `POST /models/apply`, `local-ai models install --variant`, the `install_model` MCP tool, and a split-button in the models table. An explicit choice is honored even when it does not fit, with a warning, because that is a deliberate operator decision. Older clients read the same live `gallery/index.yaml`, ignore the key they do not understand, and install exactly as before.

One gap worth knowing about: in distributed mode `InstallModel` resolves against the frontend rather than the worker that will serve the model, so a cluster with a small frontend and large workers selects conservatively. PRs [#10943](https://github.com/mudler/LocalAI/pull/10943), [#10983](https://github.com/mudler/LocalAI/pull/10983), [#10992](https://github.com/mudler/LocalAI/pull/10992), [#11027](https://github.com/mudler/LocalAI/pull/11027) and [#11139](https://github.com/mudler/LocalAI/pull/11139).

## A new engine: vllm.cpp (alpha)

[vllm.cpp](https://github.com/mudler/vllm.cpp) is Apache-2.0 and maintained by the LocalAI team. We want it community-first rather than a LocalAI-only engine, so it lives in its own repository with its own docs, benchmark record and issue tracker, and it runs without LocalAI anywhere in the picture. It began as a C++20 port of vLLM. It ships here as the `vllm-cpp` backend ([#11100](https://github.com/mudler/LocalAI/pull/11100)). It implements vLLM's V1 architecture, so paged KV cache, continuous batching, prefix caching, scheduler and sampler, on a portable tensor runtime with no Python, no PyTorch and no ggml at inference. vLLM stays its reference implementation: correctness is checked by comparing output against it, and the benchmark scoreboard is kept against it.

It has grown features vLLM does not have, which is most of the reason the port exists. It loads GGUF as well as safetensors, runs on CPU, Apple Metal and Vulkan alongside CUDA 12 and 13 and L4T for GB10, and ships speculative decoding and KV offload. Its benchmark page now measures against llama.cpp, MLX-LM and DwarfStar as well as vLLM, because on that hardware those are the engines it competes with. The project is expected to be renamed, with the new name still to be decided; it is drifting far enough that vllm.cpp will eventually mislead.

Tool calling is at llama.cpp parity by construction, because chat deliberately reuses the same autoparser path: full minja chat templates, `tool_choice: auto` lowered to a lazy structural-tag decode constraint, 30 tool dialects, 7 reasoning parsers, and streamed `ChatDelta` and `ToolCallDelta`.

Numbers from the project's own [scoreboard](https://github.com/mudler/vllm.cpp/blob/master/docs/BENCHMARKS.md), which calls ties ties and losses losses. Above 1.0 means vllm.cpp is ahead:

<div class="tw">
<table>
<thead><tr><th>Reference</th><th>Workload</th><th>Result</th></tr></thead>
<tbody>
<tr><td>vLLM</td><td>Qwen3.6-27B NVFP4, GB10</td><td>1.045x at concurrency 1, 1.007x to 1.017x from c2 to c32, output token-for-token identical</td></tr>
<tr><td>vLLM</td><td>Qwen3.6-35B-A3B NVFP4, GB10</td><td>1.010x at c16 and 1.013x at c32, behind from c1 to c8 (0.817x at c1)</td></tr>
<tr><td>llama.cpp</td><td>Qwen3.5-2B GGUF, CPU aarch64</td><td>prefill 1.18x, decode a tie, memory parity</td></tr>
<tr><td>MLX-LM</td><td>Qwen3-0.6B, Apple M4</td><td>97.6% of warm total, prefill ahead</td></tr>
<tr><td>DwarfStar (ds4)</td><td>DeepSeek-V4-Flash IQ2_XXS, one DGX Spark</td><td>16.28 vs 16.33 tok/s decode, 0.997x, a parity result</td></tr>
</tbody>
</table>
</div>

The upstream page is careful about its own noise: on the 27B grid the run-to-run spread is 0.5% and c2 through c32 land between 0.7% and 1.7%, so it calls those five ties rather than wins. The concurrency-1 result is the one it stands behind.

The DeepSeek-V4-Flash row is the one that shows how far this has moved from being a vLLM port. It runs DeepSeek-V4-Flash at roughly 2-bit (IQ2_XXS mixed, about 80 GB) on a single DGX Spark, decoding at 16.28 tok/s against DwarfStar's 16.33. At 300B+ total parameters even a 4-bit checkpoint is 156 GB or more, so a 2-bit GGUF is what fits inside the Spark's 119 GiB unified pool, and reading GGUF is what makes that possible.

Speculative decoding is in similar shape: MTP on Qwen3.6-27B NVFP4 is token-identical to vLLM's MTP and about 4% faster at concurrency 1.

Configuration is a normal backend install:

```yaml
name: qwen3-vllm
backend: vllm-cpp
context_size: 8192
parameters:
  model: Qwen3-4B      # a safetensors directory or a .gguf file
options:
- max_num_seqs:16      # also: block_size:<n>, num_blocks:<n>
```

**Treat these as alpha development builds, not a released backend.** vllm.cpp is early, and shipping it in 4.8 is about getting it in front of people who want to try it, not about recommending it for anything you care about. `llama-cpp` stays the default for real use.

The CPU path is verified end to end against `Qwen3.5-2B-UD-Q8_K_XL.gguf` with the full Ginkgo suite, covering blocking and streaming byte-parity, greedy determinism, stop words, GBNF-constrained generation, concurrent streams, reasoning split and both `required` and `auto` tool calls. The GPU images build and ship, but their runtime behavior has not been through that gate. No throughput comparison against upstream vLLM is claimed. Expect rough edges, and please report what breaks.

On Apple Silicon the image now ships vllm.cpp's MLX GEMM provider ([#11137](https://github.com/mudler/LocalAI/pull/11137)). Upstream keeps it off by default because it adds about 124 MB, so we measured before turning it on. Qwen3-1.7B-bf16 on an M4, p=512 g=128, both arms toggled on one binary so a build difference cannot explain the gap:

<div class="tw">
<table>
<thead><tr><th>Batch</th><th>MLX tok/s</th><th>native tok/s</th><th>speedup</th><th>MLX TTFT</th><th>native TTFT</th></tr></thead>
<tbody>
<tr><td>1</td><td>5.79</td><td>3.08</td><td><b>1.88x</b></td><td>3.32 s</td><td>7.68 s</td></tr>
<tr><td>4</td><td>15.75</td><td>10.24</td><td><b>1.54x</b></td><td>9.63 s</td><td>18.77 s</td></tr>
<tr><td>16</td><td>38.65</td><td>17.69</td><td><b>2.19x</b></td><td>18.33 s</td><td>54.48 s</td></tr>
</tbody>
</table>
</div>

Two reps, with rep spread reaching 9.4%, so treat the multipliers as +/-10%. Time to first token roughly halves across the range.

<figure>
<video src="/media/vllm-race.mp4" muted loop playsinline preload="none" data-lazy aria-label="vllm.cpp generating tokens"></video>
<figcaption>vllm.cpp serving a GGUF checkpoint with no Python in the process.</figcaption>
</figure>

## LocalAI generates 3D models now

3D generation is a new modality, so it had to be wired through the whole stack: a `Generate3D` RPC in `backend.proto`, a `FLAG_3D` capability so the loader knows which backends can serve it, and `POST /v1/3d/generations`.

The first engine behind it is `trellis2cpp`, an image-to-3D backend over TRELLIS.2. You give it an image, you get a GLB back. The web UI has a page for it with a native GLB viewer, so you can turn the result around in the browser instead of downloading it to find out whether it worked, history kept in IndexedDB so a reload does not lose your generations, and previewable print remeshing for output you actually intend to send to a printer ([#10979](https://github.com/mudler/LocalAI/pull/10979)).

<figure>
<video src="/media/3d-generation.mp4" muted loop playsinline preload="none" data-lazy aria-label="A generated 3D llama rotating in the LocalAI GLB viewer"></video>
<figcaption>trellis2-4b, 2,502,928 vertices and 5,012,118 triangles, turning in the browser. The remesh slider below it is the print path.</figcaption>
</figure>

## One backend, six audio endpoints

The usual shape for audio is one backend per model family, which means a process per capability and a config file for each. `audio-cpp` wraps [audio.cpp](https://github.com/0xShug0/audio.cpp), a multi-family ggml audio engine. One backend process serves several unrelated families through a single runtime vocabulary, and works out which family a checkpoint belongs to from the GGUF's own `audiocpp.model_spec.family` metadata key. There is nothing backend-specific to write in the model config.

<div class="tw">
<table>
<thead><tr><th>Endpoint</th><th>Families</th></tr></thead>
<tbody>
<tr><td><code>/v1/audio/speech</code></td><td>supertonic, chatterbox (voice cloning), irodori-voicedesign</td></tr>
<tr><td><code>/v1/audio/transcriptions</code></td><td>citrinet, nemotron, forced-aligner</td></tr>
<tr><td><code>/v1/audio/vad</code></td><td>silero-vad, marblenet-vad</td></tr>
<tr><td><code>/v1/audio/diarize</code></td><td>sortformer</td></tr>
<tr><td><code>/audio/transform</code></td><td>htdemucs (4-stem separation), voice conversion, speech to speech</td></tr>
<tr><td><code>/v1/sound-generation</code></td><td>stable-audio-sfx</td></tr>
</tbody>
</table>
</div>

Thirteen gallery entries ship with it, one per task kind the engine can actually serve. Where it cannot honestly back an RPC it returns `UNIMPLEMENTED` with a reason rather than an empty success, and a failed load is a gRPC error rather than `success: false`, which matters because the loader's greedy backend probe would otherwise pick it for a model it cannot serve.

Two changes reach past the backend itself. `AudioTransformResult` gained a `stems` field, so source separation can return the whole stem set instead of one mixdown. And `/audio/transform` no longer folds every upload to 16 kHz mono: that fold made 4-stem separation unreachable by construction, so it became a per-backend capability, with the existing backends keeping it explicitly and the default for an unregistered backend being to leave the upload alone ([#11141](https://github.com/mudler/LocalAI/pull/11141)).

## Three new backends for speech and small quants

`magpie-tts-cpp` wraps [magpie-tts.cpp](https://github.com/mudler/magpie-tts.cpp), a C++17 and ggml port of NVIDIA Magpie TTS Multilingual 357M with the NanoCodec vocoder embedded. Five voices, nine or more languages, 22.05 kHz mono, out of one self-contained GGUF. The upstream engine is parity-gated against NeMo per component, with a teacher-forced replay maximum absolute difference of 3.6e-5.

`moss-tts-cpp` wraps [moss-tts.cpp](https://github.com/mudler/moss-tts.cpp) and serves MOSS-TTS-Local v1.5 at 48 kHz stereo, with optional reference-audio voice cloning. Images cover CPU, CUDA 12 and 13, Intel SYCL, Vulkan, ROCm, L4T and Darwin Metal. Both landed in [#11115](https://github.com/mudler/LocalAI/pull/11115), [#10860](https://github.com/mudler/LocalAI/pull/10860) and [#10877](https://github.com/mudler/LocalAI/pull/10877).

<figure>
<video src="/media/magpie.mp4" muted loop playsinline preload="none" data-lazy aria-label="magpie-tts-cpp synthesising multilingual speech"></video>
<figcaption>magpie-tts-cpp, five voices and nine languages out of a single GGUF.</figcaption>
</figure>

The `bonsai` backend serves the 1-bit (Q1_0) and ternary (Q2_0) Bonsai quantizations of Qwen3 8B and Qwen3.6-27B, from about 1.15 GB. Stock llama.cpp has no kernels for those formats, so the backend builds against the PrismML fork through a wrapper Makefile that swaps only `LLAMA_REPO` and `LLAMA_VERSION`, reusing the same `grpc-server.cpp` with zero skew patches. Eight gallery entries ship with it. If Q1_0 and Q2_0 reach mainline llama.cpp, this backend retires into a routine version bump ([#10834](https://github.com/mudler/LocalAI/pull/10834), [#10866](https://github.com/mudler/LocalAI/pull/10866)).

## The operations bar became a page

The old operations bar rendered one row per in-flight operation above every page. Queue four model installs and a backend and it took most of the viewport, on every route, until the last one finished. It was doing two jobs at once. A global "something is happening" signal only needs one line, and the detail of what is happening needs a page of its own.

The strip is now one line, permanently, showing a failure first and otherwise the least-advanced running operation, with a `+N more` pill. Its `✕` hides the strip and no longer cancels anything. That is a deliberate behavior change worth knowing about before you click it out of habit: the same glyph used to cancel a 17 GB download in one row and dismiss a message in the next. Cancelling moved to the new page, behind a button that says so.

The Activity page at `/app/activity` is admin only and carries what the strip has to drop: phase, bytes, a derived time remaining, and a per-node breakdown for cluster installs. It also keeps a record. `/api/operations` dropped an operation the moment it succeeded, so if you started a large install and walked away there was no way to find out afterwards whether it finished, failed, or never started. That record is a bounded 50-entry ring, and dismissing a failure moves it into the record instead of deleting it.

Several long-standing UI bugs fell out of the rewrite: retrying a failed removal re-downloaded the model, queued operations rendered as "Installing" with a spinner, a long error message pushed every page about 270px past the viewport and gave the whole app a horizontal scrollbar, and the ETA blanked for every operation whenever any one of them was verifying ([#11163](https://github.com/mudler/LocalAI/pull/11163)).

## VRAM budgets, per node

You can now cap how much of a card LocalAI is allowed to use, as a percentage or an absolute amount ([#10833](https://github.com/mudler/LocalAI/pull/10833)):

```
LOCALAI_VRAM_BUDGET=80%
LOCALAI_VRAM_BUDGET=12GB
```

Everywhere LocalAI reads VRAM to make an allocation decision it now uses `min(detected, budget)`. Percentages above 100 are rejected and absolute values above physical are clamped, so the ceiling can only ever lower usable VRAM. Standalone it is a hard per-process cap that hardware defaults, context auto-fit, GGUF warnings and the watchdog all inherit. Distributed it is a placement ceiling: the worker reports raw VRAM plus its budget string, the node registry resolves it to a byte ceiling on registration and heartbeat, and the SQL scheduler needed no query change. Admin overrides through `PUT` and `DELETE /api/nodes/:id/vram-budget` survive worker restarts, and the same thing is available as the `set_node_vram_budget` MCP tool. Unset means all detected VRAM, so existing deployments do not change.

## Distributed mode stops reaping live backends

A model that showed as loaded on the home page but appeared on no node in the cluster turned out to be four separate bugs, all fixed in this cycle.

The reaper was deleting `node_models` rows for backends that were alive and working. `probeLoadedModels` reaped a row after a single failed one second gRPC health check, and a backend that is merely busy cannot answer one, because a single-threaded Python backend blocks for minutes inside a request. The worker spawned the backend and holds the process handle, so its answer is not blocked by whatever the backend is doing. A new `models.running` request-reply subject asks the worker directly, and the reconciler diffs the worker's process keys against the registry rows before any port probe runs. A worker that does not answer is skipped rather than assumed empty, so a NATS blip cannot delete a node's rows. Where the port probe still runs, it no longer conflates failure modes: `DeadlineExceeded` means busy, `Unavailable` means gone, and only the second counts toward a threshold that now needs three consecutive misses.

Every routed model also left an in-process stub in the frontend's `ModelLoader`, and removal paths deleted only the database row, so the stub outlived the replica and the model was reported as loaded forever. The replica-removed hook became a list, and a new invalidator drops the local stub once no healthy replica remains anywhere in the cluster.

Alongside those, `in_flight` counters no longer leak high and pin a replica's VRAM against eviction, model-load deadlines scale with checkpoint size and with progress rather than wall-clock, staging verification counts as progress rather than as a stall, backend discovery stopped hiding worker-installed and GPU-only backends behind the controller's own filesystem, and the scheduler will not place a model on a node that cannot store it. The full list is in [#11142](https://github.com/mudler/LocalAI/pull/11142) and the eighteen PRs around it.

## A security fix you should read

`POST /api/fine-tuning/jobs` accepted `reward_functions[].code`, an inline Python body that ran against a hand-rolled builtin allowlist. That allowlist was not a security boundary. Standard CPython introspection reaches the real `os` module from inside it, which is arbitrary code execution on the host, and the execution happened during a smoke test at job start on an endpoint that is unauthenticated by default.

Inline reward code is now refused unless the operator sets `LOCALAI_TRL_ALLOW_INLINE_REWARD=true` on the backend. Builtin reward functions are unaffected and need no configuration. The documentation no longer describes the allowlist as a sandbox ([#11068](https://github.com/mudler/LocalAI/pull/11068)).

Two more hardening fixes landed in the same cycle. Tar hardlinks that escape the extraction root are now rejected: the archive extractor pre-scanned members and rejected symlinks, but a tar hardlink entry carries a regular file mode and passed that check, and `Header.Linkname` was never validated, so an archive could link to a path outside the destination ([#11266](https://github.com/mudler/LocalAI/pull/11266)). And a cyclic `$ref` in a JSON-schema grammar is now rejected rather than recursing into a stack overflow ([#11041](https://github.com/mudler/LocalAI/pull/11041)). This release also picks up hono 4.12.25 for CVE-2026-54290 ([#11023](https://github.com/mudler/LocalAI/pull/11023)).

## The rest, briefly

Hugging Face model artifacts are now a managed snapshot flow: immutable snapshot resolution, authenticated downloads with real progress, materialization on gallery install and preload, runtime binding to staged artifacts, and per-file resume of an interrupted download rather than starting over. Python backends reuse the Go download path instead of fetching on their own.

The traces panel gained a sortable User column, plus client IP and user agent in the expanded row, taken from echo's `RealIP()` so a trusted proxy is honored.

Documentation got an onboarding overhaul driven by a per-page audit: one model, `qwen3-4b`, now carries through install, the web UI and a curl call; there is a new walkthrough for building your first agent; and a new runtime-errors reference is keyed on the literal error strings users actually see.

Valkey Search joins the vector store options as the `valkey-store` backend ([#11196](https://github.com/mudler/LocalAI/pull/11196)). `local-ai` can be started on demand through systemd socket activation, taking a listener it inherits rather than binding one, with ordinary `--address` behavior unchanged when no activation listener is present ([#11169](https://github.com/mudler/LocalAI/pull/11169)). Trace history now survives a restart, stored as bounded per-record JSON under the data path with no database dependency ([#11203](https://github.com/mudler/LocalAI/pull/11203)). You can edit saved chat messages in place without triggering a new inference request ([#11189](https://github.com/mudler/LocalAI/pull/11189)). And the Intel SYCL llama.cpp backend is now self-contained, so it runs without a matching oneAPI runtime on the host ([#10991](https://github.com/mudler/LocalAI/pull/10991)).

This is also the release where localai.io split in two: the project site at the root, and the documentation under `/docs/`. Every URL that was published before still resolves, through 214 generated redirect stubs, because GitHub Pages has no server-side rewrites to do it properly ([#11243](https://github.com/mudler/LocalAI/pull/11243)).

Twenty-five people contributed to this release, eleven of them for the first time. The gallery went from 1,221 entries to 1,515.

To upgrade, pull `localai/localai:latest` or re-run the install script. The [full changelog](https://github.com/mudler/LocalAI/compare/v4.7.1...v4.8.0) has everything this post left out.
