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

Strata runs its own model packs, not GGUF files alone. A pack is a directory that contains a
`tokenizer/` folder with `vocab.json`, `merges.txt`, `token_type.json`, `tokenizer.json` and
`chat_template.jinja`. See the [Strata model guide](https://github.com/Niko1221/Strata/blob/main/docs/MODELS.md).
Point the model's `model` option at the pack directory.

## Limits

No embeddings, no vision input, no grammar, no logprobs, one request at a time. Tool calls
are passed to the chat template, but the output is not parsed into tool calls.

## Versions

The Strata release and the SHA-256 of every engine package are pinned in
`backend/go/strata/Makefile`. A nightly job (`bump-strata` in `.github/workflows/bump_deps.yaml`)
opens a pull request when Strata publishes a release. The backend speaks an unversioned
line protocol, so review each bump against `backend/go/strata/README.md`.
