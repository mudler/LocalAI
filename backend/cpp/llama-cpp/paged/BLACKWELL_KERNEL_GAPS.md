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

## 6. Cheap experiment — RESULT: MXFP4 dense = free 1.44×, but not parity (kernel still untuned)

Requantized Qwen3-32B dense → MXFP4 (forced attn+ffn to mxfp4 via `--tensor-type`, `--allow-requantize`,
speed-only test) and benched prefill:

| quant | kernel | pp512 | pp2048 | vs Q4_K |
|---|---|---|---|---|
| Q4_K_M | int8-MMQ | 765 | 763 | 1.0× |
| **MXFP4** | **FP4-MMA** | **1099** | **1153** | **1.44×** |

**Findings:**
- **MXFP4 dense is a real, free 1.44× over Q4_K** — just a requantize, the existing FP4-MMA path engages for
  dense weights on GB10. Worth shipping as a **Blackwell dense-quant recommendation** in the gallery (no kernel).
- **But it is NOT parity.** 1153 t/s = **~17% of the FP4 ceiling (~6,600)** / ~35% of the BF16 ceiling. So the
  **FP4-MMA kernel is itself untuned** (consistent with the MoE measurement, ~5% of FP4 peak). MXFP4 moves dense
  from the int8 path (765) onto the FP4 path (1153), but the FP4 kernel leaves ~4–6× on the table.
- **So the kernel work is confirmed and now precise: tune the FP4-MMA kernel** (it's the highest-value, since it
  serves both dense-MXFP4 and MoE, and FP4 is the only path that can *beat* vLLM). Strategy item (3) — fix +
  tune the existing FP4-MMA on sm_121 — is the priority; a Marlin-style W4A16 BF16 kernel (2) is the alternative
  to *match* on the BF16 ceiling if FP4 tuning stalls.

Conclusion: the cheap test did NOT collapse the kernel problem (the kernels are untuned, not just the quant), but
it (a) gives a free 1.44× to ship now, and (b) sharpens the target to **tuning the FP4-MMA kernel**.

## Sources
GB10 peaks (measured): forums.developer.nvidia.com/t/351993, /360142, /373618. Marlin: github.com/IST-DASLab/marlin,
arxiv 2408.11743, developers.redhat.com Marlin/Machete. MMQ untuned: llama.cpp docs/build.md, discussions/16578,
DandinPower/llama.cpp_bench. FP4 landing/sm121: llama.cpp PR #17906/#20644, issues #19662/#18331. Roofline:
vllm.ai/blog/2026-06-01-vllm-dgx-spark, lmsys.org DGX Spark.

> **Correction (measured):** the earlier `GGML_CUDA_FORCE_CUBLAS` env test was a no-op because it's a *compile-time* `#ifdef`, not a runtime flag — cuBLAS never engaged. A real rebuild with `-DGGML_CUDA_FORCE_CUBLAS=ON` shows cuBLAS is **slower** than MMQ for dense Q4 (pp2048 690 vs 750) and runs an **Ampere `cutlass_80_tensorop` FP16 kernel** — cuBLAS-13.0 has no sm_121-tuned GEMM and falls back to sm_80. So *both* MMQ and cuBLAS sit at ~46 TFLOP/s (~21% of the 213 BF16 peak); there is **no library shortcut** to the ceiling on GB10 — a hand-tuned sm_120a kernel (Marlin-style) is required.
