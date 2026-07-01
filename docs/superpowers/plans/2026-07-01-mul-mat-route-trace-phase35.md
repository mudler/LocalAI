# Phase 35: Regular MUL_MAT Route Trace

**Goal:** Split the projection-heavy regular `MUL_MAT` serving bucket into concrete dispatch routes before attempting a projection optimization.

**Scope:** llama.cpp fork first, then LocalAI patch `0061`. Instrumentation only; no route or numeric behavior change.

## Plan

- [x] Inspect regular `ggml_cuda_mul_mat` dispatch order and projection bucket docs.
- [x] Dispatch sidecar explorers for llama.cpp route context and vLLM projection context.
- [x] Add failing host-only tests for regular `MUL_MAT` route classification.
- [x] Implement `ggml_cuda_mul_mat_route_shape_make()` and formatter.
- [x] Wire default-off `LLAMA_MUL_MAT_ROUTE_TRACE=<n>` around regular `MUL_MAT`.
- [x] Build and run `test-cuda-mmq-shape-trace` locally.
- [x] Build `llama-server`, `llama-completion`, `test-backend-ops`, and `test-cuda-mmq-shape-trace` on DGX.
- [x] Run default-off and trace-enabled md5/op gates.
- [x] Run n128 serving route trace and parse route/type/shape counts.
- [x] Run post-serving md5/op gates.
- [x] Commit fork and DGX mirror, export LocalAI patch `0061`.
- [x] Update README, parity docs, handoff, and patch maintenance.
- [x] Re-run strict patch-series mirror invariant.

## Results

Artifact: `/home/mudler/bench/phase35_mul_mat_route_trace/20260701_074359`.

Commits:

- Fork: `486c28c63 feat(cuda): trace mul mat routes`
- DGX mirror: `18f7ad005 feat(cuda): trace mul mat routes`
- LocalAI patch: `backend/cpp/llama-cpp-localai-paged/patches/paged/0061-feat-cuda-trace-mul-mat-routes.patch`

Gates:

- Default-off MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
- Default-off dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
- Trace-enabled MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
- Trace-enabled dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
- Post-serving MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
- Post-serving dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
- `MUL_MAT`: `1146/1146` in default, trace-enabled, and post-serving gates
- `MUL_MAT_ID`: `806/806` in default, trace-enabled, and post-serving gates

n128 route trace:

- `mat_f`: 2888
- `op_cublas`: 2292
- `mmq`: 1328
- `vec_q`: 1214
- `vec_f`: 470

BF16 (`type=30`) was the largest traced type: 3965 calls, split into `mat_f=2485` and `op_cublas=1330`. Top BF16 shapes were `mat_f ne1=12` 775, `op_cublas ne1=18` 760, and `mat_f ne1=8` 570.

Decision: next projection work should add cuBLAS/MMF subroute detail or test a narrow BF16 route policy for generic `op_cublas` shapes. Do not spend effort on batched cuBLAS for this measured n128 serving slice.
