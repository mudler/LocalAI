# B_MOE_PROGRESS.md - B-3 (mmq_y-down warp-remap, patch 0028) checkpoint

Agent: B3-or-assess (GPU agent, DGX GB10 sm_121). Base: clean 0025 tip (`~/llama-paged-dev`
`2f4f5ab`, branch `b-work`), independent of the held hybrid 0026. Worktree: `.../feat+paged-attention`.

## Prior: B-2 (act-quant retune) = NEGATIVE (no lift, no patch 0027). MoE ~85% of vLLM @npl128.
B-2 proved the act-quant tax (~2%) is already optimally tiled; the structural MoE residual is the
grouped FP4 `mul_mat_q<NVFP4>` GEMM (~27%, LPDDR5x BW floor) + bf16 projections (~10.5%). => try B-3.

## The lever (B-3 / SPEEDUP_HUNT B rank #3)
mmq_y-down warp-remap of the NVFP4 FP4-MMA grouped GEMM `mul_mat_q<NVFP4>` in `ggml/.../mmq.cuh`.
mmq_y tiles the weight-row (N) dimension; lowering 128->64 raises resident CTAs (smaller per-CTA
shared + accumulator + 128 vs 256 threads/CTA => ~2x blocks/SM) to hide LPDDR5x weight-load latency,
WITHOUT re-reading weights (each weight row lives in exactly one row-tile => BW-neutral). The MoE
GEMM runs at ~35% of peak BW (occupancy-limited, NOT BW-saturated), so more resident CTAs is the
right mechanism - and it is the ONE untested occupancy lever (M-tile = NEUTRAL 0015, MINBLOCKS =
+8.7% slower 0017).

## The coupling that makes it a real kernel change (not the 0017 knob alone)
The FP4-MMA path has `static_assert(nwarps*tile_C::I == mmq_y)` (mmq.cuh:3280; tile_C::I==16 for the
m16n8k64 block-scaled FP4 MMA). nwarps is global `256/warp_size = 8`, so mmq_y is pinned at 128. The
0017 `GGML_CUDA_FP4_MMQ_Y` knob alone would TRIP this assert at mmq_y=64. B-3 makes nwarps TYPE-AWARE:
`mmq_get_nwarps_device<type>()` returns mmq_y/16 = 4 for NVFP4-reduced (else stock 8), keeping the
coupling. 2 new overloads (device template + host 3-arg) + 9 call-site swaps to `<type>`. Default
GGML_CUDA_FP4_MMQ_Y==128 returns stock nwarps for EVERY type => default build byte-identical to stock.

## Bit-exactness note (the real risk)
The per-output K-reduction order is mmq_y-INVARIANT (each output row owned by one thread). BUT mmq_y=64
DOUBLES nty (row-tiles), changing the stream-k kbc partition => an output tile's K-range may be split
across CTAs at different points and recombined by `mul_mat_q_stream_k_fixup` in a different grouping =>
FP non-associativity CAN perturb the last logit bits => greedy argmax COULD flip. So B-3 is NOT
bit-exact-by-construction in the md5 sense; the md5 gate is EMPIRICAL. md5 fail => not bit-exact => STOP.

## Status: COMPLETE - BIT-EXACT but FLAT. No patch 0028. Full result + assessment in B_MOE_RESULTS.md.
- [x] Source-read mmq.cuh: nwarps/mmq_y coupling, FP4 MMA vec_dot, kernel+fixup+launch+case sites.
- [x] Edited mmq.cuh: 2 nwarps overloads + 9 `<type>` swaps. git diff clean (37+/11-).
- [x] BEFORE baseline (stock-0025 binaries, same session): dense md5 5951a5b4==ref, moe 07db32c2==ref;
      MoE S_TG npl32=441.98, npl128=756.47.
- [x] BUILD build-cuda @mmq_y=64 (full cuda rebuild): EXIT=0 - compiles (static_assert holds at 4*16=64).
- [x] md5 GATE PASS both models @64; test-backend-ops MUL_MAT 1146/1146, MUL_MAT_ID 806/806 PASS.
- [x] Clean back-to-back A/B (build-cuda-base @128 vs build-cuda @64), 3 reps: npl32 +0.29%,
      npl128 +0.40% - within the ~0.4% noise band. FLAT.
- [x] nsys A/B: grouped GEMM kernel mmq_y=64 -1.3% FASTER, BUT stream_k_fixup +42% costlier + SSM (40%)
      dominant & untouched => end-to-end inert. BW-bound confirmed (same as 0015/0017/B-2).
- [x] DECIDED: FLAT -> no patch 0028. Dev tree reverted to pristine 0025 (no ggml diff), build-cuda
      reconfigured to default + rebuilt. Bit-exact MoE ceiling = ~85% @npl128 / ~87.5% @npl32 of vLLM.
- [x] ASSESS + RECOMMEND (in B_MOE_RESULTS.md): residual = structural Marlin-NvFp4 grouped-GEMM gap,
      uncloseable bit-exactly; fall back to 0026 bf16-SSM opt-in (default-off, fails MoE KL gate, ~95%).

## Gate references
- dense q36-27b-nvfp4 md5 == 5951a5b4d624ce891e22ab5fca9bc439
- MoE   q36-35b-a3b-nvfp4 md5 == 07db32c2bcb78d17a43ed18bc22705cd
- gate cmd: `llama-completion -m M -ngl 99 -fa on -p "The capital of France is" -n 48 --temp 0 --seed 1`
- bench: `llama-batched-bench -m M -c 32768 -ngl 99 -fa on -npp 128 -ntg 128 -npl 32,128` (S_TG=decode_agg)
- vLLM ref decode_agg @npl128 = 882.2 t/s (npl32 ref 500.8).

Assisted-by: Claude:opus-4.8 [Claude Code]
