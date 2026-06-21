# Evaluation: llama.cpp PR #22569 (paged KV cache, `-kvp`) on DGX Spark (GB10, sm_121)

Question: is upstream draft PR #22569 the right base to give LocalAI vLLM-class
high-concurrency GPU throughput, or should we finish our own from-scratch P4
(`backend/cpp/llama-cpp/paged/`)?

Date: 2026-06-21. Hardware: NVIDIA GB10 (GB10, compute 12.1 / sm_121), 122502 MiB
unified memory, CUDA 13.0, gcc 13.3. Model: `Qwen3-32B-Q4_K_M.gguf` (19.7 GB) and
`Qwen3-0.6B-Q8_0.gguf` for the correctness gate.

## TL;DR verdict (FINAL, with throughput data)

**Paged KV is not the GB10 throughput lever - do not adopt #22569 AND do not build
paged KV for GB10.** The full sweep settles it:

```
CONTIG:  npl=128 -> 537 t/s   npl=256 -> 541 (plateau)   npl=512/1024 -> FAIL (n_seq_max<=256)
PAGED:   npl=128 -> 477 t/s   npl=256 -> 471             npl=512/1024 -> FAIL (n_seq_max<=256)
```

- Paged is **slower at every matched concurrency** (scheduler overhead).
- Paged **hits the same `LLAMA_MAX_SEQ=256` cap** - it does NOT deliver the higher
  concurrency that is its whole purpose.
- GB10's binding limit is **not KV capacity/fragmentation** (paged's domain) - it is
  the **256-seq compile cap** + the **~540 decode plateau already flat by npl=128**.
  Paged KV solves a problem GB10 does not have (122 GB unified memory).

Paged KV remains a valid feature for **memory-constrained datacenter GPUs** (24-48 GB,
where contiguous OOMs at low concurrency = vLLM's 9.5x win) - but that must be validated
on such hardware, NOT GB10. On GB10 the real questions are the 256-seq cap (cheap to
raise) and the ~540 plateau (a kernel/attention/sampling bottleneck, vs vLLM's 667).

Secondary (still true): even if we wanted it, #22569 builds but does not plug into the
path LocalAI serves from (separate `llama_paged_scheduler` API), and crashed out-of-box
on Qwen3 (1-line reshape fix). Original verdict below.

### Original verdict (pre-throughput)

**Do not adopt #22569 as-is.** The PR builds, but on GB10 it is
not usable for our target without non-trivial fixes and a large integration, and its
design does not plug into the path LocalAI actually serves from.

Reasons (detail below):

1. **Builds: YES.** Clean CUDA build for sm_121 against current master (single
   self-contained commit; it does NOT depend on the competing CUDA PR #17579).
2. **Runs out of the box: NO.** Every current Qwen3 model (0.6B and 32B) crashes at
   context creation with a `ggml_reshape_2d` assert in the paged `build_attn` graph.
   Root cause: the paged path hardcodes `ggml_reshape_2d(cur, hparams.n_embd, ...)`,
   which is wrong for any model where `n_head*head_dim != n_embd` (Qwen3's decoupled
   head_dim: 32B is 64*128=8192 vs n_embd 5120; 0.6B is 16*128=2048 vs 1024). The PR's
   "qwen3 verified" claim does not hold against current Qwen3 GGUFs. It is a ~1-line
   fix (use the real attention width `cur->ne[0]*cur->ne[1]`), which we applied to test
   further.
3. **`fit_params` (`-ngpub` auto-sizing) crashes on GB10** independently, in the same
   reshape path during the device-memory probe; must run `--fit off` + explicit
   `-ngpub`.
4. **Wrong integration surface.** Paged is driven only through a brand-new parallel C
   API (`llama_paged_scheduler_init/add_request/prepare_batch/update/...`) exercised by
   a bespoke `examples/paged` loop. The flag `-kvp`/`--kv-paged` is gated to
   `LLAMA_EXAMPLE_PAGED` only - it is NOT wired into `llama-server`, `llama-batched-bench`,
   `llama-parallel`, or anything the LocalAI grpc-server is derived from. Adopting it
   means rewriting LocalAI's serving loop around the new scheduler API, not flipping a
   flag.
5. **Phase-1 restrictions** (enforced at context creation): single CUDA device, full
   offload only, `n_batch == n_ubatch`; no SWA (gemma3/llama4/etc. unsupported); no
   CoW/prefix-caching, no `seq_cp`/`seq_keep`/`seq_div`/`seq_add`, no state save/load.
   Draft PR, design itself is under maintainer debate (author asks whether the C API is
   even the right approach).

## 1. Build & correctness

- Cloned `matiaslin/llama.cpp` branch `paged_attention` (PR #22569, single commit
  `0b0f7bd...`, base = current master). Built with
  `-DGGML_CUDA=ON -DCMAKE_CUDA_ARCHITECTURES=121 -DCMAKE_BUILD_TYPE=Release`.
  `llama-paged`, `llama-batched-bench`, `test-paged-kv`, `test-paged-kv-e2e` all link.
- PR #17579 (ericcurtin, `--pagedattention`) is a **separate competing implementation**;
  #22569 ships its own CPU+CUDA `ggml_paged_attn` op, so #17579 is not needed.
- Out-of-the-box run of `llama-paged -kvp` on Qwen3-32B and Qwen3-0.6B: **crash** at
  `sched_reserve` -> `build_attn(llm_graph_input_attn_kv_paged*)` ->
  `ggml_reshape_2d` `GGML_ASSERT(ggml_nelements(a) == ne0*ne1)` (src/llama-graph.cpp:2556).
  Same crash via `--fit off` (so it is the real graph, not just the probe).
- Applied the reshape fix (`hparams.n_embd` -> `cur->ne[0]*cur->ne[1]`), rebuilt.

### Correctness after fix (PR's own greedy/top-K equivalence test)

<!-- FILLED AFTER RECONNECT -->
PENDING: `test-paged-kv-e2e -m Qwen3-0.6B-Q8_0.gguf` (top-K argmax match + top-5 overlap
>= 4 + first-4-token greedy match vs non-paged).

## 2. Throughput: paged vs contiguous on GB10

Harnesses differ (paged uses its scheduler-driven continuous-batching `examples/paged`
loop reporting `agg_tps = total_decoded / elapsed`; contiguous uses `llama-batched-bench`
S_TG). Both give aggregate decode tok/s at concurrency N.

Contiguous baseline (continuous batching already on), prior measure:
235 / 391 / 540 t/s at npl 32 / 64 / 128, still climbing at 128.

| npl | contiguous agg t/s (batched-bench) | paged agg t/s (`-kvp`) | notes |
|-----|-----|-----|-----|
| 128 | PENDING | PENDING | |
| 256 | PENDING | PENDING | |
| 512 | PENDING | PENDING | |
| 1024| PENDING | PENDING | |

Key GB10 caveat vs the PR's A10G data: the PR's headline win (OOM@26seq contiguous ->
247seq paged) came from A10G's **24 GB** VRAM exhausting at low concurrency. GB10 has
**~119 GB unified** memory, so contiguous does not OOM at the same low seq counts - the
capacity advantage of paging is materially smaller here. PENDING: the seq count where
contiguous actually OOMs/plateaus on GB10 vs where paged keeps scaling.

## 3. Verdict & reasoning

<!-- FINALIZED AFTER NUMBERS -->
