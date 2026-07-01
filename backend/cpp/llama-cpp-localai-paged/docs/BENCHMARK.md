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
- Latest attempt: Phase81.
- Latest decision: default-off `LLAMA_QWEN35_GDN_S_CACHE_TYPE=bf16` is a
  promising carry-forward candidate, not default-on. Same-source decode-only
  profiling cut normalized `gdn_core` from about `2.34 ms/launch` to
  `1.20 ms/launch` and total kernel time from `3.6157 s` to `3.5244 s`, but MoE
  greedy md5 changed from the paged canonical `8cb0...` to `07db...`. Dense md5
  stayed canonical and `MUL_MAT`/`MUL_MAT_ID` gates stayed green. Phase82 must
  run the full f16-reference KL gate and serving A/B before this can be promoted
  beyond an opt-in experiment.

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

### Phase81: Qwen35 BF16 Persistent GDN S-Cache

- Date: 2026-07-01.
- Source: `/home/mudler/llama-phase81-bf16-state-source`, local fork patch in
  `/home/mudler/_git/llama.cpp` branch `localai-paged`.
- Build artifact: `/home/mudler/llama-phase81-bf16-state-source/build-cuda`.
- Gate artifact:
  `/home/mudler/bench/phase81_bf16_s_cache_gates/20260701_161350`.
- Profile artifacts:
  - default F32:
    `/home/mudler/bench/phase81_bf16_s_cache_profile/default_20260701_162117`
  - BF16 S-cache:
    `/home/mudler/bench/phase81_bf16_s_cache_profile/bf16_20260701_162028`
- KL smoke artifact:
  `/home/mudler/bench/phase81_bf16_s_cache_kl/20260701_162322`.
- Result type: source experiment. `LLAMA_QWEN35_GDN_S_CACHE_TYPE=bf16`
  stores Qwen35/Qwen35MoE persistent recurrent S cache in BF16 while keeping GDN
  recurrence math, q/k/v/g/beta, and output in F32. Default remains F32.

Implementation scope:

- Added BF16 state support for `ggml_gated_delta_net_inplace_ids` only.
- Added CPU/CUDA BF16 state load/store conversion at the persistent cache
  boundary.
- Added BF16 CPU/CUDA `SCALE` support because recurrent cache zeroing uses
  `ggml_scale_inplace(..., 0)` on the S cache.
- Added tests for BF16 `GATED_DELTA_NET_INPLACE_IDS` and BF16 in-place `SCALE`.

Local verification:

| check | result |
|-------|--------|
| RED test before implementation | `ggml_gated_delta_net_inplace_ids` rejected BF16 state at `state->type == GGML_TYPE_F32` |
| CPU `SCALE -p bf16` | `1/1` passed |
| CPU `GATED_DELTA_NET_INPLACE_IDS` | `2/2` passed |
| DGX CUDA build | completed for `llama-completion`, `llama-batched-bench`, `test-backend-ops`, `llama-server`, later `llama-perplexity` |

Gates:

| mode | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|------|---------|-----------|-----------|--------------|
| default F32 | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| BF16 S-cache | `07db32c2bcb78d17a43ed18bc22705cd` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

Profile:

| arm | env | active slots | depth start | depth mid | total kernel s | GDN ms | GDN share | `gdn_core` ms | `gdn_core` launches | `gdn_core`/launch | `mmq_nvfp4` ms |
|-----|-----|-------------:|------------:|----------:|---------------:|-------:|----------:|--------------:|--------------------:|------------------:|---------------:|
| default F32 | none | `128` | `65` | `87` | `3.6157` | `1480.44` | `40.94%` | `1399.30` | `599` | `2.336 ms` | `1394.28` |
| BF16 S-cache | `LLAMA_QWEN35_GDN_S_CACHE_TYPE=bf16` | `128` | `65` | `91` | `3.5244` | `961.61` | `27.28%` | `863.57` | `720` | `1.199 ms` | `1665.38` |

KL smoke against same-source F32 base:

| check | result |
|-------|--------|
| shape | MoE, `-c 256 -b 256 --chunks 32`, Wikitext-2 raw |
| F32 floor KLD vs F32 base | `0.000000 +/- 0.000000`, same-top-p `99.975%` |
| BF16 S-cache KLD vs F32 base | `0.055499 +/- 0.001705`, same-top-p `88.361%` |
| BF16 PPL ratio vs F32 base | `1.010356 +/- 0.005817` |

Decision:

- Carry forward as a default-off candidate and run Phase82 full gates.
- Do not make it default-on: MoE greedy md5 is not canonical, and the KL smoke is
  not the full f16-reference acceptance gate.
- Required Phase82 before patch-series promotion:
  full f16-reference KL gate for MoE and dense, same-source serving A/B against
  F32 default and vLLM, then regenerate LocalAI patches from the fork only if
  serving and KL both hold.

### Phase80: GDN Identity-Ids Shortcut Source A/B

- Date: 2026-07-01.
- Artifact root:
  `/home/mudler/bench/phase80_gdn_identity_ids_ab/20260701_153927`.
- Arms:
  - `A_baseline`: `/home/mudler/llama-phase6-source`, default source
    `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
  - `B_identity`: `/home/mudler/llama-phase80-gdn-identity-source`, one-file
    default-off source patch in `ggml/src/ggml-cuda/gated_delta_net.cu`,
    enabled with `GDN_ASSUME_IDENTITY_IDS=1`.
- Result type: source A/B of an identity-ids shortcut that skips the
  non-identity scratch gather for one-token final-state decode and reads the
  in-place state cache directly.
- Shape: same as Phase77 decode-only graph-node profile.
- Build: candidate CUDA build completed for `llama-completion`,
  `llama-batched-bench`, `test-backend-ops`, and `llama-server`.

Gates:

| arm | phase | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|-----|-------|---------|-----------|-----------|--------------|
| `A_baseline` | pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| `A_baseline` | post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| `B_identity` | pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| `B_identity` | post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

Capture:

| arm | active slots | depth start | depth mid | `gdn_core` launches |
|-----|-------------:|------------:|----------:|--------------------:|
| `A_baseline` | `128` | `74` | `96` | `600` |
| `B_identity` | `128` | `65` | `87` | `600` |

Result:

| arm | env | total kernel s | GDN ms | GDN share | `gdn_core` ms | `gdn_gather` ms | GDN macro launches |
|-----|-----|---------------:|-------:|----------:|--------------:|----------------:|------------------:|
| `A_baseline` | none | `3.7132` | `1493.57` | `40.22%` | `1411.65` | `0.79` | `3600` |
| `B_identity` | `GDN_ASSUME_IDENTITY_IDS=1` | `3.5685` | `1489.96` | `41.75%` | `1409.28` | not present | `3000` |

Decision:

- Reject carry-forward/default for `GDN_ASSUME_IDENTITY_IDS=1`.
- The shortcut did remove the `gdn_gather` fine bucket and kept all gates
  green, but the removed bucket was only `0.79 ms` over the capture and
  `gdn_core` was effectively unchanged.
- The identity assumption is too narrow/risky for the size of the measured win.
  Do not spend more parity time on gather-only GDN shortcuts unless a future
  profile shows gather becoming material.
- Keep the next real GDN source scope on recurrent-state precision/traffic.

### Phase79: GDN Decode BV32 Source A/B

- Date: 2026-07-01.
- Artifact root:
  `/home/mudler/bench/phase79_gdn_decode_bv32_ab/20260701_152530`.
- Arms:
  - `A_baseline`: `/home/mudler/llama-phase6-source`, default source
    `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
  - `B_bv32`: `/home/mudler/llama-phase79-gdn-source`, one-file default-off
    source patch in `ggml/src/ggml-cuda/gated_delta_net.cu`, enabled with
    `GDN_DECODE_BV32=1`.
- Result type: source A/B of a decode-only `S_v=128`, `n_tokens=1`,
  scalar-gate smaller-V-tile kernel inspired by vLLM's packed decode topology.
- Shape: same as Phase77 decode-only graph-node profile.
- Build: candidate CUDA build completed for `llama-completion`,
  `llama-batched-bench`, `test-backend-ops`, and `llama-server`.

Gate detail:

- Candidate default gates before profiling were green: MoE md5
  `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
  `5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT 1146/1146`,
  `MUL_MAT_ID 806/806`.
- Candidate opt-in gates before the A/B were green with `GDN_DECODE_BV32=1`:
  same md5 values, `MUL_MAT 1146/1146`, `MUL_MAT_ID 806/806`.
- A/B baseline pre-gates were green. Baseline post-gate first run hit a
  transient `MUL_MAT 1145/1146` failure on
  `MUL_MAT(type_a=q4_1,type_b=f32,m=16,n=1,k=256,...)`; immediate retry at
  `A_baseline/gate_post_retry` was green for md5, `MUL_MAT 1146/1146`, and
  `MUL_MAT_ID 806/806`.
- `B_bv32` pre/post gates were green with `GDN_DECODE_BV32=1`.

Capture:

| arm | active slots | depth start | depth mid | `gdn_core` launches |
|-----|-------------:|------------:|----------:|--------------------:|
| `A_baseline` | `128` | `67` | `89` | `600` |
| `B_bv32` | `128` | `72` | `93` | `570` |

Result:

| arm | env | total kernel s | GDN ms | GDN share | `gdn_core` ms | `gdn_core`/launch | `mmq_nvfp4` ms |
|-----|-----|---------------:|-------:|----------:|--------------:|------------------:|---------------:|
| `A_baseline` | none | `3.6274` | `1493.14` | `41.16%` | `1411.46` | `2.352` | `1392.60` |
| `B_bv32` | `GDN_DECODE_BV32=1` | `3.5739` | `1502.89` | `42.05%` | `1426.17` | `2.502` | `1363.65` |

Decision:

- Reject the BV32 decode source patch.
- Although all safety gates passed, normalized `gdn_core` worsened by about
  `6.4%` per launch and the GDN macro bucket increased.
- Lower total kernel time in the candidate is not accepted as a win because the
  capture contains fewer graph-node launches (`570` vs `600` `gdn_core`), while
  the per-launch GDN core cost is worse.
- Do not retry smaller V-tile decode topology without a new profile-level
  reason. The next GDN source hypothesis should attack recurrent-state
  precision/traffic or another structural difference from vLLM.

### Phase78: GDN Decode Launch-Shape Sweep

- Date: 2026-07-01.
- Baseline artifact:
  `/home/mudler/bench/phase77_moe_decode_only_profile/20260701_150134`.
- Sweep artifacts:
  - `/home/mudler/bench/phase78_gdn_launch_sweep/nw8_cpw8_20260701_150654`
  - `/home/mudler/bench/phase78_gdn_launch_sweep/nw16_cpw4_20260701_150954`
- Source baseline: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Result type: env-gated launch-shape sweep only; no source change.
- Shape: same as Phase77 decode-only graph-node profile.

Result:

| arm | env | gate status | GDN ms | GDN share | `gdn_core` ms | `gdn_core` share | `mmq_nvfp4` ms |
|-----|-----|-------------|-------:|----------:|--------------:|-----------------:|---------------:|
| Phase77 default | none | pre/post green | `1489.71` | `41.20%` | `1408.33` | `38.95%` | `1383.50` |
| sweep `8x8` | `GDN_NW=8 GDN_CPW=8` | pre/post green | `1525.86` | `41.94%` | `1443.55` | `39.68%` | `1366.33` |
| sweep `16x4` | `GDN_NW=16 GDN_CPW=4` | rejected | not run | not run | not run | not run | not run |

Gate detail:

- `8x8`: pre/post MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`, dense md5
  `5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT 1146/1146`,
  `MUL_MAT_ID 806/806`.
- `16x4`: completion md5 and `MUL_MAT 1146/1146` passed, but
  `MUL_MAT_ID` failed `805/806`; rejected before profiling.

Decision:

- Keep the current default `GDN_NW=16 GDN_CPW=8`.
- Do not spend more GB10 time on launch-shape retunes without a new hypothesis.
- The funded source path remains a structural default-off GDN decode A/B/PoC
  that reduces the Phase77 `gdn_core` bucket, not another existing-env sweep.

### Phase77: MoE Decode-Only Graph-Node Profile

- Date: 2026-07-01.
- Artifact:
  `/home/mudler/bench/phase77_moe_decode_only_profile/20260701_150134`.
- Setup-hiccup artifact:
  `/home/mudler/bench/phase77_moe_decode_only_profile/20260701_145815`.
- Source baseline: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Result type: current-stack llama.cpp decode-only graph-node profile; no
  source change.
- Shape: MoE `q36-35b-a3b-nvfp4`, `N=128`, long-running `/completion`
  requests, `N_PREDICT=2048`, capture after active decode.
- Capture window: active slots `128`; median decoded depth `67` at start and
  `89` mid-capture; `CAPTURE_SECONDS=4`.
- Profiler: `nsys launch --cuda-graph-trace=node`, bucketed with
  `/home/mudler/bench/bucket2.py`.

Gates:

| phase | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-----------|--------------|
| pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

Macro buckets:

| bucket | time ms | share | instances |
|--------|--------:|------:|----------:|
| GDN | `1489.71` | `41.20%` | `3600` |
| MoE/FFN-GEMM | `1400.77` | `38.74%` | `7220` |
| bf16/fp8-proj | `352.90` | `9.76%` | `7400` |
| layout-copy | `69.85` | `1.93%` | `10400` |
| act-quant | `67.63` | `1.87%` | `4820` |
| FA | `36.74` | `1.02%` | `600` |

Fine buckets:

| bucket | macro | time ms | share | instances |
|--------|-------|--------:|------:|----------:|
| `gdn_core` | GDN | `1408.33` | `38.95%` | `600` |
| `mmq_nvfp4` | MoE/FFN-GEMM | `1383.50` | `38.26%` | `4820` |
| `gdn_conv` | GDN | `71.76` | `1.98%` | `1200` |
| `gdn_l2norm` | GDN | `8.81` | `0.24%` | `1200` |
| `gdn_gather` | GDN | `0.80` | `0.02%` | `600` |

Decision:

- Phase77 confirms Phase76's GDN bucket is not only prompt/prefill
  contamination. In an isolated decode window, `gdn_core` is the largest fine
  bucket and is slightly larger than `mmq_nvfp4`.
- This supersedes the Phase75 no-GB10-GDN-source stance. The source-funded path
  is no longer C=64 prefill inverse work; it is a narrow default-off GDN decode
  A/B or standalone PoC based on the direct recurrent/packed decode structure
  found in vLLM.
- Acceptance gate for the next source attempt:
  reduce the Phase77 `gdn_core` bucket materially, keep pre/post md5 and
  `MUL_MAT`/`MUL_MAT_ID` green, and show no serving/decode throughput
  regression under the same decode-only capture shape.

### Phase76: Current MoE Serving Graph-Node Profile

- Date: 2026-07-01.
- Artifact:
  `/home/mudler/bench/phase76_current_moe_profile/20260701_145116`.
- Setup-hiccup artifacts:
  `/home/mudler/bench/phase76_current_moe_profile/20260701_144754` and
  `/home/mudler/bench/phase76_current_moe_profile/20260701_144929`.
- Source baseline: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Result type: current-stack llama.cpp graph-node serving profile; no source
  change.
- Shape: MoE `q36-35b-a3b-nvfp4`, `n=128`, `PTOK=128`, `GEN=64`,
  `PARALLEL=128`, `CTX=131072`, production defaults.
- Profiler: `nsys launch --cuda-graph-trace=node`, bucketed with
  `/home/mudler/bench/bucket2.py`.

Gates:

| phase | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-----------|--------------|
| pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| post | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

Serving result under graph-node profiling:

| n | agg_tps | decode_agg_tps | decode_perseq_tps | prefill_tps | ttft_mean_ms | wall_s |
|--:|--------:|---------------:|------------------:|------------:|-------------:|-------:|
| `128` | `204.1` | `320.7` | `2.06` | `1490.1` | `8365.1` | `40.146` |

Macro buckets:

| bucket | time ms | share | instances |
|--------|--------:|------:|----------:|
| GDN | `6669.16` | `32.88%` | `25980` |
| MoE/FFN-GEMM | `6264.88` | `30.88%` | `54406` |
| bf16/fp8-proj | `2772.38` | `13.67%` | `53880` |
| layout-copy | `1265.44` | `6.24%` | `81280` |
| ew-mul(weight/norm/GDN) | `734.61` | `3.62%` | `52464` |
| act-quant | `678.95` | `3.35%` | `37526` |
| FA | `264.50` | `1.30%` | `3660` |

Fine buckets:

| bucket | macro | time ms | share | instances |
|--------|-------|--------:|------:|----------:|
| `gdn_core` | GDN | `5876.94` | `28.97%` | `4680` |
| `gdn_conv` | GDN | `454.03` | `2.24%` | `7260` |
| `gdn_gather` | GDN | `237.87` | `1.17%` | `4680` |
| `gdn_l2norm` | GDN | `100.32` | `0.49%` | `9360` |
| `mmq_nvfp4` | MoE/FFN-GEMM | `6055.03` | `29.85%` | `34162` |

Decision:

- Phase76 contradicts the Phase75 assumption that GDN decode is not on the
  current critical path. Under graph-node current serving, GDN is the largest
  GPU-kernel macro bucket and `gdn_core` alone is nearly `29%`.
- Do not patch `gated_delta_net.cu` yet. This profile is llama-only and
  graph-node tracing depresses absolute throughput, so it is a source-funding
  signal, not a source patch gate.
- Fund Phase77 as a narrow proof before backend edits:
  compare current `gdn_core` against a vLLM-style direct recurrent/packed decode
  PoC or an in-backend default-off A/B, with pre/post md5 and op gates, and
  require a material reduction in the Phase76 `gdn_core` bucket without
  regressing serving throughput or canonical md5.

### Phase75: Post-PoC GDN/VLLM Audit

- Date: 2026-07-01.
- Artifact: no new benchmark artifact.
- Source baseline: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Result type: subagent codebase audit and gate-setting only; no source change.
- Inputs: Phase74 artifact
  `/home/mudler/bench/phase74_gdn_blocked_solve_poc/20260701_143711`,
  llama.cpp GDN implementation, vLLM FLA/GDN implementation, and parity docs.

Findings:

- llama.cpp already has the M5 tensor-core GDN path default-on under paged KV.
  It includes `KK/QK` mma, `KS/QS` 3xtf32 mma, `P*U` mma, explicit
  `T=A^-1`, `U=T*RHS`, and state carry `Kc^T*DU`.
- The current backend path is fixed at `C=16` for GB10 shared-memory limits.
  The remaining C=64/register-state class is not a shortcut patch.
- Phase74 tested a C=64 shared-memory explicit inverse-plus-apply scaffold and
  failed its source-work gate: inverse/direct speed was `0.5941x` weak decay
  and `0.5927x` mixed decay.
- vLLM has a structurally different one-token recurrent decode kernel that
  updates state directly without chunk inverse, and a packed decode path that
  avoids Q/K/V materialization copies. This is not currently source-funded in
  llama.cpp because prior parity profiles showed llama.cpp GDN decode faster
  than vLLM and decode serving dominated by host/MoE synchronization.
- vLLM's CuTeDSL GDN prefill path uses SM10x/CUDA-13 Blackwell features
  including TMA/tcgen05/CUTLASS DSL. Treat it as datacenter-Blackwell reference
  evidence unless GB10 support is proven in the local toolchain.

Decision:

- Do not start GB10 GDN backend source work after Phase74/75.
- Do not start a packed/recurrent GDN decode PoC unless a fresh same-session
  profile shows GDN decode or Q/K/V materialization back on the critical path.
- Phase75 acceptance gate for the next real parity attempt is a datacenter
  Blackwell serving rerun with the Phase72 shape:
  `NPL=8 32 128`, `PTOK=128`, `GEN=64`, `PARALLEL=128`, production defaults.
- The rerun is valid only if `hardware.txt` records
  `hardware_class=datacenter_blackwell`, pre/post md5 gates are green
  (`8cb0ce23777bf55f92f63d0292c756b0`,
  `5951a5b4d624ce891e22ab5fca9bc439`), `MUL_MAT 1146/1146` and
  `MUL_MAT_ID 806/806` are green, and decode profiles include
  `nsys --cuda-graph-trace=node`.
- If datacenter Blackwell materially lifts llama/vLLM decode ratios above the
  GB10 Phase72 record (`0.7561`, `0.7158`, `0.6935`), continue parity work on
  that surface. If not, record the residual gap as engine/kernel architecture
  rather than GB10 memory bandwidth and keep GB10 GDN stopped.

### Phase74: GDN Blocked-Solve PoC Gate

- Date: 2026-07-01.
- Plan:
  `docs/superpowers/plans/2026-07-01-gdn-blocked-solve-poc-phase74.md`.
- Artifact:
  `/home/mudler/bench/phase74_gdn_blocked_solve_poc/20260701_143711`.
- Source baseline: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Result type: standalone CUDA microbenchmark only; no llama.cpp source change.
- Toolchain: CUDA `13.0.88`, `nvcc -O3 -arch=sm_121a`.
- Hardware: NVIDIA GB10, `cc=12.1`, `48` SMs, `99 KB` dynamic shared memory.
- Shape: `C=64`, `DK=128`, `DV=128`, `chunks=4096`, `iters=1000`.
- Shared memory: direct solve/apply `81920` bytes; inverse-plus-apply
  `98304` bytes.

Result:

| case | direct ms | inverse+apply ms | inverse/direct speed | direct NMSE | inverse NMSE | direct max abs | inverse max abs | max lower row sum |
|------|----------:|-----------------:|---------------------:|------------:|-------------:|---------------:|----------------:|------------------:|
| weak decay | `3.263936` | `5.493515` | `0.5941x` | `2.081e-14` | `2.755e-15` | `8.890e-07` | `2.415e-07` | `4.072` |
| mixed decay | `3.275959` | `5.527584` | `0.5927x` | `1.981e-14` | `7.541e-16` | `8.115e-07` | `7.888e-08` | `1.635` |

Decision:

- Reject this explicit inverse-plus-apply shape as a backend source candidate on
  GB10. It is numerically clean but materially slower than direct solve/apply.
- Do not touch `ggml/src/ggml-cuda/gated_delta_net.cu` for the larger C=64 path
  based on this attempt.
- A future GDN source-work gate would need a substantially different
  tensor-core blocked solve/register-state design, not this shared-memory
  inverse scaffold.

### Phase73: Datacenter Blackwell Rerun Readiness

- Date: 2026-07-01.
- Plan:
  `docs/superpowers/plans/2026-07-01-datacenter-blackwell-rerun-readiness-phase73.md`.
- Artifact: no new benchmark artifact.
- Source baseline: `14fd69f1e feat(cuda): gate BF16 cuBLAS F32 output`.
- Result type: harness/spec audit only.

Evidence:

- Phase72 is the current GB10 serving baseline. Default llama decode/vLLM
  ratios remain `0.7561`, `0.7158`, and `0.6935` at `n=8/32/128`.
- Grouped-MMQ/W4A16: Phase61 direct activation was the last structurally
  distinct W4A16 shortcut; it failed its keep gate and stayed far behind
  default FP4-MMQ. Phase66 quantize plus gather was only `5.10%`, below the
  source-funding threshold.
- GDN: Phase71 kept shipped M5 as default. The remaining GDN gap is a larger
  FLA/CuteDSL-class C=64 blocked-solve/register-state implementation, not
  another C32/QS/global-Ai/local reorder.
- Harness: `paged-current-serving-snapshot.sh` already records
  `hardware_class=datacenter_blackwell` for B200/B100/GB200, supports
  `DRY_RUN=1`, `SERVED_MODEL_NAME`, and vLLM deployment overrides.

Decision:

- Do not start more GB10 grouped-MMQ/W4A16 source work.
- Do not start GDN backend source work until a standalone C=64 blocked-solve
  PoC records timing, numerical error, and resource estimates.
- The next parity run should be on datacenter Blackwell hardware with the
  existing same-session serving harness plus graph-node decode profiles.
- No parity claim is made by this phase.

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
