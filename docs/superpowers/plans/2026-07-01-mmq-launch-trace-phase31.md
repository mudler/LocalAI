# MMQ Launch Trace Phase 31 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the default-off MoE MMQ trace so decode serving records launch-shape, stream-k block, efficiency, and fixup facts without changing inference behavior.

**Architecture:** Keep patch `0056` selector tracing intact and add a second bounded trace line inside `launch_mul_mat_q`, where the actual stream-k block policy and `fixup_needed` are known. The new helper is host-only and tested without CUDA execution; DGX gates validate that default-off and trace-enabled inference md5/op outputs are unchanged.

**Tech Stack:** llama.cpp CUDA backend, host-only C++ unit test, LocalAI paged patch series, DGX GB10 gate scripts.

---

## Files

- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/mmq-shape-trace.h`
  - Add `ggml_cuda_mmq_launch_shape`, make/format helpers for launch metrics.
- Modify: `/home/mudler/_git/llama.cpp/tests/test-cuda-mmq-shape-trace.cpp`
  - Add host-only assertions for launch trace formatting.
- Modify: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/mmq.cuh`
  - Emit `[LLAMA_MOE_MMQ_LAUNCH]` when `LLAMA_MOE_MMQ_SHAPE_TRACE` is enabled and grouped-MMQ uses stream-k.
- Create: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/patches/paged/0057-feat-cuda-trace-moe-mmq-launch-shapes.patch`
  - Mirror the fork commit as the next incremental patch.
- Modify docs in `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/`
  - README patch table, GB10 results, lever map, handoff, patch maintenance.

## Checklist

- [x] **Step 1: Write RED host test**
  - Add assertions in `tests/test-cuda-mmq-shape-trace.cpp` that call `ggml_cuda_mmq_launch_shape_make` and expect formatted fields: `ntiles_dst`, `stream_k_blocks`, `tiles_efficiency`, `fixup`, `nsm`, `ntx`, `nty`, `ntzw`.
  - Run: `cmake --build build --target test-cuda-mmq-shape-trace -j 4`
  - Expected: compile failure because the launch helper does not exist yet.

- [x] **Step 2: Implement host launch trace helper**
  - Add `ggml_cuda_mmq_launch_shape` plus make/format helpers in `mmq-shape-trace.h`.
  - Re-run: `cmake --build build --target test-cuda-mmq-shape-trace -j 4 && ./build/bin/test-cuda-mmq-shape-trace`
  - Expected: test passes.

- [x] **Step 3: Wire bounded launch trace**
  - In `launch_mul_mat_q`, after `fixup_needed` is known, emit `[LLAMA_MOE_MMQ_LAUNCH]` only when `args.expert_bounds != nullptr`, `args.use_stream_k`, and `LLAMA_MOE_MMQ_SHAPE_TRACE` limit allows it.
  - Use a separate static counter from selector trace so the user can see up to N selector and N launch lines.

- [x] **Step 4: Build and gate on DGX**
  - Preflight: verify `docker=0`, `local_ai_worker=0`, `compute=0`, and take the owner lock.
  - Build: `cmake --build build-cuda --target llama-completion test-backend-ops test-cuda-mmq-shape-trace -j $(nproc)`
  - Default-off gate expected: MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5 `5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT_ID 806/806`.
  - Trace gate expected: same md5/op values with bounded shape and launch trace lines.

- [x] **Step 5: Run n128 serving launch trace**
  - Run h2h n128 with `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 LLAMA_MOE_MMQ_SHAPE_TRACE=4096`.
  - Parse `[LLAMA_MOE_MMQ_LAUNCH]` lines into decode-like and prefill-like buckets.
  - Decide whether a no-fixup/no-stream-k shortcut is justified from measured `stream_k_blocks`, `tiles_efficiency`, and `fixup`.

- [x] **Step 6: Mirror patch and update docs**
  - Commit llama.cpp fork.
  - Generate LocalAI patch `0057` from the fork commit.
  - Verify strict patch-series application reaches the fork tree.
  - Mark this plan complete with artifact path and gate results.
  - Commit LocalAI docs and patch with `Assisted-by: Codex:gpt-5`.

## Result

- Fork commit: `/home/mudler/_git/llama.cpp` `c78e537b5 feat(cuda): trace moe mmq launch shapes`.
- DGX mirror commit: `dgx:~/llama-phase6-source` `8b75905e9 feat(cuda): trace moe mmq launch shapes`.
- Artifact: `/home/mudler/bench/phase31_mmq_launch_trace/20260701_064424`.
- RED verified: `cmake --build build --target test-cuda-mmq-shape-trace -j 4` failed on missing `ggml_cuda_mmq_launch_shape`.
- GREEN verified locally: `cmake --build build --target test-cuda-mmq-shape-trace -j 4 && ./build/bin/test-cuda-mmq-shape-trace`.
- DGX CUDA build verified: `llama-server`, `llama-completion`, `test-backend-ops`, and `test-cuda-mmq-shape-trace`.
- Default-off, trace-enabled, and post-serving gates all matched MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5 `5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.
- n128 traced serving: `decode_agg_tps=691.0`, `agg_tps=337.0`, `prefill_tps=1500.4`, `TTFT mean=7671.0 ms`.
- Launch result: decode-like `4800/4800` and prefill-like `4920/4920` launch lines had `fixup=0` and `stream_k_blocks == ntiles_dst`.

Decision: do not pursue a no-fixup/no-stream-k shortcut for the current n128 workload. The actual launch path is already taking conventional stream-k tiling with no fixup; the remaining grouped-MMQ gap is the small-M tile/kernel shape itself, not stream-k fixup overhead.
