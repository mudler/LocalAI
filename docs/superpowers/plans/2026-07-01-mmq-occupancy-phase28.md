# MMQ Occupancy Phase 28 Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Test the remaining low-conflict NVFP4 grouped-MMQ occupancy knobs
against the current GB10 serving gap, with md5/op gates before accepting any
performance result.

**Architecture:** Build-vs-build A/B only. The knobs are existing default-off
compile-time macros in the llama.cpp fork, so this phase does not edit source
unless a variant clears the serving gate.

**Tech Stack:** DGX GB10, llama.cpp CUDA backend, `paged-inference-gates.sh`,
h2h n128 serving client, LocalAI parity docs.

---

## Checklist

- [x] **Step 1: Confirm candidate scope**
  - Projection/FP8 follow-up was rejected by source/docs review: it is already
    documented as too small or KL-failing.
  - The remaining small candidate was NVFP4 MMQ occupancy:
    `GGML_CUDA_FP4_MINBLOCKS=2` and `GGML_CUDA_FP4_MMQ_Y`.

- [x] **Step 2: Check DGX preflight**
  - `docker=0`
  - `local_ai_worker=0`
  - `compute=0`
  - GPU owner file was `FREE`.

- [x] **Step 3: Run baseline inference gates**
  - Artifact:
    `/home/mudler/bench/phase28_mmq_occupancy/20260701_040450/gate_baseline`
  - MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
  - Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
  - `MUL_MAT_ID`: `806/806`

- [x] **Step 4: Build and gate `GGML_CUDA_FP4_MINBLOCKS=2`**
  - Build dir: `/home/mudler/llama-phase6-source/build-phase28-minblocks2`
  - Artifact:
    `/home/mudler/bench/phase28_mmq_occupancy/20260701_040450/gate_minblocks2`
  - MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
  - Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
  - `MUL_MAT_ID`: `806/806`

- [x] **Step 5: Try `GGML_CUDA_FP4_MMQ_Y=64`**
  - Build dir: `/home/mudler/llama-phase6-source/build-phase28-mmqy64`
  - Result: compile-time reject.
  - Failure invariant: `static_assert(nwarps*tile_C::I == mmq_y)`.
  - Decision: do not run combined `MMQ_Y=64+MINBLOCKS=2`; the row-tile
    specialization is invalid before serving can be measured.

- [x] **Step 6: Run same-session n128 serving A/B for the viable variant**
  - Artifact:
    `/home/mudler/bench/phase28_mmq_occupancy/20260701_040450/serving_ab`
  - Baseline mean, two reps: `decode_agg_tps=705.1`,
    `decode_perseq_tps=3.970`, `agg_tps=328.8`.
  - `MINBLOCKS=2` mean, two reps: `decode_agg_tps=689.9`,
    `decode_perseq_tps=3.905`, `agg_tps=326.4`.
  - Ratio: `decode_agg_tps=0.9784`, `decode_perseq_tps=0.9836`,
    `agg_tps=0.9927`.

- [x] **Step 7: Run post-serving inference gates**
  - Artifact:
    `/home/mudler/bench/phase28_mmq_occupancy/20260701_040450/gate_minblocks2_post_serving`
  - MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
  - Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
  - `MUL_MAT_ID`: `806/806`

- [x] **Step 8: Record decision**
  - `MINBLOCKS=2` is inference-safe but rejected on throughput.
  - `MMQ_Y` is rejected as a low-conflict shortcut because the current NVFP4
    writeback specialization only accepts the stock row tile.
  - No llama.cpp source patch or LocalAI patch mirror is justified.

## Result

Phase 28 closes the small NVFP4 MMQ occupancy branch. The only buildable knob
kept md5/op gates green but regressed n128 decode serving, and the row-tile knob
does not compile against the current specialization. Future grouped-MMQ work
must be structural kernel work, not a launch-bounds or row-tile build tweak.
