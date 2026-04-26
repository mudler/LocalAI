# Working on the vLLM Backend

The vLLM backend lives at `backend/python/vllm/backend.py` (async gRPC) and the multimodal variant at `backend/python/vllm-omni/backend.py` (sync gRPC). Both wrap vLLM's `AsyncLLMEngine` / `Omni` and translate the LocalAI gRPC `PredictOptions` into vLLM `SamplingParams` + outputs into `Reply.chat_deltas`.

This file captures the non-obvious bits — most of the bring-up was a single PR (`feat/vllm-parity`) and the things below are easy to get wrong.

## Tool calling and reasoning use vLLM's *native* parsers

Do not write regex-based tool-call extractors for vLLM. vLLM ships:

- `vllm.tool_parsers.ToolParserManager` — 50+ registered parsers (`hermes`, `llama3_json`, `llama4_pythonic`, `mistral`, `qwen3_xml`, `deepseek_v3`, `granite4`, `openai`, `kimi_k2`, `glm45`, …)
- `vllm.reasoning.ReasoningParserManager` — 25+ registered parsers (`deepseek_r1`, `qwen3`, `mistral`, `gemma4`, …)

Both can be used standalone: instantiate with a tokenizer, call `extract_tool_calls(text, request=None)` / `extract_reasoning(text, request=None)`. The backend stores the parser *classes* on `self.tool_parser_cls` / `self.reasoning_parser_cls` at LoadModel time and instantiates them per request.

**Selection:** vLLM does *not* auto-detect parsers from model name — neither does the LocalAI backend. The user (or `core/config/hooks_vllm.go`) must pick one and pass it via `Options[]`:

```yaml
options:
  - tool_parser:hermes
  - reasoning_parser:qwen3
```

Auto-defaults for known model families live in `core/config/parser_defaults.json` and are applied:
- at gallery import time by `core/gallery/importers/vllm.go`
- at model load time by the `vllm` / `vllm-omni` backend hook in `core/config/hooks_vllm.go`

User-supplied `tool_parser:`/`reasoning_parser:` in the config wins over defaults — the hook checks for existing entries before appending.

**When to update `parser_defaults.json`:** any time vLLM ships a new tool or reasoning parser, or you onboard a new model family that LocalAI users will pull from HuggingFace. The file is keyed by *family pattern* matched against `normalizeModelID(cfg.Model)` (lowercase, org-prefix stripped, `_`→`-`). Patterns are checked **longest-first** — keep `qwen3.5` before `qwen3`, `llama-3.3` before `llama-3`, etc., or the wrong family wins. Add a covering test in `core/config/hooks_test.go`.

**Sister file — `core/config/inference_defaults.json`:** same pattern but for sampling parameters (temperature, top_p, top_k, min_p, repeat_penalty, presence_penalty). Loaded by `core/config/inference_defaults.go` and applied by `ApplyInferenceDefaults()`. The schema is `map[string]float64` only — *strings don't fit*, which is why parser defaults needed their own JSON file. The inference file is **auto-generated from unsloth** via `go generate ./core/config/` (see `core/config/gen_inference_defaults/`) — don't hand-edit it; instead update the upstream source or regenerate. Both files share `normalizeModelID()` and the longest-first pattern ordering.

**Constructor compatibility gotcha:** the abstract `ToolParser.__init__` accepts `tools=`, but several concrete parsers (Hermes2ProToolParser, etc.) override `__init__` and *only* accept `tokenizer`. Always:

```python
try:
    tp = self.tool_parser_cls(self.tokenizer, tools=tools)
except TypeError:
    tp = self.tool_parser_cls(self.tokenizer)
```

## ChatDelta is the streaming contract

The Go side (`core/backend/llm.go`, `pkg/functions/chat_deltas.go`) consumes `Reply.chat_deltas` to assemble the OpenAI response. For tool calls to surface in `chat/completions`, the Python backend **must** populate `Reply.chat_deltas[].tool_calls` with `ToolCallDelta{index, id, name, arguments}`. Returning the raw `<tool_call>...</tool_call>` text in `Reply.message` is *not* enough — the Go regex fallback exists for llama.cpp, not for vllm.

Same story for `reasoning_content` — emit it on `ChatDelta.reasoning_content`, not as part of `content`.

## Message conversion to chat templates

`tokenizer.apply_chat_template()` expects a list of dicts, not proto Messages. The shared helper in `backend/python/common/vllm_utils.py` (`messages_to_dicts`) handles the mapping including:

- `tool_call_id` and `name` for `role="tool"` messages
- `tool_calls` JSON-string field → parsed Python list for `role="assistant"`
- `reasoning_content` for thinking models

Pass `tools=json.loads(request.Tools)` and (when `request.Metadata.get("enable_thinking") == "true"`) `enable_thinking=True` to `apply_chat_template`. Wrap in `try/except TypeError` because not every tokenizer template accepts those kwargs.

## CPU support and the SIMD/library minefield

vLLM publishes prebuilt CPU wheels at `https://github.com/vllm-project/vllm/releases/...`. The pin lives in `backend/python/vllm/requirements-cpu-after.txt`.

**Version compatibility — important:** newer vllm CPU wheels (≥ 0.15) declare `torch==2.10.0+cpu` as a hard dep, but `torch==2.10.0` only exists on the PyTorch test channel and pulls in an incompatible `torchvision`. Stay on **`vllm 0.14.1+cpu` + `torch 2.9.1+cpu`** until both upstream catch up. Bumping requires verifying torchvision/torchaudio match.

`requirements-cpu.txt` uses `--extra-index-url https://download.pytorch.org/whl/cpu`. `install.sh` adds `--index-strategy=unsafe-best-match` for the `cpu` profile so uv resolves transformers/vllm from PyPI while pulling torch from the PyTorch index.

**SIMD baseline:** the prebuilt CPU wheel is compiled with AVX-512 VNNI/BF16. On a CPU without those instructions, importing `vllm.model_executor.models.registry` SIGILLs at `_run_in_subprocess` time during model inspection. There is no runtime flag to disable it. Workarounds:

1. **Run on a host with the right SIMD baseline** (default — fast)
2. **Build from source** with `FROM_SOURCE=true` env var. Plumbing exists end-to-end:
   - `install.sh` hides `requirements-cpu-after.txt`, runs `installRequirements` for the base deps, then clones vllm and `VLLM_TARGET_DEVICE=cpu uv pip install --no-deps .`
   - `backend/Dockerfile.python` declares `ARG FROM_SOURCE` + `ENV FROM_SOURCE`
   - `Makefile` `docker-build-backend` macro forwards `--build-arg FROM_SOURCE=$(FROM_SOURCE)` when set
   - Source build takes 30–50 minutes — too slow for per-PR CI but fine for local.

**Runtime shared libraries:** vLLM's `vllm._C` extension `dlopen`s `libnuma.so.1` at import time. If missing, the C extension silently fails and `torch.ops._C_utils.init_cpu_threads_env` is never registered → `EngineCore` crashes on `init_device` with:

```
AttributeError: '_OpNamespace' '_C_utils' object has no attribute 'init_cpu_threads_env'
```

`backend/python/vllm/package.sh` bundles `libnuma.so.1` and `libgomp.so.1` into `${BACKEND}/lib/`, which `libbackend.sh` adds to `LD_LIBRARY_PATH` at run time. The builder stage in `backend/Dockerfile.python` installs `libnuma1`/`libgomp1` so package.sh has something to copy. Do *not* assume the production host has these — backend images are `FROM scratch`.

## Backend hook system (`core/config/backend_hooks.go`)

Per-backend defaults that used to be hardcoded in `ModelConfig.Prepare()` now live in `core/config/hooks_*.go` files and self-register via `init()`:

- `hooks_llamacpp.go` → GGUF metadata parsing, context size, GPU layers, jinja template
- `hooks_vllm.go` → tool/reasoning parser auto-selection from `parser_defaults.json`

Hook keys:
- `"llama-cpp"`, `"vllm"`, `"vllm-omni"`, … — backend-specific
- `""` — runs only when `cfg.Backend` is empty (auto-detect case)
- `"*"` — global catch-all, runs for every backend before specific hooks

Multiple hooks per key are supported and run in registration order. Adding a new backend default:

```go
// core/config/hooks_<backend>.go
func init() {
    RegisterBackendHook("<backend>", myDefaults)
}
func myDefaults(cfg *ModelConfig, modelPath string) {
    // only fill in fields the user didn't set
}
```

## The `Messages.ToProto()` fields you need to set

`core/schema/message.go:ToProto()` must serialize:
- `ToolCallID` → `proto.Message.ToolCallId` (for `role="tool"` messages — links result back to the call)
- `Reasoning` → `proto.Message.ReasoningContent`
- `ToolCalls` → `proto.Message.ToolCalls` (JSON-encoded string)

These were originally not serialized and tool-calling conversations broke silently — the C++ llama.cpp backend reads them but always got empty strings. Any new field added to `schema.Message` *and* `proto.Message` needs a matching line in `ToProto()`.
