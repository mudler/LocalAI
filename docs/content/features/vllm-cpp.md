+++
disableToc = false
title = "vllm.cpp backend"
weight = 16
url = "/features/vllm-cpp/"
+++

[vllm.cpp](https://github.com/mudler/vllm.cpp) is the LocalAI team's C++20 port of
vLLM: the same continuous-batching scheduler, paged KV cache and automatic prefix
caching, with no Python at inference time. LocalAI serves it through the native
`vllm-cpp` backend, which loads either a HuggingFace safetensors directory or a
`.gguf` file and applies the chat template, tool-call parsing and reasoning split
inside the engine.

This page covers installing the backend and the models LocalAI ships ready to run
on it. For the full `engine_args` reference (KV sizing, scheduling policy,
speculative decoding, LMCache), see the
[vllm.cpp section of the text generation guide]({{% relref "features/text-generation" %}}#vllmcpp).

## Installing

```bash
local-ai backends install vllm-cpp
```

Or install it from the **Backends** page in the web UI. Images are published for
CPU, CUDA 13, Vulkan, Metal and Jetson L4T.

### Which GPUs the CUDA images cover

The CUDA images are currently built for **Blackwell-family architectures only**:
`sm_120a` (RTX 50 series, RTX PRO 6000 Blackwell) and `sm_121a` (GB10 / DGX
Spark) on x86-64, and `sm_121a` alone on arm64. CUDA 13 is required, so there is
no CUDA 12 variant.

That is narrower than vllm.cpp itself, which builds ten architectures. On an
Ampere, Ada, Hopper, Jetson Orin or Jetson Thor GPU the CUDA backend installs
successfully and then fails at the first request with `no kernel image is
available for execution on the device`. Until the build widens, use the Vulkan
or CPU image on those cards: `vulkan-vllm-cpp` builds with CUDA off entirely and
gives them a GPU path, without the NVFP4 and Marlin kernels.

## Ready-made models

The model gallery carries a curated set of vllm.cpp configurations. Each one
arrives with the engine settings already applied, so tool calling, the reasoning
split and speculative decoding work without hand-editing YAML.

| Gallery entry | Model | Size | Needs |
|---|---|---|---|
| `qwen3.6-27b-nvfp4-vllm-cpp` | Qwen3.6-27B, NVFP4 | 25 GB | Blackwell GPU |
| `qwen3.6-27b-nvfp4-mtp-vllm-cpp` | the same, with MTP speculative decoding | 25 GB | Blackwell GPU |
| `qwen3.6-27b-nvfp4-dflash-vllm-cpp` | the same, with DFlash speculative decoding | 28 GB | Blackwell GPU |
| `qwen3.6-35b-a3b-nvfp4-vllm-cpp` | Qwen3.6-35B-A3B MoE, NVFP4 | 23 GB | Blackwell GPU |
| `qwen3.6-35b-a3b-nvfp4-mtp-vllm-cpp` | the same, with MTP speculative decoding | 23 GB | Blackwell GPU |
| `qwen3-coder-30b-a3b-vllm-cpp` | Qwen3-Coder-30B-A3B, bf16 | 57 GB | Blackwell GPU, or CPU |
| `qwen3-4b-vllm-cpp` | Qwen3-4B, bf16 | 8 GB | CPU, Metal, Vulkan, Blackwell GPU |
| `qwen3-0.6b-vllm-cpp` | Qwen3-0.6B, bf16 | 1.4 GB | CPU, Metal, Vulkan, Blackwell GPU |

```bash
local-ai models install qwen3-0.6b-vllm-cpp
```

The two small bf16 entries are the ones that run anywhere the backend does,
including CPU. The NVFP4 entries need a Blackwell-class NVIDIA GPU on two counts:
NVFP4 has no kernel on older architectures, and the CUDA images are built only
for Blackwell in any case.

Sizing note: every entry sets `num_blocks` to give roughly one to four full
contexts of KV cache, which is a starting point rather than a tuned value. KV is
not free; the 4B entry, for instance, spends 144 KiB per token, so its 1024
blocks are about 4.5 GB on top of the weights. Raise `num_blocks` for more
concurrency, lower it on a small box.

### Why the 27B entries pin a revision

The Qwen3.6-27B entries pin their weights to a specific HuggingFace commit rather
than tracking the repository's default branch. This is deliberate and worth
understanding before you copy one of these configs.

The upstream repository was later re-quantized in place, under the same name, from
NVFP4 to FP8 W8A8. A config that names the repository without a revision therefore
resolves to entirely different weights, with different numerics and different
performance, and nothing about the load reports that anything changed. Pinning is
what makes the entry reproducible:

```yaml
artifacts:
  - name: model
    target: model
    source:
      type: huggingface
      repo: unsloth/Qwen3.6-27B-NVFP4
      revision: 890bdef7a42feba6d83b6e17a03315c694112f2a
```

The same reasoning applies to any quantized community repository you depend on.

### Choosing between the speculative variants

Speculative decoding trades memory for decode throughput. All three Qwen3.6-27B
entries serve the same weights and produce the same quality; they differ only in
how tokens are proposed.

| Entry | Method | Extra weights | Extra memory |
|---|---|---|---|
| `qwen3.6-27b-nvfp4-vllm-cpp` | none | none | none |
| `qwen3.6-27b-nvfp4-mtp-vllm-cpp` | MTP, depth 1 | none, the draft head ships inside the checkpoint | about 3.6 GB |
| `qwen3.6-27b-nvfp4-dflash-vllm-cpp` | DFlash, 16-token blocks | a separate 3.5 GB drafter | drafter plus draft cache |

MTP drafts one token per step from a head that already lives in the target
checkpoint's own `mtp.*` tensors, so it costs no extra download. DFlash drafts a
whole 16-token block in one non-autoregressive pass from a separate drafter, which
is the larger win at the cost of a second checkpoint on disk.

Start with the plain entry if you are short on memory, and with the DFlash entry
if you are not.

### Tool calling

Every entry above sets `use_tokenizer_template: true` and disables LocalAI's
Go-side grammar path, so tool calls are detected and parsed by the engine's own
streaming parsers and arrive as real `tool_calls` on the OpenAI response.

The parser is normally auto-detected from the chat template, but one case cannot
be: Qwen3-Coder's tool dialect is byte-identical on the wire to another family's,
so template sniffing would pick the wrong parser. The
`qwen3-coder-30b-a3b-vllm-cpp` entry therefore names it explicitly, and any
Qwen3-Coder config you write yourself should do the same:

```yaml
engine_args:
  tool_parser: qwen3_coder
```

## Beyond text generation

The `vllm-cpp` backend also serves MiniMax-H3, which generates video and audio
jointly. See [Video generation]({{% relref "features/video-generation" %}}#minimax-h3-vllmcpp).
