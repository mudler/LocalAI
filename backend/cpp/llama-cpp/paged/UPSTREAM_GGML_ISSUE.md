# Upstream ggml issue draft: MXFP4 MoE prefill underutilizes Blackwell (GB10) — ~22 TFLOP/s, ~27× behind vLLM

**Title:** CUDA: MXFP4 MoE prefill runs the Ampere-class warp `mma.sync`, far below Blackwell FP4 peak (GB10 / sm_121)

## Summary

On a GB10 (DGX Spark, sm_121), MXFP4 MoE prefill for Qwen3-Coder-30B-A3B is bottlenecked by
`mul_mat_q<MXFP4>` (the per-expert grouped MMQ), which runs at only **~22 effective TFLOP/s** — a small
fraction of the GPU's FP4 capability. Batched prefill plateaus at ~3.65k tok/s (B=32) vs vLLM FP8 ~99k
on the same box (~27×). The native FP4 block-scaled `mma.sync` path (PR #17906 et al.) *is* engaged — the
limit is that it's a warp-level MMA kernel, not a tcgen05/CUTLASS-class grouped GEMM.

## Hardware / build

- NVIDIA GB10, compute capability 12.1, 119 GiB unified LPDDR5X.
- llama.cpp built `-DCMAKE_CUDA_ARCHITECTURES=121` (sm_121a/compute_121a confirmed in cubins).
- Model: Qwen3-Coder-30B-A3B-Instruct, `MXFP4_MOE` (15.9 GiB, 4.47 BPW).

## Measurements

Single-stream (`llama-bench`, ub2048):

| metric | Q8_0 | MXFP4 | vLLM FP8 |
|---|---|---|---|
| prefill pp2048 | ~2200 | 3441 | — |
| decode tg128 | 62 | 86 | 52 |

Batched (decode-phase aggregate `S_TG`; prefill aggregate `S_PP`):

| B | llama MXFP4 prefill | vLLM FP8 prefill | llama MXFP4 decode | vLLM FP8 decode |
|---|---|---|---|---|
| 1 | 1625 | 9644 | 83 | 48 |
| 8 | 3634 | 33373 | 267 | 312 |
| 32 | 3651 | 99398 | 551 | 1171 |
| 64 | 3648 | 151990 | 770 | 2064 |

Decode is competitive (we win at B=1). **Prefill plateaus and is the gap.**

## Profiling (nsys, MXFP4 pp2048 kernel time)

| kernel | % |
|---|---|
| `mul_mat_q<(ggml_type)39>` (MXFP4 MoE GEMM) | **37.2** |
| `mul_mat_q<(ggml_type)8>` (dense/attn, still Q8) | 10.1 |
| `flash_attn_ext_f16` | 8.8 |
| `quantize_mmq_mxfp4` (activation quant) | 8.0 |

Only cutlass kernel present is `cutlass_80_tensorop` (Ampere). No tcgen05 / wgmma anywhere.

## What we ruled out (so it's the kernel, not config)

- **ubatch**: saturates at 2048 (pp4096: ub512 2994 → ub2048 3316 → ub8192 3180).
- **tile width**: `mmq_x` already selects the full 128-wide tile at ub2048 (~128 tokens/expert).
- **cuBLAS fallback**: `GGML_CUDA_FORCE_CUBLAS` is a no-op (3419 ↔ 3423 t/s) — dequant→cuBLAS-FP16 neither
  helps nor hurts, i.e. the FP4 MMQ kernel isn't worse than FP16 cuBLAS, both hit a common ceiling.
- prefill does **not** scale with bigger single prompts (attention O(N²) confounds): pp2048 3295, pp8192
  1524, pp16384 2051 — so it's the many-sequence batched MoE GEMM, not batch size.

## Proposal

A tcgen05 / CUTLASS-3.x grouped-GEMM path for FP4 (MXFP4 + NVFP4) MoE on sm_120/121:
- One grouped GEMM over all experts with per-group token offsets (full tiles regardless of tokens/expert),
  vs today's per-expert MMQ scheduler.
- Block-scaled `e2m1` operands via tcgen05 tensor-memory MMA (`mma.sync.aligned.kind::mxf4…` is the
  warp-level form; the collective-mainloop/tcgen05 form is what extracts Blackwell throughput at prefill
  tile sizes).
- Fuse activation quantization (`quantize_mmq_mxfp4`, ~8%) into the permute/gather.
- Optionally extend to dense layers (qkv/o_proj/lm_head) so full-model prefill is FP4/FP8.

This mirrors what vLLM/FlashInfer/TensorRT-LLM do for Blackwell MoE. Happy to test iterations on the GB10.

## Repro

```sh
llama-quantize qwen3coder-f16.gguf qwen3coder-mxfp4.gguf MXFP4_MOE
llama-bench -m qwen3coder-mxfp4.gguf -ngl 99 -p 2048 -n 0 -ub 2048
llama-batched-bench -m qwen3coder-mxfp4.gguf -ngl 99 -c 45056 -b 2048 -ub 2048 -npp 512 -ntg 128 -npl 1,8,32,64
```
