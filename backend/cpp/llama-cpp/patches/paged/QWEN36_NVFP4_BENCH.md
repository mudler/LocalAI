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
