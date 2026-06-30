# W4A16 Scale Broadcast Phase 3 Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Keep checkboxes current while executing.

**Goal:** Test a minimal W4A16 grouped-kernel body optimization after Phase 2 selected `bm32`.

**Scope:** Fork-first in `/home/mudler/_git/llama.cpp`; mirror into LocalAI only after build, md5, op, perf, and mirror gates pass. Keep patch `0050` incremental on top of `0049`, and keep the source diff small.

## Task 1: Implement Scale Broadcast

- [x] In `ggml/src/ggml-cuda/w4a16-gemm.cu`, replace per-lane duplicate `ggml_cuda_ue4m3_to_fp32` scale conversion with one conversion per 4-lane `n_local` group plus `__shfl_sync`.
- [x] Keep the existing dequant and MMA order unchanged.
- [x] Do not add broad diagnostic variants or extra launch shapes.

## Task 2: Gates

- [x] Build `llama-batched-bench`, `llama-completion`, and `test-backend-ops` on DGX.
- [x] Run canonical default-off paged MoE and dense greedy md5 gates.
- [x] Run forced W4A16 `bm32` vs `base` md5 gates on the canonical prompt.
- [x] Run forced W4A16 `test-backend-ops test -b CUDA0 -o MUL_MAT_ID -j 1`.
- [x] Run W4A16 default `bm32` A/B against Phase 2 at `npp=512,2048`.

## Task 3: Disposition

- [x] Keep only if it improves W4A16 prefill by at least 1% at either `npp=512` or `npp=2048` without regressing the other by more than 1%.
- [x] If kept, commit fork-first with `Assisted-by: Codex:gpt-5`, generate patch `0050`, verify mirror tree hash, update docs, and commit LocalAI. Not taken: perf gate failed.
- [x] If rejected, revert the fork experiment and record the result without adding a patch.

Result: rejected, no fork commit and no LocalAI patch `0050`.

Artifacts:

- Build: `~/llama-w4a16-phase3`
- Logs: `~/bench/w4a16_phase3`

Gates:

- Canonical paged MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`.
- Canonical dense md5: `5951a5b4d624ce891e22ab5fca9bc439`.
- Forced W4A16 `bm32` md5: `07db32c2bcb78d17a43ed18bc22705cd`.
- Forced W4A16 `base` md5: `07db32c2bcb78d17a43ed18bc22705cd`.
- Forced W4A16 `MUL_MAT_ID`: `806/806` on CUDA0.

Performance:

| Shape | 512 S_PP t/s | 2048 S_PP t/s | Decision |
|-------|--------------|---------------|----------|
| Phase 2 `bm32` | 1442.28 | 1471.77 | baseline |
| Phase 3 scale-broadcast `bm32` | 1392.46 | 1422.74 | rejected |
| Phase 2 `base` | 1310.13 | 1336.02 | baseline |
| Phase 3 scale-broadcast `base` | 1201.69 | 1221.25 | rejected |

Disposition:

- Reverted local fork experiment in `/home/mudler/_git/llama.cpp`.
- Do not retry this exact scale-broadcast approach; shuffle overhead and/or compiler scheduling cost exceeds saved FP8 scale conversion on GB10.
