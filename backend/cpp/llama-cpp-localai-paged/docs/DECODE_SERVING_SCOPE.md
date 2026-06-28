# DECODE_SERVING_SCOPE - the continuous-serving decode gap

**Status: S1 + S3 IMPLEMENTED, GPU-validated, bit-exact, shipped as patches
0040 (S1) + 0041 (S3). S2 DROPPED (measured non-target). See the results block
below; the rest of this doc is the design/rationale those patches implement.**

## Results (GB10, measured)

Phase 0 confirmed host-bound: serving graph reuse **0% over ~5k steps** (layer-A
rebuilds every step), `hostproc` 3.44 ms/step vs 1.59 static - the +1.85 ms IS the
graph rebuild; `set_inputs` 0.047 ms and block-table 0.002 ms are negligible.

- **S1 (patch 0040)** - root cause: the paged decode inputs never overrode
  `can_reuse` (defaults false), so the graph could never be reused. Fixed with a
  256-bucketed-shape `can_reuse` + live-mctx refresh. Static batched-bench A/B:
  paged decode reuse **0% -> 95.5%**, bit-exact (md5 byte-identical reuse on/off).
  Necessary but **not** sufficient in serving (13.8% reuse alone - prefill
  co-batching churns the shape).
- **S3 (patch 0041)** - keeps prefill out of decode steps so the scheduler emits
  reuse-stable pure-decode steps. **S1+S3 together (128-client staggered serving,
  MoE Qwen3.6-35B-A3B-NVFP4): reuse 0% -> 72.2%, `hostproc` 15.98 -> 6.31 ms/step,
  decode 4.05 -> 5.52 tok/s/seq median (4.24 -> 5.96 mean, at vLLM's ~5.9).**
- **S2 (double-buffer set_inputs) - DROPPED.** Phase 0 put `set_inputs` at
  ~0.05 ms/step: it is not the cost (the rebuild is), so S2 has nothing to recover.
- **Follow-up to ~100% reuse - PADDED/FIXED-SLOT DECODE SHAPE: IMPLEMENTED,
  GPU-TESTED, REJECTED (not shipped).** See the "Padded-shape lever - rejected"
  block below. Summary: it does NOT close the serving gap. Padding holds the
  pure-decode width constant by emitting masked-inert dummy decodes for idle
  slots, and it is provably inert (single-seq md5 bit-exact + per-stream
  noise-floor determinism), but it **regresses throughput at every concurrency**
  (catastrophically at low load) because the serving decode here is
  **GPU-compute-bound, not host-rebuild-bound** - so the dummy-row compute it adds
  costs more than the graph-reuse it recovers. The original "remaining ~28% is
  request-boundary churn -> pad it" hypothesis stands mechanically, but the payoff
  premise (closing reuse pulls decode toward vLLM) is **not supported by
  measurement**.

---

## Padded-shape lever - rejected (implemented + GPU-tested, 2026-06-28)

The S1 section-(a) **padded / fixed-slot decode shape** was implemented in an
isolated worktree off the committed S1/S3/tail base (paged HEAD `05eceb4`), built
CUDA-only, and benched on GB10. **Verdict: REJECTED - it regresses serving
throughput and does not close the vLLM gap.** Recorded here so it is not re-tried.

**Implementation** (default-off, `LLAMA_PAGED_PAD_DECODE=1`; `LLAMA_PAGED_PAD_WIDTH`
caps the slot range): at the end of `pre_decode()`, on any step where no prompt
tokens were admitted (`n_prompt_budgeted == 0`) and there is decode load, emit a
masked-inert dummy decode for **every IDLE slot** (`batch.add(slot.id, 0,
pos_max+1, /*output=*/true)`; cold slot -> fresh pos-0). This holds `n_tokens`,
`n_seqs`, `n_seqs_unq`, `n_outputs` and the participating seq-id SET constant
across arrivals/completions. A `release()`-side guard keeps a finished slot warm
under padding (else patch 0024's reclaim-on-idle frees its KV and the next-step
pos-0 re-warm churns paged-block allocation, destroying reuse). Each dummy is its
OWN sequence, so its recurrent (gated-DeltaNet) state is private and its paged
attention reads only its own cells; its logits are computed but never read
(`post_decode()` only consumes `slot.i_batch` of GENERATING slots).

**Gates.** (1) Single-seq greedy md5 **bit-exact PASS** - dense
`5951a5b4d624ce891e22ab5fca9bc439`, paged-MoE `8cb0ce23777bf55f92f63d0292c756b0`
(the lever lives only in `llama-server`'s `update_slots()`, never in
`llama-completion`). (2) **Per-stream serving determinism**: the literal
"ON-vs-OFF token sequences identical" gate is **unachievable** - concurrent
cuBLAS/FA decode is **not bit-reproducible run-to-run** even with padding OFF
(OFF-vs-OFF diverging streams: dense 3/16, MoE 8/16, lockstep K=16). The
**achievable inertness gate PASSED**: per-stream prefix-agreement ON-vs-OFF equals
the OFF-vs-OFF noise floor exactly (MoE 0.940/0.940, dense 0.812/0.812), i.e. the
dummy slots inject no systematic divergence beyond the pre-existing concurrent FP
noise. So padding is provably inert; it just does not help.

**Bench (MoE Qwen3.6-35B-A3B-NVFP4, GB10).** Burst h2h, decode tok/s/seq:

| n   | S1+S3 | PAD  | vLLM |
|-----|-------|------|------|
| 8   | 28.16 | 6.05 | 44.8 |
| 32  | 11.66 | 4.84 | 17.45|
| 64  | 7.16  | 4.33 | 11.07|
| 128 | 4.53  | 4.32 | 6.87 |

Staggered (`serve_bench.py` k=128 n=160 stagger0.25), aggregate decode tok/s and
graph-reuse: baseline (reuse 0%) **757.6**; S1+S3 (reuse 72%) **763.3**; **PAD
(reuse 38%) 558.0**.

**Why it fails (four independent reasons):**

1. **Serving decode is GPU-compute-bound, not host-rebuild-bound (this run).**
   Baseline reuse 0% (757.6 agg) is statistically equal to S1+S3 reuse 72% (763.3
   agg): `hostproc` is only ~4-8% of the per-step wall, so eliminating the host
   graph rebuild buys ~nothing. (This **corrects the host-bound hypothesis** above
   for this hardware: the earlier 542->762 host-bound delta did **not** reproduce
   - it was GPU-state/contention variance, not a stable reuse effect.)
2. **Padding ADDS dummy-row compute** (full-width decode), costing throughput in
   direct proportion to `pad_width - real_load`: catastrophic at low concurrency
   (n=8: 28.16 -> 6.05, ~4.6x slower, because 8 real streams pay for a 128-wide
   step).
3. **In continuous serving padding can't even hold the width constant**: arrivals
   are perpetually mid-prefill, so the idle-slot count varies and reuse DROPS
   72% -> 38% (the opposite of the goal). It only stabilises the pure-decode
   *tail* of a burst (verified: width pinned at 64 as real decoders fell 49->5),
   which is exactly where the dummy compute is most wasteful.
4. **The completion-driven batch shrink that padding prevents is itself a
   throughput WIN** in a compute-bound regime (fewer real streams -> cheaper
   steps -> survivors finish faster); forcing constant width forfeits it.

**Conclusion.** The residual burst gap (paged 4.53 vs vLLM 6.87 at n=128 ~= 66%)
is a **GPU-compute** gap (vLLM's MoE decode kernel + scheduler are ~1.3x faster on
aggregate), not a host-loop gap. A host-side graph-reuse lever cannot close it.
Do not re-pursue padded/fixed-slot shapes for throughput; if the host loop is ever
re-confirmed dominant on other hardware (re-run reason 1's baseline-vs-S1+S3 A/B
first), revisit - but only with an *adaptive* width matched to live load, never a
fixed pad-to-`--parallel`.

---

Per the
"profile-don't-assume" rule in
[`.agents/vllm-parity-methodology.md`](../../../../.agents/vllm-parity-methodology.md),
**Phase 0 (section 5) is to confirm the bottleneck on GPU before touching any
code.** Everything below the Phase-0 line is a hypothesis ranked by
value/effort/risk, not a measured result.

> **Regime warning (read first).** Every "decode is at the BW floor / ties vLLM"
> and "host scheduling loop is the structural residual" conclusion in
> [`README.md`](../README.md) section 5 was measured with **`llama-batched-bench`**:
> a STATIC serving width (fixed `npl`, all sequences in lockstep, constant
> batch shape every step). That is the **decode KERNEL** regime, and there the
> patch series is at parity (paged ~6.1 tok/s/seq vs vLLM ~5.9 at npl128). This
> document is about a **different regime**: real **continuous SERVING** through
> `llama-server`'s `update_slots()` loop, where requests arrive and complete
> asynchronously, the batch shape churns every step, and paged drops to ~3.7
> tok/s/seq (-39%) while vLLM sustains ~5.9. The gap is the **scheduler / host
> loop**, not the kernel. This is the serving analogue of the prefill-GEMM regime
> split called out in [`PREFILL_GEMM_SCOPE.md`](PREFILL_GEMM_SCOPE.md).

Cross-links: [`README.md`](../README.md) sections 2 (scheduler), 3 (patches
0008/0013/0016/0024/0025/0029), 5 (rejected levers - lever 2 graph coverage was
FLAT *in the static regime*; this doc reopens it for the *serving* regime);
[`.agents/llama-cpp-localai-paged-backend.md`](../../../../.agents/llama-cpp-localai-paged-backend.md)
(bit-exact gate);
[`.agents/vllm-parity-methodology.md`](../../../../.agents/vllm-parity-methodology.md)
(both-engine ground-truth, per-lever A/B, record-rejected-levers).

---

## 1. The two regimes, and why the kernel-parity result does not carry over

`llama-batched-bench` and a real serving workload exercise the **same decode
kernels** but **different host loops**:

| | `llama-batched-bench` (kernel regime) | `llama-server` continuous serving |
|---|---|---|
| batch shape per step | **constant** (fixed `npl`, lockstep) | **churns** (arrivals/completions, interleaved prefill) |
| participating seq-set | **fixed** for the whole run | **changes** as requests start/finish |
| graph reuse (see s.2) | holds after warmup -> 1 capture, replayed | breaks nearly every step -> rebuild + re-capture |
| measured | paged ~6.1 tok/s/seq ~ vLLM ~5.9 | paged ~3.7 vs vLLM ~5.9 (-39%) |

The README's decode parity, BW-floor, and "host loop is the irreducible
residual" findings are all **kernel-regime** findings. They prove the *kernels*
are not the serving gap. They do **not** prove the host loop is irreducible *in
serving* - the static bench holds the batch shape constant, which is exactly the
condition that lets both graph-reuse layers (section 2) stay hot. Serving
violates that condition. So the serving gap is reopened here as a host /
scheduler problem, orthogonal to the kernel.

---

## 2. Root-cause hypothesis (from source, pin `9d5d882d` + the dev tree)

There are **two independent graph-reuse layers**, and continuous batching breaks
**both** on nearly every step. This is the leading hypothesis for the -39%.

### 2a. Layer A - llama-context graph reuse (`can_reuse` / `allow_reuse`)

`llama_context::process_ubatch` (`src/llama-context.cpp` ~L1366) only **reuses
the built ggml graph** when `res->can_reuse(gparams)` holds. `allow_reuse`
(`src/llama-graph.h` ~L631) requires, among others:

```
ubatch.n_tokens     == other.ubatch.n_tokens &&
ubatch.n_seqs       == other.ubatch.n_seqs   &&
ubatch.n_seqs_unq   == other.ubatch.n_seqs_unq &&
ubatch.equal_seqs() == other.ubatch.equal_seqs()
// + (when equal_seqs) the participating sequence-id SET must match
```

In serving, `n_tokens` changes whenever the decode load `D` changes or a prefill
chunk is co-batched, and the **sequence-id set** changes whenever a request
starts or finishes. Either makes `can_reuse` return false, so `process_ubatch`
falls into the `else` branch: **rebuild the graph** (`model.build_graph`) +
`ggml_backend_sched_reset` + `ggml_backend_sched_alloc_graph` - full host-side
graph construction + allocation, **every step**. In batched-bench all sequences
are lockstep so `n_tokens`/seq-set are constant and `can_reuse` is true after
warmup (the `graphs reused = N` perf line is ~all steps).

### 2b. Layer B - CUDA graph capture (`ggml_cuda_graph_*`)

Even when layer A reuses, the CUDA backend re-checks
`ggml_cuda_graph_update_required` (`ggml-cuda.cu` ~L3367): it `memcmp`s every
node's `ne`, `nb`, and `src[]->data` pointers against the captured graph. Any
shape change -> `cudaGraphExecUpdate` / re-instantiate. Two serving-specific
triggers:

- **shape churn** (same root cause as layer A): different `n_tokens` -> different
  node `ne` -> update required.
- **paged data-pointer churn**: when a co-batched prefill allocates new KV blocks
  (or a finished sequence frees them), the per-step KV view tensors' `data`
  pointers move, so even a constant-shape decode step can trip the `memcmp`. (The
  block-table *contents* live in a fixed device buffer filled by `set_inputs`, so
  the table tensor pointer itself is stable - 0029 keeps that cheap - but the K/V
  cache views are not.)

Net: under serving, the GPU sits idle between launches while the host rebuilds
the graph (layer A) and re-instantiates the CUDA graph (layer B), then runs an
un-graphed `set_inputs` (H2D input copies) before each launch. vLLM avoids this
with **padded/bucketed decode batch shapes + piecewise CUDA graphs**: it pads the
decode batch to a fixed set of sizes and captures one persistent graph per
bucket, so the steady-state decode step is a single `cudaGraphLaunch` with no
host rebuild. Its scheduler is also a tight C++ loop with chunked-prefill
interleave that keeps the GPU fed.

### 2c. Per-step host work that runs un-graphed regardless (already instrumented)

The dev tree carries a built-in `[L5INSTR]` profiler (`src/paged-attn.cpp`,
hooks in `src/llama-context.cpp` and `src/llama-kv-cache.cpp`) that already
isolates the host buckets we care about, printed at process exit:

```
[L5INSTR] get_block_table n=.. sum=..ms mean=..ms | set_inputs n=.. mean=..ms | hostproc n=.. mean=..ms
```

- `hostproc` = `mctx->apply()` + graph reuse-check/rebuild + `set_inputs`, i.e.
  the whole host window **before** `graph_compute` (it does NOT include the GPU
  launch). Prior profiles put this near ~1.4 ms/step.
- `set_inputs` = the H2D input fills (positions, masks, block table, idxs).
- `get_block_table` = the paged block-table host build (0029 caches it
  within-step; `LLAMA_PAGED_NO_BT_CACHE` A/B-toggles that).

If `hostproc` per step is a large fraction of the serving per-step wall time
(and the `graphs reused` count is low), the gap is host-bound, not kernel-bound.

### 2d. The serial-SSM host loop (named in README s.5, secondary here)

The gated-DeltaNet decode advances recurrent state per step; sampling cannot
start until logits land. The README already names this as a structural floor in
the *kernel* regime. It is the same in serving but is the *smaller* term - the
graph-rebuild/re-capture overhead (2a/2b) is the new, serving-specific cost the
static bench hides, and it is the one to attack first.

---

## 3. What the already-shipped scheduler patches do (and do NOT do)

These exist; understand them before proposing anything. **None of them touch the
two graph-reuse layers** - they target prefill freezing and burst collapse, not
steady-state decode-step host overhead. That is why the serving gap survives them.

| Patch | What it does | What it does NOT do |
|---|---|---|
| 0008 cross-request prefix-share (server loop) | Concurrent shared-prefix requests prefill only the divergent suffix (fewer prefill tokens). | Does not stabilise decode batch shape; does not graph-reuse. |
| 0013 `LLAMA_PREFILL_BUDGET` | Static per-step prefill-token cap (vLLM `--max-num-batched-tokens` analogue); flattens the ITL spike a long prefill inflicts on co-batched decode. | Ignores decode load; per-workload tuning; no effect on decode-step graph reuse. |
| 0016 dynamic decode-first budget | `max(n_ubatch, T-D)` leftover-after-decode budget + per-slot chunk cap; decode claimed first, auto-shrinks as `D` rises. Stops a prefill chunk from inflating the step past `T`. | **Still lets the per-step decode `n_tokens` and seq-set vary**, so it does not make the decode step graph-reusable; it shapes prefill admission, not decode-shape stability. |
| 0024 paged-pool burst-reclaim | Truncate/defrag/release KV blocks; fixes long-server prefill burst collapse (488->44->532 t/s). | Host accounting only; nothing about decode-step graph capture. |
| 0025 `LLAMA_MOE_FORCE_GRAPHS` | Keeps CUDA graphs ON for the grouped-MMQ MoE decode step (lifts the conservative `MUL_MAT_ID` graph-disable). | Helps the CUDA-graph *eligibility* of one op; does **not** make layer-A/B *reuse* hold across churning steps. It is necessary-not-sufficient: a step that rebuilds anyway gets recaptured regardless. |
| 0029 block-table within-step cache | `get_block_table` computed once per step, memcpy'd to other full-attn layers (-87/-91%). | Shrinks one `set_inputs`/`hostproc` sub-term; does not address rebuild/re-capture. |

**README s.5 "lever 2 (graph/stream coverage): FLAT"** was concluded **in the
static batched-bench regime**, where graphs already reuse - so more graph
coverage was correctly a no-op there. That conclusion does **not** apply to the
serving regime, where graphs do **not** reuse. This doc reopens graph coverage
**for serving only**; record it as a regime-scoped reopening, not a contradiction.

---

## 4. Ranked lever plan (hypotheses - gate on Phase 0 first)

Ranked by value/effort with bit-exactness/risk called out. All are **host-side /
scheduler** levers (no decode-kernel changes), so all are *bit-exact-safe by
construction* provided padding tokens are masked-inert and verified against the
per-path md5 gate.

### Lever S1 (TOP) - bucketed/padded decode-step shape for graph reuse

**Value: high (targets the dominant -39% mechanism). Effort: medium-high. Risk:
medium (correctness of padding inertness; seq-set churn is harder than n_tokens).**

Make the steady-state decode step present a **stable, bucketed shape** to both
reuse layers, mirroring vLLM's padded decode batch + piecewise CUDA graphs:

- Pad the per-step decode `n_tokens` (and the stream/seq count the graph sees) up
  to the next bucket in a small fixed set (e.g. {power-of-two or fixed grid}), so
  `allow_reuse` (layer A) and `update_required` (layer B) hold across steps with
  the same bucket. Padding tokens are dummy, masked positions that contribute
  nothing to any real sequence's logits.
- Bound the number of distinct live buckets so a handful of persistent CUDA
  graphs cover steady decode (vLLM captures ~tens).
- Handle the seq-set component of `allow_reuse`: bucketing `n_tokens` alone is
  insufficient because the *participating sequence-id set* must also match. Either
  (a) pad to a fixed stream-slot layout so the seq-set is stable across arrivals
  /completions, or (b) relax/extend the reuse key so a pure-decode step keyed on
  bucket+slot-layout reuses regardless of which slots are occupied. (b) is the
  higher-leverage but more invasive option.

Bit-exact gate: greedy md5 per path with padding ON must equal the recorded
references (`5951a5b4` dense, `8cb0ce23` paged-MoE); `test-backend-ops`
unaffected (no op changes). The risk is that masked/padded positions leak into a
real logit (off-by-one in the mask) - the md5 gate catches it.

### Lever S2 - overlap per-step host work with GPU decode (double-buffer inputs)

**Value: medium-high (recovers the `hostproc` window even when S1 partial).
Effort: medium. Risk: low (host-side reordering only, bit-exact-safe).**

Even with graphs reused, `set_inputs` (+ the pre-`set_inputs` sync) runs
un-graphed and serially *before* each launch (`hostproc` ~1.4 ms/step in prior
profiles). Overlap the host scheduling + input build of step N+1 with the GPU
decode of step N: double-buffer the input device tensors so the host can fill
N+1's inputs while N's graph is in flight, and prepare the next ubatch / block
table on the host concurrently. This is the llama.cpp analogue of vLLM keeping
the GPU fed. Strictly host-side, no numeric change -> bit-exact. (0029 already
banks part of this for the block table within a step; S2 extends it across
steps.)

### Lever S3 - graph-shape-stable scheduling (bridge from 0016)

**Value: medium (multiplies S1; low marginal value without S1). Effort: low-medium
(extends the existing 0016 policy). Risk: low (scheduler policy, bit-exact when
the decode result is unchanged).**

Extend the existing decode-first budget (0016) so the scheduler actively *prefers
graph-reusable steps*: keep prefill chunks out of the decode step (run prefill in
its own steps, or at a fixed chunk size) so the decode batch shape stays on a
bucket rather than being perturbed by interleaved prefill tokens every step. This
is the policy half of S1 - S1 makes a bucketed step reusable; S3 makes the
scheduler emit bucketed steps. Pair them.

**Rejected/deferred (record so they are not re-tried):**

- **More CUDA-graph *coverage* alone (the README lever-2 redo): still FLAT
  without S1.** Forcing more ops graph-eligible (beyond 0025) does nothing while
  layer A rebuilds the graph every step - the recapture dominates. Only valuable
  *after* S1 makes reuse hold.
- **`GGML_CUDA_DISABLE_GRAPHS` / disabling graphs in serving: REJECTED a priori
  as a fix** (it is an A/B *probe* for Phase 0, not a lever) - it removes capture
  cost but also removes replay benefit; expected net-negative.
- **Precision levers (W4A16, bf16-SSM): out of scope** - this gap is host-bound,
  not GEMM/BW-bound (see README s.5 rejections; do not reopen).

---

## 5. Phase 0 - confirm it is host-bound BEFORE building (run when the GPU frees)

Do NOT build any lever until this confirms host-bound. The dev tree already has
all the instrumentation; this is a measurement, not a code change. **One GPU
bencher at a time** (GPU-contention rule).

**Workload.** Real continuous serving, not batched-bench: run `llama-server`
(paged build) with the paged config and drive it with a steady concurrent
streaming load (e.g. a K-client async generator hitting `/completion` with
staggered arrivals so requests start/finish asynchronously - the regime
batched-bench cannot produce). Use the same models/flags as README s.4:
`-fa on -ngl 99`, `LLAMA_KV_PAGED=1` (+ `LLAMA_MOE_FORCE_GRAPHS=1` for MoE),
dense Qwen3.6-27B-NVFP4 and MoE Qwen3.6-35B-A3B-NVFP4. Pick K so the *effective
decode width* matches a static `npl` you have a kernel-regime number for (e.g.
~128) - that gives the apples comparison: static 6.1 vs serving 3.7 tok/s/seq.

**Signals to capture (all already exist):**

1. **Graph reuse rate.** The `graphs reused = N` perf line (`llama-context.cpp`
   ~L4146, from `data.n_reused`) over total decode steps. Hypothesis: ~100% in
   batched-bench, near 0% in serving. This is the single most decisive number.
   A/B with `LLAMA_GRAPH_REUSE_DISABLE=1` (forces the rebuild path) - if serving
   is already near that floor, layer-A reuse is the gap.
2. **`[L5INSTR]` host buckets** (printed at exit): `hostproc`, `set_inputs`,
   `get_block_table` mean ms/step. Compare serving vs batched-bench. A/B the
   block-table cache with `LLAMA_PAGED_NO_BT_CACHE`.
3. **GPU-busy %** in a steady-state serving window via nsys (sum of kernel
   durations / wall) and the **inter-launch host gap** (time between consecutive
   `cudaGraphLaunch`/kernel launches). Hypothesis: batched-bench ~96-99% busy
   (README/methodology note the early "low util" was a window artifact); serving
   materially lower, with the gap ~= `hostproc`/step. *Watch the same window
   artifact* the methodology warns about - measure a clean steady-state span.
4. **CUDA-graph re-instantiation count** - confirm layer B is also re-capturing
   (nsys shows `cudaGraphInstantiate`/`cudaGraphExecUpdate` per step, or add a
   host-side counter print - host-side only, no kernel code).

**Decision rule.** Host-bound (proceed with S1/S2/S3) if: serving `graphs reused`
is low AND `hostproc`/step is a large fraction of serving per-step wall AND
GPU-busy% drops vs batched-bench by ~the observed throughput ratio (~3.7/6.1).
If instead GPU-busy% stays high and per-kernel time grows, the cause is
elsewhere (e.g. serving runs a worse effective batch shape into the kernels) -
re-scope before building.

**Ground-truth vLLM (both-engine rule).** Capture vLLM at the same concurrency:
GPU-busy% / step cadence (nsys) and its scheduler step time. Confirm vLLM stays
GPU-bound (persistent graphs) where paged goes host-bound - that is the
direct evidence the gap is the host loop, and it sizes the achievable win.

---

## 6. Summary

- The serving gap (paged 3.7 vs vLLM 5.9 tok/s/seq, -39%) is a **host/scheduler**
  problem, distinct from the decode **kernel** (at parity in batched-bench). The
  README's BW-floor/host-loop-residual findings are kernel-regime and do not
  bound the serving regime.
- Leading mechanism: continuous batching's **batch-shape + seq-set churn breaks
  both graph-reuse layers** (llama-context `can_reuse`, CUDA `update_required`)
  every step, so the GPU idles while the host rebuilds + re-captures + runs
  un-graphed `set_inputs`. vLLM avoids this with padded/bucketed decode shapes +
  piecewise CUDA graphs.
- The shipped scheduler patches (0008/0013/0016/0024/0025/0029) target prefill
  freezing + burst collapse, **not** decode-step graph reuse - which is why the
  serving gap survives them.
- Top levers (all host-side, bit-exact-safe): **S1** bucketed/padded decode-step
  shape for graph reuse, **S2** double-buffer/overlap per-step host work, **S3**
  graph-shape-stable scheduling (extend 0016). Gate everything on **Phase 0**:
  the `graphs reused` rate + `[L5INSTR]` host buckets + nsys GPU-busy% in real
  `llama-server` serving vs batched-bench, with vLLM ground-truthed at the same
  concurrency.
</content>
</invoke>
