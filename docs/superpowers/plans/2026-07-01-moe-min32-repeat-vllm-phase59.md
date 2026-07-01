# Phase 59: MoE Min32 Repeat and vLLM H2H

## Goal

Repeat the Phase58 MoE `LLAMA_TTFT_PREFILL_FIRST_MIN_WAITING=32` result in a
fresh DGX window, then compare against a matching vLLM `n=128`, `ptok=128`,
`gen=64` serving run.

## Patch Under Test

The temporary DGX patch stack was generated from the local llama.cpp fork
through:

- `8759213e3 feat(server): gate TTFT defer by prompt backlog`

The patch was applied to the clean DGX mirror for llama.cpp runs, then reverted
before the vLLM run.

## Verification

Pre and post llama gates stayed green:

| phase | MoE md5 | dense md5 | `MUL_MAT` | `MUL_MAT_ID` |
|-------|---------|-----------|-----------|--------------|
| pre | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |
| post llama | `8cb0ce23777bf55f92f63d0292c756b0` | `5951a5b4d624ce891e22ab5fca9bc439` | `1146/1146` | `806/806` |

## Results

Artifact:

- `/home/mudler/bench/phase59_moe_min32_repeat_vllm/20260701_123147`

MoE `n=128`, `ptok=128`, `gen=64`:

| engine / variant | agg t/s | decode agg t/s | prefill t/s | TTFT mean ms | TTFT max ms | wall s | deferred |
|------------------|---------|-----------------|-------------|--------------|-------------|--------|----------|
| llama default | `336.6` | `646.7` | `1525.1` | `7798.5` | `11666.8` | `24.334` | `0` |
| llama min32 | `336.9` | `632.0` | `1567.1` | `7167.8` | `11353.4` | `24.316` | `279` |
| vLLM | `601.3` | `938.8` | `3648.7` | `2968.1` | `4871.6` | `13.563` | n/a |

Min32 repeat delta versus llama default:

- Aggregate throughput: `+0.1%`
- Mean TTFT: `-8.1%`
- Max TTFT: `-2.7%`
- Wall time: `-0.1%`
- Prefill throughput: `+2.8%`
- Decode aggregate throughput: `-2.3%`

Llama min32 versus vLLM:

- Aggregate throughput ratio: `0.560`
- Mean TTFT: llama is `2.415x` slower
- Wall time: llama is `1.793x` slower
- Prefill throughput ratio: `0.430`
- Decode aggregate throughput ratio: `0.673`

## Decision

`LLAMA_TTFT_PREFILL_FIRST_MIN_WAITING=32` repeated as a real, inference-gated
llama.cpp scheduler QoS improvement for MoE `n=128`: it cuts mean TTFT without
moving aggregate throughput or wall time materially.

It is not a vLLM parity lever by itself. vLLM remains far ahead on the same
serving shape, especially prefill and TTFT. Keep the scheduler path opt-in and
treat it as user-visible latency tuning while parity work returns to the larger
prefill / MoE compute gap.

## Status

- Phase59 docs recorded.
- DGX lock released as `FREE phase59-cleanup`.
- No push performed.
- LocalAI `patches/paged/` not regenerated.
