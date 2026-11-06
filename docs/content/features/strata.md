+++
disableToc = false
title = "Strata backend (experimental)"
weight = 40
url = "/features/strata/"
+++

[Strata](https://github.com/Niko1221/Strata) is an MIT-licensed inference engine for large
mixture-of-experts models on consumer hardware. LocalAI exposes it through the experimental
`strata` backend. The backend starts the Strata engine as a child process and talks to it
over the engine's line protocol. Text generation only.

Strata is developed by its own authors, not by the LocalAI project.

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
(CUDA, ROCm or oneAPI). The ROCm image carries four engines, one per GPU family.

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
`backend/go/strata/README.md`). This takes a few minutes and a few GB of disk. A finished pack
is never built again. If the build fails, the load fails with the last lines of the tool.

```bash
local-ai models install qwen3.8-flash-next-ista-iq2_xs
```

**Qwen3.8-Flash-Next** (125B parameters, mixture of experts, Qwen Community License 1.0). The
n-gram table (about 29 GB in the ISTA-DASLab files) is read from disk, so use an SSD. RAM is
what the experts need plus about 10 GB; the entries map the experts from the files, so a
machine with less RAM still runs, slower. Add `--resident-experts` to `engine_args` to copy
the experts into RAM when there is room.

| Gallery name | Source and format | Download | Memory (RAM and VRAM) |
|---|---|---|---|
| `qwen3.8-flash-next-ista-q2_0` | ISTA-DASLab GSQ-RCO Q2_0, the fastest | 67 GB | about 38 GB |
| `qwen3.8-flash-next-ista-iq2_xs` | ISTA-DASLab IQ2_XS | 69 GB | about 39 GB |
| `qwen3.8-flash-next-ista-iq3_xxs` | ISTA-DASLab IQ3_XXS | 77 GB | about 47 GB |
| `qwen3.8-flash-next-ista-iq3_s` | ISTA-DASLab IQ3_S, the best quality | 85 GB | about 55 GB |
| `qwen3.8-flash-next-coder-iq1_m` | ISTA-DASLab Coder IQ1_M, half the experts, for code | 59 GB | about 32 GB of RAM |
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
names. The Unsloth and Gyro files need the
pack tool's `--compat-bf16` option, which the entries set. NVFP4 packs run on NVIDIA GPUs only.
The MTP draft layer is not configured in any entry. The context size is 32768 tokens by
default; set `context_size` for more. For less RAM with the Unsloth files, add
`--resident-budget-gib N` to `engine_args`, as described in the
[Strata model guide](https://github.com/Niko1221/Strata/blob/main/docs/NVFP4.md).

**Qwen3.6-35B-A3B** (35B parameters, 3B active, Apache License 2.0). Unsloth's GGUF with the
MTP layer, which the engine uses for speculative decoding. It runs on NVIDIA and AMD GPUs; the
Intel Arc and Apple builds of Strata do not run it. The engine keeps every expert it can in
VRAM and computes the rest on the CPU. On the first load the backend only exports the tokenizer
from the GGUF file (`pack_tool:tokenizer`).

| Gallery name | Format | Download | RAM |
|---|---|---|---|
| `qwen3.6-35b-a3b-ud-q4_k_xl-strata` | Unsloth UD-Q4_K_XL | 24 GB | about 32 GB |
| `qwen3.6-35b-a3b-ud-q6_k_xl-strata` | Unsloth UD-Q6_K_XL | 34 GB | about 44 GB |
| `qwen3.6-35b-a3b-q8_0-strata` | Unsloth Q8_0 | 39 GB | about 52 GB |

**Licences.** Qwen3.8-Flash-Next models are under the Qwen Community License 1.0: free use, but it
asks for a separate licence from Qwen if you run a Model as a Service or AI Work Assistant
business with it, and for the model name to be shown in products above its user and revenue
limits. Swift 1.5 adds UkisAI's Swift Open License 1.0. The NVIDIA checkpoint adds the NVIDIA
Open Model License Agreement. Qwen3.6 is Apache 2.0. Read the `LICENSE` file of the repository
before you offer a model to other people. The ISTA-DASLab model cards also carry an Apache 2.0
tag; their text says the weights inherit the base model's licence, and the entries follow the
text.

Not in the gallery:

- DeepSeek V4.1-Flash (ISTA-DAS 1.75, 2.00 and 2.25 bpw sets and Coder). Strata runs it with a
  separate engine package (`strata-dsv41`) and its own tokenizer and chat format, which this
  backend does not carry or drive yet.
- NVIDIA's retrained FP4 checkpoint (`NVFP4-QAD`). Strata converts NVIDIA's 102 GB of files on
  the user's machine with a tool that needs a heavy Python stack.
- OrcaRouter's uncensored IQ3_XXS. Upstream documents it as a manual conversion.

You can still run these by building the pack with Strata and writing a model config by hand
(see `backend/go/strata/README.md`).

The gallery entries are experimental. The backend has not yet been run against a real
engine and model, and the pack build has not been run on a real model file.

## Limits

No embeddings, no grammar, no video or audio input, one request at a time. See
`backend/go/strata/README.md` for the full list, including images, logprobs, tool calls and
reasoning, which the backend supports.

## Versions

The Strata release and the SHA-256 of every engine package are pinned in
`backend/go/strata/Makefile`. A nightly job (`bump-strata` in `.github/workflows/bump_deps.yaml`)
opens a pull request when Strata publishes a release. The backend speaks an unversioned
line protocol, so review each bump against `backend/go/strata/README.md`.
