# Additive layout for the paged-KV patch series - "hook, don't edit"

Goal: ship paged KV as a vendored patch series that **survives llama.cpp pin bumps with
minimal rebase pain**. PR #22569 (the upstream draft) was rejected by maintainers as
"slop" and is far too invasive to vendor - it rewrites core attention. Our series must be
the opposite: **additive**. This document is the design rule and the per-patch core-touch
budget.

## The rule

> Every change is either (a) **new code in a new vendored file** under `src/`, or (b) a
> **single, env-gated hook** at one call site in a core file that delegates to the new
> file. No logic lives in a core file. No core struct/signature is edited.

Why it works: a hook is a 1-3 line diff against a core file. When upstream churns that file,
`git apply` either still lands the hook (context unchanged) or fails *only on that tiny
hunk*, which is trivial to re-place. Logic embedded inside a core function (the PR #22569 /
old-0003 approach) conflicts on every bump and must be re-understood each time.

This is enforceable as a **core-touch budget**: each patch declares the core files it
touches and the line count; review rejects anything that grows logic in core.

## Why it's achievable here (grounded in the pinned source)

The two seams paged KV needs are both already abstract in llama.cpp at the pin
(`LLAMA_VERSION=f3e1828`), so new behavior plugs in without editing core types:

- **KV placement** - `llama_kv_cache::find_slot` already returns a `slot_info` of physical
  cell indices. Paged placement is just *different indices*. 0002 already does this as one
  gated block (`if (paged_mode) { ... continue; }`, 41 lines, one file). Ideal.
- **Graph inputs** - `llm_graph_input_i` is a pure-virtual base (`set_input()`), and
  `llm_graph_result::add_input(llm_graph_input_ptr)` lets *any* code register a new input
  subclass. So a paged graph input (the gather index) can be **a new class in a new file**,
  added from a one-line hook - no edit to `llm_graph_input_attn_kv` or `llama-graph.h`.

## Per-patch core-touch budget

| # | Patch | New files (additive) | Core hooks (gated, minimal) | Core lines |
|---|-------|----------------------|------------------------------|-----------:|
| 0001 | vendor manager | `paged-kv-manager.{h,cpp}` | `CMakeLists.txt` +1 | 1 |
| 0002 | block placement | - | one `if(paged_mode){...continue;}` in `find_slot` | ~41 |
| 0003 | gather-read | `paged-attn.{h,cpp}` | `CMakeLists.txt` +1; **one** hook in `build_attn`; 2 tiny accessors on `llama_kv_cache_context` | ~8 |
| 0004 | on-demand alloc | (uses 0001 manager) | one branch in `find_slot` calling the manager | ~10 |
| 0005 | continuous batching | - | **LocalAI `grpc-server.cpp`** (already a LocalAI override, not a core patch) | 0 core |
| 0006 | prefix caching | (uses 0001 manager) | one hash-lookup hook in the 0004 alloc branch | ~6 |

Net core surface for the *entire* engine: `find_slot` (placement/alloc - where physical
cells are already chosen) + **one** line in `build_attn` + two accessors. Everything else
is new files or the LocalAI-side server loop.

## 0003 redesigned to the rule (replaces the 4-file-surgery plan)

The old `0003-gather-read-plan.md` edited `llama-kv-cache.{h,cpp}` + `llama-graph.{h,cpp}`
(including a field added to `llm_graph_input_attn_kv` and fill logic in its `set_input`).
The additive form removes the core-struct and core-`set_input` edits entirely:

**New file `src/paged-attn.{h,cpp}`** holds *all* logic:
- `class llm_graph_input_paged_gather : public llm_graph_input_i` - owns the `I32 [n_gather]`
  gather-index tensor and a `const llama_kv_cache_context * mctx`. Its `set_input()` fills
  the index with the sequence's used cells (`{ i in [0,n_kv) : !cells.is_empty(i) }`, the
  same set the `kq_mask` keeps), in the canonical order.
- `paged_attn::gather(ctx0, res, mctx, v_trans, &k, &v, &kq_mask)` - when paged is active,
  constructs that input via `res->add_input(...)`, and applies `ggml_get_rows` to `k`, `v`,
  and the transposed `kq_mask` by the shared index (mask: `transpose -> get_rows ->
  transpose`). When not active it returns immediately -> **stock path byte-identical**.

**Core hooks (the whole core diff for 0003):**
1. `src/llama-graph.cpp`, in `build_attn` right before `build_attn_mha` (~line 2357):
   ```cpp
   paged_attn::gather(ctx0, res, mctx_cur, v_trans, &k, &v, &kq_mask); // no-op unless LLAMA_KV_PAGED
   ```
   One line. No new field on `llm_graph_input_attn_kv`; the gather input is a *separate*
   registered input, so `llama-graph.h` is untouched.
2. `src/llama-kv-cache.{h,cpp}`: two thin accessors on `llama_kv_cache_context` so the new
   file can read the used-cell set without reaching into internals -
   `uint32_t get_n_gather() const;` and `void get_gather_idxs(int32_t * dst) const;`
   (delegate to `kv`/`sinfos[i_cur]`, mirroring the existing `get_n_kv` / `set_input_k_idxs`
   pattern). ~8 lines total, no signature changes to existing methods.
3. `src/CMakeLists.txt`: `+ paged-attn.cpp`.

First cut: gate to **flash-attn + single-stream** (`GGML_ASSERT` otherwise) - the V-transposed
(non-FA) and multi-stream gathers are a localized follow-up entirely inside `paged-attn.cpp`,
no new core touch. Gate 0 stays the same: `diff` of greedy `llama-simple` output, stock vs
`LLAMA_KV_PAGED=1`, must be identical (attention is permutation-invariant over the gathered
KV set; `n_gather < n_kv` proves compaction, not identity).

## Anti-drift practices (already in `README.md`, restated as policy)

- **Stacking patches, one concern each**, exported 1:1 from a dev branch via
  `git format-patch`. On a pin bump, rebase the branch; only the conflicting small patch
  needs a touch, and the failure names the exact step.
- **Default-off (`LLAMA_KV_PAGED`)** until each gate is green, so a partial series never
  changes stock behavior - and the hooks compile to a no-op branch when the env is unset.
- **Dev tree:** `git worktree add <dev> <LLAMA_VERSION>` off any checkout that has the pin
  (e.g. the existing llama.cpp clone), `git apply` the series, develop the next patch as one
  commit, re-export. (Set up and verified for this pin during this work.)

## Status / next step

- 0001, 0002: done, additive, verified token-identical.
- 0003: **redesigned to the additive form above** (this doc). Dev tree at the pin with
  0001+0002 applied is ready (`paged` branch). Remaining work is the focused
  implement-and-verify block for `paged-attn.{h,cpp}` + the one `build_attn` hook, driven to
  the token-identical Gate 0. That is a numerical-correctness task (mask/gather alignment,
  FA-first), not a structural one - the structure is settled here.
- 0004-0006: follow the budget above; 0005 lands in LocalAI's `grpc-server.cpp` (no core
  patch at all).
