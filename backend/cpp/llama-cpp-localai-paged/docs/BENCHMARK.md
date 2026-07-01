# llama.cpp vLLM Parity Benchmark Ledger

This file tracks each parity attempt from Phase70 onward, plus the immediate
context needed to interpret the current record. Append every new attempt here
with artifact path, gates, benchmark rows, and decision.

## Current Status

- Goal: reach vLLM speed parity in llama.cpp on GB10.
- Current decision model: MoE `q36-35b-a3b-nvfp4`.
- Canonical paged MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`.
- Canonical dense md5: `5951a5b4d624ce891e22ab5fca9bc439`.
- Current tested source: DGX mirror
  `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Latest attempt: Phase72.
- Latest decision: keep `LLAMA_TTFT_PREFILL_FIRST=1`
  `LLAMA_TTFT_PREFILL_FIRST_MIN_WAITING=32` opt-in only. It regressed broad
  serving aggregate, decode, TTFT, and wall time at `n=8`, `n=32`, and `n=128`.

## Current Serving Record

Phase72 broader serving snapshot, MoE `PTOK=128`, `GEN=64`, `PARALLEL=128`.

Artifact:

- `/home/mudler/bench/phase72_ttft_min32_serving/20260701_160730`

| arm | n | agg_tps | decode_agg_tps | decode_perseq_tps | prefill_tps | ttft_mean_ms | wall_s |
|-----|--:|--------:|---------------:|------------------:|------------:|-------------:|-------:|
| llama default | `8` | `170.4` | `231.3` | `28.42` | `1693.4` | `786.4` | `3.004` |
| llama min32 | `8` | `158.5` | `218.4` | `26.27` | `1547.8` | `816.2` | `3.230` |
| vLLM | `8` | `260.0` | `305.9` | `37.32` | `4659.7` | `266.4` | `1.915` |
| llama default | `32` | `257.8` | `430.2` | `12.09` | `1720.4` | `2625.2` | `7.943` |
| llama min32 | `32` | `242.7` | `411.7` | `11.58` | `1617.4` | `2881.6` | `8.439` |
| vLLM | `32` | `463.6` | `601.0` | `17.60` | `5496.2` | `773.7` | `4.357` |
| llama default | `128` | `325.8` | `714.0` | `3.92` | `1628.8` | `7822.5` | `25.148` |
| llama min32 | `128` | `316.0` | `697.9` | `3.81` | `1606.0` | `8056.9` | `25.926` |
| vLLM | `128` | `666.4` | `1029.5` | `6.81` | `5292.5` | `2511.7` | `11.933` |

Ratios:

| n | min32/default agg | min32/default decode | min32/default TTFT | default decode/vLLM | min32 decode/vLLM |
|--:|------------------:|---------------------:|-------------------:|--------------------:|----------------:|
| `8` | `0.9302` | `0.9442` | `1.0379` | `0.7561` | `0.7140` |
| `32` | `0.9414` | `0.9570` | `1.0977` | `0.7158` | `0.6850` |
| `128` | `0.9699` | `0.9775` | `1.0300` | `0.6935` | `0.6779` |

Decision:

- Reject default-on for `LLAMA_TTFT_PREFILL_FIRST=1`
  `LLAMA_TTFT_PREFILL_FIRST_MIN_WAITING=32`.
- Keep min32 as opt-in only.
- The opt-in regressed aggregate, decode, TTFT, and wall time at every tested
  concurrency and widened the vLLM decode gap.

## Attempt Log

### Phase72: TTFT Min32 Broader Serving

- Date: 2026-07-01.
- Plan: `docs/superpowers/plans/2026-07-01-ttft-min32-serving-phase72.md`.
- Artifact:
  `/home/mudler/bench/phase72_ttft_min32_serving/20260701_160730`.
- Source: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Shape: MoE serving, `NPL=8 32 128`, prompt `128`, generation `64`,
  `PARALLEL=128`, `CTX=131072`.
- Env gate: `LLAMA_TTFT_PREFILL_FIRST=1`
  `LLAMA_TTFT_PREFILL_FIRST_MIN_WAITING=32`.

Gates:

| gate | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|------|---------|-----------|-----------|--------------|
| pre default | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| pre min32 | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | not run | not run |
| post default | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | not run | not run |
| post min32 | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | not run | not run |

Result:

- Reject default-on for min32 in the broader serving shape.
- Keep the scheduler knob opt-in only.
- min32 regressed aggregate, decode, TTFT, and wall time for every tested
  concurrency.

### Phase71: GDN Tensor-Core Revalidation

- Date: 2026-07-01.
- Plan: `docs/superpowers/plans/2026-07-01-gdn-tc-revalidation-phase71.md`.
- Artifact:
  `/home/mudler/bench/phase71_gdn_tc_revalidation/20260701_153425`.
- Source: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Shape: MoE prefill, `PP=512,2048`, `TG=4`, `B=32`, `CTX=131072`.

Canonical gates:

| gate | env | MoE md5 | dense md5 | `GATED_DELTA_NET` | `MUL_MAT` | `MUL_MAT_ID` |
|------|-----|---------|-----------|-------------------|-----------|--------------|
| default | none | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `46/46` | `1146/1146` | `806/806` |
| sequential-disabled | `GDN_CHUNK_MIN=2147483647` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `46/46` | not run | not run |
| serial-chunked | `GDN_TC=0 GDN_CHUNK_MIN=64` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `46/46` | not run | not run |
| forced M5 | `GDN_TC=4 GDN_CHUNK_MIN=64` | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `46/46` | not run | not run |

MoE prefill:

| arm | npp | S_PP t/s | T_PP s | S_TG t/s | total S t/s |
|-----|----:|---------:|-------:|---------:|------------:|
| default | `512` | `2313.57` | `7.082` | `401.82` | `2231.28` |
| sequential-disabled | `512` | `2198.28` | `7.453` | `392.50` | `2122.58` |
| serial-chunked | `512` | `1787.49` | `9.166` | `396.23` | `1740.12` |
| forced M5 | `512` | `2323.18` | `7.052` | `393.62` | `2238.13` |
| default | `2048` | `2422.88` | `27.049` | `389.91` | `2398.50` |
| sequential-disabled | `2048` | `2361.22` | `27.755` | `386.08` | `2337.91` |
| serial-chunked | `2048` | `1699.77` | `38.556` | `389.48` | `1688.69` |
| forced M5 | `2048` | `2420.52` | `27.075` | `388.72` | `2396.11` |

Ratios:

| npp | default/sequential S_PP | default/serial S_PP | forced/default S_PP |
|-----|------------------------:|---------------------:|--------------------:|
| `512` | `1.0524` | `1.2943` | `1.0042` |
| `2048` | `1.0261` | `1.4254` | `0.9990` |

Decision:

- Keep shipped GDN M5 default behavior.
- Do not reopen smaller GDN C32/QS/global-Ai32/kernel-reorder work on GB10.
- The stale "two-Gram PoC before M5 exists" framing is superseded by the
  existing `0047` M5 implementation and this revalidation.

### Phase70: BF16 F32 Output Broader Serving

- Date: 2026-07-01.
- Plan: `docs/superpowers/plans/2026-07-01-bf16-f32-output-broader-serving-phase70.md`.
- Artifact: `/home/mudler/bench/phase70_bf16_broader_serving/20260701_151500`.
- Source: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Shape: MoE serving, `NPL=8 32 128`, prompt `128`, generation `64`,
  `PARALLEL=128`, `CTX=131072`.

Gates:

| gate | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|------|---------|-----------|-----------|--------------|
| pre default | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| pre opt-in | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | not run |
| post default | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| post opt-in | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | not run |

Result:

- Default-on rejected.
- Opt-in remains correctness-clean, but broad serving is mixed-to-negative.

### Phase69: Patch Series Mirror Readiness

- Date: 2026-07-01.
- Plan: `docs/superpowers/plans/2026-07-01-patch-series-mirror-readiness-phase69.md`.
- Artifact: local dry-run only.
- Result: current `0001..0063` series matched Phase37 tree
  `dedb1182910eafe9f6875588dc8285bfb544cce5`; projected `0064..0073`
  matched fork HEAD tree `fcf5720b659c5e1e2b487ccf3c8f7289bb12b9c4`.
- Decision: patch regeneration is technically ready but blocked on explicit
  push approval by policy.

### Phase68: BF16 F32 Output Dense Serving

- Date: 2026-07-01.
- Plan: `docs/superpowers/plans/2026-07-01-bf16-f32-output-dense-serving-phase68.md`.
- Artifact: `/home/mudler/bench/phase68_bf16_dense_serving/20260701_145710`.
- Serving artifact:
  `/home/mudler/bench/phase68_bf16_dense_serving/20260701_145710/serving_ab_20260701_150249`.

Dense prefill:

| npp | default S_PP | opt-in S_PP | change |
|-----|-------------:|------------:|-------:|
| `512` | `973.13` | `975.52` | `+0.25%` |
| `2048` | `1019.88` | `1021.39` | `+0.15%` |

MoE serving `N=128`, prompt `128`, generation `128`:

| metric | default | opt-in | change |
|--------|--------:|-------:|-------:|
| `agg_tps` | `409.8` | `415.0` | `+1.27%` |
| `decode_agg_tps` | `615.3` | `627.2` | `+1.93%` |
| `prefill_tps` | `1630.2` | `1648.0` | `+1.09%` |
| `ttft_mean_ms` | `8574.7` | `8085.9` | `-5.70%` |
| `wall_s` | `39.978` | `39.480` | `-1.25%` |

Decision:

- Carry as default-off opt-in candidate pending broader serving evidence.

### Phase67: BF16 cuBLAS F32 Output

- Date: 2026-07-01.
- Plan: `docs/superpowers/plans/2026-07-01-bf16-cublas-f32-output-phase67.md`.
- Artifact: `/home/mudler/bench/phase67_bf16_f32_out/20260701_144909`.
- Fork commit: `ea0875d14 feat(cuda): gate BF16 cuBLAS F32 output`.
- DGX mirror commit: `14fd69f1e`.
- Env gate: `LLAMA_BF16_CUBLAS_F32_OUT=1`.

Gates:

| mode | MoE md5 | dense md5 | `MUL_MAT` |
|------|---------|-----------|-----------|
| default | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` |
| opt-in | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` |

MoE prefill:

| npp | default S_PP | opt-in S_PP | change |
|-----|-------------:|------------:|-------:|
| `512` | `2347.41` | `2402.34` | `+2.34%` |
| `2048` | `2440.18` | `2456.54` | `+0.67%` |

Decision:

- Keep default-off pending dense and serving A/B.
