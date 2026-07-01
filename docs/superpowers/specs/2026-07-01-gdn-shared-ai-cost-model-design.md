# GDN Shared-A/Ai Cost Model Design

## Context

The last two GDN experiments closed the low-conflict shortcut space:

- Phase 10 C32 slab M5 was md5-clean after tail-row zeroing but slower because
  each value slab recomputed the per-chunk triangular work.
- Phase 11 QS-early M5 was md5-clean but still slower because moving `QS` did
  not remove a tensor-core pass.

The remaining algorithmic gap to vLLM/FLA is not another local reorder. vLLM
builds the per-chunk triangular object once, solves/inverts it once, and reuses
that result across the WY transform. llama.cpp's current C=16 M5 already
computes A/T once for the full value width inside one CTA. A wider chunk only
fits on GB10 if value columns are split into slabs, and slabs lose unless A/T
is shared across them.

## Current Geometry

For `S_v = 128` and f32 state:

| Shape | Dynamic smem |
|-------|--------------|
| C16 full value width | 93,376 B / 91.19 KiB |
| C32 full value width | 127,360 B / 124.38 KiB |
| C32 with `dv_tile=64` plus U staging | 94,592 B / 92.38 KiB |

GB10's available dynamic smem leaves enough room for C16 full-width and C32
half-width, but not for C32 full-width. That makes a shared-A/Ai design the only
plausible C32 path.

## Candidate Approaches

### A. Global A/Ai Scratch Precompute

Add a first kernel that computes `A` and `Ai` once per `(sequence, head, chunk)`
and materializes `Ai` in global scratch. A second kernel consumes `Ai` across
value slabs.

Pros:

- Directly targets the Phase 10 failure mode.
- Mirrors the portable part of vLLM/FLA's schedule.
- Keeps each value-slab CTA within the GB10 smem limit.

Cons:

- Adds at least one extra kernel boundary.
- Requires scratch allocation and lifetime management in ggml CUDA.
- Scratch is large at real batch sizes. At `npl=32`, `BT=32`, f32 Ai costs:
  - H=40, T=2048: 320 MiB.
  - H=48, T=2048: 384 MiB.
  - H=64, T=2048: 512 MiB.
- Needs careful profiling because global scratch traffic can erase the saved
  triangular recomputation.

### B. Shared A/Ai Inside One CTA With Reduced State Residency

Keep C32 in one CTA by moving some state or value scratch out of shared memory.

Pros:

- Avoids global Ai scratch and cross-kernel synchronization.
- Could keep the current single-kernel structure.

Cons:

- The f32 state alone is 64 KiB. Removing enough shared memory for C32 full
  width likely means reading state from global during MMA tiles or reducing
  state residency, which attacks the current M5 strength.
- Higher risk of lowering achieved bandwidth and breaking md5 via new ordering.

### C. Stay C16 and Stop GDN Kernel Work on GB10

Accept C16 M5 as the local GB10 ceiling and redirect parity work to another
bucket or different hardware.

Pros:

- Avoids high-risk scratch and synchronization work.
- Matches Phase 10/11 evidence that shortcuts are now exhausted.

Cons:

- Leaves the GDN prefill gap open.
- Does not move toward vLLM prefill parity on GB10.

## Recommended Phase 12

Run a cost-model and dry-design phase before any source patch. The phase should
produce a go/no-go decision for Approach A:

1. Extract actual GDN head counts and chunk counts for the MoE and dense GGUFs.
2. Compute scratch sizes for `BT=32` and `BT=64` at the benchmark shapes.
3. Estimate extra global traffic: Ai write + Ai read per value slab.
4. Compare that traffic against the triangular recomputation saved by sharing
   A/Ai across slabs.
5. Only if the model is plausible, write a Phase 13 implementation plan for a
   default-off global-scratch prototype.

## Decision Rule

Proceed to implementation only if the model shows a credible net win at
`npp=2048, npl=32` without unreasonable memory growth. If the estimated scratch
traffic or kernel-boundary overhead is close to the saved work, record a no-go
and stop GDN kernel work on GB10 rather than adding a large patch that is likely
to be rejected.
