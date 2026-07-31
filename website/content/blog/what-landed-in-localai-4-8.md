---
title: "What landed in LocalAI 4.8"
date: 2026-07-28
author: "Ettore Di Giacinto"
category: "Release"
tags: ["release", "vllm.cpp", "gallery", "distributed", "performance"]
summary: "The web interface got 3.48x lighter, gallery entries now install the build your hardware can actually run, and there is a new inference engine in the box. 214 pull requests in thirteen days."
extracss: ["blog.css"]
---

LocalAI 4.8.0 is out. It took thirteen days and 214 merged pull requests, and the changes you will notice first are the boring ones: the web interface loads faster, model installs stop asking you to pick a quantization, and a cluster no longer reports models as loaded when they are gone.

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

## A new engine: vllm.cpp

[vllm.cpp](https://github.com/mudler/vllm.cpp) is a from-scratch C++20 port of vLLM, written and maintained by the LocalAI team under Apache-2.0, and it ships here as the `vllm-cpp` backend ([#11100](https://github.com/mudler/LocalAI/pull/11100)). It mirrors vLLM's V1 architecture, so paged KV cache, continuous batching, prefix caching, scheduler and sampler, on a portable tensor runtime with no Python, no PyTorch and no ggml at inference. It loads Hugging Face safetensors and GGUF, enforces structured output inside the engine (JSON schema, regex, choice, GBNF), and builds for CPU amd64 and arm64, CUDA 12 and 13 including Blackwell, L4T for GB10, Vulkan and Darwin Metal.

Tool calling is at llama.cpp parity by construction, because chat deliberately reuses the same autoparser path: full minja chat templates, `tool_choice: auto` lowered to a lazy structural-tag decode constraint, 30 tool dialects, 7 reasoning parsers, and streamed `ChatDelta` and `ToolCallDelta`.

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

The CPU path is verified end to end against `Qwen3.5-2B-UD-Q8_K_XL.gguf` with the full Ginkgo suite, covering blocking and streaming byte-parity, greedy determinism, stop words, GBNF-constrained generation, concurrent streams, reasoning split and both `required` and `auto` tool calls. The maturity statement from the release notes is worth repeating in full:

> The GPU images build and ship, but their runtime behavior has not been through the same e2e gate yet. This is a first release of a young engine: no throughput comparison against upstream vLLM is claimed here, and `llama-cpp` remains the default recommendation for general use. Try it, and please report what breaks.

## VRAM budgets, per node

You can now cap how much of a card LocalAI is allowed to use, as a percentage or an absolute amount ([#10833](https://github.com/mudler/LocalAI/pull/10833)):

```
LOCALAI_VRAM_BUDGET=80%
LOCALAI_VRAM_BUDGET=12GB
```

Everywhere LocalAI reads VRAM to make an allocation decision it now uses `min(detected, budget)`. Percentages above 100 are rejected and absolute values above physical are clamped, so the ceiling can only ever lower usable VRAM. Standalone it is a hard per-process cap that hardware defaults, context auto-fit, GGUF warnings and the watchdog all inherit. Distributed it is a placement ceiling: the worker reports raw VRAM plus its budget string, the node registry resolves it to a byte ceiling on registration and heartbeat, and the SQL scheduler needed no query change. Admin overrides through `PUT` and `DELETE /api/nodes/:id/vram-budget` survive worker restarts, and the same thing is available as the `set_node_vram_budget` MCP tool. Unset means all detected VRAM, so existing deployments do not change.

## Three new backends for speech and small quants

`magpie-tts-cpp` wraps [magpie-tts.cpp](https://github.com/mudler/magpie-tts.cpp), a C++17 and ggml port of NVIDIA Magpie TTS Multilingual 357M with the NanoCodec vocoder embedded. Five voices, nine or more languages, 22.05 kHz mono, out of one self-contained GGUF. The upstream engine is parity-gated against NeMo per component, with a teacher-forced replay maximum absolute difference of 3.6e-5.

`moss-tts-cpp` wraps [moss-tts.cpp](https://github.com/mudler/moss-tts.cpp) and serves MOSS-TTS-Local v1.5 at 48 kHz stereo, with optional reference-audio voice cloning. Images cover CPU, CUDA 12 and 13, Intel SYCL, Vulkan, ROCm, L4T and Darwin Metal. Both landed in [#11115](https://github.com/mudler/LocalAI/pull/11115), [#10860](https://github.com/mudler/LocalAI/pull/10860) and [#10877](https://github.com/mudler/LocalAI/pull/10877).

The `bonsai` backend serves the 1-bit (Q1_0) and ternary (Q2_0) Bonsai quantizations of Qwen3 8B and Qwen3.6-27B, from about 1.15 GB. Stock llama.cpp has no kernels for those formats, so the backend builds against the PrismML fork through a wrapper Makefile that swaps only `LLAMA_REPO` and `LLAMA_VERSION`, reusing the same `grpc-server.cpp` with zero skew patches. Eight gallery entries ship with it. If Q1_0 and Q2_0 reach mainline llama.cpp, this backend retires into a routine version bump ([#10834](https://github.com/mudler/LocalAI/pull/10834), [#10866](https://github.com/mudler/LocalAI/pull/10866)).

## Distributed mode stops reaping live backends

A model that showed as loaded on the home page but appeared on no node in the cluster turned out to be four separate bugs, all fixed in this cycle.

The reaper was deleting `node_models` rows for backends that were alive and working. `probeLoadedModels` reaped a row after a single failed one second gRPC health check, and a backend that is merely busy cannot answer one, because a single-threaded Python backend blocks for minutes inside a request. The worker spawned the backend and holds the process handle, so its answer is not blocked by whatever the backend is doing. A new `models.running` request-reply subject asks the worker directly, and the reconciler diffs the worker's process keys against the registry rows before any port probe runs. A worker that does not answer is skipped rather than assumed empty, so a NATS blip cannot delete a node's rows. Where the port probe still runs, it no longer conflates failure modes: `DeadlineExceeded` means busy, `Unavailable` means gone, and only the second counts toward a threshold that now needs three consecutive misses.

Every routed model also left an in-process stub in the frontend's `ModelLoader`, and removal paths deleted only the database row, so the stub outlived the replica and the model was reported as loaded forever. The replica-removed hook became a list, and a new invalidator drops the local stub once no healthy replica remains anywhere in the cluster.

Alongside those, `in_flight` counters no longer leak high and pin a replica's VRAM against eviction, model-load deadlines scale with checkpoint size and with progress rather than wall-clock, staging verification counts as progress rather than as a stall, backend discovery stopped hiding worker-installed and GPU-only backends behind the controller's own filesystem, and the scheduler will not place a model on a node that cannot store it. The full list is in [#11142](https://github.com/mudler/LocalAI/pull/11142) and the eighteen PRs around it.

## A security fix you should read

`POST /api/fine-tuning/jobs` accepted `reward_functions[].code`, an inline Python body that ran against a hand-rolled builtin allowlist. That allowlist was not a security boundary. Standard CPython introspection reaches the real `os` module from inside it, which is arbitrary code execution on the host, and the execution happened during a smoke test at job start on an endpoint that is unauthenticated by default.

Inline reward code is now refused unless the operator sets `LOCALAI_TRL_ALLOW_INLINE_REWARD=true` on the backend. Builtin reward functions are unaffected and need no configuration. The documentation no longer describes the allowlist as a sandbox ([#11068](https://github.com/mudler/LocalAI/pull/11068)). This release also picks up hono 4.12.25 for CVE-2026-54290 ([#11023](https://github.com/mudler/LocalAI/pull/11023)).

## The rest, briefly

Hugging Face model artifacts are now a managed snapshot flow: immutable snapshot resolution, authenticated downloads with real progress, materialization on gallery install and preload, runtime binding to staged artifacts, and per-file resume of an interrupted download rather than starting over. Python backends reuse the Go download path instead of fetching on their own.

The traces panel gained a sortable User column, plus client IP and user agent in the expanded row, taken from echo's `RealIP()` so a trusted proxy is honored.

Documentation got an onboarding overhaul driven by a per-page audit: one model, `qwen3-4b`, now carries through install, the web UI and a curl call; there is a new walkthrough for building your first agent; and a new runtime-errors reference is keyed on the literal error strings users actually see.

Fifteen people contributed to this release, five of them for the first time. The gallery went from 1,221 entries to 1,476.

To upgrade, pull `localai/localai:latest` or re-run the install script. The [full changelog](https://github.com/mudler/LocalAI/compare/v4.7.1...v4.8.0) has the other 180 pull requests.
