# Working on the ds4 Backend

`antirez/ds4` is a single-model inference engine for DeepSeek V4 Flash.
LocalAI wraps the engine's C API (`ds4/ds4.h`) with a fresh C++ gRPC server at
`backend/cpp/ds4/` - NOT a fork of llama-cpp's grpc-server.cpp.

## Pin

`backend/cpp/ds4/prepare.sh` clones `antirez/ds4` at `DS4_VERSION`. Bump that
commit to follow upstream.

## Wire shape

| RPC | Implementation |
|---|---|
| Health, Free, Status | Trivial; no engine dependency for Health |
| LoadModel | `ds4_engine_open` + `ds4_session_create`; backend is compile-time (DS4_NO_GPU → CPU, __APPLE__ → Metal, otherwise CUDA) |
| TokenizeString | `ds4_tokenize_text` |
| Predict | `ds4_engine_generate_argmax` + `DsmlParser` → one ChatDelta with content / reasoning_content / tool_calls[] |
| PredictStream | Same, per-token ChatDelta writes |

## DSML

ds4 emits tool calls as literal text markers (`<｜DSML｜tool_calls>` etc.) -
NOT special tokens. `dsml_parser.{h,cpp}` is our streaming state machine that
classifies token bytes into CONTENT / REASONING / TOOL_START / TOOL_ARGS / TOOL_END
events. `dsml_renderer.{h,cpp}` does the prompt direction: turns
OpenAI tool_calls + role=tool messages back into DSML for the next turn.

## Thinking modes

`PredictOptions.Metadata["enable_thinking"]` gates thinking on/off (default ON).
`["reasoning_effort"] == "max" | "xhigh"` selects `DS4_THINK_MAX`; anything else
maps to `DS4_THINK_HIGH`. We pass the chosen mode to `ds4_chat_append_assistant_prefix`.

## Disk KV cache

`kv_cache.{h,cpp}` implements an SHA1-keyed file cache using ds4's public
`ds4_session_save_payload` / `ds4_session_load_payload` API. Enable per request
via `ModelOptions.Options[] = "kv_cache_dir:/some/path"`. Format is **our own** -
NOT bit-compatible with ds4-server's KVC files (interop is a follow-up plan).

## Build matrix

| Build | Where | Notes |
|---|---|---|
| `cpu-ds4` (amd64 + arm64) | Linux GHA | ds4 considers CPU debug-only; useful only for wiring tests |
| `cuda13-ds4` (amd64 + arm64) | Linux GHA + DGX Spark validation | Primary production path on Linux |
| `ds4-darwin` (arm64) | macOS GHA runners | Metal; uses `scripts/build/ds4-darwin.sh` like llama-cpp-darwin |

cuda12 is intentionally omitted. ROCm / Vulkan / SYCL are not applicable.

## Hardware-gated validation

`tests/e2e-backends/backend_test.go` in `BACKEND_BINARY` mode:

```
BACKEND_BINARY=$(pwd)/backend/cpp/ds4/package/run.sh \
BACKEND_TEST_MODEL_FILE=/path/to/ds4flash.gguf \
BACKEND_TEST_CAPS=health,load,predict,stream,tools \
BACKEND_TEST_TOOL_PROMPT="What's the weather in Paris?" \
go test -count=1 -timeout=30m -v ./tests/e2e-backends/...
```

CI does not load the model; the suite is opt-in via env vars.

## Importer

`core/gallery/importers/ds4.go` (`DS4Importer`) auto-detects ds4 weights by
matching the `antirez/deepseek-v4-gguf` repo URI or the
`DeepSeek-V4-Flash-*.gguf` filename pattern. **Registered BEFORE
`LlamaCPPImporter`** in `defaultImporters` - both match `.gguf` but ds4 is more
specific, and first-match-wins. The importer emits `backend: ds4`, uses
`ds4flash.gguf` as the local filename (matches ds4's own CLI default), and
disables the Go-side automatic tool-parsing fallback (the C++ backend emits
ChatDelta.tool_calls natively via `DsmlParser`).

ds4 is also listed in `core/http/endpoints/localai/backend.go`'s pref-only
slice so the `/import-model` UI surfaces it as a manual choice for users who
want to force the backend on a non-canonical URI.
