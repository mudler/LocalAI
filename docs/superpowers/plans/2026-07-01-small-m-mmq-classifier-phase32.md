# Small-M MMQ Classifier Phase 32 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a default-off, md5-safe classifier/trace for decode-small-M MoE grouped-MMQ candidates before building any alternate numeric kernel.

**Architecture:** Extend the existing host-only MMQ trace helper with a pure small-M predicate and format helper. Wire a bounded `[LLAMA_MOE_MMQ_SMALL_M]` trace in `mul_mat_q_case` after `mmq_x_best` is selected, using a separate env `LLAMA_MOE_MMQ_SMALL_M_TRACE=<n>` so normal shape tracing behavior remains unchanged.

**Tech Stack:** llama.cpp CUDA backend, host-only C++ unit test, LocalAI paged patch series, DGX GB10 md5/op gates.

---

## Files

- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/mmq-shape-trace.h`
  - Add `ggml_cuda_mmq_small_m_shape`, make/format helpers, and candidate predicate.
- Modify: `/home/mudler/_git/llama.cpp/tests/test-cuda-mmq-shape-trace.cpp`
  - Add RED/GREEN assertions for decode-like inclusion and prefill/dense exclusion.
- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/mmq.cuh`
  - Add `LLAMA_MOE_MMQ_SMALL_M_TRACE=<n>` parser and bounded trace emission.
- Create: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/patches/paged/0058-feat-cuda-trace-moe-small-m-mmq-candidates.patch`
- Modify docs: README, GB10 results, lever map, handoff, patch maintenance, and this plan.

## Checklist

- [x] **Step 1: RED host test**
  - Add test calls to `ggml_cuda_mmq_small_m_shape_make` and assert candidate true for `is_moe=true`, `ncols_dst=1024`, `nchannels_x=256`, `ncols_max=128`, `mmq_x_best=64`, `use_stream_k=true`.
  - Assert false for dense (`is_moe=false`), prefill (`ncols_max=512`), high density (`ncols_dst=4096`), large tile (`mmq_x_best=128`), and no stream-k.
  - Run: `cmake --build build --target test-cuda-mmq-shape-trace -j 4`.
  - Expected: compile failure because the helper does not exist.

- [x] **Step 2: GREEN host helper**
  - Add helper structs/functions in `mmq-shape-trace.h`.
  - Run: `cmake --build build --target test-cuda-mmq-shape-trace -j 4 && ./build/bin/test-cuda-mmq-shape-trace`.
  - Expected: pass.

- [x] **Step 3: Wire default-off trace**
  - Add `ggml_cuda_moe_mmq_small_m_trace_limit()`.
  - Emit `[LLAMA_MOE_MMQ_SMALL_M]` only when `args.expert_bounds != nullptr`, helper says candidate, and the trace limit allows it.
  - No numeric branch or tile change in this patch.

- [x] **Step 4: DGX build and gates**
  - Build `llama-server`, `llama-completion`, `test-backend-ops`, and `test-cuda-mmq-shape-trace`.
  - Run default-off gates: MoE `8cb0ce23777bf55f92f63d0292c756b0`, dense `5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT_ID 806/806`.
  - Run trace-enabled gates with `EXTRA_ENV=LLAMA_MOE_MMQ_SMALL_M_TRACE=4`; expected same md5/op values and four small-M trace lines from MoE only.

- [x] **Step 5: n128 serving count**
  - Run h2h n128 with `LLAMA_MOE_MMQ_SMALL_M_TRACE=4096`.
  - Parse small-M lines and compare count to Phase 30/31 decode-like launch count.
  - Run post-serving gates.

- [x] **Step 6: Mirror and docs**
  - Commit fork, generate LocalAI patch `0058`.
  - Verify strict patch-series tree equals fork tree.
  - Update docs and mark this checklist complete with artifact path and decision.
  - Commit LocalAI with `Assisted-by: Codex:gpt-5`.

## Result

- Fork commit: `/home/mudler/_git/llama.cpp` `2a9964d29 feat(cuda): trace moe small-m mmq candidates`.
- DGX mirror commit: `dgx:~/llama-phase6-source` `024f494d0 feat(cuda): trace moe small-m mmq candidates`.
- Artifact: `/home/mudler/bench/phase32_small_m_classifier/20260701_070127`.
- RED verified: `cmake --build build --target test-cuda-mmq-shape-trace -j 4` failed on missing `ggml_cuda_mmq_small_m_shape`.
- GREEN verified locally: `cmake --build build --target test-cuda-mmq-shape-trace -j 4 && ./build/bin/test-cuda-mmq-shape-trace`.
- DGX CUDA build verified: `llama-server`, `llama-completion`, `test-backend-ops`, and `test-cuda-mmq-shape-trace`.
- Default-off, trace-enabled, and post-serving gates all matched MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5 `5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.
- n128 traced serving: `decode_agg_tps=689.0`, `agg_tps=343.9`, `prefill_tps=1566.5`, `TTFT mean=7849.0 ms`.
- Small-M candidate trace: `4096` candidate calls in the first serving trace window.
  - `mmq_x_best`: `64` 1800, `48` 1096, `40` 360, `32` 360, `16` 360, `24` 120.
  - density: `4` 1440, `3` 1336, `1` 840, `2` 480.

Decision: Phase 33 can A/B a default-off small-M tile policy, with `mmq_x=16` and possibly `8` as the first candidates. The classifier shows enough live candidate coverage to justify an opt-in tile-policy experiment, while preserving the existing MMQ path and md5 gates.
