# Phase 37: cuBLAS Tensor-Name Trace

**Status:** DONE.

**Scope:** additive follow-up to patch `0062`. Extend the default-off
`LLAMA_CUBLAS_ROUTE_TRACE=<n>` diagnostic with `src0`, `src1`, and `dst` tensor
names. No route or numeric behavior change.

## Checklist

- [x] Add RED/GREEN helper coverage for cuBLAS tensor-name trace fields.
- [x] Wire tensor names from the generic cuBLAS path.
- [x] Build CUDA targets on DGX.
- [x] Run md5 gates with trace off and trace on.
- [x] Run backend op gates with trace off and trace on.
- [x] Capture n128 serving name trace.
- [x] Run post-serving md5/op gates.
- [x] Commit fork and DGX mirror, export LocalAI patch `0063`.

## Result

Artifact: `/home/mudler/bench/phase37_cublas_name_trace/20260701_083227`.

- Local fork commit: `2d590d770 feat(cuda): trace cublas tensor names`
- DGX mirror commit: `2cbb61969 feat(cuda): trace cublas tensor names`
- Local/DGX tree after Phase 37: `dedb1182910eafe9f6875588dc8285bfb544cce5`
- LocalAI patch: `backend/cpp/llama-cpp-localai-paged/patches/paged/0063-feat-cuda-trace-cublas-tensor-names.patch`

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

`LLAMA_CUBLAS_ROUTE_TRACE=4096`, n128 MoE serving:

| cuBLAS route | count |
|--------------|------:|
| `bf16_tc` | 2884 |
| `sgemm` | 1212 |

Top named entries were per-layer projections:

- `bf16_tc type=30 src0=blk.N.attn_gate.weight src1=attn_norm-N dst=z-N`
- `bf16_tc type=30 src0=blk.N.ssm_out.weight src1=final_output-N dst=linear_attn_out-N`
- `sgemm type=0 src0=blk.N.ffn_gate_inp.weight src1=attn_post_norm-N dst=ffn_moe_logits-N`
- `sgemm type=0 src0=blk.N.ffn_gate_inp_shexp.weight src1=attn_post_norm-N dst=shared_expert_gate-N`

The traced serving run is diagnostic only; stderr tracing still depresses
throughput and can create client-window disconnects. Post-serving md5/op gates
remained green.

## Decision

- The Phase 36 F32 SGEMM bucket is not an opaque missed projection. It is mostly
  MoE gating and shared-expert gate projection tensors whose weights are F32.
- The next route-policy phase should not blindly force these to BF16. First
  inspect model-load tensor types for `ffn_gate_inp*` and decide whether a
  weight-conversion or graph-build route change is precision-safe. Any change
  needs md5/op gates and, if tensor type conversion is involved, KL validation.
