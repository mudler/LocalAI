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
| Vulkan GPU, Linux x86_64 and arm64 | `vulkan-colibri` | Sets `COLI_VULKAN=1`. An engine that finds no Vulkan device runs on the CPU. |
| Apple Silicon | `metal-colibri` | macOS on arm64. Not tested by LocalAI yet. |

There is no image for NVIDIA CUDA on Linux yet, because colibri publishes no Linux CUDA build.
On an NVIDIA machine LocalAI installs the CPU image; install `vulkan-colibri` to use the GPU
through Vulkan.

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
archive without changes, and a portable CPython from
[python-build-standalone](https://github.com/astral-sh/python-build-standalone).
