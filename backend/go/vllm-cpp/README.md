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

## Apple Silicon: the MLX GEMM provider (OFF by default)

`BUILD_TYPE=metal` can build vllm.cpp's optional MLX provider for the dense GEMM
(`VLLM_CPP_MLX=on`). **It is OFF by default, because it is currently slower.**

This branch originally shipped it ON, on the strength of an A/B that had MLX at
1.88-2.19x against the native MSL GEMM. That measurement was honest when taken
and is now stale: vllm.cpp's Metal kernels have since improved several-fold
(mma prefill attention, vectorised decode V accumulation, a fused qk-norm-RoPE
preamble and more), so the native path no longer resembles the one MLX was
compared against.

Re-measured on the same Apple M4, same binary, arms toggled with
`VT_OP_PROVIDER_DISABLE=mlx`, Qwen3-1.7B-bf16 warm at p=512 g=128:

| | prefill TTFT | warm throughput |
|---|--:|--:|
| MLX provider ON | 1370 ms | **11.98 tok/s** |
| MLX provider OFF | 1400 ms | **22.06 tok/s** |

MLX's steel GEMM is still ~20% faster than ours in isolation, but the provider
pays a per-op `mx::eval` synchronisation plus an output `memcpy` (it cannot write
into our buffer). On prefill's ~112 GEMMs that overhead leaves +2%; on decode,
where the same sync is paid once per matmul per token, it costs 46%.

Turning it on is therefore only sensible for prefill-dominated workloads, and
even then the margin is small. Full disposition in vllm.cpp `docs/BENCHMARKS.md`,
"The MLX provider verdict".

Build knobs:

- `VLLM_CPP_MLX=on` builds the provider in: ~19 MB `libmlx.dylib` plus a ~105 MB
  `mlx.metallib`, and currently slower end to end. Off is the default.
- `MLX_VERSION` pins the wheel (default `0.29.3`). MLX is consumed as the
  prebuilt pip wheel because building it from source needs `xcrun metal`, i.e. a
  full Xcode the macOS runners do not have.

Packaging vendors `libmlx.dylib`, `mlx.metallib` and MLX's MIT license into
`package/lib/`, and rewrites `libvllm.dylib`'s rpath to `@loader_path/lib`
(re-signing it, since `install_name_tool` invalidates the signature). The
metallib must stay beside `libmlx.dylib`: MLX looks for it there.

Testing: `make test` runs the unit specs; export `VLLM_CPP_MODEL=<model>` (and
optionally `VLLM_CPP_LIBRARY=<libvllm path>`) to enable the e2e specs.
