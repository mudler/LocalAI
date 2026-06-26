# B_MOE_RESULTS.md - B-2 (down_proj act-quant retune / M1) RESULT: NEGATIVE (no headroom)

Agent: B2-build (GPU agent, DGX GB10 sm_121). Base: clean 0025 tip (`~/llama-paged-dev` `2f4f5ab`,
branch `b-work`), independent of the held hybrid 0026 (`33e7c65`). Lever: SPEEDUP_HUNT.md section B,
rank #2 ("down_proj act-quant retune (M1): bit-exact, bounded - act-quant is ~2% of MoE step").

## VERDICT
**The existing `blockDim.x = 128` is ALREADY the kernel-level optimum for `quantize_mmq_nvfp4` on
GB10 sm_121. B-2 has zero headroom: there is nothing to bake (128 is the current default), and it
does NOT lift MoE decode (end-to-end flat within 0.4% noise across all block sizes). No patch 0027.**
MoE stays ~85% of vLLM @npl128 / ~87% @npl32, well below vLLM => the remaining MoE lever is B-3.

## The change that was built+measured (bit-exact, then REVERTED - did not lift)
`ggml/src/ggml-cuda/quantize.cu`, `quantize_mmq_fp4_cuda` NVFP4 branch. Replaced the hardcoded
`constexpr int nvfp4_block_size = 128` with a `static const int` selected once from env
`LLAMA_MOE_QUANT_BLOCK` (default 128), `block_num_y` recomputed from the SAME `blockDim.x`. ~20 LOC.

### Why ANY block size is provably byte-identical (the bit-exact invariant)
`quantize_mmq_nvfp4` maps thread -> column purely via the global linear index
`gy = blockDim.x*blockIdx.y + threadIdx.x` -> `i0_base = gy*QK_NVFP4_SUB`, with NO cross-thread
communication (no shared memory, no warp reduction) and every thread writing its OWN disjoint output
sub-block (its own `sub` slot in `block_fp4_mmq`: `yqs[2*sub+0/1]`, `d4[sub]`). The per-thread quant
body (amax, the 5-offset fp8-code search, the q0/q1 nibble packing, the writeback) is untouched. So
the (thread)->output-byte map - and the produced bytes - are invariant to `blockDim.x`. Confirmed
empirically: md5 identical at block 64, 128, AND 256, both models.

## GATE (bit-exact) - BOTH MODELS PASS at default AND at non-128 blocks
greedy `llama-completion -ngl 99 -fa on -p "The capital of France is" -n 48 --temp 0 --seed 1`:

| block | dense q36-27b-nvfp4 md5 | MoE q36-35b-a3b-nvfp4 md5 |
|------:|-------------------------|---------------------------|
| 128 (default) | 5951a5b4d624ce891e22ab5fca9bc439 == ref | 07db32c2bcb78d17a43ed18bc22705cd == ref |
| 64 | 5951a5b4...439 == ref | 07db32c2...5cd == ref |
| 256 | 5951a5b4...439 == ref | 07db32c2...5cd == ref |

test-backend-ops (CUDA0): **MUL_MAT 1146/1146 PASS**, **MUL_MAT_ID 806/806 PASS**.

## MEASUREMENT 1 - end-to-end MoE decode_agg (S_TG t/s), the actual throughput
`llama-batched-bench -m q36-35b-a3b-nvfp4.gguf -c 32768 -ngl 99 -fa on -npp 128 -ntg 128 -npl 32,128`,
1 rep/block (run-to-run noise ~0.3-0.5%):

| block | npl=32 S_TG | npl=128 S_TG |
|------:|------------:|-------------:|
| 32 | 437.54 | 750.41 |
| 64 | 437.82 | 751.68 |
| 96 | 437.69 | 749.46 |
| **128 (base/default)** | **438.14** | **751.76** |
| 160 | 436.38 | 750.99 |
| 192 | 436.81 | 751.61 |
| 256 | 437.77 | 750.14 |

Spread: npl32 = 1.76 t/s (0.4%), npl128 = 2.3 t/s (0.3%) - all within noise. **No block size lifts
end-to-end decode.** Expected: the act-quant is ~2% of the MoE step, so even a perfect (0 ns) quantize
kernel caps the end-to-end win at ~2%, and 128 is already optimal => measured 0%.

## MEASUREMENT 2 - nsys kernel-level delta on quantize_mmq_nvfp4 (the meaningful B-2 metric)
`nsys --report cuda_gpu_kern_sum`, MoE, `GGML_CUDA_DISABLE_GRAPHS=1 -npp 4 -ntg 32 -npl 128`,
8,193 kernel invocations (the kernel is 2.0-2.2% of GPU time in this decode-heavy window):

| block | total ns | avg ns | median ns | vs 128 (total) |
|------:|---------:|-------:|----------:|---------------:|
| 64 | 127,523,328 | 15,564.9 | 12,256 | +8.7% slower |
| **128 (default)** | **117,371,424** | **14,325.8** | **11,488** | baseline (fastest) |
| 192 | 128,970,464 | 15,741.5 | 12,032 | +9.9% slower |
| 256 | 125,422,048 | 15,308.4 | 11,936 | +6.9% slower |

**128 is a clean local minimum** (faster than the 64 below and the 192/256 above; 96 and 160 are its
immediate neighbors, end-to-end-neutral, nsys-stats flaked on the re-runs but cannot beat a bracketed
local min). The 7-10% kernel-level regression of the alternatives at 0% end-to-end change is exactly
why end-to-end is flat: this BW-bound, 256-tiny-expert model has no col-tile/occupancy headroom in
the act-quant - the same conclusion patch 0015 reached for the M-tile and patch 0017 for MINBLOCKS.

## WHERE MoE STANDS (decode_agg, this base = 0025 with the re-graph)
vLLM ref @npl128 = 882.2, @npl32 = 500.8.
- npl128: 751.8 / 882.2 = **85.2% of vLLM**
- npl32:  438.1 / 500.8 = **87.5% of vLLM**

B-2 adds 0 (within noise). MoE is **still well below vLLM** => **TRY B-3** (the mmq_y-down warp-remap
on the grouped `mul_mat_q<NVFP4,M-tile=64>` GEMM, ~27% of the MoE step - the only untested MoE GEMM
lever; SPEEDUP_HUNT B rank #3, real kernel change, bit-exact, predicted bounded on this BW-bound
model). The structural MoE residual is the FP4 grouped GEMM at the LPDDR5x BW floor + the bf16
projections (~10.5%); the act-quant tax (~2%) is NOT where the gap lives and is already optimally
tiled. Recurrence (~48%) is already past vLLM (0018-0022).

## DECISION
No patch 0027 (B-2 does not lift; dev tree reverted to pristine 0025). The `LLAMA_MOE_QUANT_BLOCK`
hook + this measurement confirm 128 is the GB10 optimum, should other hardware ever want re-tuning.
Hand off to B-3 (patch 0028) as the next MoE GEMM lever.

Assisted-by: Claude:opus-4.8 [Claude Code]
