# Working on the dllm Backend

`mudler/dllm.cpp` is a standalone C++/ggml engine for DiffusionGemma
block-diffusion models. LocalAI wraps it with a **pure-Go** backend at
`backend/go/dllm/` that dlopens `libdllm.so` via purego (ebitengine/purego) -
NOT cgo, and NOT a C++ grpc-server fork. The Go side owns chat templating
(gemma4 renderer) and output parsing (gemma4 streaming parser) and implements
the rich gRPC interface (`PredictRich`/`PredictStreamRich`, ChatDelta replies).

> NOTE: github.com/mudler/dllm.cpp is still **private** (publishing is
> planned). Until then the Makefile's anonymous clone fails; use the local-dev
> symlink shortcut documented at the top of `backend/go/dllm/Makefile`
> (symlink an out-of-tree `build/libdllm.so` into the backend dir and skip the
> clone), or a git credential helper with repo access.

## Pin

`backend/go/dllm/Makefile` pins `DLLM_VERSION?=<sha>` at the top
(whisper / parakeet-cpp / ds4 convention). The bump-deps bot
(`.github/workflows/bump_deps.yaml`) tracks `mudler/dllm.cpp` `main` and
rewrites that variable. After a manual bump: `make -C backend/go/dllm purge &&
make -C backend/go/dllm` (the clone is keyed on the directory existing, not
the sha).

## C-ABI and the serialization contract

The binding covers the 9-symbol flat C-ABI from dllm.cpp's
`include/dllm_capi.h` (ABI v1; `main.go` hard-fails on a version mismatch):
`abi_version, load, free, last_error, free_string, tokenize_json, generate,
generate_stream, cancel`. Contract points the Go wiring encodes (`capi.go`
header comment has the full list):

- **One ctx = one concurrent generate/tokenize.** A per-model worker
  goroutine (`Dllm.jobs` in `dllm.go`) owns ALL C calls, making the
  serialization structural instead of lock discipline.
- **`dllm_capi_cancel` is the ONE exception**: it only flips an atomic and may
  be called from any goroutine mid-generate, so `Dllm.Cancel` bypasses the
  worker queue. The flag resets at the start of each generate, so a watchdog
  racing a new generate must re-issue cancel.
- **`last_error` is a borrowed pointer** and must only be read AFTER the
  failing call returned (never while a generate is in flight on the same ctx).
- **Free vs in-flight requests**: requests hold `genMu.RLock` for their full
  duration; `Free` takes the write lock, so it only runs when nothing is in
  flight, then drains and closes the worker. Post-Free requests get a clean
  "model not loaded" error.
- `tokenize_json`/`generate` return malloc'd `char*` (bound as `uintptr`,
  copied, then `dllm_capi_free_string`d); opts/params JSON must be a FLAT
  object of scalars (`buildOptsJSON` rejects anything else).

## Wire shape

| RPC | Implementation |
|---|---|
| LoadModel | `dllm_capi_load` (params: `n_gpu_layers`, `n_threads`, `ctx_len`); `Options[]` parsed into per-request gen opts (`eb_*`, `blocks`, `kv_cache`) by `parseModelGenOpts` |
| PredictRich | render (if templated) → `dllm_capi_generate` → parse → ONE Reply with aggregated ChatDeltas + legacy `Message` bytes |
| PredictStreamRich | `dllm_capi_generate_stream`; per committed diffusion block → UTF-8 holdback → parser.Feed → one Reply per non-empty delta batch (channel closed by the CALLER, per `pkg/grpc/interface.go`) |
| Predict / PredictStream | Legacy paths, delegate to the rich pair (legacy stream INVERTS channel ownership: the impl closes) |
| TokenizeString | `dllm_capi_tokenize_json` (C side prepends BOS per `vocab.add_bos`) |
| Cancel | `dllm_capi_cancel`; currently INERT in practice - the gRPC server does not hand the request/stream context to backends, so client disconnects never reach it (plumbing is future work) |

`n_threads` and `ctx_len` are accepted-but-ignored by the engine at the
current pin (the context bound comes from GGUF `n_ctx_train`); they are sent
for forward compatibility.

## Renderer / parser (the templated chat path)

With `use_tokenizer_template` + raw Messages, the backend owns templating and
parsing (the ds4 precedent, but in Go):

- `gemma4_renderer.go` - `RenderGemma4(msgs, toolsJSON, enableThinking,
  addGenerationPrompt)`. The file embeds the FULL `tokenizer.chat_template`
  jinja (17466 bytes, md5 `8c34cf93c7a7815b3fdb300a009c4c17`) extracted
  verbatim from `diffusiongemma-26B-A4B-it-BF16.gguf` via gguf-py - e.g.
  `python scripts/dump_gguf.py model.gguf | grep -A400 chat_template` in the
  dllm.cpp checkout - as a numbered comment block; every Go rule cites its
  "tpl L<n>" line. Re-verify the md5 before blaming the renderer for a
  mismatch with a new GGUF. **BOS exception**: the template emits
  `{{- bos_token -}}` but the renderer deliberately does NOT - dllm.cpp's
  `run_generate` tokenizes with `prepend_bos = vocab.add_bos` (true for
  gemma4), so a literal `<bos>` would double it.
- `gemma4_parser.go` - streaming state machine turning raw model text
  (fragments can split anywhere, including mid-marker) into ChatDeltas:
  thought channels → `reasoning_content`, `<|tool_call>call:name{...}` →
  ToolCallDelta, `<turn|>` → done. Marker grammar cross-checked against vLLM
  PR #45163's gemma4 tool/reasoning parsers. Malformed payloads are re-emitted
  raw as content, never dropped.
- Thinking is **opt-in** for this family (`Metadata["enable_thinking"]`,
  default OFF - the inverse of ds4): the template gates every thinking branch
  on `enable_thinking`, and the no-thinking render pre-closes an empty thought
  channel, so the parser always starts in content state.
- **UTF-8 boundary holdback** (`splitValidUTF8` in `dllm.go`): per-block
  detokenization can split a multi-byte character across block boundaries, and
  grpc-go refuses to marshal invalid UTF-8 in proto3 strings. An incomplete
  trailing sequence (at most 3 bytes) is carried into the next block; genuinely
  undecodable bytes become U+FFFD.

Without `use_tokenizer_template`, the prompt passes through verbatim and the
output is NOT gemma4-parsed (plain content, like any non-autoparsing backend).

## Tests

| Layer | Gate | What |
|---|---|---|
| `backend/go/dllm/*_test.go` (renderer/parser/wiring) | none - run in plain `go test ./backend/go/dllm/...` | Ginkgo specs over a fake `generator` seam; canonical renderer fixtures from transformers' `test_modeling_diffusion_gemma.py`, parser tables from the vLLM gemma4 parsers |
| `backend/go/dllm/dllm_test.go` C-ABI smoke | `DLLM_TEST_LIBRARY` + `DLLM_TEST_TINY_MODEL` (dllm.cpp's `tests/fixtures/tiny_with_vocab.gguf`); Skips when unset | Drives the real `libdllm.so`: ABI check, load, tokenize `[2,18]`, deterministic generate, cancel |
| `tests/e2e-backends/dllm_test.go` | `BACKEND_TEST_DLLM=1` + `BACKEND_BINARY` (packaged run.sh) + `BACKEND_TEST_MODEL_FILE` (tiny fixture) | Templated chat round trip (Messages + UseTokenizerTemplate) over the real gRPC binary, non-streaming + streaming |
| Real-model e2e | `BACKEND_TEST_DLLM_REAL_MODEL_FILE` (26B BF16, ~50 GB) + `BACKEND_TEST_DLLM_REAL_GPU_LAYERS` | CUDA-13-class hardware only |

Tool-call e2e is deliberately absent from the tiny-model spec: the fixture has
random weights and cannot be coaxed into emitting tool markup; the unit tables
carry that coverage.

## Build matrix

`cpu-dllm` (amd64 + arm64), `cuda13-dllm` (amd64 + arm64), and
`cuda13-nvidia-l4t-arm64-dllm` (Jetson / DGX Spark GB10), via
`.github/backend-matrix.yml`. No darwin/Metal. CUDA builds forward
`-DDLLM_CUDA=ON` (dllm.cpp gates ggml's CUDA behind its own flag - a bare
`-DGGML_CUDA=ON` is overridden by the cache FORCE). `libdllm.so` is
self-contained (ggml statically absorbed, PIC), so packaging only ships the
one .so plus the usual ldd walk.

## Known limitations

- **Cancel is unwired**: nothing calls `Dllm.Cancel` on client disconnect
  until the gRPC server plumbs the request context through to backends.
- **Throughput**: ~0.15 tok/s on the 26B at default settings (GB10) - every
  denoise step recomputes the full prompt+canvas. The upstream prefix-KV
  cache (dllm.cpp P3) is the fix; `kv_cache:on` errors until it lands
  (`auto`/`off` are accepted no-ops).
- **Repo privacy**: see the note at the top - CI clone of dllm.cpp needs the
  repo published (or credentials) before the backend images can build.
- Engine spec/validation references: dllm.cpp `docs/validation.md` and
  LocalAI `docs/superpowers/specs/2026-06-10-dllm-cpp-design.md`.
