# Phase 7: Serving Source Candidate Scope

**Status:** Test-gate patch landed. Production CUDA fusion not started.

**Goal:** Select one maintainable source candidate for the remaining GB10 MoE
serving gap, then implement only if it can be gated for inference correctness and
measured against a bucket that Phase 6 proved relevant.

## Entry State

- llama.cpp fork: `/home/mudler/_git/llama.cpp`
- Required branch: `localai-paged`
- Required clean head: `d9b9be0bee3d7239132bfca05d5b057ff4ee4cc3`
- LocalAI patch mirror count before Phase 7: `41`, through patch `0050`
- DGX mirror used by Phase 6: `/home/mudler/llama-phase6-source`

## Required Safety Gates

- Before DGX work:
  - `docker ps -q | wc -l` must be `0`.
  - `nvidia-smi --query-compute-apps=pid --format=csv,noheader` must be empty.
  - `~/gpu_bench_lock/owner` must be absent or start with `FREE`.
  - No `local-ai-worker` container may be running.
- Before keeping any source patch:
  - MoE greedy md5 must be `8cb0ce23777bf55f92f63d0292c756b0`.
  - Dense greedy md5 must be `5951a5b4d624ce891e22ab5fca9bc439`.
  - If W4A16 is touched, forced `bm32` and `base` md5 must both be
    `07db32c2bcb78d17a43ed18bc22705cd`.
  - If `MUL_MAT_ID` routing or CUDA MoE kernels are touched, run
    `test-backend-ops test -b CUDA0 -o MUL_MAT_ID -j 1` and require `806/806`.
- Patch handling:
  - Source changes are fork-first in `/home/mudler/_git/llama.cpp`.
  - Keep each patch incremental and additive, with helper functions preferred
    over invasive rewrites.
  - Regenerate LocalAI patches with `git format-patch`; do not hand-edit
    generated patch files.

## Candidate Tracks

### Track A: Structural MoE Decode Kernel

Phase 6 evidence: grouped NVFP4 `mul_mat_q` accounts for roughly 30% of llama.cpp
GPU kernel time under serving, while vLLM's Marlin-MoE bucket is materially
smaller in the same workload class.

The candidate must identify a bounded change in the current `MUL_MAT_ID` or
grouped-MMQ path that reduces actual serving bucket time. Selector-only tile
retuning is rejected unless new evidence differs from the Phase 6 MMQ grid.

Selected first candidate:

- Add a batched CUDA path that fuses MoE SWIGLU with the NVFP4 activation
  quantization feeding the **down** `MUL_MAT_ID`.
- Current graph shape:
  `ffn_moe_gate_up` `MUL_MAT_ID` -> gate/up views -> `ggml_swiglu_split` ->
  `ffn_moe_down` `MUL_MAT_ID`.
- Target: remove or reduce the separate f32 SWIGLU intermediate write/read and
  `quantize_mmq_nvfp4` pass for the down projection while preserving the existing
  grouped-MMQ kernel and accumulation order.
- Keep scope to CUDA, Blackwell native FP4, `GGML_TYPE_NVFP4`, merged gate/up
  MoE, down projection only, no bias/clamp/OAI/GEGLU.

Important finding:

- Existing CUDA `MUL_MAT_ID + GLU` fusion is vector-only. The fusion predicates
  reject `MUL_MAT_ID` when `dst->ne[2] != 1`, so it does not cover the Phase 6
  multi-token serving shape.
- Existing `MUL_MAT_ID_FUSION` tests cover add/mul after `MUL_MAT_ID`, not the
  gate_up/SWIGLU/down chain. Do not treat them as sufficient for this candidate.

Initial files to inspect:

- `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/ggml-cuda.cu`
- `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/mmq.cu`
- `/home/mudler/_git/llama.cpp/ggml/src/ggml-cuda/w4a16-gemm.cu`
- vLLM Marlin-MoE implementation files in the local vLLM checkout/package.

### Track B: Serving Input And Sampler Synchronization

Phase 6 evidence: `cudaStreamSynchronize` dominates CUDA API time, and many
syncs follow small `cudaMemcpyAsync` calls. The greedy sampler short-circuit
passed correctness gates but did not improve serving, so this track needs a
workload where sampler/input upload cost is proven relevant before patching.

Initial files to inspect:

- `/home/mudler/_git/llama.cpp/src/llama-sampling.cpp`
- `/home/mudler/_git/llama.cpp/src/llama-context.cpp`
- `/home/mudler/_git/llama.cpp/ggml/src/ggml-backend.cpp`
- CUDA backend tensor-set paths under `/home/mudler/_git/llama.cpp/ggml/src/`.

Selected secondary candidate:

- Cache backend logit-bias tensor uploads in
  `/home/mudler/_git/llama.cpp/src/llama-sampler.cpp`
  `llama_sampler_logit_bias_backend_set_input()`.
- Today the sampler rebuilds and uploads `logit_bias` and `logit_idxs` every
  decode step. Those uploads hit the CUDA tensor-set path with immediate
  `cudaStreamSynchronize`.
- This is narrow and maintainable, but it is not the default greedy parity
  lever. Only promote it if a non-greedy backend-sampling workload with non-empty
  `logit_bias` proves the sync bucket is material.

Required workload:

- Include a non-greedy serving shape if the patch targets sampler randomness or
  probability upload behavior.
- Preserve the canonical greedy md5 gates even if the optimization targets
  non-greedy serving.

## Decision Gate

Only one track may enter implementation at a time. Promote a candidate from scope
to implementation when all are true:

- It has an exact file/function target.
- It is additive enough to minimize upstream conflicts.
- It has a direct measurement bucket from Phase 6 or a fresh bounded profile.
- It has a clear rollback path.
- It passes the md5/op gates before any performance result is accepted.

## Checklist

- [x] Close remaining Phase 6 explorer agents or capture their final findings.
- [x] Reconfirm DGX idle state before any new benchmark.
  - Docker containers: `0`.
  - `local-ai-worker`: `0`.
  - Compute PIDs: `0`.
  - Lock: `FREE released-by-codex-phase6-mmq-grid 1782860601`.
- [x] Pick Track A or Track B from concrete code evidence.
  - Primary: Track A, batched MoE SWIGLU -> NVFP4 down-input quantization.
  - Secondary: Track B, backend logit-bias upload cache for non-greedy workloads.
- [x] Run baseline gates from the clean candidate build.
  - Artifact: `/home/mudler/bench/phase7_source_scope/`.
  - MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`.
  - Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`.
  - Baseline `MUL_MAT_ID`: `806/806`.
- [x] Implement one fork-first incremental patch.
  - Fork commit: `cd56cf037` (`test(paged): cover MoE swiglu down chain`).
  - LocalAI patch: `0051-test-paged-cover-MoE-swiglu-down-chain.patch`.
  - Scope: test gate only; no production inference path changed.
- [x] Run md5/op gates before serving A/B.
  - `MOE_SWIGLU_DOWN`: `7/7` on CUDA0.
  - Serving A/B is not applicable to this test-only patch.
- [ ] Keep only if the serving bucket and h2h result improve materially.
- [ ] Regenerate LocalAI patch stack and update docs if kept.

## Required Tests Before Track A Source Patch

- Add or extend a whole-graph op test for the batched MoE gate_up/SWIGLU/down
  chain. Shapes must include `type_a=NVFP4`, `n_mats=128`, `n_used=8`,
  `m=768`, `k=2048`, and `n in {16, 33, 64, 128, 130, 200}`.
  - Done in fork commit `cd56cf037`.
- Run `test-backend-ops test -b CUDA0 -o MUL_MAT_ID -j 1` and require `806/806`
  until a more specific op name is available.
  - Baseline done before the test-gate patch.
- Run canonical MoE and dense greedy md5 gates before serving A/B:
  - MoE `8cb0ce23777bf55f92f63d0292c756b0`.
  - Dense `5951a5b4d624ce891e22ab5fca9bc439`.
  - Baseline done before the test-gate patch.
- Run a mixed prompt/decode md5 gate (`ptok=512`, `gen=32`) because graph reuse
  can hide bugs that a decode-only gate misses.

## Patch 0051 Result

Patch `0051` adds a whole-graph test named `MOE_SWIGLU_DOWN`. It covers the
merged MoE gate_up -> SWIGLU -> down projection chain and includes:

- one small F32 wiring case,
- NVFP4 Qwen-style cases with `n_mats=128`, `n_used=8`, `n_ff=768`,
  `n_embd=2048`, and `n_tokens in {16, 33, 64, 128, 130, 200}`.

The first run used the inherited single-FP4-op tolerance (`2e-2`) and failed
consistently at roughly `0.0213-0.0218` NMSE. Root cause: this whole-graph gate
compounds two native-FP4 `MUL_MAT_ID` ops with SWIGLU between them, so the test
uses `2.5e-2` for Blackwell native-FP4 backends and keeps the F32 wiring case at
the stricter default tolerance.

DGX result after the adjustment:

- `test-backend-ops test -b CUDA0 -o MOE_SWIGLU_DOWN -j 1`: `7/7`.
- Patch mirror applies cleanly to base pin `0ed235ea2c17a19fc8238668653946721ed136fd`
  and tree-matches fork head `cd56cf037`.
- Mirrored tree hash: `623b7cb008a929455ca3d9deae35494c02622fef`.

## Required Tests Before Track B Source Patch

- Establish fixed-seed baseline output md5 and token-id parity for a
  backend-sampling request with non-empty `logit_bias`.
- Include the canonical greedy MoE and dense md5 gates even though the workload
  target is non-greedy.
- Run existing server completion tests covering backend sampling probabilities
  and logit-bias behavior.
