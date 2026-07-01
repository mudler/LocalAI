# Phase 34: MMID Route Trace

**Goal:** Add a default-off `MUL_MAT_ID` route classifier so serving traces can prove whether current n128 MoE inference uses graph-safe MMVQ/grouped-MMQ paths or the host-sync fallback.

**Scope:** llama.cpp fork first, then LocalAI patch `0060`. Instrumentation only; no route or numeric behavior change.

## Plan

- [x] Inspect the current `ggml_cuda_mul_mat_id` dispatch order.
- [x] Add a failing host-only test for route classification and trace formatting.
- [x] Implement `ggml_cuda_mmid_route_shape_make()` and formatter in the existing CUDA trace helper.
- [x] Wire `LLAMA_MOE_MMID_ROUTE_TRACE=<n>` in `ggml_cuda_mul_mat_id` using the same predicates as dispatch.
- [x] Build and run `test-cuda-mmq-shape-trace` locally.
- [x] Build `llama-server`, `llama-completion`, `test-backend-ops`, and `test-cuda-mmq-shape-trace` on DGX.
- [x] Run default-off and trace-enabled md5/op gates.
- [x] Run n128 serving trace and parse route counts.
- [x] Run post-serving md5/op gates.
- [x] Commit fork and DGX mirror, export LocalAI patch `0060`.
- [x] Update README, parity docs, handoff, and patch maintenance.
- [x] Re-run strict patch-series mirror invariant.

## Results

Artifact: `/home/mudler/bench/phase34_mmid_route_trace/20260701_072737`.

Commits:

- Fork: `6c332094c feat(cuda): trace moe mmid routes`
- DGX mirror: `34a256d14 feat(cuda): trace moe mmid routes`
- LocalAI patch: `backend/cpp/llama-cpp-localai-paged/patches/paged/0060-feat-cuda-trace-moe-mmid-routes.patch`

Gates:

- Default-off MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
- Default-off dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
- Trace-enabled MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
- Trace-enabled dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
- Post-serving MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
- Post-serving dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
- `MUL_MAT_ID`: `806/806` in default, trace-enabled, and post-serving gates

n128 route trace:

- `mmq`: 2776
- `mmvq`: 1320
- `host_sync=0`: 4096
- Top shapes: `mmq ne2=12` 1096, `mmq ne2=18` 480, `mmvq ne2=8` 360

Decision: host-sync fallback is not firing in the current n128 serving path. The next phase should not chase fallback avoidance; it should either target grouped-MMQ small-M internal partitioning or pivot to the next measured bottleneck.
