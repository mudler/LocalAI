# Small-M Tile Policy Phase 33 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A/B a default-off MoE-only small-M tile policy using Phase 32 candidate criteria, starting with `LLAMA_MOE_SMALL_M_TILE=16`.

**Architecture:** Add a narrow host-side override in `mul_mat_q_case`: after the normal MoE density auto-tile logic, if `LLAMA_MOE_SMALL_M_TILE=<n>` is set and the call is decode-like (`ncols_max <= 128`, density `<=4`, stream-k), cap `mmq_x_lim` to that tile. The existing MMQ kernels and launch path remain unchanged; unsupported/default cases fall through unchanged.

**Tech Stack:** llama.cpp CUDA backend, host-only selector tests, DGX GB10 md5/op gates and n128 h2h serving A/B.

---

## Checklist

- [x] **Step 1: RED selector test**
  - Add host helper assertions for `ggml_cuda_mmq_small_m_tile_limit`.
  - Expected: compile failure before helper exists.

- [x] **Step 2: GREEN helper**
  - Implement helper in `mmq-shape-trace.h`.
  - Local test passes.

- [x] **Step 3: Wire env policy**
  - Add `LLAMA_MOE_SMALL_M_TILE`.
  - Apply only to MoE grouped-MMQ small-M candidates.
  - Default path unchanged.

- [x] **Step 4: DGX gates**
  - Build CUDA targets.
  - Run default-off gates.
  - Run `EXTRA_ENV=LLAMA_MOE_SMALL_M_TILE=16` gates.

- [x] **Step 5: n128 A/B**
  - Same-session baseline vs `LLAMA_MOE_SMALL_M_TILE=16`, h2h n128.
  - Post-serving gates.

- [x] **Step 6: Mirror/docs**
  - Generate patch `0059`.
  - Strict patch-series tree check.
  - Update docs and commit LocalAI.

## Result

- Fork commit: `/home/mudler/_git/llama.cpp` `fbed2abaa feat(cuda): gate moe small-m mmq tile policy`.
- DGX mirror commit: `dgx:~/llama-phase6-source` `dfd1eaea8 feat(cuda): gate moe small-m mmq tile policy`.
- Artifact: `/home/mudler/bench/phase33_small_m_tile_policy/20260701_071136`.
- RED verified: `cmake --build build --target test-cuda-mmq-shape-trace -j 4` failed on missing `ggml_cuda_mmq_small_m_tile_limit`.
- GREEN verified locally: `cmake --build build --target test-cuda-mmq-shape-trace -j 4 && ./build/bin/test-cuda-mmq-shape-trace`.
- DGX CUDA build verified: `llama-server`, `llama-completion`, `test-backend-ops`, and `test-cuda-mmq-shape-trace`.
- Default-off, tile16, tile8, and post-serving gates all matched MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5 `5951a5b4d624ce891e22ab5fca9bc439`, and `MUL_MAT_ID` `806/806`.
- Same-session n128 serving:
  - baseline: `decode_agg_tps=672.1`, `agg_tps=339.5`, `prefill_tps=1511.4`.
  - `LLAMA_MOE_SMALL_M_TILE=16`: `decode_agg_tps=640.3`, `agg_tps=328.9`, `prefill_tps=1522.2`, ratio `0.953x`.
  - `LLAMA_MOE_SMALL_M_TILE=8`: `decode_agg_tps=583.2`, `agg_tps=307.4`, `prefill_tps=1442.6`, ratio `0.868x`.

Decision: reject smaller `mmq_x` caps for the classified n128 small-M calls. They are md5/op safe but slower. The next structural direction must not be a simple smaller tile cap; it needs a different kernel shape or a different target bucket.
