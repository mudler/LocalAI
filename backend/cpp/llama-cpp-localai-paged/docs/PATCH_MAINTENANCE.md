# llama.cpp patch series — paged attention (vLLM-parity engine)

A **stacking** series: each patch is a small, self-contained, independently-buildable step toward an
in-model paged-attention engine. They apply in numeric order on top of the pinned `LLAMA_VERSION`
(`backend/cpp/llama-cpp/Makefile`). The build applies them automatically after checkout (see the
`llama.cpp:` target). Keeping the work as ordered patches — rather than one big diff — is what lets us
**rebase cleanly across llama.cpp bumps and avoid drift**: when a patch stops applying, only that small
patch needs fixing, and the failure points at exactly which step the upstream change touched.

## Base

- `LLAMA_VERSION` pin in `../Makefile`. **All patches are generated against that exact commit.** Bumping
  the pin = re-run the regen workflow below and fix only the patches that no longer apply.

## The series (phases → patches)

| # | Patch | What | Verifies |
|---|-------|------|----------|
| 0001 | `0001-vendor-paged-kv-manager.patch` | Add `src/paged-kv-manager.{h,cpp}` (vLLM-parity block manager, CPU foundation) + CMake; no behavior change | builds; unit-tested separately |
| 0002 | `0002-paged-kv-storage.patch` | Shared block-pool KV tensor + `set_rows`-by-slot writes, behind `LLAMA_KV_PAGED` | builds; write/gather round-trip |
| 0003 | `0003-paged-gather-read.patch` | `build_attn_paged` gather-read in `llama-graph.cpp` | **Gate 0**: token-identical greedy gen, single + multi-seq |
| 0004 | `0004-paged-ondemand-alloc.patch` | On-demand block allocation via PagedKVManager | max concurrent seqs before OOM |
| 0005 | `0005-paged-continuous-batching.patch` | Block-granular admit/evict in the server slot path | tok/s vs concurrency, mixed-length |
| 0006 | `0006-paged-prefix-caching.patch` | Block-hash cross-request prefix dedup | TTFT + memory on shared prefixes |

Each row is a separate `git commit` on the dev branch (below), exported 1:1 as a patch. Default off
(`LLAMA_KV_PAGED`) until Gate 0 (0003) is green, so partial series never changes stock behavior.

## Regen workflow (the anti-drift recipe)

```sh
# 1. check out the exact pin into a dev tree
git -C /tmp clone https://github.com/ggml-org/llama.cpp llama-dev && cd /tmp/llama-dev
git checkout <LLAMA_VERSION from ../Makefile>
git checkout -b paged

# 2. apply the current series (each becomes a commit), or develop the next patch
git am /path/to/backend/cpp/llama-cpp-localai-paged/patches/paged/00*.patch     # or `git apply` + commit per patch

# 3. iterate a phase as ONE commit, then export the whole series 1:1
git format-patch <LLAMA_VERSION>..paged -o /path/to/backend/cpp/llama-cpp-localai-paged/patches/paged/ --zero-commit -N

# 4. on a pin bump: rebase `paged` onto the new pin; only conflicting patches need edits; re-export.
```

## Build integration

The series is owned by this backend (`backend/cpp/llama-cpp-localai-paged`), not by the stock
`llama-cpp` backend, which is pure upstream. `../Makefile` (the paged wrapper) clones the pinned
`llama.cpp` via the copied stock build infra, then applies this series onto the cloned tree with the
same strict `git apply` the stock build uses for base patches:
```
for p in $(PAGED_PATCHES_DIR)/0*.patch; do git apply --verbose "$p" || exit 1; done
```
All variants (avx/avx2/avx512/cuda/…) clone + apply into their own build copy, so the series ships
everywhere without ever touching the stock `llama-cpp` source tree.

## Latest mirror check

Phase 32 re-verified the mirror invariant after adding patch `0058`:

```text
base=0ed235ea2c17a19fc8238668653946721ed136fd
applied_tree=de1bdd1892ab87aee947ec19c5efed8f53b93d40
fork_tree=de1bdd1892ab87aee947ec19c5efed8f53b93d40
```

The check used a fresh worktree at `LLAMA_VERSION`, applied every
`patches/paged/0*.patch` with strict `git apply`, staged the result, and compared
`git write-tree` to canonical fork branch `localai-paged` at
`2a9964d29 feat(cuda): trace moe small-m mmq candidates`.

## Status

- **0001 vendor manager — DONE.** Applies clean to the pin; builds into `libllama`.
- **0002 block placement — DONE + VERIFIED.** Built `llama-simple` at the pin; greedy generation is
  **token-identical** stock vs `LLAMA_KV_PAGED=1` (Qwen3-0.6B), paged branch confirmed firing.
- **0003 gather-read — DONE + VERIFIED (Gate 0 green).** Implemented in the **additive** form
  (see `../README.md`): all logic in new `src/paged-attn.{h,cpp}` (a `llm_graph_input_i` gather-index
  subclass + the K/V/mask gather), hooked by **one** line in `build_attn` + **two** thin accessors on
  `llama_kv_cache_context` + 1 CMake line (216 insertions; no edit to `llm_graph_input_attn_kv` or
  `llama-graph.h`). Greedy generation is **token-identical** stock vs `LLAMA_KV_PAGED=1` (Qwen3-0.6B,
  **9/9** across 3 prompts × {32,96,128} tokens), with `n_gather=71 < n_kv=256` confirming real
  compaction. Patch: `0003-paged-gather-read-env-LLAMA_KV_PAGED.patch`.
  - **Key correctness finding:** `get_gather_idxs` must emit cells **sorted by token position**. The CPU
    flash-attn online softmax reduces cells in physical-array order and is FP-order-sensitive, so 0002's
    scattered placement *alone* (full-window read, no gather) diverges from stock once a sequence crosses
    the first 16-cell block. The position-sorted gather reproduces stock's exact reduction order -> bit-
    identical, not merely mathematically equivalent. So 0002 is the placement substrate; **0003 is what
    makes paged placement token-identical under flash-attn.**
- 0004–0006 follow.

### Honest parity note (important)

This series delivers the paged-attention **engine** (capacity + scheduling + prefix sharing). It does **not**
by itself reach vLLM throughput parity, because the measured prefill bottleneck is the **FP4 MoE GEMM kernel**
(Lever 3: `mul_mat_q<MXFP4>` ~22 TFLOP/s, ~27× behind vLLM) — a *per-token compute* gap that paging does not
touch. Paged attention closes the **concurrency/memory** gap (more sequences, prefix reuse); the prefill/throughput
gap additionally needs the tcgen05/CUTLASS grouped-GEMM (deferred, upstream-grade, no shortcut — see
`../README.md`). So full vLLM parity = this series **AND** the
kernel; neither alone suffices.
