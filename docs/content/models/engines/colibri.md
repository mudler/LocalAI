+++
disableToc = false
title = "colibri backend (experimental)"
weight = 31
devbuild = "feature"
+++

[colibri](https://github.com/JustVugg/colibri) runs very large mixture-of-experts models, from
about 300B to 1T parameters, on one machine with little RAM. It keeps the dense weights in
memory and reads the experts from disk while it generates, with a cache of the experts that a
model uses most. colibri is a separate project. LocalAI wraps it in the experimental `colibri`
backend.

{{% callout style="warning" %}}
This backend is experimental. LocalAI has tested it on a CPU with a small model (Qwen3-0.6B):
load, chat, streaming, reasoning, cancel and the restart after a crash. LocalAI has not run a
large model with it yet. The speed and memory figures of colibri are colibri's own. Expect a few
tokens per second, or less, when the experts are read from disk.
{{% /callout %}}

## How it works

The backend starts colibri's own server, `coli serve`, as a child process. The server listens
on a free port on `127.0.0.1` only, with a random API key, so other users of the machine
cannot use it. The backend sends the requests to the OpenAI-compatible API of that server.

colibri renders the chat template of the model and reads the tool calls in the model's own
format. LocalAI sends the structured chat (the messages, the tools, `tool_choice` and the
reasoning settings) and gets back the answer, the reasoning and the tool calls as separate
parts. This is the same flow as the llama.cpp backend with `use_tokenizer_template`. A request
with a plain prompt goes to colibri's completions endpoint.

## Installing

```bash
local-ai backends install colibri
```

LocalAI picks the image that fits the machine. The image contains everything the backend
needs: the colibri release (the engines and `coli serve`, pinned and checked by SHA-256) and a
portable Python 3, which `coli serve` needs. You do not install colibri or Python yourself.

| Hardware | Image | Notes |
|---|---|---|
| CPU, Linux x86_64 and arm64 | `cpu-colibri` | The default. |
| NVIDIA GPU, CUDA 12, Linux x86_64 | `cuda12-colibri` | Ampere and newer (sm_80, 86, 89, 90, 120), with a PTX fallback for newer cards. |
| NVIDIA GPU, CUDA 13, Linux x86_64 | `cuda13-colibri` | The same targets, plus GB10 (sm_121). |
| NVIDIA arm64, CUDA 13 | `cuda13-nvidia-l4t-arm64-colibri` | Jetson AGX Orin (sm_87), Jetson Thor (sm_110) and DGX Spark (sm_121). |
| Vulkan GPU, Linux x86_64 and arm64 | `vulkan-colibri` | Sets `COLI_VULKAN=1`. An engine that finds no Vulkan device runs on the CPU. |
| Apple Silicon | `metal-colibri` | macOS on arm64. Not tested by LocalAI yet. |

colibri publishes no Linux CUDA build. The CUDA images compile colibri's engines from the
source of the pinned release (tag and commit) with colibri's own `CUDA=1` build option, and use
the release archive for everything else. colibri's own CI only checks that its CUDA code
compiles on Linux, and its releases build CUDA only for Windows, so the Linux CUDA path is less
tested upstream than the CPU and Vulkan paths. LocalAI has not run these images on a GPU yet.

The CUDA images turn on colibri's CUDA backend: `COLI_CUDA=1`, `CUDA_DENSE=1` (the dense
layers on the GPU), `CUDA_EXPERT_GB=auto` (a cache of experts that fills the free VRAM) and,
for DeepSeek V4, `DSV4_CUDA=1`. Set any of them in the model's `env` to change it, for example
`CUDA_EXPERT_GB: "20"` to leave VRAM for other models, or `COLI_CUDA: "0"` to run on the CPU.
The engines without a CUDA backend (Qwen-Image, Laya and GLiNER-Decide) are the CPU builds of
the release.

The pins move with colibri's releases: a scheduled job opens a pull request that updates the
release tag, its commit and the SHA-256 of every archive together.

## Models

The backend accepts two kinds of model:

- A GGUF file. On the first load the backend runs `coli import` once. This writes a small
  colibri model directory next to the GGUF file, named after it (`Model-Q4_K_M.gguf` gives
  `Model-Q4_K_M.colibri`). The directory points at the GGUF file; the weights are not copied.
  For a split GGUF, give the first part.
- A colibri model directory (it has a `config.json`), for example one made by `coli import` or
  `coli convert`.

colibri supports only the model families it has an engine for (GLM-5.x, Qwen3.6, Qwen3.8,
DeepSeek V4, Kimi K3, MiMo, Inkling, OLMoE and others). See the
[colibri README](https://github.com/JustVugg/colibri) for the list.

The model gallery has the models that colibri's own setup offers (`coli setup`), with every
file pinned to a Hugging Face commit and checked by SHA-256. Install one with:

```bash
local-ai models install qwen3.6-35b-a3b-colibri
```

| Gallery name | Model | Download | RAM: minimum, as measured |
|---|---|---|---|
| `qwen3.6-35b-a3b-colibri` | Qwen3.6-35B-A3B, colibri int4 container | 23 GB | 10 GB, 20 GB |
| `qwen3-coder-30b-a3b-colibri` | Qwen3-Coder-30B-A3B, colibri int4 container | 19 GB | 8 GB, 18 GB |
| `inkling-2-mini-ud-q4_k_xl-colibri` | Inkling-2-Mini, Unsloth UD-Q4_K_XL GGUF | 18 GB | 6 GB, 21 GB |
| `inkling-2-mini-ud-iq2_xxs-colibri` | Inkling-2-Mini, Unsloth UD-IQ2_XXS GGUF | 9 GB | 4 GB, 12 GB |
| `qwen3.8-flash-next-ud-iq2_xs-colibri` | Qwen3.8-Flash-Next, Unsloth UD-IQ2_XS GGUF with the MTP head | 80 GB | 16 GB, 54 GB |
| `qwen3.8-flash-next-ud-iq3_xxs-colibri` | Qwen3.8-Flash-Next, Unsloth UD-IQ3_XXS GGUF with the MTP head | 88 GB | 16 GB, 62 GB |
| `deepseek-v4-flash-reap-150b-colibri` | DeepSeek V4 Flash REAP 150B, official weight format | 85 GB | 16 GB, 32 GB |
| `deepseek-v4-flash-colibri` | DeepSeek V4 Flash 284B, official checkpoint | 167 GB | 16 GB, 32 GB |
| `qwen3.8-flash-next-fp8-colibri` | Qwen3.8-Flash-Next, official FP8 checkpoint | 186 GB | 24 GB, 32 GB |
| `mimo-v2.6-flash-colibri` | MiMo-V2.6 Flash 309B, official checkpoint | 172 GB | 32 GB, 52 GB |
| `glm-5.3-colibri` | GLM-5.3 744B, colibri int4 container | 419 GB | 16 GB, 24 GB |
| `glm-5.2-colibri` | GLM-5.2 744B, colibri int4 container with the MTP head | 429 GB | 16 GB, 24 GB |
| `inkling-colibri` | Inkling 975B, colibri container | 514 GB | 120 GB, 128 GB |
| `mimo-v2.6-pro-colibri` | MiMo-V2.6 Pro 1.02T, official checkpoint | 564 GB | 54 GB, 64 GB |

The RAM figures are colibri's (`coli setup`'s catalog): the minimum to start the model, and the
amount at which colibri measured it. For the models larger than RAM, put them on a fast NVMe SSD:
colibri reads the experts from disk while it answers. LocalAI has not run these entries yet.
colibri's Kimi K3 entries are not in the gallery, because they need a tokenizer step after the
download that the backend does not run, and its Qwen-Image entries are not, because this backend
generates text only.

To use a GGUF model from the import form, choose `colibri` as the backend. A model config by
hand:

```yaml
name: glm-5.3-flash
backend: colibri
context_size: 32768
parameters:
  model: GLM-5.3-Flash-UD-IQ2_XXS-00001-of-00003.gguf
template:
  use_tokenizer_template: true
function:
  grammar:
    disable: true
```

`use_tokenizer_template: true` makes LocalAI send the structured chat to colibri.
`grammar.disable: true` is necessary for tool calls: colibri reads the model's own tool-call
format and does not accept LocalAI's grammars.

## Options

| Model config | `coli serve` flag | What it does |
|---|---|---|
| `context_size` | `--ctx` | Context size in tokens. Without it, colibri chooses from the free memory. |
| `threads` | `OMP_NUM_THREADS` | Number of CPU threads. An `OMP_NUM_THREADS` in `env` takes priority. |
| `options: [parallel:N]` (or `kv_slots:N`) | `--kv-slots` | Number of conversations that the engine keeps at the same time. |
| `options: [ram:N]` | `--ram` | RAM budget in GB, which sets the size of the expert cache. |
| `options: [cap:N]` | `--cap` | Expert cache slots per layer. |
| `options: [gpu:...]` | `--gpu` | `auto`, `none`, or a device list such as `0,1`. |
| `options: [vram:N]` | `--vram` | VRAM budget in GB. |
| `options: [policy:...]` | `--policy` | `quality`, `balanced` or `experimental-fast`. |
| `options: [effort:...]` | `--effort` | Default reasoning effort: `minimal` to `xhigh`. |
| `options: [think:true]` | `--think` / `--no-think` | Reasoning on or off by default. |
| `options: [ngen:N]` | `--ngen` | Maximum tokens of an answer when a request does not set it. |
| `options: [max_queue:N]`, `[queue_timeout:S]` | `--max-queue`, `--queue-timeout` | colibri's request queue. |
| `options: [start_timeout:S]` | | Time to wait for `coli serve` to start. Default 600 seconds. |
| `options: [import_dir:PATH]` | | Where `coli import` writes the model directory. |
| `env` | | Environment variables for colibri, for example `COLI_VULKAN: "0"`. See colibri's [environment variables](https://github.com/JustVugg/colibri/blob/main/docs/ENVIRONMENT.md). |

## Requests

The backend sends these request fields to colibri: the messages (with tool calls, tool results
and earlier reasoning), `tools`, `tool_choice`, `max_tokens`, `temperature`, `top_p`, `stop`
(colibri accepts up to four), `logit_bias`, `logprobs` and `top_logprobs`, and the reasoning
settings `enable_thinking` and `reasoning_effort`.

colibri refuses what it cannot do with an error, and the backend passes that error on. For
example, colibri gives log probabilities only for some engines and not while streaming, and it
does not support a non-empty `logit_bias`.

The backend refuses these requests itself:

- A request with a grammar, which includes `response_format` with `json_object` or
  `json_schema`.
- Images, video and audio in the request.
- Tools in a request without structured messages (no `use_tokenizer_template`).

colibri has no setting for `top_k`, `min_p`, the repeat, presence and frequency penalties,
`typical_p`, `tfz`, mirostat and `seed`. LocalAI fills some of these with defaults on every
request, so the backend cannot tell a default from a value that you set. It does not send them.

Tool calls work only when the model writes the tool-call format of its colibri family. A model
that colibri runs with the engine of another family can write a format that colibri does not
read. Then the call is returned as text. LocalAI's test with Qwen3-0.6B, which runs on the
Qwen3.6 engine, had this result.

Embeddings and tokenization are not supported.

## When colibri stops

`coli serve` restarts its own engine when the engine stops. When `coli serve` itself stops, the
request that was in progress fails with the last lines that colibri wrote. The next request
starts `coli serve` again from the same configuration. If that fails too, the model stays in an
error state until it is loaded again, and each request returns the reason. The model status
shows the real state of the process.

## Licensing

colibri is licensed under the Apache License 2.0. The images contain the upstream release
archive without changes (the CUDA images replace its engines with ones compiled from the same
release's source), a portable CPython from
[python-build-standalone](https://github.com/astral-sh/python-build-standalone), and in the
CUDA images the NVIDIA CUDA runtime and cuBLAS libraries, which keep their own licence. Each
model in the gallery has its own licence, which its entry names.
