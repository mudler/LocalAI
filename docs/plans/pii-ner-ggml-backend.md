# Plan: ML-based PII filter (privacy-filter family) via a GGML token-classification backend

Status: **research / pre-decision**. This document captures the research findings and a
proposed direction. We decide the plan after reviewing it together.

Author note: AI-assisted research per `.agents/ai-coding-assistants.md` — attribution via
`Assisted-by:` trailer on any resulting commits; the human submitter owns/reviews the code.

---

## 1. Goal

Add a *semantic* (model-based) tier to LocalAI's PII filter middleware, driven by the
**openai/privacy-filter** family of token-classification models — in particular
**OpenMed/privacy-filter-multilingual** for its 16-language coverage. The model output (BIOES
token labels → entity spans) must map onto the existing PII redactor's NER seam.

Hard constraint from the request: **no Python at inference time**. If no suitable C++/GGML
runtime exists, we implement one in GGML, following the methodology used for vibevoice.cpp,
LocalVQE, and parakeet.cpp.

---

## 2. The model family (research findings)

### 2.1 openai/privacy-filter (the base)

Source: <https://github.com/openai/privacy-filter>, model card
<https://huggingface.co/openai/privacy-filter>, and the HF Transformers integration
(`OpenAIPrivacyFilterConfig` / `OpenAIPrivacyFilterModel` /
`OpenAIPrivacyFilterForTokenClassification`).

- **Task**: bidirectional **token classification** for PII (not generative). One forward
  pass labels every token; spans are then decoded.
- **Lineage**: starts from a **gpt-oss-style autoregressive** checkpoint, then *converted to
  bidirectional* and post-trained with supervised token-classification loss.
- **Architecture** (per the model card):
  - 8 transformer blocks, `d_model = 640`
  - Grouped-query attention: 14 query heads, 2 KV heads (group size 7)
  - Rotary position embeddings (RoPE)
  - **Sparse MoE FFN**: 128 experts total, **top-4 routing** per token
  - **Banded (local/sliding-window) attention**, band size 128 → effective window 257 tokens
  - Token-classification head over `d_model = 640`
  - **1.5B total params, ~50M active per token**, 128k context
- **Output classes**: 33 = 1 background (`O`) + 8 categories × 4 BIOES tags
  (B/I/E/S). Categories: account_number, private_address, private_email, private_person,
  private_phone, private_url, private_date, secret.
- **Decoding**: a **constrained Viterbi** over a linear-chain BIOES grammar (not per-token
  argmax). Exposes **6 transition-bias parameters** to tune precision/recall at runtime.
- **Tokenizer**: tiktoken **o200k_base** (gpt-oss family).
- **License**: **Apache-2.0** — commercial use OK.
- **Runtimes shipped**: PyTorch only (the `opf` CLI: redact/eval/train). Now also a
  HF Transformers model class.

### 2.2 OpenMed/privacy-filter-multilingual (the target)

Source: <https://huggingface.co/OpenMed/privacy-filter-multilingual>.

- Fine-tune of `openai/privacy-filter`; **same architecture** (gpt-oss-style sparse MoE,
  128 experts top-4, BIOES head).
- **1.4B total / ~50M active**.
- **54 PII categories** across 7 domains (identity, contact, address, dates, gov-IDs,
  financial, crypto, vehicle, digital, auth) → **217 output classes** (1 + 54×4).
- **16 languages**: ar, bn, zh, nl, en, fr, de, hi, it, ja, ko, pt, es, te, tr, vi.
  Strongest: de/es/fr/it/hi/te/en; weaker on CJK & low-resource morphology.
- Trained on AI4Privacy `pii-masking-{200k,400k}` + `open-pii-masking-500k`, language-balanced.
- Runtimes: PyTorch + **MLX** (bf16 ~2.6 GB / 8-bit ~1.4 GB). MLX is explicitly out of scope
  for us. There is also a `privacy-filter-nemotron` (NVIDIA Nemotron PII data) clinical-leaning
  variant.

### 2.3 Existing non-Python / non-MLX implementations — survey result

**None found.** Concretely:

- No GGUF, no llama.cpp support, no standalone C++/Rust/GGML port, no ONNX export published.
- `screenpipe/privacy-filter` (GitHub) sounded promising but is a **Python + vLLM + ONNX**
  HTTP service (PolyForm Noncommercial license), not a portable runtime.
- Community claim "llama.cpp can't run it / no GGUF exists" is *true as published* — but the
  reasoning ("GGUF is only for generative models") is a packaging convention, not a hard
  limit. The compute kernels this model needs already exist in ggml/llama.cpp (see §6).

**Conclusion**: to drop Python we must build the runtime ourselves in GGML. This is the same
situation as vibevoice/parakeet/LocalVQE.

---

## 3. How LocalAI's PII filter works today

Package: `core/services/routing/pii/` (+ adapters in `core/services/routing/piiadapter/`,
routes in `core/http/routes/pii.go`).

- **Tier 1 (live)**: deterministic **regex** redactor (`redactor.go`, `patterns.go`) —
  email, phone, SSN, credit-card (Luhn), IPv4, API-key prefixes. Actions per pattern:
  `block` (HTTP 400), `mask` (placeholder), `allow` (audit only).
- **Request path**: `pii.RequestMiddleware(...)` runs innermost (after RouteModel /
  admission), per-model opt-in via the model config `pii:` block. Adapters
  (`piiadapter.OpenAI()` / `.Anthropic()`) extract scannable text and re-apply redactions by
  index.
- **Response/streaming path**: `stream.go` buffers a tail sized to the longest pattern so a
  redaction is never split across SSE chunks; remaps `block`→`mask` on the wire.
- **Audit**: one `PIIEvent` per detected span (hash-prefixed, never the raw value) into a
  10k ring buffer; admin API at `/api/pii/*`.
- **Config**: model YAML `pii: { enabled, patterns: [...] }`; global `--pii-config`,
  `--disable-pii`; runtime overrides persisted to `runtime_settings.json`.

### 3.1 The NER seam that's already waiting (this is the key finding)

The redactor was built with a **Tier-2 encoder/NER hook already designed in but unwired**:

- `core/services/routing/pii/ner.go`:
  - `type NERDetector interface { Detect(ctx, text) ([]NEREntity, error) }`
  - `NEREntity{ Group string; Start, End int /*byte offsets*/; Score float32 }`
  - `NERConfig{ Detector; MinScore; EntityActions map[group]Action; DefaultAction }`
  - `ResolveAction(group)`; audit rows use synthetic pattern IDs `ner:<group>` so the
    existing disable/override/events machinery works unchanged.
- `redactor.go`: `RedactWithNER(ctx, text, overrides, nerCfg)` already exists; nil Detector =
  zero-cost fallback to regex-only.
- `types.go:8` explicitly notes the encoder tier is "out of scope for this slice — added
  later, fed by the gRPC TokenClassify RPC."

### 3.2 The gRPC `TokenClassify` RPC is also already plumbed

`backend/backend.proto`:
```
rpc TokenClassify(TokenClassifyRequest) returns (TokenClassifyResponse) {}
message TokenClassifyRequest  { string text = 1; float threshold = 2; }
message TokenClassifyEntity   { string entity_group = 1; int32 start = 2;  // byte offsets
                                int32 end = 3; float score = 4; string text = 5; }
message TokenClassifyResponse { repeated TokenClassifyEntity entities = 1; }
```
Client-side plumbing is complete: `pkg/grpc/{backend,client,embed}.go`,
`pkg/model/connection_evicting_client.go`, generated stubs.

**Server-side, the only implementer today is `backend/python/transformers/backend.py`**
(`Type=TokenClassification`), which uses the HF `pipeline("token-classification",
aggregation_strategy="simple")`. Note: "simple" aggregation = argmax + B/I merge — it does
**not** run the model's constrained-Viterbi BIOES decode, so it's a lossy approximation of
the intended decoding (see §6.4).

### 3.3 What is therefore *missing* to light up model-based PII

1. A **`core/backend` wrapper** for TokenClassify (none exists yet — `grep` confirms).
2. A **`NERDetector` implementation** in `core/application` that calls that wrapper for a
   configured model and converts char→byte offsets.
3. **Wiring** `RedactWithNER` into the request/stream middleware + the model `pii:` config
   (entity→action map, min score, which model to use).
4. **Capability metadata**: there is no `Usecase`/`Method` for token classification in
   `core/config/backend_capabilities.go` (it has tokenize/rerank/detection but not
   classification). Add `MethodTokenClassify` + a `classification`/`ner` usecase + register
   it on whichever backend(s) implement it. Follow the
   `.agents/api-endpoints-and-auth.md` checklist for the surface.
5. The **GGML backend** that implements `TokenClassify` server-side (the Python-removal goal).

Items 1–4 are needed regardless of Python-vs-GGML; item 5 is the substantive new work.

---

## 4. Mapping model output → middleware

```
text ──► [GGML privacy-filter backend]
            tokenize (o200k_base)
            forward (bidirectional MoE transformer) ──► per-token logits [T, 217]
            constrained Viterbi (BIOES + 6 transition biases) ──► label path
            spans + char offsets ──► byte offsets
         ──► TokenClassifyResponse{ entities:[{entity_group, start, end, score, text}] }
                       │
                       ▼
   core/backend.TokenClassify wrapper
                       │
                       ▼
   pii.NERDetector.Detect ──► []NEREntity{Group, Start, End, Score}
                       │
                       ▼
   Redactor.RedactWithNER(text, overrides, NERConfig{EntityActions, MinScore, DefaultAction})
                       │  merges NER hits with regex hits, resolves action per entity group
                       ▼
   mask / block / allow  +  PIIEvent{ PatternID: "ner:FIRSTNAME", ... }
```

Mapping notes / gotchas:
- **Entity-group vocabulary**: the model emits 54 category names (FIRSTNAME, IBAN, …). These
  become `NERConfig.EntityActions` keys and `ner:<GROUP>` audit IDs. We should ship a sane
  default action map (e.g. block secrets/credentials, mask names/contact, allow-log
  low-risk) and let admins override per model.
- **Offsets**: `NEREntity.Start/End` and `TokenClassifyEntity.start/end` are **byte** offsets;
  HF "simple" aggregation and tiktoken offsets are **character/codepoint**-based. For
  multilingual UTF-8 (the whole point of this model) we must convert char→byte carefully. The
  existing Python path returns Python `str` indices — a latent bug for non-ASCII that we
  should fix in the wrapper/backend regardless.
- **Threshold**: `TokenClassifyRequest.threshold` ↔ `NERConfig.MinScore`. The Viterbi
  transition biases are a *second*, orthogonal knob (precision/recall of span boundaries) —
  we should expose them as backend load options, not per-request, to start.
- **Streaming**: model-based detection needs the full text; on the response path it composes
  with the existing tail-buffer stream filter, but a 50M-active forward per chunk is costly.
  Decision needed: request-side only first, or buffer-and-classify on response too (§9).

---

## 5. Runtime strategy: llama.cpp vs a standalone ggml graph

The request is to drop Python at inference. Two ways to get a C++/ggml runtime:

- **(A) Extend llama.cpp** — the model is *literally a gpt-oss variant* (config confirms
  `model_type: openai_privacy_filter`, 8 layers, d640, 128 experts top-4, `sliding_window:
  128`, YaRN θ=150000 factor 32, vocab 200064 = o200k). llama.cpp already ships the entire
  gpt-oss graph (`LLM_ARCH_OPENAI_MOE`, `src/models/openai-moe.cpp`): MoE top-k routing,
  attention sinks, sliding-window (iswa) attention, RoPE/YaRN, and the o200k tokenizer. The
  only missing pieces are a token-classification head and per-token logit output — and an
  open upstream PR already adds exactly that substrate (see §6.2).
- **(B) Standalone `privacy-filter.cpp` ggml graph** (the original plan; kept as fallback in
  §6.7) — hand-build the MoE + banded-attention + sinks graph from scratch, à la
  vibevoice/parakeet/LocalVQE.

**Recommendation: pursue (A), the llama.cpp path.** Re-implementing the gpt-oss MoE graph
(experts + sinks + iswa + YaRN) by hand in (B) is exactly the work llama.cpp has already done
and hardened across CPU/CUDA/Metal/Vulkan, and would also force us to vendor the o200k
tokenizer and reinvent quantization. (A) reuses all of it and rides upstream momentum;
quantization (incl. apex-quant `--tensor-type-file`) and multi-backend acceleration come for
free. The cost is carrying a llama.cpp patch until/unless we upstream it (see trade-offs in
§6.5 and the new decisions in §9). **This supersedes the earlier "own a privacy-filter.cpp
repo" framing** — under path (A) the artifact is a llama.cpp patch (in
`backend/cpp/llama-cpp/patches/`) + a GGUF converter, not a separate C++ engine, and the
o200k-tokenizer-vendoring question (old §9.6) is moot because llama.cpp already has it.

This does **not** change Phase 0: the Python `transformers` backend stays the interim path and
the reference oracle (§3.3 items 1–4, decision §9.1 = "try the existing python backend first").

---

## 6. Implementing in llama.cpp (recommended path)

### 6.1 The model is gpt-oss + a token-classification head (config-confirmed)

`openai/privacy-filter` config.json: `architectures: [OpenAIPrivacyFilterForTokenClassification]`,
`model_type: openai_privacy_filter`, `num_hidden_layers: 8`, `hidden_size: 640`,
`num_attention_heads: 14`, `num_key_value_heads: 2`, `head_dim: 64`, `intermediate_size: 640`,
`num_local_experts: 128`, `num_experts_per_tok: 4`, `sliding_window: 128`, `vocab_size:
200064`, `rope_parameters: {yarn, θ=150000, factor=32, orig=4096}`, `num_labels: 33`. The
multilingual fine-tune is identical except `num_labels: 217`. Aside from the classification
head and the (non-causal) attention question, this is the gpt-oss architecture llama.cpp
already runs.

### 6.2 What llama.cpp already provides (verified against the vendored checkout)

- **gpt-oss arch**: `LLM_ARCH_OPENAI_MOE` → gguf `"gpt-oss"` (`src/llama-arch.cpp:115`), graph
  in `src/models/openai-moe.cpp` — `build_moe_ffn` top-k routing, `attn_sinks`
  (`ggml_soft_max_add_sinks`), iswa sliding-window (`build_attn_inp_kv_iswa`), NEOX
  RoPE + YaRN, gate/up/down expert tensors + biases. (PR #15091.)
- **o200k tokenizer**: `LLAMA_VOCAB_PRE_TYPE_GPT4O` regex + harmony special tokens
  (`src/llama-vocab.cpp:2207-2213`); convert via `_set_vocab_gpt2()` BPE export.
- **Non-causal / bidirectional attention**: per-context `cparams.causal_attn`,
  `llama_set_causal_attn(ctx,false)`, `LLAMA_ATTENTION_TYPE_NON_CAUSAL`
  (`src/llama-context.cpp:161-181, 1108-1118`). Non-causal needs the whole sequence in one
  ubatch (`n_ubatch ≥ n_tokens`). Used by BERT/ModernBERT/EuroBERT/T5-encoder.
- **Sequence-level classification head (merged)**: `LLAMA_POOLING_TYPE_RANK`, `cls`/`cls_out`
  tensors (`LLM_TENSOR_CLS{,_OUT}`), label metadata key `*.classifier.output_labels`,
  `n_cls_out` width. (PR #9510 reranking.)
- **Token-level classification head (OPEN upstream PR #19725 "llama: add
  BertForTokenClassification support")** — the substrate we want, and it explicitly targets
  OpenMed NER models:
  - adds `LLAMA_POOLING_TYPE_TOKEN_CLS` (`--pooling token-cls`);
  - in `build_pooling` applies `cls_out` projection at **every** token position →
    `[n_cls_out, n_tokens]`;
  - repurposes `llama_get_embeddings_ith(ctx,i)` to return `n_cls_out` **label logits** per
    token (instead of `n_embd` embeddings);
  - convert support for `*ForTokenClassification` (drops `head.` tensors, sets
    `PoolingType.TOKEN_CLS`, writes `n_cls_out` from `id2label`).
  - Small/contained: +209/−56 across 15 files. **But wired only for encoder BERT/ModernBERT,
    not a decoder arch like gpt-oss.**

### 6.3 The gap to close for a gpt-oss token classifier

On top of PR #19725's substrate, `LLM_ARCH_OPENAI_MOE` needs:

1. **A `GptOssForTokenClassification` (here: `OpenAIPrivacyFilterForTokenClassification`)
   convert class** in `convert_hf_to_gguf.py` that emits `cls.output`(+bias) and the
   `output_labels` metadata, sets `PoolingType.TOKEN_CLS`, and writes `n_cls_out` from
   `id2label` (33 or 217). Reuse the gpt-oss tensor mapping + `_set_vocab_gpt2()`.
2. **Load `cls_out`/`cls_out_b` for `LLM_ARCH_OPENAI_MOE`** in `src/llama-model.cpp`.
3. **Invoke `build_pooling` from `src/models/openai-moe.cpp`** — today that graph sets
   `t_logits` (LM head) and returns; it must instead expose per-token hidden states and run
   the TOKEN_CLS projection.
4. **Attention mode** (resolved from the HF source — see §6.9): the model is **bidirectional**
   (`self.is_causal = False`, no KV cache, `position_ids = arange`) with a **symmetric band**
   `|q − kv| ≤ 128` (the config `sliding_window: 128`; the `+1`→129 in the code is an FA-only
   symmetry fudge — use 128 for a manual mask). **Attention sinks are retained** (14, one per
   query head) and the **softmax is forced fp32**. All 8 layers use the *same* mask (no
   causal/global alternation). So we run `causal_attn=false` with a **non-causal,
   symmetric-banded mask** — which, per §6.10, **already exists** in llama.cpp
   (`LLAMA_SWA_TYPE_SYMMETRIC` + the no-cache `fill_mask`); we just select it via hparams. The
   residual risk is numerical parity vs HF (the `n_swa=256` mapping, fp32 softmax), not new ggml.

This is roughly the order of work of PR #19725 (a convert class + a handful of `src/` edits)
**plus** the non-causal banded-mask variant and the gpt-oss-reuse fixes in §6.9. We build on
PR #19725 (decision §9.10 = build on upstream, carry its diff in
`backend/cpp/llama-cpp/patches/` for dev — `prepare.sh` already injects files; we extend it to
apply patches). If upstream gets messy or stalls, the fallback ladder is: carry a patch →
copy the needed bits into a standalone project (§6.7).

### 6.4 Wiring into LocalAI's vendored llama-cpp backend

LocalAI's llama.cpp backend (`backend/cpp/llama-cpp/grpc-server.cpp`, injected into the
upstream tree by `prepare.sh`; llama.cpp pinned by commit in the `Makefile`) already
implements `Embedding`, `Rerank` (`POOLING_TYPE_RANK`), and `Score` via the server-context
task queue — but **not** `TokenClassify`. Adding it mirrors the Rerank path:

1. New `BackendServiceImpl::TokenClassify` RPC + a `SERVER_TASK_TYPE_TOKEN_CLASSIFY`.
2. Load the model with `pooling_type = TOKEN_CLS` and `causal_attn = false`
   (a new load flag, à la `--reranking`).
3. Run one non-causal forward (full sequence in one ubatch); read per-token logits via
   `llama_get_embeddings_ith` (= `n_cls_out` logits/token under TOKEN_CLS).
4. **Viterbi BIOES decode + span assembly + offset mapping** in the grpc-server (C++), using
   llama.cpp's tokenizer offsets to produce **byte** offsets, then fill
   `TokenClassifyEntity{entity_group, start, end, score, text}`. (Alternatively decode on the
   Go side, but C++ keeps the token→byte offset mapping next to the tokenizer.)
5. Capability metadata: add `MethodTokenClassify` + a `classification`/`ner` usecase in
   `core/config/backend_capabilities.go` and register it on the `llama-cpp` backend; follow
   the `.agents/api-endpoints-and-auth.md` checklist.

The Go `NERDetector` (§3.3 item 2) then just calls `TokenClassify` over the existing gRPC
client plumbing — identical contract whether the server is the Python backend (Phase 0) or
the llama.cpp backend (Phase 2).

### 6.5 Trade-offs of the llama.cpp path (vs standalone)

**Pros**: reuse the hardened gpt-oss MoE/sinks/iswa/YaRN graph; o200k tokenizer for free;
quantization (incl. apex-quant `--tensor-type-file`) and CUDA/Metal/Vulkan/CPU acceleration
for free; same backend LocalAI already ships and updates; potential to upstream and share
maintenance. **Cons**: we carry a patch against a pinned llama.cpp commit (rebases on bumps)
until upstreamed; depends on (or vendors) PR #19725; the bidirectional + banded-mask + sinks
combination is novel for a decoder arch and must be numerically verified; non-causal forces
single-ubatch (fine for short PII inputs, caps very long contexts).

### 6.6 Decoding correctness (a feature, not just a port)

The current Python path's `aggregation_strategy="simple"` ignores the model's intended
**constrained Viterbi** over the BIOES grammar (6 transition-bias params for precision/recall).
Implementing Viterbi properly (§6.4 step 4) is both the faithful port **and** an accuracy
improvement over what LocalAI does today. Keep the 6 transition biases configurable (backend
load options to start).

### 6.7 Fallback: standalone `privacy-filter.cpp` ggml graph

If the llama.cpp path is blocked (PR #19725 abandoned + we don't want to carry it, or the
bidirectional/sinks combination proves intractable inside llama.cpp), fall back to a dedicated
ggml graph under `localai-org` (decision §9.4), following vibevoice/parakeet/LocalVQE:
`*_graph.cpp` (banded non-causal MoE), `*_model.cpp`, `*_api.{h,cpp}`, vendored o200k
tokenizer, Viterbi, GGUF converter; `backend/go/privacy-filter-cpp/` dlopens it via `purego`
and implements `TokenClassify`. This is more code and duplicates llama.cpp's MoE graph, hence
the fallback ranking.

### 6.8 Quantization (apex-quant) — not needed day one

Decision §9.8 = F16 is acceptable (~3 GB). When/if we quantize: apex-quant
(<https://github.com/localai-org/apex-quant>) does MoE-aware mixed precision on **stock
llama.cpp** via `--tensor-type-file` — routed experts aggressive (IQ4_XS/Q4_K mid, Q5_K
near-edge, Q6_K edge), shared/always-active Q8_0, attention Q6_K. Under the llama.cpp path
this works out of the box. Verify with a **task metric (span-F1 per language) + KL-vs-F16**,
*not* perplexity (this is a classifier, not an LM).

### 6.9 Layer-by-layer reference (HF) + gpt-oss reuse risks

Verified against the HF *modular* source (`modular_openai_privacy_filter.py`, which inherits
gpt-oss + `masking_utils.py` + `modeling_rope_utils.py`) and the `opf/_core/` decoder. This is
the contract the llama.cpp port must match numerically, and it lists where the model **differs
from stock gpt-oss** (so a naive `LLM_ARCH_OPENAI_MOE` reuse drifts).

**Block order** (pre-norm, 8 layers, RMSNorm eps 1e-5, fp32 norm; no embedding scaling; final
`model.norm`; no LM head, no tied embeddings):
`x → input_layernorm → attn → +residual → post_attention_layernorm → MoE → +residual`, then
final norm → `score` (Linear 640→33/217, **with bias**, no activation, dropout 0.0) →
`log_softmax` → Viterbi.

**Tensor names → GGUF mapping** (per layer N):

| HF tensor | shape | note for the port |
|---|---|---|
| `model.embed_tokens.weight` | [200064, 640] | o200k vocab |
| `model.layers.N.input_layernorm.weight` | [640] | RMSNorm |
| `…self_attn.q_proj.{weight,bias}` | [896,640]/[896] | 14×64; **bias present** |
| `…self_attn.k_proj.{weight,bias}` | [128,640]/[128] | 2×64 (GQA group 7) |
| `…self_attn.v_proj.{weight,bias}` | [128,640]/[128] | |
| `…self_attn.o_proj.{weight,bias}` | [640,896]/[640] | |
| `…self_attn.sinks` | [14] | one per query head — **keep** |
| `…post_attention_layernorm.weight` | [640] | RMSNorm |
| `…mlp.router.{weight,bias}` | [128,640]/[128] | top-4 router, **bias present** |
| `…mlp.experts.gate_up_proj` | [128,640,1280] | fused, **chunk layout** (see risk #7) |
| `…mlp.experts.gate_up_proj_bias` | [128,1280] | |
| `…mlp.experts.down_proj` | [128,640,640] | |
| `…mlp.experts.down_proj_bias` | [128,640] | |
| `model.norm.weight` | [640] | |
| `score.{weight,bias}` | [33,640]/[33] | classification head (217 for multilingual) |

**Reuse risks vs stock gpt-oss (each is a known drift source — verify per §7):**

1. **Non-causal symmetric band** `|q−kv| ≤ 128`, same mask all layers (no causal, no
   global-layer alternation). gpt-oss uses causal iswa. → new mask variant.
2. **Window 128, not 129** (the code's `+1` is FA-only). Use 128 for the manual mask.
3. **Attention sinks present + softmax forced fp32** (sink logit column appended to the
   denominator, then dropped). gpt-oss has sinks too, but confirm fp32 softmax.
4. **Q and K each scaled by `head_dim**-0.25` *after* RoPE**, attention `scaling = 1.0`
   (algebraically `1/√d`, but split across q/k and post-RoPE — matters in low precision).
5. **No q_norm / k_norm.**
6. **RoPE = YaRN** with `attention_scaling = 0.1·ln(32)+1 = 1.34657` baked into cos/sin,
   θ=150000, **`truncate=False`** (no floor/ceil on the correction range — a YaRN impl that
   always floors/ceils diverges); cos/sin fp32; gpt-oss chunked-half rotary pairing.
7. **MoE expert gate/up uses `chunk(2)` (concatenated) layout, NOT gpt-oss interleaved
   `::2`/`1::2`.** The convert script must emit the layout llama.cpp's gpt-oss graph expects
   (or we adjust the graph). Wrong split → wrong gate/up assignment. Clamp: gate `max=7`, up
   `±7`, `(up+1)·(gate·σ(1.702·gate))`.
8. **MoE double-scaling**: router does `softmax(top4)/4`; the MLP then multiplies the expert
   sum by `num_experts_per_tok (=4)`. Net = `softmax(top4)`, but the divide/multiply happen at
   different points and everything runs **fp32** ("very sensitive to accumulation order").
9. **Router has a bias** `[128]` — don't drop it.
10. **Classification `score` head has a bias, no tanh/activation** (unlike some rerank heads).
    `n_cls_out` = 33 (base) / 217 (multilingual); label 0 = `O`.

**Decoder (`opf/_core/`)**: per-token `log_softmax` (fp32) → **constrained Viterbi** over BIOES
with start/end scores and valid-transition rules; fallback to per-token argmax if all paths
die. The **6 transition-bias params are all 0.0 in the shipped `viterbi_calibration.json`** —
so at the default operating point the biases are inert and only the *structural* BIOES
constraints matter (we still expose them as load options). Spans: BIOES walk → token spans →
**byte-accurate char offsets** from the tiktoken byte stream (`decode_text_with_offsets`,
UTF-8 aware) → convert to the **byte** offsets the proto/`NEREntity` want. Optional whitespace
trim + per-label de-overlap.

### 6.10 Concrete llama.cpp changes + arch decision (grounded in the vendored source)

Read against the vendored checkout (commit `22d66b56`): the gpt-oss graph
`src/models/openai-moe.cpp` (169 lines) and PR #19725's `build_pooling` branch. Any in-tree
work must follow `backend/cpp/llama-cpp/llama.cpp/AGENTS.md`.

**What we reuse unchanged** (this is why we don't hand-roll a graph): `build_inp_embd`,
`build_norm` (RMS), `build_qkv` (q/k/v + biases), `ggml_rope_ext` (YaRN via hparams),
`build_attn` (already takes `attn_sinks`!), and **`build_moe_ffn(..., LLM_FFN_SWIGLU_OAI_MOE,
…, SOFTMAX_WEIGHT, …)`** with the per-expert gate/up/down **biases** and router bias — exactly
the gpt-oss MoE. The expert tensors are loaded **already split** into `ffn_gate_exps` /
`ffn_up_exps` (the convert script does the splitting), so the chunk-vs-interleaved gate/up
issue (§6.9 risk #7) is **convert-time only** — once we split with `chunk(2)` the graph is
identical.

**Two numerical clarifications from reading the graph:**
- `build_attn(...)` is called with `kq_scale = 1.0f/sqrtf(n_rot) = 1/8`. The HF model scales q
  and k each by `head_dim**-0.25` post-RoPE with attn scale 1.0 → net `q·k/√d = 1/8` too. So
  **the standard `1/√d` path is numerically equivalent**; we do *not* need to replicate the
  split (verify within tolerance, §7).
- **YaRN `truncate=false`**: HF keeps the correction range as floats (no floor/ceil). ggml's
  `ggml_rope_ext` corr-dims may floor/ceil — a small potential drift. Treat as a verification
  risk; add a flag only if parity fails.

**What differs from gpt-oss (the actual new work):**
1. **Attention input** — *already supported, not new code.* gpt-oss uses
   `build_attn_inp_kv_iswa()` (causal KV-cache SWA). We instead use the **no-cache** path
   `build_attn_inp_no_cache()` whose `fill_mask` (`llama-graph.cpp:428-462`) already honors
   `cparams.causal_attn` **and** `is_masked_swa(...)`. Setting (in `load_hparams`)
   `causal_attn=false`, `swa_type=LLAMA_SWA_TYPE_SYMMETRIC`, `n_swa=256` yields exactly
   `|q−kv| ≤ 128` bidirectional — `LLAMA_SWA_TYPE_SYMMETRIC` (`llama-hparams.h:342`) masks
   `|p1−p0| > n_swa/2`. So the band falls out of existing primitives; the only trap is the
   **`n_swa = 2·sliding_window` (=256)** mapping (most likely off-by-one — verify against an HF
   mask dump). The band is **required for correctness**: real PII inputs routinely exceed the
   257-token window, and unbanded attention on them computes a different function. (An earlier
   draft called this "the one new primitive" and proposed "full non-causal first" — both wrong.)
2. **Serving long inputs — overlapping windows, exact.** Because attention is *strictly* local
   (±128), a token's logits depend only on its ±128 neighborhood. Processing the text in
   windows of width W with a halo ≥128 each side and keeping only the interior labels is
   **bit-exact** vs a single banded forward (window `[0,W)` keep `[0,W−128)`; stride `W−256`;
   absolute start/end need no halo). This bounds compute/memory to O(N·W), keeps each ubatch
   small (non-causal needs `n_ubatch ≥ window`), and is streamable. It is also *better* than
   OpenAI's `opf` runtime, which uses non-overlapping `n_ctx` windows and degrades at seams.
   Plan: ship the **single banded forward** first (correct at any length, simplest to verify),
   add **windowing** as the throughput/memory path for long inputs. HF parity tests use
   single-window inputs vs one banded forward.
3. **Output tail**: gpt-oss ends with the lm_head (`build_lora_mm(model.output)` → `t_logits`,
   lines 162-166). We instead stop at `result_norm` (`res->t_embd`) and route through
   **`build_pooling` with `LLAMA_POOLING_TYPE_TOKEN_CLS`** (PR #19725): `cur = inp; cur =
   ggml_mul_mat(cls_out, cur); cur = ggml_add(cur, cls_out_b);` → `[n_cls_out, n_tokens]`, like
   the encoder graphs. No lm_head, no `output`/`output_norm`-as-lm_head.
4. **Tensors**: load `cls_out`/`cls_out_b` (from `score.{weight,bias}`) instead of `output`;
   set `pooling_type = TOKEN_CLS`, `n_cls_out` (33/217), and write `output_labels` metadata —
   all already supported by PR #19725's plumbing.
5. **`build_inp_out_ids`**: with token-level pooling, n_outputs = all tokens (PR #19725's
   server already branches `token_level_pooling = NONE || TOKEN_CLS`).

**Decision §9.14 — register a SEPARATE arch.** Recommend a new
`LLM_ARCH_OPENAI_PRIVACY_FILTER` (gguf name `openai-privacy-filter`, matching HF
`model_type: openai_privacy_filter`) with its own ~120-line `src/models/openai-privacy-filter.cpp`
that *composes the shared helpers above* but swaps in the non-causal banded attn input and the
TOKEN_CLS tail. Rationale: (a) it does **not** fork the hot, well-maintained gpt-oss graph with
causal-vs-non-causal / lm_head-vs-cls conditionals (maintainers dislike that); (b) it mirrors
HF's separate model type; (c) it reuses every expensive kernel, so the diff is "a new graph
file + arch enum + loader + convert class," not new ggml ops. Touch points (per the
add-architecture guide): `gguf-py/gguf/constants.py` (MODEL_ARCH + tensor list incl.
`CLS_OUT`), `tensor_mapping.py`, a `convert_hf_to_gguf.py` class (subclass the gpt-oss
converter to reuse o200k vocab + tensor map; override the expert split to `chunk(2)`; emit
`cls.output(+bias)`, `PoolingType.TOKEN_CLS`, `n_cls_out`, `output_labels`; drop lm_head),
`src/llama-arch.{h,cpp}`, `src/llama-model.cpp` (`load_hparams` + tensor load + rope-type),
`src/models/openai-privacy-filter.cpp` + `models.h` + `src/CMakeLists.txt`.

**Draft skeletons** (grounded in the source above) live in `docs/plans/pii-ner-ggml/`:
`conversion_openai_privacy_filter.py` (the `GptOssModel` subclass — incl. the critical
`chunk(2)` expert split and the `score`→`cls.output` head), `openai-privacy-filter.cpp` (the
graph: `openai-moe.cpp` + the 3 marked changes), and `INTEGRATION.md` (the full touch-point
list, the PR #19725 carry, the `n_swa=256` trap, the LocalAI `TokenClassify` + windowing +
Viterbi wiring, and the verification ladder). They are design drafts, not built code.

---

## 7. Conversion methodology (from LocalVQE / parakeet experience)

Distilled from `~/c/LocalVQE-train/PROCESS.md` + `~/c/LocalVQE/ggml/` and the request's
pointers. These are the rules that made those ports succeed:

1. **Block-level numeric equivalence first.** Implement faithfully, then prove each block
   matches a PyTorch reference *before* the end-to-end path. LocalVQE dumps reference
   activations (`compare.py`) and asserts per-block match in C++ tests
   (`test_encoder.cpp`, …). For us: dump from the HF model, for a fixed multilingual prompt
   set, the intermediates most likely to drift given §6.9 — **post-RoPE q/k (after the
   `head_dim**-0.25` scaling)**, **attention probs with the sink column included (fp32)**,
   **router top-4 scores**, **per-expert gate/up/down outputs**, **post-MoE hidden (after the
   ×4 rescale)**, **final-norm hidden**, and **`score` logits** — then assert the llama.cpp
   graph matches each within tolerance, layer by layer. Also check the **YaRN inv_freq /
   attention_scaling** table directly (truncate=False). **Re-run the comparison after every
   change.** End-to-end accuracy hides where drift is introduced.
2. **One change at a time;** run a clean check before concluding. (PROCESS principle #5.)
3. **Architect for inference / quant-friendly shapes.** gpt-oss dims (640, 128 experts) are
   already fixed by the checkpoint, but choose Viterbi/buffer layouts and any padding to be
   SIMD- and block-size friendly (Q4_K block 256). (PROCESS principle #8.)
4. **No per-call allocation on the hot path.** LocalVQE pre-allocates with a ggml graph
   allocator and reports flat peak RSS regardless of thread count. Build a static graph sized
   to max context; reuse buffers across calls. (PROCESS §5 / §6.)
5. **Fuzz + assert.** LocalVQE ships a `fuzz/` harness and a `build-fuzz` config. Fuzz the
   tokenizer, offset mapping, and Viterbi decoder (empty input, all-`O`, adversarial UTF-8,
   max-length) with assertions on invariants (valid BIOES paths, offsets within bounds,
   no OOB).
6. **Cross-check references.** LocalVQE found real bugs in *both* upstream references by
   checking against two. Our references: the `opf` CLI output and the HF
   `OpenAIPrivacyFilterForTokenClassification` — compare both, especially around Viterbi vs
   pipeline aggregation.
7. **Verify per scenario/distribution.** Their lesson: averaged metrics hide failures. For
   us: evaluate span-F1 **per language** (the model is known-weaker on CJK), not one blended
   number. Build a small held-out set per language from AI4Privacy.

---

## 8. Proposed phased plan (for discussion)

**Phase −1 — optional upstream warm-up (parallelizable, decision §9.13).**
Help review/test/land **PR #19725** (the TOKEN_CLS substrate we build on) and/or revive
**PR #15189** (`echo` logprobs). Builds the maintainer relationship and de-risks Phase 1.
*Not* the originally-proposed standalone Score PR (that niche is taken).

**Phase 0 — interim, Python-backed (proves the seam end to end).**
Wire §3.3 items 1–4 against the existing `transformers` backend (`Type=TokenClassification`,
model = OpenMed/privacy-filter-multilingual). Fix char→byte offsets. Ship default
entity→action map + model `pii:` config. This delivers working ML PII filtering and gives us
the **reference oracle** for the port. (Removable later; keep behind config.)

**Phase 1 — llama.cpp conversion + offline parity** (path A, §6).
Track/grab PR #19725 for the TOKEN_CLS substrate. Add the
`OpenAIPrivacyFilterForTokenClassification` convert class (emits `cls.output` + `output_labels`
+ `n_cls_out`, reuses gpt-oss mapping/vocab); load `cls_out` for `LLM_ARCH_OPENAI_MOE`; call
`build_pooling` from `openai-moe.cpp`; resolve the non-causal/banded-mask + sinks question
(§6.3.4) **numerically against HF**. Achieve **block-level + final-logit parity** (§7.1) at
F16 on a per-language prompt set, using `llama-embedding --pooling token-cls` (or the
grpc-server) before any LocalAI wiring.

**Phase 2 — LocalAI llama-cpp backend `TokenClassify`.**
Add `TokenClassify` RPC + `SERVER_TASK_TYPE_TOKEN_CLASSIFY` to `grpc-server.cpp` (load with
`pooling=TOKEN_CLS`, `causal_attn=false`), Viterbi + offset mapping in C++ (§6.4); capability
metadata; carry the llama.cpp diff via `backend/cpp/llama-cpp/patches/` if not upstream. Point
the Go `NERDetector` at the llama-cpp backend; the Python path becomes optional/fallback.

**Phase 3 — productionization.**
Streaming/response-path decision (§9), gallery entry, docs (`docs/content/`), admin UI knobs,
MCP tool if it becomes admin-managed (`.agents/localai-assistant-mcp.md`).

**Phase 4 — quantization (apex-quant) + eval.**
Mixed-precision GGUF; verify with **span-F1 per language + KL-vs-F16**, not perplexity. Ship
F16 first; quantize only if footprint matters.

---

## 9. Open questions / decisions to make together

1. **Phase 0 first?** Do we ship the Python-backed interim to validate the middleware seam and
   get a reference oracle, or go straight to GGML? 

Try the existing python backend first.

2. **Model variant**: multilingual (217 classes, 16 langs) as primary? Also support the base
   (33 classes, en) and/or nemotron (clinical)? The label→action map and tests differ per
   variant.

   multilingual as primary

3. **Response/streaming**: request-side redaction only at first (cheapest, safest), or also
   buffer-and-classify model output? A 50M-active forward per SSE flush is non-trivial.

   Request side only at first

4. **Backend home**: new `privacy-filter.cpp` repo under which org, and do we own the upstream
   (like vibevoice/parakeet under `mudler`) or `localai-org` (like LocalVQE/apex-quant)?

   localai-org and yes we own the upstream

5. **Banded bidirectional attention**: confirm the band/window (card says band 128 → window
   257) against the actual config.json before building the mask.

   I don't know, but if there is a difference then I would have thought that the config.json is correct, but this is somethign to test

6. **Tokenizer**: reuse llama.cpp's o200k_base/tiktoken support as a vendored lib, or a
   minimal standalone BPE in `privacy-filter.cpp`?

   vendor it
7. **Default action policy**: what's the out-of-the-box mapping of 54 categories →
   block/mask/allow, and how much do we trust the model to *block* vs only *mask*?

   mask

8. **Quantization need**: is F16 (~3 GB) acceptable, or do we need apex-quant from day one
   (e.g. for edge/Vulkan targets)?

   F16 is OK

### New decisions raised by the llama.cpp path (§5/§6)

9. **Runtime path**: confirm **(A) extend llama.cpp** as primary (recommended), with the
   standalone `privacy-filter.cpp` graph as fallback (§6.7)? This reframes the old decision
   §9.4 — under (A) the artifact is a llama.cpp patch + GGUF converter, not a `localai-org`
   C++ engine — and makes §9.6 (vendor a tokenizer) moot (llama.cpp already has o200k).

10. **Upstream vs carry** — DECIDED: **build on upstream PR #19725** and carry its diff in
    `backend/cpp/llama-cpp/patches/` for dev. Escalation ladder if upstream is slow/messy:
    carry a patch → if real friction, copy only the bits we need into a standalone project
    (§6.7). We still aim to upstream our gpt-oss token-classification arch.

11. **Bidirectional + banded + sinks** — RESOLVED from the HF source (§6.3.4 / §6.9):
    **symmetric band `|q−kv| ≤ 128`** (config 128, not 129), **non-causal**, **sinks retained**
    (14/query-head), **fp32 softmax**, same mask all 8 layers. The new primitive is a
    non-causal symmetric-banded SWA mask. *Remaining* validation = numerical parity vs HF
    (§7.1), not "does it exist". Upstream-friendliness: this is a clean new arch
    (`openai_privacy_filter`) rather than a hack on gpt-oss; keep it that way (see §9.13).

12. **Where Viterbi lives**: decode BIOES + map token→byte offsets in the C++ grpc-server
    (next to the tokenizer, recommended) or on the Go side after receiving raw per-token
    logits (would need a new proto carrying logits + offsets)? Recommend C++ to keep the
    `TokenClassify` contract unchanged.

13. **Easy upstream warm-up** — the proposed "Score / continuation (echo) logprobs into
    llama-server" is **already owned by a core maintainer**: ngxson's PR #17935 (closed) and
    fo40225's PR #15189 (open) implement OpenAI-style `echo`+prompt-logprobs, modelled on
    `tools/perplexity/perplexity.cpp` (the same primitive LocalAI's `Score` uses —
    `core/backend/score.go`, grpc-server `Score`). Opening a competing PR would collide. Better
    warm-ups that build the *same* familiarity/relationship and de-risk our path:
    **(a) help review/test and land PR #19725** (the substrate we depend on — author pinged
    CISC; it needs 2 approvals and is unreviewed); **(b) help revive PR #15189** (gets `echo`
    logprobs upstream so LocalAI can eventually retire its custom `Score`). Recommend (a).
    Note hygiene norms before any PR: snake_case, `LLAMA_*` enum prefixes, 4-space + brace-on-
    same-line, plain `for` loops, **no new deps/files/headers**, reuse existing machinery,
    one PR at a time, and an `Assisted-by:` trailer (LocalAI policy) / plain AI-disclosure line
    (upstream norm).

14. **Arch identity upstream** — RECOMMENDED (analysis in §6.10): register a **separate**
    `LLM_ARCH_OPENAI_PRIVACY_FILTER` (gguf `openai-privacy-filter`, matching
    `config.model_type`) with its own small graph file composing the shared gpt-oss helpers,
    rather than forking `LLM_ARCH_OPENAI_MOE` with causal/lm_head conditionals. Confirm this
    framing (it drives the upstream PR shape). Open sub-question: does the non-causal
    symmetric-banded mask go in as a reusable primitive or a privacy-filter-local mask?

---

## 10. Key references

- openai/privacy-filter — model card: <https://huggingface.co/openai/privacy-filter>;
  repo: <https://github.com/openai/privacy-filter>; model card PDF:
  <https://cdn.openai.com/pdf/c66281ed-b638-456a-8ce1-97e9f5264a90/OpenAI-Privacy-Filter-Model-Card.pdf>
- OpenMed/privacy-filter-multilingual: <https://huggingface.co/OpenMed/privacy-filter-multilingual>
  (MLX variants `-mlx-8bit`; clinical `privacy-filter-nemotron`); docs:
  <https://openmed.life/docs/anonymization/>
- HF Transformers integration:
  <https://github.com/huggingface/transformers/blob/main/docs/source/en/model_doc/openai_privacy_filter.md>
- apex-quant (MoE GGUF quant): <https://github.com/localai-org/apex-quant>
- llama.cpp gpt-oss support: PR <https://github.com/ggml-org/llama.cpp/pull/15091>,
  guide <https://github.com/ggml-org/llama.cpp/discussions/15396>
- llama.cpp token-classification substrate (the key precedent, OPEN): PR
  <https://github.com/ggml-org/llama.cpp/pull/19725> ("add BertForTokenClassification support")
- llama.cpp reranking / sequence-classification head (merged): PR
  <https://github.com/ggml-org/llama.cpp/pull/9510>
- llama.cpp `echo`/prompt-logprobs (the contested "Score" warm-up): PR
  <https://github.com/ggml-org/llama.cpp/pull/15189> (open),
  <https://github.com/ggml-org/llama.cpp/pull/17935> (ngxson, closed),
  issues <https://github.com/ggml-org/llama.cpp/issues/8942>,
  <https://github.com/ggml-org/llama.cpp/issues/12591>; primitive in
  `tools/perplexity/perplexity.cpp` (`log_softmax` + `llama_get_logits_ith`)
- llama.cpp "add a new architecture" guide:
  <https://github.com/ggml-org/llama.cpp/discussions/16770>,
  <https://github.com/ggml-org/llama.cpp/blob/master/docs/development/HOWTO-add-model.md>;
  contribution norms: <https://github.com/ggml-org/llama.cpp/blob/master/CONTRIBUTING.md>
- HF reference (source of truth for layer parity): `transformers`
  `models/openai_privacy_filter/modular_openai_privacy_filter.py` (+ inherited `gpt_oss`,
  `masking_utils.py`, `modeling_rope_utils.py`); decoder `openai/privacy-filter`
  `opf/_core/{decoding,sequence_labeling,spans,runtime}.py`; `viterbi_calibration.json`
- LocalAI vendored llama.cpp backend: `backend/cpp/llama-cpp/{grpc-server.cpp,prepare.sh,Makefile}`,
  upstream graph `src/models/openai-moe.cpp`; LocalAI Score shape: `core/backend/score.go`
- Conversion methodology: `~/c/LocalVQE-train/PROCESS.md`, `~/c/LocalVQE/ggml/`,
  `~/c/LocalVQE/README.md`
- LocalAI integration points (in-repo):
  - PII seam: `core/services/routing/pii/{ner.go,redactor.go,types.go}`
  - gRPC: `backend/backend.proto` (TokenClassify), `pkg/grpc/*`, `pkg/model/connection_evicting_client.go`
  - Existing Python impl (reference): `backend/python/transformers/backend.py:203,271`
  - Capability registry: `core/config/backend_capabilities.go`
  - GGML backend templates: `backend/go/{vibevoice-cpp,localvqe,parakeet-cpp}/`
</content>
</invoke>
