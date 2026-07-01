# llama.cpp vLLM Parity Benchmark Ledger

This file tracks each parity attempt from Phase70 onward, plus the immediate
context needed to interpret the current record. Append every new attempt here
with artifact path, gates, benchmark rows, and decision.

## Current Status

- Goal: reach vLLM speed parity in llama.cpp on GB10.
- Current decision model: MoE `q36-35b-a3b-nvfp4`.
- Canonical paged MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`.
- Canonical dense md5: `5951a5b4d624ce891e22ab5fca9bc439`.
- Current tested source: DGX mirror `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Latest attempt: Phase70.
- Latest decision: keep `LLAMA_BF16_CUBLAS_F32_OUT=1` default-off. It is
  correctness-clean but not serving-safe enough to default on.

## Current Serving Record

Phase70 broader serving snapshot, MoE `PTOK=128`, `GEN=64`, `PARALLEL=128`.

Artifact:

- `/home/mudler/bench/phase70_bf16_broader_serving/20260701_151500`

| arm | n | agg_tps | decode_agg_tps | decode_perseq_tps | prefill_tps | ttft_mean_ms | wall_s |
|-----|--:|--------:|---------------:|------------------:|------------:|-------------:|-------:|
| llama default | `8` | `178.5` | `242.6` | `29.82` | `1767.2` | `754.8` | `2.868` |
| llama opt-in | `8` | `158.8` | `218.3` | `26.60` | `1541.1` | `848.9` | `3.225` |
| vLLM | `8` | `260.9` | `299.5` | `36.67` | `5415.6` | `239.0` | `1.917` |
| llama default | `32` | `250.1` | `418.7` | `11.75` | `1661.2` | `2717.0` | `8.187` |
| llama opt-in | `32` | `247.9` | `417.6` | `11.79` | `1650.3` | `2803.9` | `8.261` |
| vLLM | `32` | `465.3` | `608.4` | `17.74` | `5394.4` | `782.7` | `4.314` |
| llama default | `128` | `322.5` | `706.2` | `3.87` | `1613.9` | `7836.5` | `25.401` |
| llama opt-in | `128` | `324.8` | `697.9` | `3.88` | `1671.1` | `7720.9` | `25.220` |
| vLLM | `128` | `659.9` | `1020.4` | `6.75` | `5228.0` | `2543.1` | `12.060` |

Ratios:

| n | opt/default agg | opt/default decode | opt/default TTFT | default decode/vLLM | opt decode/vLLM | default agg/vLLM | opt agg/vLLM |
|--:|----------------:|-------------------:|-----------------:|--------------------:|----------------:|-----------------:|-------------:|
| `8` | `0.8896` | `0.8998` | `1.1247` | `0.8100` | `0.7289` | `0.6842` | `0.6087` |
| `32` | `0.9912` | `0.9974` | `1.0320` | `0.6882` | `0.6864` | `0.5375` | `0.5328` |
| `128` | `1.0071` | `0.9882` | `0.9852` | `0.6921` | `0.6839` | `0.4887` | `0.4922` |

Decision:

- Reject default-on for `LLAMA_BF16_CUBLAS_F32_OUT=1`.
- Keep as default-off opt-in only.
- The opt-in regressed `n=8` throughput and TTFT materially, and slightly
  widened the vLLM decode gap at `n=32` and `n=128`.

## Attempt Log

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
