# Paged-attention / parity benchmarks (GB10 / DGX Spark)

Goal of the series: vLLM parity. This records the measured gap so the parity claim is data-backed, not asserted.

**Setup:** GB10 (sm_121, 119 GiB unified). Model Qwen3-Coder-30B-A3B. llama.cpp = pinned base + this series
(MXFP4_MOE, `-fa 1 -b 2048 -ub 2048`, `llama-batched-bench`, PP=512 TG=128). vLLM = 0.23.0 FP8 (recorded
prior run, same box/model). S_PP / S_TG are aggregate prefill / decode tok/s across B streams.

## Fresh llama.cpp (this series, MXFP4) vs vLLM (FP8)

| B | llama S_PP | vLLM S_PP | PP gap | llama S_TG | vLLM S_TG | TG gap |
|---|-----------|-----------|--------|-----------|-----------|--------|
| 1 | 1565 | 9644 | 6.2× | **83** | 48 | **llama wins** |
| 8 | 3648 | 33373 | 9.1× | 126 | 312 | 2.5× |
| 32 | 2074 | 99398 | 48× | 319 | 1171 | 3.7× |
| 64 | 3643 | 151990 | 42× | 771 | 2064 | 2.7× |

## Verdict — two distinct gaps, only one is the engine's

1. **Prefill (S_PP): 6–48× behind, and it does NOT scale with B** (plateaus ~3.6k). This is the **FP4 MoE
   GEMM kernel** (`mul_mat_q<MXFP4>` ~22 TFLOP/s), confirmed earlier. **Paged attention cannot close this** —
   it's per-token compute. Needs the tcgen05/CUTLASS grouped-GEMM (Lever 3, multi-week, no upstream base).
2. **Decode at concurrency (S_TG): 2.5–3.7× behind for B≥8** (we *win* at B=1). This gap IS partly the
   engine's domain — vLLM's block-paged KV + continuous batching pack more concurrent decode work per step.
   **This is what patches 0003–0006 target.** The win here is realistic; the prefill win is not (kernel).

## CORRECTION — decode-phase profile (B=64, decode-dominated nsys)

The "decode gap is engine-addressable" read above was **wrong**. Profiling a decode-dominated B=64 run:

| kernel | % GPU time |
|---|---|
| `mul_mat_q<MXFP4>` (MoE GEMM) | **54.6** |
| `flash_attn_ext` (attention) | 19.8 |
| `mul_mat_q<Q8>` (dense) | 10.9 |
| KV writes / quant / norms / rest | ~15 |

**Decode at concurrency is ALSO dominated by the FP4 MoE GEMM (54.6%)** — the same Lever-3 kernel as prefill.
Attention (the only thing paging optimizes) is ~20%, and the gather-read reclaims only the *masked-cell*
fraction of that. So **the paged series (0003–0006) cannot close the vLLM gap in either phase** — both are
MoE-kernel-bound. vLLM's concurrency advantage is its MoE/attention *kernels*, not (mainly) its KV management.

### What the paged series IS still good for (just not throughput parity)

- **Capacity**: block-granular + on-demand allocation → fit more/longer concurrent sequences in fixed VRAM.
- **Prefix sharing**: cross-request block dedup → lower TTFT + memory on shared system prompts / RAG.

These are real wins on *memory-pressured* and *shared-prefix* workloads — but they are not tok/s parity, and
batched-bench (fresh, non-fragmented, no shared prefix) won't show them.

## So, honestly, where parity stands

- **Decode single-stream: already at/above parity** (B=1: 83 vs 48).
- **Decode concurrency: a real, engine-addressable gap** the paged series can narrow (0004 on-demand pool +
  0005 continuous batching). Target: close the 2.5–3.7× at B≥8.
- **Prefill: kernel-bound, not engine-bound.** No amount of paging reaches vLLM here; that's a separate track.

**Series status when measured:** 0001 (vendor) + 0002 (placement, token-identical) done; 0003 (gather-read)
turn-key-planned, not yet implemented. These numbers are the *baseline* the engine patches must improve on at
B≥8 decode — re-run this table after 0004/0005 to show the concurrency gap closing.
