# Working on the ds4 Backend

`antirez/ds4` is a single-model inference engine for DeepSeek V4 Flash.
LocalAI wraps the engine's C API (`ds4/ds4.h`) with a fresh C++ gRPC server at
`backend/cpp/ds4/` - NOT a fork of llama-cpp's grpc-server.cpp.

## Pin

`backend/cpp/ds4/Makefile` pins `DS4_VERSION?=<sha>` at the top. The `ds4`
target in the Makefile clones `antirez/ds4` at that commit (mirroring the
llama-cpp / ik-llama-cpp / turboquant pattern). The bump-deps bot
(`.github/workflows/bump_deps.yaml`) finds this pin via grep and opens a
daily PR to update it. To bump manually: edit the `DS4_VERSION?=` line,
then `make purge && make` (or rely on CI's clean build).

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

## Engine options (LoadModel)

`LoadModel` maps `ModelOptions.Options[]` (`"key:value"`, from model-YAML
`options:`) onto `ds4_engine_options` through a **declarative table**
(`kEngineOptSpecs` + `apply_engine_option` in `grpc-server.cpp`). The struct is
plain C with no reflection, so the field set is enumerated once in the table;
adding a future engine knob is a one-line table row, not a new branch. Unknown
keys are ignored (back-compat). A bare flag (`ssd_streaming` with no value)
means `true`. Path-type values (`mtp_path`, `expert_profile_path`,
`directional_steering_file`) resolve **relative to the model directory**, so a
gallery entry can reference a companion file it downloaded by bare filename;
absolute values pass through. `ds4_role` / `ds4_layers` / `ds4_listen` /
`ds4_route_timeout` / `kv_cache_dir` keep their dedicated handling (validation
+ coordinator wiring) and are not in the table.

Wired keys: `mtp_path`, `mtp_draft`, `mtp_margin`, `prefill_chunk`,
`power_percent`, `warm_weights`, `quality`, `ssd_streaming`,
`ssd_streaming_cold`, `ssd_streaming_preload_experts`,
`ssd_streaming_cache_experts` (count or `NGB`, sets both experts+bytes via
`ds4_parse_streaming_cache_experts_arg`), `simulate_used_memory` (`NGB` via
`ds4_parse_gib_arg`), `expert_profile_path`, `directional_steering_file`,
`directional_steering_attn`, `directional_steering_ffn`.

## SSD streaming (running models larger than RAM)

ds4's **SSD streaming** keeps non-routed weights resident and streams routed MoE
experts from the GGUF on cache misses, turning "does it fit in RAM" into a speed
spectrum. **Metal (Darwin) only** - it is a no-op on CUDA/CPU. Enable with
`options: ["ssd_streaming"]`; size the routed-expert cache with
`ssd_streaming_cache_experts:NGB` (omit for ds4's automatic 80%-of-working-set
budget). Gallery entries built on this: `deepseek-v4-flash-q4-ssd` (153 GB Flash
on a 128 GB Mac) and `deepseek-v4-pro-q2-ssd` (433 GB Pro, experimental).

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

## Distributed mode

ds4 supports **layer-split** distributed inference (a model too big for one host,
split by transformer layer; the GGUF must be present on every machine, each loads
only its slice). Topology is **inverted** vs llama.cpp: the coordinator listens,
workers dial in.

- **`ds4-worker` binary**: built and packaged next to `grpc-server` (`package.sh`
  copies it into `package/`). Links the same engine objects plus `ds4_distributed.o`;
  **no gRPC/protobuf dependency** (speaks ds4's own TCP transport), so it builds
  even where `grpc-server` can't. Runs the worker serving loop (`ds4_dist_run`).
- **Coordinator wiring**: the ds4 `grpc-server` acts as coordinator when `LoadModel`
  `ModelOptions.Options` (from model-YAML `options:`) carry:
  - `ds4_role:coordinator` (enables distributed mode; absent → single-node, back-compat)
  - `ds4_layers:0:19` (coordinator's own slice, inclusive; `N:output` includes the head)
  - `ds4_listen:0.0.0.0:1234` (address workers dial into)
  - `ds4_route_timeout:60` (optional; seconds Predict/PredictStream wait for the route
    to form before returning gRPC `UNAVAILABLE`; default 60)
- **Worker CLI**: `local-ai worker ds4-distributed -- <ds4-worker args>` resolves the
  ds4 backend and execs the packaged `ds4-worker` (raw passthrough), e.g.
  `--role worker --model /models/ds4flash.gguf --layers 20:output --coordinator <host> 1234`.

Opt-in e2e in `tests/e2e-backends/backend_test.go`, gated by
`BACKEND_TEST_DS4_DISTRIBUTED=1` (plus `BACKEND_TEST_DS4_WORKER_BINARY`,
`BACKEND_TEST_DS4_WORKER_LAYERS`, `BACKEND_TEST_DS4_COORDINATOR_LAYERS`,
`BACKEND_TEST_DS4_LISTEN`). Design spec:
`docs/superpowers/specs/2026-05-30-ds4-distributed-inference-design.md`.

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
