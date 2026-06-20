# Blackwell (GB10 / sm_121) kernel gaps — measured + the corrected strategy

Supersedes the "greenfield tcgen05 FP4 grouped GEMM" framing in `FP4_GROUPED_MOE_KERNEL.md`. Research +
profiling reframed the problem: the kernels we need **already exist in ggml**; they're just **untuned for
Blackwell**. And the parity target is far lower than the headline vLLM number implied.

## 1. The parity target was wrong — it's ~3,300 t/s single-stream, not 24,444

vLLM's dense "24,444 t/s" is **aggregate concurrent-batch** throughput, not single-sequence. The GB10
compute roofline caps **single-stream** Qwen3-32B prefill at **~3,300 t/s (BF16/INT8 ceiling)** / **~6,600
(FP4 ceiling)**. So: don't chase 24,444 with one kernel. Aggregate parity = (a kernel at the ceiling) +
(batched-prefill scheduling). The *kernel* job is to reach ~3,300 (matches vLLM, which on GB10 also runs at
the BF16 ceiling) or ~6,600 (beats it, via FP4).

## 2. GB10 per-precision DENSE peaks (measured, not spec)

| precision | dense peak | vs BF16 |
|---|---|---|
| BF16 / FP16 | ~213 TFLOP/s | 1.0× |
| INT8 | ~215 TOPS | **1.0×** |
| FP4 (MXFP4/NVFP4) | ~427–500 TFLOP/s | **2.0×** |

Memory: ~273 GB/s LPDDR5X (the bottleneck for *decode*; prefill is compute-bound). **Critical:** GB10 is
**1:1:2** (BF16:INT8:FP4), NOT datacenter Blackwell's 1:2:4 — **INT8 gives ZERO speedup over BF16 here.** So
int8-MMQ has no precision advantage; only FP4 does. (NVIDIA spec sheets still claim 1:2:4 — contradicted by
direct GB10 measurement; on-the-record discrepancy.)

## 3. Measured gaps (nsys, GB10)

| path | kernel | % of prefill | achieved | % of ceiling |
|---|---|---|---|---|
| **Dense** Q4_K_M | `mul_mat_q<Q4_K/Q6_K>` (int8 MMQ) | 80% | ~46 TFLOP/s | **~21% of 215** |
| **MoE** MXFP4 | `mul_mat_q<MXFP4>` (FP4 MMA) | 37% | ~22 TFLOP/s | **~4–5% of 500** (or ~10% of BF16) |

Both kernels are **engaged correctly but untuned for Blackwell** — llama.cpp's MMQ was "tuned primarily for
RTX 3000/4000" (Ampere/Ada). The headroom (4–5×) is recoverable; it's not an architectural ceiling.

## 4. ggml's current quantized-matmul paths (what exists)

- **MMQ** (int8): quantizes activations to Q8_1, int8 `mma.sync`/`dp4a`. Prefill path. **Untuned for sm_12x.**
- **FP4 MMA** (#17906, merged): native MXFP4/NVFP4 `m16n8k64` block-scaled FP4 mma for cc≥12.0. Works on GB10
  for MoE (we measured 3441 t/s MXFP4 prefill) — but underutilized (~5% of FP4 peak). On **sm_121** it's hit
  by build-flag (`120f`) + nvcc `-O3` miscompile (#18331) + capability-gating issues.
- **dequant→cuBLAS-FP16**: unfused fallback (materializes FP16 weights, round-trips memory). Not a fused
  Marlin. (Our `GGML_CUDA_FORCE_CUBLAS` no-op = this didn't even engage for Q4_K.)
- **NO fused Marlin-style W4A16 kernel** (dequant 4-bit→BF16 in-shared-mem → BF16 tensor cores). Real gap.

## 5. Strategy — match vs beat (this replaces the tcgen05-greenfield plan)

**To MATCH vLLM (~3,300 single-stream): FP4 is NOT required.** Because INT8 == BF16 on GB10, a tuned MMQ and
a BF16 Marlin kernel share the *same* ceiling — and vLLM hits parity via W4A16 Marlin (BF16), since its FP4
is also broken on sm_121.

Ranked, by effort:
1. **Probe: tune the existing int8 MMQ for Blackwell** (dense). Cheapest. We're at 21% of the ceiling —
   recover via tile sizes, async copy (`cp.async`), double-buffered shared-mem pipeline, occupancy. Caveat:
   the `nwarps*tile_C::I==mmq_y` static_assert (found earlier) couples the constants; and the Q8_1
   activation-quant overhead caps pure-MMQ tuning. Bounded upside, but a fast experiment.
2. **Build a Marlin-style W4A16 BF16 GEMM** (dense) — the robust path to ~3,300 (4.3× over today's 765).
   Dequant 4-bit→BF16 in shared memory, MMA on BF16 tensor cores, `cp.async` multi-buffer, offline weight
   reshuffle. Mirrors vLLM's actual GB10 path; keeps activations BF16 (better quality than int8 MMQ); fills a
   genuine ggml gap. **This is the recommended kernel to MATCH.**

**To BEAT vLLM (~6,600, 2×): fix — don't rewrite — the FP4 path on sm_121.**
3. **Get the existing FP4 MMA (#17906/#20644) fully working + tuned on sm_121.** It already works on sm_120
   (RTX 5090: +43–68% prefill) and on GB10 for MoE. The blockers are the `120f` arch flag, the `-O3`
   miscompile (#18331), capability gating — **build/compiler fixes, not a new kernel.** Then tune the FP4 MMQ
   (it's at ~5% of FP4 peak). This is where upstream momentum already is, and the only route past vLLM.

**Dropped:** the from-scratch tcgen05/CUTLASS grouped GEMM (the old scaffold). It aimed past the matchable
ceiling, duplicates work the FP4-MMA path already does, and FP4 on sm_121 is a *fix* problem not a *write*
problem. The `fp4-grouped-moe.cu` scaffold/hook stays as a useful dispatch seam, but the kernel behind it
should be one of (1)/(2)/(3), not a greenfield CUTLASS collective.

## 6. Cheap experiment worth running next

Quantize a **dense** model to **MXFP4/NVFP4** and benchmark prefill: does the existing FP4-MMA path lift dense
from ~765 (Q4_K int8-MMQ) toward the FP4 ceiling, as it does for MoE (3441)? If yes, **dense parity may be a
quantization choice + the existing kernel**, no new kernel — modulo the sm_121 build/miscompile fixes (3).
(Needs an F16 source or a lossy Q4_K→MXFP4 requant for a speed-only test.)

## Sources
GB10 peaks (measured): forums.developer.nvidia.com/t/351993, /360142, /373618. Marlin: github.com/IST-DASLab/marlin,
arxiv 2408.11743, developers.redhat.com Marlin/Machete. MMQ untuned: llama.cpp docs/build.md, discussions/16578,
DandinPower/llama.cpp_bench. FP4 landing/sm121: llama.cpp PR #17906/#20644, issues #19662/#18331. Roofline:
vllm.ai/blog/2026-06-01-vllm-dgx-spark, lmsys.org DGX Spark.
