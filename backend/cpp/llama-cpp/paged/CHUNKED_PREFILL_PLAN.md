# Chunked prefill + n_batch/n_ubatch decouple — implementation plan

Scope: LocalAI's llama.cpp backend (`backend/cpp/llama-cpp/`). Companion to
`PHASED_VLLM_PARITY_PLAN.md` Phase 3. This document is the concrete, file-cited
plan for what the brief called "chunked prefill".

Line numbers below are from two trees:
- LocalAI: `backend/cpp/llama-cpp/grpc-server.cpp`, `core/backend/options.go`,
  `backend/backend.proto`, `core/backend/hardware_defaults.go` — exact.
- Vendored upstream scheduler: `llama.cpp/tools/server/server-context.cpp`. The
  build copies `llama.cpp/tools/server/*` into `tools/grpc-server/` (`prepare.sh`
  lines 15-17) and only overrides `grpc-server.cpp` + `CMakeLists.txt`. So
  `update_slots()` is **inherited upstream code, not LocalAI code**. Line numbers
  cited for it are from a same-era checkout (`d12cc3d`, 2026-04-09); the pin is
  `f3e1828` (Makefile line 2). The structure is identical; exact lines may drift
  a few rows at the pin — match on the quoted comment strings, not the integers.

---

## TL;DR — the headline finding

**Chunked prefill with prefill/decode interleaving is ALREADY implemented** in the
llama.cpp server scheduler that LocalAI vendors. It is not a missing feature on
this version. `update_slots()` in `server-context.cpp`:

1. **Adds ongoing decode tokens first** — "first, add sampled tokens from any
   ongoing sequences" (≈ line 2088). Every `SLOT_STATE_GENERATING` slot gets its
   one sampled token into the shared `llama_batch` before any prefill is added.
2. **Then fills the remaining `n_batch` budget with prompt (prefill) tokens** —
   "next, batch any pending prompts without exceeding n_batch" (≈ line 2166),
   gated by `params_base.cont_batching` (LocalAI sets `cont_batching = true` by
   default, `grpc-server.cpp:547`). The per-slot prefill fill loop
   (≈ line 2552) is `while (slot.prompt.n_tokens() < slot.task->n_tokens() &&
   batch.n_tokens < n_batch)` — i.e. it caps each slot's prefill contribution to
   the **remaining** budget and defers the rest to the next iteration.
3. **Decodes the combined batch in one pass** (≈ line 2728-2741): decode tokens
   and prefill-chunk tokens go through the **same `llama_decode`**, which then
   splits internally into `n_ubatch` physical sub-batches.

This is exactly the behavior the abandoned-looking draft **upstream PR #10718**
("server : chunked prefill support") asked for — "the first task is no longer
blocked by the second long prompt processing task." That PR is still marked OPEN
but its goal was absorbed into the natural evolution of `update_slots()`; we do
**not** need to port it. A long prefill no longer stalls the decode batch: decode
slots are serviced first every iteration, prefill consumes only the leftover
budget.

**Therefore: do not re-implement chunked prefill.** The real LocalAI gap is
narrow and is the rest of this plan:

- **Phase A (the actual gap): the `n_batch`/`n_ubatch` decouple.** LocalAI ties
  the scheduler token budget (`n_batch`) to the physical forward width
  (`n_ubatch`) at `grpc-server.cpp:515` + `:519`. This forces
  `n_batch == n_ubatch`, so the logical scheduling window can never be wider than
  one physical ubatch. You cannot keep `n_ubatch` at the Blackwell GEMM sweet
  spot (2048) while widening `n_batch` so concurrent prefills + decodes co-batch
  into a larger logical window. There is no first-class `batch:`/`ubatch:` split
  on the Go side, and there is only a one-directional `ubatch` override on the C++
  side (you can shrink ubatch below the coupled value, never grow n_batch above
  it).
- **Phase B (optional policy lever): a decode-headroom prefill cap.** Upstream
  caps prefill at the full `n_batch` shared with decode. Under heavy mixed load
  one fat prefill chunk per iteration still adds inter-token latency (ITL) jitter
  to the decoders sharing that forward. vLLM exposes
  `long_prefill_token_threshold` / `max_num_partial_prefills` for this. A
  LocalAI-specific per-iteration prefill cap (a patch to vendored `update_slots`)
  bounds that jitter. This is genuinely not in upstream and is the only place a
  scheduler-policy change is warranted.

---

## 1. Current behavior — precise citations

### 1.1 The scheduler is upstream, inherited verbatim
- `prepare.sh:15-17` copies all of `llama.cpp/tools/server/*` into the
  `grpc-server` build dir; `grpc-server.cpp` (LocalAI) replaces only the HTTP/gRPC
  service + `params_parse` + `parse_options`. `update_slots()`, the slot state
  machine, and the batch builder are **upstream `server-context.cpp`**, untouched
  by LocalAI today.
- Slot states: `server-context.cpp:36-42` —
  `SLOT_STATE_IDLE / WAIT_OTHER / STARTED / PROCESSING_PROMPT / DONE_PROMPT /
  GENERATING`.

### 1.2 Decode-first, then prefill-fill, one shared batch
- `common_batch_clear(batch)` (≈ 2078) — one batch per `update_slots` iteration.
- Decode phase (≈ 2088-2156): for each `SLOT_STATE_GENERATING` slot,
  `common_batch_add(batch, slot.sampled, …, /*logits=*/true)` adds exactly one
  token. Decode is guaranteed a seat before prefill runs.
- Budget fetch (≈ 2158-2160): `n_batch = llama_n_batch(ctx)`,
  `n_ubatch = llama_n_ubatch(ctx)`.
- Prefill phase (≈ 2166): `if (params_base.cont_batching || batch.n_tokens == 0)`
  → with cont_batching ON, prefill is added to the **same** batch as decode.
- Per-slot prefill fill (≈ 2552-2597):
  `while (slot.prompt.n_tokens() < slot.task->n_tokens() && batch.n_tokens < n_batch)`
  — adds prompt tokens until the slot is done **or** the shared budget is hit.
  Whatever does not fit stays for the next iteration (the slot remains
  `SLOT_STATE_PROCESSING_PROMPT`).
- Whole-prompt completion (≈ 2603-2615): when the slot's prompt is fully consumed
  it flips to `SLOT_STATE_DONE_PROMPT`, sets `batch.logits[last] = true`, inits
  the sampler. Next iteration it becomes `GENERATING`.
- Budget break (≈ 2693-2695): `if (batch.n_tokens >= n_batch) break;`.
- Decode (≈ 2728-2741): loops `batch_view` slices of `min(n_batch, remaining)` and
  calls `llama_decode`; the physical `n_ubatch` split happens inside
  `llama_decode`.

### 1.3 The chunking is gated by `can_split()`
- `server-context.cpp:225-231`: `can_split()` returns true unless the task needs
  embeddings with non-LAST pooling. So **completion/generation tasks always
  chunk-and-interleave**; only embeddings/rerank force the whole prompt into one
  ubatch (≈ 2234-2244 raises "input is too large… increase the physical batch
  size" — this is exactly why LocalAI bumped `n_ubatch` for rerank, see below).

### 1.4 LocalAI ties n_batch to n_ubatch (the gap)
- `grpc-server.cpp:515` — `params.n_batch  = request->nbatch();`
- `grpc-server.cpp:519` — `params.n_ubatch = request->nbatch();` with the comment
  that this fixes reranking being capped at the 512 default `n_ubatch`.
- `grpc-server.cpp:781-784` — the **only** decouple knob today: an `n_ubatch` /
  `ubatch` option that overrides `n_ubatch` alone (added for embeddings/rerank).
  There is **no** `batch` / `n_batch` option parse, so `n_batch` cannot be raised
  above the coupled value from a model config. Confirmed: `grep '"n_batch"|"batch"'`
  in `grpc-server.cpp` returns nothing.
- Options arrive via `request->options(i)` parsed as `optname:optval`
  (`grpc-server.cpp:584-585`); these come from `ModelOptions.Options` ⟵
  `c.Options` (`core/backend/options.go:221`).

### 1.5 Go side sends a single batch number
- `backend/backend.proto:341` — `int32 NBatch = 4;` is the only batch field; there
  is **no** `NUBatch`.
- `core/backend/options.go:108-129` `EffectiveBatchSize`: returns `c.Batch` if set,
  else context size for single-pass (score/embed/rerank), else
  `hardwareDefaultBatchSize(512)`.
- `core/backend/options.go:228` — `NBatch: int32(b)` (single value to the
  backend; becomes both `n_batch` and `n_ubatch` via 1.4).
- `core/backend/hardware_defaults.go:28,37-40` — `BlackwellBatchSize = 2048`;
  on Blackwell an unset batch defaults to 2048, so today
  `n_batch == n_ubatch == 2048` there.

---

## 2. Why the decouple matters for serving (not just rerank)

Invariant: `n_ubatch <= n_batch`. `n_ubatch` is the physical forward-pass GEMM
width (compute efficiency; GB10 sweet spot ≈ 2048). `n_batch` is the per-iteration
**scheduler token budget** — the logical window shared by decode + prefill chunks,
analogous to vLLM's `max_num_batched_tokens`.

With `n_batch == n_ubatch` (today), the scheduling window cannot exceed one
physical ubatch. Consequences:
- Under concurrency, the combined (decode + multiple prefill chunks) logical batch
  is capped at the physical ubatch, so aggregate prefill cannot grow past one
  ubatch worth of tokens per iteration even when more slots have prompts queued.
- A user who shrinks `batch:` for memory also shrinks the physical ubatch,
  degrading prefill GEMM efficiency — and vice versa.

Decoupling lets us hold `n_ubatch = 2048` (efficient GEMM) while setting a larger
`n_batch` (e.g. 4096) so more concurrent prefill+decode tokens co-schedule into one
logical window, lifting aggregate prefill under mixed load — `llama_decode` still
tiles the physical work at 2048.

---

## 3. Phased implementation

### Phase 0 — Verification harness (do first; TDD red)
Bite-sized, no code change to the scheduler.
- **0.1 Token-identical greedy under mixed load.** Script: start the backend with
  `n_parallel >= 4`, greedy sampling (temp 0, fixed seed). Fire (a) several short
  decode streams and (b) one ~8k-token prompt concurrently (the exact repro from
  PR #10718's body works). Capture each stream's full token id sequence. Re-run
  with the prefill request absent. **Assert the short streams' token ids are
  byte-identical** in both runs — proves interleaving does not perturb decode
  numerics (KV/position correctness across chunk boundaries). Wire as a Ginkgo
  spec under the backend e2e suite.
- **0.2 Mixed-workload throughput baseline.** Use `llama-batched-bench` (built from
  the same tree) or a small driver hitting `/v1/chat/completions`: measure
  aggregate prefill tok/s and decode tok/s, and p50/p99 ITL of the decode streams,
  under the mixed workload. Record numbers for the current `n_batch==n_ubatch`
  config. This is the before of Phase A/B.

Expected result of Phase 0: 0.1 already passes (interleave is correct today);
0.2 gives the baseline the decouple must beat.

### Phase A — Decouple n_batch from n_ubatch
Goal: let model config set the physical ubatch independently of the logical batch,
defaulting to today's behavior (no regression).

- **A.1 C++: accept a `batch`/`n_batch` option (and keep `ubatch`).**
  In `grpc-server.cpp`, after the existing `ubatch` branch (`:781-784`), add a
  sibling branch:
  ```cpp
  } else if (!strcmp(optname, "n_batch") || !strcmp(optname, "batch")) {
      if (optval != NULL) {
          try { params.n_batch = std::stoi(optval_str); } catch (...) {}
      }
  ```
  This is the missing direction (raise `n_batch` above the coupled value). Order
  matters: both `:515/:519` run first (coupling as default), then option parsing
  overrides either independently. Add a clamp note: if a user sets
  `n_ubatch > n_batch`, llama.cpp will clamp/upbatch; log a warning. Keep the
  `:519` aliasing for backward compat (rerank still works with no options).

- **A.2 Proto: add an explicit physical ubatch field.**
  `backend/backend.proto:341` add `int32 NUBatch = <next free tag>;` (do not reuse
  4). Regenerate with `make protogen-go` + the C++ proto build.

- **A.3 C++: honor `NUBatch` when present.**
  In `grpc-server.cpp` `params_parse`, after `:519`, add:
  ```cpp
  if (request->nubatch() > 0) {
      params.n_ubatch = request->nubatch();
  }
  ```
  so an explicit physical ubatch wins over the `n_batch` alias, with the `ubatch`
  string-option as a third path for users who only edit `options:`.

- **A.4 Go: config surface + plumbing.**
  - Add `UBatch *int` (yaml `ubatch`) to the llama config struct alongside `Batch`
    (search `core/config` for the `Batch` field; mirror it).
  - In `core/backend/options.go`: add `EffectiveUBatchSize(c)` mirroring
    `EffectiveBatchSize` (return `c.UBatch` if set, else
    `min(EffectiveBatchSize(c), BlackwellBatchSize-or-512)` so the physical ubatch
    stays at the hardware sweet spot while `n_batch` may be larger). Set
    `NUBatch: int32(EffectiveUBatchSize(c))` next to `NBatch:` (`:228`).
  - Keep the default such that when neither is set, `NUBatch == NBatch` ⇒
    byte-identical to today.

- **A.5 Serving default (the lever).**
  In `hardware_defaults.go`, introduce `BlackwellLogicalBatch = 4096` (or a
  measured value) and let `EffectiveBatchSize` return it for **multi-slot serving**
  configs (when `n_parallel > 1` and the model is a completion model), while
  `EffectiveUBatchSize` stays at `BlackwellBatchSize = 2048`. Gate behind the same
  Blackwell detection already used at `:37-40`. Single-stream/embedding/rerank
  paths keep `n_batch == n_ubatch`. This is the only behavioral change shipped by
  Phase A; Phase 0.2 must show it is net-positive before defaulting it on.

- **A.6 Tests.** Extend `hardware_defaults_internal_test.go` with
  `EffectiveUBatchSize` cases; add a `grpcModelOpts` test asserting
  `NUBatch <= NBatch` and that unset config yields `NUBatch == NBatch`. Re-run
  0.1 (must still be token-identical) and 0.2 (must show aggregate-prefill gain or
  neutral ITL) at `n_batch=4096, n_ubatch=2048`.

### Phase B — Decode-headroom prefill cap (optional policy, vendored patch)
Only if Phase 0.2 / A shows decode ITL jitter from fat prefill chunks. This is the
one change that touches the inherited scheduler, so it lives as a patch in
`backend/cpp/llama-cpp/patches/` (applied by `prepare.sh:6-11` / Makefile
`:141-145`), never as an edit to a checked-in upstream file.

Policy (pseudocode; insert into `update_slots()` prefill fill loop, the
`while (… && batch.n_tokens < n_batch)` at ≈ `server-context.cpp:2552`):

```
# token budget for THIS iteration, decode already seated:
n_decode_in_batch = batch.n_tokens            # set after the decode phase
prefill_budget    = n_batch                    # default == today

if serving_mode and n_decode_in_batch > 0:
    # leave room so decoders are not starved/jittered by one giant prefill chunk
    # max_prefill_per_iter defaults to n_ubatch (one physical tile) when decode active
    prefill_budget = min(n_batch, n_decode_in_batch + max_prefill_per_iter)

# fill loop guard becomes:
while slot.prompt.n_tokens() < slot.task->n_tokens()
      and batch.n_tokens < prefill_budget:
      ...
```

- `max_prefill_per_iter` is a new `common_params` field surfaced as an
  `options:` knob (`max_prefill_tokens` / `mpt`) parsed in `grpc-server.cpp`
  exactly like A.1, default `0` = disabled = today's behavior.
- Semantics mirror vLLM `long_prefill_token_threshold`: cap the prefill share so
  ongoing decodes keep a steady cadence; the remaining prompt rides the next
  iteration (already supported by the state machine — slot stays
  `PROCESSING_PROMPT`).
- **Correctness:** unchanged KV/position path — chunk boundaries already advance
  `slot.prompt.tokens.pos_next()` per added token (≈ 2570) and the slot resumes
  from `slot.prompt.n_tokens()` next iteration. Capping the budget only changes
  *how many* tokens are added this iteration, not *which* positions, so 0.1 must
  remain token-identical.

### Phase C — Docs + defaults rollout
- Document `batch` / `ubatch` (and `max_prefill_tokens` if B ships) in
  `docs/content/` model-config reference, with the serving recipe
  (`n_parallel>1`, `n_batch=4096`, `ubatch=2048`).
- Note the orthogonality to paged KV (below) in
  `PHASED_VLLM_PARITY_PLAN.md` Phase 3.

---

## 4. Risk / correctness

- **KV-cache & positions across chunks:** already handled upstream. Each prefill
  token added advances `pos_next()` (≈ 2570) and is pushed to `slot.prompt.tokens`
  (≈ 2573); the next iteration resumes from `slot.prompt.n_tokens()`. Chunk
  boundaries are transparent to the KV cache because positions are absolute, not
  per-chunk. Phase A changes only budgets, not positions; Phase B changes only the
  per-iteration count. The 0.1 token-identical test is the guardrail.
- **Unified KV cache (LocalAI default, `n_parallel` slots share one cache):**
  unaffected — co-batching prefill+decode across slots is what the unified cache is
  for; positions are per-`seq_id` (`{ slot.id }` in `common_batch_add`).
- **`n_ubatch > n_batch`:** invalid; A.4 clamps `EffectiveUBatchSize <=
  EffectiveBatchSize` and A.1 logs a warning if options violate it.
- **Embeddings / rerank:** must keep `n_ubatch >= prompt length` (single pass,
  `can_split()==false`). The existing `:519` alias + `EffectiveBatchSize`
  context-sizing for single-pass usecases (`options.go:119-124`) must be preserved
  — do not let the serving `BlackwellLogicalBatch` default leak into single-pass
  configs (A.5 gates on completion + `n_parallel>1`).
- **Turboquant fork:** the fork lacks some `common_params` fields (see
  `LOCALAI_LEGACY_LLAMA_CPP_SPEC` precedent at `grpc-server.cpp:755`). `n_batch` /
  `n_ubatch` are ancient fields and safe; if Phase B adds `max_prefill_per_iter`,
  guard the new field behind a `#ifndef` like the checkpoint block does.

## 5. Orthogonality to paged KV (Phase 2)

Keep them independent. Paged KV (the `-kvp` / block-manager effort, draft #22569,
and `paged/`) changes **where** KV blocks live (allocation/utilization). Chunked
prefill / this decouple changes **how many tokens per iteration** the scheduler
batches (the `n_batch` budget and decode/prefill interleave). They compose: paged
KV raises the concurrency ceiling (more slots), the decouple widens the per-iter
scheduling window to feed those slots; neither touches the other's data structures.
The only contact point is `update_slots()` — if both ship a vendored patch to it,
land them as separate, ordered patches in `patches/` and keep the hunks disjoint
(paged touches allocation/seq_rm; chunked-prefill Phase B touches the prefill fill
budget).

---

## 6. Bottom line

- Chunked prefill + decode interleave: **already present and correct** on the
  pinned llama.cpp — verify (Phase 0.1), do not rebuild.
- Real work: the **n_batch/n_ubatch decouple** (Phase A) — small, additive,
  default-preserving — plus an **optional decode-headroom prefill cap** (Phase B)
  if measurements show ITL jitter. Both are LocalAI-side: A in `grpc-server.cpp`
  + proto + `options.go`; B as a vendored `patches/` hunk.
