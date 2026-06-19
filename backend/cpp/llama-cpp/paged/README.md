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
| **P2/P3 (in-model)** | **`build_attn_paged` in llama-graph.cpp + Gate 0 (token-identical generation) + win-2 throughput** | ⛔ **NOT DONE** — large in-tree effort |

The design's central risk — *does gather-to-scratch produce correct attention?* — is
**retired**: paged, non-contiguous KV through the existing ggml attention ops is
bit-accurate. What remains is wiring that into the model's graph and proving
token-identical generation on a real GGUF, then measuring tok/s vs concurrency.

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
