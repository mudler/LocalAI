# Phase 60: Current W4A16 Prefill Profile

## Goal

Re-profile the current clean W4A16 grouped MoE prefill path after the Phase1-5
W4A16 work, then decide whether another low-conflict W4A16 patch is justified.

## Artifact

- `/home/mudler/bench/phase60_w4a16_current_profile/20260701_104915`

## Source State

- DGX mirror: `~/llama-phase6-source`
- Branch: `localai-paged`
- Commit: `2cbb61969443cf52aa1aa58eb9f5a8d7c20a7780`

## Gates

| phase | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-----------|--------------|
| pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

DGX cleanup:

- Docker containers: `0`
- GPU compute apps: `0`
- Lock released: `FREE phase60-cleanup 20260701T105438Z`

## End-to-End A/B

MoE `llama-batched-bench`, `npl=32`, `ntg=4`, `npp=512,2048`:

| path | PP | S_PP t/s | T_PP s | S_TG t/s | total S t/s |
|------|----|----------|--------|----------|-------------|
| default FP4-MMQ | `512` | `2327.69` | `7.039` | `399.87` | `2243.83` |
| default FP4-MMQ | `2048` | `2423.20` | `27.045` | `391.58` | `2398.94` |
| forced W4A16 | `512` | `1451.00` | `11.291` | `319.32` | `1412.21` |
| forced W4A16 | `2048` | `1482.76` | `44.199` | `303.40` | `1471.61` |

Forced W4A16 remains:

- `0.623x` default FP4-MMQ at `npp=512` (`-37.7%` S_PP).
- `0.612x` default FP4-MMQ at `npp=2048` (`-38.8%` S_PP).

## `npp=512` Kernel Summary

Default FP4-MMQ top rows:

| bucket | time % | total time |
|--------|--------|------------|
| `mul_mat_q<nvfp4,128>` | `39.2%` | `2.712s` |
| `gated_delta_net_chunked_cuda` | `12.2%` | `0.843s` |
| `quantize_mmq_nvfp4` | `4.5%` | `0.314s` |

Forced W4A16 top rows:

| bucket | time % | total time |
|--------|--------|------------|
| `w4a16_grouped_kernel<32,128,1,4,2>` | `42.5%` | `4.142s` |
| `k_get_rows_float<float,float>` | `11.2%` | `1.094s` |
| `gated_delta_net_chunked_cuda` | `8.6%` | `0.838s` |
| `w4a16_cast_act_f32_bf16` | `5.3%` | `0.517s` |
| residual `quantize_mmq_nvfp4` | `1.4%` | `0.132s` |

## Decision

Reject another small W4A16 body/metadata/cast tweak as the next parity phase.

The current W4A16 path avoids most activation quantization, but the grouped
kernel is still `1.53x` slower than default MMQ's main `mul_mat_q` bucket at
`npp=512` (`4.142s` versus `2.712s`) and sorted activation gathers add another
`1.094s`. Eliminating the cast kernel entirely would recover only `5.3%` of the
forced-W4A16 profile and would not close the `37-39%` end-to-end S_PP loss.

Next W4A16 work would need a larger redesign that both improves the grouped
kernel body and removes or fuses the sorted activation gather. That is outside
the low-conflict incremental patch track. For near-term parity work, return to
the broader prefill/GDN/MoE design track or a hardware-pivot benchmark rather
than another W4A16 micro-patch.
