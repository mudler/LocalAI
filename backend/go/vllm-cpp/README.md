# vllm-cpp backend

LocalAI backend for [vllm.cpp](https://github.com/mudler/vllm.cpp),
the LocalAI-team C++20 port of vLLM (paged KV cache, continuous batching,
safetensors + GGUF loading, CUDA / CPU / Metal / Vulkan) with no Python at
inference time.

It serves two things: text generation, and MiniMax-H3 joint video+audio
generation.

The backend dlopens the engine's stable C ABI (`libvllm`, `include/vllm.h`,
ABI v20) through purego:

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

`patches/` carries fixes the pinned engine SHA does not have yet, applied to
the clone the same way `longcat-video` patches its upstream. `git apply` is
unguarded on purpose: a patch that stops applying must fail the clone loudly
rather than leave a pin silently missing a fix it is documented to carry. Each
patch header says what retires it.

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

## MiniMax-H3 video+audio generation

`GenerateVideo` -> `vllm_video_generate` (ABI v12). H3 renders picture and sound
together, so the output MP4 carries a real AAC track.

The video engine is a SECOND handle (`vllm_video_engine`), not a mode of the
text one, because H3 is a checkpoint SET rather than a model directory: the DiT,
the text encoder and two VAEs are separate artifacts, and vllm.cpp has the two
loaders refuse each other's checkpoints. `Load` takes the video branch when the
model config carries any of the video options below; `parameters.model` is the
DiT and everything else is named in `options:`.

```yaml
name: minimax-h3-fl2va-q4
backend: vllm-cpp
cuda: true
known_usecases: [video]
parameters:
  model: minimax-h3/MiniMax-H3-FL2VA-Q4_K_M.gguf
options:
- video_encoder:minimax-h3/qwen3vl-32B-MiniMax-H3-Q4_K_M.gguf
- video_tokenizer:minimax-h3/tokenizer.json
- video_vae:minimax-h3/video_vae.safetensors
- video_vae_config:minimax-h3/video_vae_config.json
- audio_vae:minimax-h3/audio_vae.safetensors
- audio_vae_config:minimax-h3/audio_vae_config.json
- video_partition:fl2va
- video_device:cuda
- video_dequant_bf16:true
- video_width:1344
- video_height:768
- video_num_frames:124
```

Three things are worth knowing before touching this path.

**The partition is declared, not detected, and a mismatch does not fail
cleanly.** The FL2VA DiT serves `t2va` and `fl2va`; `ref2va` is a different
checkpoint. The community GGUF/NVFP4 quantisations strip the release metadata
and the two DiTs are byte-structurally identical, so the engine refuses every
generate until `video_partition` says which one it has. Handing reference
conditioning to an FL2VA DiT renders for hours and returns a coloured lattice
over the frame, so `checkPartitionConditioning` refuses that combination here,
before the engine is called.

**ffmpeg comes from the host.** libvllm writes the frames and the WAV and
COMPOSES the mux argv, then spawns nothing — that process boundary is upstream's
decision. `muxVideo` takes the composed argv, substitutes `argv[0]` with the
resolved binary and execs it; the backend image is `FROM scratch` and carries no
ffmpeg, the same arrangement `vibevoice-cpp` uses for transcoding. ffmpeg also
converts a `start_image`/`end_image` upload into the binary PPM at the exact
output canvas the engine requires, since libvllm vendors neither an image codec
nor a resampler.

**It is slow.** Roughly 176 s per denoise step at 1344x768 on a 20-SM device, so
the 50-step default is hours. Nothing here imposes a deadline.

Geometry mirrors the engine so the two agree: the canvas is truncated onto a
32-pixel grid, the frame count sits on the 17n+5 grid, and an unspecified canvas
with a keyframe is derived from that image's aspect on a 768-pixel short edge
(`MiniMaxH3ResolveShape`, `minimax_h3_planner.cpp`).

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
