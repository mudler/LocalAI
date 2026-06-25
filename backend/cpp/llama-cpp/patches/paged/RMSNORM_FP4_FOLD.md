# RMSNORM_FP4_FOLD.md - ceiling-critic verdict (label ceiling-critic, READ-ONLY, no GPU)

Completeness audit of the post-0022/0023 bit-exact decode surface: is the rms_norm -> fp4
producer-fold the BEST remaining bit-exact decode lever, or is something better being missed?
Source: all paged/*.md verdicts + the 0019/0021/0023 patch diffs (local, read-only). No GPU touched.

## Starting line (post-0023)
- Dense q36-27b-nvfp4: 373.2 t/s @ npl128 = 95.4% of vLLM 391. Dense is UNTOUCHED by 0023.
- MoE q36-35b-a3b: 758 t/s @ npl128 (0023 +1.73%).
- Decode = ONE replayed CUDA graph, single stream, 99.94% GPU-busy, 0.06% idle. Removed/folded
  kernel GPU-time cuts wall 1:1, and DISJOINT folds STACK 1:1 (each removes a distinct kernel).
- gated_delta_net recurrence = ~50% of the step, at 84.6% peak BW (past vLLM's 82.4%), PLATEAUED.

## TIER 0 - confirmed NO bit-exact lever (dead, do not pursue)

(a) GDN recurrence past 84.6% - NO. The 0022 sweep is MONOTONIC toward grid.z=1: 8x4 (grid.z=4,
    32 cols/block) = 79.9%, 16x4/8x8 (grid.z=2) = 82.3%, 16x8/32x4 (grid.z=1, all 128 cols in one
    block = max in-flight independent state-loads per warp) = 84.6%. grid.z>1 is the WRONG direction
    (fewer cols/block = less memory-level parallelism = lower BW), already measured worse. The only
    thing past 84.6% is the float4/vectorized load or a different row-partition, BOTH of which
    repartition which rows a lane sums into the warp-butterfly = a different reduction grouping =
    breaks md5 (the exact f32x4 trap that was explicitly avoided). 84.6% (230.9 of 273 GB/s) is at
    the practical LPDDR5x DRAM ceiling AND past vLLM. No bit-exact decomposition exists. FLOOR.
(b) flash_attn_ext_f16 (3.1%) - NO. 48 CTAs = exactly one full wave, no occupancy headroom, no tail.
    Every grid knob (split-KV / parallel_blocks / ncols / cols_per_block / KV-retile) changes the
    online-softmax running-max/sum RESCALE ORDER across KV blocks = forbidden. FLOOR.
(c) lm_head (nvjet/cublas, 3.1%) - NO. cublas-internal; any algo/kernel swap changes the K-accum
    order vs the current f32 reference = breaks md5. Already tuned. No knob. NO lever.
(d) mul_mat_q FP4 GEMM (~24-27%, the biggest bucket) - NO decode lever. P2a (mmq_y=64 / minblocks=2)
    is bit-exact (1115/805, md5-identical) but MEASURED FLAT on decode (decode mmq -1.1%, stream_k
    fixup +1.7ms = net worse). The -24.7% is a PREFILL large-N asymptotic number; the m=128 decode
    GEMM is LPDDR5x-bandwidth-bound and mmq_y is deliberately bandwidth-neutral. FLOOR.

=> Of the four largest buckets (recurrence 50% + GEMM 25% + lm_head 3% + attn 3% = ~81% of the
   step), NONE has any bit-exact lever left. All remaining headroom lives in the ~12% of small,
   foldable glue/quantize/gather buckets below.

## TIER 1 - the bit-exact-feasible folds, RANKED by ROI (gain / plumbing+risk)

Confirmed bit-exact-foldable buckets from the post-0021/0022 node trace:
- quantize_mmq_nvfp4 ........ 4.5% (dense-foldable ~2.7% ceiling; fold captures ~2-2.5%)
- k_get_rows_float .......... 1.9-2.1% (STILL LIVE post-0021; pure gather)
- pointwise glue ............ ~3.1% (k_bin_bcast 1.7% + silu/sigmoid output-gate 1.4%; ~1.5-2.5% net)

Rank 1 - POINTWISE ACTIVATION FOLD (~1.5-2.5%, MEDIUM plumbing, NO new ABI). Best ROI/risk of the
  three. Fold k_bin_bcast residual-adds + gate-muls and the silu/sigmoid output gate into adjacent
  kernel epilogues/prologues. Pure elementwise f32, same formula+order standalone or folded =
  byte-identical. STRICT EXCLUSION: do NOT re-fold the rms_norm/l2_norm REDUCTIONS (reduction-tree /
  eps-placement trap). No frozen ABI, no GEMM surgery. Well-scoped already (NONRECURRENCE Lever #2).

Rank 2 - rms_norm -> fp4 PRODUCER-FOLD (the proposed lever) (~2-2.5% realistic dense, HIGHEST
  plumbing). LARGEST single clean dense bucket and HIGHEST-confidence ROI (skip-B measured dense
  +2.7% for the whole quantize; the fold removes the f32 round-trip, keeps the quant compute, so
  ~2-2.5%). BIT-EXACT VERDICT: SOUND, and NOT the f32x4-trap class. The trap changed a REDUCTION
  grouping; this fold touches only (i) the sumsq block-reduce, kept BYTE-IDENTICAL, and (ii) the
  writeback, where the post-norm normalize-MUL is pointwise (order-independent, identical out_i for
  any thread partition) and the NVFP4 quant is per-16-consecutive PER-THREAD with NO cross-thread
  shfl (verified in quantize.cu; 0023 already shipped on exactly this property and held the byte
  gate). Re-partitioning the writeback to 16-consecutive-per-thread therefore changes only WHO
  writes/quantizes each element, not the VALUES or the reduction. md5-safe. BUT it carries the worst
  plumbing-to-ROI ratio: 3-op {RMS_NORM,MUL,MUL_MAT(NVFP4)} graph fusion + a mul_mat_q
  prequantized-src1 path + the frozen block_fp4_mmq ABI + a per-call scratch pool. This is the
  LAST-MILE lever, not the first.

Rank 3 - GET_ROWS / STATE-GATHER FOLD (~up to 2%, LOW-MEDIUM plumbing, ZERO reduction risk -
  but UNDER-SCOPED). k_get_rows_float is STILL 7.29-7.32 ms = ~2.1% of the step post-0021/0022; the
  0021 author KEPT the build_rs conv-tap + recurrent-state gathers, explicitly deferring them
  ("tiny; not one of the eliminated buckets"), NOT proving them unfoldable. A gather is a pure copy
  with NO reduction = the SAFEST possible bit-exact fold (the exact property the 0023 dedup
  exploited). Folding the residual build_rs gathers into their consuming kernel (read from cache via
  ids/block-table instead of a pre-gathered f32 scratch, mirroring 0019's gather-free recurrence) is
  bit-exact by construction. Ranked 3 only because the FOLDABLE FRACTION needs a one-pass source
  scoping (some of the 2% may be the "tiny" conv-tap part already); the ROI is lower-confidence than
  Rank 1/2, but the RISK is the lowest of all. THIS IS THE "SOMETHING BEING MISSED": it is a live
  ~2% bit-exact bucket that the current plan does not address.

## IS THE fp4 FOLD THE RIGHT NEXT BUILD?

DEFENSIBLE, but NOT unambiguously the best by ROI. It is the largest single well-understood
bit-exact dense bucket and the verdict is sound (no trap). HOWEVER its plumbing is the highest of
the three folds, and the POINTWISE fold matches its realistic gain (~1.5-2.5%) at MEDIUM plumbing
with no new ABI, while the GET_ROWS fold offers ~2% at the lowest risk (pure copy). The fp4 fold has
the worst gain/plumbing ratio of the candidates.

Recommended build order (all bit-exact, all stack 1:1 on the serial single stream):
  1. POINTWISE activation fold first (cheapest, no ABI, ~1.5-2.5%).
  2. GET_ROWS gather fold second, after a short source-scoping pass (~up to 2%, lowest risk).
  3. rms_norm -> fp4 producer-fold LAST (the high-plumbing last mile, ~2-2.5% dense), built only if
     the remaining gap to the chosen target still justifies the ABI/graph-fusion surgery.
If the workflow insists on a SINGLE decisive lever and accepts the plumbing, the fp4 fold is the
biggest one and a legitimate choice - but it should be sequenced after the cheap folds, not before.

## HONEST BIT-EXACT CEILING

The three folds remove DISJOINT kernels on a 99.94%-busy serial stream, so they STACK:
  ~2-2.5% (fp4) + ~1.5-2.5% (pointwise) + ~2% (get_rows) = ~5.5-7% gross on dense.
  373 t/s + ~6% = ~393-399 t/s = ~100-102% of vLLM 391.
=> The bit-exact dense ceiling is vLLM PARITY-to-slightly-ahead (~100%), NOT 95%. Declaring the
   ceiling at ~95% would leave ~4-5% of identified, bit-exact-FEASIBLE fold headroom unbuilt.
   Realistic SHIPPABLE ceiling (fold inefficiency + the realistic-vs-ceiling haircut + some buckets
   resisting clean folding): ~98-100% of vLLM dense. The recurrence (50%) is already past vLLM and
   at the DRAM floor; attention/lm_head/mul_mat_q have no bit-exact lever; everything left is the
   ~6% of small folds above. There is no fourth large bit-exact lever hiding anywhere.

Caveat that frames the whole result: vLLM 391 is a LOWER-precision reference (w4a4/w4a16 acts vs
llama's q8_1; the recurrence is algebraically reassociated). Bit-exact-vs-vLLM is IMPOSSIBLE; the
only meaningful cross-engine bar is throughput + top-1/KL, and llama at 373 (95%) bit-exact f32 is
already doing strictly MORE precise arithmetic at near-equal throughput. Closing the last ~5% with
the folds reaches throughput parity at higher precision - a strong result, but each fold is a
diminishing 1.5-2.5% at rising plumbing. The bf16-state over-clock (shelved) is the only thing that
goes materially AHEAD, and it is non-bit-exact (KL-gated), out of scope for this gate.

Assisted-by: Claude:opus-4.8 [Claude Code]

====================================================================================================

# RMS_NORM -> NVFP4 PRODUCER-FOLD - PRECISE IMPLEMENTATION DESIGN (label fold-design, READ-ONLY, no GPU)

Design-only, no GPU. Reads: DGX `~/llama-paged-dev` HEAD f7409c2 (patch 0023) + `git stash@{0}`
(trackA1-prequant-nvfp4-fused-rmsnorm) + norm.cu/quantize.cu/mmq.cu/mmq.cuh/ggml-cuda.cu/qwen35.cpp.

## 0. One-line verdict
The fold is bit-exact-FEASIBLE, BUT the Lever-2 stash that exists as the starting point is
(a) almost certainly bit-INEXACT and (b) was measured FLAT. The single mandatory fix is the
reduction block_size dispatch; the single thing that makes it not-flat is de-dup-across-siblings
+ skipping the dead f32 write at the FFN boundary. Build the FFN boundary first, gate on a measured
per-call producer-vs-removed-quantize win before extending. Honest expectation: ~1.5-2.5% dense
best case, real risk of flat (Lever-2 precedent). Lower-risk alternative in Section 7.

## 1. Which graph nodes fuse
Both boundaries already collapse rms_norm+gain into ONE `rms_norm_f32<bs, do_multiply=true>` kernel
(existing fuse, ggml-cuda.cu:3675). That kernel's f32 output is the byte-exact target.

- FFN (STRONGEST), qwen35.cpp:188-192 + build_layer_ffn:478-487:
  `attn_post_norm = build_norm(cur, RMS)` feeds EXACTLY `ffn_up` + `ffn_gate` (both NVFP4 MMQ at
  m=128). NO non-NVFP4 consumer (residual = pre-norm `cur`; ffn_down eats silu(gate)*up). => the
  f32 normed tensor is DEAD once both GEMMs read fp4 -> producer can skip the f32 write. An existing
  `{MUL_MAT, MUL_MAT, GLU}` fuse (ggml-cuda.cu:3631) already groups up+gate+GLU -> the natural seam.
- GDN/attn (weaker), qwen35.cpp:161 + build_qkvz:228-243:
  `attn_norm = build_norm(inpL, RMS)` feeds `wqkv` + `wqkv_gate` (NVFP4 MMQ, share src1) AND
  `ssm_beta` + `ssm_alpha` (small N=n_v_heads -> MMVQ, READ THE f32). => f32 still live, producer
  MUST write f32 -> smaller win.
- MoE FFN (qwen35moe.cpp) goes via mul_mat_id, already 0023-deduped -> out of scope. Fold = dense only.

## 2. Byte-exact target (norm.cu rms_norm_f32<bs,true>)
Dispatch (norm.cu:304-380): `bs = (ncols < 1024) ? 256 : 1024`, shmem 32*float.
```
for col=tid; col<ncols; col+=bs: tmp += x[col]*x[col];           // (R1) strided sumsq grouping
tmp = block_reduce<SUM, bs>(tmp, s_sum);                          // (R2) tree width depends on bs
mean = tmp/ncols; scale = rsqrtf(mean+eps);                       // (R3) exact eps/div
for col=tid; col<ncols; col+=bs: dst[col] = scale*x[col]*mul[col];// (W) per-channel gain, mul_col==col
```
(W) is per-column independent (scale block-uniform) -> writeback may be re-partitioned. (R1/R2/R3)
are the ONLY order-sensitive parts and must stay byte-identical.

## 3. Fused producer kernel (quantize.cu) - deltas vs the stash
Start from stash `rms_norm_mul_quantize_nvfp4_kernel` + the factored `quantize_nvfp4_write_subblock`
(verbatim per-thread NVFP4 quant). Required changes:
1. TEMPLATE on block_size + launch `bs=(ncols<1024)?256:1024` (NOT the stash's hardcoded 256). MANDATORY.
2. Reduction pass VERBATIM (R1/R2/R3): scalar strided sumsq, `block_reduce<SUM,bs>`, `mean=tmp/ncols`,
   `scale=rsqrtf(mean+eps)`. Byte-identical once bs matches.
3. Writeback re-partitioned to 16-consecutive-per-thread: `for s=tid; s<n_sub; s+=bs`, col0=s*16,
   `v=scale*xr[col]*mul[col]` (col<ncols else 0), amax=max|v|, `quantize_nvfp4_write_subblock(vals,
   amax, sub, y+ib)`, `ib=k_block*ne11+row`, n_sub=ncols_padded/16. x is re-read (canonical does too).
4. `template<bool write_f32>`: FALSE at FFN (skip `dr[col]=v` -> drop the producer's f32 store),
   TRUE at GDN (beta/alpha read it). THIS is what turns re-bucketing into a real traffic cut.
Buffer ABI frozen: block_fp4_mmq = {uint32_t d4[4]; int8_t qs[128]} = 144B = 9 uint4 = 4*block_q8_1
(mmq.cuh:53). Same layout quantize_mmq_fp4_cuda emits; GEMM stride
s12=ne11*ne10_padded*sizeof(block_fp4_mmq)/(QK_K*sizeof(int)).

## 4. mul_mat_q prequantized-src1 plumbing (mmq.cu/mmq.cuh)
Re-add the stash hook on top of 0023: `ggml_cuda_mul_mat_q(..., const char* src1_prequantized=nullptr)`.
In the NON-ids branch: if non-null, skip quantize_mmq_fp4_cuda + the local pool alloc, point mmq_args
src1_q8_1 at it. GEMM byte-UNTOUCHED (the bit-exactness firewall). 0023 ids-branch untouched (orthogonal).
Sharing across non-adjacent siblings:
- FFN (preferred): extend `{MUL_MAT,MUL_MAT,GLU}` to `{RMS_NORM,MUL,MUL_MAT,MUL_MAT,GLU}` super-fuse;
  one producer (write_f32=false) + one pool buf spanning both GEMMs + GLU, all in one handler. Clean.
- GDN/general: a scratch cache keyed by the normed tensor ptr (graph-eval lifetime); defer until FFN wins.
The stash folds only ONE consumer with a stack-scoped qbuf -> the sibling still standalone-quantizes
(a key reason it was flat; nsys showed quantize 12896->10816, not ->0).

## 5. Bit-exactness argument
(1) NVFP4 quant of each 16-elem sub-block = PURE per-thread function, NO cross-thread shfl/reduction
    (quantize.cu; the exact property 0023 shipped on). => writeback re-partition cannot change a byte.
(2) v=scale*x[col]*mul[col] byte-identical iff scale identical (R1/R2/R3 preserved via bs dispatch)
    AND expression verbatim (left-assoc, scalar). Per-column independent -> partition-invariant.
=> produced block_fp4_mmq bytes == standalone == 0022/0023 baseline; GEMM untouched -> md5 held.
Gate: BATCHED (ne[1]>8) md5 == 5951a5b4 dense + 1115/1115 - NOT just batch=1 (the gate Lever-2 skipped).

## 6. THE TRAP
- block_size trap (the stash's latent bug): canonical = `ncols<1024?256:1024`; qwen35 n_embd is
  1024/2560/4096 (qwen35.cpp:30-31) -> canonical is rms_norm_f32<1024> (LEVER2 nsys confirms). Stash
  hardcodes 256 -> different strided grouping {tid,tid+256,...} vs {tid,tid+1024,...} AND 8-warp vs
  32-warp reduce -> different f32 order -> md5 break. FIX = template+dispatch matching bs.
- f32x4 vectorize trap (recurrence class): do NOT vectorize the sumsq load or align the reduction
  partition to the 16-consecutive writeback. Keep sumsq scalar + strided-by-bs.
- eps/assoc: `rsqrtf(mean+eps)`, `mean=tmp/ncols`, `(scale*x)*mul`. Never reassociate.
- GEMM K-reduction / stream-k / tile loads: forbidden (NONRECURRENCE FORBIDDEN list). Fold only
  changes WHO writes src1.

## 7. Contrast with Lever-2 + lower-risk alternative
Lever-2 (stash) was FLAT (+0.3% dense) and NET-ADDED GPU-time (+2.3% fused vs -1.1% quantize -0.9%
rms_norm) because it (a) folded only 1 of 2 siblings, (b) always wrote f32, (c) bs=256 (wrong AND
non-canonical). It md5'd only batch=1 (fuse off) -> bit-inexactness never caught. The new fold beats
it ONLY with de-dup-both-siblings + skip-dead-f32-at-FFN; without BOTH, expect flat again.
LOWER-RISK alt (recommend evaluating first): dense quantize DE-DUP, no fold - keep the efficient
standalone quantize, quantize the shared normed activation ONCE, reuse for wqkv+wqkv_gate /
ffn_up+ffn_gate (CSE keyed by src1 ptr, the dense analog of 0023). ZERO reduction risk (rms_norm
untouched), much less plumbing; ceiling ~<=1% (redundant half only), which the fold's de-dup half
captures anyway. The fold's only incremental value is the f32 round-trip read, which Lever-2 showed
is easily eaten by the fused kernel's added work.

## 8. Scope + build order (the gate)
Scope dense qwen35: quantize.cu/.cuh (templated kernel + bs dispatch), mmq.cu/.cuh (src1_prequantized
on non-ids path), ggml-cuda.cu (FFN super-fuse, gate: NVFP4 src0 + Blackwell + ne[1]>MMVQ_MAX_BATCH_SIZE
+ ne2==ne3==1 + per-channel gain; flag LLAMA_FUSE_NVFP4_QUANT).
Build order: (1) FFN super-fuse only (write_f32=false + de-dup); measure per-call producer GPU-time
vs the two removed quantizes (nsys node trace, same-build flag toggle); SHIP only if decode_agg
actually lifts AND batched md5==0022/1115. (2) Only if (1) lifts, add the GDN boundary (write_f32=true,
keyed scratch). Realistic: ~1.5-2.5% dense FFN best case; ceiling +2.7% (skip-ALL) is unreachable
(fold keeps quant compute+write). If step 1 is flat, dense quantize is at its bit-exact floor -> stop.

Assisted-by: Claude:opus-4.8 [Claude Code]

====================================================================================================

# RE-PROFILE TARGET MEASUREMENT (label reprofile-target, THE GPU agent) - post-0023, HEAD f7409c2

Fresh node-level nsys re-profile of the DENSE decode to confirm the fold target size, foldable
fraction, critical-path status, and the realistic recoverable ceiling, BEFORE BuildFold commits.

## Build-dir correction (acted on)
The orchestrator framed `build-cuda-base` as the clean 0023 build. It is NOT: empirically
`build-cuda-base` = stale pre-0021 (336.71 t/s), the real post-0023 build is `build-cuda` (371.81 t/s,
git-clean tree, no mmq.cuh P2a remap). All numbers below are from `build-cuda`. (Dense profiling is
unaffected by the 0023 MoE de-dup knob - dense has no MoE.)

## Confirmed baseline
- llama-batched-bench dense q36-27b-nvfp4 npl128 ntg128: 371.81 t/s, 344 ms/decode-step. CONFIRMS the
  ~343 ms / ~373 t/s target. (build-cuda-base stale = 336.71 t/s.)
- nsys --cuda-graph-trace=node, 103 steady windowed steps: step span 345.0 ms, mean kernel-busy 99.0%,
  sum-of-kernels/span = 98.9% (< 100% => no NET overlap; serial single stream, ~1.1% idle).

## Dense decode decomposition (ms/step)
gated_delta_net 168.06 (49.2%) BINDING | mul_mat_q<NVFP4,128> 93.57 (27.4%) |
**quantize_mmq_nvfp4 17.55 (5.1%)** | nvjet 12.02 (3.5%) | flash_attn_ext 11.64 (3.4%) |
ssm_conv 8.56 (2.5%) | k_get_rows_float 7.32 (2.1%) | silu 5.36 | k_bin_bcast(mul) 4.64 |
stream_k_fixup 3.95 | rms_norm 3.53 (1.0%). TOTAL kernel 341.25.

## quantize_mmq_nvfp4 at the dense decode shape (the answer)
- TOTAL: 17.55 ms/step = 5.1% of kernel time = 5.08% of the 345 ms wall. 496 quant calls/step (1 per
  NVFP4 GEMM src1). CONFIRMS the verdict's 17.66 ms / ~4.5-5% (the stray "3.7%" reading was wrong).
- Decomposes EXACTLY by input dim K (graph-verified in qwen35.cpp; 64 layers = 48 GDN + 16 attn):
  - K=5120 (368/step) FOLDABLE: GDN {wqkv, wqkv_gate, beta, alpha} + attn {wq,wk,wv} + both {ffn_up,
    ffn_gate}. All fed by a plain rms_norm+mul (attn_norm or attn_post_norm). beta/alpha CONFIRMED
    foldable: they read the same `cur` as wqkv (qwen35.cpp:359/366).
  - K=6144 (64/step) UNAVOIDABLE: ssm_out (gated-norm = rms_norm + mul(ssm_norm) + mul(silu(gate)),
    two muls break the chain) + wo (attn-gated producer).
  - K=17408 (64/step) UNAVOIDABLE: ffn_down (silu(gate)*up producer).

## Foldable portion (measured) - LARGER than the byte-model 2.7%
The quant kernel is NOT byte-proportional: ffn_down (K=17408) measures 3.62 ms but a byte-model
predicts 5.75 ms. Small-K quants are launch/overhead-bound (flat ~21.7 us floor, K=5120 vs 6144
indistinguishable), so the byte model UNDER-counts the numerous small-K (foldable) calls.
- byte-model FOLDABLE  = 9.73 ms = 2.82% of step
- flat-split FOLDABLE  = 11.90 ms = 3.45% of step  (368 small-K quants, the physically correct one)
- => true FOLDABLE raw GPU-time = 9.7 - 11.9 ms = 2.8% - 3.4% of step. UNAVOIDABLE = ssm_out+wo
  ~2.1 ms + ffn_down 3.62 ms = ~5.7 ms (1.6%).
- Sub-split for the build order: the FFN boundary alone (ffn_up+ffn_gate, f32 DEAD -> cleanest fold)
  = 128 quants/step ~4.1 ms; the input-norm boundary (wqkv/wqkv_gate/wq/wk/wv, +beta/alpha keep f32)
  = ~7.8 ms raw but lower net efficiency.

## Critical path: YES (1:1)
98.9% kern/span, 99.0% busy, single serial stream, no net overlap. The quant kernels are inline on the
serial decode chain; removing their GPU-time cuts the wall ~1:1. Not a gap-fill (there are no gaps).

## Realistic recoverable - and the honest haircut
RAW foldable removed = 9.7-11.9 ms. NET recoverable is LESS, for reasons the fold-design + ceiling-critic
already flagged and this profile does not overturn:
- the fused producer KEEPS the quant compute + the fp4 write (only the f32 round-trip read is saved,
  and the f32 write is droppable ONLY at the FFN boundary where it is dead);
- Lever-2 precedent: the existing stash fold measured FLAT (+0.3% dense) because it folded 1 of 2
  siblings, always wrote f32, and used a non-canonical bs=256 reduction;
- TENSION TO FLAG: the critic cites a skip-B probe of only ~+2.7% for the WHOLE quantize, yet the whole
  quantize is 5.1% on a 98.9%-serial stream (which predicts ~5.1% if cleanly 1:1). Either these small
  kernels are not perfectly 1:1, or the skip probe is unreliable (same class as the NONREC
  garbage-routing skip artifact). This caps the realistic NET nearer the conservative end.
=> Realistic NET recoverable: ~1.5 - 2.5% dense (consistent with fold-design Section 8), real risk of
   FLAT. Optimistic ceiling if the f32 round-trips fully convert: up to ~3% (371.8 -> ~383 t/s); do not
   bank above ~2.5%.

## VERDICT (GPU-measurement view)
- The target is REAL: foldable raw GPU-time 9.7-11.9 ms (2.8-3.4%, slightly LARGER than the prior 2.7%
  byte-model floor), squarely on the single-stream critical path (1:1), bit-exact-FEASIBLE (no precision
  change), and the largest single clean dense bucket left after the plateaued recurrence.
- BUT the NET recoverable is the contested ~1.5-2.5% with a documented FLAT risk, and this fold has the
  HIGHEST plumbing of the three identified folds. Worst gain/plumbing ratio of the candidates.
- RECOMMENDATION: build is DEFENSIBLE but should be SEQUENCED AFTER the cheaper pointwise + get_rows
  folds (per ceiling-critic). If built as the single decisive lever, do the FFN boundary FIRST (cleanest
  ~4.1 ms, f32 dead), gate per-call producer-GPU-time vs the two removed quantizes, and SHIP only if
  decode_agg actually lifts AND batched md5 == 5951a5b4 (1115/1115). Kill-switch: if the only bit-exact
  construction forces re-partitioning the sumsq reduction (changing accumulation order), abort - not
  bit-exact.

Assisted-by: Claude:opus-4.8 [Claude Code]

====================================================================================================

# BUILD VERDICT (label fold-build, THE GPU agent) - post-0023, HEAD f7409c2 = patch 0023

DECISION: NO BUILD. The bit-exact decode ceiling is effectively reached for any lever that justifies
its plumbing. The proposed rms_norm -> fp4 producer-fold is NOT built (it was already built once and
measured FLAT), and the recommended lower-risk alternative (dense quantize de-dup) does NOT have a
clean, contained construction for the portion that matters. Tree left clean at 0023; nothing committed
to the code; this verdict appended only.

I extended the read-only agents' analysis with the two things they could not verify from the .md
verdicts alone: (1) the prior EMPIRICAL fold attempt, and (2) the actual graph/dispatch structure in
the source. Both kill the build.

## 1. The fp4 producer-fold was ALREADY BUILT and measured FLAT (decisive)
LEVER2_PROGRESS.md + stash@{0} (trackA1-prequant-nvfp4-fused-rmsnorm) is exactly this fold. Measured:
  - dense q36-27b-nvfp4 npl128: 333.32 -> 334.44 t/s (+0.3%), npl32 -0.5%
  - MoE   q36-35b-a3b   npl128: 690.23 -> 690.89 (+0.1%), npl32 -0.3%
nsys A/B (fusion fires): quantize_mmq_nvfp4 -2080 inst (-1.1%), rms_norm_f32<1024> -2080 (-0.9%),
NEW rms_norm_mul_quantize_nvfp4 +2080 (+2.3%). NET GPU-time = +0.3%. The fused producer ADDS BACK
the GPU-time it removes - it RELOCATES work, it does not remove it. The +0.3% wall is exactly
consistent with strict 1:1 wall scaling on the single serial stream (reprofile's own model). So the
fold is not a victim of a bad implementation that a rewrite fixes - it is structurally flat: the
producer must still read x, compute sumsq, normalize, quantize and WRITE the fp4 blocks; the only
recoverable traffic is the f32 round-trip, which the fused kernel's extra work eats (Lever-2 proved
this empirically; fold-design Section 7 and reprofile both predicted it). The design's two "fixes"
(de-dup both siblings + skip dead f32 at FFN) do not change this: the skip-f32 saves one f32 write at
the FFN boundary only (~0.5% of step), and the de-dup-both-siblings is item 2 below.

## 2. The dense quantize de-dup is NOT a clean analog of 0023 (the meaningful part is infeasible)
This is the critical finding the read-only agents missed. 0023's MoE de-dup lifted +1.73% because the
redundancy is INTRA-CALL: inside ONE mul_mat_id, the broadcast (ne11==1) up/gate quantize repeats the
SAME token n_expert_used times, all within a single ggml_cuda_mul_mat_q call, so de-dup is a contained
quantize-once + gather with a stack-scoped buffer. NO precedent issue, NO cross-node lifetime.
The DENSE redundancy is INTER-NODE and that is a different, much harder problem:
  - The shared-src1 GEMMs are SEPARATE graph nodes. build_qkvz (qwen35.cpp:228-243) emits wqkv MM,
    reshape, wqkv_gate MM; then ssm_beta MM, reshape, sigmoid; ssm_alpha MM, reshape, add, softplus,
    mul. The four src1-sharing MMs are INTERSPERSED with reshape/sigmoid/softplus/add/mul - they are
    NOT consecutive graph nodes, so ggml's consecutive-op fusion framework cannot match them. A
    contained, single-handler de-dup (the only kind with safe buffer lifetime, like 0023) is impossible
    for the qkvz bucket.
  - De-duping them therefore requires graph-level CSE: recognize 2-4 non-adjacent MUL_MAT nodes share
    src1, quantize once, and keep that pool buffer alive across the intervening nodes until the last
    sibling GEMM consumes it - under CUDA-graph CAPTURE (buffer addresses baked at capture, the pool
    must not recycle the buffer between siblings). This is the SAME high-plumbing scratch-pool +
    src1_prequantized path the fold needs, with real implementation risk (graph-capture
    non-determinism / crashes), and NO precedent in the tree. fold-design's "much less plumbing"
    framing for this alternative is optimistic - the hard part (inter-node buffer sharing under graphs)
    is common to both.
  - The qkvz bucket (the big one, ~192 redundant quants/step ~= 1.4%) is exactly the inter-node case.
  - The ONLY contained, tractable dense de-dup is the FFN {MUL_MAT,MUL_MAT,GLU} (consecutive; build_ffn
    LLM_FFN_PAR). But that existing fusion executes ONLY via ggml_cuda_mul_mat_vec (gated on batch<=8;
    ggml_cuda_should_fuse_mul_mat_vec_q). At npl128 (m=128) it falls through to two separate MMQ nodes.
    Adding an MMQ-path branch to quantize src1 once captures only the FFN redundancy = ~64 quants/step
    ~= 0.5% of the step - below the +-0.3-0.5% bench noise the runs already show, not worth a new
    fusion code path + the risk to the byte gate.

## 3. The pointwise + get_rows folds are not clean wins either
- Pointwise: the cheap ops are ALREADY fused in the tree - {RMS_NORM,MUL(,ADD)} -> rms_norm_fused
  (ggml-cuda.cu:4194/4199), {SSM_CONV,(ADD),SILU} -> ssm_conv (4204/4209), {UNARY(silu/sigmoid/
  softplus),MUL} -> unary_mul (4216). The residual silu 5.36 + k_bin_bcast 4.64 ms is the un-fusable
  remainder inside the GDN gating chain feeding the 50% binding gated_delta_net kernel; GAP_PROGRESS
  measured the whole gating-glue ceiling at only 3.35% and folding further means surgery on the binding
  kernel. Lower-confidence, needs a GPU node-scoping pass - not a clean lever.
- get_rows: 0019 already folded the main recurrent-state gathers; the residual ~2% is an unquantified
  mix of the conv-tap (already deferred as "tiny") and leftovers - under-scoped, not a confirmed win.

## 4. Tree state / gates
- Dev tree clean at HEAD f7409c2 (git diff empty; mmq.cuh/mmq.cu/quantize.cu no uncommitted diff -
  no P2a remap to revert). build-cuda = the clean 0023 build (371.81 t/s dense @npl128, per reprofile).
- No code change made -> no md5 gate needed (baseline 27b = 5951a5b4, 35b = 07db32c2 unchanged).
- No GPU build/bench launched (no buildable candidate clears the ROI bar; re-confirming the baseline
  the reprofile already measured would waste the GPU window).

## 5. FINAL BIT-EXACT CEILING
Dense q36-27b-nvfp4: 371.81 t/s @npl128 = 95.0% of vLLM 391. MoE q36-35b-a3b: 758.1 @npl128 (0023).
This is the bit-exact f32 decode plateau and there is no single decisive bit-exact lever left:
  - gated_delta_net recurrence (~50%) is at 84.6% peak LPDDR5x BW, PAST vLLM (82.4%) - DRAM floor.
  - mul_mat_q NVFP4 GEMM (~27%), flash_attn (~3.4%), lm_head nvjet (~3.5%) have NO bit-exact lever
    (any knob changes a K-/softmax-reduction order vs the f32 reference).
  - The remaining ~5% of small foldable buckets is real GPU-time on the critical path, but the largest
    piece (the fp4 fold, ~1.5-2.5%) is EMPIRICALLY FLAT, the next (dense qkvz quant de-dup, ~1.4%) has
    no clean inter-node construction and shares the fold's flat-risk, and the contained remainder is
    each <=0.5% (FFN de-dup) or entangled in the binding kernel (pointwise) - none clears the
    plumbing/risk bar for a 1:1 single-stream gain that the bench noise floor (~0.3-0.5%) can swallow.
FRAME: vLLM 391 is a LOWER-precision (w4a4) reference; bit-exact-vs-vLLM is impossible. llama at 371.81
bit-exact f32 is doing strictly MORE precise arithmetic at ~95% of vLLM's throughput. The only thing
that goes materially further is bf16 state (precision change, KL-gated, out of scope, shelved).
RECOMMENDATION: ship the 0023 plateau as the bit-exact decode result. Do not build the fp4 fold (flat).
If a future agent insists on the dense qkvz de-dup, it must first build the inter-node graph-CSE
scratch-pool/CUDA-graph-lifetime plumbing and prove on a same-build flag toggle that decode_agg lifts
above the +-0.5% noise AND batched md5 == 5951a5b4 - and should expect the Lever-2 flat outcome.

Assisted-by: Claude:opus-4.8 [Claude Code]
