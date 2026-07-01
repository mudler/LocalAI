# Phase 36: cuBLAS Route Trace

**Status:** DONE.

**Scope:** llama.cpp fork first, then LocalAI patch `0062`. Instrumentation only;
no route, branch, or numeric behavior change.

## Checklist

- [x] Add RED/GREEN helper tests for cuBLAS subroute classification.
- [x] Add default-off `LLAMA_CUBLAS_ROUTE_TRACE=<n>` around generic cuBLAS
  `MUL_MAT` dispatch.
- [x] Build CUDA targets on DGX.
- [x] Run md5 gates with trace off and trace on.
- [x] Run backend op gates with trace off and trace on.
- [x] Capture n128 serving route distribution.
- [x] Run post-serving md5/op gates.
- [x] Commit fork and DGX mirror, export LocalAI patch `0062`.

## Result

Artifact: `/home/mudler/bench/phase36_cublas_route_trace/20260701_081228`.

- Local fork commit: `38c4ef2e4 feat(cuda): trace cublas routes`
- DGX mirror commit: `e0224393a feat(cuda): trace cublas routes`
- Local/DGX tree after Phase 36: `208189d119efe27477f1900cc6f7428bd1720449`
- LocalAI patch: `backend/cpp/llama-cpp-localai-paged/patches/paged/0062-feat-cuda-trace-cublas-routes.patch`

## Gates

| check | status | actual |
|-------|--------|--------|
| default-off MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| default-off dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| trace-enabled MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| trace-enabled dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| post-serving MoE md5 | ok | `8cb0ce23777bf55f92f63d0292c756b0` |
| post-serving dense md5 | ok | `5951a5b4d624ce891e22ab5fca9bc439` |
| `MUL_MAT` | ok | `1146/1146` default, trace, post-serving |
| `MUL_MAT_ID` | ok | `806/806` default, trace, post-serving |

## Serving Trace

`LLAMA_CUBLAS_ROUTE_TRACE=8192`, n128 MoE serving:

| cuBLAS route | count |
|--------------|------:|
| `bf16_tc` | 5681 |
| `sgemm` | 2511 |

Top shapes:

| route | shape | count |
|-------|-------|------:|
| `bf16_tc` | `type=30 row_diff=32 src1_ncols=510 ne00=2048 ne10=2048` | 360 |
| `bf16_tc` | `type=30 row_diff=8192 src1_ncols=510 ne00=2048 ne10=2048` | 240 |
| `bf16_tc` | `type=30 row_diff=2048 src1_ncols=510 ne00=4096 ne10=4096` | 240 |
| `sgemm` | `type=0 row_diff=256 src1_ncols=510 ne00=2048 ne10=2048` | 240 |
| `sgemm` | `type=0 row_diff=1 src1_ncols=510 ne00=2048 ne10=2048` | 240 |

The traced serving run is diagnostic only: heavy stderr tracing depressed
throughput and the client window reported disconnects at shutdown. The
post-serving md5/op gates above stayed green.

## Decision

- Generic cuBLAS serving calls are BF16 tensor-core and F32 SGEMM; the measured
  route does not show NVFP4 cuBLAS or batched cuBLAS as the next bucket.
- The next projection phase should investigate why the F32 SGEMM shapes remain
  `type=0` and whether they are expected glue/projection tensors or a missed
  BF16 route. Any route-policy change must be separately gated by the same md5
  and `test-backend-ops` checks before benchmarking.
