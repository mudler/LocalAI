# PREFILL_GEMM_SCOPE - large-M NVFP4 expert/dense GEMM (design only)

**Status: DESIGN + PLAN ONLY. No kernel written, no GPU run in this pass.**
This scopes the #1 prefill lever for `llama-cpp-localai-paged`: the NVFP4 weight
GEMM at large M (prefill), where llama.cpp's `mul_mat_q` (MMQ) NVFP4 path is far
slower than vLLM's `marlin_moe_wna16` (MoE) + cutlass/nvjet (dense). Per the
prefill ground-truth that motivated this scope, the GEMM bucket is ~232 us/tok
(paged) vs ~68 us/tok (vLLM) - 3.4x slower, ~51% of the paged-vs-vLLM prefill
gap (164 us/tok).

> **Regime warning (read first).** Every "GEMM is at the BW floor / ties vLLM"
> conclusion in `README.md` section 5 is a **DECODE** finding (M<=128,
> bandwidth-bound). This document is about **PREFILL** (large M, compute /
> tensor-core-throughput bound) - a different regime, which is exactly why the
> rejected "W4A16-Marlin MoE GEMM" lever is revisited here **for prefill only**.
> The 232/164/68 us/tok prefill bucket came from the prefill ground-truth that
> commissioned this scope and is **not** in a committed in-repo profile (the
> committed profiling - `GAP_PROGRESS.md` etc. - is decode-focused). Per the
> "profile-don't-assume" rule in `.agents/vllm-parity-methodology.md`, **step 0 of
> any build is to re-confirm the prefill GEMM bucket on GPU** (nsys, prefill-only
> window) before touching code.

---

## 1. Why `mul_mat_q` is slow at large M (confirmed from source)

Source: `ggml/src/ggml-cuda/mmq.cu`, `mmq.cuh` at this backend's pin (`9d5d882d`).

MMQ is built for the **M<=128 decode tile**. Three structural facts from the code:

1. **The M (column/token) tile is capped at 128.**
   `get_mmq_x_max_host()` / `get_mmq_x_max_device()` (mmq.cuh ~108-140) return
   `128` on Blackwell (`turing_mma_available(cc)`), and the host launch loop
   (mmq.cuh ~4237) picks `mmq_x_best` only to *minimise the column-tile count for
   `ncols_max`, never exceeding `mmq_x_max`*. So a prefill ubatch of M=512 (or
   4096) tokens is processed as many `mmq_x<=128` column-tiles. The compile-time
   accumulator tile is `mmq_x`-wide; there is no large-M (e.g. 256-wide) tile
   variant. The whole tile-selection machinery exists to pick a *small* tile for
   *small* batches, not to grow for large ones.

2. **The FP4-MMA kernel is register-bound to 1 CTA/SM.**
   `mul_mat_q` for FP4 is `__launch_bounds__(warp_size*nwarps, min_blocks=1)`
   (mmq.cuh ~3579-3585), i.e. 256 threads, 1 resident block/SM (~255 regs/thread).
   The patch-0017 comment in-tree states this plainly: the kernel is
   "REGISTER-bound to 1 CTA/SM ... the under-occupancy that strands the kernel at
   ~3% of FP4 peak at M=128." At large M the work per tile is bigger, but with one
   CTA/SM the tensor cores still stall on LPDDR5x / shared-memory weight loads
   with no CTA-level latency hiding - the design has no async multi-stage global->
   shared pipeline (cp.async double-buffering) that large-M GEMMs need.

3. **Per-tile fixed overheads amortise poorly only because the tile stays small.**
   Each tile re-stages weights into shared memory, runs the `MMQ_ITER_K_FP4=512`
   K-loop, and the activations are quantized to Q8_1 (`quantize_mmq_fp4_cuda`,
   block_fp4_mmq = FP4 weights x int8 activations). For decode this is the right
   trade (FP4 weight traffic is the bottleneck). For large-M prefill the GEMM is
   compute-bound, so the right structure is big tensor-core output tiles (e.g.
   128x256), a deep async load pipeline, and full SM occupancy - exactly what
   cutlass 3.x / nvjet (cuBLAS) and marlin implement and MMQ does not.

Patch 0017 already proved every *cheap* large-tile/occupancy lever inside MMQ
(`GGML_CUDA_FP4_MMQ_Y`, `GGML_CUDA_FP4_MINBLOCKS`) is a no-win on GB10 - because
the limit is the small-tile kernel *structure*, not a tunable. To win at large M
you must leave MMQ for a large-M kernel.

---

## 2. Options (feasibility / bit-exactness / effort)

### Key enabling facts already in the tree

- **NVFP4 -> bf16/f16 dequant kernels already exist.** `convert.cu` defines
  `dequantize_row_nvfp4_cuda`; `ggml_get_to_bf16_cuda` / `ggml_get_to_fp16_cuda`
  / `ggml_get_to_fp16_nc_cuda` all return it for `GGML_TYPE_NVFP4`. The
  non-Blackwell fallback ("falls back to dequant", README s2) already uses this.
- **cuBLAS on GB10 dispatches to nvjet** (NVIDIA's JIT tensor-core GEMM) - the
  committed profiles already show `nvjet lm_head` and `nvjet non-FP4 cublas GEMM`
  rows. So a dequant->cuBLAS bf16 GEMM lands on a vendor-tuned large-M kernel for
  free.
- **BUT NVFP4 is explicitly excluded from the tensor-core cuBLAS path.** In
  `ggml_cuda_op_mul_mat_cublas` (ggml-cuda.cu ~1659) the `use_fp16` predicate
  begins `src0->type != GGML_TYPE_NVFP4 && ...`. So if NVFP4 reaches cuBLAS today
  it falls to the `else` branch: dequant to **F32** + `cublasSgemm` (**no tensor
  cores**) - useless for prefill. Relaxing this one exclusion (route NVFP4 to the
  bf16/f16 tensor-core branch, where `to_*_cuda(NVFP4)` already exists) is the
  pivot that makes option (a) a few-line change rather than a kernel.

### (a) Dequant -> cuBLAS/cutlass bf16 GEMM for large M  -- RECOMMENDED

Dequant the NVFP4 weights to bf16 (transient pool buffer) once per prefill step,
then a large-M tensor-core `cublasGemmEx` (CUBLAS_COMPUTE_32F accumulate, bf16
inputs). Activations stay bf16 (not Q8_1-quantized).

- **Feasibility: HIGH.** All pieces exist (dequant kernels, cuBLAS bf16 path,
  pool allocator). The only code change for the dense path is (i) make
  `ggml_cuda_should_use_mmq` return false for NVFP4 dense above an M threshold so
  the dispatch falls through to `ggml_cuda_op_mul_mat_cublas`, and (ii) relax the
  `src0->type != GGML_TYPE_NVFP4` exclusion so it dequants to bf16 and uses
  `cublasGemmEx` tensor-core, not f32 Sgemm.
- **Cost model (the crux - why it wins ONLY at large M).** Dequant is one extra
  weight-sized memory pass (read ~0.5B/elt FP4 + scales, write 2B/elt bf16). The
  bf16 GEMM then reads weights as bf16 = **4x the byte traffic of the FP4-MMQ
  read**. At small M (decode) this 4x weight traffic dominates -> bf16-cuBLAS
  loses -> keep MMQ (this is why decode stays FP4-MMQ; consistent with the
  README decode verdict). At large M the GEMM is compute-bound and weight traffic
  is amortised over hundreds of columns, so the 4x is cheap and cuBLAS's mature
  large tiles + async pipeline + full occupancy dominate MMQ's 3%-of-peak small
  tile. The dequant pass itself is ~one weight-read amortised over the whole
  prefill step - negligible at large M.
- **Honest ceiling.** GB10 bf16 tensor-core peak is ~**half** the FP4 tensor-core
  peak. A bf16 cuBLAS GEMM at ~70-80% of bf16 peak is ~35-40% of FP4 peak. That
  is a huge jump from MMQ's ~3% large-M utilisation, but it is **not** automatic
  full vLLM parity (vLLM prefill uses 4-bit weight tiles, staying near FP4-class
  throughput). Expect this to recover most, not all, of the 232->68 gap. See s4.
- **Bit-exactness: NEW FP path** (NVFP4->bf16 round, bf16 TC, f32 accumulate) vs
  fused FP4xQ8_1 MMQ. **Not byte-identical** - gate per-path via KLD exactly like
  the paged-MoE `8cb0ce23` precedent (README s5 / `PAGED_BITEXACT_NOTE.md`). It
  should pass *easily and favourably*: keeping activations in bf16 instead of
  Q8_1 is strictly more precise than the MMQ path, so KLD(dequant-bf16 || f16)
  should be <= KLD(FP4-MMQ || f16). This is a precision-neutral-to-better change,
  not a precision regression like the rejected lever 4.
- **Effort: LOW-MEDIUM (a few days).** Dispatch flip + exclusion relax + an M
  threshold + the KL gate + a prefill bench. No new kernel. Dense first; MoE is
  the harder follow-on (see (c)/plan).
- **Memory note.** Dequant into a *transient* pool scratch per step (do **not**
  cache bf16 weights - a persistent bf16 copy is 4x VRAM for those tensors and
  would erase the backend's "1.5-3x less memory" property). The per-step dequant
  pass is the price of keeping the model FP4-resident.

### (b) Marlin-style fused NVFP4 large-M MoE GEMM (port `marlin_moe_wna16`)

Port vLLM's marlin grouped MoE kernel (4-bit weights, f16 activations, dequant-
in-register, async cp.async pipelines, swizzled layouts).

- **Feasibility: LOW (hardest).** Marlin is a hand-tuned CUTLASS-class kernel and
  is **not NVFP4-aware** (it targets wna16 group-quant, not NVFP4's 16-elt blocks
  with ue4m3 micro-scales). You would either (i) adapt marlin to dequant NVFP4
  in-register and accumulate in f16 (abandoning native Blackwell FP4-MMA), or
  (ii) write a brand-new Blackwell sm_121 FP4-MMA large-M kernel - which is
  essentially re-implementing what cutlass 3.x / nvjet already give you via (a).
- **Bit-exactness:** new FP path, KL-gate (same as (a)).
- **Effort: HIGH (multi-week, high risk),** kernel + layout + Blackwell MMA
  scheduling + graph-safety + the bit-exact gate.
- **Verdict: do NOT start here.** Its only structural advantage over (a) is 4-bit
  weight traffic, which matters only when BW-bound = small M = **decode**, the
  regime already rejected. At large M (a) reaches the same vendor large-M kernels
  for ~1% of the effort. Keep (b) on the shelf as the *only* route to true 68
  us/tok parity if (a)'s bf16 ceiling proves insufficient and the win justifies a
  multi-week kernel.

### (c) M-threshold routing (the integration mechanism for (a))

Not an alternative to (a) - it is *how* (a) is wired. Keep FP4-MMQ for decode
(M<=threshold), switch to the large-M path for prefill.

- **Cleanest hook:** `ggml_cuda_should_use_mmq(type, cc, ne11_or_ne12, n_experts)`
  already receives M (`ne11` dense / `ne12` MoE tokens). Add an NVFP4+Blackwell
  branch: return false when M > `LLAMA_FP4_PREFILL_M` (default e.g. 256-512,
  env/`-D` tunable, default value chosen so default == today's behaviour until
  validated). It is called from both `ggml_cuda_mul_mat` (~2573/2582) and
  `ggml_cuda_mul_mat_id` (~2664), so one edit covers dense + MoE routing.
- **Dense fallthrough is clean:** `ggml_cuda_mul_mat` final `else` ->
  `ggml_cuda_op_mul_mat(..., ggml_cuda_op_mul_mat_cublas, ...)` -> with the
  exclusion relaxed, dequant->bf16->`cublasGemmEx`. Works.
- **MoE fallthrough is NOT clean (the catch):** in `ggml_cuda_mul_mat_id`, a
  false `should_use_mmq` falls to `should_use_mmf` (no NVFP4 support) then to the
  **host-side sorted per-expert loop** with a `cudaStreamSynchronize` (ggml-cuda.cu
  ~2700) - slow and **not CUDA-graph-safe** (it would break the MoE re-graph,
  patch 0025). So MoE large-M needs a *dedicated graph-safe grouped GEMM* (dequant
  the expert-gathered weights to bf16 + `cublasGemmGroupedBatchedEx`, CUDA 12.5+,
  over the existing `expert_bounds`/`ids_dst` sorted layout), not a bare
  fallthrough. This is why the plan ships **dense first, MoE second**.

---

## 3. Recommended approach + implementation plan

**Recommendation: (a) dequant->bf16 cuBLAS, wired via (c) M-threshold routing,
dense-path first, MoE grouped-cuBLAS second. Reject (b).**

### Phase 0 - confirm the bucket on GPU (no code)
- nsys prefill-only window (`-npp <large> -ntg 0/1`, exclude the graph-capture
  step) on q36-27b dense and q36-35b-a3b MoE at the backend pin. Confirm the
  NVFP4 `mul_mat_q` / `mul_mat_id` bucket is ~232 us/tok and that it is
  compute-bound at prefill M (check tensor-core active % low, not BW-saturated).
  If the bucket is not what the ground-truth claims, stop and re-scope.

### Phase 1 - dense large-M NVFP4 -> bf16 cuBLAS (the bankable win)
Files / edits:
1. `ggml/src/ggml-cuda/mmq.cu` - `ggml_cuda_should_use_mmq`: add
   `if (type==GGML_TYPE_NVFP4 && blackwell_mma_available(cc) && ne11 > LLAMA_FP4_PREFILL_M && n_experts==0) return false;`
   (n_experts==0 = dense only in Phase 1). Default threshold == effectively
   disabled until A/B-validated, env/`-D` overridable (mirror the 0017
   `GGML_CUDA_FP4_*` knob style + in-tree comment).
2. `ggml/src/ggml-cuda/ggml-cuda.cu` - `ggml_cuda_op_mul_mat_cublas`: relax the
   `src0->type != GGML_TYPE_NVFP4` guard in `use_fp16` (prefer a dedicated bf16
   branch: NVFP4 -> `ggml_get_to_bf16_cuda` -> `cublasGemmEx` CUDA_R_16BF /
   COMPUTE_32F, matching the existing BF16 src0 branch for best accuracy).
3. Transient pool scratch for the dequanted weights (reuse `ggml_cuda_pool_alloc`
   as the existing branch does; no persistent allocation).

### Phase 2 - MoE grouped large-M (the harder, higher-value follow-on)
1. New grouped path reached from `ggml_cuda_mul_mat_id` when
   `should_use_mmq`==false for NVFP4+large-M+`n_experts>0`: dequant the
   expert-gathered weights to bf16 and run `cublasGemmGroupedBatchedEx` over the
   existing `expert_bounds` / `ids_dst` sorted layout that `mul_mat_q` already
   builds. Reuse the patch-0023 de-dup'd activation gather where applicable.
2. **Must stay CUDA-graph-safe** - no host sync (do not fall into the legacy
   sorted loop). Validate the MoE re-graph (patch 0025 / `LLAMA_MOE_FORCE_GRAPHS`)
   still captures.

### The bit-exact / KL gate (both phases)
- Greedy md5 on the standard prompt (README s5) to detect *unexpected* divergence
  on the non-prefill paths (must stay == the per-path reference: dense
  `5951a5b4`, paged-MoE `8cb0ce23`). The large-M path itself will differ -> gate
  it by KLD vs the f16 reference, requiring `KLD(new||f16) <= KLD(FP4-MMQ||f16)`
  and PPL within the established band, recorded in `PAGED_BITEXACT_NOTE.md`.
- `test-backend-ops` MUL_MAT / MUL_MAT_ID at NVFP4 **prefill shapes** (large M)
  CUDA0-vs-CPU, plus the existing decode shapes to prove decode is byte-untouched
  (default threshold keeps decode on MMQ).

### The bench
- `llama-batched-bench -fa on -ngl 99` reporting **S_PP** (prefill t/s), swept
  over prefill length and `npl`, A/B with `LLAMA_FP4_PREFILL_M` off vs on, dense
  and MoE, vs stock and vs the vLLM prefill reference. Per-lever A/B discipline
  (`.agents/vllm-parity-methodology.md`): one knob at a time, record the rejected
  threshold values too.

---

## 4. Honest risk + expected speedup

- **Phase 1 (dense) is a tractable routing change, not a kernel project** - days,
  low risk. It reuses existing dequant kernels and the existing nvjet/cuBLAS
  large-M path; the net new code is a threshold + a one-line exclusion relax + a
  KL gate.
- **Phase 2 (MoE) is medium risk** - the grouped-batched cuBLAS wiring +
  CUDA-graph-safety is real work (the bare fallthrough is a slow, graph-breaking
  host loop), but still far short of a from-scratch kernel.
- **Will the GEMM bucket hit 232 -> ~68 us/tok (full vLLM parity)? Honestly, no -
  not from bf16-cuBLAS alone.** bf16 tensor-core peak on GB10 is ~half FP4 peak,
  so the realistic floor for a dequant->bf16 GEMM is ~**90-130 us/tok** (roughly
  35-45% of FP4 peak at ~70-80% of bf16 peak). That recovers ~**60-75%** of the
  232->68 bucket gap = a large prefill win (the GEMM is ~51% of the total prefill
  gap, so closing ~two-thirds of it is a meaningful S_PP improvement), but it
  leaves a residual. **True 68 us/tok parity requires a native FP4-MMA large-M
  kernel (option (b)) - the multi-week project** to greenlight only if Phase 1's
  measured win proves the prefill regime matters enough to fund it.
- **Recommendation:** build Phase 1, measure, and let the measured dense S_PP
  gain decide whether Phase 2 (MoE grouped cuBLAS) and ultimately (b) (native FP4
  large-M kernel) are worth funding. Bank the cheap two-thirds before paying for
  the kernel.

---

## 5. Summary table

| Option | Feasibility | Bit-exact | Effort | Verdict |
|---|---|---|---|---|
| (a) dequant->bf16 cuBLAS large-M | HIGH (parts exist) | new FP path, KL-gate (likely better PPL) | LOW-MED (days) | **RECOMMENDED** (dense first) |
| (b) Marlin/native FP4 large-M kernel | LOW | new FP path, KL-gate | HIGH (multi-week) | shelf - only route to true 68 us/tok |
| (c) M-threshold routing | HIGH | n/a (mechanism) | LOW | **the wiring for (a)** |

Decode is untouched by all of the above (threshold keeps M<=128 on FP4-MMQ); this
is a **prefill-only** lever.
