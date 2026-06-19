# Paged Attention for llama.cpp (vLLM-parity), CPU-first

A from-scratch port of vLLM V1's paged KV-cache model into the llama.cpp / ggml
world, built CPU-first and verified incrementally. The host-side block manager is
a faithful port of vLLM; the compute stays in ggml (no new op — the read path
gathers blocks with `ggml_get_rows` and feeds the existing attention ops).

Design: `docs/superpowers/specs/2026-06-19-paged-attention-llamacpp-design.md`
Plan:   `docs/superpowers/plans/2026-06-19-paged-attention-llamacpp.md`

## Status

| Phase | What | State |
|------|------|-------|
| P0 | vLLM-parity host block manager (`FreeBlockQueue`, `BlockPool`, `PagedKVManager`, chained-hash prefix cache) | ✅ verified — `make check`, 4/4 suites |
| P1 | ggml paged write/gather mechanism (`set_rows` by slot_mapping → `get_rows` gather) | ✅ verified — `make ggml-check`, non-contiguous blocks `[2,1,5]` round-trip + isolation |
| P2 (core) | attention over gathered paged KV matches independent host reference | ✅ verified — max abs err **7.5e-08** |
| P3 (partial) | capacity & prefix-sharing wins | ✅ measured — `make bench`: **9.2×** more concurrent seqs, **11.3×** less KV memory |
| **P3 (in-model placement)** | **paged, non-contiguous block KV placement in the real model** | ✅ **Gate 0 PASSED** — Qwen3-0.6B token-identical (`patches/0001-paged-kv-block-placement.patch`) |
| P4 (in-model compute) | gather-read (`build_attn_paged`, read only a seq's blocks) + win-2 throughput + multi-seq | ⛔ remaining |

The design's central risk — *does paged (non-contiguous) KV produce correct attention?* —
is **retired at two levels**: (1) at the ggml-op level (P2, 7.5e-08 vs reference) and
(2) **in a real model** (P3): with KV physically scattered across permuted, non-contiguous
blocks (cells `0-15, 144-159, 32-47, …`), Qwen3-0.6B greedy generation is **token-for-token
identical** to the contiguous cache. Reproduce:

```sh
# from backend/cpp/llama-cpp-fallback-build/llama.cpp (patch applied, CPU build)
B=build-cpu/bin/llama-simple; M=<Qwen3-0.6B.Q4_K_M.gguf>; P="...long prompt..."
"$B" -m "$M" -n 40 "$P"                         > base.txt
LLAMA_KV_PAGED=1 "$B" -m "$M" -n 40 "$P"        > paged.txt
diff base.txt paged.txt && echo TOKEN-IDENTICAL
# LLAMA_KV_PAGED_DEBUG=1 prints the permuted physical cells per step
```

This proves the **storage/placement** layer of paged attention in-model. What remains (P4)
is the **compute** optimization that yields the throughput win: a gather-read that attends
only a sequence's own blocks (instead of scanning `[0,n_kv)` with a mask), plus the
multi-sequence driver to measure tok/s vs concurrency. The patch is single-sequence scope.

## Build & test

```sh
make check                     # P0 host-manager unit suites (pure C++, no deps)
make ggml-check GGML_SRC=<llama.cpp>/ggml GGML_BUILD=<ggml-build>   # P1/P2 ggml tests
make bench                     # P3 capacity + prefix-sharing numbers
```

`ggml-check` needs a built ggml. To build one CPU-only from a llama.cpp checkout:
`cmake -S <llama.cpp>/ggml -B /tmp/ggml-build -DGGML_CUDA=OFF -DCMAKE_BUILD_TYPE=Release && cmake --build /tmp/ggml-build -j`
(if it complains about a missing `ggml.pc.in`, add a minimal pkg-config stub).

## Files

- `paged_kv_manager.{h,cpp}` — the vLLM-parity block manager (no ggml/llama dep).
- `tests/test_free_block_queue.cpp` — intrusive LRU free list.
- `tests/test_block_pool.cpp` — alloc/touch/free/evict/cache.
- `tests/test_paged_kv_manager.cpp` — allocate/block_table/slot_mapping/free.
- `tests/test_prefix_cache.cpp` — chained block hashing + first-miss cache hit.
- `tests/test_ggml_paged_rw.cpp` — paged write/gather through real ggml ops.
- `tests/test_ggml_paged_attn.cpp` — attention over paged KV vs host reference.
- `paged-bench.cpp` — capacity (win 1) + prefix-sharing (win 3) measurements.

## Remaining work — integration map (for the next session)

Target: a paged read path active behind a flag, producing **token-identical** greedy
output vs the contiguous cache on a real model (Gate 0), then `paged-bench` win 2.

Exact seams in the vendored llama.cpp (`backend/cpp/llama-cpp-fallback-build/llama.cpp`,
the pinned build fetches `LLAMA_VERSION=f3e182816421…`):

1. **Memory type** — `src/llama-model.cpp:2070` `create_memory()` constructs `llama_kv_cache`.
   Add a paged variant (or a flag on the existing cache) implementing `llama_memory_i`
   (`src/llama-memory.h`), backed by `PagedKVManager`.
2. **Allocation** — `src/llama-kv-cache.cpp:818` `find_slot()` produces `slot_info.idxs`.
   Replace the ring-buffer scan with block-aligned allocation from `PagedKVManager`.
3. **Read path** — `src/llama-kv-cache.cpp:1145/1165` `get_k`/`get_v` return a contiguous
   `[0,n_kv)` view. For paged, gather the sequence's blocks (`ggml_get_rows`) into scratch.
   The new branch lives alongside `build_attn` in `src/llama-graph.cpp` (`build_attn_mha`).
4. **Mask** — `src/llama-graph.cpp` `build_attn_inp_kq_mask` sizes the mask to the gathered
   length per sequence.
5. **Gate 0 driver** — `build-cpu/bin/llama-simple` (greedy argmax) on
   `Qwen3-0.6B.Q4_K_M.gguf`; assert paged output == contiguous output token-for-token.

### Honest caveats (from the maintainer discussion + reading `find_slot`)

- llama.cpp's **unified cache already shares one KV pool** across sequences and already
  tolerates non-contiguous slots. So win-1 vs *unified* is smaller than vs per-seq
  reservation (stream mode). The durable LocalAI wins are **on-demand sizing** and
  **automatic cross-tenant prefix sharing** (P0 implements the block-hash machinery).
- vLLM's classic `paged_attention_v1/v2` CUDA kernel is **deprecated**; the live path is
  FlashAttention/FlashInfer over a block table. The port targets that pattern, not the
  old kernel. Upstream draft PRs #22569 (new `ggml_paged_attn` op) and #17579 (CUDA) are
  unmerged; maintainers are skeptical for single-user use.
