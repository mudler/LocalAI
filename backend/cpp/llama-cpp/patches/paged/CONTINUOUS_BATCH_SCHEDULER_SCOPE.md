# Durable scope: token-granular continuous-batch scheduler for llama-server on GB10

Build-ready plan. **Not implemented in this workflow** (serving-loop rewrite). This
document scopes the durable path to give llama-server's `update_slots()` a vLLM-v1-style
token-granular continuous-batch scheduler, and records the single honest finding that
re-shapes what the change can and cannot buy.

Hardware: NVIDIA GB10 / DGX Spark (sm_121, CC=1210 = `GGML_CUDA_CC_DGX_SPARK`), unified
LPDDR5x ~273 GB/s. Models: dense Qwen3.6-27B NVFP4 (`~/bench/q36-27b-nvfp4.gguf`),
MoE Qwen3.6-35B-A3B NVFP4 (`~/bench/q36-35b-a3b-nvfp4.gguf`). Dev tree `~/llama-paged-dev`
(branch `paged`, HEAD `151343b`, patch 0015), `build-cuda` sm_121, `LLAMA_KV_PAGED=1`.
Scheduler code: `tools/server/server-context.cpp::update_slots()` (LocalAI override that
`#include`s it: `backend/cpp/llama-cpp/grpc-server.cpp`).

## TL;DR (the honest reframe)

Three findings, read directly from the source at HEAD `151343b` and from the committed
NVFP4 re-run (`QWEN36_NVFP4_BENCH.md`), collapse the apparent size of this work and reset
what it is allowed to claim:

1. **The unified mixed batch already exists.** `update_slots()` already builds ONE
   `llama_batch` per step = {every ready decode token} **+** {a bounded chunk of prefill
   tokens}, in a fixed two-phase order: Phase 1 (lines 2604-2719) appends every
   `SLOT_STATE_GENERATING` slot's sampled token **unconditionally** (no budget gate), then
   Phase 2 (lines 2753-3330) fills the remaining batch capacity with prompt tokens. Decode
   is therefore **already claimed first and never dropped or capped** - the exact property
   vLLM's "RUNNING-before-WAITING" pass works to guarantee is **free** here by construction.

2. **The chunked-prefill slot state already exists and already persists across steps.** A
   slot in `SLOT_STATE_PROCESSING_PROMPT` with `slot.prompt.n_tokens() < slot.task->n_tokens()`
   is a partial prefill; it stays in that state and resumes next step until its prompt is
   fully ingested, at which point it flips to `SLOT_STATE_DONE_PROMPT` -> `GENERATING`
   (line 3252, then 3502). Multiple slots can be `PROCESSING_PROMPT` and `GENERATING`
   simultaneously; there is **no global "one prefill at a time" gate**. So the mission's
   "allow a slot to be mid-prefill while others decode in the same step" is **not a state
   machine to build - it is already the behaviour.** This is the single biggest de-risking
   fact in this document.

3. **What is genuinely missing is the budget POLICY, and it is small.** Patch 0013
   (`LLAMA_PREFILL_BUDGET`) is a single **static** per-step prefill cap, consumed greedily by
   slots in iteration order. It is not decode-load-aware (does not subtract the live decode
   count `D`), not adaptive (one constant across npl 8..128), and not fair (the first
   `PROCESSING_PROMPT` slot can eat the whole budget). The durable delta is to convert that
   static cap into vLLM's **dynamic, decode-first, per-slot-fair token budget**: one total
   per-step budget `T`, decode claims its `D` tokens first, prefill gets the **leftover**
   `T - D` distributed across waiting prompts with a per-slot cap. That is ~the only
   behavioural change. **No new slot states, no batch-formation rewrite.**

### The honest ceiling (this is load-bearing for how the work is scoped and sold)

The committed re-run and a dedicated profiling pass (`QWEN36_NVFP4_BENCH.md`, plus
`~/bench/stag_128.json`) establish that **the residual ~2.4x high-concurrency decode gap is a
decode-KERNEL batch-scaling ceiling, not a scheduler defect**:

- At npl8 the kernels are **at parity** (dense 99%, MoE 84% of vLLM decode).
- A clean staggered full-batch-128 run, with **all 128 slots cleanly decoding and zero
  prefill starvation**, still tops out at **decode_agg 157.4 tok/s** (dense) - the same
  ~157-161 ceiling that four independent measurements converge on. vLLM does **390.7** at the
  same effective batch. With a *perfect* scheduler the kernel still gives ~157. **The
  scheduler cannot lift this.**
- Patch 0013 budget-256 **already reaches ~161** (the ceiling) at npl128. So a token-granular
  scheduler buys **little additional steady-state decode_agg** over 0013 on the all-at-once
  workload.

Therefore this scheduler's deliverable is **NOT "match vLLM's 391/811 decode."** It is:

- **Close the 12x TTFT gap** (dense 305 s @ 0013 / 491 s stock -> vLLM's ~25 s, and ~2 s on
  staggered arrival) - the genuine, large win.
- **Robustly HOLD the decode ceiling** (~161 dense / ~333 MoE @npl128) **without
  per-workload budget tuning** - 0013 needs a hand-picked constant (256 for dense, costs MoE
  TTFT, net-negative at low npl); the dynamic `T - D` budget is self-tuning across the whole
  npl range and across dense vs MoE.
- **Burst-robustness**: bounded TTFT for *all* concurrently-arriving prompts (kill the
  burst-TTFT spread), and no admission collapse under sustained load.

Closing the residual 2.4x decode-throughput gap is a **separate, named lever**: the
paged-attention **decode-kernel** batch-scaling work (patches 0009-0011 territory) and/or
CUDA-graphed decode. It is called out explicitly in P3 and is **out of this scope's
scheduler mandate**. We must measure and sell this work on **TTFT + burst-robustness +
self-tuning hold of the ceiling**, never on a decode_agg number the kernel forbids.

## The gap, precisely localized (recap of the committed bench)

At matched NVFP4 on one GB10 box (`QWEN36_NVFP4_BENCH.md`), llama (patch 0015) vs vLLM 0.23.0,
decode_agg tok/s | TTFT mean, npl swept 8/32/64/128:

| npl | dense llama (0013 b256) | dense vLLM | MoE llama (0013 b256) | MoE vLLM |
|----:|------------------------:|-----------:|----------------------:|---------:|
| 8   | 63.5  / 4.3 s   | 64.3  / 2.6 s | 169.3 / 1.7 s  | 202.0 / 0.8 s |
| 32  | 105.7 / 23.1 s  | 189.8 / 7.5 s | 239.0 / 9.0 s  | 462.0 / 2.3 s |
| 64  | 132.0 / 109 s   | 284.2 / 13 s  | 277.0 / 16.2 s | 624.5 / 4.1 s |
| 128 | **161.2 / 305 s** | 390.7 / 24.8 s | **333.5 / 98 s** | 811.1 / 8.0 s |

Both models converge to the **same ~41% of vLLM decode at npl128** after 0013. That
convergence is the signal: once prefill starvation is removed, a dense model and a
12x-cheaper-prefill MoE land on the **identical** ceiling -> the residual is **not prefill**
and **not the kernel-at-parity-@npl8** - it is the **quality of the per-step batching
decision** (TTFT/robustness) plus the **kernel decode ceiling** (the throughput residual).
This scope addresses the first; it names the second as the separate lever.

## What already exists (reuse, do NOT rebuild)

All line numbers verified at `tools/server/server-context.cpp` HEAD `151343b`.

- **[A] decode-first co-batch** - Phase 1, lines 2604-2719. Iterates `slots`; every
  `SLOT_STATE_GENERATING` slot (gated only by `can_batch_with`, line 2611) is pushed to
  `generating[]`; line 2715-2719 `for (slot : generating) slot.update_batch(batch)` appends
  its sampled token (+ draft tokens) via `common_batch_add`. After this loop,
  `batch.n_tokens == D` (the decode-token count). **No budget gate** - decode always goes in.
- **[B] chunked-prefill state per slot** - the pair `slot.prompt.n_tokens()` (=
  `num_computed_tokens`) vs `slot.task->n_tokens()` (= `num_tokens`). A `PROCESSING_PROMPT`
  slot with `prompt.n_tokens() < task->n_tokens()` resumes next step (Phase 2 re-enters it).
  Transition to `DONE_PROMPT` at line 3252 when the prompt is exhausted; to `GENERATING` at
  line 3502. **This is exactly vLLM's "leave the request in `running`, advance
  `num_computed_tokens` next step" - already implemented.**
- **[C] single shared batch + compute chunking** - one `llama_batch` holds decode+prefill;
  the compute loop (lines ~3366-3378) `for (i=0; i<batch.n_tokens; i+=n_tokens){ n_tokens =
  min(n_batch, batch.n_tokens-i); llama_decode(batch_view); }` runs it as one `llama_decode`
  when `batch.n_tokens <= n_batch`; `n_ubatch` (512) splitting happens inside `llama_decode`.
- **[D] patch 0013 static prefill budget** - the thing to supersede. Read once at lines
  2737-2747 (`n_prefill_budget = min(n_batch, atoi(LLAMA_PREFILL_BUDGET))`, a CONSTANT for
  the run); enforced as an extra `while` predicate at line 3188 (`n_prompt_budgeted <
  n_prefill_budget`), counter at 3214, outer break at 3326. `0` = disabled = byte-identical
  stock.
- **[E] productization seam** - `backend/cpp/llama-cpp/grpc-server.cpp` lines 781-791 parse
  the model option `max_prefill_tokens` / `mpt` / `prefill_budget` and `setenv`
  `LLAMA_PREFILL_BUDGET` before context init (same pattern as `kv_paged`). New knobs hang off
  this seam identically.
- **[F] paged KV (patches 0001-0011)** - on-demand block allocation keyed by sequence
  position. Batch formation only changes **which** tokens are in a step; paged alloc is
  driven by the per-slot sequence positions, which are unchanged. Orthogonal (see Correctness).

## vLLM v1 reference algorithm (the target, for fidelity)

From `vllm/v1/core/sched/scheduler.py::schedule()` (0.23.0, on the box). The unifying idea:
there is no prefill phase vs decode phase. Every request advances `num_computed_tokens`
toward `num_tokens` by up to N this step; for a decoder N=1, for a prefiller N=remaining
prompt. One per-step `token_budget = max_num_batched_tokens` bounds the TOTAL (decode +
prefill). Pass 1 visits `running` first (decoders cost 1 each -> all decode claimed before
any prefill is sized); Pass 2 admits `waiting` (new prompts) only with leftover budget, each
chunked by `min(remaining_prompt, long_prefill_token_threshold, leftover_budget)`. Caps:
`max_num_seqs` (concurrent sequences), `long_prefill_token_threshold` (~4% of max_model_len,
per-request prompt-chunk cap so one giant prompt cannot monopolize a step). Net: decode batch
maximal every step (-> the GEMM-batching throughput vLLM gets), prefill always makes bounded
progress (-> low, flat TTFT), one `model.forward()` per step.

The mapping to llama is clean because [A]+[B] already give us "running visited first" and
"prefiller resumes next step." We are missing only: **one total budget `T`, leftover `T - D`
sizing, and the per-request chunk cap with fair distribution.**

## The unified per-step batch-formation algorithm (the design)

New knobs (all default to current behaviour; env set before context init like `LLAMA_KV_PAGED`):

- `T` = `LLAMA_MAX_BATCH_TOKENS` (option `max_batch_tokens` / `mbt`) - total per-step token
  budget (decode + prefill), the analogue of `max_num_batched_tokens`. Default `n_batch`
  (2048). Clamped `T = min(T, n_batch)` so the existing single-`llama_decode` chunking is
  unchanged.
- `PREFILL_CAP` = `LLAMA_PREFILL_CAP` (option `prefill_cap`) - per-slot max prompt tokens per
  step, the `long_prefill_token_threshold` analogue. Default `min(T, ceil(0.04 * n_ctx))`,
  floored at `n_ubatch` (512) so a single prompt still makes a full ubatch of progress.
- Back-compat: if only the legacy `LLAMA_PREFILL_BUDGET` is set (new knobs unset), behave
  exactly as 0013 (static cap) - 0013 is the degenerate `T = n_batch`, no-leftover case.

Pseudocode, mapping to real variables and seams (the `>>` lines are the change vs today):

```
common_batch_clear(batch);                                  // line 2594

// PASS 1 - DECODE FIRST (unchanged: lines 2604-2719)
for (slot : slots) if (slot.state == GENERATING && can_batch_with) generating.push(slot);
... speculative draft ...
for (slot : generating) slot.update_batch(batch);           // appends decode (+draft) tokens

>> D = batch.n_tokens;                                       // NEW seam: decode load is now final (after 2719)
>> T = min(LLAMA_MAX_BATCH_TOKENS ? : n_batch, n_batch);
>> prefill_budget_step  = max(0, T - D);                     // DYNAMIC leftover, auto-shrinks with D
>> prefill_cap_per_slot = PREFILL_CAP;                       // long_prefill_token_threshold analogue
>> n_prompt_budgeted    = 0;                                 // total prompt tokens added this step (subsumes 0013)

// PASS 2 - PREFILL FILLS THE LEFTOVER (lines 2753-3330, budget made dynamic + per-slot fair)
if (cont_batching || batch.n_tokens == 0) {
>>  for (k = 0; k < n_slots; ++k) {                          // round-robin start offset (fairness, see P2)
>>      slot = slots[(rr_start + k) % n_slots];
        if (!slot.is_processing() || !can_batch_with) continue;
        if (slot.state == STARTED) slot.state = PROCESSING_PROMPT;     // line 2782 (unchanged)
>>      slot_prompt_added = 0;                               // NEW: per-slot chunk counter (reset each slot)
        // inner prompt-fill (lines 3187-3239), guard now triple-bounded:
        while (slot.prompt.n_tokens() < slot.task->n_tokens()
>>             && batch.n_tokens   < T                       // was: < n_batch
>>             && n_prompt_budgeted < prefill_budget_step    // was: 0013 static n_prefill_budget
>>             && slot_prompt_added < prefill_cap_per_slot) {// NEW: per-slot cap -> fair distribution
            common_batch_add(batch, cur_tok, pos_next, {slot.id}, need_embd);
            slot.prompt.tokens.push_back(cur_tok);
            slot.n_prompt_tokens_processed++;
            n_prompt_budgeted++; slot_prompt_added++;
            ... checkpoint-boundary breaks (unchanged) ...
        }
        if (slot.prompt.n_tokens() == slot.task->n_tokens()) slot.state = DONE_PROMPT;  // line 3252
        ... checkpoint creation (unchanged) ...
>>      if (batch.n_tokens >= T) break;                      // was: >= n_batch (line 3320)
>>      if (n_prompt_budgeted >= prefill_budget_step) break; // was: 0013 break (line 3326)
    }
}

for (i=0; i<batch.n_tokens; i+=n) { n=min(n_batch,batch.n_tokens-i); llama_decode(view); }  // unchanged
```

The whole change is: (a) compute `prefill_budget_step = T - D` at the new seam after line
2719 instead of reading a static env constant at 2737; (b) bound the inner/outer loops by `T`
and the dynamic budget instead of `n_batch` and the static budget; (c) add `slot_prompt_added`
with `prefill_cap_per_slot` for per-slot fairness; (d) a round-robin start offset so the same
early slots do not always win the leftover.

**Why this holds the decode ceiling without tuning.** `T` bounds total tokens per step ->
bounds step compute time -> decode steps fire at a steady high rate (high decode-steps/sec).
As decode load `D` rises, `prefill_budget_step = T - D` auto-shrinks, so prefill never inflates
the step beyond `T` even at npl128. This is the mechanism by which 0013's hand-tuned 256
reaches 161; here it is reached **automatically across the npl range** because the budget is
`T - D`, not a constant. **Why this closes TTFT.** Prefill always gets a non-zero leftover
(`prefill_budget_step >= 0`, and `T` is sized so leftover > 0 until the box is fully decode-
saturated), distributed across waiting prompts by `prefill_cap_per_slot`, so every prompt makes
bounded progress every step instead of waiting for a dedicated prefill burst.

## Slot state machine changes (minimal - this is the headline de-risk)

**No new states. No state-transition rewrite.** The existing 6-state machine
(`IDLE / WAIT_OTHER / STARTED / PROCESSING_PROMPT / DONE_PROMPT / GENERATING`, lines 67-72)
already encodes everything:

- "mid-prefill while others decode" = a `PROCESSING_PROMPT` slot coexisting with `GENERATING`
  slots in the same step. **Already happens** (Phase 1 and Phase 2 populate one batch).
- "chunked-prefill state per slot" = `(state == PROCESSING_PROMPT) && (prompt.n_tokens() <
  task->n_tokens())`. **Already persisted** across `update_slots()` calls; Phase 2 re-enters
  the slot and resumes from `prompt.n_tokens()`.

The only **additions** are per-step scheduler scratch, not slot lifecycle state:

1. `slot_prompt_added` - a per-slot, per-step counter (local to the Phase-2 loop body), for
   the per-slot chunk cap. Not stored on the slot across steps.
2. A `rr_start` round-robin offset (one `size_t` on the server, advanced each step) so the
   leftover budget is distributed fairly across `PROCESSING_PROMPT` slots rather than always
   draining the lowest-index slot first (this is what kills the burst-TTFT *spread* - without
   it, slot 0's prompt finishes first every time and the last slots starve).
3. Optional, P2: a per-step admission cap `K` on how many `STARTED -> PROCESSING_PROMPT`
   transitions begin in one step. This falls out of the budget arithmetic already (a bounded
   `prefill_budget_step` with a per-slot floor admits only `~budget/floor` prompts/step), so it
   may need no explicit code; if made explicit it is the `max_num_seqs`-style "don't admit a
   new prefill if the step is full" guard, mapped onto the pre-allocated `n_parallel` slots.

That is the entire state-machine footprint: two pieces of per-step scratch and an optional cap.
The mission's feared "slot-state rewrite" does not materialize.

## How it supersedes / subsumes patch 0013

| property | 0013 (static cap) | this scheduler (dynamic `T - D`) |
|----------|-------------------|----------------------------------|
| per-step prefill bound | constant `n_prefill_budget` | `T - D`, shrinks as decode load rises |
| decode-load aware | no (ignores `D`) | yes (leftover after decode) |
| works across npl 8..128 with one config | no (256 best @128, net-negative @8) | yes (self-tuning) |
| fair across multiple waiting prompts | no (greedy, slot 0 wins) | yes (`prefill_cap_per_slot` + round-robin) |
| TTFT on bursty arrival | raises it (defers first tokens) | bounded for all prompts |
| decode-first guarantee | structural (Phase 1) | structural (Phase 1) - **kept** |

0013 is the **degenerate case** `T = n_batch` with `prefill_budget_step` pinned to a constant
and no per-slot cap. The patch keeps `LLAMA_PREFILL_BUDGET` working for back-compat (when the
new knobs are unset). When `LLAMA_MAX_BATCH_TOKENS` is set, the static path is replaced by the
dynamic one. **Default (all knobs unset) = byte-identical stock**, exactly like 0013.

## Correctness

- **KV cache during chunked prefill** - unchanged from today. A `PROCESSING_PROMPT` slot already
  advances `slot.prompt.tokens` / `pos_next()` chunk by chunk across steps; we only change the
  chunk SIZE per step, not how positions or sequence ids are assigned. `common_batch_add`
  receives the same `(tok, pos, {slot.id})` tuples in the same order. No new KV state.
- **Determinism** - greedy (temp 0) output can differ from a single-`n_batch`-chunk run only by
  the **intrinsic flash-attn chunk-size FP grouping** that 0013 already documented and bounded:
  pure stock `-b256` diverges from `-b2048` the same way with this patch inactive; output stays
  coherent and answers correctly. The op-level math per token is position-determined and
  unchanged; only the FA reduction grouping over a step's token mix shifts. The deterministic
  oracle is the CPU backend / the op test (bit-exact); the GB10 CUDA greedy-decode band applies
  to end-to-end only, never to the op test.
- **Paged KV (patches 0001-0011)** - **orthogonal**. Paged on-demand block allocation is keyed
  by sequence position and slot/stream, which this change does not touch; it changes only which
  tokens are in a given `llama_decode`. The in-kernel paged decode read (0009-0011) operates
  per-token via the block tables regardless of what prefill tokens are co-batched. Required gate:
  run the full P0-P2 suite with `LLAMA_KV_PAGED=1` **and** `=0` and confirm **identical
  scheduling decisions** (same per-step token counts, same admission order) - paged must be a
  no-op on the scheduler.
- **`can_batch_with` constraint** (line 302) - a batch admits only slots with the same
  `task->type` and equal LoRA. Homogeneous-completion serving (the benchmark and the dominant
  LocalAI case) satisfies it, so the mixed decode+prefill batch forms freely. Mixed task types /
  per-request LoRA fall back to separate batches - a pre-existing bound, not a regression; note
  it, do not try to lift it here.
- **Checkpoint interaction (a real, orthogonal serving defect to account for)** - each slot that
  reaches `DONE_PROMPT` may call `create_checkpoint` (line 2147), ~149 MiB per checkpoint on the
  dense 27B, gated by `n_ctx_checkpoints > 0` (line 3133). Profiling found that under sustained
  heavy load the checkpoint subsystem **thrashes**: admission collapsed to one slot every ~13 s,
  zero decoding for 290 s, while `/slots` itself serialized behind a 13 s `update_slots` step.
  This is **independent** of the decode/prefill mix but it **masks** the scheduler's win if left
  on. **P0 must isolate it** (run with `n_ctx_checkpoints=0`), and **P2's admission decision
  should be checkpoint-cost-aware** on the 128 GB unified box (do not admit a fresh prefill whose
  checkpoint would thrash the pool). Treat as a named co-defect, not part of the core batching
  change.

## Phased plan P0 -> P3 (work, payoff, files, risk)

| Phase | Work | Expected payoff (dense / MoE @npl128 unless noted) | Files | Risk |
|-------|------|-----------------------------------------------------|-------|------|
| **P0** baseline + metrics harness | Per-step effective-decode-batch poller (`/slots`), TTFT percentiles (p50/p90/p99/max), `decode_agg` over the fully-overlapped window, decode-ITL (worst freeze / median), **step-time histogram**, admission rate (slots/s reaching GENERATING), checkpoint-event log. Lock the staggered-arrival ceiling (**157.4** dense, all-128 clean) and the all-at-once burst pathology as the two reference traces. Isolate checkpoints (`n_ctx_checkpoints=0`). | dev-tree only: `~/bench/` (reuse `stag.py`, `slot_poll.py`, `h2h_cli.py`, `h2h_moe_sweep.sh`; `stag_128.json`, `h2h_real128b.json`) | **None** (gate). Locks correctness + the 157/333 ceiling so any regression is caught. | Low |
| **P1** unified mixed-batch formation | Replace the static budget read (2737-2747) with the **dynamic `T - D`** computed at the new seam after line 2719; bound the inner/outer Phase-2 loops by `T` (3188, 3320) and `prefill_budget_step` (3326) instead of `n_batch` and the static cap. No per-slot cap, no round-robin yet (that is P2). | `tools/server/server-context.cpp` (seam @2719, knob read, 3188, 3320, 3326); mirror to `0016-paged-continuous-batch-scheduler.patch` | **TTFT**: removes the burst penalty 0013 inflicts - staggered TTFT ~2 s, burst TTFT collapses toward vLLM's ~25 s / 8 s. **Decode**: holds the ceiling **(~161 / ~333)** *without per-workload tuning* (0013 needed 256 hand-picked). No new throughput beyond the ceiling - by design. | Low-Med (loop-bound edits in a hot path; default-off gate makes it byte-identical stock) |
| **P2** scheduling policy / fairness | Add `slot_prompt_added` + `prefill_cap_per_slot` (the `long_prefill_token_threshold` analogue) and the **round-robin start offset**; optional explicit per-step admission cap `K` + checkpoint-cost-aware admission. Tune `T`, `PREFILL_CAP` on GB10 (dense vs MoE, npl 8/32/64/128). | `server-context.cpp` (Phase-2 loop body @2753-3330, server-level `rr_start`); `grpc-server.cpp` (options `max_batch_tokens`/`mbt`, `prefill_cap` @781-791) | **TTFT spread**: bounds first-token latency for **all** concurrently-arriving prompts (kills the burst-TTFT spread, e.g. dense max 305 s -> single-digit-s on staggered, bounded on burst). **Robustness**: no admission collapse under sustained load; decode batch stays maximal so the *time-averaged* decode_agg on real (non-burst) traffic rises toward the staggered 157/333 because slots reach GENERATING fast. | Med (fairness + admission logic; e2e coherence + A/B vs 0013 required) |
| **P3** residual decode throughput | **Honest boundary: this is the decode-KERNEL lever, NOT the scheduler.** The scheduler has delivered TTFT + robustness + ceiling-hold. Closing the residual 2.4x (161 -> 391 dense, 333 -> 811 MoE) requires paged-attention **decode-kernel** batch-scaling (patches 0009-0011 territory) and/or **CUDA-graphed decode** (the now-uniform decode-only step is graph-capturable). Scope/track separately. | (separate scope) `ggml/src/ggml-cuda/` decode-read kernels; optional CUDA-graph capture seam in `update_slots` | This is **where 391/811 would come from**; it is **out of this scope's mandate** and must not be charged against the scheduler. The scheduler makes the decode step uniform (a precondition that *helps* a future graph capture). | High (kernel work; the GB10 occupancy wall, see below) |

**Per-phase payoff vs the mission targets (TTFT 25 s / 8 s, decode 391 / 811 @npl128):**

- **TTFT 25 s / 8 s** - reached by **P1 + P2** (the 12x gap is the scheduler's to close; on
  staggered arrival it goes below the vLLM burst figure to ~2 s).
- **Decode 391 / 811** - **NOT a P1/P2 deliverable.** P1/P2 hold **161 / 333** (= ~41% of vLLM,
  the kernel ceiling) robustly and tuning-free. The remaining ~2.4x is **P3 kernel**, a separate
  lever. Pre-registering this split is the point: the scheduler is judged on TTFT + holding the
  ceiling, the kernel on the throughput residual.

## GB10 considerations

- **Bandwidth floor ~273 GB/s** is the *cause* of the decode ceiling (NVFP4 weight-read +
  paged-KV gather per step). The scheduler cannot lift a bandwidth/kernel floor - it can only
  keep the batch *at* the ceiling. Size `T ~= n_batch` (2048) so the compute step stays a single
  `llama_decode`; `n_ubatch` (512) governs the internal split.
- **`T` is the ITL/TTFT trade knob** (vLLM's `max_num_batched_tokens`): larger `T` = more
  prefill/step = faster TTFT but bigger per-step ITL spike; smaller `T` = smoother ITL, slower
  TTFT. Because the budget is `T - D`, the spike is bounded at `T` regardless of decode load.
  Default `T = n_batch`; expect to tune down toward ~1024 for ITL-sensitive serving.
- **Checkpoint ~149 MiB/slot thrash** on the 128 GB unified box - admission must be
  checkpoint-cost-aware (P2); P0 measures with checkpoints off to isolate the batching win.
- **Memory**: paged on-demand KV (dense 52->94 GB, MoE 39->61 GB across npl) vs vLLM's flat
  ~112 GB pre-reservation - llama's standing multi-tenant advantage, unaffected by this change.
- **Eager mode** both engines today; **CUDA-graphed decode** is the P3 kernel lever, and the
  scheduler's uniform decode-only step is a precondition that *helps* a future capture.

## Biggest risks and how to de-risk

1. **"Slot-state rewrite" (the feared big risk) = actually LOW.** The mid-prefill-while-others-
   decode state and the chunked-prefill resume already exist ([B]); we add only per-step scratch
   (`slot_prompt_added`, `rr_start`), not lifecycle states. **De-risk**: keep all 6 states
   untouched; gate every change behind the new knobs; default-off = byte-identical 0013/stock,
   verified by an A/B diff of per-step token counts.
2. **Correctness regression in the mixed batch = the FA chunk-grouping nondeterminism.** Already
   documented and bounded by 0013 (stock `-b256` vs `-b2048` diverge identically). **De-risk**:
   op-test bit-exact where deterministic; greedy-coherence e2e on both models; A/B vs 0013 with
   the new knobs set to reproduce 0013 (`T = n_batch`, no leftover) and confirm **byte-identical**
   to 0013.
3. **Paged-KV interaction = LOW (orthogonal positions).** **De-risk**: run the whole P0-P2 suite
   with `LLAMA_KV_PAGED=1` and `=0`; assert identical scheduling decisions (paged must be a
   no-op on batch formation). This is a hard gate, not a spot check.
4. **Checkpoint thrash masks the win = MEDIUM.** A real serving defect that can swamp the
   scheduler's signal. **De-risk**: P0 isolates it (`n_ctx_checkpoints=0`); P2 makes admission
   checkpoint-cost-aware; report the scheduler metrics both with and without checkpoints so the
   batching win is legible independent of the checkpoint co-defect.
5. **Honest-payoff risk = the decode_agg number barely moves over 0013 (kernel ceiling), so the
   work can be mis-judged as "no win."** This is the most important risk to manage. **De-risk**:
   frame and measure on **TTFT percentiles, burst-TTFT spread, step-time histogram, admission
   rate, and tuning-free ceiling-hold across npl/dense/MoE** - the axes the scheduler actually
   moves - and **pre-register the decode-kernel as the separate residual-closer** (P3) so the
   scheduler is never charged with the 391/811 number the kernel forbids.

## Commit / hygiene

Scope doc only (this file). **No engine change committed in this workflow.** Bench and parity
scripts stay dev-tree-only (`~/bench/`, `~/llama-paged-dev/benches/`). When P1/P2 are
implemented they mirror to `backend/cpp/llama-cpp/patches/paged/0016-paged-continuous-batch-
scheduler.patch` (next free slot after 0015) and the LocalAI option lands in `grpc-server.cpp`
beside `max_prefill_tokens`. Commit with `git commit -s`, trailer
`Assisted-by: Claude:opus-4.8 [Claude Code]`, no `Co-Authored-By`, no em-dashes. Do not push
(human pushes).

---

## Review / risk (adversarial, source-verified)

Skeptical staff review against the actual source at HEAD `151343b` (server-context.cpp,
llama-batch.cpp, llama-kv-cache.cpp, paged-*.cpp), grpc-server.cpp in this worktree, and the
committed `QWEN36_NVFP4_BENCH.md` plus the vLLM H2H serve logs/scripts on the box.

### Verdict: the scope is SOUND. GO on P0 -> P1, CONDITIONAL P2, separate-track P3.

The central de-risking claims check out against the code, and the load-bearing honesty (decode
residual is a kernel ceiling, not a scheduler defect) is correct and now further corroborated.
Two calibration fixes are required before P1 (below), neither changes the go decision.

### (1) Tractability - CONFIRMED bounded; zero libllama changes. What enables/blocks it, concretely:

- **Enables (already-exercised path, not new surface).** A mixed prefill+decode ubatch with
  per-seq different `n_past` is the *existing* behaviour. `llama_batch` carries per-token `pos`
  and `seq_id` (`common_batch_add(batch, tok, pos_next(), {slot.id}, ...)`); `llama_kv_cache` +
  `paged_alloc::place()` place each `(seq, pos)` independently; `llama_kv_cache::init_batch`
  (line 742) already splits the mixed batch into ubatches. **The server emits exactly this mixed
  decode+prefill batch today** - patch 0013 ships it and produces coherent output - so the new
  scheduler changes only the *count* of prefill tokens, never the batch *structure*. There is no
  `llama_decode`/ubatch/KV rewrite in scope.
- **Blocks: nothing in libllama.** The only constraints are pre-existing and orthogonal to the
  target workload: (i) `can_batch_with` (same task type + equal LoRA per batch); (ii)
  `split_equal(sequential=true)` errors on *coupled* sequences (shared-prompt parallel sampling),
  forcing `-kvu`. Neither is introduced by this change.
- **Correction to fold in:** the scope's [C] and the pseudocode imply contiguous `split_simple`
  chunking. The real serving/benchmark config (`--parallel 128`, `kv_unified` default = `false`
  -> `n_stream = n_seq_max = 128`) takes the **`split_equal(n_ubatch, sequential=true)`** path
  (llama-kv-cache.cpp:742), which balances per-sequence rather than slicing contiguously. This
  does not break anything (0013 already hits it) but it means the actual scheduled object is a
  split_equal ubatch set; P0 must characterize that ubatch shape (not assume contiguous 512-chunks)
  and the determinism band is over split_equal groupings. Lock the split path (unified vs not) in
  the A/B so the byte-identical-to-0013 gate is meaningful. grpc seam [E] verified at
  grpc-server.cpp:761-786 (`kv_paged`, `max_prefill_tokens`/`mpt`); new `mbt`/`prefill_cap` knobs
  hang off it identically.

### (2) Does it close the gap - the 2.4x is NOT CUDA graphs, and the TTFT root is quantified.

- **CUDA graphs ruled out (verified).** Both NVFP4 H2H vLLM servers ran `--enforce-eager`
  (`h2h_dense_vllm.sh`, `h2h_moe_serve_vllm.sh`; engine logs show `enforce_eager=True`,
  `cudagraph_mode=NONE`, `CompilationMode.NONE`). So the npl128 2.4x decode gap is a genuine
  **eager-mode kernel + per-step host-overhead** gap (ggml graph rebuild/realloc + ~1k kernel
  launches per step on the weak Grace cores, paged-KV gather, MoE expert gather). The scheduler
  cannot touch it; the staggered all-128-decoding 157.4 tok/s ceiling is solid. Scope is right to
  refuse the 391/811 number. (CUDA graphs are a future *both-sides* lever, not the current cause.)
- **The TTFT gap has a measured root the scope under-uses: prefill_tps collapse.** From the bench,
  llama `prefill_tps` falls 1117 -> 752 -> 465 -> **125** (dense, npl 8/32/64/128) while vLLM holds
  **flat ~1420** (MoE: 2813 -> 657 vs vLLM flat ~4263). That collapse - not a separate "scheduling
  quality" abstraction - is the direct cause of the 491 s / 85 s TTFT, and it is exactly what the
  dynamic `T - D` budget attacks: when decode load `D` is low (early in a burst) the leftover
  `T - D` lets prefill take ~`n_batch` per step, and because llama's *larger per-step chunk*
  compensates for its ~2.4x slower steps, a `T = 2048` budget can sustain prefill_tps at or above
  vLLM's ~1420 during the drain. **So burst-TTFT parity is mechanically plausible, not just
  "toward"** - the static budget-256 throttles prefill to 256/step (hence its weak 305 s) where the
  dynamic budget would not. This strengthens P1's case beyond what the doc claims.
- **Mandatory calibration fix:** that TTFT win **couples to a decode-ITL knob**. Spending the full
  `T - D` on prefill during the drain makes those steps full `T`-token (mixed) computes, so
  co-batched decoders get 1 token per slow step (ITL spike) *during the drain* - precisely vLLM's
  tradeoff, navigated by `T`. The 157/333 ceiling is the **post-drain steady state**, not the
  drain phase. Therefore the scope must **co-report drain-phase decode-ITL alongside TTFT** and
  treat `T` as the published trade knob; reporting TTFT alone would hide the cost and reporting
  decode_agg alone would hide the win (it is averaged across drain + steady state, which is why it
  "barely moves"). Soften "P1+P2 reach 25 s / 8 s": the defensible claim is *staggered/realistic
  arrival ~2 s, and all-at-once burst approaching vLLM with a tunable decode-ITL cost*.

### (3) Correctness - paged orthogonality confirmed at source; the real risks are config, not code.

- **Paged-KV is the same `llama_kv_cache` class** with `paged_alloc::` hooks inside the existing
  find_slot/placement (llama-kv-cache.cpp:1043-1083), driven by per-slot `(seq, pos)` - which this
  change does not touch. `init_batch`/split is paged-agnostic. The scope's "orthogonal" claim is
  verified, not asserted. Keep the hard `LLAMA_KV_PAGED=1` vs `=0` identical-decisions gate.
- **Determinism**: the FA grouping nondeterminism is over **split_equal** ubatches in the real
  config; the `T = n_batch` A/B-must-be-byte-identical-to-0013 gate is the right oracle and is
  sound (default-off path is untouched).
- **Low-concurrency regression**: gated to byte-identical when knobs unset; the only live vector is
  a **mis-tuned `T`** spiking ITL at low npl (the scope already flags `T` defaults). Config hygiene,
  not a code risk. Add a guard/floor so `T` cannot be set below `n_ubatch`.

### (4) Smaller higher-ROI step - yes, and the scope already contains it (P1).

The minimal high-ROI change is **P1 alone**: replace the static read (server-context.cpp:2737-2747)
with `prefill_budget_step = max(floor, T - batch.n_tokens)` computed after the decode-fill at line
2719, and bound the Phase-2 loops by `T` / that budget (3188, 3320, 3326). That is a handful of
line edits at named seams, default-off, and it captures the self-tuning + the bulk of the TTFT win.
The even-smaller validation spike: a one-line `n_prefill_budget = max(floor, T - batch.n_tokens)`
to confirm the prefill_tps/TTFT mechanism before writing the full P1. **P2** (round-robin +
`prefill_cap_per_slot` + checkpoint-aware admission) is genuinely higher-effort and lower-marginal
(it buys TTFT *spread*/tail and burst robustness, not the median); **gate P2 on P1's measured
burst-TTFT-spread and drain-ITL**, do not commit to it up front. There is no smaller step that also
fixes the static budget's npl-dependence - tuning 0013's constant cannot (256 is net-negative at
npl8 and costs MoE TTFT), so P1 is the floor.

### Realistic effort / payoff and sequencing

- **P0** ~0.5-1 wk (harness largely exists in `~/bench/`): add drain-phase decode-ITL to the metric
  set, lock the split path, isolate checkpoints (`n_ctx_checkpoints=0`). Gate only.
- **P1** ~2-4 days: small diff + the A/B-vs-0013 byte-identical gate + the npl/dense/MoE sweep.
  Payoff: self-tuning hold of 161/333 with no hand-picked constant; burst-TTFT 3-10x better than
  0013 (plausibly approaching vLLM on the burst, parity on staggered), at a published `T`-tunable
  decode-ITL cost. **This is the high-ROI core and the clean supersession of 0013.**
- **P2** ~1-2 wk, conditional: fairness/admission + checkpoint-cost-awareness + tuning. Payoff: TTFT
  tail/spread + no admission collapse under sustained load. Worth it only if P1 metrics show a
  residual spread/robustness problem.
- **P3** separate track, high effort: the *only* path to 391/811 is the eager-kernel + per-step
  host-overhead residual. Highest-value probe is a **CUDA-graph capture of the steady-state
  pure-decode step** - but note this works *independent of the scheduler* (the all-128-decoding
  step is already fixed-shape today); the scheduler neither blocks nor specially enables it, so do
  not credit graphs to the scheduler. The scope's "uniform decode step is a precondition" is a mild
  over-claim; correct it to "graphs apply to the pure-decode steady state, which the scheduler does
  not change."

### Bottom line

GO. The work is correctly localized to `update_slots()` batch-formation policy, requires no
libllama changes (the mixed per-seq batch is the existing, shipping path), and supersedes 0013
cleanly. The honest ceiling is real and well-stated; the two fixes are (a) co-report drain-phase
decode-ITL with TTFT and stop selling/charging the decode_agg number, and (b) acknowledge the
`split_equal`/`n_stream=128` path in the determinism and ubatch-shape analysis. Sequence
P0 -> P1, measure, then decide P2; keep P3 (kernel/CUDA-graph) on its own track as the sole owner
of the 2.4x throughput residual.
