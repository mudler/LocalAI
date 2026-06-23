# Qwen3.6 NVFP4-vs-NVFP4: llama.cpp vs vLLM on GB10 (DGX Spark)

Apples-to-apples benchmark. Both engines run the **same NVFP4 weights** on the **same box**
(GB10, sm_121, LPDDR5x unified memory ~273 GB/s). The question is not "who wins the HW
lottery" but "at matched NVFP4, on one bandwidth-limited box, does our paged llama.cpp
(patch 0015, expert-density-aware MoE token-tile auto-select, default-on) sit at par with /
ahead of / behind vLLM?"

## Setup

- **Box**: GB10 / DGX Spark, sm_121, unified LPDDR5x (~273 GB/s). Memory figures are
  unified-memory used GB (`MemTotal-MemAvailable`), so they cover weights + KV + runtime.
- **llama.cpp**: dev tree `~/llama-paged-dev` branch `paged` HEAD `151343b` (patch 0015),
  `build-cuda` sm_121, `LLAMA_KV_PAGED=1`, `llama-server -c 131072 --parallel 128 -b 2048
  -ub 512 -ngl 99 -fa on`. **NOTE: run WITHOUT `max_prefill_tokens` (patch 0013) - see the
  TTFT caveat in the verdict.**
- **vLLM**: 0.23.0, `--enforce-eager --gpu-memory-utilization 0.85 --max-model-len 4096
  --max-num-seqs 256 -tp 1`.
- **Client**: identical async client for both engines. Per request: 512-token unique prompt
  (unique leading tokens defeat cross-request prefix caching), `max_tokens=256`,
  `temperature=0`, `ignore_eos=True`, streaming with usage. Concurrency (npl) swept 8/32/64/128.
- **Metrics** (localmaxxing.com schema): `decode_agg_tps` (aggregate decode tok/s across all
  live seqs), `decode_perseq_tps` (mean per-sequence decode), `prefill_tps`, `ttft_mean_ms`,
  `PEAK_GB` (unified-memory peak).

## The 4 models (NVFP4, matched weights)

| Model | llama.cpp GGUF | vLLM checkpoint | Match |
|-------|----------------|-----------------|-------|
| DENSE Qwen3.6-27B (28B dense) | `q36-27b-nvfp4.gguf` (native Blackwell FP4) | `q36-27b-nvfp4-vllm/` (unsloth TRUE W4A4) | clean W4A4 both sides |
| MoE Qwen3.6-35B-A3B (36B total, ~3B active) | `q36-35b-a3b-nvfp4.gguf` (241 NVFP4 tensors, nvidia weights) | `q36-35b-a3b-nvfp4-vllm/` (nvidia modelopt; vLLM picks Marlin NvFp4 MoE + FA2) | NVFP4 weight-only, identical nvidia weights |

---

## Results (decode aggregate tok/s, per-seq, prefill, TTFT, peak GB)

### MoE Qwen3.6-35B-A3B (~3B active)

| npl | engine | decode agg | decode/seq | prefill | TTFT mean ms | peak GB |
|----:|--------|-----------:|-----------:|--------:|-------------:|--------:|
| 8   | llama  | 170.2 | 20.27 | 2813 | 855     | 38.98 |
| 8   | vLLM   | 202.0 | 24.92 | 4648 | 799     | 111.49 |
| 32  | llama  | 235.4 | 6.77  | 2005 | 4970    | 43.06 |
| 32  | vLLM   | 462.0 | 13.59 | 4755 | 2308    | 111.26 |
| 64  | llama  | 271.7 | 3.88  | 2389 | 7205    | 52.53 |
| 64  | vLLM   | 624.5 | 8.90  | 4784 | 4072    | 111.46 |
| 128 | llama  | 292.2 | 2.05  | 657  | 84800   | 61.42 |
| 128 | vLLM   | 811.1 | 5.46  | 4263 | 7980    | 111.61 |

llama decode as % of vLLM: **84 / 51 / 44 / 36** at npl 8/32/64/128.

### DENSE Qwen3.6-27B

| npl | engine | decode agg | decode/seq | prefill | TTFT mean ms | peak GB |
|----:|--------|-----------:|-----------:|--------:|-------------:|--------:|
| 8   | llama  | 63.8  | 7.60 | 1117 | 2029    | 51.72 |
| 8   | vLLM   | 64.3  | 7.98 | 1514 | 2593    | 112.07 |
| 32  | llama  | 108.9 | 3.08 | 752  | 13212   | 61.48 |
| 32  | vLLM   | 189.8 | 5.57 | 1555 | 7477    | 112.09 |
| 64  | llama  | 126.2 | 1.78 | 465  | 53818   | 74.90 |
| 64  | vLLM   | 284.2 | 3.92 | 1526 | 12942   | 112.11 |
| 128 | llama  | 134.6 | 0.93 | 125  | 491195  | 94.03 |
| 128 | vLLM   | 390.7 | 2.50 | 1420 | 24806   | 112.12 |

llama decode as % of vLLM: **99 / 57 / 44 / 34** at npl 8/32/64/128.

---

## Verdict

**At matched NVFP4 on one GB10 box: llama.cpp is at parity only at low concurrency; vLLM
scales substantially better as concurrency rises.**

1. **npl=8 (low concurrency): near parity.** Dense 99%, MoE 84% of vLLM decode. The MoE's
   ~3B active shows: per-seq decode 20-25 tok/s (MoE) vs 8 tok/s (dense) on both engines.

2. **npl>=32 (high concurrency): vLLM pulls decisively ahead** - decode ~2x (npl32) rising to
   ~2.8-2.9x (npl128) on both models. vLLM scales monotonically (dense 64->391, MoE 202->811);
   llama plateaus (dense 64->135, MoE 170->292).

3. **TTFT is the clearest gap, and it is largely self-inflicted here.** llama's TTFT explodes
   at high concurrency (dense **491 s**, MoE **85 s** at npl128) while vLLM stays bounded (25 s,
   8 s). **This run used llama WITHOUT `max_prefill_tokens` (patch 0013)** - so 128 concurrent
   512-token prefills starve each other and the decode. Crucially, that starvation also drags
   `decode_agg` down: while many slots are stuck prefilling, fewer are actually decoding, so the
   measured aggregate understates llama's steady-state decode. A re-run with `max_prefill_tokens`
   (the QoS budget this PR already ships) is expected to bound TTFT AND raise high-concurrency
   decode by keeping all slots live.

4. **Memory: llama wins on efficiency.** vLLM pre-reserves the whole pool (~112 GB at
   gpu-mem-util 0.85); llama grows on demand (MoE 38->61 GB, dense 52->94 GB). The paged
   on-demand KV is materially more memory-efficient / multi-tenant-friendly.

5. **vs the localmaxxing reference (259.5 MoE / 254.8 dense top-speed):** those are single-stream
   on fast datacenter HW. GB10 per-seq decode tops out far lower (MoE ~25, dense ~8 tok/s at
   npl8) - the LPDDR5x ~273 GB/s bandwidth floor, as expected. The reference is a ceiling, not a
   GB10 target.

### Honest bottom line

The "par-or-beat vLLM" goal is **met at low concurrency but NOT at high concurrency** on these
NVFP4 models. vLLM's continuous-batched decode + bounded prefill scheduling scale better on a
bandwidth-limited box. Two of the three gap drivers are addressable on our side: (a) **prefill
starvation** - re-run with `max_prefill_tokens` (patch 0013), which this PR ships; (b) **decode
batching efficiency at high concurrency** - the runtime/scheduler lever (the small/unsaturated
regime). The kernel itself is at parity (npl8). Next step: a fair re-run with the prefill budget
on, plus decode-batch tuning, to get llama's true high-concurrency numbers before concluding the
absolute gap.

---

## Fair re-run (max_prefill_tokens on)

The prior tables ran llama-server **without** the QoS prefill budget (patch 0013). This section
re-runs the same A/B with `LLAMA_PREFILL_BUDGET` set, sweeping the per-step prompt-token cap over
**256 / 512 / 1024**. Everything else is byte-identical to the prior run: dev-tree llama-server
(branch paged, HEAD `151343b`), `-c 131072 --parallel 128 -b 2048 -ub 512 -ngl 99 -fa on`,
`LLAMA_KV_PAGED=1`, same workload (512-token unique prompt, `max_tokens=256`, `temperature=0`,
`ignore_eos`), same harness (`h2h_moe_sweep.sh` -> `h2h_cli.py`). vLLM numbers are unchanged
(carried over from the committed dense table, not re-run).

### DENSE Qwen3.6-27B - budget sweep (decode agg tok/s | TTFT mean ms | peak GB)

| npl | metric | stock (no budget) | budget 256 | budget 512 | budget 1024 | vLLM |
|----:|--------|------------------:|-----------:|-----------:|------------:|-----:|
| 8   | decode agg | 63.8  | 63.5   | 63.8   | 63.5   | 64.3  |
| 8   | TTFT ms    | 2029  | 4255   | 3756   | 2653   | 2593  |
| 32  | decode agg | 108.9 | 105.7  | 107.7  | 108.8  | 189.8 |
| 32  | TTFT ms    | 13212 | 23114  | 18934  | 13912  | 7477  |
| 64  | decode agg | 126.2 | 132.0  | 131.2  | 118.2  | 284.2 |
| 64  | TTFT ms    | 53818 | 109455 | 74272  | 92450  | 12942 |
| 128 | decode agg | 134.6 | **161.2** | 146.9 | 128.3 | 390.7 |
| 128 | TTFT ms    | 491195| **305423**| 543448| 424058| 24806 |

Peak host GB is budget-independent (on-demand paged KV grows with concurrency): ~51.5 (npl8) ->
~61.5 (npl32) -> ~74.7 (npl64) -> ~93.5 (npl128) for every budget, vs vLLM's flat ~112.1.

### Best budget = 256 (only the saturated npl128 regime benefits)

At the fully-saturated point (npl128), **budget 256 is the clear winner on both axes**:

- **decode_agg: 134.6 -> 161.2 tok/s (+19.8%)** vs the starved stock run.
- **TTFT mean: 491.2 s -> 305.4 s (-37.8%, -186 s)** vs stock.
- llama decode as % of vLLM at npl128: **34.5% -> 41.3%**. TTFT still ~12x vLLM's 24.8 s.

Larger budgets help less at npl128 (512 -> 146.9 tok/s; 1024 -> 128.3, i.e. ~stock) because a
looser cap lets a long prefill grab a bigger slice per step and re-introduce decode jitter. So
the tightest cap (256) protects in-flight decode the most when the box is saturated.

### Honest caveat: this bursty workload is the worst case for TTFT

At npl 8 / 32 / 64 the budget **raised** TTFT (e.g. npl8 2029 -> 4255 ms at budget 256) and left
decode_agg roughly flat. Reason: the harness fires all N requests simultaneously, so at t=0 there
is **no in-flight decode to protect** - capping prefill purely defers first tokens. The budget
only pays off once enough slots are decoding that an unbounded prefill would starve them, which on
this box happens only at npl128. Budget 1024 tracks stock closely at light load (npl8 TTFT 2653 ~
stock 2029) because a 512-token prompt fits in one <=1024 step. In a steadier (staggered) arrival
pattern the budget would protect decode jitter without the burst-TTFT penalty; that regime is not
exercised here.

### Bottom line (dense)

The prefill budget is a **real but narrow** lever on this workload: at maximum saturation
(npl128) budget=256 lifts decode_agg ~20% and cuts TTFT ~38% vs the starved run, moving llama
from 34.5% to 41.3% of vLLM decode. It does **not** close the gap - vLLM still decodes ~2.4x
faster and keeps TTFT ~12x lower at npl128, and scales monotonically where llama plateaus. At
light/moderate concurrency the budget is net-negative for TTFT in this all-at-once workload, so it
should be applied selectively (high-concurrency serving), not as an unconditional default.
