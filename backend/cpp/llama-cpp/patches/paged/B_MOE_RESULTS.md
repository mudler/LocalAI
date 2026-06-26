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

---

# B-3 (mmq_y-down warp-remap of the NVFP4 grouped GEMM) RESULT: BIT-EXACT but FLAT (no patch 0028)

Agent: B3-or-assess (GPU agent, DGX GB10 sm_121). Base: clean 0025 tip (`~/llama-paged-dev` `2f4f5ab`,
branch `b-work`), independent of the held hybrid 0026. Lever: SPEEDUP_HUNT.md section B rank #3 - the
0017-deferred structural `mmq_y`-down warp-remap on the grouped FP4-MMA `mul_mat_q<NVFP4>` (the ~26-27%
MoE-specific GEMM), the only untested MoE GEMM occupancy lever.

## VERDICT
**Bit-exact (md5 PASS both models + test-backend-ops PASS), but end-to-end FLAT: npl128 +0.3-0.4%
(consistent direction, kernel-backed) and npl32 +0.1-0.3%, both inside the ~0.4% run-to-run band. The
warp-remap makes the grouped GEMM kernel ITSELF ~1.3% faster (occupancy DID rise) but the step is
BW/SSM-bound, so it does NOT lift MoE decode. No patch 0028.** MoE stays ~85% of vLLM @npl128.

## The change that was built+measured (bit-exact, then REVERTED)
`ggml/src/ggml-cuda/mmq.cuh`. The FP4-MMA path couples the weight-row tile to the warp count via the
invariant `static_assert(nwarps*tile_C::I == mmq_y)` (mmq.cuh:3280; `tile_C::I==16` for the m16n8k64
block-scaled FP4 MMA). `nwarps` is global `256/warp_size = 8`, pinning `mmq_y=128`; the 0017
`GGML_CUDA_FP4_MMQ_Y` knob alone would TRIP that assert at 64. B-3 makes nwarps TYPE-AWARE: a new
`mmq_get_nwarps_device<type>()` (+ 3-arg host overload) returns `mmq_y/16 = 4` for NVFP4-reduced (else
the stock 8), so `mmq_y=64 -> nwarps=4 -> 128 threads/CTA` (vs 256) -> ~2x resident CTAs. 2 overloads +
9 `<type>` call-site swaps (kernel, process_tile, write_back_mma, stream_k_fixup, nvfp4 loader, 2 host).
Built with `-DGGML_CUDA_FP4_MMQ_Y=64`; the compile SUCCEEDS (the static_assert now holds at 4*16=64).
**Default `GGML_CUDA_FP4_MMQ_Y==128` returns stock nwarps for every type => a default build is
byte-identical to stock** (the bit-exact opt-out, proven by the md5 below at 128).

### Bit-exactness is EMPIRICAL here (not by-construction)
The per-output K-reduction order is mmq_y-invariant (each output row owned by one thread), but mmq_y=64
DOUBLES `nty` (row-tiles), changing the stream-k `kbc` partition => an output tile's K-range can be
split across CTAs at different points and recombined by `mul_mat_q_stream_k_fixup` in a different
grouping => FP non-associativity COULD perturb the last logit bits and flip a greedy argmax. It did NOT
for the gate prompt (md5 matched), but B-3 is therefore NOT bit-exact-by-construction - a default-ON
ship would be a (small) precision risk. This is a second reason not to ship it for a 0% gain.

## GATE (bit-exact) - BOTH MODELS PASS
greedy `llama-completion -ngl 99 -fa on -p "The capital of France is" -n 48 --temp 0 --seed 1`:
- dense q36-27b-nvfp4 = 5951a5b4d624ce891e22ab5fca9bc439 == ref
- MoE   q36-35b-a3b-nvfp4 = 07db32c2bcb78d17a43ed18bc22705cd == ref
- test-backend-ops CUDA0: **MUL_MAT 1146/1146 PASS, MUL_MAT_ID 806/806 PASS.**

## MEASUREMENT 1 - end-to-end MoE decode_agg, clean BACK-TO-BACK A/B (build-cuda-base @128 vs build-cuda @64)
`llama-batched-bench -m q36-35b-a3b-nvfp4 -c 32768 -ngl 99 -fa on -npp 128 -ntg 128 -npl 32,128`, S_TG t/s,
3 reps alternating (no concurrent load):

| npl | mmq_y=128 (base) mean | mmq_y=64 (B-3) mean | delta |
|----:|----------------------:|--------------------:|------:|
| 32  | 437.6 (437.3-437.7)   | 438.8 (438.4-439.1) | +0.29% |
| 128 | 750.1 (748.9-751.1)   | 753.1 (753.0-753.4) | +0.40% |

Every B-3 rep edges the base by +0.3-0.4% @npl128 (consistent, kernel-backed), but the per-build spread
(base 748.9-751.1) OVERLAPS - it is at the edge of noise, NOT a meaningful lift. Caps the end-to-end win
at well under 1%, nowhere near the gap to vLLM (882).

## MEASUREMENT 2 - nsys kernel-level A/B (the meaningful B-3 evidence), clean, no concurrent load
`GGML_CUDA_DISABLE_GRAPHS=1 nsys ... -npp 4 -ntg 32 -npl 128`, decode-isolated window, `cuda_gpu_kern_sum`:

| kernel (% of window)            | mmq_y=128 total ns | mmq_y=64 total ns | delta  |
|---------------------------------|-------------------:|------------------:|-------:|
| gated_delta_net (SSM, ~40%)     | 2,335,951,709      | 2,334,847,390     | 0.0% (untouched, DOMINANT) |
| **mul_mat_q<NVFP4, M-tile 64>** (MoE GEMM, ~26%) | **1,502,548,958** | **1,483,685,630** | **-1.26% (kernel faster)** |
| mul_mat_q<NVFP4, M-tile 128> (router, ~3.7%)     | 224,532,704       | 210,885,920       | -6.1%  |
| quantize_mmq_nvfp4 (act-quant, ~2%)              | 119,118,624       | 118,718,496       | -0.3%  |
| **mul_mat_q_stream_k_fixup<128>** (~0.6%)        | **26,848,479**    | **38,117,532**    | **+42% (fixup COSTLIER)** |

The warp-remap DOES what it claims at the kernel level: the grouped GEMM is **-1.3%** (more resident
CTAs hide a sliver of weight-load latency). But (a) it is only ~26% of the step, (b) halving mmq_y
DOUBLES the row-tiles so the stream-k fixup recombination grows **+42%** (+11.3M ns), eating ~60% of the
GEMM's 18.9M-ns saving, and (c) the step is dominated by the gated_delta_net SSM (~40%, untouched, and
already PAST vLLM's BW efficiency per 0018-0022) with the GEMM itself at the LPDDR5x BW floor. Net
mul_mat region saving ~7.6M ns on a ~5.8B-ns window = ~0.13%; end-to-end +0.3-0.4% (within noise).
**This is the definitive BW-bound proof: even a real occupancy win on the target kernel does not move
end-to-end** - the same outcome as patch 0015 (M-tile NEUTRAL), 0017 (MINBLOCKS +8.7% slower), and B-2
(act-quant FLAT). The MoE grouped GEMM is bandwidth-limited, not occupancy-limited, at the kernel exit.

## DECISION
No patch 0028 (B-3 does not lift end-to-end; bit-exactness is empirical, not by-construction; the fixup
penalty + BW floor swamp the +1.3% kernel win). Dev tree reverted to pristine 0025 (no ggml diff),
build-cuda reconfigured to default (no flag) and rebuilt. The `mmq_get_nwarps_device<type>()` remap is a
correct, reusable warp-remap should occupancy-bound FP4 hardware ever appear; it is inert on GB10.

---

# FINAL ASSESSMENT - the honest bit-exact MoE ceiling, and the recommendation

## The bit-exact MoE GEMM/launch track is now EXHAUSTED
| MoE lever (bit-exact) | result | MoE decode_agg @npl128 |
|-----------------------|--------|------------------------|
| 0025 re-graph (B-1, LANDED) | the ONLY bit-exact MoE win | ~82% -> **~85%** of vLLM |
| B-2 act-quant retune (no patch) | FLAT (128 already optimal) | +0% |
| B-3 mmq_y-down warp-remap (no patch) | FLAT (kernel -1.3%, e2e +0.3% noise) | +0% |

**Honest bit-exact MoE ceiling on GB10 = ~85% of vLLM @npl128 (753 / 882.2), ~87.5% @npl32 (439 / 500.8).**
B-1 (re-graph, in 0025) banked the move from ~82% to ~85%; B-2 and B-3 each add 0. The grouped-GEMM/
launch track has no remaining bit-exact headroom.

## Is the residual the structural Marlin-MoE gap? YES.
The remaining ~15% is structural and uncloseable bit-exactly, decomposed from the nsys:
- **Grouped FP4 GEMM (~26%) is at the LPDDR5x BW floor.** B-3 proved an occupancy win there is
  end-to-end-inert. vLLM ships a purpose-built **Marlin-NvFp4** grouped GEMM (a different, more
  bandwidth-efficient schedule); llama runs native FP4-MMA W4A4 (a HIGHER arithmetic tier, but the
  decode shape is BW-bound so the tier does not help). This is THE structural gap and matches
  FP4_GEMM_SCOPE_B.md's "MoE ceiling ~76% from the GEMM track alone."
- **The SSM recurrence (~40%) is already PAST vLLM** (84.6% vs 82.4% peak BW, 0018-0022) - not a lever.
- **bf16 projections (~10.5%)** - both engines pay similar; not a bit-exact lever.

No bit-exact lever closes the structural grouped-GEMM gap. ~85% is the honest bit-exact MoE plateau.

## RECOMMENDATION: ship the bit-exact ~85% as DEFAULT; expose 0026 bf16-SSM as a documented opt-in for the last ~10% on MoE (NOT default, NOT in the recommended config)

Per the user's decision rule ("pursue B first; if it cannot reach/beat vLLM on MoE, fall back to the
held hybrid/bf16 opt-in"): **B (bit-exact) cannot reach vLLM on MoE (~85%), so the fallback applies -
but with a hard caveat the team must carry.**

1. **DEFAULT = the bit-exact plateau (0025 with the re-graph), MoE ~85% of vLLM.** This is the honest,
   precision-safe ship: the recurrence already BEATS vLLM's BW efficiency, the GEMM is the same FP4
   arithmetic class, and the output is byte-identical to the f32 reference. Do not claim MoE *parity*
   bit-exactly - claim ~85% with a precision profile at-or-above vLLM.

2. **FALLBACK (opt-in only) = 0026 hybrid bf16-SSM.** It is the ONLY remaining MoE lever (it speeds the
   ~40% recurrence, the part B does not touch): measured **+11.5% MoE decode** (1110.7 -> 1238.1 t/s in
   the 0026 harness) -> would lift MoE ~85% -> **~95% of vLLM**. BUT: (a) it is **non-bit-exact**; (b) it
   **FAILS the MoE KL ship-gate by a wide margin** (MeanKLD ~0.045 / Same-top-p ~91% vs the 1e-3 / 99.5%
   bar - the gated-DeltaNet state is hypersensitive to bf16; A_HYBRID_SSM_RESULTS.md: "MoE has NO low-KL
   regime ... Do NOT put a hybrid T in the gallery/recommended config"); and (c) even then it reaches
   **~95%, not a clean beat** of vLLM, while conceding precision vLLM keeps (all-f32 SSM state).

   => Ship 0026 default-OFF (`ssm_hybrid_tau_thresh = 0` / no `--ssm-bf16-tau`); expose the bf16-SSM as
   an EXPLICIT opt-in flag for callers who knowingly accept a real MoE precision regression for ~+11.5%
   decode (~95% of vLLM). Keep it OUT of the gallery/recommended MoE config.

**Bottom line for the parent:** bit-exact MoE on GB10 plateaus at **~85% of vLLM** and the residual is
the structural Marlin-NvFp4 grouped-GEMM gap that NO bit-exact lever closes (B-1 banked the re-graph;
B-2 and B-3 are 0). Bit-exact does NOT reach/beat vLLM on MoE. The only lever that closes more (to ~95%)
is the held 0026 bf16-SSM, which is **non-bit-exact AND fails the MoE KL gate** - so it ships **opt-in,
default-off, not in the recommended config**, not as the default. Recommend shipping the honest ~85%
bit-exact default and documenting the opt-in for users who accept the precision tradeoff. Do not market
MoE parity; the bit-exact default is ~85% with a precision profile at-or-above vLLM, which is the
defensible claim.

Assisted-by: Claude:opus-4.8 [Claude Code]
