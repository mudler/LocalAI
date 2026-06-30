# W4A16 Kernel Shape Phase 2 Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Keep checkboxes current while executing.

**Goal:** Attack the remaining W4A16 prefill gap at the grouped kernel body, not metadata.

**Scope:** Fork-first in `/home/mudler/_git/llama.cpp`; LocalAI patch series is regenerated only after the fork commit is validated. Keep W4A16 default-off unless `LLAMA_W4A16_PREFILL_M > 0`.

## Task 1: Profile-Guided Target Selection

- [x] Run `nsys` for default FP4-MMQ and forced W4A16 at `npp=512`.
- [x] Compare kernel attribution for metadata/cast/body costs.
- [x] Decide next implementation target from measured cost, not speculation.

Result: `w4a16_grouped_kernel` is the dominant forced-W4A16 cost (`5231.667 ms`, `47.8%` of profiled GPU kernel time). `w4a16_cast_act_f32_bf16` is visible but much smaller (`517.195 ms`, `4.7%`). Phase 2 targets grouped-kernel tile shape/body first.

## Task 2: Runtime Shape Selector

**Files:**
- Modify fork-first: `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/w4a16-gemm.cu`

- [x] Add a small runtime selector for W4A16 grouped-kernel shape experiments.
- [x] Preserve the current `64x128` shape as the default path.
- [x] Add multiple candidate specializations behind an environment selector: a vLLM-inspired wider-`N` candidate, a ragged-M candidate, an occupancy candidate, and a deeper pipeline candidate.
- [x] Keep launch and shared-memory calculations template-safe for each specialization.

## Task 3: DGX Validation And Kill Gate

- [x] Build the fork on DGX from the updated source snapshot.
- [x] Run canonical paged MoE and dense greedy md5 gates after the final code change.
- [x] Confirm gate hashes match the established inferencing references before committing.
- [x] Run forced W4A16 A/B for default shape and candidate shape at `npp=512,2048`.
- [x] Run forced W4A16 `MUL_MAT_ID` op checks for selected `bm32` and old `base`.
- [x] Profile the winning candidate if it improves enough to understand the new bottleneck.
- [x] Record whether the candidate improves, regresses, or is neutral.

Initial candidates:

- `default` / `64x128`: current Phase 1 shape.
- `bn256`: wider N reuse, inspired by vLLM large-batch Marlin config.
- `bm32`: smaller M tiles for ragged MoE expert tails.
- `bn64`: smaller N tiles to test occupancy/latency limits.
- `stages3`: current tile shape with deeper `cp.async` pipeline.

Kill gate: keep a shape candidate as the new default only if it improves forced W4A16 prefill throughput by at least 3% at either `npp=512` or `npp=2048` without regressing the other by more than 1%. Otherwise revert or leave it as an off-by-env diagnostic only if it is useful for future sweeps.

## Task 4: Mirror And Document

- [x] Commit the accepted fork-first result with `Assisted-by: Codex:gpt-5`.
- [x] Regenerate only the new LocalAI patch mirror entry.
- [x] Verify the full LocalAI patch mirror applies to the base pin and matches fork HEAD.
- [x] Update `PARITY_HANDOFF.md` and phase results with artifact paths and decision.
- [x] Commit the LocalAI mirror/docs result with `Assisted-by: Codex:gpt-5`.

Artifacts:

- Profile directory: `~/bench/w4a16_phase1/profile`
- Candidate build directory: `~/llama-w4a16-phase2`
- Candidate benchmark directory: `~/bench/w4a16_phase2`

Result:

| Shape | 512 S_PP t/s | 2048 S_PP t/s | Decision |
|-------|--------------|---------------|----------|
| `base` / `64x128` | 1308.02 | 1339.46 | old baseline |
| `bn256` | 1286.99 | 1311.56 | rejected |
| `bm32` / `32x128` | 1442.99 | 1475.65 | selected |
| `bn64` | 1334.80 | 1362.55 | diagnostic only |
| `stages3` | 1271.01 | 1295.96 | rejected |
| `bn256x16` | 1084.66 | 1100.95 | rejected |

Only `bm32` and the old `base` selector are shipped in patch `0049`. The other
candidate shapes were benchmarked in the Phase 2 build and then deliberately
left out to keep the upstream conflict surface small.

Follow-up default verification with `LLAMA_W4A16_SHAPE` unset:

| PP | TG | B | N_KV | T_PP s | S_PP t/s | T_TG s | S_TG t/s | T s | S t/s |
|----|----|---|------|--------|----------|--------|----------|-----|-------|
| 512 | 4 | 32 | 16512 | 11.360 | 1442.28 | 0.321 | 397.00 | 11.682 | 1413.43 |
| 2048 | 4 | 32 | 65664 | 44.529 | 1471.77 | 0.331 | 386.06 | 44.860 | 1463.75 |

Profile:

- `bm32` `w4a16_grouped_kernel`: `4107.355 ms` (`41.7%`) at profiled `npp=512`.
- Phase 1 `64x128` `w4a16_grouped_kernel`: `5231.667 ms` (`47.8%`) at profiled `npp=512`.

Canonical post-change gates:

- MoE command: `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1 ./llama-completion -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf -ngl 99 -fa on -c 4096 --temp 0 --seed 1 -n 48 -p "The capital of France is" </dev/null | md5sum`
- MoE greedy md5: `8cb0ce23777bf55f92f63d0292c756b0` (matched canonical paged MoE reference).
- Dense command: `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1 ./llama-completion -m /home/mudler/bench/q36-27b-nvfp4.gguf -ngl 99 -fa on -c 4096 --temp 0 --seed 1 -n 48 -p "The capital of France is" </dev/null | md5sum`
- Dense greedy md5: `5951a5b4d624ce891e22ab5fca9bc439` (matched canonical dense reference).
- Forced W4A16 `bm32` md5 with `LLAMA_W4A16_PREFILL_M=1`: `07db32c2bcb78d17a43ed18bc22705cd`.
- Forced W4A16 `base` md5 with `LLAMA_W4A16_PREFILL_M=1 LLAMA_W4A16_SHAPE=base`: `07db32c2bcb78d17a43ed18bc22705cd`.
- Forced W4A16 shape md5 status: PASS, selected `bm32` is byte-identical to old `base` on the gate prompt.
- Forced W4A16 `MUL_MAT_ID` op check: `test-backend-ops test -b CUDA0 -o MUL_MAT_ID -j 1` passed `806/806` for both `bm32` and `base`.
- Inference gate status: PASS before fork commit and LocalAI mirror commit.

Mirror verification:

- Applying all 40 `patches/paged/*.patch` files to base pin
  `0ed235ea2c17a19fc8238668653946721ed136fd` reproduces fork HEAD
  `7dfa0e17548c5f04f83d2cc2a057b0a9941b599a` by tree hash:
  `dabe225efbf20ec047b8309d1e1f19b34fc7c5c9`.
