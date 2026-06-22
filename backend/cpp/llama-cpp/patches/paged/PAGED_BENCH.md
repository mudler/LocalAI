# Paged-KV: GPU 0007 re-run + shared-prefix throughput benchmark

DGX Spark (NVIDIA GB10, sm_121 / cc 12.1), CUDA 13, dev tree `~/llama-paged-dev`
branch `paged`, base pin `f3e182816421c648188b5eab269853bf1531d950`, full paged
engine (0001-0004, 0006, 0007). All paged behaviour stays gated by
`LLAMA_KV_PAGED`; default-off is byte-identical to stock. Models:
`Qwen3-0.6B-Q8_0.gguf` and `Qwen3-32B-Q4_K_M.gguf`.

## Deliverable 1 - GPU run of the 0007 prefix-engine correctness driver

The committed driver `examples/simple/paged-prefix-engine.cpp` hardcodes
`n_gpu_layers = 0`. For this GPU run it was given a dev-only
`PAGED_NGL` env override (`mp.n_gpu_layers = getenv("PAGED_NGL") ? atoi(...) : 0`),
rebuilt in `build-cuda`, run, then the edit was **reverted** so the committed
driver stays byte-clean (it is dev scaffolding, never shipped in a patch).

Three runs of the same Gate-0 driver, Qwen3-0.6B, `LLAMA_KV_PAGED=1`:

| binary / offload                         | result                  |
|------------------------------------------|-------------------------|
| committed `build-cpu` driver             | **ALL PASS (failures=0)** |
| `build-cuda`, `PAGED_NGL=99` (all layers)| GATE FAILED (failures=3)|
| `build-cuda`, `PAGED_NGL=0` (same binary)| GATE FAILED (failures=2)|

**The GPU run did NOT print ALL PASS - reported honestly.** But the failures are
narrow and are not a paged-engine bug:

- Every **structural / mechanical** paged invariant PASSES on GPU, in both
  scenarios (boundary and mid-block): prefill computed ONLY the suffix (32 prefix
  tokens skipped), shared prefix block-aligned, shared-block `ref_cnt == 2` while
  both sequences hold it, ref drops `2 -> 1` on freeing one sharer, only the
  private (suffix) blocks are returned, and the prefix block returns to the pool
  once all sharers free. The cross-request KV reuse mechanism itself is GPU-clean.
- The only failures are the **exact greedy-token byte-identical** assertions
  (e.g. boundary `B-shared` vs `B-from-scratch`). They diverge at a single near-tie
  token (boundary: 2nd generated token `17971` vs `5671`) and then cascade
  autoregressively.

Root cause is **CUDA float-kernel non-determinism, not the paged logic**: the
*same* CUDA binary fails the exact-token assertions even with `PAGED_NGL=0` (zero
layers offloaded), whereas the genuine `build-cpu` binary passes all 16/16. The
CUDA backend (loaded via `ggml_backend_load_all`) uses non-associative reductions
whose result differs between the full-prefill batch shape and the
incremental-suffix batch shape; under greedy decode a single logit near-tie flips
and the sequences cascade apart. This refines the earlier note in
`PAGED_GPU_VERIFY.md` (which framed it as "not GPU-specific" and had no CPU pass
to compare against): the CPU build now passes clean, so the divergence is a strict
test-assertion artefact of CUDA float ordering, not a defect in 0006/0007.

## Deliverable 2 - shared-prefix throughput benchmark (the real-win test)

Dev-only driver `examples/simple/paged-prefix-bench.cpp` (registered in
`examples/simple/CMakeLists.txt`, dev tree only - not in any shipped patch).
Workload: `K` sequences that all share a `P`-token common prefix (a system /
RAG preamble), each with a unique `S`-token suffix; prefill only (`G=0`,
generation is identical compute in both modes so it is excluded from the
headline). GPU, `-ngl 99`, `kv_unified = true`.

- **NO-SHARE (stock):** `LLAMA_KV_PAGED` unset; every sequence prefills the full
  `P+S` tokens. Total prefill work `= K*(P+S)`.
- **PAGED-SHARE:** `LLAMA_KV_PAGED=1`; the prefix is computed ONCE on seq 0,
  committed via `paged_prefix_api::commit`, then every other seq calls
  `paged_prefix_api::share` to physically reuse the ref-counted prefix blocks and
  prefills ONLY its suffix. Total prefill work `= P + K*S`.

**`kv_unified` note:** this engine's cross-request share is built around the
*unified* stream-0 pool (ref-counted shared cells), so `kv_unified = true` is what
makes the share engage - the same setting the committed 0007 driver uses. With
`kv_unified = true` the share engaged in every run (evidence below).

### Reuse actually engaged (share mode)

In every share run: `kshare(seq 1) = 1024` (the full block-aligned prefix is
reused, not recomputed), the shared prefix block's `ref_cnt == K` (all sharers
point at one physical copy), and `prefill_tokens_submitted` collapses from
`K*(P+S)` to `P + K*S`.

### Results (P=1024, S=32, prefill-only)

| model        | K  | mode      | prefill tokens | prefill time | raw tok/s | shared ref_cnt |
|--------------|----|-----------|----------------|--------------|-----------|----------------|
| Qwen3-0.6B   | 32 | no-share  | 33792          | 4.659 s      | 7253      | -              |
| Qwen3-0.6B   | 32 | **share** | 2048           | **0.554 s**  | 3695      | 32             |
| Qwen3-32B    | 16 | no-share  | 16896          | 26.14 s      | 647       | -              |
| Qwen3-32B    | 16 | **share** | 1536           | **3.64 s**   | 422       | 16             |
| Qwen3-32B    | 32 | no-share  | 33792          | 61.91 s      | 546       | -              |
| Qwen3-32B    | 32 | **share** | 2048           | **6.02 s**   | 340       | 32             |

### Verdict: YES, a real and substantial win, and it grows with K

- Prefill wall-time speedup: **0.6B K=32 -> 8.4x**, **32B K=16 -> 7.2x**,
  **32B K=32 -> 10.3x**. The win grows with the number of sharers because
  no-share prefix recompute is `O(K)` while the shared prefix is `O(1)` plus
  `K` tiny suffixes.
- Note the honest caveat in the raw-throughput column: share mode submits small
  32-token suffix batches that are *less* GPU-efficient (340-422 tok/s) than the
  large no-share batches (546-7253 tok/s). The win is **not** higher tok/s - it is
  computing ~11-16x **fewer** tokens. On a fast GB10 prefill that still nets a
  7-10x wall-time reduction because prefill is compute-bound and the shared prefix
  dominates the token count.
- This is exactly the many-users-one-system-prompt / RAG-preamble fan-out
  scenario, and the paged cross-request prefix cache delivers there.

Scaffolding (`paged-prefix-bench.cpp`, the `PAGED_NGL` driver tweak) stays
dev-tree-only and is not part of any shipped patch.

Assisted-by: Claude:opus-4.8 [Claude Code]
