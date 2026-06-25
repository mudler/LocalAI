# NONRECURRENCE_BITEXACT.md - bit-exact non-recurrence decode levers (label nonrec-design, READ-ONLY, no GPU)

Post-0022 the gated-DeltaNet recurrence is at 84.6% BW = 102.6% of vLLM (3.488 ms/call), past parity.
The remaining ~5% to vLLM lives in the non-recurrence path. Per the node-level decode trace (nsys
`--cuda-graph-trace=node`, clean build, q36-27b-nvfp4 dense, npl128) the decode step is ONE replayed
CUDA graph, ALL kernels on a SINGLE stream (stream 14), strictly serial, 99.94% GPU-busy, 0.06% idle.
That single-stream-99.94%-busy fact is load-bearing for everything below: there is NO overlap, so any
kernel GPU-time genuinely removed (or any kernel folded away) cuts wall-clock 1:1; and conversely, if a
"faster kernel" leaves wall-clock flat, then the kernel did NOT actually get faster at the decode shape.

Post-recurrence-fix kernel mix of the ~367 ms decode step (was 380.4 pre-0022; recurrence now smaller):
- `mul_mat_q` FP4 GEMM (496 calls/step) ~24% (the biggest non-recurrence bucket)
- `quantize_mmq_nvfp4` (496/step) ~4.5%
- `nvjet` lm_head GEMM ~3.1%
- `flash_attn_ext_f16` (16 attn layers) ~3.1%
- elementwise glue: k_bin_bcast (gate mul+add) ~1.7%, unary_gated silu/sigmoid ~1.4%, rms_norm ~0.9%,
  l2_norm ~0.2%, plus conv-state concat_cont/cpy (Lever-1 territory, not in this scope).

Files read on the DGX 0022 tree (HEAD 8a3229f): `mmq.cuh`, `mmq.cu`, `quantize.cu`, `gated_delta_net.cu`,
`fattn.cu`, `fattn-common.cuh`.

---

## RESOLUTION of the P2a puzzle (load-bearing) - mmmq_y=64 / minblocks: bit-exact but FLAT on decode

The existing P2a machinery is two NVFP4-gated, default-stock flags in `mmq.cuh`:
- `GGML_CUDA_FP4_MMQ_Y` (L143-163): overrides the weight-row N-tile `mmq_y` 128 -> 64/96 for NVFP4 on
  Blackwell. mmq_y tiles N (output rows); each weight row lives in exactly one row-tile, so total weight
  traffic is unchanged. **Bit-exact**: the per-output K-reduction is the `for frag` loop in
  `vec_dot_fp4_fp4_mma` (L1097-1108, `sum[...] += C.x[l]`), whose order is independent of mmq_y. md5-
  verified in prior runs (1115/805 gate, byte-identical).
- `GGML_CUDA_FP4_MINBLOCKS` (L205-216): raises the `__launch_bounds__` min-blocks operand (L3579-3585)
  for NVFP4 so >1 CTA co-resides per SM. **Bit-exact**: register allocation / occupancy cannot change
  results.

The paradox restated: P2a made a standalone `mul_mat_q<NVFP4,m=128>` -24.7% faster (bit-exact), yet
decode was FLAT (335->336 post-0020). The trace says decode is 99.94% single-stream busy and mul_mat_q
is ~24% of it, so a -24.7% cut should give ~+6%. RESOLUTION (airtight, from the single-stream fact):

> On a 99.94%-busy single stream, freed kernel GPU-time MUST lower the wall 1:1. Decode is flat =>
> mmq_y=64 did NOT free per-call GPU-time at the DECODE shapes => the -24.7% was measured at a
> NON-decode shape (a single large-N or prefill-M GEMM that runs enough waves to reach asymptotic
> throughput). There is no contradiction; the two measurements are at different GEMM shapes.

Mechanism (grounded in the launch path, `launch_mul_mat_q` L3989-4088): decode runs ONE `mul_mat_q` per
weight with mmq_x=128 fused tokens => ntx=1, and the grid is `nty = N / mmq_y` CTAs (xy-tiling, or
stream-k at nsm=48 when `tiles_efficiency_percent < 90`, L4044-4047). The 496 decode GEMMs have small N:
- FFN up/gate N=17408 -> nty=136 CTAs (mmq_y=128) = ceil(136/48)=3 waves, last wave 40/48=83% full
- FFN down / qkv / o-proj N~5120-6144 -> nty=40-48 CTAs = 1 wave (and eff<90 => stream-k at 48 CTAs)

So EVERY decode GEMM is a 1-3 wave, 40-136 CTA kernel: it is **ramp + tail (wave-quantization) bound**,
dominated by the first-wave weight-load latency before any MMA can start plus the fractional last wave -
NOT by steady-state occupancy. mmq_y=64 doubles the grid (272 CTAs, 6 waves for the fat FFN) which only
helps the ASYMPTOTIC achieved-BW the microbench measures; at 1-3 waves there is no steady state for it
to act over, and each CTA now carries half the arithmetic-per-weight-load so the ramp is relatively MORE
exposed. minblocks=2 is worse: the FP4 MMA is register-bound at ~255 regs/thread (the `(256,1)` bound),
so forcing 2 CTAs/SM register-caps to ~128 regs => heavy spill => net-negative. Both are the in-wave
occupancy lever, and the decode GEMM has no in-wave occupancy problem - it has a too-few-waves problem.

VERDICT: re-test P2a (mmq_y=64, and 96) and minblocks=2 ON TOP of 0022 because it is a FREE one-build
re-test (flags already exist, default stock). **Design prediction: still ~flat (maybe +1-2% from the
one fat-FFN N=17408 GEMM that has 3->6 waves of room; ~0% from the 1-wave thin GEMMs).** The decisive
measurement for the reprofile agent is NOT a standalone microbench - it is the PER-CALL `mul_mat_q`
GPU-time at the REAL decode shapes (the 496 calls), flag on vs off, summed. If per-call decode time
drops, it ships (free bit-exact win). If per-call decode time is ~unchanged (predicted), the -24.7%
was a large-N artifact and the GEMM has no bit-exact occupancy lever - confirming the structural wall.

WHY the decode GEMM has no high-value bit-exact lever: its bottleneck is wave-quantization at a small
grid. The only knobs that change the grid are (a) mmq_y-down [bit-exact, flat per above], (b) mmq_x-down
[FORBIDDEN: re-reads the 18 GB weights ntiles_x times, strictly worse, and pins one-read], (c) the
stream-k-vs-tiling threshold [FORBIDDEN for bit-exactness: stream-k splits each output tile's K-sum
across CTAs and re-adds via the fixup kernel - a DIFFERENT K-accumulation order than one-CTA-full-K
tiling, so flipping the L4047 threshold changes which path a GEMM takes and breaks md5 vs the 0022
baseline]. So at the bandwidth/wave-quant floor for these tiny grids, 3% FP4 efficiency is structural;
no order-preserving change moves it.

---

## RANKED bit-exact non-recurrence levers

Ranked by expected bit-exact decode gain. "Bit-exact-safe" = keeps the exact reduction/FMA order; the
gate is md5-identity to llama 0022 f32 output on both models (dense + MoE), greedy temp0.

### 1. Quantize producer-fold (Track A) - bit-exact-safe - ceiling 4.5%, realistic ~2-2.5%
Fold `quantize_mmq_nvfp4` (4.5%, ~17 ms, 496/step) into the PRODUCER epilogue (the rms_norm / silu that
emits each GEMM's activation), so the f32 activation is quantized to `block_fp4_mmq` directly from the
producer's registers instead of being written to HBM as f32 and re-read by a standalone quantize kernel.
- **Bit-exactness: SAFE, and unusually clean.** `quantize_mmq_nvfp4` (quantize.cu:78-171) computes
  `amax_raw` PER-THREAD over the thread's own QK_NVFP4_SUB=16 values (L108-118) with NO cross-thread
  shfl/reduction (unlike `quantize_mmq_q8_1` which does a warp shfl_xor). Each thread independently runs
  the +/-2 ue4m3 scale search (L120-150) and `ggml_cuda_float_to_fp4_e2m1` packing (L155-166). So the
  output block is a pure per-thread function of its 16 inputs. Copy that arithmetic VERBATIM into the
  producer epilogue and the `block_fp4_mmq` bytes are identical => md5-safe. The only requirement is the
  producer thread-layout owns contiguous 16-element K-sub-blocks (feasible for an rms_norm/silu epilogue).
- **Expected gain:** the win is removing the standalone kernel's f32 activation READ (the producer already
  holds the f32); the quant compute + fp4 write still happen (now folded). So ~the read-half of the 17 ms,
  ~2-2.5% of the step, and it is REAL because the step is single-stream 99.94% busy (no overlap to hide
  the removed kernel).
- **Trap / caveat:** the SPENT "Lever-2" was a DIFFERENT fusion (quantize -> GEMM *consumer* prologue,
  measured net-zero because the GEMM still reads the same activation bytes). Track A is the *producer*
  fold and removes a true f32 round-trip, so it is not subject to that flatness - but it needs real
  producer-kernel surgery + the frozen `block_fp4_mmq` ABI (mmq.cuh:53), more plumbing than the others.
- Ranked #1: largest cleanly-bit-exact non-GEMM bucket, no reduction trap (per-thread quant).

### 2. Activation / op fold - POINTWISE subset only - bit-exact-safe - realistic ~1.5-2.5%
Fold the pure pointwise glue off the single-stream chain into the adjacent kernel's epilogue/prologue:
the GDN residual ADDs and gate MULs (`k_bin_bcast`, ~1.7%), the `silu`/`sigmoid` (`unary_gated`, ~1.4%,
the part that is the output gate, not FFN), and the post-GDN gate MUL after the output rms_norm.
- **Bit-exactness: SAFE for the pointwise ops only.** Add/mul/silu/sigmoid are elementwise fp32 with the
  same formula and the same op order whether standalone or folded => byte-identical. This is the bit-exact
  half of the prior Lever-3 design.
- **THE TRAP (FORBIDDEN half):** the `rms_norm`/`l2_norm` REDUCTIONS must NOT be re-folded with a
  different reduction tree. The standalone `l2_norm_f32<32>`/`rms_norm_f32` use a specific warp/block
  reduction; folding the norm into a kernel with a different `warp_reduce_sum` width or eps placement
  (`x*rsqrt(sumsq+eps)` vs `x/max(sqrt(sumsq),eps)`) changes the last ULP => breaks md5. Fold the MUL that
  FOLLOWS the norm (pointwise, safe); do NOT fold the norm's reduction. (This is the direct analog of the
  f32x4 lane-remap trap that blocked the recurrence's vectorized state loads: any change to a reduction's
  grouping is forbidden.)
- **Expected gain:** ceiling ~3.3% (the Lever-3 slice), realistic ~1.5-2.5% once the norm reductions are
  excluded. Real (single-stream, no overlap), bounded, lower plumbing than #1 (no new ABI).
- Ranked #2: smaller than #1 and the high-value pieces (norms) are off-limits.

### 3. mul_mat_q occupancy retune (existing P2a: mmq_y=64/96, minblocks=2) - bit-exact-safe - ~FLAT
See the P2a resolution above. Bit-exact-safe (N-tiling / register-cap preserve the K-reduction order;
md5-verified). Design prediction FLAT on decode (decode GEMMs are 40-136 CTA, 1-3 wave, ramp/tail-bound;
the -24.7% was an asymptotic large-N number). **Worth the one-build re-test only because it is free**
(flags exist, default stock). Possible marginal +1-2% from the single N=17408 fat-FFN GEMM (3->6 waves).
Measure PER-CALL decode-shape `mul_mat_q` time, not a microbench. Ranked #3: zero plumbing, but low/zero
expected gain - it is the diagnostic that confirms the GEMM wall is structural, not a shippable lever.

### 4. Attention occupancy (flash_attn_ext_f16) - NO bit-exact lever - NO-GO
`flash_attn_ext_f16` is ~3.1% (11.67 ms, 16 attn layers), grid 48 CTAs = exactly ONE full wave on 48
SMs (trace). There is no occupancy headroom (already 1 wave, perfectly filled, no tail) and no in-wave
under-occupancy to fix. The only knobs that change the attention grid are split-KV / parallel_blocks /
a different KV-tile (the `ncols1`/`ncols2`/`cols_per_block` selection in `fattn.cu`), and EVERY one of
them changes the online-softmax running-max/sum RESCALING ORDER across KV blocks => NOT bit-exact
(forbidden, the softmax-rescale analog of the reduction-tree trap). At 3.1% with one full wave the
attention is effectively at floor. Ranked last: no bit-exact lever exists; do not pursue.

---

## FORBIDDEN levers (require a precision or accumulation-order change - excluded by the gate)
- Stream-k vs plain-tiling threshold flip for the GEMM wave-quant tail: splits + re-adds the K-sum across
  CTAs => different f32 accumulation order than one-CTA-full-K tiling => breaks md5.
- Vectorized / lane-remapped tile loads in the GEMM (`load_tiles_nvfp4_nvfp4` / `load_ldmatrix`): any
  remap of which lane holds which K-element changes the MMA fragment->accumulator mapping => can change
  the per-output sum grouping => forbidden (the f32x4 lane-remap trap, same class that blocked the
  recurrence's vectorized state loads).
- mmq_x-down at dense decode: re-reads the 18 GB weights `ntiles_x` times. Order-preserving but strictly
  slower and breaks the one-read invariant; not a lever.
- Folding rms_norm / l2_norm with a different reduction tree or eps placement: last-ULP change => md5 break.
- flash-attn split-KV / KV-retile: changes the online-softmax rescale order => not bit-exact.
- bf16 state / bf16 anything: precision change, SHELVED, forbidden by the gate.

---

## One-line summary for the parent
The remaining non-recurrence decode gap has NO single big bit-exact lever. The largest cleanly bit-exact
win is the **quantize producer-fold (Track A, ~2-2.5%, the per-16 NVFP4 quant has no cross-thread
reduction so it copies verbatim into the rms_norm/silu epilogue)**; second is the **pointwise activation
fold (~1.5-2.5%, fold the residual adds / gate muls / silu but NOT the norm reductions)**; the
**mul_mat_q occupancy retune (P2a mmq_y/minblocks) is bit-exact but predicted FLAT** (decode GEMMs are
small-grid wave-quant/ramp-bound, so the -24.7% asymptotic number does not apply per-call - confirmed by
the airtight single-stream-99.94%-busy logic, re-test only because the flag is free); and **attention has
NO bit-exact lever** (already one full wave; every grid knob changes the softmax rescale order). The
P2a puzzle is resolved: not a contradiction - the -24.7% and the flat decode are simply at different GEMM
shapes (large-N asymptotic vs 1-3-wave decode per-call).

Assisted-by: Claude:opus-4.8 [Claude Code]

---

# EMPIRICAL P2a RE-TEST ON 0022 (label reprofile-puzzle, GPU agent) - measured, build + bench + nsys

The design section above PREDICTED P2a flat from the single-stream logic. This section is the GPU
measurement that CONFIRMS it byte-for-byte, plus one load-bearing correction: an early "+11% decode"
A/B was a STALE-BASELINE artifact, not the flag. Box: DGX GB10 (sm_121a), HEAD 8a3229f (patch 0022),
SM+MEM clock pinned 2190 MHz (verified via `nvidia-smi dmon`, identical base vs flag - NOT a clock story).

## (1) Fresh node-level decode decomposition (nsys --cuda-graph-trace=node, dense q36-27b-nvfp4, npl128)
Per-instance trace windowed to one steady decode step (103 steady steps, step = 48 GDN-layer boundaries):

  Committed-default build (build-cuda-base, 336 t/s @128) -- step span 383.1 ms, kernel-busy 99.24-99.30%:
    gated_delta_net (SSM recurrence)   193.97 ms/step   51.0%   <- BINDING KERNEL
    mul_mat_q<NVFP4,m=128,nc=0>         93.64 ms/step   24.6%   <- the P2a target
    quantize_mmq_nvfp4                  16.77 ms/step    4.4%
    nvjet (cublas lm_head GEMM)         12.07 ms/step    3.2%
    flash_attn_ext_f16                  11.69 ms/step    3.1%
    concat_cont 8.14 / cpy_scalar 7.49 / k_get_rows 7.29 / ssm_conv 6.55 / silu 5.32 / k_bin_bcast 4.67
    mul_mat_q_stream_k_fixup 3.95 / rms_norm 3.56 / ... ; SUM 380.1 ms = 99.24% of the 383.1 ms wall.

  conv-inplace + GDN(16,8) build (the 374 t/s state) -- step span 345.3 ms, kernel-busy 99.0%:
    gated_delta_net 167.99 (49.2%), mul_mat_q<NVFP4,128,0> 93.79 (27.5%), quantize 17.66 (5.2%),
    nvjet 12.05 (3.5%), flash_attn 11.66 (3.4%), ssm_conv(fused update) 8.44 (2.5%), k_get_rows 7.32 (2.1%).

  BINDING KERNEL = gated_delta_net (~49-51% of the step) in BOTH; mul_mat_q<NVFP4,m=128> is #2 (~25-27.5%).
  Decode is ~99.0-99.3% GPU-busy single-stream (confirms the 99.94% claim; ~0 idle, strictly serial).

## (2) P2a A/B - the -DGGML_CUDA_FP4_MMQ_Y=64 nwarps-remap, re-applied + built + bit-exact-gated on 0022
The committed 0022 machinery was PARTIAL (patch 0017 templated get_mmq_y_device<type> but left
mmq_get_nwarps_device() stock -> mmq_y=64 + nwarps=8 fails static_assert nwarps*tile_C::I==mmq_y at
mmq.cuh:3280). Re-derived the full threading: templated mmq_get_nwarps_device<type>() -> mmq_y/16 (=4)
for NVFP4+flag; type-aware mmq_get_nwarps_host(...,type); threaded <type> through the NVFP4 loader (998),
write_back_mma (3266), process_tile (3500), mul_mat_q launch_bounds (3579/83/85) + body (3602),
stream_k_fixup launch_bounds (3832) + body (3843), 2 host launch sites (3994/4172). Reverted after.

  cuobjdump proof the flag took effect: mul_mat_q<NVFP4,m=128,nc=0> STACK 112 -> 56 (256-thr/8-warp CTA
  -> 128-thr/4-warp CTA => 1 -> 2 resident CTAs/SM). REG 255 (HW-capped), no new spill.
  BIT-EXACT GATE (HELD): test-backend-ops MUL_MAT 1115/1115, MUL_MAT_ID 805/805; greedy md5 base==flag
  IDENTICAL = 5951a5b4d624ce891e22ab5fca9bc439 (matches the prior P2a gate hash). Byte-identical output.

  CLEAN A/B (same build dir, ONLY mmq.cuh toggled => non-mmq .o byte-identical; back-to-back, pinned clocks)
  S_TG t/s, llama-batched-bench -fa on -npp128 -ntg128:
    DENSE q36-27b:   npl 32  208.02 -> 207.51 (-0.2%)   npl 128  374.30 -> 373.19 (-0.3%)   FLAT
    MoE  q36-35b-a3b: npl 32  438.83 -> 439.30 (+0.1%)   npl 128  745.71 -> 745.07 (-0.1%)   FLAT
  Prefill S_PP also flat at 0022 (npp128 1056->1050; npp2048/npl1 1028.85->1024.19).

## (3) RESOLUTION - why FLAT, where the GEMM time goes, and a correction to the prior "-24.7%->+6%" logic
Decode-isolated per-kernel A/B (node trace, same-source toggle, identical non-mmq code):
    gated_delta_net          167.99 -> 167.89 ms/step  (IDENTICAL - it is byte-identical code, untouched)
    mul_mat_q<NVFP4,128,0>    93.79 ->  92.74 ms/step  (-1.1%, FLAT)            <- the P2a target, decode shape
    mul_mat_q_stream_k_fixup   3.96 ->   5.65 ms/step  (+1.7ms, REGRESSES at nwarps/2=2)
  => the decode mmq FAMILY is flat-to-slightly-WORSE; the flag delivers ~nothing at the m=128 decode shape.

The "-24.7%" is REAL but it is a PREFILL-shape number. Full-run aggregate (npp128 ntg128, prefill+decode)
mul_mat_q<NVFP4,128>: 19630 -> 17569 ms = -10.5%; subtracting the flat decode portion (93.8x128 vs
92.7x128) leaves the prefill-shape portion at 7625 -> 5699 ms = -25.3% (matches the prior -24.7%). So the
occupancy lever genuinely cuts the COMPUTE/occupancy-bound prefill-shape GEMM ~25%, and ~0 of the
BANDWIDTH-bound m=128 decode-shape GEMM (it reads the full NVFP4 weight matrix from 273 GB/s LPDDR5x; the
mmq_y knob is deliberately bandwidth-neutral - every weight row still read once - so it cannot move a
bandwidth-bound wall). Confirmed at the SOURCE-of-decode level, not inferred.

Reconciling with "99.94% busy single stream => a -24.7% cut should give ~+6%": the PREMISE is false. The
flag does NOT cut the decode mul_mat_q by 24.7% (it cuts it 1.1%). There is therefore NO freed time on the
99% busy stream - so the "where does the freed time go (idle gaps?)" question is moot: no time is freed at
the decode shape. The contradiction dissolves: mul_mat_q IS on the critical path AND single-stream-busy, but
the lever simply doesn't accelerate the decode-shape invocation. (Net it slightly hurts via stream_k_fixup.)

CORRECTION to an earlier in-session A/B (recorded so the parent does not chase it): a first pass showed
build-cuda-base 334.6 -> "flag" 372 (+11%). That was a STALE-BASELINE artifact, NOT the flag. build-cuda-base
(binaries 18:46) was compiled from a pre-0021 source - it has NO ssm_conv_update_f32 (cuobjdump symbol count
0 vs 4 in the 0022 build) and the un-retuned GDN default (gated_delta_net 194 vs 168 ms/step). Those ~40 ms
of non-mmq differences (conv fuse ~14 ms + GDN ~26 ms) are the entire 334.6->373 gap. With a correct
same-source baseline (toggle ONLY mmq.cuh in one build dir) the flag is flat (373.19 vs 374.30). Lesson:
the only valid P2a A/B holds every non-mmq .o byte-identical; comparing two independently-built trees mixes
in whatever other flag/patch state each was built from.

## VERDICT
P2a (mmq_y=64 nwarps-remap) is BIT-EXACT (md5-identical, 1115/805) and a genuine ~25% PREFILL-shape FP4-GEMM
kernel win, but it is FLAT on decode (dense and MoE, npl 32 and 128) on 0022, AND flat on end-to-end prefill
S_PP at 0022 (prefill is GDN/other-bound at these sizes, not mmq-bound). It is NOT a decode-parity lever and
the decode commit-gate (lift decode_agg) is NOT met -> do NOT ship for decode. The binding decode kernel is
gated_delta_net (~50%); the only decode levers left are the bit-exact folds in the design section above
(quantize producer-fold ~2-2.5%, pointwise activation fold ~1.5-2.5%) and the GDN-region launch/fusion that
vLLM already has. The mmq P2a machinery was reverted; the 0022 tree is left git-clean.

Assisted-by: Claude:opus-4.8 [Claude Code]

---

# nonrec-build (GPU agent) - built + measured. Lever shipped: MoE NVFP4 quantize de-dup (patch 0023)

Box: DGX GB10 (sm_121a), baseline = clean rebuild of HEAD 8a3229f (patch 0022) in build-cuda
(verified: mmq.cu.o rebuilt from clean source; the A/B-left binary was stale). md5 references
locked: q36-27b-nvfp4 5951a5b4d624ce891e22ab5fca9bc439, q36-35b-a3b-nvfp4 07db32c2bcb78d17a43ed18bc22705cd.
Baseline decode S_TG: dense 208.7/373.6, MoE 441/746 (npl 32/128). ncu unavailable (no
GPU-counter permission, no sudo) -> all verdicts are nsys + back-to-back same-build A/B.

## Levers EVALUATED

### A. quantize_mmq_nvfp4 occupancy retune (token-packing) - BIT-EXACT, FLAT -> not shipped
The decode quantize at the K=2048 shape is grid (128,1,1) = 128 CTAs = ~2.67 waves on 48 SMs.
Unlike mul_mat_q (bandwidth-bound on LPDDR5x, so P2a was flat), quantize moves trivial memory,
so I tried packing TPB token-rows per CTA (blockDim.y) to cut wave-quant - each thread still
quantizes its own 16 consecutive values, so byte-identical (md5 5951a5b4/07db32c2 held at TPB
1/2/4, after fixing the output ib index to use the token i1 not blockIdx.x). Result: DENSE npl128
DEAD-FLAT 373.25 across TPB 1/2/4; npl32 and MoE flat-to-slightly-WORSE at TPB>1. The decode
quantize is at its best config already (TPB=1 = max CTA parallelism = best latency hiding;
fewer/bigger CTAs hurt). Second bit-exact occupancy lever (after P2a) proven flat. Reverted.

### B. skip-ALL-quantize probe (NON-bit-exact, diagnostic) - the +30% MoE number is an ARTIFACT
Skipping quantize_mmq_fp4_cuda entirely (garbage buffer, FP4-MMA timing data-independent) showed
DENSE +2.7%/+3.7% (npl128/32) but MoE +29.9%/+43.9%. The MoE figure is NOT a valid ceiling: the
garbage activation also corrupts the router (ffn_gate_inp) quantize -> degenerate topk expert
selection -> less / better-localized expert work -> artificially fast. The authoritative
decode decomposition (nsys --cuda-graph-trace=node, npp8 ntg128 npl128) shows quantize is only
3.7% of MoE decode GPU-time, not 23%. Dense +2.7% IS real (rms_norm-fold territory, see D).

### C. SHIPPED - MoE NVFP4 activation-quantize de-dup (patch 0023) - BIT-EXACT, lifts decode+prefill
ggml mul_mat_id quantizes the gathered rows ne11_flat = ne12*n_expert_used. For the broadcast
up/gate proj (ne11==1) every expert of a token sees the SAME token activation, so stock
re-quantizes each token n_expert_used (=4 here) times. quantize_mmq_nvfp4 has NO cross-thread
reduction (per-16-element per-thread), so the gathered blocks are byte-identical across experts.
Lever: quantize the ne12 unique tokens once, then gather the block_fp4_mmq rows into the
expert-gathered layout with a coalesced uint4 copy (block_fp4_mmq = 9 uint4 = 144 B). GEMM
untouched; down_proj (ne11==n_expert_used, distinct) keeps stock.
- Gather v1 (per-thread 144 B struct copy) was UNCOALESCED: gather 478 ms ate 84% of the 570 ms
  quantize saving -> flat. Gather v2 (coalesced uint4, output written contiguously) = 32 ms.
- nsys decode-isolated: quantize_mmq_nvfp4 868 -> 457 ms/run (-411 ms), gather +32 ms, net -379 ms.
- DECODE S_TG: MoE npl128 745.2 -> 758.1 (+1.73%), npl32 +0.6%. PREFILL T_PP -4%. DENSE byte-flat.
- BIT-EXACT GATE (default on): q36-27b 5951a5b4 (unchanged), q36-35b-a3b 07db32c2 (on==off==0022);
  test-backend-ops MUL_MAT 1115/1115, MUL_MAT_ID 805/805. On by default; GGML_CUDA_MOE_QUANT_DEDUP=0
  restores stock. Committed: DGX f7409c2 + worktree patch 0023.

### D. NOT built - dense quantize producer-fold (rms_norm -> fp4) - real but ~2.7%, needs graph fusion
Dense decode quantize is ~2.7% (skip B, real). Folding it into the rms_norm+mul producer is
bit-exact-feasible (keep the strided sumsq reduction byte-identical, re-partition only the
writeback to 16-consecutive-per-thread + the verbatim per-thread quant) but requires a 3-op
{RMS_NORM,MUL,MUL_MAT(NVFP4)} graph fusion hoisting the GEMM into the producer node and a
mul_mat_q pre-quantized-src1 path (the scratch is a per-call pool buffer). High plumbing for
~2.7% dense only; left for a follow-up. mul_mat_q (bandwidth wall), flash_attn (softmax rescale
order), lm_head (cublas) have NO bit-exact lever.

## Verdict
The non-recurrence path has ONE shippable bit-exact decode lever found and built: the MoE
quantize de-dup (0023, +1.73% MoE npl128 decode + 4% prefill, dense untouched, byte-identical).
It is the only redundant-work bucket; the rest of the non-recurrence kernels are at their
bit-exact floor (mul_mat_q bandwidth-bound, quantize occupancy-flat, attention softmax-locked).
The remaining bit-exact headroom is the dense rms_norm->fp4 producer-fold (~2.7% dense, graph-
fusion surgery, not built) and then bf16 state (precision change, shelved) - no other bit-exact
lever moves the LPDDR5x-bandwidth-bound, recurrence-dominated (~50%, past vLLM parity) decode wall.

Assisted-by: Claude:opus-4.8 [Claude Code]
