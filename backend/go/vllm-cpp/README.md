# vllm-cpp backend

LocalAI text-generation backend for [vllm.cpp](https://github.com/mudler/vllm.cpp),
the LocalAI-team C++20 port of vLLM (paged KV cache, continuous batching,
safetensors + GGUF loading, CUDA / CPU / Metal / Vulkan) with no Python at
inference time.

The backend dlopens the engine's stable C ABI (`libvllm`, `include/vllm.h`,
ABI v10) through purego:

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

The struct mirrors in `govllmcpp.go` are hand-written against one ABI version,
and the engine refuses to load against any other. Moving `VLLM_CPP_VERSION` in
the Makefile therefore means updating `abiVersion` plus the mirrors (and their
offsets in `vllmcpp_test.go`) in the same change; `make abi-check` compares the
pinned header against the bindings and the library build runs it first.

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

## Apple Silicon: the MLX GEMM provider (ON by default, gated to prefill)

`BUILD_TYPE=metal` builds vllm.cpp's MLX provider for the dense GEMM
(`VLLM_CPP_MLX=on`, the default here). It is on because upstream now SHAPE-GATES
it to prefill; it was briefly off in this branch's history, and that was correct
at the time for an ungated provider.

The gate matters more than the flag. MLX's steel GEMM wins prefill but loses
decode, because the provider pays an `mx::eval` synchronisation plus an output
memcpy on every call and decode makes ~112 calls *per token*. Measured on an
Apple M4, Qwen3-1.7B-bf16 warm at p=512 g=128:

| configuration | prefill TTFT | warm throughput |
|---|--:|--:|
| MLX **gated to prefill** (pin >= 89c46aeb) | **524.5 ms** | **24.37 tok/s, 97.6% of MLX-LM** |
| MLX ungated (older pins) | 537 ms | 12.7 tok/s |
| MLX off | 602 ms | 23.9 tok/s, 95.9% |

Ratios are against an MLX-LM baseline measured INTERLEAVED with ours over four
ABBA blocks (its spread 0.34%, ours 0.12%). An earlier revision of this file
claimed 99.1%; that used a two-run MLX-LM baseline containing an outlier and
overstated us by about 1.5 points.

**`VLLM_CPP_VERSION` and this flag are coupled.** Moving the pin back before
`89c46aeb` while leaving `VLLM_CPP_MLX=on` would take the middle row — roughly
half throughput. If you roll the pin back, roll the default back with it.

One caveat: MLX's GEMM is not bit-identical to the native kernel, so an MLX build
produces a different greedy sequence than a non-MLX one. That is a property of the
provider, not of the gate, and it predates this packaging. Full disposition in
vllm.cpp `docs/BENCHMARKS.md`.

Build knobs:

- `VLLM_CPP_MLX=off` builds Metal without the provider: ~124 MB smaller, and
  96.4% of MLX-LM instead of 99.1%.
- `MLX_VERSION` pins the wheel (default `0.29.4`). MLX is consumed as the
  prebuilt pip wheel because building it from source needs `xcrun metal`, i.e. a
  full Xcode the macOS runners do not have.

Packaging vendors `libmlx.dylib`, `mlx.metallib` and MLX's MIT license into
`package/lib/`, and rewrites `libvllm.dylib`'s rpath to `@loader_path/lib`
(re-signing it, since `install_name_tool` invalidates the signature). The
metallib must stay beside `libmlx.dylib`: MLX looks for it there.

Testing: `make test` runs the unit specs; export `VLLM_CPP_MODEL=<model>` (and
optionally `VLLM_CPP_LIBRARY=<libvllm path>`) to enable the e2e specs.
