# llama.cpp multi-user decode overhead on DGX Spark (GB10, sm_121)

Investigation of the Qwen3-32B concurrent-decode throughput gap (llama.cpp ~547 t/s
vs vLLM ~667 t/s) on the GB10 box, build `~/llama.cpp-pr24423/build` (Release,
sm_121, `LLAMA_MAX_SEQ=256`, flash-attn on), model
`~/bench/q3-32b-gguf/Qwen3-32B-Q4_K_M.gguf`.

## TL;DR (the result overturns the brief's premise)

On **this** build the prime suspect is wrong and the host-overhead premise does not
hold:

1. **CUDA graphs are NOT disabled at high concurrency.** At npl=128, 94 of 98
   decode `graph_compute` calls **replay a captured CUDA graph** (0 resets, stable
   key, no property churn post-warmup). The keyed-warmup gate works.
2. **There is no ~170ms/step host hotspot here.** The GPU is **~96% active during
   decode with graphs ON and ~96% active with graphs OFF**. Decode at npl=128 is
   **GPU-compute-bound**, not host-bound.
3. The brief's "20% GPU util / 66ms GPU / 170ms host per step" was measured on a
   different/earlier build (mainline without these graph fixes). It is not
   reproducible on `llama.cpp-pr24423`.
4. Because the GPU is the bottleneck, re-enabling graphs cannot lift the number:
   the clean A/B shows graphs ON vs OFF = **+1.5% at npl=128** (and +2.9% at
   npl=32 - the benefit shrinks as concurrency rises and the GPU saturates).
5. The real gap to vLLM is the **quantized decode GEMM kernel**: `mul_mat_q`
   (Q4_K + Q6_K) is ~68% of decode GPU time and runs ~2.1x above the GB10
   memory-bandwidth floor. Closing the gap requires Marlin/Machete-style int4
   GEMM kernels, not host-side work. This is a kernel project (the direction the
   prior session's uncommitted `marlin-w4a16.cu` / `fp4-grouped-moe.cu` already
   started, though those target w4a16/GPTQ-int4, not the K-quants this GGUF uses).

## 1. Why CUDA graphs are (not) disabled - exact code + measurement

### The gate (code)

PR24423 refactored the CUDA-graph path into a keyed, warmup-based scheme in
`~/llama.cpp-pr24423/ggml/src/ggml-cuda/ggml-cuda.cu`:

- `ggml_cuda_graph_get_key(cgraph)` (~L3343) keys the cached CUDA graph by
  `cgraph->nodes[0]` (first-node pointer).
- `ggml_cuda_graph_check_compability(cgraph)` (~L3301) disables graphs only for:
  - **split buffers** (`ggml_backend_buft_is_cuda_split`), and
  - **`GGML_OP_MUL_MAT_ID`** when `src0` is non-quantized **or**
    `ne[2] > get_mmvq_mmid_max(...)` (MoE expert routing needs a stream sync).
  Qwen3-32B is **dense** -> no `MUL_MAT_ID` -> this condition never fires.
- `ggml_backend_cuda_graph_compute` (~L4514) warmup gate: a graph is used only
  after **2 consecutive calls with no property change** (`warmup_complete`); any
  property change resets warmup. `ggml_cuda_graph_update_required` (~L3347)
  detects change by `memcmp` of the full `ggml_tensor` struct + per-src
  data-ptr/ne/nb, with a fast path when `cgraph->uid` is unchanged.

### Why it stays enabled across decode steps

The graph stays stable because llama.cpp's host-side graph reuse holds during
decode, so node pointers/props (and `cgraph->uid`) do not churn:

- `llama_kv_cache::get_n_kv` (`src/llama-kv-cache.cpp` L1223-1233) **pads n_kv to
  a multiple of 256** ("so that the graph remains constant across batches and can
  be reused"). For ntg<=256 within the first KV block, n_kv is constant.
- `can_reuse_kq_mask` (`src/llama-graph.cpp` L43) keeps the KQ-mask dims stable:
  `ne=[n_kv, n_tokens/n_stream, 1, n_stream]` = `[256,1,1,128]` every decode step
  at npl=128.
- `can_reuse` (`src/llama-context.cpp` L1283) therefore returns true, so the
  scheduler is **not** reset/re-split. `graph->uid` is only reassigned inside
  `ggml_backend_sched_split_graph` (`ggml/src/ggml-backend.cpp` L1033, L1485),
  which is skipped on the reuse path -> stable uid -> CUDA graph replays.

### Measurement (instrumented build, npl=128, ntg=96)

Env-gated counters added to `ggml_backend_cuda_graph_compute` /
`ggml_cuda_graph_update_required` (since `GGML_LOG_DEBUG` is compiled out in
Release / NDEBUG). End-of-run summary:

```
[GTRACE-SUMMARY] calls=98 notenab=0 warming=3 warmdone=1 RESET=0 USED=94 incompat=0 distinct_keys=1
```

94/98 decode `graph_compute` calls **replayed** a captured CUDA graph; **0**
warmup resets; a **single** distinct graph key for the whole decode; no node
property churn after warmup. Graphs are fully engaged at npl=128.

(The instrumentation was reverted afterwards; the checkout is back to its
pre-task state and the `.so` rebuilt clean.)

## 2. The per-step CPU "hotspot" - there isn't one on this build

GPU utilization during npl=128 decode (ntg=256):

- **Graphs ON** - `nvidia-smi` sampled every 0.7s through the decode phase:
  steady **96% GPU util**, SM clock **2184 MHz** (not throttled), 45-47 W.
- **Graphs OFF** (`GGML_CUDA_DISABLE_GRAPHS=1`) - nsys CUDA trace, 8s window:
  total GPU kernel time = `3,983,292,128 ns / 0.516` = **~7.72s of the 8s
  window = ~96% GPU-active**. Even with every kernel launched individually from
  the host, the GPU is still ~96% busy. There are essentially **no host gaps**.

Per-step wall = 60.6s / 256 steps = **~237 ms/step**, and the sum of one decode
graph's kernel times (nsys, graphs-on capture) is ~244 ms -> GPU kernel time per
step ~= wall time per step. The host work between steps is in the low single-digit
ms (the ~4% idle), consistent with graphs ON giving only +1.5% at npl=128.

This directly contradicts the brief's 66ms-GPU / 170ms-host split, which must have
come from a pre-graphs build.

### Per-step GPU breakdown (nsys, npl=128 decode, graphs off, 8s window)

| Kernel | % GPU time | ~ms/step |
|--------|-----------:|---------:|
| `mul_mat_q` Q4_K (type 12) | 51.6 | ~118 |
| `flash_attn_ext_f16` | 19.3 | ~44 |
| `mul_mat_q` Q6_K (type 14) | 16.2 | ~37 |
| `unary_gated` silu | 4.1 | ~9 |
| mmq stream-k fixup + quantize_q8_1 | ~5 | ~12 |
| rms_norm / rope / set_rows / add | ~4 | ~10 |

Quantized matmul = **~68%** of decode GPU time (~155 ms/step). Attention ~19%.

`perf` could not profile the host (kernel `perf_event_paranoid=4`), but it is moot:
the host is ~4% of the wall, so there is no ~170ms host hotspot to chase.

## 3. Fix attempt + measured result

### The requested fix (re-enable graphs / pad the decode batch) is a no-op here

Graphs are already enabled and the batch is already stable (n_kv padded to 256,
kq_mask dims constant). The clean cold A/B (cooldowns between every run):

| npl | graphs ON (t/s) | graphs OFF (t/s) | delta |
|----:|----------------:|-----------------:|------:|
| 32  | 242.60 | 235.75 | +2.9% |
| 64  | 398.59 | 389.06 | +2.5% |
| 128 | 543.95 | 535.71 | +1.5% |

Baseline (separate cold runs, original non-instrumented build):
npl=32 243.9, npl=64 397.1, **npl=128 544.95** (matches the ~546 baseline).

Graphs help, but the benefit **monotonically shrinks** as concurrency rises and
the GPU saturates. At npl=128 there is only ~1.5% of host launch overhead left to
remove, and GPU util is ~96% in both columns. **You cannot lift npl=128 decode
toward 667 by working on graphs/host overhead - the GPU is the bottleneck.**

### Where the number actually is, and the real lever

- vLLM 667 t/s at this concurrency = **192 ms/step**; llama.cpp 547 = **237
  ms/step**. The ~45 ms/step gap maps almost entirely onto the quantized matmul.
- GB10 memory-bandwidth floor for a 32B Q4_K_M (~19.8 GB of weights, read once
  per step and shared across the 128 sequences) at ~273 GB/s is **~72 ms/step**.
  llama.cpp's `mul_mat_q` spends ~155 ms/step on matmul = **~2.1x the bandwidth
  floor**. vLLM's Marlin/Machete int4 GEMMs run much closer to the floor; that
  efficiency difference is the ~547 -> 667 gap.
- The Q6_K matmul (`mul_mat_q` type 14) also shows pathological tail latency
  (median 0.89 ms, max 5.5 ms) - the MMQ kernel is not well-tuned for the skinny
  n=128 decode shape.

**The lever to beat 547 is a faster quantized decode GEMM**, i.e. a Marlin-style
int4 kernel for the decode shapes. This is exactly the direction of the prior
session's uncommitted `ggml/src/ggml-cuda/marlin-w4a16.cu` and
`fp4-grouped-moe.cu` (already wired via
`if (!split && ggml_cuda_w4a16_mul_mat(...)) return;` in `ggml_cuda_mul_mat`).
Note those target **w4a16 / GPTQ-int4**, while this GGUF is **K-quant (Q4_K/Q6_K)**,
so they are inert for this model - a Marlin path for K-quants (or shipping the
model in a Marlin-friendly int4 format) would be required. That is a multi-day
kernel effort, out of scope for this session, but it is the only lever that can
move the number.

### Why the "bump LLAMA_MAX_SEQ to 1024 -> 377" data point is consistent

`llama_batch_allocr` keeps `seq_cpl` as an `LLAMA_MAX_SEQ x LLAMA_MAX_SEQ` table
(`src/llama-batch.cpp`), so per-batch seq bookkeeping scales ~O(MAX_SEQ^2). At
MAX_SEQ=1024 that host cost becomes large enough (~70 ms/step) to dominate and
drop decode to 377. At MAX_SEQ=256 the same term is ~4.4 ms/step (the ~1.5% that
graphs reclaim); lowering to 128 would save ~3 ms/step (~1%). So MAX_SEQ tuning
confirms the host term is real but tiny at 256 - not a path to 667.

## How this would land in LocalAI

- **No host/graph patch is warranted** for this build: graphs already engage and
  the decode is GPU-bound. A "pad the decode batch / force graph capture" patch
  would change nothing measurable at high concurrency.
- The actionable upstream/vendored work is a **Marlin-style int4 decode GEMM**
  (extend the prior `marlin-w4a16.cu` to cover K-quants, or quantize the served
  model into a Marlin-friendly int4 layout). That is where the ~547 -> 667+ lives.
- If a small host win is still wanted, keep `LLAMA_MAX_SEQ` no larger than the max
  concurrency actually used (the per-batch `seq_cpl` table is O(MAX_SEQ^2)).

## Reproduction

```
# baseline / A/B (cold, 30s cooldowns)
llama-batched-bench -m Qwen3-32B-Q4_K_M.gguf -npp 16 -ntg 128 -npl 32,64,128 \
  -ngl 99 -b 2048 -ub 2048 -fa on            # graphs on
GGML_CUDA_DISABLE_GRAPHS=1 ...same...        # graphs off

# GPU util (graphs on): sample nvidia-smi during decode -> ~96%, 2184 MHz
# GPU active (graphs off): nsys profile -t cuda --delay=6 --duration=8 ...
#   nsys stats --report cuda_gpu_kern_sum  -> sum/0.516 ~= 7.72s of 8s = ~96%
```

## UPDATE: NVFP4 closes most of the decode gap (no Marlin-for-K-quants needed)

The diagnosis above said the lever is "a more bandwidth-efficient int4 decode GEMM"
and feared a multi-day Marlin-for-K-quants kernel. But the FP4-MMA path is already
that kernel. Measured (npl=128, cold A/B, npp=16 ntg=128):

| quant | decode S_TG (t/s) | vs Q4_K | vs vLLM 667 |
|---|---|---|---|
| Q4_K_M | 547 (548/546) | - | 82% |
| **NVFP4** | **619 (617/622)** | **+13%** | **93%** |

NVFP4's `mul_mat_q<NVFP4>` runs closer to the GB10 bandwidth floor at the thin n=128
decode shape than Q4_K's int8-MMQ (which ran ~2.1x above it). So shipping the model
as NVFP4 closes the decode gap from ~22% to ~7% AND wins prefill (1209 vs Q4 767 /
vLLM 800). Net on GB10: llama.cpp+NVFP4 is ahead on prefill (1.5x) and within ~7% on
decode. The remaining ~7% would be incremental FP4-MMA decode-kernel tuning, NOT a
from-scratch Marlin kernel - a much smaller, optional effort. NVFP4 is the answer to
both the prefill and the decode gap.
