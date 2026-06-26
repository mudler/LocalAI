# B_MOE_PROGRESS.md - B-2 (down_proj act-quant retune, patch 0027) checkpoint

Agent: B2-build (GPU agent). Base: 0025 tip (DGX `~/llama-paged-dev` `2f4f5ab`, branch `b-work`),
independent of the held hybrid 0026. Worktree: `.../feat+paged-attention`.

## The lever (B-2 / M1)
Bit-exact block/grid/occupancy retune of `quantize_mmq_nvfp4` (the MoE down_proj activation-quant,
~2% of the MoE decode step). `ggml/src/ggml-cuda/quantize.cu`, `quantize_mmq_fp4_cuda` NVFP4 branch.

## Why it is provably byte-identical
`quantize_mmq_nvfp4` maps thread -> column purely through the global linear index
`gy = blockDim.x*blockIdx.y + threadIdx.x` -> `i0_base = gy*QK_NVFP4_SUB`, with NO cross-thread
communication (no shared memory, no warp reduction) and every thread owning a disjoint output
sub-block (its own `sub` slot in `block_fp4_mmq`). So the (thread)->output-byte map - and thus the
produced bytes - are invariant to `blockDim.x` as long as `block_num_y` is recomputed from the SAME
`blockDim.x`. We retune ONLY `blockDim.x`; the per-thread quant body + writeback are untouched.

## Change
`static const int nvfp4_block_size` selected once via env `LLAMA_MOE_QUANT_BLOCK` (default 128 =
baseline; final = measured GB10 winner), `block_num_y` recomputed consistently. ~20 LOC, one TU.

## Status: COMPLETE - NEGATIVE (no lift). Full result in B_MOE_RESULTS.md.
- [x] Branched `b-work` off 0025 (`2f4f5ab`); patch applied to quantize.cu.
- [x] Build clean (llama-completion, llama-batched-bench, test-backend-ops). BUILD_EXIT=0.
- [x] md5 gate @block=128 (default): dense 5951a5b4 == ref, MoE 07db32c2 == ref. MUL_MAT 1146/1146,
      MUL_MAT_ID 806/806 PASS.
- [x] BIT-EXACT proof across block sizes: block 64 AND 256 -> identical md5 both models.
- [x] Sweep block {32,64,96,128,160,192,256}: end-to-end FLAT (npl32 436-438, npl128 749-752, all
      within 0.4% noise). NO block lifts decode.
- [x] nsys quantize_mmq_nvfp4: block=128 is the FASTEST (117.4M ns; 64 +8.7%, 192 +9.9%, 256 +6.9%).
      128 already optimal => ZERO headroom.
- [x] DECISION: no patch 0027 (does not lift). Dev tree reverted to pristine 0025. Recommend B-3.

## Gate references
- dense q36-27b-nvfp4 md5 == 5951a5b4d624ce891e22ab5fca9bc439
- MoE   q36-35b-a3b-nvfp4 md5 == 07db32c2bcb78d17a43ed18bc22705cd
- gate cmd: `llama-completion -m M -ngl 99 -fa on -p "The capital of France is" -n 48 --temp 0 --seed 1`
- bench: `llama-batched-bench -m M -c 32768 -ngl 99 -fa on -npp 128 -ntg 128 -npl 32,128` (S_TG=decode_agg)
- vLLM ref decode_agg @npl128 = 882.2 t/s (npl32 ref 500.8).

Assisted-by: Claude:opus-4.8 [Claude Code]
