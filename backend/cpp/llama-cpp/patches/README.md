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
| 0001 | `0001-vendor-paged-kv-manager.patch` | Add `src/paged-kv-manager.{h,cpp}` (vLLM-parity block manager, CPU foundation) + CMake; no behavior change | builds; unit-tested separately under `../paged/` |
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
git am /path/to/backend/cpp/llama-cpp/patches/00*.patch     # or `git apply` + commit per patch

# 3. iterate a phase as ONE commit, then export the whole series 1:1
git format-patch <LLAMA_VERSION>..paged -o /path/to/backend/cpp/llama-cpp/patches/ --zero-commit -N

# 4. on a pin bump: rebase `paged` onto the new pin; only conflicting patches need edits; re-export.
```

## Build integration

`../Makefile`'s `llama.cpp:` target runs, after `git checkout -b build $(LLAMA_VERSION)`:
```
for p in $(CURRENT_MAKEFILE_DIR)/patches/0*.patch; do git apply --verbose "$p"; done
```
All variants (avx/avx2/avx512/cuda/…) copy the patched `llama.cpp/` tree, so the series ships everywhere.

## Status

- **0001 vendor manager — DONE.** Applies clean to the pin; builds into `libllama`.
- **0002 block placement — DONE + VERIFIED.** Built `llama-simple` at the pin; greedy generation is
  **token-identical** stock vs `LLAMA_KV_PAGED=1` (Qwen3-0.6B), paged branch confirmed firing.
- **0003 gather-read — NEXT.** The intricate `build_attn` graph surgery; the real engine compute. Multi-session.
- 0004–0006 follow.

### Honest parity note (important)

This series delivers the paged-attention **engine** (capacity + scheduling + prefix sharing). It does **not**
by itself reach vLLM throughput parity, because the measured prefill bottleneck is the **FP4 MoE GEMM kernel**
(Lever 3: `mul_mat_q<MXFP4>` ~22 TFLOP/s, ~27× behind vLLM) — a *per-token compute* gap that paging does not
touch. Paged attention closes the **concurrency/memory** gap (more sequences, prefix reuse); the prefill/throughput
gap additionally needs the tcgen05/CUTLASS grouped-GEMM (deferred, upstream-grade, no shortcut — see
`../paged/UPSTREAM_GGML_ISSUE.md` and `DGX_BLACKWELL_PLAN.md`). So full vLLM parity = this series **AND** the
kernel; neither alone suffices.
