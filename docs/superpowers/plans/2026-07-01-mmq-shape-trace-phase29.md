# MMQ Shape Trace Phase 29 Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:test-driven-development for source changes and
> superpowers:verification-before-completion before claiming the phase is green.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a default-off, md5-safe MoE grouped-MMQ shape trace so the next
structural grouped-MMQ kernel can be sized from live serving evidence.

**Architecture:** Host-side instrumentation only. The trace records selector
inputs and estimated density at `mul_mat_q_case`, without reading device
`expert_bounds` or adding synchronization.

**Tech Stack:** llama.cpp CUDA backend, local host-only unit test, DGX CUDA
build, `paged-inference-gates.sh`.

---

## Checklist

- [x] **Step 1: Write the RED test**
  - Added `tests/test-cuda-mmq-shape-trace.cpp`.
  - First build failed on missing `ggml-cuda/mmq-shape-trace.h`, proving the
    test covered the new API before implementation.

- [x] **Step 2: Implement the minimal helper**
  - Added `ggml/src/ggml-cuda/mmq-shape-trace.h`.
  - Helper computes `n_active_est`, `density`, and formats a stable trace line.

- [x] **Step 3: Wire default-off instrumentation**
  - Added `LLAMA_MOE_MMQ_SHAPE_TRACE=<n>` in `mmq.cuh`.
  - Trace is capped by the env value; nonnumeric truthy values default to 256.
  - Env unset or `0` stays silent.

- [x] **Step 4: Verify local GREEN**
  - `cmake --build build --target test-cuda-mmq-shape-trace -j 4`
  - `./build/bin/test-cuda-mmq-shape-trace`

- [x] **Step 5: Verify DGX CUDA build**
  - Artifact: `/home/mudler/bench/phase29_mmq_shape_trace/20260701_042428`
  - `cmake --build build-cuda --target llama-completion test-backend-ops test-cuda-mmq-shape-trace`

- [x] **Step 6: Run default-off inference gates**
  - MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
  - Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
  - `MUL_MAT_ID`: `806/806`

- [x] **Step 7: Run trace-enabled inference gates**
  - `EXTRA_ENV=LLAMA_MOE_MMQ_SHAPE_TRACE=4`
  - MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
  - Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
  - `MUL_MAT_ID`: `806/806`
  - Trace lines: `4`

- [x] **Step 8: Mirror into LocalAI**
  - Fork commit: `20a99518a feat(cuda): trace moe mmq batch shapes`
  - LocalAI patch: `0056-feat-cuda-trace-moe-mmq-batch-shapes.patch`

## Result

Phase 29 is instrumentation-only. It does not claim a speed win, but it gives a
bounded and gate-safe way to collect grouped-MMQ selector shape evidence for the
next structural kernel phase.
