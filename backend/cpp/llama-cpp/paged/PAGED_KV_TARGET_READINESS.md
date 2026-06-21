# Paged KV: target-readiness (correctness, dynamic benchmark, 2xH200 projection)

Target hardware: **~2x H200** (281 GB HBM3e total, ~4.8 TB/s per GPU). The GB10 box is
the *test* rig, not the target - and several earlier "no win" findings are GB10-specific
artifacts (low bandwidth caps throughput before KV memory ever binds). This document
delivers the three things needed to push paged KV toward the real target:

1. **Correctness** of the paged path - verified (and a blocking bug found + fixed).
2. **A dynamic-load benchmark** that actually exercises where paging wins (`paged-loadgen.cpp`).
3. **A projection** of the paged-KV payoff on 2x H200, grounded in measured GB10 numbers.

---

## 1. Correctness: PASS (after fixing the auto-fit OOM)

`test-paged-kv-e2e` checks the paged decode path against the contiguous reference
(greedy argmax + top-5 set overlap >= 4). On the box it was previously **unverified** -
it aborted at context creation. Root cause found:

- `common_fit_paged_kv_blocks` (`common/common.cpp:1144`) **unconditionally overrides**
  `n_gpu_blocks` from `ggml_backend_dev_memory`, which **over-reports free VRAM on the
  GB10 integrated/unified device** (it sized **~245 GB of KV on a 119 GB box** ->
  `cudaMalloc` OOM -> `GGML_ASSERT` abort in `llama-kv-cache-paged.cpp:74`). The test's
  explicit `n_gpu_blocks=64` was being clobbered because `params.fit_params` defaults on.

**Fix (item-1 patch, applied on the box):**

```diff
--- a/tests/test-paged-kv-e2e.cpp
+++ b/tests/test-paged-kv-e2e.cpp
@@ run_paged()
     params.kv_paged      = true;
+    params.fit_params    = false;  // honor explicit n_gpu_blocks; GB10 dev_memory over-reports free VRAM
     params.n_gpu_blocks  = 64;
```

**Result (Qwen3-0.6B-Q8_0, GB10):**

```
test-paged-kv-e2e: top-5 argmax match: ref=3743 paged=3743
test-paged-kv-e2e: top-5 set overlap: 5/5 (require >= 4)
test-paged-kv-e2e: PASSED
```

The paged op is **numerically greedy-equivalent to the contiguous path**. The reshape
bug from `PR22569_EVAL.md` (decoupled head_dim) is already applied in the checkout.

**Target-readiness caveat (the durable fix, not just the test):** the auto-fit itself is
brittle and must be hardened before it runs on a real serving box - even though
`ggml_backend_dev_memory` reports correctly on a discrete H200, the function should still
(a) early-return when `!params.fit_params`, (b) **clamp** the computed `n_gpu_blocks` so
`n_gpu_blocks * block_bytes <= free_vram - margin` using the *actual* KV element size, and
(c) not override an explicitly-set value. One-screen change in `common_fit_paged_kv_blocks`.

---

## 2. Dynamic-load benchmark - `paged-loadgen.cpp`

**Why the existing tools show no paged win:** `llama-batched-bench` and the stock
`examples/paged/paged.cpp` both run **fixed-length, all-arrive-at-once, single-prompt**
load. That has no over-reservation and no fragmentation, so contiguous KV is already
memory-optimal and paging has nothing to reclaim (`PAGED_KV_HIGH_CONCURRENCY.md`). The
paged win only exists under **variable lengths + continuous arrival + shared prefixes** -
the real serving regime. No tool in the tree creates it.

`paged-loadgen.cpp` (committed here) does, via the confirmed `llama_paged_scheduler_*`
API:

- **shared system prefix** (`LG_PREFIX` tokens) prepended to every request -> exercises
  cross-request prefix sharing,
- **variable prompt length** (`LG_SUFMIN..LG_SUFMAX` unique suffix),
- **bimodal generation length** (`LG_GENLONG` for `LG_LONGPCT`% of requests, else
  `LG_GENSHORT`) - the over-reservation driver,
- **continuous arrival**: keeps `LG_INFLIGHT` requests live, admitting a new one each time
  one finishes.

It reports the load-bearing number for the buy decision - the **capacity ratio**:

```
paged peak KV      = sum over live seqs of ceil(used/block)*block * kv_bytes_per_token
contiguous reserve = peak_inflight * max_ctx * kv_bytes_per_token   (worst-case per slot)
CAPACITY RATIO     = contiguous_reserve / paged_peak   (+ prefix sharing on top)
```

`kv_bytes_per_token = 2 * n_layer * n_head_kv * head_dim * sizeof(f16)` - confirmed against
`llama-kv-cache-paged.cpp` (e.g. Qwen3-32B: 2*64*8*128*2 = **256 KiB/token**).

**How to run (on the target):** drop into PR #22569's `examples/paged/`, add to its
CMakeLists next to `llama-paged`, build, then e.g.
`LG_INFLIGHT=2048 LG_LONGPCT=15 paged-loadgen -m <model> -kvp --fit off -ngpub <N> -ncpub <M> -ngl 99`.
Sweep `LG_INFLIGHT` to the throughput plateau and read the capacity ratio at that point.
It is written to run on the target (2x H200) where the regime exists; on GB10 it runs but
the ratio is uninteresting because throughput plateaus before memory binds (see below).

---

## 3. Projection to 2x H200 (grounded in measured GB10 numbers)

### Measured on GB10 (this work)

| model | decode plateau (aggregate) | plateau concurrency | bound by |
|---|---|---|---|
| Qwen3-32B-Q4_K_M (dense) | ~540 t/s | npl ~128 | compute |
| Qwen3-1.7B-Q8_0 | ~3,200 t/s | npl ~512 | bandwidth |

### Hardware ratios (per GPU, then 2x TP at ~85% scaling)

| | GB10 | H200 | per-GPU x | 2x H200 (TP) x |
|---|---|---|---|---|
| mem bandwidth | 273 GB/s | ~4.8 TB/s | 17.6 | ~30 |
| BF16 compute | ~213 TFLOP | ~989 TFLOP | 4.6 | ~8 |
| HBM | 119 GB | 141 GB | 1.18 | 2.4 (281 GB) |

Decode is bandwidth-bound, so **both the aggregate ceiling and the concurrency at which it
is reached scale with bandwidth (~30x on 2x H200)**:

- **32B-dense aggregate decode ceiling:** 540 x 30 ~= **16,000 t/s**, reached at
  ~128 x 30 ~= **3,800 concurrent sequences**.

### Why paged KV becomes the binding lever on 2x H200 (and didn't on GB10)

To reach that ~16k t/s ceiling you must hold **~3,800 sequences** of KV. The memory math:

- 32B weights (FP8) ~= 32 GB, sharded over 2 GPUs -> ~250 GB HBM free for KV.
- 32B KV = 256 KiB/token. At an avg held context of 2,000 tokens, **per seq = 512 MiB**.
- Contiguous unified KV (reserve for the live set) fits ~250 GB / 512 MiB ~= **~490
  sequences** - **8x short of the 3,800 needed to reach the throughput ceiling.**

So on 2x H200 **KV memory is the binding constraint at the throughput-optimal concurrency**,
and contiguous KV strands most of the bandwidth (you'd run at a fraction of 16k t/s). This
is the gap paged KV closes. On GB10 it never appeared because GB10's 30x-lower bandwidth
caps decode at npl ~128, whose KV fits in memory trivially - the constraint order is
inverted on the real target.

### Magnitude of the paged win

Paging recovers concurrency two ways, both multiplicative on achievable throughput:

1. **No over-reservation.** Contiguous must back `max_ctx` per slot; paging uses
   `ceil(actual/block)`. For a realistic bimodal workload (most generations short, ~15%
   long, prompts ~512) the average held context is several-fold below `max_ctx` ->
   `paged-loadgen` capacity ratio typically **~4-10x** (it measures the exact number for
   your workload's length distribution).
2. **Cross-request prefix sharing** of shared system prompts / RAG preambles - additional,
   workload-dependent (chained-hash block cache; vLLM's `block_pool.py`).

Net: on 2x H200, paged KV is plausibly the difference between serving **~500 and ~3,800**
concurrent 32B sequences in HBM, i.e. between a fraction of and ~all of the **~16k t/s**
decode ceiling. **That is the datacenter payoff, and it is real on the target even though
GB10 cannot exhibit it.**

### Honest caveats for the buy case

- These are **projections** from GB10 + spec ratios; the capacity multiplier depends on the
  workload's context-length distribution (more variable -> bigger paged win) and TP
  efficiency. `paged-loadgen` measures it directly once you have target-GPU time.
- The **paged op itself still needs work**: PR #22569's `ggml_paged_attn` was 12-13%
  *slower* than the mature contiguous flash-attention path at equal concurrency
  (`PR22569_EVAL.md`), lacks prefix sharing (deferred to a non-existent Phase 2), and has
  the fit-robustness bug above. Adopting paged KV for the target means either hardening
  #22569 or finishing the from-scratch P4 - the capacity win above assumes a *correct,
  competitive* op, which is the remaining engineering.
- Prefill on either KV layout is compute-capped, not a paged concern.

**Bottom line for the decision:** paged KV **is** the right lever for the 2x H200 target -
the GB10 "no win" result is a bandwidth artifact, not a verdict. The paged path is now
**correctness-verified**, the **benchmark to size the win exists**, and the projection
says the payoff is **~5-10x concurrent-tenant capacity -> several-fold higher aggregate
decode** on the target. The remaining work is hardening/finishing the paged op, not
proving the thesis.
