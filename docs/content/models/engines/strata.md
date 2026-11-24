+++
disableToc = false
title = "Strata backend (experimental)"
weight = 30
devbuild = "feature"
aliases = ["/features/strata/"]
+++

[Strata](https://github.com/Niko1221/Strata) runs very large mixture-of-experts models, such as a 125B Qwen, on one machine with a GPU, a lot of RAM and a lot of disk. Strata is a separate project. LocalAI wraps it in the experimental `strata` backend, which starts the Strata engine as a child process and talks to it over the engine's line protocol.

{{% callout style="warning" %}}
This backend is experimental. LocalAI's tests have not run the Qwen gallery entries against a real Strata engine and model, and the pack build has not been run on a real model file. The Hy4-Flash and Inkling 2 Mini entries have not been run on real hardware either: the backend was tested against a stub engine and against the real tokenizer data of the model file. The speed, memory and quality figures on this page are Strata's own.
{{% /callout %}}

Qwen entries, Tencent Hy4-Flash and Inkling 2 Mini take text and images.

## What you need

- A GPU from the [supported hardware](#supported-hardware) list.
- RAM and disk as listed for the model in [Models](#models). Even the smallest Qwen entry downloads about 24 GB, and the largest ones more than 250 GB. Tencent Hy4-Flash needs at least 64 GiB of RAM and about 130 GB of disk. Inkling 2 Mini is much smaller: from about 9 GB of disk, and a 4 GiB GPU is enough to start.
- An SSD or NVMe drive for the larger entries, because the n-gram table is read from disk.

## Installing

```bash
local-ai backends install strata
```

LocalAI picks the image that fits the machine. The image already contains everything the
backend needs: the engine, a portable Python (with numpy) for the tokenizer helper and the pack
tool, and the Strata Python modules. You do not build or download an engine yourself.

## Supported hardware

| Hardware | Image | Notes |
|---|---|---|
| NVIDIA GPU, CUDA 12 (x86_64) | `cuda12-strata` | GTX 10 and newer. NVIDIA driver 575 or newer. |
| NVIDIA GPU, CUDA 13 (x86_64) | `cuda13-strata` | RTX 20 series and newer. NVIDIA driver 580 or newer. |
| NVIDIA DGX Spark (arm64, CUDA 13) | `cuda13-nvidia-l4t-arm64-strata` | The engine is built for the GB10 chip (`sm_121`) only. Needs glibc 2.39 or newer. |
| AMD Radeon (x86_64, Linux) | `rocm-strata` | RX 9000 and Radeon AI PRO R9700, RX 7000, Strix Halo, and RX 6000. The image holds one engine per GPU family and the launcher picks the one that matches the card. Only the `amdgpu` kernel driver is needed, not a ROCm install. RX 6000 is built upstream but not tested on a real card. |
| Intel Arc (x86_64, Linux) | `intel-strata` | SYCL engine. Install the Intel compute runtime on the host (`libze1`, `intel-opencl-icd`). Tested upstream on Arc A-series only. |
| Apple Silicon | `metal-strata` | macOS 15 or newer. |

These targets have no Strata engine, so there is no image for them: CPU only, Vulkan,
NVIDIA Jetson (JetPack 6, CUDA 12 on arm64), other AMD GPU families, and Intel Macs. On
those hosts `local-ai backends install strata` reports that no variant fits the machine.

The images are large because each engine package carries its own GPU runtime libraries
(CUDA, ROCm or oneAPI). The ROCm image carries four engines, one per GPU family. Each image
also carries the same set of engines for Tencent Hy4-Flash and for Inkling 2 Mini, which run on their own
engine programs (`strata-hy4` and `strata-inkling`) and have their own packages; the launcher picks the one
that matches the card, as it does for the Qwen engine. Upstream publishes both engines for the same targets as above.

## Licensing

The engine and the Strata Python modules are MIT licensed. The engine packages published
by Strata also bundle runtime libraries from NVIDIA (CUDA), AMD (ROCm) and Intel (oneAPI),
which keep their own licences. The images ship the upstream packages unchanged.

## Models

Strata does not run a single GGUF file. A model is a directory that holds the GGUF files and
a Strata pack (an index, the dense weights and the tokenizer). The model gallery installs all
of them in one step, with the `strata` backend, the model's own chat template, and the vision
projector for images. Every file is pinned to a Hugging Face commit and checked by SHA-256.

For the GGUF models the gallery downloads only the GGUF files. The backend builds the pack
with Strata's own tools the first time the model loads (the `pack_gguf` option; see
the [backend README](https://github.com/mudler/LocalAI/blob/master/backend/go/strata/README.md)). This takes a few minutes and a few GB of disk. A finished pack
is never built again. If the build fails, the load fails with the last lines of the tool. Deleting the model removes the pack too.

```bash
local-ai models install qwen3.8-flash-next-ista-iq2_xs
```

**Qwen3.8-Flash-Next** (125B parameters, mixture of experts, Qwen Community License 1.0). The
n-gram table (about 29 GB in the ISTA-DASLab files) is read from disk, so use an SSD. RAM is
what the experts need plus about 10 GB; with less RAM it still runs, at lower speed. Add `--resident-experts` to `engine_args` to copy
the experts into RAM when there is room.

| Gallery name | Source and format | Download | Memory |
|---|---|---|---|
| `qwen3.8-flash-next-ista-q2_0` | ISTA-DASLab GSQ-RCO Q2_0, the fastest | 67 GB | about 38 GB |
| `qwen3.8-flash-next-ista-iq2_xs` | ISTA-DASLab IQ2_XS | 69 GB | about 39 GB |
| `qwen3.8-flash-next-ista-iq3_xxs` | ISTA-DASLab IQ3_XXS | 77 GB | about 47 GB |
| `qwen3.8-flash-next-ista-iq3_s` | ISTA-DASLab IQ3_S, the best quality | 85 GB | about 55 GB |
| `qwen3.8-flash-next-coder-iq1_m` | ISTA-DASLab Coder IQ1_M, half the experts, for code | 59 GB | about 32 GB of RAM |
| `qwen3.8-flash-next-unsloth-ud-iq1_m-v3.1` | Unsloth UD-IQ1_M (Dynamic v3.1), the smallest download | 68 GB | about 45 GB |
| `qwen3.8-flash-next-unsloth-ud-q2_0-v3.1` | Unsloth UD-Q2_0 (v3.1) | 71 GB | about 47 GB |
| `qwen3.8-flash-next-unsloth-ud-iq2_xs-v3.1` | Unsloth UD-IQ2_XS (v3.1) | 76 GB | about 52 GB |
| `qwen3.8-flash-next-unsloth-ud-q2_k_xl-v3.1` | Unsloth UD-Q2_K_XL (v3.1) | 80 GB | about 56 GB |
| `qwen3.8-flash-next-unsloth-ud-iq3_xxs-v3.1` | Unsloth UD-IQ3_XXS (v3.1) | 84 GB | about 60 GB |
| `qwen3.8-flash-next-unsloth-ud-q3_k_xl-v3.1` | Unsloth UD-Q3_K_XL (v3.1) | 90 GB | about 66 GB |
| `qwen3.8-flash-next-unsloth-ud-iq4_xs-v3.1` | Unsloth UD-IQ4_XS (v3.1), a newer file than the one below | 94 GB | about 70 GB |
| `qwen3.8-flash-next-unsloth-ud-iq4_xs` | Unsloth UD-IQ4_XS | 95 GB | about 72 GB for all experts |
| `qwen3.8-flash-next-unsloth-ud-q4_k_xl` | Unsloth UD-Q4_K_XL | 112 GB | about 88 GB |
| `qwen3.8-flash-next-unsloth-ud-q5_k_xl` | Unsloth UD-Q5_K_XL | 159 GB | about 108 GB |
| `qwen3.8-flash-next-unsloth-ud-q6_k_xl` | Unsloth UD-Q6_K_XL | 170 GB | about 118 GB |
| `qwen3.8-flash-next-unsloth-q8_0` | Unsloth Q8_0, the reference | 189 GB | about 136 GB |
| `qwen3.8-flash-next-gyro-s` | agentionai Gyro-S, the smallest | 59 GB | about 28 GB |
| `qwen3.8-flash-next-gyro-m` | agentionai Gyro-M | 93 GB | about 35 GB |
| `swift-1.5-qwen3.8-flash-next-iq2_xs` | UkisAI Swift 1.5 fine-tune, IQ2_XS | 69 GB | about 39 GB |
| `swift-1.5-qwen3.8-flash-next-iq3_xxs` | Swift 1.5, IQ3_XXS | 77 GB | about 47 GB |
| `swift-1.5-qwen3.8-flash-next-iq3_s` | Swift 1.5, IQ3_S | 85 GB | about 55 GB |
| `qwen3.8-flash-next-nvfp4-gptq` | NVFP4 experts (GPTQ), ready Strata pack | 129 GB | about 80 GB of RAM |
| `qwen3.8-flash-next-nvfp4-nvidia` | NVIDIA's NVFP4 checkpoint, ready Strata pack | 129 GB | about 80 GB of RAM |

The ISTA-DASLab, Unsloth and Gyro files are those of the Hugging Face repositories of the same
names. The `-v3.1` entries are the Unsloth "Dynamic v3.1" files of 2026-11-04. They are new builds, not
re-uploads of the older entries, and the Hugging Face files of those older entries stay pinned
as they were. Upstream Strata has run some of these sets on real hardware; LocalAI has not run
them yet. The Unsloth and Gyro files need the
pack tool's `--compat-bf16` option, which the entries set. NVFP4 packs run on NVIDIA GPUs only.
The MTP draft layer is not configured in any entry. The context size is 32768 tokens by
default; set `context_size` for more. For less RAM with the Unsloth files, add
`--resident-budget-gib N` to `engine_args`, as described in the
[Strata model guide](https://github.com/Niko1221/Strata/blob/main/docs/NVFP4.md).

The memory columns come from Strata's guides. They are a minimum, and Strata does not state them the same way for every model.

**Qwen3.6-35B-A3B** (35B parameters, 3B active, Apache License 2.0). Unsloth's GGUF with the
MTP layer, which the engine uses for speculative decoding. It runs on NVIDIA and AMD GPUs; the
Intel Arc and Apple builds of Strata do not run it. The engine keeps every expert it can in
VRAM and computes the rest on the CPU. On the first load the backend only exports the tokenizer
from the GGUF file (`pack_tool:tokenizer`).

| Gallery name | Format | Download | Memory |
|---|---|---|---|
| `qwen3.6-35b-a3b-ud-q4_k_xl-strata` | Unsloth UD-Q4_K_XL | 24 GB | about 32 GB |
| `qwen3.6-35b-a3b-ud-q6_k_xl-strata` | Unsloth UD-Q6_K_XL | 34 GB | about 44 GB |
| `qwen3.6-35b-a3b-q8_0-strata` | Unsloth Q8_0 | 39 GB | about 52 GB |

**Tencent Hy4-Flash** (220B parameters, 12B active per token, 1M tokens of context, text and
images in, Tencent Hy License 1.0). Strata publishes three quantizations of it as `.strata` files
(not GGUF) with a shared Engram file that is read from disk while the model answers, and a small vision file.
A model entry downloads the build, the Engram file and the vision file. The three entries use one
directory, so the Engram and vision files are downloaded once and shared. There is no pack to build: the
backend reads the tokenizer and the chat template from the build file on the first load (seconds).

```bash
local-ai models install hy4-flash-sq-64-strata
```

| Gallery name | Made for | Download | Memory in use |
|---|---|---|---|
| `hy4-flash-sq-64-strata` | 64 GiB of RAM and a GPU with 8 GiB or more | about 130 GB | about 59.4 GiB |
| `hy4-flash-sq-96-strata` | 96 GiB of RAM, close to the full model | about 157 GB | about 85.1 GiB |
| `hy4-flash-sq-128-strata` | 128 GiB of RAM, the closest to the full model | about 181 GB | about 107.5 GiB |

The download is the build plus the 67.6 GB Engram file and the 0.93 GB vision file. Memory in use is
RAM and GPU memory together. All figures in this table are the model author's and Strata's own, not
measured by LocalAI. Use a fast SSD or NVMe drive: the Engram file is read at random while the
model answers. The entries set a context of 131072 tokens, Strata's default for this model; lower
`context_size` on a card with little memory. Speculative decoding (DFly2) is built into the engine
and is on by default. Strata's recommended sampling is temperature 0.9, top_p 1.0 and no top_k; the entries
set it. The reasoning effort of a request can be `none`, or any of the usual words, which the backend
maps to the model's `no_think`, `high` and `max`.

Options of the Hy4-Flash entries (set in `options:`; the backend README lists all options):

| Option | Meaning |
|---|---|
| `family:hy4` | Selects the Hy4-Flash engine and file layout. |
| `native`, `engram`, `vision` | The build file, the Engram file and the vision file, relative to the model directory. `vision:off` turns images off; the other two are required. |
| `drafter` | `dfly2` (default) or `off`. The drafter is used only if the engine says it has one. |
| `vram_experts_gb`, `ram_experts_gb` | Engine flags that cap how many gigabytes of experts go to the card and to RAM. Strata's own launcher computes them for a card; the entries set none, so the engine decides. Set them if the model does not fit your card. |

The engine takes no `logprobs` and no logit bias, and the backend refuses a request for them.

**Inkling 2 Mini** (Thinking Machines Lab, 26.4B parameters, 3.4B active per token, up to 1M tokens of
context, text and image input, tool calling, Apache 2.0). It has no thinking mode and answers directly.
It is much smaller than the models above, and it runs on its own engine, `strata-inkling`. There are two
kinds of entry. The Strata builds (`.strata` files, by Strata) carry their own DSpark drafter. The Unsloth
GGUF files are read as they are, with no conversion step, and use the DSpark drafter file that Unsloth
publishes. All entries use one directory, so the vision file (0.64 GB), the tokenizer files and the drafter
file are downloaded once. There is no pack to build: the backend makes the tokenizer from the model files on
the first load (seconds). A Strata build has the chat template inside it and needs the published
`tokenizer.json`; a GGUF needs the published `tokenizer.json` and `chat_template.jinja`. The entries download them.

```bash
local-ai models install inkling-2-mini-sq-10-strata
```

| Gallery name | Format | Download | Made for |
|---|---|---|---|
| `inkling-2-mini-sq-10-strata` | Strata SQ-10 | about 11 GB | a 12 GiB card or a 16 GiB Mac, whole in memory; mixed RAM and GPU from a 4 GiB card |
| `inkling-2-mini-sq-15-strata` | Strata SQ-15 | about 15 GB | a 16 GiB card or a 24 GiB Mac; mixed on a 12 GiB card |
| `inkling-2-mini-sq-20-strata` | Strata SQ-20 (lossless) | about 20 GB | a 24 GiB card or more, or 32 GiB or more of unified memory |
| `inkling-2-mini-ud-q8_k_xl-strata` | Unsloth UD-Q8_K_XL | about 24 GB | 24 GiB or more of GPU memory, or 32 GiB or more of RAM |
| `inkling-2-mini-ud-q4_k_xl-strata` | Unsloth UD-Q4_K_XL | about 19 GB | a 16 to 24 GiB card |
| `inkling-2-mini-ud-q4_k_m-strata` | Unsloth UD-Q4_K_M | about 18 GB | a 16 to 24 GiB card |
| `inkling-2-mini-ud-iq4_xs-strata` | Unsloth UD-IQ4_XS | about 17 GB | a 16 GiB card |
| `inkling-2-mini-ud-q3_k_xl-strata` | Unsloth UD-Q3_K_XL | about 15 GB | a 12 to 16 GiB card |
| `inkling-2-mini-ud-iq3_xxs-strata` | Unsloth UD-IQ3_XXS | about 13 GB | an 8 to 12 GiB card |
| `inkling-2-mini-ud-q2_k_xl-strata` | Unsloth UD-Q2_K_XL | about 10 GB | a 4 to 12 GiB card with about 16 GiB of RAM |
| `inkling-2-mini-ud-iq2_xxs-strata` | Unsloth UD-IQ2_XXS | about 10 GB | a 4 to 8 GiB card with about 16 GiB of RAM |
| `inkling-2-mini-ud-iq1_m-strata` | Unsloth UD-IQ1_M | about 9 GB | a 4 to 8 GiB card |
| `inkling-2-mini-ud-iq1_s-strata` | Unsloth UD-IQ1_S | about 9 GB | a 4 to 8 GiB card |

The sizing column is Strata's own guidance, not measured by LocalAI. The entries set a context of 32768 tokens
(the model reads up to 1M, and the cache grows with the context) and the model card's sampling, temperature 1.0 with no
truncation. LocalAI already has `inkling-2-mini` entries on the `llama-cpp` backend; these run the same model on Strata.

Options of the Inkling 2 Mini entries (set in `options:`; the backend README lists all options):

| Option | Meaning |
|---|---|
| `family:inkling` | Selects the Inkling engine and file layout. |
| `native`, `vision` | The model file and the vision file, relative to the model directory. `vision:off` turns images off. |
| `dspark` | The drafter file of a GGUF entry. A Strata build carries its own drafter. |
| `drafter` | `dspark` (default) or `off`. The drafter is used only if the engine says it has one. |
| `vram_experts_gb`, `ram_experts_gb` | Engine flags that cap how many gigabytes of experts go to the card and to RAM. The entries set none, so the engine decides. |

The engine takes no `logprobs` and no logit bias, and the backend refuses a request for them.

**Licences.** Qwen3.8-Flash-Next models are under the Qwen Community License 1.0: free use, but it
asks for a separate licence from Qwen if you run a Model as a Service or AI Work Assistant
business with it, and for the model name to be shown in products above its user and revenue
limits. Swift 1.5 adds UkisAI's Swift Open License 1.0. The NVIDIA checkpoint adds the NVIDIA
Open Model License Agreement. Qwen3.6 and Inkling 2 Mini are Apache 2.0. Tencent Hy4-Flash is under the Tencent Hy
License 1.0, derived from Apache 2.0 with extra commercial terms for very large Model as a
Service businesses. Read the `LICENSE` file of the repository
before you offer a model to other people. The ISTA-DASLab model cards also carry an Apache 2.0
tag; their text says the weights inherit the base model's licence, and the entries follow the
text.

DeepSeek V4.1-Flash is not supported: Strata v0.2.15 removed it, and the backend refuses `family:deepseek41`.

Not in the gallery:

- NVIDIA's retrained FP4 checkpoint (`NVFP4-QAD`). Strata converts NVIDIA's 102 GB of files on
  the user's machine with a tool that needs a heavy Python stack.
- OrcaRouter's uncensored IQ3_XXS. Upstream documents it as a manual conversion.

You can still run these by building the pack with Strata and writing a model config by hand
(see the backend README).

## Vision

For vision chat requests with `template.use_tokenizer_template: true`, send images as
`image_url` content parts on their conversation messages. LocalAI adds one placeholder
per decoded image to that message before Strata applies the model's chat template,
including images in earlier turns. There is no need to insert `media_marker` into the
text or change the model's chat template. The backend's optional `media_marker` setting
changes the placeholder used between LocalAI and Strata.

## Limits

The backend has no embeddings, no grammar and no video or audio input, and it serves one request at a time. Qwen entries support images, logprobs, tool calls and reasoning. Hy4-Flash supports images, tool calls and reasoning, and no logprobs. Inkling 2 Mini supports images and tool calls, has no thinking mode, and no logprobs. The backend README has the full list.

## Versions

Upstream versions each platform on its own, and a release doesn't always hold every package. One release can have the NVIDIA engine at one version and another platform's engine at an older one. Mixed versions are normal.

`backend/go/strata/Makefile` pins every package on its own: the release tag it comes from, its version and its SHA-256 (`STRATA_ENGINE_TAG_<package>`, `STRATA_ENGINE_VERSION_<package>` and `STRATA_ENGINE_SHA256_<package>`). The download URL is built from the package's own tag. `STRATA_VERSION` and `STRATA_COMMIT` name the newest release, and the backend's Python modules are fetched at that commit.

The Hy4-Flash and Inkling engine packages have their own pins of the same kind (`STRATA_HY4_TAG_<package>`, `STRATA_HY4_VERSION_<package>` and `STRATA_HY4_SHA256_<package>`, and the same with `STRATA_INKLING_`), and the same job moves them.

A nightly job (`bump-strata` in `.github/workflows/bump_deps.yaml`, script `.github/bump_strata.sh`) takes each package from the newest release that has it and opens one pull request when any pin changes. If a package is missing from the newest release, the job takes it from an older one and logs a notice. If a package is in no release, it keeps its pin and prints a warning. The job fails when a SHA-256 in the release's `SHA256SUMS.txt` differs from the digest GitHub computed for the uploaded file. The pull request lists each package that changed, with its old and new tag and version. The backend speaks an unversioned line protocol, so review each bump against the backend README.
