+++
disableToc = false
title = "gufo backend"
weight = 86
url = "/features/gufo/"
+++

[gufo](https://github.com/gufo-org/gufo) is an MIT-licensed inference engine
with hand-written HIP kernels for the AMD Strix Halo APU (Ryzen AI MAX+ 395 with
Radeon 8060S, `gfx1151`). LocalAI exposes it through the `gufo` backend, which
serves chat completions for the models that gufo supports: Qwen3.8 27B, Qwen3.8
Flash-Next and DeepSeek V4 Flash.

gufo is developed by the gufo-org project, not by the LocalAI project.

## Hardware

gufo builds and runs **only on Linux x86-64 with a `gfx1151` GPU**. The backend
checks the GPU when it loads a model and refuses the load on any other GPU, with
a message that names the problem. There is no CPU, CUDA, Metal or arm64 build.
Because LocalAI cannot yet tell a `gfx1151` from other AMD GPUs, it never
selects `gufo` automatically: install it and set `backend: gufo` yourself, or
install one of the gallery models below.

The backend image is `quay.io/go-skynet/local-ai-backends:latest-gpu-rocm-hipblas-gufo`
(ROCm 7.2.1).

### Container access to the GPU

The container that runs the backend needs the ROCm device nodes and the groups
that own them:

- Pass `/dev/kfd` and `/dev/dri` into the container (for example
  `--device /dev/kfd --device /dev/dri`).
- The user in the container must be able to read and write `/dev/kfd` and
  `/dev/dri/renderD*`. On the host these usually belong to the `render` and
  `video` groups. Groups named `render` or `video` inside the container do not
  keep the host's supplementary groups; with rootless Podman use
  `--group-add keep-groups` (this needs the `crun` runtime).
- If ROCm reports no device, check `id` and `ls -l /dev/kfd /dev/dri/renderD*`
  inside the container.

On Fedora and other SELinux-enforcing hosts, GPU enumeration can succeed while
SELinux blocks the mapping of `/dev/kfd`, and ROCm then reports a misleading
"Memory critical" error. gufo's README describes the check and the fix. Look
for a denied `map` in the host audit log:

```bash
sudo ausearch -m avc -ts recent | grep -E '/dev/kfd|hsa_device_t'
```

If there is one, Podman documents this setting:

```bash
sudo setsebool -P container_use_devices true
```

This setting lets all containers on the host use the devices passed into them.
Review that scope before you enable it.

### Memory

gufo targets the 128 GiB Strix Halo configuration. The figures below are from
gufo's own documentation; LocalAI did not measure them.

| Model | Download | gufo's memory note |
|---|---|---|
| Qwen3.8 27B UD-Q4_K_XL | 17.6 GB, plus 0.93 GB projector and 1.14 GB DFlash2 drafter | 16.35 GiB of weights; about 38 GiB peak at a 262144-token capacity |
| Qwen3.8 Flash-Next UD-Q4_K_XL | 111.3 GB in four shards, plus 2.79 GB MTP predictor and 0.91 GB projector | about 86 GiB peak at a 133121-token capacity |
| DeepSeek V4 Flash 0731 IQ2XXS | 86.7 GB, plus 5.99 GB DSpark support model | 80.76 GiB of weights; use a 128 GiB system |

Memory grows with the context size and with `sessions`, because each session
reserves its own state. Reduce `context_size` or `sessions` if a load fails for
lack of memory.

## Installing

```bash
local-ai backends install gufo
```

To build the backend image locally:

```bash
make BUILD_TYPE=hipblas BASE_IMAGE=rocm/dev-ubuntu-24.04:7.2.1 backends/gufo
```

## Gallery models

| Gallery name | Weights | Speculative decoding | License |
|---|---|---|---|
| `qwen3.8-27b:gufo` | Qwen3.8-27B UD-Q4_K_XL + vision projector | none | Apache-2.0 |
| `qwen3.8-27b-dflash2:gufo` | same, plus the z-lab DFlash2 Q4_K_M drafter | DFlash2 | Apache-2.0 |
| `qwen3.8-flash-next-mtp:gufo` | Qwen3.8-Flash-Next UD-Q4_K_XL (4 shards) + shared Q8_0 MTP predictor + vision projector | MTP | Qwen Community License 1.0 |
| `deepseek-v4-flash-dspark:gufo` | antirez's DeepSeek-V4-Flash 0731 mixed IQ2/Q2/Q8 GGUF + DSpark support model | DSpark | MIT |

```bash
local-ai models install qwen3.8-27b-dflash2:gufo
```

Each entry sets the model's recommended sampling, because LocalAI sends an
explicit temperature and top_p on every request and would otherwise replace
gufo's per-model preset with its own defaults:

- Qwen3.8 entries: temperature 1.0, top_p 0.95, top_k 20 (the Qwen thinking
  preset). If you turn thinking off, Qwen recommends temperature 0.7, top_p 0.8
  and presence_penalty 1.5; set those in the model YAML or the request.
- DeepSeek entry: temperature 1.0, top_p 0.95.

## Model YAML

```yaml
name: qwen3.8-27b-dflash2
backend: gufo
context_size: 32768
parameters:
  model: gufo/models/qwen3.8-27b/Qwen3.8-27B-UD-Q4_K_XL.gguf
  temperature: 1.0
  top_p: 0.95
  top_k: 20
# Optional vision projector, relative to the models directory.
mmproj: gufo/models/qwen3.8-27b/mmproj-BF16.gguf
template:
  # Required: gufo renders the chat template itself from structured messages.
  use_tokenizer_template: true
function:
  grammar:
    disable: true
known_usecases:
  - chat
  - vision
options:
  - speculative:dflash2
  # Relative paths are resolved beside the model file.
  - draft_model:Qwen3.8-27B-DFlash2-Q4_K_M.gguf
```

- `parameters.model` is the GGUF file. For a split GGUF, point it at the first
  shard; gufo finds the other shards in the same directory.
- `mmproj` is resolved relative to the models directory, as for other backends.
- `draft_model` and `cache_disk` are resolved relative to the directory of the
  model file when they are not absolute.
- gufo's chat templates are compiled into the engine. Keep
  `use_tokenizer_template: true` so LocalAI sends structured messages; a plain
  prompt without messages goes through gufo's raw completion path.

## Options

Options are `key:value` entries in `options:`, split on the first colon.
**An unknown key or an invalid value fails the load** with a message that names
the key, so a typing error does not silently fall back to a default.

| Option | Default | Description |
|---|---|---|
| `speculative` | not set (autoregressive) | `off`, `dflash2`, `mtp` or `dspark`. Every mode except `off` needs `draft_model`. The mode must match the model: gufo refuses a mismatch. |
| `draft_model` | not set | The drafter GGUF: the DFlash2 drafter, the MTP predictor or the DSpark support model. |
| `draft_tokens` | `7` | Maximum proposals for each speculative step, 1 to 64. |
| `min_draft_tokens` | `1` | Minimum proposals for each speculative step, 1 to 64. Must not be more than `draft_tokens`. |
| `sessions` | `1` | Number of requests that generate at the same time, 1 to 64. Each session reserves its own context state. |
| `max_pending` | `16` | Maximum number of requests that are active or waiting, in total. |
| `max_pending_per_client` | `4` | Maximum number of requests that are active or waiting for one client. Must not be more than `max_pending`; when it is not set and `max_pending` is smaller than 4, it follows `max_pending`. |
| `request_timeout_ms` | `0` (no timeout) | Time limit for one request, 0 to 86400000 (24 hours). |
| `max_output_bytes` | `1048576` (1 MiB) | Maximum size of the output of one request. |
| `max_buffered_output_bytes` | `8388608` (8 MiB) | Output that one request can buffer while the client reads slowly. |
| `max_buffered_output_bytes_total` | `33554432` (32 MiB) | Output that all requests together can buffer. |
| `think` | model default (on) | `true` or `false`. Turns thinking on or off for all requests. |
| `preserve_thinking` | model default | `true` or `false`. Keeps the reasoning of earlier turns in the prompt. |
| `reasoning_effort` | model default | `minimal`, `low`, `medium`, `high`, `xhigh` or `max`. Cannot be set together with `think:false`. |
| `cache_disk` | not set (no disk cache) | Directory for gufo's on-disk conversation cache. |
| `cache_disk_bytes` | `0` (gufo's default, 8 GiB) | Size limit of the on-disk cache. |

The two buffered-output defaults are larger than gufo's own (64 KiB and
256 KiB): with gufo's defaults a gRPC client that reads slowly could make a
request fail with an output backpressure error.

### Concurrency

LocalAI sends no client identity to the backend, so gufo counts every request
as coming from the same client. Concurrency is therefore bounded by
`max_pending_per_client` (4 by default), even when `sessions` and `max_pending`
are larger. To serve more requests at the same time, raise the three options
together, and make sure the memory is sufficient for the extra sessions:

```yaml
options:
  - sessions:4
  - max_pending:32
  - max_pending_per_client:32
```

## Thinking

Thinking is **on by default** in gufo's compiled-in templates: Qwen3.8 uses
`xhigh` effort and keeps earlier reasoning; DeepSeek uses `high` effort.
The reasoning is returned in `reasoning_content`.

To turn thinking off for a model, set the `think:false` option, or set
`reasoning.disable: true` in the model YAML. For one request, send
`"reasoning_effort": "none"` (or a level to change the effort).

## Limits in this release

- **Chat only.** gufo also runs speech recognition, speech synthesis, image
  and video models; LocalAI will add them later.
- **No `grammar` and no `response_format`.** A request that carries a grammar,
  including one that LocalAI derives from `response_format`, is refused with
  `gufo: grammar and response_format are not supported yet`. Tool calls work:
  gufo parses them with its own parsers.
- **At most 16 images** for each request. Images attach to the last user turn.
  DeepSeek V4 Flash is text only.
- **One DeepSeek instance for each host.** gufo's DeepSeek runtime takes an
  exclusive lock on `/tmp/ds4.lock` (the `DS4_LOCK_FILE` environment variable
  moves it). A second DeepSeek model that shares that `/tmp` fails to load.
- Temperature is applied exactly as sent, so `temperature: 0` gives greedy
  output.

## Licenses

The gufo engine is MIT. The model weights have their own licenses: Qwen3.8 27B
and its DFlash2 drafter are Apache-2.0, Qwen3.8 Flash-Next is under the Qwen
Community License 1.0, and DeepSeek V4 Flash is MIT. Read the model license
before you use the weights.
