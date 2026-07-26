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

## Apple Silicon: the MLX GEMM provider

`BUILD_TYPE=metal` builds the Metal backend with vllm.cpp's optional MLX
provider for the dense GEMM (`VLLM_CPP_MLX=on`, the default here). Upstream keeps
it off because it costs a ~19 MB `libmlx.dylib` plus a ~105 MB `mlx.metallib`;
this backend accepts that because the provider was measured to pay for it on an
Apple M4, against the native MSL GEMM in the SAME binary (arms toggled with
`VT_OP_PROVIDER_DISABLE=mlx`), Qwen3-1.7B-bf16 at p=512 g=128:

| Concurrency | MLX agg tok/s | native agg tok/s | speedup |
|--:|--:|--:|--:|
| 1 | 5.79 | 3.08 | 1.88x |
| 8 | 25.70 | 13.69 | 1.88x |
| 16 | 38.65 | 17.69 | 2.19x |

TTFT improves 2x to 3x, peak memory is unchanged, and the GEMM output is
bit-identical to the native kernel on every parity shape. MLX serves the dense
GEMM only: paged attention stays vllm.cpp's own kernel, because MLX has no
paged-KV primitive. Full disposition in vllm.cpp `docs/BENCHMARKS.md`,
"MLX GEMM provider A/B on Apple M4".

Build knobs:

- `VLLM_CPP_MLX=off` builds Metal without the provider: ~124 MB smaller, slower.
- `MLX_VERSION` pins the wheel (default `0.29.3`). MLX is consumed as the
  prebuilt pip wheel because building it from source needs `xcrun metal`, i.e. a
  full Xcode the macOS runners do not have.

Packaging vendors `libmlx.dylib`, `mlx.metallib` and MLX's MIT license into
`package/lib/`, and rewrites `libvllm.dylib`'s rpath to `@loader_path/lib`
(re-signing it, since `install_name_tool` invalidates the signature). The
metallib must stay beside `libmlx.dylib`: MLX looks for it there.

Testing: `make test` runs the unit specs; export `VLLM_CPP_MODEL=<model>` (and
optionally `VLLM_CPP_LIBRARY=<libvllm path>`) to enable the e2e specs.
