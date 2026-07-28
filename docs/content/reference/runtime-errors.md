
+++
disableToc = false
title = "Runtime errors and troubleshooting"
weight = 26
+++

This page maps the runtime and backend error messages you actually see in the logs (or in an API response) to their likely cause and fix. It covers failures that happen while a model is loading or running, as opposed to API-envelope validation errors (bad request shape, unknown field, wrong content type), which are documented in {{% relref "reference/api-errors" %}}.

If you only have an HTTP `500` and no message, read [How to read the real error](#how-to-read-the-real-error) first: the useful text is almost always in the server log, not in the HTTP body.

## Symptom, cause, fix

The left column is the literal string as it appears in the LocalAI server log (or, for the cooldown case, in the HTTP response). Match on the string, then read across.

| Error string / symptom | Likely cause | Fix |
|------------------------|--------------|-----|
| `could not load model: ...` | The selected backend started but rejected the model (bad path, corrupt or truncated GGUF, wrong architecture, unsupported quantization). The `...` is the backend's own message. | Read the wrapped backend message. Re-download the model if it is truncated. Confirm the backend matches the model (for example a GGUF needs `llama-cpp`). Run with `DEBUG=true` to see the full backend output. |
| `could not load model (no success): ...` | The backend replied to the load request but reported failure without a fatal error. | Same as above. The trailing message is the backend's status text; check it for the concrete reason (out of memory, unsupported option, missing file). |
| `could not load model - all backends returned error: ...` | LocalAI tried every candidate backend for the model and each one failed. Usually the backend for this model type is not installed, or the model file is unusable. | Install the correct backend with `local-ai backends install <backend>` (or from the Backends page). Confirm the model config `backend:` field names an installed backend. Inspect the concatenated per-backend messages for the real cause. |
| `grpc service not ready` | The backend process was spawned but its gRPC server did not become healthy in time (slow start, crash on startup, or the process died while loading). | Check the log lines just above for the backend's stderr. A crash here often means out of memory, a missing shared library, or an incompatible CPU (see `SIGILL`). Increase available RAM/VRAM or pick a smaller quantization. |
| `failed to load model: ...` | Returned by the load endpoints and several feature paths (voice, realtime, audio transform) when the model config could not be resolved or the backend load failed. | Confirm the model name exists (`local-ai models list`) and its YAML is valid. The trailing text carries the specific reason. |
| HTTP `503` with a `Retry-After` header, after a load failed | Model-load failure cooldown. After a model fails to load, LocalAI refuses new load attempts for that model for a short window so a client that keeps polling a broken model does not respawn a crashing backend on every request. The window starts at `--model-load-failure-cooldown` (default `10s`) and doubles per consecutive failure up to 5m; it resets on the first success. | Fix the underlying load failure (see the rows above), then wait out the `Retry-After` seconds before retrying, or restart LocalAI to clear the cooldown. Set `--model-load-failure-cooldown 0` (or `LOCALAI_MODEL_LOAD_FAILURE_COOLDOWN=0`) to disable the cooldown entirely. See {{% relref "reference/cli-reference" %}}. |
| HTTP `503` with a `Retry-After` header, under load | Per-model concurrency limit reached. When a model config sets a `MaxConcurrent` limit, extra requests are rejected with `503` and a `Retry-After` (whole seconds, floor 1) instead of queueing. | Retry after the advised delay, raise the model's concurrency limit, or run more replicas. |
| `invalid pitch` (with CUDA) | The prompt exceeded the model's context size. | Reduce the prompt length, or raise the model's context size (`context_size:` in the model YAML). |
| `SIGILL` (illegal instruction) on startup | The prebuilt backend binary uses CPU instructions your CPU does not have (for example AVX512, AVX2, F16C, FMA). | Rebuild the backend for your CPU. In a container, set `REBUILD=true` and disable the unsupported instructions, for example `CMAKE_ARGS="-DGGML_F16C=OFF -DGGML_AVX512=OFF -DGGML_AVX2=OFF -DGGML_FMA=OFF" make build`. |
| CUDA / VRAM out of memory (backend log shows `out of memory`, `CUDA error: out of memory`, or the process is killed loading) | The model plus its KV cache does not fit in GPU memory. | Use a smaller quantization, reduce `context_size:`, offload fewer layers to the GPU (lower `gpu_layers:`), or free VRAM held by other processes. On multi-GPU hosts, confirm the model is not trying to load entirely onto one device. |
| Backend was terminated by the watchdog | The idle or busy watchdog stopped a backend that ran longer than its threshold. This is expected behavior when the watchdog is enabled, not a crash. | If the backend was killed prematurely, raise `--watchdog-idle-timeout` / `--watchdog-busy-timeout`, or disable the relevant watchdog (`LOCALAI_WATCHDOG_IDLE=false`, `LOCALAI_WATCHDOG_BUSY=false`). The next request reloads the model. |
| Context size exceeded / truncated output | The combined prompt and generation exceeded the model's context window. | Shorten the prompt or raise `context_size:` in the model YAML (bounded by what the model and your memory allow). |

{{% notice note %}}
The exact wording of a backend's own error (the `...` part above) is produced by the backend engine (llama.cpp, vLLM, whisper.cpp, and so on), not by LocalAI, and can change between backend versions. Match on the LocalAI-side prefix (`could not load model`, `grpc service not ready`) and treat the trailing text as the backend's diagnosis.
{{% /notice %}}

## How to read the real error

Most user-facing failures surface as an HTTP `500` whose body is a short, generic message. The real cause is in the LocalAI server log, where the backend gRPC error is logged in full:

1. **Look at the server log, not just the HTTP response.** A `500` from a chat or completion request usually wraps a backend gRPC error. The log line that matters is the one printed by LocalAI when the backend replied (or failed to start).
2. **Turn on debug output.** Run with `DEBUG=true` in the environment, or pass `--log-level=debug` (equivalently `--debug`) on the command line. This prints the backend's stdout/stderr, the load parameters, and per-token timing.

   ```bash
   DEBUG=true local-ai run
   # or
   local-ai run --log-level=debug
   ```
3. **Read the backend's own output.** Backend engines write their diagnostics to stderr, which LocalAI captures into its log at debug level. A load failure (`could not load model`, `grpc service not ready`) almost always has the concrete reason (out of memory, missing library, unsupported quantization, illegal instruction) in the backend lines immediately above the LocalAI error.

## Performance: everything is slow

Slow inference is usually a configuration or hardware issue rather than an error:

- **Do not store models on an HDD.** Prefer an SSD. If you are stuck with an HDD, disable `mmap` in the model config so the model loads fully into memory.
- **Do not overbook the CPU.** Ideally `--threads` matches the number of physical cores. For a 4-core CPU, allocate `<= 4` threads per model.
- **Run with `DEBUG=true`** to see token-inference stats and confirm where time is going.
- **Confirm you are getting output at all:** send a simple request with `"stream": true` and watch how fast tokens arrive.
- **Check GPU offload.** If you expect GPU acceleration, confirm the model actually loaded onto the GPU (the backend log reports offloaded layers). See {{% relref "features/GPU-acceleration" %}}.

## See also

- {{% relref "reference/api-errors" %}} for API-envelope error formats and status codes.
- {{% relref "getting-started/first-agent" %}} for the "if your agent will not run" checklist (agent-specific failures).
- {{% relref "reference/cli-reference" %}} for the full flag list, including the model-load failure cooldown and watchdog options.
