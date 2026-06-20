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

### P0 — Harness + baseline — DONE
- **Correctness gate (GREEN):** `test-backend-ops test -o MUL_MAT -b CUDA0` → **1103/1103 passed** (CUDA vs CPU
  reference, covers Q4_0/Q4_K at the real FFN shapes m=4096,k=14336,n=1..512). This is *the* parity check the
  W4A16 kernel must keep green at every phase — it tests the CUDA MUL_MAT path the kernel will hook. The
  `not supported` lines are `type_b=f16` combos (irrelevant; prefill uses f32 activations).
- **Perf baseline:** `llama-bench` dense Q4_K prefill = **~750 t/s (pp512 718 / pp2048 750) ≈ 46 TFLOP/s ≈ 21%
  of the 213 BF16 ceiling**. The kernel must beat this toward ~3,300. (`test-backend-ops perf -o MUL_MAT` gives
  per-shape GFLOPS too; build it once with the harness.)
- **Op-level baseline (the canonical kernel target), `test-backend-ops perf -o MUL_MAT`, m=4096 k=14336 (FFN):**
  | n (tokens) | q4_0 | q4_K | regime |
  |---|---|---|---|
  | 1 | 817 GFLOPS | 761 GFLOPS | decode / mat-vec (memory-bound) |
  | 8 | 5.77 TFLOPS | 4.11 TFLOPS | small-batch |
  | **512** | **49.5 TFLOPS** | **47.1 TFLOPS** | **prefill GEMM — ~22% of the 213 ceiling** |

  So the prefill GEMM target: lift q4_K n=512 from **47 → toward ~213 TFLOPS** (~4.5×). This per-shape number
  is cleaner than end-to-end for kernel iteration.
- **Harness script:** `~/p0harness.sh` on the DGX (build test-backend-ops + correctness + perf). Reusable each
  phase: `test-backend-ops test -o MUL_MAT -b CUDA0` must stay 1103/1103; the q4_K n=512 perf must climb from 47.
- test-backend-ops needed `-DLLAMA_BUILD_TESTS=ON`; now built in `~/llama.cpp-pr24423/build`.

### P1 — Dispatch seam (no behavior change) — DONE
- `marlin-w4a16.{cuh,cu}` + a gated hook in `ggml_cuda_mul_mat` (dense, non-ids path), behind
  `GGML_CUDA_W4A16` + sm_120/121 (`cc >= GGML_CUDA_CC_BLACKWELL`) + type∈{Q4_0,Q4_K} + f32 activations.
  Returns false → falls back to MMQ. Source + apply instructions: `kernel/w4a16/` (`HOOK.md`).
- **Verified on GB10:** clean build; `test-backend-ops MUL_MAT` = **1103/1103** (byte-identical default);
  `llama-bench` dense Q4 pp512 unchanged (717.77 default / 718.26 with flag); `GGML_CUDA_W4A16=1` reaches the
  seam (stderr `[w4a16] ... P1 seam - using MMQ`) and falls back. The empty frame P2/P3 fills.

### P2 — Correctness-first kernel (slow OK) — DONE
- **Kernel:** `marlin-w4a16.cu` replaces the P1 TODO with a real W4A16 GEMM. In-kernel dequant Q4→BF16 into
  shared mem, `mma.sync.aligned.m16n8k16.row.col.f32.bf16.bf16.f32` via ggml's `mma.cuh` tile abstractions
  (`tile<16,8,nv_bfloat162>` A, `tile<8,8,nv_bfloat162>` B, `tile<16,8,float>` C), F32 accumulate, F32 write.
  One warp per 16(M)x8(N) output tile, K looped in steps of 16. Both src0 (weights, row m) and src1 (acts,
  row n) are row-major `[row][k]`, so A and B load symmetrically via `load_generic`; the mma does the dot over k.
- **Types handled:** Q4_0 and Q4_K. Q4_0 dequant `w=d*(q-8)` inline; Q4_K via the superblock decode mirrored
  from `convert.cu` (`get_scale_min_k4`, 8x32 sub-blocks, `d*q-m`).
- **Shape classes handled:** contiguous 2D GEMM (the prefill path), `ne2==ne3==1`, f32 activations, K%16==0
  (always true: Q4_0 K%32, Q4_K K%256). **Falls back to MMQ (returns false)** for batched (bs!=[1,1]),
  broadcast (nr!=[1,1]), permuted / non-contiguous (per!=[0,1,2,3]), and any non-f32 activation (e.g. f16) -
  keeps the gate green. M / N boundaries are zero-padded in-kernel (handles M not %16, N not %8).
- **Parity (the gate):** `GGML_CUDA_W4A16=1 test-backend-ops test -o MUL_MAT -b CUDA0` = **1103/1103 passed**
  (the Q4_0/Q4_K f32 contiguous shapes run the kernel and match the CPU reference; batched/permuted/f16 fall
  back). Default (flag-unset) build still **1103/1103** (byte-identical, seam returns false).
- **Model sanity / P2 perf:** `GGML_CUDA_W4A16=1 llama-bench -m Qwen3-32B-Q4_K_M.gguf -ngl 99 -p 512 -n 16
  -ub 2048` runs clean: **pp512 = 31.75 t/s**, tg16 = 6.28 t/s. Slow as expected (naive 1-warp/tile, weights
  re-dequantized per n-tile, no pipeline) - this is the correctness checkpoint; P3 brings the speedup. The real
  Q4_K model matmul path engages the kernel without error.

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
