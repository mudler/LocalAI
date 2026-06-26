# MOE_GAP_PROGRESS.md - moe-gap-groundtruth GPU agent checkpoint

Status: **DONE.** Both-engine MoE decode decomposition complete. Findings in `MOE_GAP_VS_VLLM.md`.

## Runs (DGX GB10 sm_121, GPU free, foreground)
- llama: `build-cuda` 2f4f5ab (0025), `llama-batched-bench -npp128 -ntg128 -npl128 -c32768 -fa on`,
  `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1`. S_TG=752.3 t/s, step 169.8 ms, busy 97.5%.
  Artifacts on DGX: `~/llama-paged-dev/moe_gap_llama.{nsys-rep,trace.csv}`.
- vLLM 0.23.0 graphs-ON (FULL_AND_PIECEWISE, the 882-ref config): `~/bench/moe_gap_vllm.py` under
  `nsys --capture-range=cudaProfilerApi`. step 142.0 ms, busy 99.7%.
  Artifacts on DGX: `~/bench/moe_gap_vllm.{nsys-rep,trace.csv}`, script `~/bench/moe_gap_vllm.py`.
- Extractor: `~/bench/decode_decomp2.py` (dual-engine, steps = GDN-kernel-count / 30; cross-checked vs
  flash/reshape_cache = 10x and vs throughput). Grouped-MoE GEMM isolated by per-call duration (LONG/SHORT).

## Result (1 line)
Gap = 27.8 ms/step (llama 83.6% of vLLM). **MoE grouped GEMM is a llama WIN** (native FP4-MMA W4A4 47.3 ms
vs Marlin W4A16 50.0 ms). The 15% is bf16-projections+convert (+6.5), recurrence state-gather plumbing
(+6.6, led by k_get_rows 5.2 ms), graph/overlap (+7.0), W4A4 act-quant tax (+3.3), router/glue (+5.4).
Marlin is NOT the lever; do not build a W4A16 MoE GEMM.

Assisted-by: Claude:opus-4.8 [Claude Code]
