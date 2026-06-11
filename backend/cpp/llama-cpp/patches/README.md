# llama.cpp carry-patches

`prepare.sh` applies every file in this directory to the freshly-cloned
`llama.cpp/` tree with `patch -p1`, in lexical order, before the grpc-server
sources are copied in. Keep patches small, ordered, and documented here.

## 0001-token-cls-pooling-substrate.patch

**What:** adds a per-token classification pooling path to llama.cpp:
`LLAMA_POOLING_TYPE_TOKEN_CLS` (= 5). Under this pooling type `build_pooling`
applies the model's `cls_out` (+`cls_out_b`) head to **every** token instead of
to a single pooled vector, and `llama_context::{encode,decode}` copy the
resulting `[n_cls_out, n_tokens]` logits into the embeddings buffer
(`llama_get_embeddings_ith(i)` then returns the `n_cls_out` logits for token
`i`). The `--pooling token-cls` CLI flag and the `llama-embedding` example are
taught to treat it as token-level (like `none`). In `tools/server`,
`send_embedding` likewise treats TOKEN_CLS as token-level (per-token
`llama_get_embeddings_ith` reads, no normalization) and the embeddings
endpoint rejects it for OAI-compat response types — this is what lets
embedding *tasks* return raw per-token classifier logits, which the
grpc-server's TokenClassify RPC rides for slot-loop-scheduled NER.

This is the substrate the `openai-privacy-filter` token-classifier arch needs
(patches 0002/0003): the encoder graph ends at `result_norm` and lets the
framework attach the score head per token.

**Provenance:** a reduced subset of upstream PR
[ggml-org/llama.cpp#19725](https://github.com/ggml-org/llama.cpp/pull/19725)
("llama: add BertForTokenClassification support"). We carry **only** the
pooling-mechanism hunks (`include/llama.h`, `src/llama-graph.cpp`,
`src/llama-context.cpp`, `common/arg.cpp`, `examples/embedding/embedding.cpp`,
`gguf-py/gguf/constants.py`, and the PR's `tools/server/server-context.cpp`
`token_level_pooling` hunks re-based to the pin's `slot.ctx_tgt` naming).
We deliberately drop the PR's BERT/WPM-specific parts (the `convert_hf_to_gguf.py` BertModel changes — our converter is its own
`conversion/openai_privacy_filter.py`; and the WPM `do_lower_case` tokenizer
plumbing — privacy-filter uses o200k BPE, not WordPiece). The prerequisites the
substrate assumes (`gguf_writer.add_embedding_length_out` /
`add_classifier_output_labels`, `hparams.n_cls_out` / `n_embd_out()`,
`model.cls_out` / `cls_out_b`) already exist in the pinned tree.

**Re-sync:** PR #19725 is still OPEN; if it changes under review, re-diff.
If/when we upstream the `openai-privacy-filter` arch we will depend on TOKEN_CLS
having landed (or keep carrying this).

**Version note:** authored against `d6588daa8`; re-verified (line-offset
only) against the current pin `5dcb71166`. See the consolidated version
note at the bottom of this file.

## 0002-arch-openai-privacy-filter.patch

**What:** registers the `openai-privacy-filter` architecture (matching the
model's `config.model_type == "openai_privacy_filter"`):
- `src/llama-arch.h` / `.cpp`: `LLM_ARCH_OPENAI_PRIVACY_FILTER` + name string.
  No per-arch tensor-name table is needed — this llama.cpp uses a single global
  `LLM_TENSOR_NAMES` map, and every tensor we use (incl. `cls.output`,
  `attn_sinks`) is already in it.
- `gguf-py/gguf/constants.py`: `MODEL_ARCH.OPENAI_PRIVACY_FILTER`, its name, and
  a `MODEL_TENSORS` list = the gpt-oss set **minus `OUTPUT`** (no LM head)
  **plus `CLS_OUT`** (the score head).
- `gguf-py/gguf/tensor_mapping.py`: maps HF `score` → `MODEL_TENSOR.CLS_OUT`, so
  `score.{weight,bias}` convert to `cls.output.{weight,bias}`.

The loader/graph for the arch (`llama-model.cpp`, `src/models/…`) come in 0003.
`patch -p1 --dry-run` clean atop 0001 (see the version note at the bottom).

## 0003-convert-openai-privacy-filter.patch

**What:** the HF→GGUF converter. Adds `conversion/openai_privacy_filter.py`
(`OpenAIPrivacyFilterModel`, a `GptOssModel` subclass) and registers it in
`conversion/__init__.py` (`OpenAIPrivacyFilterForTokenClassification` →
`openai_privacy_filter`). It reuses the gpt-oss vocab and tensor handling and
overrides only:
- **expert `gate_up` split** — privacy-filter packs gate/up as **concatenated
  halves** (`chunk(2)`), *not* gpt-oss's interleaved `::2`/`1::2`. This is the
  one load-bearing divergence; a wrong split yields a silently-wrong model.
  **Confirmed correct by per-layer parity** (full-logit cos = 1.0 vs HF; the FFN
  out matched once the attention upstream was fixed — see below).
- **per-dim RoPE frequency factors** (`generate_extra_tensors`) — the model's
  `rope_parameters` set YaRN with `truncate: false`, but ggml's `rope_yarn`
  unconditionally `floor()/ceil()`s the interpolation-ramp boundaries. That
  rounding shifts the ramp in the transition band (here dims ~20–34), a per-dim
  frequency error up to ~21% that mis-rotates Q/K and softens attention. Rather
  than change ggml's shared YaRN (which would perturb every other YaRN model),
  the converter computes HF's *exact* `inv_freq` (truncate=false) and writes
  `rope_freqs.weight = extrap / inv_freq` (1.0 … factor). The loader (0004)
  then disables ggml's YaRN ramp and keeps only the YaRN attention mscale, so
  these freq-factors fully define the per-dim frequencies.
- **token-classification head** — writes `pooling_type = TOKEN_CLS`, the ordered
  `id2label` table (`add_classifier_output_labels`), and `n_embd_out =
  len(labels)` (= n_cls_out). `score.{weight,bias}` map to `cls.output.*` via
  the 0002 `tensor_mapping` entry; no LM head is emitted.
- aliases `rope_parameters` → `rope_scaling` so the base YaRN handling fires
  (this arch renamed the key).

Everything else (down_proj, q/k/v/o + biases, attn sinks, router, norms,
embeddings) converts via the gpt-oss base unchanged.

**Validated end-to-end** against the real `OpenMed/privacy-filter-multilingual`
weights: `convert_hf_to_gguf.py` produces a 156-tensor F16 GGUF whose metadata
is correct — `general.architecture = openai-privacy-filter`, `pooling_type = 5`
(TOKEN_CLS), 217 `classifier.output_labels`, `embedding_length_out = 217`,
`cls.output.{weight 640×217, bias 217}`, rope `yarn`/factor 32/orig_ctx 4096/
freq_base 150000, experts 128/4, sliding_window 128. The only thing the GGUF
structure can't confirm is the gate_up *packing order* — that's a numeric check
deferred to per-layer parity (Task 5).

Repro (one-shot env; torch needs a 64-bit libstdc++ on `LD_LIBRARY_PATH` under
nix): `pip install torch numpy safetensors sentencepiece protobuf transformers`
into a venv, then
`PYTHONPATH=gguf-py python convert_hf_to_gguf.py <model_dir> --outtype f16`.

## 0004-graph-openai-privacy-filter.patch

**What:** the model class, graph, and loader wiring for the
`openai-privacy-filter` arch. Adds `src/models/openai-privacy-filter.cpp`
(`llama_model_openai_privacy_filter` — `load_arch_hparams` /
`load_arch_tensors` / `build_arch_graph` + nested `graph`), its `struct`
declaration in `src/models/models.h`, and wiring sites in
`src/llama-model.cpp` (the factory `case`, the **NORM** `rope_type` list, and
the `n_ff_exp` info-log condition). No `CMakeLists.txt` change — model
sources are gathered by `file(GLOB "models/*.cpp")`.

The graph is the `llama_model_openai_moe` body re-purposed as a
bidirectional token classifier:
- `load_arch_hparams` sets `causal_attn = false`, `swa_type =
  LLAMA_SWA_TYPE_SYMMETRIC`, `n_swa = 2 * sliding_window` (SYMMETRIC masks
  `|p1-p0| > n_swa/2`, so the HF half-width window round-trips), and
  `set_swa_pattern(0)` so **every** layer is windowed (uniform band, no
  alternating dense layers).
- the graph uses `build_attn_inp_no_cache()` (no KV cache; the no-cache
  input allocates the SWA mask because `swa_type != NONE`), passes the
  per-layer `attn_sinks` to `build_attn`, and **omits `build_inp_out_ids()`
  pruning** so every token keeps a logit.
- **RoPE.** privacy-filter uses the **interleaved (GPT-J) rope layout**
  (`_apply_rotary_emb` pairs `x[..., ::2]/x[..., 1::2]`), so the arch returns
  `LLAMA_ROPE_TYPE_NORM` — *unlike* gpt-oss (`OPENAI_MOE`), which uses NEOX
  rotate-half. (This was the dominant parity bug: NEOX mis-pairs the rotated
  dims, leaving a per-token cos ≈ 0.82 that no frequency tweak could fix.)
  `load_arch_hparams` also bakes the YaRN `truncate=false` fix: it sets
  `rope_scaling_type = NONE` (disables ggml's floor/ceil YaRN ramp), keeps the
  YaRN mscale via `rope_attn_factor = 1 + 0.1·ln(factor)`, sets the SWA
  freq-scale to 1.0, and the graph passes the per-layer `rope_freqs`
  (loaded from `rope_freqs.weight`, written by 0003) into `ggml_rope_ext` so
  the per-dim frequencies reproduce HF exactly.
- it ends at `res->t_embd` (no LM head). The framework then calls
  `build_pooling()`, which under `pooling_type == TOKEN_CLS` applies
  `cls_out`/`cls_out_b` per token (carry-patch 0001). `load_arch_tensors`
  loads `cls.output.{weight,bias}`, the per-layer `rope_freqs`, and no
  `output`/LM head.

**Parity: solved.** Against `OpenMed/privacy-filter-multilingual` at F16, the
new arch matches the HF reference token-for-token (12/12 argmax, full-logit
cosine = 1.0; every layer's residual stream cos = 1.0, relerr ≈ 2e-4 = F16
rounding), including the e-mail BIOES span. Verified on the real
`llama-embedding` binary (model-default TOKEN_CLS pooling — do **not** pass
`--pooling none`, which overrides it). The two parity-gated assumptions —
`n_swa = 2 * sliding_window` and 0003's gate_up packing — are both confirmed
correct.

## 0005-no-cache-all-swa-mask-fix.patch

**What:** a robustness fix to the no-cache attention input
(`src/llama-graph.{cpp,h}`). The no-cache input creates two mask tensors —
the full (non-SWA) mask and, when `swa_type != NONE`, the SWA mask — but a
*graph* consumes a mask only if it builds a layer of that attention type,
and the allocator prunes an unconsumed graph input (null buffer). The
openai-privacy-filter encoder makes **every** layer SWA (uniform symmetric
window), so the full `self_kq_mask` is never referenced and the stock
unconditional fill in `set_input` writes through a null `->data` and aborts
at `GGML_ASSERT(ggml_backend_buffer_is_host(...))`.

The fix records the layer composition of the graph *while it is built*: the
no-cache `build_attn` accumulates an `llm_swa_mix` (UNSET/NONE/SOME/ALL)
state machine on the input object as each built layer selects its mask, and
`set_input` then fills exactly the masks the current graph consumes — full
iff `mix != ALL`, SWA iff `mix != NONE` — asserting that every filled mask
exists and is host-allocated and that at least one `build_attn` ran
(`mix != UNSET`). Deriving the predicate from recorded per-graph consumption
(rather than model-global hparams, which can diverge from a graph's actual
layer subset, or buffer-nullness, which would silently absorb allocation
bugs) keeps failures loud: a mask the graph needs but didn't get still
asserts here instead of corrupting attention downstream.

This is a general fix — any all-SWA no-cache (encoder) model needs it — and
is a candidate to upstream separately. Without it the model loads but
aborts on first `decode`. Re-verified at pin `7c158fbb`: all five patches
apply in order with `patch -p1 --fuzz=0`, the tree compiles, and
`llama-embedding` reproduces the HF reference (21/21 per-token argmax,
full-logit cosine ≥ 0.9997 at F16).

## 0006-server-task-type-score.patch

**What:** adds `SERVER_TASK_TYPE_SCORE` to `tools/server` so teacher-forced
log-prob scoring of a candidate continuation rides the slot scheduler like
completions/embeddings/rerank, instead of requiring a dedicated locked
`llama_context`. A score task carries `params.score_start` (the index of the
first scored token); the batch builder flags positions `score_start-1 ..
n-2` for logits output, a per-chunk `accumulate_score` harvests
`log_softmax(logits)[next_token]` after **every** successful `llama_decode`
(the context only retains the outputs of the most recent call, and a scored
region can span multiple `n_batch` chunks — this also lifts the input cap to
`n_ctx`), and `send_score` emits a `server_task_result_score` (sum + per-token
log-probs) at `DONE_PROMPT`. Prompt-cache reuse is clamped to
`score_start-1` so the position predicting the first scored token is always
re-decoded; beyond that, cross-candidate prompt-KV reuse comes free from the
slot prompt cache. Touches `server-task.{h,cpp}` and `server-context.cpp`
only — no HTTP endpoint; LocalAI's grpc-server `Score` RPC is the consumer.

**Provenance:** original (no upstream equivalent). Written upstream-shaped:
an upstream PR would add a `POST /score` route on top.

**Why:** the grpc-server's `Score` and `TokenClassify` RPCs previously drove
`llama_decode` directly, serialized by mutexes plus a fatal tripwire against
the slot loop, and LocalAI's config validation had to forbid combining
`score`/`token_classify` with chat/completion on llama-cpp. With 0006 (and
0001's server hunks for TOKEN_CLS), both RPCs post regular server tasks and
those restrictions are gone.

---

**Version note (applies to all patches here):** patches 0001–0003 were
originally authored against `d6588daa8`, regenerated for `5dcb71166`, and
re-synced after LocalAI bumped `Makefile` `LLAMA_VERSION` to
`7c158fbb4aec1bdc9c81d6ca0e785139f4826fae`. All six patches apply in order
with `patch -p1 --fuzz=0` (no rejected hunks, idempotent re-run) at that pin,
and the patched tree compiles (`llama-server` and `grpc-server` targets).
Re-run the apply check after any further `LLAMA_VERSION` bump.
