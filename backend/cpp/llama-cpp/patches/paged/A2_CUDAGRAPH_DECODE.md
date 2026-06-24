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

# Phase 2 - the real decode lever, located (per-kernel decomposition)

Phase 1 ended on "decode is GPU-compute-bound; the 2.6x gap to vLLM lives in the
per-step GPU kernel work (FP4 GEMM + attention at batch 128)." Phase 2 measured
that per-step GPU work directly - per kernel and per memcpy, on the Phase-1 nsys
`.sqlite` reps - and the "FP4 GEMM + attention" attribution does not survive the
measurement. Two corrections, then the lever.

The conditional Phase 2 fix (make the paged decode graph-capturable) is moot:
Phase 1 already showed the default paged decode captures, and the fresh re-check
below reconfirms the graph win is noise. Neither Phase 2 branch (within-step graph
fix / between-step host loop) is the lever; the lever is a third thing, measured
here.

## Fresh re-confirmation: graphs are not the lever

Independent run (npl128, ntg32, paged, fusion off), not reusing Phase 1's table:

| paged decode  | S_TG t/s | vs vLLM 391 |
|---------------|----------|-------------|
| graphs ON     | 146.03   | 37.3%       |
| graphs OFF    | 144.90   | 37.1%       |

+0.78%, within noise - same verdict as Phase 1's 4-cell. The ON nsys rep is also
99.5% busy with the same ~3267 ms of memcpy as OFF: graphs capture the memcpy
nodes too, so they cannot remove either the copies or the compute.

## Correction 1: the model is a hybrid SSM, not a plain transformer

`q36-27b-nvfp4.gguf` has `general.architecture = qwen35` with
`qwen35.ssm.{conv_kernel,state_size,group_count,time_step_rank,inner_size}`. The
decode-window kernel cadence (per step, ~19.8 steps in the window) is 48
`gated_delta_net_cuda` + 48 `ssm_conv_f32` vs 16 `flash_attn_tile`, i.e. **48
gated-DeltaNet linear-attention layers : 16 full-attention layers** (a 3:1
hybrid, Qwen3-Next family). Paged attention only touches the 16 full-attention
layers.

## Correction 2: the 99.4% "busy" is ~19% D2D memcpy, not compute

Interval-union sweep over the steady decode window (last 17 s of the npl128/ntg24
OFF rep; single CUDA stream; running-max-end so it is overlap-correct):

| activity set           | GPU busy | idle  |
|------------------------|----------|-------|
| kernels only           | 80.2%    | 19.8% |
| kernels + memcpy (all) | 99.4%    | 0.6%  |

The 969 inter-kernel gaps (>=1 ms, ~48/step) that drop kernels-only to 80% are
filled by **D2D memcpy: 1584 copies/run (~80/step), ~230 MB each, ~2 ms each,
356 GB moved in 17 s**. At batch 128 a ~230 MB block is the gated-DeltaNet
recurrent state; these are the per-SSM-layer state copies. (HtoD copies = the
paged block-table/index upload: 731/run but only 3 ms total, negligible; DtoH
47 ms.) Phase 1's `cuda_gpu_trace`-based 99.4% counted these memcpys as "busy"
and lumped them into "GPU kernel compute" - they are memory movement, and they
are addressable.

## Decode GPU-time decomposition (% of kernel+memcpy busy)

OFF/eager rep, steady window. `/step` = instances per decode step.

| share | activity                          | /step | role                          |
|-------|-----------------------------------|-------|-------------------------------|
| 23.4% | gated_delta_net_cuda              | 48    | linear-attn recurrence        |
| 21.9% | k_get_rows_float                  | 97    | SSM state / conv-state gather |
| 18.9% | MEMCPY DtoD                       | 80    | SSM recurrent-state copy      |
| 15.5% | mul_mat_vec_q (nvfp4, ncols=1)    | 48    | FP4 GEMV                      |
| 10.4% | mul_mat_q (nvfp4)                 | 352   | FP4 GEMM                      |
|  1.9% | quantize_mmq_nvfp4                | 448   | act requant for MMQ           |
|  1.0% | concat_cont                       | 48    | SSM state glue                |
|  0.8% | ssm_conv_f32                      | 48    | SSM short conv                |
|  0.7% | unary_gated_op silu               | 112   | SSM gating                    |
|  0.4% | flash_attn_tile/_ext              | 16    | FULL attention (paged)        |

Grouped:
- gated-DeltaNet / SSM machinery (recurrence + get_rows gather + DtoD state copy
  + conv + gating glue): **~67% of decode**.
- FP4 matmul (GEMV + GEMM + requant + stream-k fixup): **~28%**.
- Full attention - everything paged attention optimizes: **~0.4%**.

## Verdict and scope of the real lever

1. CUDA graphs: not the lever (Phase 1, re-confirmed: +0.78%, noise). They capture
   the memcpy too, so they cannot touch the copies or the compute.
2. Host loop: not the lever (true host idle in the union is 0.24%, ~41 ms/17 s).
3. FP4 GEMM: secondary, ~28%. Consistent with Track B P2a (making the FP4 GEMM 26%
   faster left decode_agg flat) - it was never the long pole.
4. Paged / full attention: ~0.4% of decode. **No paged-attention change (graphs,
   block-table stabilization, gather rewrite) can move decode_agg on this model**
   - it optimizes under half a percent of the step. This is the structural reason
   A.2, and the paged-decode track generally, cannot close the vLLM gap on
   q36-27b: the model barely uses the path being optimized.

The throughput lever is the ggml **qwen35 gated-DeltaNet decode**. Per SSM layer
per step it re-materializes and D2D-copies the full recurrent state (~230 MB at
batch 128; ~80 copies/step, ~18 GB/step) and feeds the recurrence through ~2
`get_rows` gathers, so ~61% of decode (state copy + state gather + recurrence) is
SSM state plumbing. vLLM's gated-DeltaNet decode (the flash-linear-attention
`fused_recurrent_gated_delta_rule` path) keeps the state in place and fuses the
gather into the scan, avoiding both the per-layer D2D copy and the gathers.

Next-step scope (the real lever, to be done in the ggml/llama qwen35 SSM path -
not paged-attn, not a graph capture, not a block-table tweak):
1. Eliminate the per-layer recurrent-state D2D copy: update the state tensor
   in place (or double-buffer / write-back), so the recurrence consumes and
   produces the persistent state without a full-state copy each layer each step.
2. Fuse the `get_rows` state / conv-state gather into the recurrent kernel.

Ceiling from this rep (upper bound; assumes the work is fully removed, not just
overlapped):
- remove the DtoD state copy: reclaim 18.9% -> ~146 to ~180 t/s.
- remove copy + gather: reclaim ~41% -> ~146 to ~247 t/s, which puts llama within
  ~1.6x of vLLM 391 with the FP4 GEMM still untouched.

No code patch in Phase 2 either: the lever is a gated-DeltaNet decode rewrite in
the SSM path, too large for this measurement pass and orthogonal to paged
attention. `patches/paged/0018` stays free. Evidence on the DGX:
`~/bench/a2_decompose/decode_decomp.txt` (per-kernel table + reproducing SQL in
its header), `~/bench/a2_decompose/SUMMARY.txt`, and the Phase-1 reps
`~/bench/a2_nsys/paged_off_npl128.sqlite` / `paged_on_npl128_node.sqlite`.

# A.2 final synthesis - the four-point verdict

All numbers measured on the DGX (GB10, sm_121, q36-27b-nvfp4 dense, fusion OFF,
`decode_agg` = `S_TG t/s`), npl 128 unless noted.

**1. CUDA-graph lever size (measured, not guessed).** +0.13% (4-cell, stock
ON-vs-OFF) to +0.78% (fresh paged re-check) at npl 128; +1.1% to +1.4% at npl 32.
All inside run-to-run noise. The earlier grounding GUESSED ~10-20% from a
94.6%-busy reading; direct measurement puts the steady decode at 99.4-99.5% busy,
so the real graph ceiling is < 1%, not 10-20%. The guess was wrong because the
busy-fraction it rested on was under-read (a graphs-ON nsys trace under-counts
GPU-busy unless `--cuda-graph-trace=node` is set - trap #2).

**2. Was "paged decode runs eager" fixed, and what is the decode_agg win?**
There was nothing to fix: the premise was false. At the benchmarked context the
DEFAULT in-kernel paged decode already captures and replays graphs, with a
256-token reset cadence identical to stock non-paged (10 complete / 8 reset over
~320 steps, resets clustered only at the 256/512 token boundaries). "graphs
reused=0" was a uid fast-path false negative, not eager execution (trap #1). The
only genuinely-eager path is the `LLAMA_KV_PAGED_GATHER=1` fallback (unpadded
index grows every step), which is not the default decode. Because graphs were
already engaged, the decode_agg win from "enabling" them is ~0 (+0.1% to +0.8%).
Graphs DID collapse within-step launch idle (0.37% -> 0.11%, ~80k -> ~15k
launches/run), but the GPU stays 99.4-99.5% busy, so throughput is unchanged.

**3. New llama %-of-vLLM @npl128.** Unchanged by A.2: 146-148.6 t/s vs vLLM 391 =
**37.3-38.0%**. Graphs ON vs OFF both land here (146.03 / 144.90 in the fresh
re-check; 148.41 / 148.21 in the 4-cell). A.2 did not move the percentage.

**4. Honest verdict - did A.2 move toward parity; residual + next lever.** No.
A.2 closed zero of the 2.6x gap, and it provably cannot on this model: paged /
full attention is ~0.4% of decode (16 full-attention layers vs 48 gated-DeltaNet
layers, a 3:1 hybrid SSM), so no graph / block-table / gather change to the paged
path can move decode_agg. The residual gap is structural and lives elsewhere:
~67% of decode is gated-DeltaNet / SSM state plumbing (23.4% recurrence + 21.9%
get_rows state gather + 18.9% D2D recurrent-state copy of ~230 MB per SSM layer
per step, ~18 GB/step), and ~28% is FP4 matmul (already shown secondary by Track
B: a 26%-faster GEMM left decode_agg flat). The within-step launch loop is solved
(graphs) and the between-step host loop is a 0.24% second-order floor hidden under
GPU compute - neither is the residual.

The next lever is NOT in this track. It is the ggml qwen35 gated-DeltaNet decode:
(1) eliminate the per-layer recurrent-state D2D copy (in-place / double-buffer
write-back), and (2) fuse the get_rows gather into the recurrent kernel - mirroring
vLLM's `fused_recurrent_gated_delta_rule`, which keeps the state in place and
fuses the gather. Measured ceiling on this rep: remove the copy -> ~146 to ~180
t/s; remove copy + gather -> ~146 to ~247 t/s (within ~1.6x of vLLM with FP4 GEMM
still untouched). That work is orthogonal to paged attention; `patches/paged/0018`
stays free.
