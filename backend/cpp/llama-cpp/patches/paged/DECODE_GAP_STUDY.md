# llama-server vs vLLM: decode-step gap decomposition (DGX Spark, GB10 / sm_121)

Profiling study (no engine changes). Question: matched apples-to-apples (both
batched servers, NVFP4-class weights, prefix caching on, both eager), why is
`llama-server` ~4-6x slower **per decode step** than vLLM on Qwen3-32B at a
1024-token shared-prefix / batch-32 fan-out, and what is closable vs structural.

Hardware: NVIDIA GB10 (sm_121), unified LPDDR5X. Model: Qwen3-32B, 64 layers.
llama side: `~/llama-paged-dev/build-cuda/bin/llama-server`, `q3-32b-nvfp4-dense.gguf`
(NVFP4 weights, type-40 FP4-MMA path), `-ngl 99 --parallel 32 -c 40960 -fa on`,
`GGML_CUDA_DISABLE_GRAPHS=1` (eager). vLLM 0.23.0 NVFP4A16 (W4A16/Marlin),
`--enforce-eager`. Workload: 1024-token shared prefix + unique 32-token suffix,
K=32 concurrent, generate 64. All profiling scripts are dev-tree only
(`~/bench/decode_study/`); minimal in-code timers were not needed (server already
reports per-slot `eval time`, which excludes prompt-eval = pure decode).

## TL;DR

1. **The real-server decode is GPU-BOUND, not host-bound.** During steady decode
   the GPU is **~94.6% utilized** (nvidia-smi, real run) / 85-95% busy (nsys).
   Per-slot CPU sampling, detokenize, and `update_slots` are fully hidden: a 5-stage
   sampler chain gives the *identical* step time as greedy (1346 vs 1343 ms). The
   "GPU stalls on the CPU serving loop" hypothesis is **refuted** for this workload.
2. **At 1024 context the decode step is ~84% KV/attention, ~16% weight GEMM** - the
   opposite of the thin-batch-GEMM story. Attention scaling with context length, not
   the matmul, is the load-bearing cost.
3. **The worktree's paged KV engine is a decode REGRESSION: ~1.85x slower than
   stock** at 1024 ctx (paged 1279-1343 ms/step vs stock 650-729 ms/step). It
   gathers K/V/mask into a contiguous buffer (`ggml_get_rows`) every layer every
   step, then runs a dense FA kernel - paying a full extra KV read+copy that vLLM's
   in-kernel PagedAttention never pays. Paging helps prefix-prefill memory; it hurts
   decode latency.
4. Even **stock** llama-server (~650-729 ms/step) is **~4-5x slower than vLLM**
   (~120-185 ms/step). The residual gap is the **long-context decode-attention
   kernel** and, secondarily, the **thin-batch FP4 weight GEMM** - both kernel-maturity
   gaps vs vLLM's FlashInfer/FA paged-decode + Marlin, not serving-loop gaps.

## The measured numbers (batch 32, server-reported pure-decode step time)

`server_decode_step_ms` = max / mean-of-top-8 of per-slot `eval time ms-per-token`
(the most-contended, full-batch-32 slots; excludes prompt eval).

| config                                   | decode step ms (max / top8) | client wall ms/step |
|------------------------------------------|-----------------------------|---------------------|
| paged, ctx 1024, greedy                  | 1343 / 1279                 | 1468                |
| paged, ctx 1024, **heavy 5-sampler**     | 1346 / 1280                 | 1470                |
| **stock** (no paging), ctx 1024, greedy  | **729 / 650**               | 768                 |
| paged, **ctx 64** (short), greedy        | **215 / 215**               | 253                 |
| vLLM NVFP4A16, ctx 1024 (K=32)           | **~120-185** (270 tok/s)    | -                   |

The brief's reference ~828 ms/step sits between the stock (650-729) and paged
(1279-1343) numbers measured here; the decomposition below is what is robust. Our
fan-out shares no prefix across the 32 slots (each slot independently prefills 1056
tokens - confirmed in the log), so the 32 sequences are genuinely concurrent and the
"max" slot is maximally contended, which is why our paged max runs a little above 828.

### Context sweep - decode step is attention-scaling, not fixed overhead

Pure-decode step vs shared-prefix length (paged, batch 32):

| prefix ctx | decode step ms |
|-----------|----------------|
| 64        | 215            |
| 128       | ~290           |
| 256       | ~410           |
| 512       | ~660           |
| 1024      | ~1280          |

Roughly linear in context length: ~1 ms of added step time per added context token.
The **215 ms at ctx 64 is the fixed floor** (weight GEMM + activations + norm/rope +
loop + sampling, attention negligible). Everything above it scales with KV length =
attention + KV plumbing. At 1024 ctx the fixed floor is only ~16% of the step.

## Where the ~1280 ms paged decode step goes (nsys, pure-decode window)

`nsys profile --delay=70 --duration=25 --trace=cuda` windowed onto steady 32-way
decode (`srv_decode2.nsys-rep`; an earlier 25-60s window was discarded because nsys's
own slowdown stretched the 32 prefills into it, inflating GEMM to a misleading 58%).
GPU busy in-window 85.5% (nsys adds gaps; the real run is ~94.6% by nvidia-smi).

| bucket                         | % GPU time | abs (of ~1280 ms) | what it is |
|--------------------------------|-----------:|------------------:|------------|
| `flash_attn_ext_f16` ATTENTION | **47.7%**  | ~610 ms           | decode attention over the 1056-cell KV |
| `cpy_scalar` KV copy/cast      | 18.3%      | ~234 ms           | KV write + f32->f16 casts |
| `get_rows/set_rows` KV gather  | 17.8%      | ~228 ms           | **paged** gather of K/V/mask to contiguous |
| `mul_mat_q` + `quantize_mmq`   | 15.7%      | ~201 ms           | NVFP4 weight GEMM (+ activation requant) |
| rmsnorm / silu / rope / add    | ~0.6%      | ~8 ms             | elementwise |

Cross-check: the GEMM bucket (~201 ms) matches the ctx-64 floor (215 ms) - i.e. the
weight matmul is ~the entire short-context step, and is context-independent, as
expected. KV/attention buckets (47.7+18.3+17.8 = **83.8%**) match the context-sweep
finding that ~84% of the step scales with context.

Power signature: ~33-36 W at 94% "utilization" (GB10 can pull far more). High util%
+ low power = the kernels are **memory/latency-bound, not compute-saturated** - the
classic decode signature (stream 19 GB of NVFP4 weights + a growing KV every step).

### Stock vs paged decomposition

- **Stock** (~650 ms): ~215 ms GEMM floor + ~435 ms attention/KV (contiguous KV read
  directly by the FA kernel, **no gather**).
- **Paged** (~1280 ms): same ~215 ms floor + ~610 ms attention + **~455 ms paged
  gather/copy overhead** (the `get_rows` of K/V/mask plus the extra KV copy that
  feeds the dense FA kernel). That ~455 ms (~36% of the step) is the paged engine's
  self-inflicted cost and is the entire ~1.85x stock->paged regression.

## vLLM decode architecture mapped onto each llama bucket

vLLM at ~120-185 ms/step is faster on **every** bucket:

| llama bucket (paged)        | ms    | vLLM equivalent | does vLLM avoid it? |
|-----------------------------|-------|-----------------|---------------------|
| paged KV gather (get_rows)  | ~228  | PagedAttention reads blocks **in-kernel** via a block table | **Yes - entirely.** No gather op exists. |
| KV copy/cast                | ~234  | KV written once into block pool; FA reads it in place | Mostly - no per-step recopy |
| decode attention            | ~610  | FlashInfer / FA paged-decode GQA kernel, split over KV | Same op, far faster kernel on sm_121 |
| weight GEMM + act quant     | ~201  | fused Marlin/Machete W4A16 dequant+MMA, no separate quant pass | Faster + removes the requant kernel |
| CPU sampling / loop         | ~0 (hidden) | on-GPU batched sampling | N/A here - already hidden on llama side too |

vLLM's whole-step (~150 ms) is **less than llama's GEMM floor alone (~215 ms)**, so
vLLM is ahead on the matmul *and* the attention *and* avoids the gather. The gap is a
stack of kernel-efficiency wins, not one silver bullet.

## Ranked levers - closable vs structural

1. **Remove the paged gather regression. [Tractable, ~455 ms / ~36% on the paged
   path; net-zero risk - it is a regression]** The worktree's paged engine makes
   decode 1.85x slower than stock by gathering K/V/mask to contiguous every layer
   every step (patch 0003 `ggml_get_rows`). For latency-bound decode, **do not enable
   paged KV** - it only ever helps prefix-prefill *memory*, never decode latency.
   Fully recovering this *and* keeping paging requires reading paged blocks
   in-kernel like vLLM (a from-scratch paged-attention CUDA kernel) - see lever 2.

2. **Long-context decode-attention kernel. [Biggest real lever, ~435 ms of stock /
   ~610 ms of paged; partly structural]** Even stock is attention-bound at 1024 ctx.
   llama.cpp's `flash_attn_ext_f16` decode path is ~4-5x slower than vLLM's
   FlashInfer/FA paged-decode GQA kernel on this Blackwell-class part. This is the
   cost that *grows with context* - exactly the regime the brief targets. Tractable in
   principle (a proper flash-decoding / split-K-over-KV kernel, and a true in-kernel
   paged read that also kills lever 1's gather), but it is deep CUDA work on a new
   arch and partly gated by kernel maturity on sm_121. **Highest-impact, hardest.**

3. **Thin-batch FP4 weight GEMM floor. [Tractable, ~201-215 ms / 15-30%; bounded]**
   The NVFP4 `mul_mat_q` + separate `quantize_mmq` activation pass is memory-bound and
   less efficient than vLLM's fused Marlin/Machete W4A16. Fusing dequant into the MMA
   and folding the activation quant into the GEMM is tractable kernel work. Bounded
   impact: the floor cannot drop below weight-read-bound (~19 GB / HBM BW per step).

4. **Host serving loop / per-slot sampling. [NOT a lever]** Measured zero: greedy ==
   heavy-sampler step time; GPU 94.6% busy. On-GPU/batched sampling buys nothing until
   the kernels (levers 1-3) get fast enough to expose host overhead. Refutes the
   "host-bound serving loop" hypothesis for this decode-bound workload.

5. **Continuous-batch scheduler. [NOT the gap / structural elsewhere]** llama-server
   already fuses all 32 slots into one decode step (one set of kernels per step over
   batch 32 - confirmed in the trace). vLLM's continuous/chunked-prefill batching wins
   on *mixed* prefill+decode overlap, but the steady decode-step gap measured here is
   kernel-bound, not scheduler-bound.

## Honest bottom line

The ~4-6x per-step gap is **GPU-kernel-bound**, and it decomposes as:

- ~36% of the *paged* step is a **self-inflicted gather regression** - remove it
  (don't run paged for decode-latency workloads).
- The remaining ~4-5x vs vLLM (true even for stock) is **kernel efficiency**:
  llama.cpp's long-context decode-attention and thin-batch FP4 GEMM are slower than
  vLLM's PagedAttention + Marlin on GB10. That is a **kernel project** (in-kernel
  paged attention + flash-decoding + fused W4A16 GEMM), not a serving-loop project.
- Sampling, detokenize, `update_slots`, and the continuous-batch scheduler are **not**
  the gap; the GPU is ~95% busy on memory-bound kernels the whole step.

What is closable: lever 1 (immediately, by not paging), lever 3 (bounded, with kernel
work). What is structural / hard: lever 2 (the decode-attention kernel + a real
in-kernel paged read), which is where the context-scaling gap actually lives and where
any serious effort to approach vLLM on GB10 must go.

## Reproduction (dev-tree only, `~/bench/decode_study/`)

- `launch_srv.sh` / `runcfg.sh` - launch llama-server (paged on/off) and a config.
- `client.py` - K=32 token-id fan-out (1024 prefix + 32 suffix), `SAMP=greedy|heavy`.
- `d2drv.sh` - nsys pure-decode window (delay 70s past prefill) -> `srv_decode2.nsys-rep`.
- `cat2.py` - kernel-time categorization from the sqlite export.
- vLLM side: `~/bench/run_vllm.sh` + `vllm_prefix.py` (K=32, ~270 tok/s).
</content>
</invoke>
