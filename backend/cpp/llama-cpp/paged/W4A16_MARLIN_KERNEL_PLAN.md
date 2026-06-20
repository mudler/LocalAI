# W4A16 Marlin-style GEMM for ggml-cuda on Blackwell (sm_120/121) — implementation plan

The committed multi-week kernel. Goal: get 4-bit-weight dense matmul to the GB10 **BF16 ceiling (~213
TFLOP/s ≈ ~3,300 t/s prefill on Qwen3-32B)**, ~4.3× over today's 765. This is the *match-vLLM* path; vLLM's
own GB10 dense throughput runs on W4A16 Marlin (its FP4 path is broken on sm_121).

## Why a custom kernel (validated, not assumed)

On GB10 (sm_121), measured: **both** llama-MMQ (int8, Ampere-tuned) **and** cuBLAS-FP16 sit at ~46 TFLOP/s
(~21% of peak). cuBLAS falls back to an Ampere `cutlass_80_tensorop` kernel (CUDA-13 has no sm_121 GEMM for
these shapes); rebuilt with `-DGGML_CUDA_FORCE_CUBLAS=ON` it's *slower* than MMQ (690 vs 750). **No library
path reaches the ceiling on consumer Blackwell** — a hand-tuned sm_120a kernel is required. `mmapeak` measures
the 213 BF16 peak as reachable, and vLLM's Marlin hits it, so the ceiling is real; the work is reaching it.

## What Marlin does (the design we mirror)

Weights stored 4-bit, **dequantized in-register/shared-mem** in-flight; GEMM math on **FP16/BF16 tensor
cores** (`mma.sync m16n8k16`). Speed comes from: `cp.async` global→shared with a **multi-stage double-buffered
pipeline**, **offline weight reshuffle** into the MMA-friendly layout, activations kept resident in registers,
and **Stream-K** partitioning. Sources: IST-DASLab/marlin, arXiv 2408.11743, vLLM machete (Hopper successor).

## Phases (each ends with: numerical parity vs MMQ + a prefill benchmark)

### P0 — Harness + baseline (do first)
- Add a `test-backend-ops` MUL_MAT case for Q4_K/Q4_0 at prefill shapes (M=512/2048) — gives a numerical
  reference and a microbench. Confirm baseline ~46 TFLOP/s.
- Model-level gate: token-identical greedy generation (Qwen3) before/after, like the paged Gate 0.
- Deliverable: a red/green parity check the kernel must pass at every phase.

### P1 — Dispatch seam (no behavior change)
- New `ggml/src/ggml-cuda/marlin-w4a16.cu` + a gated hook in `ggml_cuda_mul_mat` (dense, non-ids path),
  behind `GGML_CUDA_W4A16` + sm_120/121 + type∈{Q4_0,Q4_K}. Initially returns false → falls back to MMQ.
  (Mirror of the `fp4-grouped-moe.cu` scaffold seam.) Builds byte-identical by default.

### P2 — Correctness-first kernel (slow OK)
- Dequant Q4→BF16 (reuse ggml's `dequantize_block_q4_K`) into shared mem, naive `mma.sync m16n8k16` BF16
  accumulate, small tiles. Goal: **bit-parity vs MMQ** (within fp tol) on the toy + the real model. Establishes
  the data plumbing + the harness pass. Not expected to beat MMQ yet.

### P3 — The Marlin pipeline (the speedup)
- `cp.async` double/triple-buffered global→shared; offline weight reshuffle (a one-time repack of the Q4
  tensor into the mma+pipeline layout — likely a load-time transform or a new tensor variant); register-
  resident activation tiles; Stream-K split for the prefill M. Target: ≥150 TFLOP/s (≥~2,300 t/s), then ~213.

### P4 — Tune
- Tile (mmq_x/y analogues), warps, pipeline depth, occupancy. We have nsys (throughput) but **not ncu** on the
  DGX — tuning is empirical (sweep configs, measure t/s). Note ncu would need sudo/driver perms we lack.

### P5 — Enable
- Default on for sm_120/121 + Q4_0/Q4_K dense when parity holds + faster; keep the flag as an escape hatch.
  Ship as a LocalAI llama.cpp patch (the patches/ series) and/or upstream (ggml has no Marlin-equivalent —
  issue #1519 — so it's net-new upstream value; float it with maintainers first).

## Risks / notes
- **Multi-week, expert-CUDA, DGX-only** (GB10 is the only sm_121). The session's network flakiness +
  `llama-cli` hang make `llama-bench`/`test-backend-ops` the reliable verification tools (both work).
- Quantization correctness: Q4_K's superblock structure (256-elem, 6-bit scales) is more complex to dequant
  in-kernel than Q4_0; consider landing Q4_0 first, then Q4_K.
- **Beat-path follow-on:** the FP4-MMA path (`mul_mat_q<MXFP4>`, ~5% of FP4 peak) tuned/fixed on sm_121 reaches
  ~6,600 (2× BF16). Separate track; this W4A16 kernel is the match-path foundation.
- Reuse ggml's `mma.cuh` tile abstractions (MMQ already uses them) rather than raw PTX where possible.
