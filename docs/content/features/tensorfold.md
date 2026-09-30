+++
disableToc = false
title = "TensorFold backend"
weight = 40
url = "/features/tensorfold/"
+++

[TensorFold](https://github.com/ashhart/TensorFold) is an MIT-licensed engine that
serves language models on Apple Silicon and NVIDIA GPUs. Each model family supplies
its own kernels and draft verification. LocalAI exposes it through the `tensorfold`
backend, which runs TensorFold in the backend process. LocalAI ships the Apple Silicon
(MLX) image today; the NVIDIA (CUDA 13) images follow.

TensorFold is developed by its own authors, not by the LocalAI project.

## Exact speculative decoding

TensorFold accepts a draft token only when it equals the token the same engine would
produce serially. Upstream states that exactness is against the same engine, weights,
runtime and settings. It does not imply identical output between MLX and CUDA, between
different quantizations, or between different tensor-parallel rank counts.

## Hardware

| Platform | Supported |
|---|---|
| Apple Silicon (MLX) | Yes |
| NVIDIA, compute capability 9.0 or newer (sm_90, sm_100, sm_120, sm_121), CUDA 13, including DGX Spark | Image follows; not installable yet |
| NVIDIA Ampere and Ada | No, refused at load time with an explicit message |
| AMD, Intel, Vulkan, CPU | No |

LocalAI does not publish a CUDA 13 image for this backend yet, so today it installs on
Apple Silicon only. The CUDA notes on this page describe how the backend behaves once
that image ships.

## Installing

```bash
local-ai backends install tensorfold
```

Or install it from the **Backends** page in the web UI. `tensorfold` is a
preference-only backend: LocalAI does not pick it automatically during model import,
because MLX repositories are ambiguous with the `mlx` and `mlx-vlm` backends. Set
`backend: tensorfold` in the model YAML, or select it explicitly in the import form.

## Model configuration

The backend downloads the weights itself, so a model config only names the Hugging
Face repository. Name the drafter explicitly: `drafter:auto` never downloads a drafter,
and the Qwen3.8 27B CUDA engine refuses to start without one unless `no_drafts` is set.

```yaml
name: qwen3.8-27b
backend: tensorfold
context_size: 8192
parameters:
  model: Vontra/Qwen3.8-27B-MLX-4bit
options:
  - drafter:z-lab/Qwen3.8-27B-DFlash2
template:
  use_tokenizer_template: true
```

Each loaded model needs its own backend process, because MLX memory limits are
process-wide. LocalAI already starts one backend process per loaded model.

## Load options

Set load options in the model `options` list as `key:value` (split on the first
colon). Each key maps to one `tensorfold serve` flag, so TensorFold validates the value
and supplies the default. **An unknown key fails the load.** TensorFold also refuses
options that do not apply to the chosen engine (for example an MLX-only option on
CUDA), and the backend passes that error through unchanged.

| Key | Meaning | Engine |
|---|---|---|
| `parallel` | Concurrent requests. MLX `auto` admits up to 8 within the memory budget; CUDA `auto` is 1, an explicit number enables supported shared rounds | Both |
| `context` | Prompt plus reply capacity. Defaults to the model's `context_size` | Both |
| `max_tokens` | Default reply limit | Both |
| `thinking` | `true` or `false`, the template thinking toggle. Upstream defaults thinking to on | Both |
| `reasoning_effort` | Template effort when a request sets none | Both |
| `thinking_budget` | Token-count limit inside reasoning | Both |
| `temperature`, `top_p`, `top_k`, `min_p` | Sampling defaults; temperature zero is greedy | Both |
| `drafter` | `auto`, `none`, a repository ID or a directory | Both |
| `no_drafts` | `true` to decode serially | Both |
| `mtp_drafts` | Family-specific cap on MTP drafts | Both |
| `mtp_confidence` | Flash Next: stop a draft chain before a later draft under this probability, 0 to 1 | CUDA |
| `kv_dtype` | `bf16`, `int8` or `int4`. Flash Next only | CUDA |
| `prompt_cache_gib` | Retained conversation-prefix budget; zero disables retention | MLX |
| `checkpoint_slots` | Number of retained conversation prefixes | MLX |
| `spill_gib` | Write evicted conversation prefixes to disk, up to this many GiB; zero disables | MLX |
| `snapshot_dir` | Directory for persistent prefix snapshots; `none` disables them | MLX |
| `max_snapshots` | Number of system-block snapshots loaded at start | MLX |
| `decode_share` | Share of each prefill chunk's time that running replies keep decoding | MLX |
| `mlx_cache_gib` | Reusable freed-buffer cache | MLX |
| `vision` | `true` to accept image input on compatible Qwen3.5/3.8 dense checkpoints | Both |
| `memory_limit_gb` | Process memory budget in GiB, exported as `TENSORFOLD_MEMORY_LIMIT_GB` | MLX |

LocalAI forwards requests to the backend concurrently, and the backend serves up to
`PYTHON_GRPC_MAX_WORKERS` calls at once (default 10, which leaves room for health checks
beside eight replies). Raise it together with `parallel` above 8.

Example:

```yaml
options:
  - drafter:z-lab/Qwen3.8-27B-DFlash2
  - thinking:false
  - parallel:2
```

## Limits

The CUDA items apply once the CUDA 13 image ships.

- On CUDA, Nemotron serves one request at a time, and `parallel:auto` also means one
  request at a time for every family.
- The first load on CUDA compiles TensorFold's kernels for the GPU that is present.
  This takes time, and later loads reuse the compiled kernels.
- GLM-5.3-Flash and two-GPU (two-rank) serving are not supported.

## Gallery models

The model gallery has TensorFold entries tagged `tensorfold`. Model weights keep their
own licenses:

| Entry | License |
|---|---|
| `qwen3.8-27b:tensorfold` | apache-2.0 |
| `nemotron-3.5-lightning-30b-a3b:tensorfold` | openmdw-1.1 |
| `gemma-4-26b-a4b:tensorfold` | apache-2.0 (Apple Silicon only) |
| `qwen3.8-flash-next:tensorfold` | qwen-community-1.0; read it before commercial use |

The DFlash drafters from z-lab are apache-2.0. All entries use MLX weights and run on
Apple Silicon. NVIDIA entries are added with the CUDA 13 image.
