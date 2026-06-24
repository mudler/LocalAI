# A.2 - CUDA-graphing the paged decode: measured lever + gap diagnosis

Phase 1 (measure, do not punt). DGX GB10 (sm_121), dev tree `~/llama-paged-dev`
HEAD 089f78d (patch 0017), `build-cuda`. Model `q36-27b-nvfp4.gguf` (dense),
harness `llama-batched-bench`, fusion held OFF (`LLAMA_FUSE_NVFP4_QUANT=0`) for a
clean stock-kernel baseline. `decode_agg` = the `S_TG t/s` column.

## TL;DR verdict

CUDA-graphing the paged decode is **NOT a real throughput lever** (ceiling well
under 1%). The steady decode step is **GPU-compute-bound: 99.4-99.5% GPU-busy**.
Total GPU idle is ~0.5-0.6% of the step, split into within-step launch gaps
(0.37%, the only thing CUDA graphs remove) and a between-step host-loop gap
(0.24%, one ~2 ms gap per step). Graphs already engage on the default paged
decode and do collapse the launch gaps (0.37% -> 0.11%), but the GPU stays
99.4-99.5% busy either way, so decode_agg is unchanged. The 2.6x gap to vLLM
(148 vs 391) lives in the per-step GPU **kernel work** (FP4 GEMM + attention at
batch 128), not in launch overhead or the host loop.

The premise that "the paged decode runs eager (graphs reused=0)" did not survive
measurement: at the benchmarked context the default paged decode captures and
replays graphs exactly like stock non-paged. Two measurement traps (below)
explain the earlier "reused=0 / gap-bound" reading.

## Method note: a graph-enable trap that was corrected

`GGML_CUDA_DISABLE_GRAPHS` is read with `getenv(...) != nullptr`
(`ggml/src/ggml-cuda/common.cuh:1234`), so setting it to an **empty** string
still disables graphs. A first 4-cell pass that used
`GGML_CUDA_DISABLE_GRAPHS=""` for the "graphs ON" cells therefore ran graphs OFF
in all four cells (an OFF-vs-OFF comparison). The numbers below ("v2") unset the
variable with `env -u` for the ON cells. The `-lv 99` probe is unaffected (it
never set the variable).

## Step 1 - the 4-cell decode_agg table (corrected, graphs genuinely enabled)

npp 128, ntg 128, npl 32 and 128, c 40960, b/ub 2048, fa on. `S_TG t/s`:

| cell             | npl 32  | npl 128 |
|------------------|---------|---------|
| stock_graphon    | 116.47  | 148.41  |
| stock_graphoff   | 115.17  | 148.21  |
| paged_graphon    | 116.21  | 148.60  |
| paged_graphoff   | 114.62  | 147.65  |

ON vs OFF (the graph win):

| config | npl 32 | npl 128 |
|--------|--------|---------|
| stock  | +1.13% | +0.13%  |
| paged  | +1.39% | +0.64%  |

- (a) Does STOCK get a graph win? Essentially no: +0.13% at npl 128, +1.13% at
  npl 32 (small-batch, where per-kernel launch overhead is relatively larger).
  All within run-to-run noise (~1% at npl 32, ~0.2% at npl 128).
- (b) Does PAGED get a graph win? Same picture: +0.64% / +1.39%. Paged is NOT
  eager at this config (see Step 2); it captures graphs like stock.
- (c) LEVER SIZE (proxy = stock graph win, now genuinely measured): +0.13% at
  npl 128, +1.1% at npl 32. Negligible vs the 2.6x (=+164%) gap to vLLM.

All four cells sit at ~148 (npl 128) / ~115 (npl 32) within ~1%. The ~148 wall is
shared by stock and paged; it is not paged-specific. Calibration cross-check
(paged ON, ntg 64): 147.64, matching the reference 148-149.

## Step 2 - why the "eager" premise is wrong, and what actually mutates

CUDA-graph state machine (`ggml_backend_cuda_graph_compute` in
`ggml/src/ggml-cuda/ggml-cuda.cu`): warmup completes after a step whose node
properties did not change vs the previous step; any later change logs
`CUDA graph warmup reset` and reverts to eager until stable again.
`ggml_cuda_graph_update_required` memcmps every node's full `ggml_tensor` plus
each src's `data` ptr / `ne` / `nb`.

`-lv 99` probe, short context (npp 64, ntg 32, ctx <= 96):
- stock:  `warmup complete` x2, `warmup reset` x0.
- paged:  `warmup complete` x2, `warmup reset` x0.
Both capture and then replay silently. The `CUDA Graph id N reused` line stays 0
for both because llama rebuilds the cgraph each ubatch (new `cgraph->uid`), so
the uid fast-path never fires; the graph is still replayed via the
`instance != nullptr` path, which logs nothing. **"reused=0" is a false negative,
not evidence of eager execution.** (Trap #1.)

Cadence probe (npp 200, ntg 320, npl 4, ctx 200->520, crosses the 256 and 512
token boundaries), counts over ~320 decode steps:

| path                          | complete | reset | interpretation                |
|-------------------------------|----------|-------|-------------------------------|
| paged in-kernel (default)     | 10       | 8     | resets only at 256-boundaries |
| paged gather (KV_PAGED_GATHER)| 0        | 0     | never captures -> pure eager  |
| stock non-paged               | 10       | 8     | identical 256-cadence         |

The 8 resets cluster at the two boundary crossings (timestamps ~9.9 s and ~34 s),
not per-step. The default paged decode is therefore captured for ~97% of steps,
re-warming only every ~256 tokens, with the **same cadence as stock**.

What mutates (the block-table / gather input):
- in-kernel decode (default): the block-table graph input
  `idx = ggml_new_tensor_2d(ctx0, I32, n_view, n_stream)` with
  `n_view = GGML_PAD(n_gather, 256)` (`src/paged-attn.cpp:199,213`). Its `ne[0]`
  steps 256 -> 512 -> 768 only when the context crosses a 256-token boundary. The
  kq_mask input (ne0 = n_kv, also padded to 256) steps in lockstep. So the
  property change is per-256-tokens, not per-step.
- gather fallback (`LLAMA_KV_PAGED_GATHER=1`, transposed-V, or prefill): the
  index input `idx = ggml_new_tensor_2d(ctx0, I32, n_gather, n_stream)`
  (`src/paged-attn.cpp:106`) has `ne[0] = n_gather` (UNPADDED), which grows every
  step (the unit's own comment, `src/paged-attn.cpp:28-30`: "n_gather grows every
  step"). That changes a node property every step, warmup never completes, and
  the path runs pure eager. This is the only "graphs reused=0" path, and it is
  not the default decode path.

`LLAMA_KV_PAGED_DEBUG` dump at ctx 201 (first 2 decode calls, identical across
the pair): `in-kernel decode n_stream=4 n_kv=256 n_gather=201` -> block-table
`ne[0] = GGML_PAD(201,256) = 256`, stable until n_gather crosses 256.

## Step 3 - where the step time goes (nsys), and a second trap

npl 128, ntg 24, ctx 56 (< 256, so the ON run stays captured after warmup).
Idle split by gap size: within-step launch gaps < 1 ms, between-step host gaps
>= 1 ms. Steady window = 40%-97% of the trace span (excludes model load / graph
reserve / prefill one-offs).

Trap #2: `nsys --trace=cuda` does NOT emit the kernels INSIDE a replayed CUDA
graph into `cuda_gpu_trace` by default. The graphs-ON trace had only 15,279 GPU
rows vs 84,946 for the identical OFF workload and reported a bogus 0.3% GPU-busy.
Re-profiling the ON case with `--cuda-graph-trace=node` restored all 84,946 rows
and 99.5% busy. **Any "decode is idle/gap-bound" reading taken from a graphs-ON
nsys trace without `--cuda-graph-trace=node` is artifactually idle-inflated** -
the likely source of the earlier "freed GPU time became idle gaps" conclusion.

Reliable steady-state numbers:

| trace                          | GPU rows | busy   | within-step idle | between-step idle | host gap/step |
|--------------------------------|----------|--------|------------------|-------------------|---------------|
| OFF (eager)                    | 84,946   | 99.4%  | 0.37%            | 0.24%             | ~2.0 ms       |
| ON (captured, node-trace)      | 84,946   | 99.5%  | 0.11%            | 0.38%             | ~1.9 ms       |

- CUDA graphs replay (cudaGraphLaunch=46) and collapse the launch path: ON has
  ~15k kernel launches/run vs OFF ~80k (cudaLaunchKernel 6,024 vs 31,764, plus
  ExC 9,049 vs 48,165). That cuts within-step launch idle from 0.37% to 0.11%.
- But the GPU is 99.4-99.5% busy in both, so decode_agg is unchanged.
- Between-step host idle is one ~2 ms gap per decode step (the 128-way sample +
  update_slots + batch build), 0.24-0.38% of the ~896 ms step.

Per-step decomposition at npl 128: ~896 ms/step, of which ~890 ms is GPU kernel
compute, ~2 ms host-loop gap, ~3 ms (eager) / ~1 ms (captured) launch gaps.

## The load-bearing question, answered

Within-step or between-step? **Neither is large.** The steady decode is 99.4%
GPU-busy; the entire idle budget is ~0.6% of the step. CUDA graphs already remove
the within-step launch fraction (0.37% -> 0.11%), and the between-step host gap is
~2 ms/step (0.24%). There is no large gap for a host-loop rewrite to reclaim
either; the host loop is currently **hidden under GPU compute** (the GPU stays
busy while the host syncs/schedules). It would only become a lever once the
kernels are fast enough to drop GPU-busy below the host time, i.e. it is a
second-order floor, not the present bottleneck.

## Verdict

1. CUDA-graphing the paged decode is not the lever. Graphs already engage on the
   default decode; capturing reduces within-step launch idle from 0.37% to 0.11%
   but leaves the GPU 99.4-99.5% busy, so decode_agg moves by ~0% (measured
   +0.1% to +0.6% at npl 128, +1.1% to +1.4% at npl 32, all within noise).
2. The between-step host loop is not the present lever either (0.24%, ~2 ms/step,
   hidden under GPU compute). It is the candidate floor only after the kernels
   speed up.
3. The decode is GPU-compute-bound at this NVFP4 fusion-OFF baseline. The 2.6x
   gap to vLLM is in the per-step GPU kernel work (FP4 GEMM + attention at batch
   128). That, not graphs and not the host loop, is the throughput lever.
4. Corrected premises: paged is not perpetually eager (it captures with a
   256-token reset cadence identical to stock); "graphs reused=0" was a uid
   fast-path false negative; and a graphs-ON nsys trace under-counts GPU-busy
   unless `--cuda-graph-trace=node` is set.

No code patch in Phase 1 (graphs are not the lever, so there is no paged
graph-capture patch to land). Evidence: `~/bench/a2_4cell_v2/`, `~/bench/a2_probe`,
`~/bench/a2_probe2`, `~/bench/a2_nsys/*.nsys-rep` on the DGX.
