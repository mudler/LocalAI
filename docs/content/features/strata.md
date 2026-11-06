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
backend needs: the engine, a portable Python for the tokenizer helper, and the Strata
Python modules. You do not build or download an engine yourself.

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

Strata does not run a single GGUF file. A model is a directory that holds a Strata pack
(`pack/` with `index.txt`, `native_experts.txt`, `dense.bin`, `experts.bin` and a `tokenizer/`
folder), plus GGUF side files: the dense weights, the n-gram (PLE) table and the token
embedding. The model gallery installs such a directory in one step. All entries use the
`strata` backend, the model's own chat template, and the vision projector for images.

| Gallery name | Format | Download | Hardware | Licence |
|---|---|---|---|---|
| `qwen3.8-flash-next-nvfp4-gptq` | NVFP4 experts (GPTQ), Strata pack | 129 GB | NVIDIA GPU with 12 GB of VRAM or more, about 80 GB of RAM | Qwen Community License 1.0 |
| `qwen3.8-flash-next-nvfp4-nvidia` | NVFP4 experts (NVIDIA's checkpoint), Strata pack | 129 GB | NVIDIA GPU with 12 GB of VRAM or more, about 80 GB of RAM | NVIDIA Open Model License Agreement, and the Qwen Community License 1.0 of the base model |

```bash
local-ai models install qwen3.8-flash-next-nvfp4-gptq
```

Both are Qwen3.8-Flash-Next (125B parameters, mixture of experts). The packs are published
on Hugging Face by Maximilian228 for the Strata NVFP4 work, and the gallery pins the
revision and the SHA-256 of every file. The n-gram table (48 GiB) is read from disk, so
use an SSD. NVFP4 packs run on NVIDIA GPUs only, so the entries need the CUDA build of the
backend. The MTP draft layer in the repositories is not configured. The context size is
32768 tokens by default; set `context_size` for more. With less than about 80 GB of RAM, add
`--resident-budget-gib N` to the `engine_args` option, as described in the
[Strata model guide](https://github.com/Niko1221/Strata/blob/main/docs/NVFP4.md).

The Qwen Community License 1.0 lets you use the model freely, but asks for a separate
licence from Qwen if you run a Model as a Service or AI Work Assistant business with it, and
for the model name to be shown in products above its user and revenue limits. Read the
`LICENSE` file in the repository before you offer the model to other people.

These models are not in the gallery, because Strata builds their pack on the user's
machine from GGUF files (`tools/iq_pack.py`), and the gallery can only download files:

- Qwen3.8-Flash-Next in the ISTA-DASLab (Q2_0, IQ2_XS, IQ3_XXS, IQ3_S and Coder), Unsloth,
  Swift 1.5 and Gyro quantizations, from about 60 to 190 GB.
- Qwen3.6-35B-A3B (Unsloth GGUF, Apache 2.0). It needs no pack, but the backend needs a
  `tokenizer/` folder that Strata exports from the GGUF file.
- DeepSeek V4.1-Flash, which uses a separate Strata engine that the backend image does not
  carry.

You can still run these by building the pack with Strata and writing a model config by hand
(see `backend/go/strata/README.md`).

The gallery entries are experimental. The backend has not yet been run against a real
engine and model.

## Limits

No embeddings, no grammar, no video or audio input, one request at a time. See
`backend/go/strata/README.md` for the full list, including images, logprobs, tool calls and
reasoning, which the backend supports.

## Versions

The Strata release and the SHA-256 of every engine package are pinned in
`backend/go/strata/Makefile`. A nightly job (`bump-strata` in `.github/workflows/bump_deps.yaml`)
opens a pull request when Strata publishes a release. The backend speaks an unversioned
line protocol, so review each bump against `backend/go/strata/README.md`.
