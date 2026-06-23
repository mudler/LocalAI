# Patch 0014 findings: expert-aware MoE token-tile cap (LLAMA_MOE_MMQ_X)

Near-term lever for the MoE-vs-vLLM workflow on GB10 (sm_121). Companion to
`0014-paged-expert-aware-moe-token-tile-cap.patch`. Model:
Qwen3-Coder-30B-A3B, 128 experts, top-8, mxfp4 experts
(`~/bench/qwen3coder-mxfp4.gguf`). Dev tree `~/llama-paged-dev` (branch `paged`),
`build-cuda` sm_121.

## Headline (honest): there is no npl128 cliff to erase on this build

The mission premise was a 25% decode drop at npl128 (batched-bench 253/505/830/620
@ npl 8/32/64/128). It does **not** reproduce. Stock decode is monotonic:

```
llama-batched-bench, qwen3coder-mxfp4.gguf, -fa on, -npp 128 -ntg 128, S_TG t/s
  npl        1     8    32    64   128   256
  stock     85   282   629   935  1295  1779     <- monotonic, no knee
```

The old cliff was a real high-batch regression since fixed upstream: mxfp4 MoE
decode on GB10 already takes the sorted grouped FP4-MMA GEMM (MUL_MAT_ID ->
`ggml_cuda_mul_mat_q` ids branch: `mm_ids_helper` moe_align/scatter + one
persistent stream-k `mul_mat_q`), i.e. vLLM's algorithm. See
`MOE_GROUPED_GEMM_SCOPE.md`.

## What the knob does

`mul_mat_q_case` picks the token-tile width `mmq_x` to cover `ncols_max`
(= `ne12`, the per-expert column upper bound = token count, up to 128) in one
column-tile. At MoE decode the per-expert density is `~ne12*k/n_experts`
(top-8/128 => ~1/16 of `ne12`), so each expert's `mmq_x`-wide col-tile is only
~6% filled: the MMA accumulator tile is `mmq_x`-wide at compile time and wastes
throughput on the padding columns, and the larger y-tile lowers occupancy.

`LLAMA_MOE_MMQ_X=<n>` caps `mmq_x` on the MUL_MAT_ID path only
(`expert_bounds != nullptr`). It only lowers the selection-loop upper bound and
still chooses from the same granularity/shared-memory-validated `mmq_x` set stock
already uses for smaller batches - no new kernel configuration. Default
(unset/<=0) = disabled => byte-identical to stock.

## Measurements (same binary, only LLAMA_MOE_MMQ_X differs)

Decode throughput, S_TG t/s:

```
  npl     stock   cap16   cap32   cap64
   1       85      85      85      85
   8      282     280     282     282
  32      629     623     629     628
  64      935     915     949     934
 128     1295    1204    1344    1357     <- cap64 +4.8% (cap16 -7%)
 256     1779    1370    1723    1820     <- cap64 +2.3% (cap16 -23%)
```

Prefill throughput, S_PP t/s (the cost):

```
  npl     stock   cap16   cap32   cap64
 128     3083    1817    2559    3038
 256     3084    1818    2560    3046
                 -41%    -17%    -1.3%
```

Reproducibility (interleaved off/cap64, two reps each):

```
  npl    off rep1/rep2   cap64 rep1/rep2
  128    1300 / 1290     1357.5 / 1357.0
  256    1786 / 1782     1826.3 / 1824.5
```

cap64 is stable to <0.1% and the gain sits well above the ~1% run-to-run band.

## Why 64 is the only value that helps net

A 512-token prefill ubatch routes ~32 tokens/expert. cap16/cap32 force those into
16/32-wide tiles, overflowing into extra col-tiles + weight re-reads -> prefill
craters (-41% / -17%). cap64 still holds the prefill density in one tile (32 < 64)
so prefill is near-neutral (-1.3%), while decode (~8 tokens/expert at npl128) gets
the fuller, higher-occupancy tile.

## Verdict

- Real but **modest** high-effective-batch DECODE micro-optimization
  (+4.8% npl128, +2.3% npl256), neutral at npl<=64, ~1.3% prefill cost at cap64.
- **Not** a cliff fix (no cliff) and **not** a real-server unlock (llama-server
  continuous batching already scales). Shipped as an opt-in, default-off knob;
  recommended value 64 for decode-heavy high-concurrency deployments.
- Correctness: greedy temp-0 server output with cap64 is byte-identical to stock
  for single-stream generation and stays coherent; thousands of capped MoE
  matmuls at npl128/256 ran with no CUDA error / NaN.

## Durable follow-up (scoped, not implemented)

Replace the blunt global cap with a density-aware auto-select: choose `mmq_x`
from `ne_get_rows / n_active_experts` inside `mul_mat_q_case` so decode gets the
small tile while prefill keeps its large tile automatically (removes the ~1.3%
prefill cost). Plus the block-padded `moe_align` in `mm_ids_helper`. See
`MOE_GROUPED_GEMM_SCOPE.md`.
