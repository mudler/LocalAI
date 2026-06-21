# Paged KV at high concurrency on a single GB10 - the datacenter-scale test

Closes the open question left by `PR22569_EVAL.md`: that eval could not test the
"paged KV unlocks thousands of sequences" thesis because **both** KV paths hit the
`LLAMA_MAX_SEQ=256` compile cap, and the 32B-dense model it used is compute-bound
(plateaus by npl=128 for an unrelated reason). This run removes both confounders:
**recompiled `LLAMA_MAX_SEQ=2048`** and used a **bandwidth-bound model (Qwen3-1.7B-Q8_0)**
where decode aggregate is free to keep climbing with concurrency.

Hardware: NVIDIA GB10 (sm_121, 119 GiB unified LPDDR5X, ~273 GB/s). Build:
`~/llama.cpp-pr22569` (PR #22569 paged path + the reshape fix), `LLAMA_MAX_SEQ=2048`,
sm_121 Release. Contiguous = `llama-batched-bench` (unified KV) `S_TG`. Paged =
`llama-paged -kvp --fit off` `aggregate tps`. `npp=16, ntg/n_predict=128, b=ub=2048,
-ngl 99`. Cold runs, 12 s cooldowns.

## TL;DR for the decision

**On a single GB10, paged KV does NOT deliver a throughput or concurrency win - the
aggregate-decode ceiling is set by the hardware, not the KV layout, and contiguous KV
already reaches it.** Measured across two model regimes and concurrency up to 2048
sequences:

- Aggregate decode **plateaus** once the GPU saturates - for both KV layouts:
  - 32B-dense (compute-bound): ~540 t/s, flat from npl=128 (prior eval).
  - 1.7B (bandwidth-bound): ~3,200-3,700 t/s, flat from npl=512 (this run).
- Paged and contiguous land at the **same ceiling**; PR #22569's paged op was 12-13%
  *slower* than the mature contiguous flash-attention path at equal concurrency on 32B.
- Pushing concurrency past the plateau is **actively harmful to UX**: per-sequence
  throughput collapses (23 -> 1.9 tok/s) and TTFT explodes (0.6 s -> 4.3 s avg, **64 s
  max**) while aggregate stays flat.

**vLLM's ~24k aggregate headline is unreachable on a single GB10 with these models
regardless of KV layout** - it needs aggregate memory bandwidth / compute that one GB10
does not have (i.e. many GPUs). Paged KV is a **memory-capacity / anti-fragmentation /
prefix-sharing** feature, not a single-node throughput-ceiling feature. The static
single-model benchmark deliberately does not create the memory-pressure regime where
paging pays off, which is exactly why no win appears.

## The numbers

### Aggregate decode vs concurrency, Qwen3-1.7B-Q8_0 (bandwidth-bound), `LLAMA_MAX_SEQ=2048`

| npl | contiguous `S_TG` (t/s) | paged `aggregate tps` (t/s) | paged per-seq tps | paged TTFT avg / max |
|----:|------------------------:|----------------------------:|------------------:|---------------------:|
| 128 | 2,643 | 2,887 | 23-25 | - |
| 256 | 2,925 | - | - | - |
| 512 | 3,215 | 3,637 | 7.2-7.8 | 0.57 s / 0.90 s |
| 1024 | 3,118 | 3,695 | 3.7-4.2 | 1.17 s / 2.37 s |
| 2048 | (not run) | 3,608 | 1.9-14.6 | 4.28 s / **63.8 s** |

Both paths flatten by npl~512. 8x more concurrency (128->1024) buys contiguous only
**+18%** and paged **+28%**, then both stop. (The two tools meter slightly differently -
`llama-paged` aggregate vs `batched-bench` decode-only `S_TG` - so the small paged-vs-
contiguous offset is not a real paged advantage; the prior apples-to-apples 32B eval had
paged 12-13% *behind*.)

### Why it plateaus (the hardware ceiling, not the KV layout)

Decode is memory-bandwidth-bound: each step reads the model weights once and shares that
read across the whole batch. Once concurrency is high enough that the shared weight-read
is amortized, the per-step cost is dominated by KV reads + attention + host work, none of
which paging makes cheaper. The GB10's ~273 GB/s sets the floor; at the plateau the GPU
is ~saturated. Adding sequences past that point cannot raise aggregate - it only divides
the same throughput across more users (per-seq tps falls, TTFT rises). The 32B-dense case
plateaus even earlier (npl=128) because it saturates on **compute** (weight matmuls), not
bandwidth - the kernel decomposition is in `VLLM_DECOMPOSITION.md`.

## What paged KV is actually for (the honest, deliverable value)

Paging never helps a static, uniform-length, single-model benchmark on a GPU with memory
to spare - there is no fragmentation and no over-reservation to reclaim. Its real wins,
which require the regime this hardware+benchmark does not exercise, are:

1. **Concurrent-tenant capacity under memory pressure.** Block KV fits more *diverse*
   in-flight sequences (variable, dynamically arriving/leaving contexts) without the
   contiguous path's per-slot reservation/fragmentation. Pays off when KV memory, not
   compute/bandwidth, is the binding constraint - i.e. at multi-GPU datacenter scale or
   with very long/variable contexts.
2. **Cross-request prefix sharing.** A chained-hash block cache shares identical system
   prompts / RAG preambles across requests (vLLM's `block_pool.py` + block-hash map). A
   real token-budget win for shared-prefix workloads; PR #22569 defers this to a
   non-existent Phase 2 (our from-scratch P0 has the machinery).

These are measured as **max concurrent distinct tenants** and **KV memory saved**, not as
aggregate tok/s on one model. They do not move the single-GB10 throughput ceiling.

## Recommendation

- **Do not pitch paged KV as a single-GB10 throughput lever** - it is measured flat to
  the contiguous ceiling (and PR #22569 is slower). Doing so would not survive a
  benchmark.
- **The single-GB10 throughput story is already strong without paging:** llama.cpp is
  ahead of vLLM single-stream (MXFP4 1153 > 800) and at ~70-81% of vLLM aggregate at
  npl<=128 with a near-identical batching multiplier (`VLLM_DECOMPOSITION.md`). Ship the
  MXFP4/NVFP4-dense prefill win (`NVFP4_TEST.md`) - that is the cheap, real, defensible
  Blackwell number.
- **If datacenter-scale (thousands of concurrent tenants) is the genuine target,** the
  lever is **multiple GPUs** plus paged KV's **capacity + prefix-sharing** features -
  framed and measured as concurrent-tenant capacity and KV memory saved, on a
  variable-context / shared-prefix workload. A single GB10 cannot produce the ~24k
  aggregate regardless of KV layout; that is a fleet-level result.

## Reproduction (DGX, `~/llama.cpp-pr22569`, `LLAMA_MAX_SEQ=2048`)

```sh
M=~/bench/draft17/Qwen3-1.7B-Q8_0.gguf
# contiguous
for NPL in 128 256 512 1024; do
  ./build/bin/llama-batched-bench -m $M -npp 16 -ntg 128 -npl $NPL -ngl 99 \
    -b 2048 -ub 2048 -fa on -c $((NPL*160)); done
# paged
for NPL in 512 1024 2048; do
  ./build/bin/llama-paged -m $M -kvp --fit off -ngpub 32768 -ncpub 128 \
    -np $NPL -ns $NPL -n 128 -b 2048 -ub 2048 -ngl 99; done
```
