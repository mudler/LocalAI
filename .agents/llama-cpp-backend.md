# llama.cpp Backend

The llama.cpp backend (`backend/cpp/llama-cpp/grpc-server.cpp`) is a gRPC adaptation of the upstream HTTP server (`llama.cpp/tools/server/server.cpp`). It uses the same underlying server infrastructure from `llama.cpp/tools/server/server-context.cpp`.

## Building and Testing

- Test llama.cpp backend compilation: `make backends/llama-cpp`
- The backend is built as part of the main build process
- Check `backend/cpp/llama-cpp/Makefile` for build configuration

## CPU variants

Every llama-cpp image ships `llama-cpp-cpu-all`: ggml built with `GGML_BACKEND_DL`, and `ggml` picks the best `libggml-cpu-*.so` for the host at run time. The variants are built once per architecture by the `cpu-variants` stage of `backend/Dockerfile.llama-cpp` (`.docker/llama-cpp-cpu-variants.sh`, plain gcc, Ubuntu 22.04) and copied into every flavor. The GPU stage builds with `-DGGML_CPU=OFF` and compiles only its own backend and the loader, so icpx and hipcc never compile `ggml-cpu`. Rules:

- The variants must come from the same llama.cpp commit as the GPU tree. The Makefile checks this through the `LLAMA_VERSION` file the stage writes.
- Keep the stage on the oldest glibc of any builder image. The script refuses a variant that needs a newer glibc or libstdc++ than Ubuntu 22.04 has.
- turboquant, bonsai and olala build a llama.cpp fork and use the same stage with their own pin (`CPU_VARIANTS_VERSION_VAR`, and `OLALA_CMAKE_ARGS` for olala's CPU flags) and their own patches. The variants of a fork come from that fork, never from upstream. ik-llama-cpp has its own ggml without the backend loader, so it keeps per-instruction-set binaries.
- `llama-cpp-grpc` and `llama-cpp-rpc-server` come from the same tree as `llama-cpp-cpu-all` in the image builds (`CPU_ALL_RPC=ON`, set by the `.docker/*-compile.sh` scripts of llama-cpp, bonsai, turboquant and olala). That tree also builds `libggml-rpc.so` and `ggml-rpc-server`, and both binaries find the RPC backend through the backend registry. Do not go back to a separate static grpc tree: it compiles the GPU backend a second time for every CUDA or HIP arch, which was half of each GPU build and took the ROCm images to GitHub's 6h job limit. The Darwin and Windows builds still call the static `llama-cpp-grpc` target.
- `backend.yml` and `backend_pr.yml` end with a `build-timings` job (`scripts/ci/backend-build-timings.sh`). It writes the slowest image jobs to the run summary and fails when a llama.cpp-family job ran longer than 4h.

## NCCL (CUDA images)

ggml's CUDA backend links NCCL when CMake finds it (`GGML_CUDA_NCCL` is on by default) and uses it for the cross-GPU reductions of `split_mode: tensor`. `.docker/install-nccl.sh` installs `libnccl2` and `libnccl-dev` in both builder stages of `backend/Dockerfile.llama-cpp`, for cublas on amd64 (CUDA 12 and 13) and on arm64 with CUDA 13. The Jetson (CUDA 12 arm64) image has none, since NVIDIA ships no NCCL for Jetson. Rules:

- It is not in `install-base-deps.sh` on purpose: the forks that share that script would link NCCL too and each grow by about 190 to 290 MB compressed.
- The version is the newest `libnccl2` built for the exact CUDA `major.minor` of the image (`+cudaX.Y`), so its driver requirement matches the bundled CUDA libraries. `NCCL_VERSION` overrides it.
- `package.sh` runs `scripts/build/check-nccl-bundle.sh`: it fails the build when an ELF needs `libnccl.so.2` that `lib/` lacks, or when the builder has NCCL but `libggml-cuda.so` does not link it. It also copies the NCCL license notice to `licenses/`.
- A missing `libnccl.so.2` at run time would make ggml skip `libggml-cuda.so` (GPU silently lost), which is why the library is bundled and checked rather than left to the host.

## llguidance (grammar_engine)

The image builds link llguidance (`LLGUIDANCE=ON` in `.docker/llama-cpp-compile.sh`, `-DLLAMA_LLGUIDANCE=ON`). llama.cpp builds it with cargo, so the compile step installs a pinned Rust toolchain (`RUST_TOOLCHAIN`) into BuildKit cache mounts; nothing Rust ships in the image. `scripts/build/llama-cpp-darwin.sh` turns it on when the runner has cargo. The forks keep GBNF.

- The Go side builds the GBNF grammar and, through `ModelConfig.SchemaGrammar`, its llguidance form (`pkg/functions/grammars/llguidance.go`). The form travels in `PredictOptions.Metadata["llguidance_grammar"]`, which a client cannot set.
- `grpc-server.cpp` picks the form in `parse_options` with `select_grammar` (`grammar_engine.h`): llguidance only when the build has it and `grammar_engine` is not `gbnf`.
- A grammar the user wrote, mixed mode, `schema_type: llama3.1` and the lazy grammars of the chat template stay GBNF. llama.cpp has no lazy triggers for llguidance.
- With the flag on, upstream `json_schema_to_grammar()` returns an llguidance grammar unless `force_gbnf` is set. Check new callers of it after a llama.cpp bump.

## Architecture

- **grpc-server.cpp**: gRPC server implementation, adapts HTTP server patterns to gRPC
- Uses shared server infrastructure: `server-context.cpp`, `server-task.cpp`, `server-queue.cpp`, `server-common.cpp`
- The gRPC server mirrors the HTTP server's functionality but uses gRPC instead of HTTP

## Patches to llama.cpp

`prepare.sh` applies `backend/cpp/llama-cpp/patches/*.patch` to the pinned checkout: the score task (0001) and the paged serving hooks (0002: admission, preemption with resume by recompute, prefill budget; options `paged_kv`, `prefill_budget`, `kv_output_reserve`, `paged_preempt`, `kv_pool_tokens`). Read [backend/cpp/llama-cpp/PATCHES.md](../backend/cpp/llama-cpp/PATCHES.md) before you bump llama.cpp or touch a patch: it lists each hook, the upstream invariants it relies on, the re-porting steps and the upstream changes to watch (`upstream-watch.sh`). The paged serving policy is in `serving_policy.h`, tested by `serving_policy_test.cpp`.

## Common Issues When Updating llama.cpp

When fixing compilation errors after upstream changes:
1. Check how `server.cpp` (HTTP server) handles the same change
2. Look for new public APIs or getter methods
3. Store copies of needed data instead of accessing private members
4. Update function calls to match new signatures
5. Test with `make backends/llama-cpp`

## Key Differences from HTTP Server

- gRPC uses `BackendServiceImpl` class with gRPC service methods
- HTTP server uses `server_routes` with HTTP handlers
- Both use the same `server_context` and task queue infrastructure
- gRPC methods: `LoadModel`, `Predict`, `PredictStream`, `Embedding`, `Rerank`, `TokenizeString`, `GetMetrics`, `Health`

## Tool Call Parsing Maintenance

When working on JSON/XML tool call parsing functionality, always check llama.cpp for reference implementation and updates:

### Checking for XML Parsing Changes

1. **Review XML Format Definitions**: Check `llama.cpp/common/chat-parser-xml-toolcall.h` for `xml_tool_call_format` struct changes
2. **Review Parsing Logic**: Check `llama.cpp/common/chat-parser-xml-toolcall.cpp` for parsing algorithm updates
3. **Review Format Presets**: Check `llama.cpp/common/chat-parser.cpp` for new XML format presets (search for `xml_tool_call_format form`)
4. **Review Model Lists**: Check `llama.cpp/common/chat.h` for `COMMON_CHAT_FORMAT_*` enum values that use XML parsing:
   - `COMMON_CHAT_FORMAT_GLM_4_5`
   - `COMMON_CHAT_FORMAT_MINIMAX_M2`
   - `COMMON_CHAT_FORMAT_KIMI_K2`
   - `COMMON_CHAT_FORMAT_QWEN3_CODER_XML`
   - `COMMON_CHAT_FORMAT_APRIEL_1_5`
   - `COMMON_CHAT_FORMAT_XIAOMI_MIMO`
   - Any new formats added

### Model Configuration Options

Always check `llama.cpp` for new model configuration options that should be supported in LocalAI:

1. **Check Server Context**: Review `llama.cpp/tools/server/server-context.cpp` for new parameters
2. **Check Chat Params**: Review `llama.cpp/common/chat.h` for `common_chat_params` struct changes
3. **Check Server Options**: Review `llama.cpp/tools/server/server.cpp` for command-line argument changes
4. **Examples of options to check**:
   - `ctx_shift` - Context shifting support
   - `parallel_tool_calls` - Parallel tool calling
   - `reasoning_format` - Reasoning format options
   - Any new flags or parameters

### Speculative Decoding Types

The `spec_type` option in `grpc-server.cpp` delegates to upstream's `common_speculative_types_from_names()`, so new speculative types added to the `common_speculative_type_from_name` map in `common/speculative.cpp` are picked up automatically with no code changes - only docs need an entry in `docs/content/models/model-configuration.md`. Current values: `none`, `draft-simple`, `draft-eagle3`, `draft-mtp`, `draft-dflash`, `draft-dspark`, `ngram-simple`, `ngram-map-k`, `ngram-map-k4v`, `ngram-mod`, `ngram-cache`.

When `draft_model` is set and no `spec_type` is configured, `params_parse` asks `common_speculative_types_from_gguf()` (guarded by `LOCALAI_HAS_SPEC_TYPES_FROM_GGUF`, probed in `prepare.sh` for forks that lack it) to read the draft GGUF's `general.architecture`, and falls back to `draft-simple`. The importer (`core/gallery/importers/llama-cpp.go`) writes `draft_model` plus an explicit `spec_type` for `dspark-`, `dflash-`, `eagle3-` and `mtp-` sidecars, using the same keywords as llama.cpp's `common/download.cpp`.

`draft-mtp` (Multi-Token Prediction, [ggml-org/llama.cpp#22673](https://github.com/ggml-org/llama.cpp/pull/22673)) does not need a separate draft GGUF: when `spec_type` includes `draft-mtp` and `draftmodel` is empty, the upstream server creates an MTP context off the target model itself. LocalAI's gRPC layer needs no changes for this — it works through the existing `params.speculative.types` plumbing and the derived `cparams.n_rs_seq = params.speculative.need_n_rs_seq()` in `common_context_params_to_llama`.

### Implementation Guidelines

1. **Feature Parity**: Always aim for feature parity with llama.cpp's implementation
2. **Test Coverage**: Add tests for new features matching llama.cpp's behavior
3. **Documentation**: Update relevant documentation when adding new formats or options
4. **Backward Compatibility**: Ensure changes don't break existing functionality

### Files to Monitor

- `llama.cpp/common/chat-parser-xml-toolcall.h` - Format definitions
- `llama.cpp/common/chat-parser-xml-toolcall.cpp` - Parsing logic
- `llama.cpp/common/chat-parser.cpp` - Format presets and model-specific handlers
- `llama.cpp/common/chat.h` - Format enums and parameter structures
- `llama.cpp/tools/server/server-context.cpp` - Server configuration options

## Speech-to-speech (AudioToAudioStream)

`grpc-server.cpp` implements `AudioToAudioStream` on upstream's realtime slot (`server_realtime_slot`, task type `SERVER_TASK_TYPE_REALTIME`, `server-realtime.cpp`). It is compiled only when `prepare.sh` finds the realtime task type (`LOCALAI_HAS_REALTIME`); forks without it answer `UNIMPLEMENTED`.

- `RealtimeStreamSession` (in `grpc-server.cpp`) feeds the slot with `SERVER_TASK_TYPE_CONTROL` tasks and writes the events. A reader thread handles client messages, the RPC thread consumes slot results.
- `realtime_session.h` holds everything that needs no model: the turn text derived from the chat template, the conversation state machine and the translation of slot results into events. The text of a response goes through the splitter that `grpc-server.cpp` builds from a `common_chat_session` (grammar, preserved tokens and incremental parser of the chat template). `realtime_session_test.cpp` tests it (picked up by `backend/cpp/run-unit-tests.sh`). `realtime_chat_check.cpp` checks it against the LFM3 template of the llama.cpp tree and runs under ctest (`-DLLAMA_GRPC_BUILD_TESTS=ON`).
- No proto change: tools travel in `AudioToAudioConfig.tools` (OpenAI format) and `tool_choice` in its `params`, tool calls leave as `response.function_call_arguments.done`, `response.done` carries `raw_text` for a Go-side parse when the C++ parser found no call, tool results arrive as a `conversation.item.create` control with a `function_call_output` item. The event names are listed in the comments of `backend.proto`.
- Upstream's slot has no voice selection and no VAD. Tool calls follow upstream's `server-realtime.cpp`: the session grammar goes into the sampling of the response (`server_control_response::sampling`), the tool turn is derived from the template. A requested voice is reported in `session.created` with `voice_applied: false`.
- Upstream renames controls and moves the slot often. After a llama.cpp bump, build the backend and run the `realtime_*` tests first.
