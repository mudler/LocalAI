# W4A16 Shared-Memory Padding Phase 4 Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Keep checkboxes current while executing.

**Goal:** Test whether padding the grouped W4A16 A tile in shared memory reduces bank conflicts after Phase 2 selected `bm32`.

**Scope:** Fork-first experiment only. Keep the patch small, preserve math order, and ship no patch unless it passes md5/op gates and improves prefill.

## Task 1: Implement A-Tile Padding

- [x] Add a small shared-memory row-stride constant for `sA`.
- [x] Pad `sA` rows by 4 `uint32_t` slots while keeping 16-byte chunk alignment.
- [x] Update only A-copy and `ldmatrix` indexing; do not change W loads, dequant, MMA order, metadata, or launch shape.

## Task 2: Gates

- [x] Build `llama-batched-bench`, `llama-completion`, and `test-backend-ops` on DGX.
- [x] Run canonical default-off paged MoE and dense greedy md5 gates.
- [x] Run forced W4A16 `bm32` vs `base` md5 gates.
- [x] Run forced W4A16 `test-backend-ops test -b CUDA0 -o MUL_MAT_ID -j 1`.
- [x] Run W4A16 default `bm32` A/B against Phase 2 at `npp=512,2048`.

## Task 3: Disposition

- [x] Keep only if it improves W4A16 prefill by at least 1% at either `npp=512` or `npp=2048` without regressing the other by more than 1%.
- [x] If kept, commit fork-first with `Assisted-by: Codex:gpt-5`, generate patch `0050`, verify mirror tree hash, update docs, and commit LocalAI.
- [ ] If rejected, revert the fork experiment and record the result without adding a patch.

Result: kept as fork commit `d9b9be0bee3d7239132bfca05d5b057ff4ee4cc3` and LocalAI patch `0050-feat-paged-pad-W4A16-A-shared-tile-stride.patch`.

Artifacts:

- Build: `~/llama-w4a16-phase4`
- Logs: `~/bench/w4a16_phase4`

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
| Phase 4 A-pad `bm32` | 1466.62 | 1495.93 | selected |
| Phase 2 `base` | 1310.13 | 1336.02 | baseline |
| Phase 4 A-pad `base` | 1337.88 | 1364.98 | positive diagnostic |

Mirror verification:

- Applying all 41 `patches/paged/*.patch` files to base pin
  `0ed235ea2c17a19fc8238668653946721ed136fd` reproduces fork HEAD
  `d9b9be0bee3d7239132bfca05d5b057ff4ee4cc3` by tree hash:
  `8fcb151e0620fd0fc82b80c04318e5c34320b087`.
