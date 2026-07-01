# GDN M5 State-Boundary Design

## Context

Phase 10 tested a default-off C32 slabbed M5 path for
`ggml/src/ggml-cuda/gated_delta_net.cu`. It was correctness-clean only after
zeroing staged tail rows, then failed the performance gate:

| Model | PP | M5 S_PP t/s | C32 slab S_PP t/s |
|-------|----|-------------|-------------------|
| MoE | 2048 | 2430.32 | 2054.86 |
| Dense | 2048 | 1019.25 | 903.73 |

The likely root cause is duplicated A/T work per value slab. vLLM/FLA computes
the per-chunk triangular object once and reuses it through the WY transform; the
two-slab M5 shortcut could not do that without a larger scratch/precompute
design.

## Design Choice

Phase 11 stays at the shipped C=16 M5 geometry and tests a smaller C=16
state-boundary variant before reopening chunk-size changes. The candidate
targets the two tensor-core state-boundary products that both multiply a chunk
matrix by the same pre-update state `Sd`:

- `KS = Kc * S0`, currently used to form `Ud`.
- `QS = Qc * S0`, currently deposited later as the cross-chunk output term.

The first implementation should be default-off and selected by an explicit env
var such as `GDN_M5_QS_EARLY=1`. It should not change the default `GDN_TC=5`
path until it clears correctness and performance gates.

## Candidate Shape

The low-conflict version moves the QS state-boundary pass earlier in the C=16
M5 chunk loop and stores `gamma_t * QS[t][j]` in `attn_base` before the solve.
The later output section then reuses the predeposited cross-chunk term exactly
as it does today.

This does not yet fuse K and Q in a single MMA instruction. It tests whether
moving QS earlier and tightening the state-boundary scheduling helps without
new global scratch, changed state ownership, or C32 slab duplication. If it is
flat, Phase 11 should be rejected quickly and the next GDN work should be a
larger shared-A/Ai design rather than more local scheduling.

## Non-Goals

- Do not reintroduce C32 slabs.
- Do not add global A/Ai scratch in this phase.
- Do not import vLLM CuteDSL/TMA kernels.
- Do not route decode into chunked GDN; keep `GDN_CHUNK_MIN > 1`.
- Do not change default inference behavior unless gates prove a win.

## Gates

Correctness:

- Build `test-backend-ops` and `llama-completion` on DGX.
- Run default and forced-candidate `GATED_DELTA_NET` CUDA0 gates.
- Run canonical MoE and dense greedy md5 gates:
  - MoE: `8cb0ce23777bf55f92f63d0292c756b0`.
  - Dense: `5951a5b4d624ce891e22ab5fca9bc439`.
- If md5 changes, stop and run the existing KL gate before any performance
  claim.

Performance:

- Compare against current M5 with `GDN_TC=5 GDN_CHUNK_MIN=64`.
- Run MoE and dense `llama-batched-bench` at `npp=512,2048`, `ntg=4`,
  `npl=32`.
- Reject if either model regresses outside noise or if the GDN bucket does not
  improve in a profile-gated follow-up.

## Decision Rule

Accept only if the candidate is md5/KL-safe and improves end-to-end S_PP. If it
is flat or slower, record it as rejected and move to a larger shared-A/Ai
blocked-solve design, likely requiring a separate scratch/precompute phase.
