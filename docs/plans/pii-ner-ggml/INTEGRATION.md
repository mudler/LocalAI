# Phase-1 integration guide — `openai-privacy-filter` in llama.cpp + LocalAI

DRAFT companion to `../pii-ner-ggml-backend.md`. Skeletons in this dir:
`conversion_openai_privacy_filter.py` (HF→GGUF) and `openai-privacy-filter.cpp` (graph). All
line/symbol references are against the vendored checkout at commit `22d66b56`
(`backend/cpp/llama-cpp/llama.cpp`). Any in-tree work must follow that tree's `AGENTS.md`.

## What's already there vs what we add

| Capability | Status @ 22d66b56 | Action |
|---|---|---|
| gpt-oss graph (MoE top-k, sinks, RoPE/YaRN, o200k vocab) | present (`src/models/openai-moe.cpp`, `conversion/gpt_oss.py`) | reuse |
| `cls_out` / `cls_out_b` tensors, `n_cls_out`, `*.classifier.output_labels` | present (reranker; `bert.cpp:38`, `llama-arch.cpp:288,394`) | reuse |
| **Symmetric banded non-causal mask** | present — `LLAMA_SWA_TYPE_SYMMETRIC` (`llama-hparams.h:342`) + no-cache `fill_mask` honors `causal_attn` + `is_masked_swa` (`llama-graph.cpp:428-462`) | reuse (set hparams) |
| `LLAMA_POOLING_TYPE_TOKEN_CLS` + per-token `cls_out` in `build_pooling` + per-token extract in `llama-context` | **absent** (only RANK uses `cls_out`) | **carry PR #19725** |
| `TokenClassify` in LocalAI llama-cpp grpc-server | absent (`Embedding`/`Rerank`/`Score` only) | **add** |

The big de-risk: the bidirectional ±128 band is *not* new ggml code — it's
`causal_attn=false` + `swa_type=SYMMETRIC` + `n_swa=256` on the existing no-cache path.

## A. New architecture registration (llama.cpp)

1. **`gguf-py/gguf/constants.py`**: add `MODEL_ARCH.OPENAI_PRIVACY_FILTER`,
   `MODEL_ARCH_NAMES[...] = "openai-privacy-filter"`, and a `MODEL_TENSORS[...]` list =
   gpt-oss's tensor set **minus `OUTPUT`** (no lm_head) **plus `CLS_OUT`**. Also `PoolingType.TOKEN_CLS = 5` (PR #19725).
2. **`gguf-py/gguf/tensor_mapping.py`**: map HF `"score"` → `MODEL_TENSOR.CLS_OUT` (so
   `score.weight`/`score.bias` → `cls.output.{weight,bias}`).
3. **`conversion/__init__.py`**: `"OpenAIPrivacyFilterForTokenClassification": "openai_privacy_filter"`.
4. **`conversion/openai_privacy_filter.py`**: the skeleton here (subclass `GptOssModel`).
5. **`src/llama-arch.h` / `.cpp`**: `LLM_ARCH_OPENAI_PRIVACY_FILTER`, name string, and a
   per-arch tensor-name table (clone gpt-oss's, drop `OUTPUT`, add `CLS_OUT` → `cls.output`).
6. **`src/llama-model.cpp`**: register `llama_model_openai_privacy_filter` (load_hparams,
   load_tensors, build_graph dispatch — see the `.cpp` skeleton); ensure `llama_model_rope_type`
   returns NEOX for it (same as gpt-oss).
7. **`src/models/models.h`** + **`src/CMakeLists.txt`**: declare the class + add the source.

## B. Carry PR #19725 (token-level classification substrate)

Apply as `backend/cpp/llama-cpp/patches/0001-token-cls.patch` (extend `prepare.sh` to `git apply`
patches after the source copy). The diff (verified from the PR) adds:
- `include/llama.h`: `LLAMA_POOLING_TYPE_TOKEN_CLS = 5`.
- `src/llama-graph.cpp` `build_pooling`: a `case LLAMA_POOLING_TYPE_TOKEN_CLS` that applies
  `cls_out` (+`cls_out_b`) to **every** token → `[n_cls_out, n_tokens]` (vs RANK pooling to one).
- `src/llama-context.cpp`: under TOKEN_CLS, size the embd buffer to `n_tokens*n_cls_out` and
  have `llama_get_embeddings_ith(i)` return `n_cls_out` logits for token `i`.
- `convert`/gguf-py label plumbing (`add_classifier_output_labels`, `n_cls_out`).
- `tools/server/server-context.cpp`: `token_level_pooling = NONE || TOKEN_CLS`.

We depend on this; if it changes under review, re-sync the patch.

## C. The one numeric trap — `n_swa` mapping (verify first)

`is_masked_swa(SYMMETRIC)` masks when `|p1−p0| > n_swa/2`. HF band is `|q−kv| ≤ 128`. So
**`n_swa = 256`** (the loader doubles `sliding_window`). This ×2 is the most likely bug.
Verify by dumping the HF attention mask for a >257-token input and asserting the GGUF run masks
the identical (i,j) pairs. (Other parity checks: YaRN `inv_freq`/`attention_scaling` with
`truncate=false`; expert gate/up split is `chunk(2)` not interleaved; fp32 softmax incl. sinks.)

## D. LocalAI llama-cpp backend — add `TokenClassify`

In `backend/cpp/llama-cpp/grpc-server.cpp`, mirror `Rerank`/`Embedding`:
1. Load with `pooling_type = LLAMA_POOLING_TYPE_TOKEN_CLS` and (forced by the arch) non-causal.
   Add a load flag analogous to `--reranking`.
2. `TokenClassify(text, threshold)`:
   - tokenize once (o200k, with offsets — keep the token→byte map);
   - run the windowed forward (§E) → per-token `n_cls_out` logits via `llama_get_embeddings_ith`;
   - `log_softmax` (fp32) per token → constrained **Viterbi** over BIOES (§F);
   - assemble spans → byte offsets → `TokenClassifyEntity{entity_group, start, end, score, text}`.
3. Capability metadata: add `MethodTokenClassify` + a `classification`/`ner` usecase in
   `core/config/backend_capabilities.go`; register on the `llama-cpp` backend. Follow
   `.agents/api-endpoints-and-auth.md`.

The Go `NERDetector` then calls `TokenClassify` over the existing gRPC client — same contract
as the Phase-0 Python backend.

## E. Windowed inference (long inputs, exact)

Attention is strictly local (±128), so per-token logits depend only on the ±128 neighborhood.
Process in windows of width `W` (e.g. 1024), keeping only interior labels:

```
HALF = 128                       // = sliding_window
stride = W - 2*HALF              // 768 for W=1024
for start in 0, stride, 2*stride, ...:
    win = tokens[start : start+W]            // one non-causal ubatch (n_ubatch >= W)
    logits = forward(win)                     // [n_cls_out, len(win)]
    lo = (start == 0)            ? 0   : HALF              // drop left halo
    hi = (start+W >= N)         ? len(win) : W - HALF      // drop right halo
    emit logits[lo:hi] as global positions [start+lo : start+hi]
```

Bit-exact vs a single banded forward (interior tokens see their full receptive field), bounds
memory/compute to O(N·W), keeps ubatches small, and is streamable. Strictly better than the
`opf` runtime's non-overlapping `n_ctx` windows (no seam loss). For ≤W inputs it's one forward.

## F. Viterbi + offsets (C++, next to the tokenizer)

Port `opf/_core/{decoding,sequence_labeling,spans}.py`: constrained linear-chain Viterbi over
BIOES with start/end scores + the 6 transition biases (all 0.0 in the shipped
`viterbi_calibration.json` → structural constraints only at default; expose as load options);
per-token-argmax fallback if all paths die. Map token spans → byte offsets from the o200k byte
stream (UTF-8 aware). Optional whitespace-trim + per-label de-overlap. Keep it here (not Go) so
the token→byte mapping stays with the tokenizer and the `TokenClassify` proto is unchanged.

## Verification ladder (per `../pii-ner-ggml-backend.md` §7)

1. Mask parity (§C) on a >257-token input.
2. Per-layer parity vs HF (post-RoPE q/k, sink-incl. fp32 probs, router top-4, expert outs,
   post-MoE ×4 rescale, final norm, `score` logits) — single-window inputs.
3. Span-F1 per language vs the `opf` CLI on an AI4Privacy held-out slice.
4. Windowing equivalence: windowed vs single banded forward on a long input → identical labels.
</content>
