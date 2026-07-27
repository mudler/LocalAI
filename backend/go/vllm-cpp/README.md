# vllm-cpp backend

LocalAI text-generation backend for [vllm.cpp](https://github.com/mudler/vllm.cpp),
the LocalAI-team C++20 port of vLLM (paged KV cache, continuous batching,
safetensors + GGUF loading, CUDA / CPU / Metal / Vulkan) with no Python at
inference time.

The backend dlopens the engine's stable C ABI (`libvllm`, `include/vllm.h`,
ABI v2) through purego:

- `Load` -> `vllm_engine_load`: accepts a `.gguf` file or a HF-style model
  directory (`config.json` + safetensors). `context_size` maps to
  `max_model_len`; `options: ["block_size:<n>", "num_blocks:<n>",
  "max_num_seqs:<n>"]` size the KV cache and scheduler admission.
- `Predict` -> `vllm_complete` (blocking).
- `PredictStream` -> `vllm_complete_stream`; concurrent gRPC requests batch
  continuously in the engine's shared AsyncLLM scheduler.
- Chat / tool calling rides the SAME code path as the llama.cpp autoparser:
  with `use_tokenizer_template: true` the backend implements
  `PredictRich`/`PredictStreamRich` over the ABI v3 chat entry points
  (`vllm_chat` / `vllm_chat_stream`). The ENGINE applies the model's chat
  template (GGUF `tokenizer.chat_template` or `tokenizer_config.json`),
  decides when a tool call engages (`tool_choice: auto` lowers to a LAZY
  structural-tag decode constraint; `required`/named force one), parses tool
  calls with its streaming Hermes-style parser, and the backend maps each
  `chat.completion.chunk` onto `ChatDelta`/`ToolCallDelta` protos.
- Without structured messages the plain path applies:
  `PredictOptions.Grammar` -> the ABI's `structured_grammar` (GBNF) for
  LocalAI's Go-side grammar-constrained tool calling; JSON-schema / regex /
  choice constraints are also exposed by the ABI.

## Hardware coverage

The CUDA builds require the CUDA 13 toolchain and target Blackwell only:
`sm_120a` + `sm_121a` on x86_64, `sm_121a` (GB10 / DGX Spark) on arm64. CUDA
12.x nvcc cannot compile the Blackwell fp4 kernels, so no CUDA 12 variant is
shipped and `backend/index.yaml` maps the `nvidia-cuda-12` /
`nvidia-l4t-cuda-12` capabilities at the CPU build. Practically:

| Host | Installed build |
|---|---|
| x86_64 + CUDA 13 | `cuda13-vllm-cpp` |
| DGX Spark / GB10 (JetPack 7, CUDA 13) | `nvidia-l4t-arm64-vllm-cpp` |
| Jetson AGX Orin (sm_87, JetPack 6, CUDA 12) | `cpu-vllm-cpp` |
| Apple Silicon | `metal-vllm-cpp` |
| Anything else | `vulkan-vllm-cpp` or `cpu-vllm-cpp` |

The capability a host reports comes from `/run/localai/capability` inside the
LocalAI container, which the image bakes in at build time (see `Dockerfile`).
A DGX Spark running the CUDA 12 `-nvidia-l4t-arm64` image therefore reports
`nvidia-l4t-cuda-12` and gets the CPU build; use the `-nvidia-l4t-arm64-cuda-13`
image, or set `LOCALAI_FORCE_META_BACKEND_CAPABILITY=nvidia-l4t-cuda-13`, to
get the GPU one.

Model config example:

```yaml
name: qwen3-vllm
backend: vllm-cpp
context_size: 8192
parameters:
  model: Qwen3-4B   # model dir (safetensors) or .gguf file
options:
- max_num_seqs:16
```

Testing: `make test` runs the unit specs; export `VLLM_CPP_MODEL=<model>` (and
optionally `VLLM_CPP_LIBRARY=<libvllm path>`) to enable the e2e specs.
