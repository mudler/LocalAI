# Paged llama.cpp vs vLLM - apples-to-apples (batched + NVFP4 + prefix cache)

Definitive matched comparison on a DGX Spark (GB10, sm_121). Both engines batched,
both NVFP4-class weights, both with prefix caching on, both eager (no CUDA graphs).
Workload: shared 1024-token system prefix + unique 32-token suffix, generate 64
tokens, K requests fired concurrently (cold fan-out), one client hitting both
OpenAI-compatible servers with identical token-id prompts.

This run fixes the two confounders in the earlier comparison (a *serial* Q4_K dev
driver vs a *batched* FP4 vLLM server). Here both sides are batched and NVFP4.

## Setup

- llama.cpp: `llama-server` built from the paged dev tree (`~/llama-paged-dev`,
  branch `paged`, patches 0001-0007), CUDA `build-cuda/` (sm_121).
  `LLAMA_KV_PAGED=1`, `-ngl 99 --parallel 32 -c 40960`, model
  `q3-32b-nvfp4-dense.gguf` (NVFP4 weights, FP4-MMA kernel). OpenAI `/completion`.
- vLLM 0.23.0: `vllm serve q3-32b-nvfp4a16/` (compressed-tensors W4A16 / Marlin),
  `--enforce-eager --max-model-len 4096 --gpu-memory-utilization 0.9
  --max-num-seqs 64`, APC on (default). OpenAI `/v1/completions`.

## Finding 1 - the paged cross-request prefix cache does NOT engage in llama-server

This is itself a key result. The paged engine has two distinct mechanisms:

1. Physical paged block placement (patches 0002/0004) - runs inside
   `llama_kv_cache::find_slot`, gated only by `LLAMA_KV_PAGED`. This DOES engage in
   the server: with `LLAMA_KV_PAGED_DEBUG=1`, 2 concurrent shared-prefix requests
   produced 14 `[paged-alloc] ... grew` lines, one stream per `seq`.

2. Cross-request prefix recompute-skip (patch 0007) - the actual fan-out win
   (`shares N prefix blocks ... prefix NOT recomputed`, ref-counted block sharing).
   This is reachable ONLY through `paged_prefix_api::share/commit`
   (`src/paged-prefix-api.cpp`), which only the standalone driver calls.

Evidence it does not reach the server:
- Static: `grep -rn "paged_prefix\|share_prefix\|LLAMA_KV_PAGED" tools/server/`
  returns nothing; `nm` on the binary finds no `paged_prefix` symbol use from the
  server path. Nothing in `llama_decode` or the server calls `share`/`commit`.
- Runtime: the 2-request verify run logged **0** `shares prefix blocks` /
  `NOT recomputed` lines. Both `seq=0` and `seq=1` independently grew to 65 blocks,
  each allocating and recomputing the full ~972-token prefix separately - no
  cross-slot KV block sharing, no `ref_cnt>1`.

So the 0007 recompute-skip, proven in the driver, does **not** yet reach the
server. Closing it needs server-side wiring: when admitting a slot whose prompt
shares a prefix with another live/committed slot, the server would have to call
the `paged_prefix_api::share` / `commit` seam. That is a future patch.

Note: llama-server has its OWN native prefix reuse (the slot prompt cache /
"context checkpoints"). In the K=32 wave the server reused the prefix cached by the
earlier wave, so prefill was only the 32-token suffix (`prompt eval ... / 32
tokens`). But that is a separate mechanism, it only helps prefill, and prefill is
not the bottleneck here (see below), so it does not change the verdict.

## Finding 2 - the matched comparison

Both batched, both NVFP4, both prefix-cache on, both eager. Cold concurrent fan-out,
identical token-id prompts via one client.

| K  | engine   | wall (s) | aggregate gen tok/s | req/s | vLLM speedup |
|----|----------|----------|---------------------|-------|--------------|
| 16 | llama.cpp| 50.7     | 18.9                | 0.30  | -            |
| 16 | vLLM     | 8.57     | 119.5               | 1.87  | ~5.9x        |
| 32 | llama.cpp| 58.3     | 34.0                | 0.53  | -            |
| 32 | vLLM     | 8.86     | 231.1               | 3.61  | ~6.6x        |

vLLM APC confirmed engaged: prefix cache hit rate 90.9% (K=16), 95.5% (K=32),
enforce_eager (CUDA graphs disabled), `enable_prefix_caching=True`.

### Verdict: not competitive - vLLM ~6x faster, and prefix caching is not why

With every confounder removed (both batched, both NVFP4, both eager, both with
prefix caching on), vLLM is still ~6x faster end-to-end. The gap is decode-bound,
not prefill/cache-bound:

- The G=64 workload is dominated by decode. In the llama K=32 run, decode was
  52.98s of the 58.3s wall; prefill was ~3.5s (and only the 32-token suffix, since
  the server's native prompt cache already reused the prefix). So even perfect
  prefix sharing - paged or native - cannot move the total much.
- llama.cpp batched decode: **~828 ms per decode step** at batch 32
  (1.21 tok/s per sequence).
- vLLM batched decode: ~170 tok/s aggregate gen at 32 running reqs ->
  **~185 ms per step**, roughly **4-5x faster per decode step**.
- CUDA graphs are NOT the differentiator: both sides are eager (llama
  `graphs reused = 0`, vLLM `--enforce-eager`). The win is vLLM's batched-decode
  efficiency: PagedAttention + fused W4A16 (Marlin) GEMMs + chunked-prefill
  scheduler, versus llama.cpp's per-step eager graph and NVFP4-GGUF decode path on
  this Blackwell-class part.

Because decode dominates, wiring the paged 0007 recompute-skip into the server
(Finding 1) would mainly remove redundant prefill across slots - a real saving for
short-generation / long-prefix RAG fan-out, but at G=64 it is a few seconds against
a decode floor that is already ~6x slower than vLLM. The fan-out win does not, on
its own, make llama.cpp competitive here; the decode kernel/batching gap is the
load-bearing factor.

## Caveats

- NVFP4-GGUF is double-quant and is speed-representative (it routes onto the
  FP4-MMA kernel); output quality is not the subject of this run.
- vLLM side is NVFP4A16 (W4A16 / Marlin) - 4-bit weights, 16-bit activations;
  llama side is NVFP4 weights on FP4-MMA. Both are NVFP4-weight class.
- One llama request per run hit an intermittent HTTP 500 ("output does not match
  the expected Content-only format" - a Qwen3 thinking-output quirk on
  `/completion`), so llama counts were 15/16 and 31/32. The failed request returns
  early and reduces batch contention for the rest, so a clean 16/16 / 32/32 llama
  run would be marginally slower - i.e. the ~6x gap reported here is conservative
  (favorable to llama.cpp).
- Both servers cold-started; numbers are end-to-end wall from the concurrent
  client. Disk healthy (~325 GB free), GPU otherwise idle.
