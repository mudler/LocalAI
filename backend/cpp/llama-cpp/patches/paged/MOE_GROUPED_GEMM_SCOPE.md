# Durable scope: grouped FP4-MMA MoE GEMM for ggml CUDA on GB10 (sm_121)

Build-ready plan. **Not implemented in this workflow** (large kernel work). This
document scopes the durable path to match or beat vLLM MoE grouped-GEMM efficiency
on GB10 for the Qwen3-30B-A3B-class mxfp4 MoE, and records the single honest
finding that re-shapes the whole effort.

Hardware: NVIDIA GB10 (sm_121, CC=1210 = `GGML_CUDA_CC_DGX_SPARK`), unified
LPDDR5X ~273 GB/s. Model: Qwen3-Coder-30B-A3B, 128 experts, top-8, mxfp4 experts
(`~/bench/qwen3coder-mxfp4.gguf`). Dev tree `~/llama-paged-dev` (branch `paged`,
HEAD at patch 0013), `build-cuda` sm_121.

## TL;DR (the honest reframe)

**The grouped GEMM the mission scoped to build from scratch already exists in
upstream ggml, and it already runs on GB10 for mxfp4.** For mxfp4 experts on
sm_121 `ggml_cuda_should_use_mmq()` returns true (`turing_mma_available`), so
MUL_MAT_ID takes the **grouped mmq path**, which already contains both vLLM
building blocks:

1. a moe_align / token-sort-by-expert (`mmid.cu` `mm_ids_helper`:
   count -> warp-scan/cumsum -> scatter into expert-sorted contiguous buffers),
2. a **single persistent stream-k grouped FP4-MMA GEMM** (one `mul_mat_q` launch;
   grid flattened into kbc-continuous space over expert x col-tile x row-tile x
   k-block; native FP4 MMA via `block_fp4_mmq` under `BLACKWELL_MMA_AVAILABLE`).

The per-expert host-side row-gather loop in `ggml-cuda.cu`
`ggml_cuda_mul_mat_id()` (~L2632-2790) - the path the mission's root-cause
analysis describes as "the cliff" - is a **fallback only reached when
`should_use_mmq()==false`** (f16/bf16 experts, non-Blackwell). It is **never the
GB10 mxfp4 path.**

Consequence: the "npl128 MoE cliff" does not exist on the current dev HEAD.
Re-measured batched-bench decode (`S_TG` t/s) on the mxfp4 MoE rises monotonically
`85 / 278 / 637 / 950 / 1306 / 1771` at npl `1 / 8 / 32 / 64 / 128 / 256`. The
original `253/505/830/620` cliff was a real high-batch regression that has since
been **fixed upstream** (FP4-native grouped mmq + MoE stream-k balancing), not a
batched-bench artifact.

**Therefore the durable work is NOT "port moe_align + a grouped GEMM."** It is a
**surgical fix to the one place ggml diverges from vLLM: the M-tile (token-tile)
sizing heuristic.** This document scopes that delta, plus the optional
block-padded align, plus the parity gate and phased plan. It also records what is
intentionally NOT built and why (the W4A16 occupancy wall).

## The one structural gap: M-tile sizing

`mul_mat_q_case` / `launch_mul_mat_q` pick `mmq_x` (the token/M tile) by
**minimizing** `ntiles_x = ceil(ncols_max / mmq_x)` over the **aggregate** token
count (`ncols_max = ne12`). On Blackwell `get_mmq_x_max = 128`, so the heuristic
always selects the **largest** `mmq_x` that fits shared memory. vLLM's
CUTLASS/Triton fused_moe does the **opposite**: a small tuned `BLOCK_SIZE_M`
(typ. 16/32/64), padded **per expert**.

ggml then applies its over-large `mmq_x` **per expert**. In MoE decode the tokens
per expert is tiny - Qwen3-30B-A3B top-8 of 128: at npl64 ~512 assignments over
~126 activated experts ~= 4 tok/expert; at npl128 ~1024 over ~128 ~= 8 tok/expert.
So each expert's single M-tile of width 128 is **3-6% filled** -> ragged tiny-M
tiles run a dense-GEMM-tuned config, wasting MMA M-throughput, and (with
`need_check`) every expert runs as a masked partial tail.

The FP4 MMA N-fragment (`tile_C::J`) is 8, so the **ideal M-tile ~= tokens/expert
(~8)**, 16x smaller than the 128 ggml picks. This mismatch is the durable gap.

Critically for GB10: at tokens/expert <= 8 there is exactly **one col-tile per
expert**, so a smaller `mmq_x` causes **no extra weight re-read** (weight rows are
re-read only across multiple col-tiles, of which there is one) while it **lowers
shared-mem footprint and raises occupancy** - strictly aligned with the GB10
occupancy lessons.

## What already exists (reuse, do NOT rebuild)

Engine files on DGX `~/llama-paged-dev/ggml/src/ggml-cuda/`:

- **[A] moe_align / scatter** = `mmid.cu` `mm_ids_helper`. One CUDA block per
  expert (`gridDim.x = n_experts`); warp counts tokens routed to this expert,
  warp-scan for the compaction index, scatters into `ids_src1` (column gather
  permutation, expert-sorted contiguous), `ids_dst` (output scatter), and writes
  `expert_bounds[expert] = prefix start`, `expert_bounds[n_experts] = total`.
  This **is** count -> cumsum -> permute; `expert_bounds` is the analogue of
  vLLM's `num_tokens_post_padded` boundaries. No `-1` pad today because segments
  are exact (not block-padded).
- **[B] persistent grouped FP4 GEMM** = `mmq.cuh` `mul_mat_q` stream-k
  (kernel ~L3542, `process_tile` ~L3447, launch ~L3943, case-select ~L4055).
  Single launch, fixed grid (`nsm` CTAs, or `ntiles` when >=90% tile efficiency).
  Each CTA walks a contiguous `kbc` slice of (expert `zt` via `expert_bounds`,
  col-tile `jt`, row-tile `it`, k-block) space; the weight row-tile (`mmq_y=128`
  x K) is loaded once per col-tile in the `process_tile` k-loop; empty col-tiles
  past `col_diff` are SKIPPED by advancing `kbc += blocks_per_ne00`; a
  `stream_k_fixup` pass recombines split tiles.
- **[C] native FP4-MMA expert weights** = `block_fp4_mmq` + `MMQ_MMA_TILE_X_K_FP4`
  (== Q8_1 tile, skew-pad +4) under `BLACKWELL_MMA_AVAILABLE`;
  `quantize_mmq_fp4_cuda` quantizes activations to the q8-style y-layout **with
  the `ids_src1` gather fused** (one pass, no separate row-copy).

Dispatch seam: `ggml-cuda.cu` `ggml_cuda_mul_mat_id()` (~L2632-2790). For mxfp4
with `ne2`(tokens) > 7, `should_use_mmq()` -> true -> `ggml_cuda_mul_mat_q()`
(`mmq.cu` id-branch ~L162-225) -> `mm_ids_helper` then ONE
`mul_mat_q_switch_type`. The per-expert host loop below it is the gated fallback.

(Below npl8, MXFP4 mmid routes through `mmvq` - `MMVQ_MAX_BATCH_SIZE=8`, mmid max
7 for turing_plus - which is fine for thin batch and out of scope here.)

## What to add (the durable delta, priority order)

### [1] Expert-aware M-tile selection (host-side only, zero new kernel)

In `mul_mat_q_case` / `launch_mul_mat_q`, when `ids != null`, choose `mmq_x` from
**per-expert density** (~`ne_get_rows / n_active_experts`, derivable cheaply, or
capped via env) instead of minimizing `ntiles` over aggregate `ncols_max`.

- `mmq_x` is a **compile-time template** (switch 8..128 step 8), so this is a pure
  host-side SELECTION change - it picks a different already-compiled instantiation.
  **Zero new kernel. Very low risk, high leverage.** Matches vLLM `BLOCK_SIZE_M`.
- Doubles as near-term lever-1: env-gated `LLAMA_MOE_MMQ_X` cap at the knee.
- GB10-aligned: smaller `mmq_x` -> smaller shared mem -> higher occupancy, and at
  tokens/expert <= 8 (one col-tile/expert) it costs no extra weight read.

This is the single highest-leverage change and the seed of the durable port.

### [2] Block-padded moe_align (the moe_align_block_size port proper)

Extend `mm_ids_helper` to pad each expert segment up to a multiple of the chosen
block: write a sentinel (`-1`) `ids_dst` for pad lanes, put `expert_bounds` on
block boundaries. Then every col-tile is **full**, which:

- drops the `need_check` masking + per-expert partial-tail MMA,
- makes the stream-k `kbc` space exact (no skipped tiles, cleaner persistent
  schedule), removing the `col_diff` skip branch.

Medium risk: touches the scatter, the `col_diff`/`need_check` logic, and the
`write_back` masking (pad rows must not write output). This is the proper
`moe_align_block_size` analogue and the durable second step.

### [3] Bespoke masked-grouped FP4 kernel - ONLY if [1]+[2] insufficient

A CUTLASS/DeepGEMM-style masked-grouped FP4 kernel. **Largest risk, likely
unnecessary** given [B] is already a persistent stream-k grouped GEMM. Listed for
completeness; do not start without [1]+[2] measured as insufficient.

## Integration into ggml_mul_mat_id (dispatch seam + gated fallback)

- The seam is unchanged: `ggml_cuda_mul_mat_id()` -> `should_use_mmq()` ->
  `ggml_cuda_mul_mat_q()`. [1] and [2] live entirely inside the mmq id-branch
  (`mmq.cu` ~L162-225) and its callees (`mmq.cuh` selection/launch, `mmid.cu`
  scatter). No change to the host dispatch decision.
- **Gated fallback preserved**: the existing per-expert host loop
  (`should_use_mmq()==false` path) stays as-is for f16/bf16 experts and
  non-Blackwell GPUs. The new selection only fires on the grouped path.
- **Env gates** (off = exact current behavior):
  - `LLAMA_MOE_MMQ_X=<8..128>` - cap/override the token tile for the id-path
    (lever-1 + [1] manual knob).
  - `LLAMA_MOE_BLOCK_ALIGN=0|1` - enable block-padded scatter ([2]).
  Default both off until parity + throughput proven, then flip [1]'s
  auto-selection on by default.

## Correctness / parity gate

Primary: `tests/test-backend-ops.cpp` `test_mul_mat_id` (~L4181). The CPU
reference is **deterministic** - the op test must be **bit-exact**.

- Sweep `type_a` in {`MXFP4`, `NVFP4`}, `type_b = F32`, `n_mats = 128`,
  `n_expert_used = 8`, `n_tokens` in {8, 32, 64, 128} (the decode-density band).
- **Add ragged small-M shapes** to the harness if absent (n_tokens not a multiple
  of mmq_x; experts with 0/1/2 tokens) - these are exactly where [1]/[2] change
  tile geometry and where block-pad masking can leak.
- Pass criterion: new `mmq_x` selection and padded-align produce dst **identical**
  to current op-test output (op test is exact; the GB10 CUDA greedy-decode
  non-determinism band applies only to end-to-end, never to the op test).
- End-to-end sanity: `llama-batched-bench` on `~/bench/qwen3coder-mxfp4.gguf`,
  `-fa on -npp 128 -ntg 128`, npl 8/32/64/128/256; confirm `S_TG` stays monotonic
  and `S_PP` flat ~3050-3090. Verify greedy-decode output within the documented
  CUDA batch-shape non-determinism band (CPU is the deterministic oracle).

Bench/parity scripts stay **dev-tree-only** (`~/llama-paged-dev/benches/`).

## Phased plan, expected payoff, risk per phase

| Phase | Work | Expected payoff | Risk |
|-------|------|-----------------|------|
| **P0** harness | Add ragged small-M + MXFP4/NVFP4 mmid shapes to `test_mul_mat_id`; capture current bit-exact baseline + the monotonic batched-bench curve as the reference. | None (gate). Locks correctness + the 85->1771 t/s baseline so any regression is caught. | Low. |
| **P1** sort op | Confirm `mm_ids_helper` is the moe_align; if [2] is pursued, prototype the block-pad scatter behind `LLAMA_MOE_BLOCK_ALIGN`. | Enables exact stream-k schedule; removes `need_check` masking (P3 payoff). | Medium (scatter + write-back masking). |
| **P2** grouped GEMM ([1]) | Expert-aware `mmq_x` selection in `mul_mat_q_case`/launch, `LLAMA_MOE_MMQ_X` gate. | The headline: reclaim the 3-6% M-tile fill waste at npl64-128. Modeled as removing wasted MMA M-throughput on every activated expert; net throughput up at high batch with no extra weight read. | **Low** (host-side template selection, no new kernel). |
| **P3** tune ([2] + fixup) | Land block-padded align; tune `mmq_x` per density, profile stream-k `fixup` overhead and `mmq_x`/`mmq_y` tile choice with nsys on the grouped `mul_mat_q<MXFP4>` kernel. | Remove per-expert partial-tail MMA; tighten the persistent schedule. Diminishing vs P2; this is pure micro-efficiency toward/past vLLM's saturated grouped-GEMM. | Medium-high (kernel masking paths). |

**Honest payoff framing:** the npl128 "cliff" is already gone on HEAD, so there is
no broken path to unlock. The durable win is **matching vLLM's saturated
grouped-GEMM M-tiling** (small per-expert block) and erasing the dense-GEMM-tuned
M-tile mismatch - a micro-efficiency gain at large effective batch, not a
step-change. vLLM 0.23.0 cannot even serve this model on GB10 (bf16 MoE-warmup
hang + hard reboot; GGUF loader can't map fused qwen3moe experts), and llama
already uses the same sorted-grouped-GEMM algorithm, so structural parity is
**already met**; this closes the residual kernel micro-gap.

## The biggest risk: the GB10 W4A16 occupancy wall

The dominant risk is **repeating the W4A16 dead-end** that hit only ~9 TFLOPS /
178 t/s on GB10. GB10 is **occupancy-dominated**: deep `cp.async` pipelines and
XOR-swizzle shared layouts **collapse occupancy** there. Any P3 kernel work MUST:

- keep **small shared mem + high occupancy** (do NOT add deep `cp.async` stages
  or XOR-swizzle - they are exactly what killed W4A16);
- preserve the **skew-pad (+4)** tile layout already in `MMQ_MMA_TILE_X_K_FP4`;
- stay on the **FP4-MMA path** (`block_fp4_mmq`), the only path that hits Blackwell
  FP4 = 2x INT8/BF16 rate;
- respect the ~273 GB/s LPDDR5X weight-read floor (dense decode is already at it;
  MoE wins come from occupancy/tile fit, not bandwidth).

Smaller `mmq_x` ([1]) is **strictly consistent** with these lessons: it reduces
shared-mem footprint, raises occupancy, and at tokens/expert <= 8 adds no weight
re-read. So the low-risk lever ([1]) is also the one most aligned with what GB10
rewards - which is why it leads the plan and [3] is gated behind it.

## Commit / hygiene

Scope doc only (this file). No engine change committed in this workflow. Bench and
parity scripts are dev-tree-only. Commit with `git -s`, trailer
`Assisted-by: Claude:opus-4.8 [Claude Code]`, no `Co-Authored-By`, no em-dashes.
Do not push (human pushes). When [1]/[2] are implemented they mirror to
`backend/cpp/llama-cpp/patches/paged/0014-*` (next free slot).
