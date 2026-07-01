# GDN Global-Ai Prototype Design

## Goal

Prototype the only remaining plausible C32 GDN prefill path on GB10: compute
the per-chunk triangular inverse once into global f32 Ai scratch, then reuse it
from two `dv_tile=64` value-slab CTAs.

## Scope

The prototype is default-off and intentionally narrow:

- `S_v=128`
- `BT=32`
- f32 Ai scratch
- two `dv_tile=64` value slabs
- non-KDA, final-state-only path matching the existing chunked M5 conditions
- no decode routing; `GDN_CHUNK_MIN` remains greater than 1

## Architecture

The prototype splits current M5 work into two CUDA stages:

1. `gdn_ai32_cuda`: one CTA per `(sequence, head, chunk)` computes the C32
   chunk-local triangular inverse `Ai = A^-1` and writes `[BT, BT]` f32 scratch.
2. `gdn_chunked_ai32_cuda`: one CTA per `(sequence, head, value slab)` loads Ai
   for each chunk and performs the value-dependent work for its 64 output
   columns.

This mirrors the portable scheduling idea from vLLM/FLA without importing
CuteDSL, TMA, or BF16 storage. It directly tests whether sharing A/Ai across
slabs can beat the duplicated work that rejected Phase 10.

## Scratch

Ai scratch is sized:

```text
n_seqs * H * ceil(n_tokens / 32) * 32 * 32 * sizeof(float)
```

At `npp=2048,npl=32`, this is:

- MoE H=32: 256 MiB.
- Dense H=48: 384 MiB.

Scratch allocation must use the existing ggml CUDA pool, be scoped to the op,
and be default-off behind an explicit env selector.

## Selector

Use:

```text
GDN_GLOBAL_AI32=1
```

The default path remains current C16 M5. The candidate only engages when:

- `S_v == 128`
- `n_tokens >= GDN_CHUNK_MIN`
- `!KDA && !keep_rs_t`
- `GDN_GLOBAL_AI32=1`

## Correctness

The first implementation uses f32 Ai to maximize chances of md5 stability. It
must pass:

- `test-backend-ops -b CUDA0 -o GATED_DELTA_NET`
- MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`
- Dense md5 `5951a5b4d624ce891e22ab5fca9bc439`

If md5 changes, the prototype must stop for KL before any performance claim.

## Performance

Compare same-session against current M5:

```text
LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GDN_TC=5 GDN_CHUNK_MIN=64
```

versus:

```text
LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GDN_TC=5 GDN_CHUNK_MIN=64 GDN_GLOBAL_AI32=1
```

Run MoE and dense at `npp=512,2048`, `ntg=4`, `npl=32`.

## Decision Rule

Accept only if the prototype is correctness-safe and improves end-to-end S_PP.
Reject if it is flat or slower. If rejected, save the diff under
`/home/mudler/bench/phase13_gdn_global_ai32/rejected/` and do not add a LocalAI
patch.
