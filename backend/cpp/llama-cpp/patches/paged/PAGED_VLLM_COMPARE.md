# Paged-attention closing measurements: stock GPU determinism + vLLM comparison

Two closing measurements for the paged-attention series, run on a DGX Spark
(NVIDIA GB10, compute capability 12.1 / sm_121), CUDA 13. Dev tree
`~/llama-paged-dev` branch `paged`, paged engine gated by env `LLAMA_KV_PAGED`
(default-off = stock). Models: `Qwen3-0.6B-Q8_0.gguf` and
`Qwen3-32B-Q4_K_M.gguf` (llama.cpp), `Qwen3-32B` nvfp4a16 / W4A16 HF safetensors
(vLLM 0.23.0). All dev drivers are dev-tree-only and not shipped.

## Deliverable 1: stock GPU determinism across batch shapes (no paging)

Question: is the patch-0007 GPU byte-identity "failure" (a near-tie greedy token
flips on CUDA, e.g. 17971 vs 5671) caused by paging, or is it inherent stock
CUDA non-determinism from running the same tokens in a different batch shape?

Method: a new dev-only driver `llama-paged-batchshape` (paging explicitly OFF:
`unsetenv("LLAMA_KV_PAGED")`). For a prompt `[P+S]` it greedy-decodes two ways,
both stock contiguous KV:

- (a) `full`  - prefill the whole `[P+S]` in ONE `llama_decode`.
- (b) `split` - prefill `P` in one `llama_decode`, then `S` in a second.

The two paths write byte-for-identical token ids; the only difference is the
batch shape submitted to the kernels (full prefill vs P-then-S), which changes
the float reduction order in the GEMMs and therefore the KV values by tiny
amounts. 5 distinct prompts, suffix S=16.

### Single next token (the literal T_full vs T_split)

Both CPU and CUDA returned the SAME greedy next token for all 5 prompts
(0/5 flips). BUT the top-2 logit gap measurably changes with the batch shape on
CUDA, proving the float order does differ:

```
CUDA, S=8:  prompt 1  T_full=1896 (gap 0.07072)   T_split=1896 (gap 0.17986)
CUDA, S=8:  prompt 4  T_full=49584 (gap 0.93304)  T_split=49584 (gap 0.85785)
```

The argmax simply did not flip on the immediate next token for these prompts -
the gaps, while shifting, stayed wide enough.

### Generated stream (what 0007 actually byte-asserts)

0007 asserts byte-identity over a *generated* token stream, where the tiny
prefill-shape KV perturbation accumulates and eventually crosses a near-tie.
Generating G tokens greedily from `full` vs `split` and reporting first
divergence:

| gen length | CPU diverged | CUDA diverged |
|-----------|--------------|---------------|
| G=24 (0007 default) | 1/5 (prompt 0 @ step 5) | 2/5 (prompt 1 @ step 3, prompt 4 @ step 6) |
| G=64 | 2/5 (steps 5, 42) | 3/5 (steps 3, 6, 30) |

Example CUDA divergence, pure stock, zero paging:
`prompt 1: DIVERGES at gen step 3: full=1260 split=576`.

### Verdict (Deliverable 1): HYPOTHESIS HELD

The 0007 GPU byte-identity failure is **stock batch-shape non-determinism, not a
paged bug**. With paging entirely OFF, stock llama.cpp produces a different
greedy token stream when the same prompt is processed in a full-prefill batch vs
a split (prefix-then-suffix) batch - exactly the shape difference that 0007's
prefix-share path introduces (full B-from-scratch vs prefix-cached + suffix-only).

Refinement (reported honestly): it is **not strictly CUDA-only**. CPU exhibits
the same divergence, just less often and later (1/5 vs 2/5 at G=24, and CPU's
flips land at later generation steps). This is exactly why 0007's small, short
CPU scenarios happened to pass 16/16 while the CUDA run flipped: CUDA's larger
parallel reductions reorder more aggressively, so a near-tie crosses earlier and
more frequently. The phenomenon is floating-point GEMM-batching non-determinism,
inherent to both backends; paging is not the cause.

## Deliverable 2: vLLM vs llama.cpp+paged on a shared-prefix fan-out

Workload: K requests share a 1024-token system prefix, each with a unique
32-token suffix, then generate 64 tokens. Both engines cache the shared prefix
(vLLM automatic prefix caching ON by default; llama.cpp via the paged
cross-request prefix cache, `LLAMA_KV_PAGED=1`).

Quant is the realistic apples-to-oranges, reported honestly:
- llama.cpp: Qwen3-32B **Q4_K_M** (GGUF), `-ngl 99`, CUDA dequant kernels.
- vLLM: Qwen3-32B **nvfp4a16 (W4A16)**, served via the **Marlin FP4
  weight-only** kernel because GB10 (sm_121) has **no native FP4 compute** -
  i.e. vLLM is on a slower-than-ideal kernel path here. vLLM also ran
  `enforce_eager=True` (no CUDA graphs / torch.compile; the env lacked a working
  inductor/ninja toolchain), so the vLLM numbers are if anything **conservative**.

### vLLM (automatic prefix caching), end-to-end

APC hits confirmed in the engine log: **"Prefix cache hit rate: 97.0%"**,
`prefix_cache_hits 33040/34848` (K=16) and `99344/102432` (K=32).

| K | APC | prefill wall (G=1) | total wall (G=64) | throughput |
|---|-----|--------------------|--------------------|-----------|
| 16 | ON  | 0.749 s | 6.63 s | 2.41 req/s |
| 16 | OFF | 20.19 s | 27.21 s | 0.59 req/s |
| 32 | ON  | 1.13 s  | 7.56 s | 4.23 req/s |
| 32 | OFF | 40.19 s | 48.71 s | 0.66 req/s |

vLLM's APC cuts the fan-out prefill ~27x (K=16) to ~36x (K=32) vs APC-off; the
huge ratio reflects how slow the FP4-emulation prefill is when forced to
recompute all K prefixes.

### llama.cpp + paged prefix cache (prefill phase)

The paged shared-prefix bench (`llama-paged-prefix-bench`, `BENCH_GEN=0`,
`PAGED_NGL=99`). Reuse confirmed: `kshare(seq1)=1024`, shared-block
`ref_cnt = K` (all sequences hold the one prefix), 15360 / 31744 prefix tokens
skipped.

| K | mode | prefill tokens submitted | prefill wall | vs no-share |
|---|------|--------------------------|--------------|-------------|
| 16 | PAGED-SHARE | 1536  | 3.66 s  | 7.15x |
| 16 | NO-SHARE    | 16896 | 26.17 s | 1.0x  |
| 32 | PAGED-SHARE | 2048  | 6.04 s  | 10.3x |
| 32 | NO-SHARE    | 33792 | 62.17 s | 1.0x  |

The paged prefix cache delivers the expected **7.15x (K=16) / 10.3x (K=32)**
prefill wall-time reduction - the headline cross-request prefix-skip win, on a
real 32B model on GPU.

### Head-to-head, both engines caching the shared prefix

Prefill of the cached fan-out (vLLM G=1, ~prefill; llama.cpp G=0, pure prefill):

| K | llama.cpp+paged prefill | vLLM APC prefill | vLLM faster by |
|---|-------------------------|------------------|----------------|
| 16 | 3.66 s | 0.749 s | ~4.9x |
| 32 | 6.04 s | 1.13 s  | ~5.3x |

### Verdict (Deliverable 2): competitive in kind, behind in absolute terms

With both engines caching the shared prefix, **llama.cpp+paged is qualitatively
competitive but absolutely behind vLLM on this GB10 box**:

- **Same optimization, same order of magnitude.** llama.cpp's paged prefix cache
  reproduces exactly the win vLLM's APC gives - skip the shared-prefix recompute
  - and yields a 7-10x prefill reduction vs its own no-share baseline. On the
  RAG/system-prompt fan-out the algorithmic gap is closed: llama.cpp no longer
  pays K x prefix.

- **vLLM still wins head-to-head by ~5x on the cached prefill** (0.75s vs 3.66s
  at K=16; 1.13s vs 6.04s at K=32), and by more end-to-end because it does
  **continuous batched decode** (all K sequences decoded in one fused step)
  while the llama.cpp paged *dev driver* decodes each sequence serially. That
  decode-batching gap is a property of the serving stack, not of the paged
  prefix cache. Notably vLLM wins here while handicapped (eager mode, FP4
  weight-only emulation with no native FP4 on GB10); a tuned vLLM would lead by
  more.

- **Honest caveats / blockers.** (1) Quant differs (Q4_K_M vs nvfp4a16). (2) The
  comparison is prefill-vs-prefill plus vLLM end-to-end; a clean llama.cpp
  end-to-end on this driver is blocked because its generation phase has a
  stale-logits bug (`get_logits_ith` reads seq 0's prefill index after later
  sequences' prefills overwrote the logits buffer -> segfault), and even fixed
  its decode is serial, so it would not be apples-to-apples vs vLLM's batched
  decode. The fair end-to-end llama.cpp number needs the grpc / llama-server
  continuous-batching path, not this dev scaffold. (3) vLLM ran eager + FP4
  emulation, making its numbers conservative.

Bottom line: paged gives llama.cpp the cross-request prefix-skip that vLLM's APC
provides, which is the categorical win and removes the K x prefix penalty on
RAG/system-prompt fan-out. On absolute wall-time on this hardware vLLM retains a
~5x prefill lead and a larger end-to-end lead from continuous batched decode and
a more optimized serving stack.
