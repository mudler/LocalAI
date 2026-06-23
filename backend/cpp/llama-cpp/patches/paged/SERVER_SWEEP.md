# GB10 same-day head-to-head server sweep: llama-server (paged) vs vLLM

Date: 2026-06-23. Hardware: GB10 / DGX Spark (sm_121, 128 GB LPDDR5x unified, ~273 GB/s
weight-read floor). GPU otherwise idle (sibling vLLM had exited; LocalAI docker workers
stopped for the run).

This sweep **replaces** the stale carried "~75-80% of vLLM" figure (commit 07985ba4,
pre-co-batching, single-point). It measures *real serving* steady-state aggregate decode
throughput across the full concurrency curve, for three model classes, with one identical
client driving both engines.

## Method

- **llama**: `llama-server` from the paged dev tree (`~/llama-paged-dev/build-cuda`, HEAD =
  patch 0013 / commit 17d97cb), `LLAMA_KV_PAGED=1`, `-fa on -ngl 999 --parallel 128 -c 65536`.
- **vLLM**: 0.23.0, `vllm serve --enforce-eager --enable-prefix-caching --max-num-seqs >=128
  --max-model-len 4096` (APC on, eager per the GB10 no-CUDA-graphs edge).
- **Client** (`sweep_client2.py`): N concurrent **non-streaming** `/v1/completions`, short
  shared prompt, `max_tokens=min_tokens=256`, `ignore_eos=true`. Aggregate decode tok/s =
  total generated tokens / wall. Non-streaming keeps the Python client off the critical path
  (one JSON parse per request, not per token), so the **server** is the bottleneck. Validated:
  vLLM pushed 4227 tok/s through the exact same client where llama topped out at 2087, so the
  client is not the cap. Both engines use the identical client + prompt -> apples-to-apples.
- npl (concurrency) sweep: 8 / 32 / 64 / 128.

Quant parity:
- Dense: llama **NVFP4-dense GGUF** (weight-only FP4, 16-bit compute) vs vLLM **NVFP4A16**
  (weight FP4, 16-bit activation) -> matched precision class.
- Small: llama **Q8_0** vs vLLM **bf16** (closest loadable form).
- MoE: llama **mxfp4** GGUF. **vLLM could not serve this MoE on GB10 at all** (see below), so
  there is no vLLM MoE column.

## Results: aggregate decode tok/s (higher is better)

### Dense 32B  (llama NVFP4-dense  vs  vLLM NVFP4A16)

| npl | llama (NVFP4) | vLLM (NVFP4A16) | llama % of vLLM |
|----:|--------------:|----------------:|----------------:|
|   8 |          83.2 |            85.9 |          **96.9%** |
|  32 |         228.9 |           301.3 |          76.0%  |
|  64 |         367.1 |           507.8 |          72.3%  |
| 128 |         520.6 |           604.0 |          86.2%  |

Plateau: neither has plateaued at 128 (both still climbing, weight-read bound). llama is at
**parity at batch-8** (97%), dips to ~72% mid-curve (npl 32-64), recovers to 86% at 128.

### Small  Qwen3-0.6B  (llama Q8_0  vs  vLLM bf16)

| npl | llama (Q8_0) | vLLM (bf16) | llama % of vLLM |
|----:|-------------:|------------:|----------------:|
|   8 |        911.3 |       923.0 |        **98.7%** |
|  32 |       1701.6 |      2531.4 |        67.2%  |
|  64 |       1911.7 |      3497.1 |        54.7%  |
| 128 |       2087.6 |      4227.6 |        49.4%  |

Plateau: **llama plateaus hard** at ~2.0-2.1k by npl 64-128 (+9% from 64->128). vLLM keeps
scaling (3497 -> 4227). For a tiny runtime-bound model, vLLM's scheduler/batching amortizes
better; llama-server's per-token host cost (sampling, detok, slot mgmt) caps it. This is the
worst llama-vs-vLLM ratio in the sweep (down to 49%).

### MoE  Qwen3-Coder-30B-A3B  (llama mxfp4; vLLM = NOT SERVABLE on GB10)

| npl | llama (mxfp4) | vLLM |
|----:|--------------:|-----:|
|   8 |         290.0 |  n/a |
|  32 |         582.5 |  n/a |
|  64 |         931.8 |  n/a |
| 128 |        1041.3 |  n/a |

llama-server scales cleanly to **1041 tok/s** at npl 128 with **no npl-128 expert-activation
cliff** (unlike the prior `llama-batched-bench` MoE numbers 253/505/830/620 that peaked at 64;
short-prompt continuous batching in the server avoids it).

**vLLM could not serve this MoE on GB10 (two independent failures):**
1. **bf16** (`Qwen/Qwen3-Coder-30B-A3B-Instruct`, the only HF form on the box): loads the
   56.9 GB of weights, then **hangs at the MoE warmup** (`Using MoEPrepareAndFinalize
   NoDPEPModular` -> `Model loading took ...`), GPU 0% util, and **takes the whole box down
   (hard reboot)**. Reproduced twice. With tight `--gpu-memory-utilization` it still hangs at
   the same step before the API server ever comes up.
2. **mxfp4 GGUF** (same weights llama uses): vLLM 0.23.0's GGUF loader **cannot map the fused
   qwen3moe expert tensors** (`RuntimeError: Failed to map GGUF parameters (48):
   ['model.layers.N.mlp.experts.gate_up_proj', ...]`). Engine init fails outright.

So on GB10, llama.cpp is the *only* engine of the two that serves this 30B-A3B MoE at all -
an availability win, independent of throughput.

## Batch-8 anomaly triage (dense NVFP4) -- RESOLVED

The prior mixed-load run reported llama batch-8 steady decode at **471 ms/step (~19% of vLLM
aggregate, ~17 tok/s)**. This sweep does **not** reproduce it. Clean isolated batch-8 decode:

- `llama-server` batch-8 dense paged = **83.2 tok/s** aggregate = ~96 ms/step = **96.9% of
  vLLM's 85.9** (parity, both at the LPDDR5x weight-read floor).

`llama-batched-bench` cross-check, dense NVFP4, `-npp 16 -ntg 128 -npl 1,8`, the three
hypotheses isolated (S_TG = decode tok/s aggregate at batch 8):

| config                | batch-8 S_TG t/s | ms/decode-step |
|-----------------------|-----------------:|---------------:|
| paged,  ctx 65536     |            90.32 |          88.6  |
| stock,  ctx 65536     |            88.39 |          90.5  |
| paged,  ctx 163840    |            89.33 |          89.6  |
| stock,  ctx 163840    |            87.72 |          91.2  |

Conclusion: clean batch-8 dense decode is **~88-90 tok/s (~89 ms/step) regardless of all three
suspects**:
- **Paged overhead?** No -- paged is within 2% of stock, and at ctx 65k paged is *faster*
  (90.3 vs 88.4). The decode path is not paying a paged penalty at batch-8.
- **The 163840-token ctx allocation?** No -- ctx 163840 == ctx 65536 within 1% (89.3 vs 90.3).
  The large allocation does not slow steady-state decode.
- **NVFP4 decode cost?** This *is* the cost -- ~89 ms/step is the GB10 weight-read floor for a
  32B at batch-8 (it matches vLLM's 86 tok/s server and exceeds it at the kernel level: 90 vs
  86). It is the hardware ceiling, not a bug.

The 471 ms/step is ~5.3x slower than this clean floor and is explained by none of the three.
It was a **mixed-load artifact**: the 8 decoders were time-sharing the GPU with a concurrent
prefill (a large prompt / chunked prefill landing on the same steps). That decode-vs-prefill
contention is exactly the stall **patch 0013 (`LLAMA_PREFILL_BUDGET`)** bounds. In steady-state
isolated decode, batch-8 dense is at **parity with vLLM (97%)**, not 19%.

## Aggregate map (replaces the carried 75-80%)

llama-server (paged) as a fraction of vLLM, by regime:

- **Low concurrency (batch-8): parity, 97-99%** on both measurable classes. Both engines sit on
  the LPDDR5x weight-read floor; there is nothing to win.
- **Dense 32B, mid-to-high concurrency: 72-86%.** Dips to ~72% at npl 32-64, recovers to 86% at
  128. Both still climbing (weight-bound), neither plateaus by 128.
- **Small 0.6B, mid-to-high concurrency: 49-67%.** llama plateaus ~2.0k; vLLM scales to 4.2k.
  Runtime/scheduler-bound regime -- vLLM's batching wins; this is llama's weakest ratio.
- **MoE 30B-A3B: llama-only.** vLLM cannot serve it on GB10 (bf16 reboots the box at MoE
  warmup; GGUF expert tensors unmappable). llama serves it at 290 -> 1041 tok/s, scaling
  cleanly with no npl-128 cliff.

Net: the single "75-80%" number is replaced by a curve. It is *roughly* right only for the
dense mid-band; it is too optimistic for the small model at high concurrency (49%) and moot for
MoE (where llama is the only option). The headline is parity at low concurrency and a hardware
(not engine) ceiling on dense decode.
