# PAGED_POOL_BURST_FIX (patch 0024)

Fixes the paged-pool **burst-degradation bug** identified in `OTHER_PATHS_INVESTIGATION.md`
(section C, Part 2): on a long-lived `llama-server` with `LLAMA_KV_PAGED=1`, a high-fan-out
prefill burst strands KV blocks in the host-side paged pool, so a subsequent lower-npl prefill
draws from a depleted / fragmented pool and its throughput collapses (the benchmark's documented
"restart the server per npl" crutch). Decode is unaffected. The fix touches **only host-side block
accounting and placement - never KV values or compute** - so it is gated behind `LLAMA_KV_PAGED`
and is byte-identical to HEAD with the flag unset.

## Root cause (two compounding host-side defects)

1. **Reclamation gap.** `paged_alloc` returned a sequence's blocks only on a full-range wipe
   (`seq_rm(seq, 0, MAX)`). A partial **tail** truncation `seq_rm(seq, p0>0, MAX)` - which
   `llama-server` issues on every reused slot and before a cross-request prefix splice - freed the
   kv-cache CELLS but left the manager owning the trailing BLOCKS. The two desync; the free pool
   shrinks. (Applies to pure-attention paged caches; on hybrid SSM models the partial seq_rm is
   rejected by the recurrent cache before it reaches the attention cache, so the dominant leak there
   is #1b below.)
1b. **Idle-slot retention.** Stock `llama-server` keeps a finished slot's KV resident for that
   slot's own next-prompt cache. Under the paged engine, the blocks of the many slots a burst
   touches but a later low-npl run never reassigns are stranded for the process lifetime - a later
   run sees a depleted pool.
2. **No compaction.** `BlockPool::free_blocks` returns blocks in free order; after a burst the free
   queue is a scrambled permutation of physical ids, so a later prefill pops physically scattered
   blocks and its KV scatter-write + paged-attention gather lose locality.

## The fix (all behind `LLAMA_KV_PAGED`; `LLAMA_PAGED_NO_RECLAIM=1` restores pre-fix behavior)

- **Fix-1 - reclaim trailing blocks.** `paged::PagedKVManager::truncate(seq, n_keep)` frees every
  block at index >= `ceil(n_keep/bs)` (ref-counted, mirroring vLLM's free of a truncated suffix),
  exposed as `paged_alloc::truncate(cache, stream, seq, n_keep)` and called from
  `llama_kv_cache::seq_rm` for the `p1 == MAX && p0 > 0` case. Manager accounting now tracks the
  kv-cache exactly. (`src/paged-kv-manager.*`, `src/paged-alloc.*`, `src/llama-kv-cache.cpp`)
- **Fix-2 - defrag on empty.** When the pool becomes fully idle (`all_free()`),
  `defrag_free_pool()` relinks the free queue into ascending block-id order (`FreeBlockQueue::rebuild`),
  preserving content-cache hashes. Triggered after `release`/`truncate`. (`src/paged-kv-manager.*`,
  `src/paged-alloc.*`)
- **Fix-3 - release on slot completion.** At `server_slot::release()` the paged engine issues
  `prompt_clear()` (full seq_rm: clears cells AND releases+defrags the blocks) and drops the
  slot-local prompt cache, so a finished-idle slot returns its blocks promptly; cross-request reuse
  still works through the committed paged content cache. (`tools/server/server-context.cpp`)

## Validation (DGX GB10, dense q36-27b-nvfp4 = qwen35 hybrid; HEAD f7409c2 = patch 0023)

### Bit-exactness (the parity-safe property)
Greedy decode, fixed prompt/seed, 48 tokens, `llama-completion`:

| build / flag | md5 |
|---|---|
| 0023 baseline (paged off) | `5951a5b4d624ce891e22ab5fca9bc439` |
| AFTER paged **off** | `5951a5b4d624ce891e22ab5fca9bc439` (== baseline) |
| AFTER paged **on**, reclaim default-on | `5951a5b4d624ce891e22ab5fca9bc439` (== baseline) |
| AFTER paged **on**, `LLAMA_PAGED_NO_RECLAIM=1` | `5951a5b4d624ce891e22ab5fca9bc439` (== baseline) |

Identical across the board: the fix changes no KV value or compute. `test-backend-ops` is unaffected
by construction (the change touches only host-side block accounting in libllama and the server; no
ggml operator is modified) and was re-run green against the fixed `libllama`.

### Host-side unit test (`llama-paged-reclaim-unit`, no GPU)
- Fix-1: `allocate(0,512)` -> 32 blocks; `truncate(0,256)` reclaims exactly **16** trailing blocks;
  `truncate(0,16)` returns to 1 block; `free` returns to pristine.
- Fix-2: 8 blocks freed in scrambled order then `defrag_free_pool()` -> next `block_table` pops
  **ascending** physical ids. `UNIT PASS`.

### Repro on the model (`llama-paged-burst-bench`, A/B on one binary via `LLAMA_PAGED_NO_RECLAIM`)
NSLOT=64, NPL=8, PP=512, pool=2527 blocks. Same binary, A/B by env.

- **Fix-2 (fragmentation -> prefill).** Fresh npl8 vs npl8 after a scrambling burst+drain:
  - BEFORE (`NO_RECLAIM`): prefill 870.5 -> 822.1 t/s, **ratio 0.944** (fragmented free queue).
  - AFTER (defrag on):     prefill 869.2 -> 867.8 t/s, **ratio 0.998** (free queue compacted).
- **Fix-3 mechanism (idle-slot leak -> reclaim).** Burst 64 sequences left idle, then full-release
  (what Fix-3's `prompt_clear` issues at `slot.release()`): pool free
  **2527 (pristine) -> 479 (64 idle slots strand 2048 blocks) -> 2527 (reclaimed == fresh)**. The
  leaked-block count is exactly 64 x ceil(512/16) = 2048.
- Decode is untouched throughout (single-token append; the fix only moves/accounts blocks).

### Server repro (`llama-server`, one long-lived process, FRESH-npl8 -> BURST-npl64 -> POST-npl8)
`-c 36000 -np 64 -b 2048 -ub 512`, `LLAMA_MAX_BATCH_TOKENS=512`, distinct 512-token prompts,
`cache_prompt:false`, A/B by `LLAMA_PAGED_NO_RECLAIM`. Aggregate prefill = total prompt tokens / wave
wall.

| wave | BEFORE (`NO_RECLAIM`) | AFTER (fix) |
|---|---|---|
| FRESH-npl8 | 488 t/s (wall 8.4 s) | 525 t/s (wall 7.8 s) |
| POST-npl8 (after burst) | **44 t/s (wall 93 s)** | **532 t/s (wall 7.7 s)** |
| post / fresh | **0.090 (11x collapse)** | **1.01 (recovered, within 1%)** |
| paged release lines in log | 17 | **96** (Fix-3 fires at each slot completion) |
| `CANARY_TOKENS_MATCH` (fresh vs post, identical prompts) | **YES** | **YES** |

The bug reproduces exactly (the investigation's 507 -> 65 collapse; here 488 -> 44); the fix restores
POST-npl8 to within ~1% of fresh and the release-log count jumps from 17 to 96, confirming Fix-3
returns each finished slot's blocks. The canary tokens are identical fresh-vs-post in BOTH arms:
paged placement is value-invariant, so the fix never changes the served output - only when the pool
recovers. Decode is structurally untouched (release happens after a request completes); greedy md5
above proves decode values are byte-identical.

## Tradeoff / scope notes
- On **hybrid SSM models** (qwen35), the recurrent cache rejects a partial tail `seq_rm`, so the
  hybrid wrapper never forwards it to the attention cache: Fix-1 effectively applies to
  pure-attention paged caches, while the hybrid leak is dominated by idle-slot retention (Fix-3) and
  fragmentation (Fix-2). Confirmed by the unit test (Fix-1 logic) and Test-C (2048 blocks stranded
  by 64 idle slots, returned to fresh on reclaim).
- Fix-3 clears a finished slot's KV at `release()`, so a repeated-prompt workload loses the
  slot-local prompt cache. Cross-request reuse normally falls back to the committed paged content
  cache, but that publish path (`paged_prefix_api::commit`) is itself a no-op on hybrid wrappers, so
  for hybrid + repeated prompts Fix-3 trades prompt-cache reuse for pool hygiene. Gated behind
  `LLAMA_KV_PAGED`; `LLAMA_PAGED_NO_RECLAIM=1` restores the stock retain-idle behavior.

## Files
- `src/paged-kv-manager.{h,cpp}` - `truncate`, `defrag_free_pool`/`defrag_free_queue`,
  `FreeBlockQueue::rebuild`, `all_free`/`total_blocks`.
- `src/paged-alloc.{h,cpp}` - `truncate`, `reclaim_active`, defrag-on-empty in `release`/`truncate`,
  `num_free_global`/`num_managers`.
- `src/llama-kv-cache.cpp` - partial-tail-seq_rm reclaim hook.
- `src/paged-prefix-api.{h,cpp}` - `num_free_global`/`num_managers` introspection passthrough.
- `tools/server/server-context.cpp` - Fix-3 paged release at `slot.release()`.
- `examples/simple/paged-reclaim-unit.cpp`, `paged-burst-bench.cpp` - dev test scaffolding.

Assisted-by: Claude:opus-4.8 [Claude Code]
