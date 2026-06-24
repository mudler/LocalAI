# Working on the SGLang Backend

The SGLang backend lives at `backend/python/sglang/backend.py` (async gRPC). It wraps SGLang's `Engine` (`sglang.srt.entrypoints.engine.Engine`) and translates LocalAI's gRPC `PredictOptions` into SGLang sampling params + outputs into `Reply.chat_deltas`. Structurally it mirrors `backend/python/vllm/backend.py` — keep them shaped the same so changes in one have an obvious analog in the other.

## `engine_args` is the universal escape hatch

A small fixed set of fields on `ModelOptions` is mapped to typed SGLang kwargs in `LoadModel` (model, quantization, load_format, gpu_memory_utilization → mem_fraction_static, trust_remote_code, enforce_eager → disable_cuda_graph, tensor_parallel_size → tp_size, max_model_len → context_length, dtype). **Everything else** flows through the `engine_args:` YAML map.

Validation happens in `_apply_engine_args`. Keys are checked against `dataclasses.fields(ServerArgs)` (`sglang.srt.server_args.ServerArgs` is a flat `@dataclass` with ~380 fields). Unknown keys raise `ValueError` at LoadModel time with a `difflib.get_close_matches` suggestion — same shape as the vLLM backend.

**Precedence:** typed `ModelOptions` fields populate `engine_kwargs` first, then `engine_args` overrides them. So a YAML that sets both `gpu_memory_utilization: 0.9` and `engine_args.mem_fraction_static: 0.5` ends up at `0.5`. Document this when answering "why didn't my YAML field stick?".

**ServerArgs is flat.** Unlike vLLM, where speculative decoding is nested under `engine_args.speculative_config: {...}`, SGLang exposes flat top-level fields: `speculative_algorithm`, `speculative_draft_model_path`, `speculative_num_steps`, `speculative_eagle_topk`, `speculative_num_draft_tokens`, `speculative_dflash_block_size`, etc. There is no `speculative_config:` dict. Same goes for compilation, kv-transfer, attention — all flat.

The canonical reference is `python/sglang/srt/server_args.py:ServerArgs` (line ~304). When SGLang adds new flags, no LocalAI code change is needed — they're automatically available via `engine_args:`. The validator picks them up because it introspects the live dataclass.

## Speculative decoding cheatsheet

`--speculative-algorithm` accepts `EAGLE`, `EAGLE3`, `NEXTN`, `STANDALONE`, `NGRAM`, `DFLASH`. `NEXTN` is silently rewritten to `EAGLE` in `ServerArgs.__post_init__` (`server_args.py:3286-3287`). MTP (Multi-Token Prediction) is the same EAGLE path with `num_steps=1, eagle_topk=1, num_draft_tokens=2` against a target whose architecture has multi-token heads (e.g. MiMo-7B-RL, DeepSeek-V3-MTP).

| Algorithm | Drafter requirement | Gallery demo target | Gallery demo drafter |
|-----------|--------------------|---------------------|----------------------|
| `NEXTN` / `EAGLE` (MTP) | Assistant drafter or built-in heads | google/gemma-4-E2B-it, google/gemma-4-E4B-it | google/gemma-4-E2B-it-assistant, google/gemma-4-E4B-it-assistant |
| `EAGLE3` | EAGLE3 draft head | (no gallery entry yet) | e.g. jamesliu1/sglang-EAGLE3-Llama-3.1-Instruct-8B |
| `DFLASH` | Block-diffusion drafter | (no gallery entry yet) | e.g. z-lab/Qwen3-4B-DFlash-b16 |
| `STANDALONE` | Smaller LLM as drafter | (no gallery entry yet) | any smaller chat-tuned LLM in the same family |
| `NGRAM` | None — uses prefix history | (no gallery entry yet) | n/a |

The Gemma 4 demos use `mem_fraction_static: 0.85` (cookbook default) and the cookbook's `num_steps=5, num_draft_tokens=6, eagle_topk=1` parameters. Other algorithms are reachable from any user YAML via `engine_args:` but don't have shipped demos yet — that's a deliberate gallery scope choice, not a backend limitation.

Gemma 4 support requires sglang built from a commit that includes [PR #21952](https://github.com/sgl-project/sglang/pull/21952). LocalAI's pinned release for cublas12 / cublas13 includes it. The `l4t13` (JetPack 7 / sbsa cu130) build floors at `sglang>=0.5.0` because the `pypi.jetson-ai-lab.io` mirror still ships only `0.5.1.post2` as of 2026-05-06 — Gemma 4 / MTP recipes are therefore not available on l4t13 until that mirror catches up. `backend.py` keeps backward compat with the 0.5.x → 0.5.11 `SamplingParams.seed` → `sampling_seed` rename via runtime detection.

Compatibility caveats per the SGLang docs: DFLASH and NGRAM are incompatible with `enable_dp_attention`; DFLASH requires `pp_size == 1`; STANDALONE is incompatible with `enable_dp_attention`; NGRAM is CUDA-only and disables the overlap scheduler.

### `mem_fraction_static` + quantization + MTP on consumer GPUs

When combining online weight quantization (`engine_args.quantization: fp8` / `awq` / etc.) with built-in-head MTP (`speculative_algorithm: EAGLE`/`NEXTN`) on a tight VRAM budget, sglang's default `mem_fraction_static: 0.85` will OOM during draft-worker init. The reason: sglang quantizes the **target** model's transformer blocks but loads the **MTP draft worker's vocab embedding** at the source dtype (typically bf16). For a 7 B-class model with a 150k-token vocab × 4096 hidden, that's another ~1.2 GiB allocated *after* the static pool is reserved. At 0.85 fraction on a 16 GB card there's no room left.

Workaround: drop `mem_fraction_static` to ~0.7 so the post-static heap can absorb the MTP embedding alloc + CUDA graph private pools. Verified end-to-end on MiMo-7B-RL + fp8 + MTP on a 16 GB RTX 5070 Ti (`gallery/sglang-mimo-7b-mtp.yaml`) at ~88 tok/s. Models with larger vocabs or more MTP layers (e.g. DeepSeek-V3-MTP) need an even smaller fraction.

This isn't documented anywhere upstream as of 2026-05-06 — the SGLang Gemma 4 cookbook uses 0.85 because their MTP path doesn't go through `eagle_worker_v2.py` for an embedding-bearing draft module. Don't blanket-apply 0.7 across all sglang YAMLs; only when MTP-with-built-in-heads + quantization combine.

## Tool-call and reasoning parsers stay on `Options[]`

ServerArgs has `tool_call_parser` and `reasoning_parser` fields, and the backend does pass them through to `Engine` so SGLang's own HTTP/OAI surface keeps working. But for the **LocalAI** request path the backend constructs fresh per-request parser instances in `_make_parsers` (`backend.py:286`) because the parsers are stateful — the streaming and non-streaming paths each need their own.

So the user-facing knob stays on `Options[]`:

```yaml
options:
  - tool_parser:hermes
  - reasoning_parser:deepseek_r1
```

Putting these in `engine_args:` will set them on `ServerArgs` but the LocalAI-level streaming `ChatDelta` will not pick them up. Don't recommend that path.

## What's missing today (out of scope, but worth tracking)

- `core/config/hooks_sglang.go` — there is no SGLang equivalent of `hooks_vllm.go`. The vLLM hook auto-selects parsers for known model families from `parser_defaults.json` and seeds production engine_args defaults. A symmetric hook for SGLang could reuse the same `parser_defaults.json` (the SGLang parser names are different but the family detection is shared) and seed defaults like `enable_metrics: true` or attention-backend choices.
- `core/gallery/importers/sglang.go` — vLLM has an importer that resolves model architecture → parser defaults at gallery-import time. A matching importer for SGLang would let `local-ai install` populate sensible parsers automatically.

These should be a follow-up PR, not a blocker for the engine_args feature.
