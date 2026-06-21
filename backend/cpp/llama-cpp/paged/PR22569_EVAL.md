# Evaluation: llama.cpp PR #22569 (paged KV cache, `-kvp`) on DGX Spark (GB10, sm_121)

Question: is upstream draft PR #22569 the right base to give LocalAI vLLM-class
high-concurrency GPU throughput, or should we finish our own from-scratch P4
(`backend/cpp/llama-cpp/paged/`)?

Date: 2026-06-21. Hardware: NVIDIA GB10 (compute 12.1 / sm_121), 122502 MiB unified
memory, CUDA 13.0, gcc 13.3. Models: `Qwen3-32B-Q4_K_M.gguf` (18.4 GB, 64 layers,
n_head 64 / n_head_kv 8 / head_dim 128 / n_embd 5120) and `Qwen3-0.6B-Q8_0.gguf` for
the correctness gate.

## TL;DR verdict: DO NOT adopt #22569. Finish our own P4.

On GB10 with a 32B dense model, PR #22569 delivers **no throughput win and no concurrency
win** - it is ~12% *slower* than the existing contiguous path and hits the *same*
256-sequence ceiling. The "scale to thousands of sequences like vLLM" premise does not
hold for this PR or this hardware/model. On top of that it is broken out of the box,
wired to the wrong integration surface, and a contested draft.

## 1. Builds? Correct?

- **Builds: YES.** Cloned `matiaslin/llama.cpp@paged_attention` (PR #22569, single commit
  `0b0f7bd...`, base = current master). Clean CUDA build for sm_121
  (`-DGGML_CUDA=ON -DCMAKE_CUDA_ARCHITECTURES=121 -DCMAKE_BUILD_TYPE=Release`).
  `llama-paged`, `llama-batched-bench`, `test-paged-kv`, `test-paged-kv-e2e` all link.
  It is self-contained (ships its own CPU+CUDA `ggml_paged_attn` op) and does **not**
  depend on the competing CUDA PR #17579 (ericcurtin, `--pagedattention`).

- **Runs out of the box: NO.** `llama-paged -kvp` on Qwen3-32B *and* Qwen3-0.6B crashes
  at context creation:
  `build_attn(llm_graph_input_attn_kv_paged*) -> ggml_reshape_2d ->`
  `GGML_ASSERT(ggml_nelements(a) == ne0*ne1)` (src/llama-graph.cpp:2556). Same crash with
  `--fit off` (so it is the real graph, not just the memory probe).
  **Root cause:** the paged path hardcodes `ggml_reshape_2d(cur, hparams.n_embd, ...)`,
  wrong for any model where `n_head*head_dim != n_embd`. Qwen3 decouples head_dim:
  32B = 64*128 = **8192** vs n_embd 5120; 0.6B = 16*128 = **2048** vs 1024. The PR's
  "qwen3 verified" claim does **not** hold against current Qwen3 GGUFs. Fix is ~1 line
  (use the real attention width `cur->ne[0]*cur->ne[1]`); applied for the rest of the eval.

- **`fit_params` (`-ngpub` auto-sizing) also crashed on GB10** in the same reshape path
  during the device-memory probe (before the fix). After the reshape fix, paged
  auto-fit works (sized 96624 GPU blocks on the 0.6B from 85 GiB free).

- **Correctness after the reshape fix:** paged decode runs and produces **coherent**
  output on Qwen3-32B (sensible mercury / miso-soup / Starry-Night answers across 128 and
  256 concurrent sequences), indicating the `ggml_paged_attn` op is functionally roughly
  correct. PR's own greedy/top-K equivalence test (`test-paged-kv-e2e`, top-K argmax +
  top-5 overlap >= 4 + first-4-token greedy match vs non-paged) on Qwen3-0.6B did
  **not** reach a PASS/FAIL verdict on GB10: its paged auto-fit grabs ~88 GiB
  (96531 blocks) and the run then stalls at cache init (a third GB10 fit-robustness
  issue, distinct from the reshape bug). So the formal greedy-equivalence gate is
  **unverified on this box**, but the qualitative evidence (coherent multi-sequence 32B
  output with explicit small `-ngpub`) indicates the fixed op is roughly correct. This
  does not change the verdict, which is decided by throughput below.

## 2. Throughput: paged vs contiguous on GB10 (Qwen3-32B-Q4_K_M)

Contiguous = `llama-batched-bench` (unified KV, continuous batching), S_TG decode tok/s.
Paged = `llama-paged -kvp --fit off` (its scheduler-driven continuous-batching loop),
`aggregate tps`. Both `npp~16, ntg/n_predict=128, n_batch=n_ubatch=2048, -ngl 99`.

| npl  | contiguous (S_TG t/s) | paged `-kvp` (agg t/s) | outcome |
|------|----------------------|------------------------|---------|
| 128  | **537** (S 553)      | **477**                | both run; paged ~12% slower |
| 256  | **541** (S 550)      | **471**                | both run; paged ~13% slower; neither gains over 128 |
| 512  | FAIL                 | FAIL                   | **both** die: `n_seq_max must be <= 256` |
| 1024 | FAIL                 | FAIL                   | **both** die: `n_seq_max must be <= 256` |

### The decisive facts

1. **PR #22569 does NOT lift the 256-sequence ceiling.** Both contiguous and paged fail
   identically at npl 512/1024 with `n_seq_max must be <= 256` (llama.cpp's compile-time
   `LLAMA_MAX_SEQ`). It is **not** an OOM - GB10 has 119 GiB and at npl=256 contiguous KV
   is only 16 GiB. Paging gives **zero** concurrency headroom over contiguous here. The
   "paged unlocks thousands of seqs" premise is false for this PR.

2. **Paged is slower, not faster.** The fresh `ggml_paged_attn` op (477/471 t/s) loses to
   the mature CUDA flash-attention contiguous path (537/541 t/s) by ~12-13% at equal
   concurrency. The PR's A10G "2.5x" came entirely from contiguous OOMing at 26 seqs on a
   24 GiB card; that lever does not exist on GB10's 119 GiB.

3. **The 32B dense model is compute-bound and plateaus by npl=128 on GB10.** Aggregate is
   flat from 128->256 (contiguous 537->541; paged 477->471). Doubling concurrency buys
   nothing because the GPU is already saturated on the 32B weight matmuls. Even if we
   recompiled with a larger `LLAMA_MAX_SEQ`, aggregate would not climb - so vLLM-class
   ~24k aggregate is **unreachable for 32B-dense on a single GB10 regardless of KV
   layout**. The throughput gap to vLLM at this model/hardware is a compute/bandwidth
   problem, not a KV-fragmentation problem.

## 3. Verdict and reasoning: finish our own P4

**Do not adopt #22569 as the base.** Reasons:

- **No win on target hardware.** Even fully completed, on GB10 + 32B it is slower than
  what we already have and capped at the same 256 seqs. There is no throughput or
  concurrency dividend to harvest here.
- **Wrong integration surface.** Paged is driven only by a brand-new parallel C API
  (`llama_paged_scheduler_init/add_request/prepare_batch/get_batch_info/update/...`) and a
  bespoke `examples/paged` loop. `-kvp`/`--kv-paged` is gated to `LLAMA_EXAMPLE_PAGED`
  only - it is NOT wired into `llama-server`/`batched-bench`/`parallel`, i.e. NOT the path
  LocalAI's grpc-server derives from. Adopting it means rewriting LocalAI's serving loop
  around the new scheduler API.
- **Broken / restricted.** Crashes out of the box on all current Qwen3 (and any
  decoupled-head-dim model); fit_params crashed; Phase-1 restrictions enforced at context
  creation: single CUDA device, full offload only, `n_batch == n_ubatch`, no SWA
  (gemma3/llama4/etc. unsupported), no CoW / prefix-caching, no
  `seq_cp`/`seq_keep`/`seq_div`/`seq_add`, no state save/load.
- **Contested draft.** Unmerged; the author is openly asking maintainers whether the C
  API is even the right design; maintainers are skeptical of paged for single-node use.

**What P4 should actually target (re-scoped by this data).** The aggregate-throughput
gap to vLLM on a compute-bound dense model on one GB10 is not addressable by paged KV.
The durable, real LocalAI wins from paging are the ones our from-scratch P0 already
implements the machinery for and that #22569 explicitly omits:
- **on-demand KV sizing** (fit more *diverse* concurrent tenants without per-seq
  over-reservation), and
- **automatic cross-tenant prefix sharing** (chained-hash block cache - shared system
  prompts / RAG preambles), which #22569 defers to a non-existent Phase 2.

Finish our own P4 (CPU gather-read + a CUDA gather-read) against these capacity/
prefix-sharing objectives - measured as max concurrent *distinct* tenants and KV memory
saved, not single-model aggregate tok/s. To chase raw aggregate, the levers are lifting
`LLAMA_MAX_SEQ` and smaller/MoE models in memory-bandwidth-bound regimes - orthogonal to
paged attention. The ~1-line reshape fix found here (and the GB10 fit_params crash) are
worth upstreaming to #22569 regardless, but the PR is not our base.

### Reproduction (DGX, `~/llama.cpp-pr22569`)
```sh
export PATH=/usr/local/cuda/bin:$PATH
# contiguous
./build/bin/llama-batched-bench -m Qwen3-32B-Q4_K_M.gguf -ngl 99 -npp 16 -ntg 128 \
  -npl 128 -c 20480 -b 2048 -ub 2048        # 256/512/1024 -> n_seq_max must be <= 256
# paged (needs the src/llama-graph.cpp:2556 reshape fix: hparams.n_embd -> cur->ne[0]*cur->ne[1])
./build/bin/llama-paged -m Qwen3-32B-Q4_K_M.gguf -kvp --fit off -ngpub 2048 -ncpub 128 \
  -np 128 -ns 128 -n 128 -b 2048 -ub 2048 -ngl 99   # 512/1024 -> n_seq_max must be <= 256
```
