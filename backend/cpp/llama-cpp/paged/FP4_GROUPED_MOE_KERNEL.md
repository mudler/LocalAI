# FP4 grouped-GEMM MoE kernel (Lever 3) — scaffold + implementation plan

The one piece of work that actually closes the vLLM gap on Blackwell (GB10/sm_121). Both phases are
bottlenecked by the same kernel: `mul_mat_q<MXFP4>` (warp-level `mma.sync` grouped MMQ, ~22 TFLOP/s) is
**37%** of prefill and **54.6%** of decode-at-B=64 GPU time (`BENCHMARKS.md`). Paged attention can't touch
it (proven). The fix is a CUTLASS-3.x collective-mainloop grouped GEMM with block-scaled `e2m1` operands via
tcgen05 tensor-memory MMA — what vLLM/FlashInfer/TRT-LLM use.

## Scaffold (DONE — builds clean, default byte-identical)

Lives in the DGX checkout `~/llama.cpp-pr24423/ggml/src/ggml-cuda/` (to be rebased onto the pin as a patch /
upstreamed). Captured diff: `patches/kernel/0001-fp4-grouped-moe-scaffold.patch`.

- `fp4-grouped-moe.{cuh,cu}` — entry `ggml_cuda_fp4_grouped_moe(ctx, src0, src1, ids, dst) -> bool`
  (true = handled, false = fall back to MMQ). Gated behind env `GGML_CUDA_FP4_GROUPED`. Currently always
  returns false → **default build unchanged**.
- Hook in `ggml_cuda_mul_mat_id` (the MoE dispatch), before the `ggml_cuda_mul_mat_q(...ids...)` call:
  `if (ggml_cuda_fp4_grouped_moe(...)) return;`. Builds via the `file(GLOB "*.cu")` (re-run cmake configure
  after adding the file — GLOB is configure-time).

This is the integration seam. The kernel fills the stub.

## Implementation phases (each: build on GB10 → numerical parity vs `mul_mat_q<MXFP4>` → bench)

1. **Reference grouped GEMM (correctness first, slow OK).** Per-expert problem sizes + offsets from `ids`;
   dequant `e2m1`+scales → BF16; loop CUTLASS (or cuBLAS) per group. Gate: output matches MMQ within fp tol
   on a 2-expert toy + the real model (token-identical greedy). Establishes the harness + the data plumbing.
2. **CUTLASS GemmGrouped, sm_120a, BF16 operands.** Replace the loop with one `cutlass::gemm::device::
   GemmGrouped` launch over all experts (per-group offsets). Measures the grouping win alone.
3. **Block-scaled FP4 operands (the real lever).** `e2m1` A/B with `e8m0`(MX)/`e4m3`(NV) block scales via the
   Blackwell scaled-MMA collective (tcgen05 tensor-memory). This is where the TFLOP/s jumps. Needs CUTLASS
   3.x + sm_120a; verify the block-scale layout matches ggml's MXFP4/NVFP4 packing.
4. **Fuse activation quant** (the F32→FP4 of src1) into the gather/permute prologue.
5. **Enable by default** on sm_120/121 when parity holds + faster; keep the env as an escape hatch.

## Dependencies / decisions

- **CUTLASS is not currently a ggml dependency** (the profile's `cutlass_80_tensorop` is cuBLAS-internal).
  Adding it = submodule/fetch + include dir, gated to CUDA sm_120+. Float the approach with ggml maintainers
  early (Discussion #18369 is the home; JohannesGaessler asked to discuss arch before big kernel work).
- Target sm_120a/121a (consumer Blackwell). Datacenter Blackwell (sm_100) is a separate tile config.
- Risk: needs ncu-driven iteration on the GB10; this is multi-week, expert-CUDA. No upstream base to fork
  (exhaustive search confirmed). Net-new value upstream.

## DENSE follow-up (TODO #28 — important, do before committing to MoE-only)

This kernel is **grouped** (MoE). **Dense** models (e.g. Qwen3 ~27B) use the non-grouped FP4 GEMM path — a
different kernel. Before assuming the kernel work is MoE-only, benchmark **Qwen3-27B dense: vLLM NVFP4 vs
llama.cpp Q4_K_M** (prefill+decode, GB10). If dense shows the same large gap → the kernel track must also
deliver a non-grouped block-scaled FP4 GEMM (a CUTLASS dense GEMM, simpler than grouped). If dense is already
competitive (single-stream dense was only ~10% of MoE-model time) → MoE-grouped is the priority and dense can
ride the existing MMQ/cuBLAS path. This decides the kernel scope.
